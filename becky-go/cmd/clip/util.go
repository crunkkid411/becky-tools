package main

// util.go holds the small, pure, cross-platform helpers the becky-clip backend
// uses: time formatting, path basename (separator-agnostic for Windows paths),
// clamping, slugging, and the typed accessors for assistant.Action args. Kept
// pure so they are trivially unit-testable with no window, no engine, no OS deps.

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	"becky-go/internal/assistant"
	"becky-go/internal/edl"
	"becky-go/internal/pathx"
)

// mmss formats a position in seconds as a short "m:ss" label for the UI. Negative
// input clamps to zero.
func mmss(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	total := int(sec + 0.5)
	return fmt.Sprintf("%d:%02d", total/60, total%60)
}

// baseName returns the filename of a path, separator-agnostic so a Windows
// "C:\\case\\ring.mp4" basenames correctly even on Linux/CI (pathx, per
// CLAUDE.md §2 — never filepath.Base on a value that may be a Windows path).
func baseName(p string) string {
	if p == "" {
		return ""
	}
	return pathx.Base(p)
}

// clampNonNeg returns f if it is >= 0, else 0. Times into a source are never
// negative.
func clampNonNeg(f float64) float64 {
	if f < 0 {
		return 0
	}
	return f
}

// slugName lowercases and replaces runs of non-alphanumeric chars with a single
// '-' for a safe reel/file name. Empty input yields "becky".
func slugName(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "becky"
	}
	return out
}

// maxClipID returns the highest numeric suffix among clip IDs of the form "cN",
// so a loaded reel's next add continues the sequence without collision. IDs not
// matching "cN" are ignored.
func maxClipID(clips []edl.Clip) int {
	max := 0
	for _, c := range clips {
		if len(c.ID) >= 2 && c.ID[0] == 'c' {
			if n, err := strconv.Atoi(c.ID[1:]); err == nil && n > max {
				max = n
			}
		}
	}
	return max
}

// argStr returns the named arg from an action as a trimmed string, coercing
// numbers/bools to their text form. Missing → "".
func argStr(a assistant.Action, key string) string {
	if a.Args == nil {
		return ""
	}
	v, ok := a.Args[key]
	if !ok {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int:
		return strconv.Itoa(t)
	case bool:
		return strconv.FormatBool(t)
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", v))
	}
}

// tcOrSeconds parses a clip time that may be a bare seconds value ("12.4"), an
// SRT/SMPTE timecode ("00:00:12,400" / "00:00:12.400" / "1:02"), or empty (→ 0).
// This lets the assistant's add_clip actions (which emit SRT timecodes) and the
// UI (which sends seconds) share one entry point.
func tcOrSeconds(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return clampNonNeg(f)
	}
	norm := strings.Replace(s, ",", ".", 1)
	parts := strings.Split(norm, ":")
	var h, m, sec float64
	switch len(parts) {
	case 3:
		h = atofSafe(parts[0])
		m = atofSafe(parts[1])
		sec = atofSafe(parts[2])
	case 2:
		m = atofSafe(parts[0])
		sec = atofSafe(parts[1])
	case 1:
		sec = atofSafe(parts[0])
	default:
		return 0
	}
	return clampNonNeg(h*3600 + m*60 + sec)
}

// atoiSafe parses an int, returning 0 on failure.
func atoiSafe(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// atofSafe parses a float, returning 0 on failure.
func atofSafe(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}

// fileExists reports whether path is an existing, regular (non-directory) file.
// Used to resolve sidecar/binary paths without a panic on a missing entry.
func fileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

// isWindows reports whether the running binary is on Windows (so a sibling
// executable's name gets the .exe suffix).
func isWindows() bool {
	return runtime.GOOS == "windows"
}

// truncateText clips s to at most max runes, appending "…" when it had to cut —
// used for the H-5 event stream's one-line activity text, so a long chat
// utterance or error can't blow up the right panel's status line. max<=0
// disables truncation (returns s unchanged).
func truncateText(s string, max int) string {
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// truthy reports whether a string value means "on" (true/1/yes/on). Anything
// else (incl. empty) is false — so an unspecified overlay value reads as off.
func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "on", "y":
		return true
	default:
		return false
	}
}
