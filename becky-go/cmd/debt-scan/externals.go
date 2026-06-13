// externals.go — best-effort enrichment from external linters when present.
//
// The pure-Go core already produces findings for every category without any
// external tool. This layer ADDS findings from real linters when their binary
// is on PATH, and records a "skipped: <tool> not installed" note when it isn't.
// An external tool failing or being absent never fails the scan.
//
// Currently wired:
//   - Go:     `go vet` (ships with the toolchain) -> extra findings.
//   - staticcheck, vulture, radon, clippy, eslint, tsc: presence is probed and
//     noted; running each is left to CI (they need project-specific setup).
//
// We keep invocations conservative (read-only, scoped to the scan root) and cap
// what we parse so a noisy linter can't blow up the report.
package main

import (
	"os/exec"
	"strings"

	"becky-go/internal/beckyio"
)

// externalTool is a linter we can opportunistically run or note.
type externalTool struct {
	name   string // binary name probed on PATH
	lang   string // language it applies to (for the skip note)
	reason string // human label
}

// candidateTools are probed for presence and noted regardless of whether they
// run, so the report always states what enrichment was/wasn't available.
var candidateTools = []externalTool{
	{name: "go", lang: langGo, reason: "go vet static analysis"},
	{name: "staticcheck", lang: langGo, reason: "staticcheck extended checks"},
	{name: "vulture", lang: langPython, reason: "vulture dead-code"},
	{name: "radon", lang: langPython, reason: "radon complexity"},
	{name: "cargo", lang: langRust, reason: "cargo clippy"},
	{name: "eslint", lang: langTS, reason: "eslint"},
	{name: "tsc", lang: langTS, reason: "tsc --noUnusedLocals"},
}

// runExternals enriches findings with external-linter output for the languages
// actually present, and returns notes describing what was used or skipped. It is
// strictly additive and never errors out the scan.
func runExternals(root string, langs []string, want map[string]bool, verbose bool) ([]Finding, map[string]string) {
	notes := map[string]string{}
	var findings []Finding
	langSet := stringSet(langs)

	for _, t := range candidateTools {
		if !langSet[t.lang] {
			continue // language not in this repo; don't even mention it
		}
		if _, err := exec.LookPath(t.name); err != nil {
			notes["external:"+t.name] = "skipped: " + t.name + " not installed (" + t.reason + ")"
			continue
		}
		// Tool present. Only `go vet` is invoked: it ships with the toolchain, is
		// read-only, and maps cleanly to file:line. The rest are noted as
		// available; wiring each runner risks heavy project-specific setup.
		if t.name == "go" && want[catDeadCode] {
			vetFindings, note := runGoVet(root)
			findings = append(findings, vetFindings...)
			notes["external:go-vet"] = note
			beckyio.Logf(verbose, "%s", note)
		} else {
			notes["external:"+t.name] = "available: " + t.reason + " (not auto-run; enable in CI for deeper checks)"
		}
	}
	return findings, notes
}

// runGoVet runs `go vet ./...` rooted at the scan path and parses its
// "file:line:col: message" diagnostics into findings. A vet failure (e.g. a
// package that doesn't compile) is captured as a note, not a crash.
func runGoVet(root string) ([]Finding, string) {
	cmd := exec.Command("go", "vet", "./...")
	cmd.Dir = root
	var out, errBuf strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	_ = cmd.Run() // vet exits non-zero when it finds issues; that's expected
	combined := out.String() + "\n" + errBuf.String()
	findings := parseVetOutput(combined)
	if len(findings) == 0 {
		return nil, "ran: go vet ./... (no diagnostics, or no Go module at root)"
	}
	return findings, "ran: go vet ./... (" + itoa(len(findings)) + " diagnostics)"
}

// parseVetOutput turns vet diagnostic lines into findings. Lines look like:
//
//	path\to\file.go:42:7: something is wrong
//
// We accept both / and \ separators and a colon-delimited line[:col].
func parseVetOutput(out string) []Finding {
	var findings []Finding
	for _, ln := range splitLines(out) {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") || strings.HasPrefix(ln, "go:") {
			continue
		}
		file, line, msg, ok := parseDiagLine(ln)
		if !ok || langOf(file) != langGo {
			continue
		}
		findings = append(findings, Finding{
			Category: catDeadCode, // vet's reachability/unused diagnostics map here
			File:     file,
			Line:     line,
			Severity: sevMedium,
			Language: langGo,
			Source:   "go vet",
			Message:  msg,
		})
	}
	return findings
}

// parseDiagLine parses "file:line[:col]: message", tolerating Windows drive
// letters (C:\...).
func parseDiagLine(ln string) (file string, line int, msg string, ok bool) {
	rest := ln
	prefix := ""
	if len(rest) > 2 && rest[1] == ':' && isLetter(rest[0]) {
		prefix = rest[:2] // preserve "X:" drive so it isn't taken as separator
		rest = rest[2:]
	}
	parts := strings.SplitN(rest, ":", 3)
	if len(parts) < 3 {
		return "", 0, "", false
	}
	file = prefix + parts[0]
	ln2 := atoi(parts[1])
	if ln2 == 0 {
		return "", 0, "", false
	}
	tail := parts[2]
	if i := strings.Index(tail, ":"); i >= 0 && atoi(strings.TrimSpace(tail[:i])) > 0 {
		tail = tail[i+1:] // drop a leading "col:"
	}
	return file, ln2, strings.TrimSpace(tail), true
}

func isLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
