package main

// acquire.go puts becky-captions in front of local ASR, so becky-subtitle
// follows the same caption-acquisition rule the rest of the suite does
// (CLAUDE.md: prefer a TRUSTWORTHY official transcript, fall back to Parakeet):
//
//  1. Ask becky-captions whether a complete official transcript already sits
//     beside the source, or can be fetched for it. It also catches the case that
//     matters forensically — an official transcript that is SHORT because the
//     stream was YouTube-edited — and refuses to trust it.
//  2. use_official  -> no ASR run at all. The official .srt is the transcript.
//  3. local_needed / becky-captions missing / becky-captions errored -> run
//     becky-transcribe exactly as before.
//
// becky-captions is OPTIONAL, same as in becky-clip: if the binary isn't there
// we go straight to ASR rather than failing. Nothing here writes the source
// media, and nothing overwrites an official .srt.
//
// The honest limit, stated where the code does it: an official .srt is CUE
// level. becky's caption pacing (internal/subs) is word-timed — the 22-char cap
// and the pause breaking both need per-word times, and inventing them would be
// fabricating timing. So when the transcript we end up with is cue-level, each
// official cue becomes one caption, still snapped to the edit's cuts, and the
// caller is warned that these are the official lines rather than becky pacing.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"becky-go/internal/captions"
	"becky-go/internal/proc"
	"becky-go/internal/quotes"
	"becky-go/internal/subs"
)

// captionsTimeout bounds one becky-captions exec: a probe plus at most one
// yt-dlp subtitle fetch (no media download). BECKY_CAPTIONS_TIMEOUT overrides.
const captionsTimeout = 10 * time.Minute

// acquisition is what the caption-acquisition step decided for one source.
type acquisition struct {
	// Words are the transcript's word timings when we have them (local ASR).
	Words []subs.Word
	// CueLevel is set when Words came from an official .srt rather than ASR:
	// each entry spans a whole official cue, so it cannot be re-paced.
	CueLevel bool
	// OfficialSRT is the official transcript used, when one was.
	OfficialSRT string
	Warnings    []string
}

// askCaptions runs becky-captions for one source. ok is false when the tool
// isn't installed or the call failed — the caller then goes straight to ASR,
// which is the pre-existing behaviour.
func askCaptions(source string, verbose bool) (captions.Decision, bool) {
	bin, found := resolveCaptionsBin()
	if !found {
		return captions.Decision{}, false
	}
	to := captionsTimeout
	if d := strings.TrimSpace(os.Getenv("BECKY_CAPTIONS_TIMEOUT")); d != "" {
		if parsed, err := time.ParseDuration(d); err == nil && parsed > 0 {
			to = parsed
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), to)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, source, "--json")
	proc.NoWindow(cmd)
	if verbose {
		cmd.Stderr = os.Stderr
	}
	out, err := cmd.Output()
	if err != nil {
		return captions.Decision{}, false
	}
	var d captions.Decision
	if err := json.Unmarshal(out, &d); err != nil {
		return captions.Decision{}, false
	}
	return d, true
}

// resolveCaptionsBin finds becky-captions: BECKY_CAPTIONS, then beside this
// executable (how build-all-tools.bat ships the suite), then PATH. Absence is
// reported, never fatal.
func resolveCaptionsBin() (string, bool) {
	if p := strings.TrimSpace(os.Getenv("BECKY_CAPTIONS")); p != "" {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, true
		}
		return "", false
	}
	if exe, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(exe), captionsExeName())
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			return cand, true
		}
	}
	if p, err := exec.LookPath("becky-captions"); err == nil {
		return p, true
	}
	return "", false
}

func captionsExeName() string {
	if os.PathSeparator == '\\' {
		return "becky-captions.exe"
	}
	return "becky-captions"
}

// wordsFromOfficialSRT turns an official .srt into the word list the caption
// path consumes, with ONE entry per cue. The times are the cue's own — nothing
// is interpolated, so no timing is invented. The result cannot be re-paced,
// which is why the caller marks it CueLevel and warns.
func wordsFromOfficialSRT(path string) ([]subs.Word, error) {
	cues, err := quotes.ParseSRTFile(path)
	if err != nil {
		return nil, err
	}
	words := make([]subs.Word, 0, len(cues))
	for _, c := range cues {
		text := strings.TrimSpace(strings.ReplaceAll(c.Text, "\n", " "))
		if text == "" {
			continue
		}
		words = append(words, subs.Word{Word: text, Start: c.Start, End: c.End})
	}
	if len(words) == 0 {
		return nil, fmt.Errorf("official transcript %s has no cues", filepath.Base(path))
	}
	return words, nil
}
