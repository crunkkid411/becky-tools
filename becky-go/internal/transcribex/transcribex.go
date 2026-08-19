// Package transcribex gets a transcript for a video, MAKING one if it does not
// exist yet.
//
// This exists because of becky's central principle (CLAUDE.md §2): the caller
// makes ONE dumb call and becky does the thinking inside it. A tool that stops to
// say "no transcript sidecar — run becky-transcribe first" has pushed its own
// orchestration onto a human, and Jordan is not a developer: for him that is a
// dead end, not an instruction. becky-tools is *where transcripts come from*, so
// needing one is never a reason to refuse.
//
// The transcript is written BESIDE the video as a .srt, which means
// sidecar.FindSubtitle picks it up forever after — transcribing a long stream
// happens once, not once per run.
package transcribex

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"becky-go/internal/sidecar"
	"becky-go/internal/subs"
)

// Logf is an optional progress sink (nil is fine). Transcription of a long stream
// takes minutes, so a caller with a console should say what is happening rather
// than appear hung.
type Logf func(format string, a ...any)

// Bin finds the becky-transcribe executable, in order:
//
//	(a) the BECKY_TRANSCRIBE env var (an explicit path),
//	(b) next to the running executable (how build-all-tools.bat ships them),
//	(c) on PATH.
//
// Same order as cmd/clip's resolver, which this generalises; cmd/clip still has
// its own copy and can move onto this one whenever it is next touched.
func Bin() (string, error) {
	if p := strings.TrimSpace(os.Getenv("BECKY_TRANSCRIBE")); p != "" {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if fileExists(p) {
			return p, nil
		}
		return "", fmt.Errorf("becky-transcribe not found at BECKY_TRANSCRIBE=%q", p)
	}
	if exe, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(exe), exeName())
		if fileExists(cand) {
			return cand, nil
		}
	}
	if p, err := exec.LookPath("becky-transcribe"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("becky-transcribe not found — build it with build-all-tools.bat " +
		"(or set BECKY_TRANSCRIBE to its path)")
}

// IsMedia reports whether path looks like a video/audio file rather than a
// transcript, so callers know when transcription is even a possibility.
func IsMedia(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4", ".mkv", ".mov", ".avi", ".webm", ".m4v", ".ts", ".flv", ".wmv",
		".mp3", ".wav", ".m4a", ".aac", ".flac", ".opus", ".ogg":
		return true
	}
	return false
}

// EnsureSRT returns a transcript path for video, transcribing it if none exists.
//
// It returns (path, madeIt, error). madeIt is true when transcription actually
// ran, so a caller can report the minutes it spent rather than looking hung.
func EnsureSRT(video string, logf Logf) (string, bool, error) {
	if s := sidecar.FindSubtitle(video); s != "" {
		return s, false, nil
	}
	if !IsMedia(video) {
		return "", false, fmt.Errorf("%s is not a media file and has no transcript beside it",
			filepath.Base(video))
	}
	bin, err := Bin()
	if err != nil {
		return "", false, err
	}

	// Beside the video, so it is found automatically from now on.
	out := strings.TrimSuffix(video, filepath.Ext(video)) + ".srt"
	if logf != nil {
		logf("no transcript beside %s — transcribing it now (this is the slow part; it only happens once)",
			filepath.Base(video))
	}

	cmd := exec.Command(bin, "--format", "srt", "--output", out, video)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", false, fmt.Errorf("could not transcribe %s: %v\n%s",
			filepath.Base(video), err, tail(stderr.String()))
	}
	st, serr := os.Stat(out)
	if serr != nil || st.Size() == 0 {
		return "", false, fmt.Errorf("transcribing %s produced no transcript", filepath.Base(video))
	}
	if logf != nil {
		logf("transcript written: %s", filepath.Base(out))
	}
	return out, true, nil
}

func exeName() string {
	if runtime.GOOS == "windows" {
		return "becky-transcribe.exe"
	}
	return "becky-transcribe"
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 600 {
		return "…" + s[len(s)-600:]
	}
	return s
}

// wordFile mirrors becky-transcribe's --format json sidecar. Only the word
// timings matter here.
type wordFile struct {
	Words []subs.Word `json:"words"`
}

// FindWords returns the word-level transcript sidecar for a source, or "".
// Same search order cmd/subtitle uses (which still carries its own copy and can
// move onto this one whenever it is next touched, exactly as Bin() notes for
// cmd/clip).
func FindWords(source string) string {
	dir := filepath.Dir(source)
	stem := strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	for _, cand := range []string{
		filepath.Join(dir, stem+".transcript.json"),
		source + ".transcript.json",
		filepath.Join(dir, "transcripts", stem+".json"),
	} {
		if fileExists(cand) {
			return cand
		}
	}
	return ""
}

// EnsureWords returns WORD-LEVEL timings for video, transcribing if there are
// none.
//
// Captions need word times and a .srt carries only cue times, so EnsureSRT is
// not enough to burn captions into a clip. The contract is otherwise identical:
// it MAKES what is missing rather than telling the caller to go and run another
// tool first, because for Jordan that is a dead end and not an instruction.
//
// Returns (words, madeIt, error).
func EnsureWords(video string, logf Logf) ([]subs.Word, bool, error) {
	if p := FindWords(video); p != "" {
		if w, err := loadWords(p); err == nil {
			return w, false, nil
		} else if logf != nil {
			// A sidecar without word timings is not a reason to refuse - it is a
			// reason to make one that has them.
			logf("%s: %v — re-transcribing for word timings", filepath.Base(p), err)
		}
	}
	if !IsMedia(video) {
		return nil, false, fmt.Errorf("%s is not a media file and has no word-level transcript beside it",
			filepath.Base(video))
	}
	bin, err := Bin()
	if err != nil {
		return nil, false, err
	}

	out := strings.TrimSuffix(video, filepath.Ext(video)) + ".transcript.json"
	if logf != nil {
		logf("no word-level transcript beside %s — transcribing it now (once, then it is cached)",
			filepath.Base(video))
	}
	cmd := exec.Command(bin, "--format", "json", "--output", out, video)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, false, fmt.Errorf("could not transcribe %s: %v\n%s",
			filepath.Base(video), err, tail(stderr.String()))
	}
	w, err := loadWords(out)
	if err != nil {
		return nil, false, err
	}
	if logf != nil {
		logf("word-level transcript written: %s (%d words)", filepath.Base(out), len(w))
	}
	return w, true, nil
}

func loadWords(path string) ([]subs.Word, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read transcript %s: %w", path, err)
	}
	var t wordFile
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, fmt.Errorf("parse transcript %s: %w", filepath.Base(path), err)
	}
	if len(t.Words) == 0 {
		return nil, fmt.Errorf("no word-level timings in %s", filepath.Base(path))
	}
	return t.Words, nil
}
