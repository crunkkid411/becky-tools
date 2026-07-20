package subs

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Jordan edits the caption prompt HERE, in a plain text file — not in Go source.
//
// His words, 2026-07-20: "LET ME CHANGE THE LLM PROMPT TOMORROW WHEN I LOOK AT
// THE RESULTS". He is not a developer. Leaving the prompt as a Go constant means
// the only way for him to tune the thing he cares most about — how his captions
// read — is to edit source and recompile, which he will not and should not do.
//
// So: if caption-prompt.txt exists, it IS the prompt. No rebuild, no CLI flag,
// no restart of anything but the next caption run. If it is missing or empty,
// the built-in prompt is used and nothing changes.
//
// The file must contain exactly one %d — the character limit is substituted into
// it. A file without one is REJECTED rather than used, because Sprintf would
// otherwise silently emit "%!d(MISSING)" into the prompt and quietly degrade
// every caption he generates.
const promptFileName = "caption-prompt.txt"

var (
	promptOnce sync.Once
	promptText string
	promptFrom string // where it came from, for the tool to report
)

// promptSearchPath is where caption-prompt.txt is looked for, nearest first:
// beside the binary, then the repo's usual home. BECKY_CAPTION_PROMPT overrides
// with an explicit path.
func promptSearchPath() []string {
	var p []string
	if v := strings.TrimSpace(os.Getenv("BECKY_CAPTION_PROMPT")); v != "" {
		p = append(p, v)
	}
	if exe, err := os.Executable(); err == nil {
		p = append(p, filepath.Join(filepath.Dir(exe), promptFileName))
	}
	p = append(p,
		filepath.Join(`X:\AI-2\becky-tools`, promptFileName),
		promptFileName, // cwd
	)
	return p
}

// ReviewPrompt returns the caption-grouping prompt and where it came from.
func ReviewPrompt() (text, source string) {
	promptOnce.Do(func() {
		promptText, promptFrom = reviewSystem, "built-in"
		for _, path := range promptSearchPath() {
			b, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			s := stripNotes(string(b))
			if s == "" {
				continue
			}
			// Must carry the character-limit placeholder, or Sprintf would inject
			// "%!d(MISSING)" and silently poison every caption run.
			if strings.Count(s, "%d") != 1 {
				promptFrom = "built-in (" + filepath.Base(path) + " ignored: it must contain exactly one %d for the character limit)"
				return
			}
			promptText, promptFrom = s, path
			return
		}
	})
	return promptText, promptFrom
}

// stripNotes removes the leading "#" note lines the shipped file explains itself
// with, so Jordan can annotate his own prompt freely without any of it reaching
// the model.
func stripNotes(raw string) string {
	var keep []string
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		keep = append(keep, line)
	}
	return strings.TrimSpace(strings.Join(keep, "\n"))
}
