// learn.go — the natural-language "teach the system a person from a clip" op. It is
// the friendly front for becky-enroll's single-clip mode, so a user never types
// "enroll": `becky learn "Shelby" clip.mp4` or, in plain language with no keyword at
// all, `becky "this is Shelby" clip.mp4`.
//
// It shells becky-enroll --clip <video> --name "<name>" (single-clip teach mode),
// which extracts the dominant speaker's voice + the clearest face from the clip and
// APPENDS them to the KB. The op prints a plain-English headline + the enroll JSON.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// teachPhrasePatterns recognize a plain-language "this is <name>" instruction as the
// FIRST argument (so `becky "this is Shelby" clip.mp4` works with no keyword). Each
// captures the name. Kept deliberately small and explicit — this is a deterministic
// front-door, not an NLU engine.
var teachPhrasePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^\s*(?:this|that)\s+is\s+(?:a\s+(?:video|clip|recording)\s+of\s+)?(.+?)\s*$`),
	regexp.MustCompile(`(?i)^\s*(?:this|that)'?s\s+(.+?)\s*$`),
	regexp.MustCompile(`(?i)^\s*(?:learn|remember|meet)\s+(.+?)\s*$`),
	regexp.MustCompile(`(?i)^\s*(.+?)\s+is\s+(?:the\s+)?(?:person|speaker|one)\s+(?:speaking|talking|here)\s*$`),
}

// parseTeachPhrase extracts a person name from a plain-language teaching phrase. It
// returns (name, true) only when the phrase clearly names someone — a bare op word or
// a path is left for the normal dispatcher. The extracted name must look like a name
// (letters), not a filename, so `becky test.mp4` is not misread as a teach.
func parseTeachPhrase(arg string) (string, bool) {
	a := strings.TrimSpace(arg)
	if a == "" || looksLikePath(a) {
		return "", false
	}
	for _, re := range teachPhrasePatterns {
		if m := re.FindStringSubmatch(a); len(m) == 2 {
			name := cleanName(m[1])
			if isPlausibleName(name) {
				return name, true
			}
		}
	}
	return "", false
}

// cleanName trims trailing punctuation/quotes and collapses whitespace in a captured
// name ("Shelby." / "\"Shelby\"" -> "Shelby").
func cleanName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `."'!,`)
	return strings.Join(strings.Fields(s), " ")
}

// isPlausibleName rejects captures that are clearly not a person (a path, empty, or no
// letters) so the natural-language route never hijacks a real command.
func isPlausibleName(s string) bool {
	if s == "" || looksLikePath(s) {
		return false
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return true
		}
	}
	return false
}

// looksLikePath reports whether a string is obviously a file path / has a media
// extension (so it is treated as the clip argument, not a name).
func looksLikePath(s string) bool {
	if strings.ContainsAny(s, `/\`) {
		return true
	}
	switch strings.ToLower(filepath.Ext(s)) {
	case ".mp4", ".mov", ".mkv", ".avi", ".webm", ".m4v", ".mpg", ".mpeg",
		".jpg", ".jpeg", ".png":
		return true
	}
	return false
}

// runLearn teaches the KB one person from one clip. args[0] is the name; the first
// positional that looks like a media path is the clip.
func runLearn(args []string) error {
	cf, rest := extractCommon(args)
	kb, rest := flagValue(rest, "kb", "becky-kb")
	device, rest := flagValue(rest, "device", "")

	name, clip := splitNameAndClip(rest)
	if name == "" || clip == "" {
		return fmt.Errorf("usage: becky learn \"<name>\" <clip> [--kb <dir>]  (or: becky \"this is <name>\" <clip>)")
	}
	if !fileExists(clip) {
		return fmt.Errorf("clip not found: %s", clip)
	}

	toolArgs := []string{"--clip", clip, "--name", name, "--kb", kb}
	if device != "" {
		toolArgs = append(toolArgs, "--device", device)
	}
	if cf.bin != "" {
		toolArgs = append(toolArgs, "--bin", cf.bin)
	}
	if cf.verbose {
		toolArgs = append(toolArgs, "--verbose")
	}

	headline(cf, "Learning %q from %s ...", name, filepath.Base(clip))
	out, err := runTool(cf, "enroll", toolArgs)
	if err != nil {
		return err
	}

	var res struct {
		Name     string `json:"name"`
		Enrolled struct {
			Voice bool `json:"voice"`
			Face  bool `json:"face"`
		} `json:"enrolled"`
		VoiceClip *string           `json:"voice_clip"`
		FaceImage *string           `json:"face_image"`
		SpeakerID string            `json:"speaker_id"`
		Skip      map[string]string `json:"skip_reason"`
		Notes     []string          `json:"notes"`
	}
	_ = json.Unmarshal(out, &res)

	switch {
	case res.Enrolled.Voice && res.Enrolled.Face:
		headline(cf, "Learned %s: voice + face added to the KB (%s).", res.Name, absOr(kb))
	case res.Enrolled.Voice:
		headline(cf, "Learned %s: voice added (face skipped: %s).", res.Name, res.Skip["face"])
	case res.Enrolled.Face:
		headline(cf, "Learned %s: face added (voice skipped: %s).", res.Name, res.Skip["voice"])
	default:
		headline(cf, "Could NOT learn %s — nothing clean to enroll (voice: %s; face: %s).",
			res.Name, res.Skip["voice"], res.Skip["face"])
	}
	for _, n := range res.Notes {
		headline(cf, "  note: %s", n)
	}
	os.Stdout.Write(out)
	return nil
}

// splitNameAndClip separates the name (first non-path positional) from the clip (the
// first positional that looks like a media path), order-independent.
func splitNameAndClip(args []string) (name, clip string) {
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		if clip == "" && looksLikePath(a) {
			clip = a
			continue
		}
		if name == "" {
			name = cleanName(a)
		}
	}
	return name, clip
}
