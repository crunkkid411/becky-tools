package main

// prompts.go - what the models are told lives in text files Jordan can edit
// (Jordan, 2026-10-20: "so that I can edit the prompts immediately"), one per
// question, in the step's folder of workflows\video-edit. The first run writes
// each file with today's built-in words; after that the file wins. A file that is
// missing or empty falls back to the built-in words. Words in {CAPS} are filled in
// by the tool (the transcript, the times).
//
// An edited file makes its step ask the model again on the next run, and only that
// step: every saved answer carries a fingerprint of the words it was asked with
// (stampPrompts). Everything else is reused.

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Step folders (workflows\video-edit) that hold prompt files.
const (
	dirQwen    = `06_Content-Decision\A_Qwen-version (the one you tested first)`
	dirYouTube = `06_Content-Decision\B_YouTube-version (newest)`
	dirMoments = `09_Visual-Moments (livestreams only today)`
	dirBreath  = `10_Breath-Check`
	dirPublish = `11_Publish-Check`
	dirUpload  = `23_Upload-Text`
)

// promptRoot is workflows\video-edit, or "" (built-in words only: tests, other PCs).
var promptRoot = func() string {
	if testing.Testing() {
		return ""
	}
	for _, d := range []string{os.Getenv("BECKY_VIDEO_EDIT"), `X:\AI-2\becky-tools\workflows\video-edit`} {
		if st, err := os.Stat(d); d != "" && err == nil && st.IsDir() {
			return d
		}
	}
	return ""
}()

// P is the prompt in <step folder>\PROMPT - <name>.txt, written with def when missing.
func P(dir, name, def string) string {
	if promptRoot == "" {
		return def
	}
	path := filepath.Join(promptRoot, filepath.FromSlash(strings.ReplaceAll(dir, `\`, "/")), "PROMPT - "+name+".txt")
	b, err := os.ReadFile(path)
	if err != nil {
		_ = os.WriteFile(path, []byte(def), 0o644)
		return def
	}
	if s := strings.ReplaceAll(string(b), "\r\n", "\n"); strings.TrimSpace(s) != "" {
		if s != def {
			edited[s] = true
		}
		return s
	}
	return def
}

// edited holds the prompts whose file differs from the built-in words.
var edited = map[string]bool{}

// fill puts the tool's data into a prompt's {CAPS} words.
func fill(tmpl string, kv ...string) string {
	return strings.NewReplacer(kv...).Replace(tmpl)
}

// stampPrompts deletes the saved answers in cache when the prompts they were asked
// with have changed since, so that step asks again. Answers saved before there was
// a fingerprint were asked with the built-in words: kept unless a file was edited.
func stampPrompts(cache string, prompts ...string) {
	h := sha256.New()
	for _, p := range prompts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	sum := hex.EncodeToString(h.Sum(nil))
	stamp := cache + ".prompts"
	old, err := os.ReadFile(stamp)
	changed := err == nil && strings.TrimSpace(string(old)) != sum
	for _, p := range prompts {
		changed = changed || (err != nil && edited[p])
	}
	if changed {
		_ = os.Remove(cache)
	}
	_ = os.WriteFile(stamp, []byte(sum), 0o644)
}
