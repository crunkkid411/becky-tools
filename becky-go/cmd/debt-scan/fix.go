// fix.go — the deliberately conservative autofix engine.
//
// Philosophy: prefer reporting over rewriting. We only ever apply fixes that are
// provably safe and reversible by a formatter:
//
//   - Go files with fixable findings (e.g. an unused import) are handed to
//     `gofmt -w`, which Go itself guarantees is semantics-preserving. gofmt
//     tidies formatting and import grouping; it will NOT silently delete an
//     unused named import, so we only claim the formatting fix and never drop
//     code on the user's behalf.
//
// --fix-dry-run lists what WOULD run and changes nothing. --fix runs gofmt and
// records what it touched. Anything riskier (deleting code, editing TODOs) is
// reported as a finding but never auto-applied.
package main

import (
	"os/exec"
	"sort"
	"strings"
)

// planFixes returns the safe fixes implied by the findings as human-readable
// one-liners (used for both dry-run output and the applied log). It also returns
// the set of Go files eligible for `gofmt -w`.
func planFixes(findings []Finding, files []sourceFile) (plan []string, gofmtTargets []string) {
	relToPath := map[string]string{}
	for _, f := range files {
		relToPath[f.rel] = f.path
	}
	goFixFiles := map[string]bool{}
	for _, f := range findings {
		if f.Language == langGo && f.Fixable {
			if p, ok := relToPath[f.File]; ok {
				goFixFiles[p] = true
			}
		}
	}
	for p := range goFixFiles {
		gofmtTargets = append(gofmtTargets, p)
	}
	sort.Strings(gofmtTargets)
	for _, p := range gofmtTargets {
		plan = append(plan, "gofmt -w "+p+" (format + tidy imports)")
	}
	if len(plan) == 0 {
		plan = append(plan, "no safe autofixes available (findings require manual review)")
	}
	return plan, gofmtTargets
}

// applyFixes runs gofmt -w on the eligible Go files and returns a log of what
// was actually changed. gofmt missing or failing is reported in the log, not
// fatal.
func applyFixes(gofmtTargets []string) []string {
	if len(gofmtTargets) == 0 {
		return []string{"no safe autofixes to apply"}
	}
	gofmt, err := exec.LookPath("gofmt")
	if err != nil {
		return []string{"skipped: gofmt not found on PATH"}
	}
	var log []string
	for _, target := range gofmtTargets {
		// `gofmt -l -w` lists files it changed; capture that to report precisely.
		cmd := exec.Command(gofmt, "-l", "-w", target)
		var out, errBuf strings.Builder
		cmd.Stdout = &out
		cmd.Stderr = &errBuf
		if rerr := cmd.Run(); rerr != nil {
			log = append(log, "gofmt failed on "+target+": "+strings.TrimSpace(errBuf.String()))
			continue
		}
		if strings.TrimSpace(out.String()) != "" {
			log = append(log, "gofmt -w reformatted "+target)
		} else {
			log = append(log, "gofmt -w "+target+" (already formatted, no change)")
		}
	}
	return log
}
