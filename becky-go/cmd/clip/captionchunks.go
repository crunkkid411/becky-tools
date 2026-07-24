package main

// captionchunks.go exposes the caption_chunks verb: the DETERMINISTIC, pace-based
// caption chunker (internal/subs.Build - the exact same rules becky-subtitle uses:
// break on speech pauses first, 22 chars only as a last-resort cap, contiguous/no-gap,
// phrases kept whole) applied to ONE source's word-level transcript. The native
// timeline's derived caption lane calls this instead of the raw Parakeet transcript
// so an ad-hoc clip (one the user just dropped on the timeline, with no reel sidecar
// yet) still shows proper TikTok-style captions - not one long transcript line with
// speech gaps. This CHANGES NO RULES; it only routes the derived lane through the
// chunker that already exists.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"becky-go/internal/subs"
)

// CaptionWord is one word of a cue, in the SOURCE's own seconds. The native
// timeline carries these so that splitting a caption lands the cut BETWEEN words
// by their real timing (Jordan: "we KNOW the word level timestamps and it needs to
// split the captions accordingly") instead of guessing a character position from a
// time fraction. Only present when the source had a word-level transcript.
type CaptionWord struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// CaptionCue is one chunked caption in the SOURCE's own seconds (the derived lane
// maps it onto each clip's window, the same way it mapped the raw transcript cues).
type CaptionCue struct {
	Start float64       `json:"start"`
	End   float64       `json:"end"`
	Text  string        `json:"text"`
	Words []CaptionWord `json:"words,omitempty"`
}

// CaptionChunks returns pace-based chunked captions for the source named `name`
// (an indexed video basename). It returns an ERROR when the video cannot be
// resolved at all - which at boot means "the folder is not indexed YET" (the reel
// loads before open_folder). The native lane treats that error as retry-until-ready,
// exactly like the transcript verb; a valid empty result (no word transcript) is
// NOT an error. Read-only, degrade-never-crash.
func (a *App) CaptionChunks(name string) ([]CaptionCue, error) {
	v, ok := a.lookupVideo(name)
	if !ok {
		// Fall back to a resolvable source path (an indexed source, or an absolute
		// path that exists) - the derived lane passes a basename once the folder is
		// indexed, but this keeps the verb usable before/without an index too.
		if v, ok = a.resolveSourceForRead(name); !ok {
			return nil, fmt.Errorf("no such video in folder: %s", name)
		}
	}
	tpath := findWordTranscript(v.Path)
	words := loadWordTranscript(tpath)
	if len(words) == 0 {
		// No word-level timing (e.g. an official .srt with no words) - fall back to
		// the raw transcript cues so a source without word timing still shows SOMETHING,
		// exactly as the lane did before. Only word-level transcripts get pace chunks.
		return a.rawTranscriptCues(name), nil
	}
	// One segment covering the whole source, in source time (Start 0), so subs.Build
	// returns cues on a source-time output timeline. FPS 0: no frame-snap for a
	// live preview (the render sidecar, when built, snaps to the reel's real rate).
	seg := subs.Segment{Source: v.Path, Start: 0, End: words[len(words)-1].End, Words: words}
	opt := subs.DefaultOptions()
	opt.GapSeconds = subs.AutoGapSeconds(words) // pace-based, derived from this transcript's own timing
	opt.FPS = 0
	out := make([]CaptionCue, 0, 64)
	for _, c := range subs.Build([]subs.Segment{seg}, opt) {
		if c.End > c.Start && strings.TrimSpace(c.Text) != "" {
			out = append(out, CaptionCue{Start: c.Start, End: c.End, Text: c.Text})
		}
	}
	attachWords(out, words)
	return out, nil
}

// attachWords maps each word onto the cue that owns it, so the caller can split a
// cue on real word timing. The cues Build returns are contiguous and
// non-overlapping (gap-filled, then overlap-clamped), so they TILE the source
// timeline: a word belongs to the cue whose span contains its MIDPOINT, and
// anything past the last cue's end (a zero-duration final word) belongs to the
// last cue. Per-word mapping, so a stray count mismatch can never misalign the
// rest of the lane.
func attachWords(cues []CaptionCue, words []subs.Word) {
	if len(cues) == 0 {
		return
	}
	for _, wd := range words {
		if strings.TrimSpace(wd.Word) == "" {
			continue
		}
		mid := (wd.Start + wd.End) / 2
		idx := len(cues) - 1 // tail default: past the last cue's end
		for i := range cues {
			if mid >= cues[i].Start && mid < cues[i].End {
				idx = i
				break
			}
		}
		cues[idx].Words = append(cues[idx].Words, CaptionWord{Word: wd.Word, Start: wd.Start, End: wd.End})
	}
}

// rawTranscriptCues is the fallback: the source's raw transcript segments (what the
// derived lane used before), as CaptionCues, for a source with no word-level timing.
func (a *App) rawTranscriptCues(name string) []CaptionCue {
	cues, err := a.Transcript(name)
	if err != nil {
		return []CaptionCue{}
	}
	out := make([]CaptionCue, 0, len(cues))
	for _, c := range cues {
		if c.End > c.Start && strings.TrimSpace(c.Text) != "" {
			out = append(out, CaptionCue{Start: c.Start, End: c.End, Text: c.Text})
		}
	}
	return out
}

// findWordTranscript locates a WORD-LEVEL transcript beside a source (becky-transcribe
// JSON), newest convention first. Mirrors cmd/subtitle's resolver so the derived lane
// and the render sidecar agree on which file is the transcript. "" when there is none.
func findWordTranscript(source string) string {
	dir := filepath.Dir(source)
	stem := strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	for _, cand := range []string{
		filepath.Join(dir, stem+".transcript.json"),
		source + ".transcript.json",
		filepath.Join(dir, "transcripts", stem+".json"),
	} {
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			return cand
		}
	}
	return ""
}

// loadWordTranscript reads a becky-transcribe JSON's word-level timings.
func loadWordTranscript(path string) []subs.Word {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var t struct {
		Words []subs.Word `json:"words"`
	}
	if json.Unmarshal(b, &t) != nil {
		return nil
	}
	return t.Words
}
