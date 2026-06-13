// util.go — small, package-local helpers shared across the factory stages:
// slug derivation, string truncation, file probes, JSON emission, and a generic
// command runner. Kept in one place so the stage files stay focused.
//
// Fact-Forcing-Gate self-certification:
//  1. Callers: every other cmd/new-tool file (stages.go, models.go, claude_stages.go,
//     eval_stage.go, integrate.go, orchestrator.go, main.go) uses these.
//  2. No-dup: package-local helpers; internal/config + cmd/becky have their own
//     fileExists but in different packages — not importable here without cycles.
//  3. Data shape: string/path utilities + emitState (PrintJSON the run-state). No
//     data files of their own.
//  4. Verbatim instruction: "BUILD: a deterministic state-machine orchestrator
//     `cmd/new-tool/` over a resumable `state.json`".
package main

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"

	"becky-go/internal/beckyio"
)

// binName returns the binary file name for a slug (e.g. becky-redact -> becky-redact.exe).
func binName(slug string) string {
	if runtime.GOOS == "windows" {
		return slug + ".exe"
	}
	return slug
}

// cmdDirName returns the cmd/ sub-directory name for a slug, following the house
// convention: the directory is the BARE name WITHOUT the "becky-" prefix (e.g. slug
// "becky-redact" -> cmd/redact), while the binary stays becky-redact.exe. Every place
// that builds a cmd path uses this so S5/S6/S9/S10 agree and build-all-tools.bat's
// `set TOOLS=<bare names>` line works.
func cmdDirName(slug string) string {
	return strings.TrimPrefix(slug, "becky-")
}

// truncate caps s to n bytes with an ellipsis.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n < 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

// tail2 returns the trailing n bytes of s (trimmed), for compact error context.
func tail2(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return "..." + s[len(s)-n:]
	}
	return s
}

// oneLine collapses whitespace/newlines to a single line for a PROGRESS row.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

var slugRe = regexp.MustCompile(`[^a-z0-9-]+`)

// sanitizeSlug normalizes a model/cheap-suggested slug to becky-<kebab>.
func sanitizeSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return ""
	}
	if !strings.HasPrefix(s, "becky-") {
		s = "becky-" + s
	}
	return s
}

// deriveSlug builds a becky-<kebab> slug deterministically from the pain text (the
// first few meaningful words).
func deriveSlug(pain string) string {
	words := strings.Fields(strings.ToLower(pain))
	stop := map[string]bool{"a": true, "an": true, "the": true, "i": true, "need": true,
		"want": true, "tool": true, "that": true, "to": true, "for": true, "of": true, "and": true}
	var kept []string
	for _, w := range words {
		w = slugRe.ReplaceAllString(w, "")
		if w == "" || stop[w] {
			continue
		}
		kept = append(kept, w)
		if len(kept) >= 3 {
			break
		}
	}
	if len(kept) == 0 {
		return "becky-tool"
	}
	return "becky-" + strings.Join(kept, "-")
}

// guessInputKind makes a deterministic best-guess of the input modality from the text.
func guessInputKind(pain string) string {
	p := strings.ToLower(pain)
	switch {
	case strings.Contains(p, "video") || strings.Contains(p, "clip") || strings.Contains(p, "mp4") || strings.Contains(p, "frame"):
		return "video"
	case strings.Contains(p, "audio") || strings.Contains(p, "voice") || strings.Contains(p, "speech"):
		return "audio"
	case strings.Contains(p, "image") || strings.Contains(p, "photo") || strings.Contains(p, "picture"):
		return "image"
	case strings.Contains(p, "url") || strings.Contains(p, "web") || strings.Contains(p, "http"):
		return "url"
	case strings.Contains(p, "json") || strings.Contains(p, "transcript"):
		return "json"
	default:
		return "text"
	}
}

// clamp01 clamps a float to [0,1].
func clamp01(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

// fileExists reports whether path is an existing regular file.
func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// dirExists reports whether path is an existing directory.
func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// containsWord reports whether s contains word as a whitespace-delimited token.
func containsWord(s, word string) bool {
	for _, f := range strings.Fields(s) {
		if f == word {
			return true
		}
	}
	return false
}

// readFileBest reads up to capBytes bytes of a file; "" on error.
func readFileBest(path string, capBytes int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return truncate(string(data), capBytes)
}

// goRunCmd runs a single command (e.g. a .bat) from the build root, capturing output.
func (o *orchestrator) goRunCmd(ctx context.Context, command string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = o.buildRoot
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// emitState writes the run-state JSON to stdout (the machine-readable contract).
func emitState(s *State) {
	beckyio.PrintJSON(s)
}
