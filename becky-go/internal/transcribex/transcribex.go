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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"becky-go/internal/sidecar"
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
