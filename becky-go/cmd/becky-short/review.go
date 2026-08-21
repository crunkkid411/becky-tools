// review.go — becky-short --review: measure a RENDERED short, not the plan
// that produced it.
//
// Every reference short-form pipeline is feed-forward: transcript -> pick ->
// crop -> render -> done (research/shorts-gap-decisions.md). Every real defect
// this pipeline has shipped produced a file that plays and exits 0 — a crop
// framed on a lamp, captions carrying words from elsewhere in the video, a
// moment that stops mid-sentence. All of them were found by looking at the
// OUTPUT, none by reading code. This is that look, automated.
//
// Three checks, all deterministic, all re-using machinery that already exists
// elsewhere in this repo:
//
//  1. Is the subject actually in frame? Independent FACE detection
//     (internal/faceembed + internal/facetrack, the same chain
//     cmd/becky-speaking and internal/facesig already run) over the rendered
//     file itself — a different detector than the POSE tracker that made the
//     crop, so if the two disagree that is itself a finding.
//  2. Do the burned captions match the audio? Fresh transcription of the
//     RENDERED file (internal/transcribex.EnsureWords) compared against the
//     .srt that was actually burned in (captionSidecarPath / --review-srt).
//  3. Does it end on a completed thought? internal/moment's payoffScore and
//     endsSentence, reused via their exported wrappers, applied to the
//     rendered file's OWN closing words.
//
// No model call anywhere in this file. If a check's inputs are unavailable
// (no face model configured, no captions burned, no speech at all) it
// degrades with a note and OK stays true for that check — a check that could
// not run is not the same thing as a check that failed.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"unicode"

	"becky-go/internal/config"
	"becky-go/internal/facesig"
	"becky-go/internal/mediainfo"
	"becky-go/internal/moment"
	"becky-go/internal/pathx"
	"becky-go/internal/quotes"
	"becky-go/internal/subs"
	"becky-go/internal/transcribex"
)

// reviewFaceSamplePeriod is how often the review's independent face pass
// samples the RENDERED short, in seconds. facesig.DefaultSamplePeriod (2.0s)
// is tuned for scanning a whole long video during moment ranking; a review
// clip is only 10-60s and the whole point is to catch a gap the render's own
// per-frame pose tracker missed, so this samples much denser.
const reviewFaceSamplePeriod = 0.25

// reviewCaptionOffsetLimit is the median caption-vs-audio offset, in seconds,
// above which a caption is no longer "roughly right". captions.go's
// capWordPad (0.25s) and the measured 79-89ms Parakeet clock drift noted
// there set the noise floor; 0.75s is comfortably past that and well short of
// "burned on the wrong timeline" (a jumpcut-timeline bug or a source-vs-clip
// mixup runs several seconds, not tenths).
const reviewCaptionOffsetLimit = 0.75

// reviewMatchWindow bounds how many leading words of a cue matchCue compares
// against a candidate transcript position — enough to disambiguate a common
// first word ("the", "so") without paying for a full alignment.
const reviewMatchWindow = 5

// reviewMaxUnmatchedFrac is the fraction of burned cues allowed to have NO
// matching text anywhere in the fresh transcript before the caption check
// fails on CONTENT (as opposed to timing) — the "captions from elsewhere in
// the video" failure class, independent of the offset check below.
const reviewMaxUnmatchedFrac = 0.34

type reviewFaceCheck struct {
	OK              bool     `json:"ok"`
	Coverage        float64  `json:"coverage"`
	Samples         int      `json:"samples"`
	LongestGapS     float64  `json:"longest_gap_s"`
	GapStartS       float64  `json:"gap_start_s,omitempty"`
	GapEndS         float64  `json:"gap_end_s,omitempty"`
	ClaimedCoverage *float64 `json:"claimed_coverage,omitempty"`
	Note            string   `json:"note,omitempty"`
}

type reviewCaptionCheck struct {
	OK             bool    `json:"ok"`
	Cues           int     `json:"cues"`
	Matched        int     `json:"matched"`
	Unmatched      int     `json:"unmatched"`
	MedianOffsetS  float64 `json:"median_offset_s"`
	WorstOffsetS   float64 `json:"worst_offset_s"`
	WorstCueText   string  `json:"worst_cue_text,omitempty"`
	WorstCueStartS float64 `json:"worst_cue_start_s,omitempty"`
	Note           string  `json:"note,omitempty"`
}

type reviewEndingCheck struct {
	OK           bool    `json:"ok"`
	LastText     string  `json:"last_text,omitempty"`
	EndsSentence bool    `json:"ends_sentence"`
	PayoffScore  float64 `json:"payoff_score"`
	Note         string  `json:"note,omitempty"`
}

type reviewReport struct {
	File     string             `json:"file"`
	Duration float64            `json:"duration"`
	OK       bool               `json:"ok"`
	Face     reviewFaceCheck    `json:"face"`
	Captions reviewCaptionCheck `json:"captions"`
	Ending   reviewEndingCheck  `json:"ending"`
}

// runReview is --review's entry point: measure clip and print the verdict.
func runReview(cfg config.Config, clip, srtOverride string, claimedCoverage, minCov, maxGap float64, verbose bool) int {
	rep, err := review(cfg, clip, srtOverride, claimedCoverage, minCov, maxGap, verbose)
	if err != nil {
		fmt.Fprintln(os.Stderr, "becky-short --review:", err)
		return 2
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(rep)
	if !rep.OK {
		return 1
	}
	return 0
}

// review runs all three checks against an already-rendered short. The only
// hard error is "cannot even read the file" — every check inside degrades on
// its own missing inputs rather than aborting the other two.
func review(cfg config.Config, clip, srtOverride string, claimedCoverage, minCov, maxGap float64, verbose bool) (reviewReport, error) {
	if _, err := os.Stat(clip); err != nil {
		return reviewReport{}, fmt.Errorf("cannot read %s: %w", clip, err)
	}
	info, err := mediainfo.Probe(cfg.FFprobe, clip)
	if err != nil || info.Duration <= 0 {
		return reviewReport{}, fmt.Errorf("could not read %s's duration: %v", pathx.Base(clip), err)
	}

	rep := reviewReport{File: clip, Duration: info.Duration}
	rep.Face = reviewFace(cfg, clip, info.Duration, claimedCoverage, minCov, maxGap, verbose)

	srtPath := srtOverride
	if srtPath == "" {
		srtPath = captionSidecarPath(clip)
	}
	words, _, wErr := transcribex.EnsureWords(clip, func(f string, a ...any) { logIfShort(verbose, f, a...) })

	rep.Captions = reviewCaptions(srtPath, words, wErr)
	rep.Ending = reviewEnding(words, wErr, info.FPS)

	rep.OK = rep.Face.OK && rep.Captions.OK && rep.Ending.OK
	return rep, nil
}

// reviewFace independently re-detects the subject over the RENDERED file —
// FACE detection (internal/faceembed/internal/facetrack), not the POSE
// tracker that made the crop, so this is a genuinely separate signal from the
// render's own claim, not the same measurement read twice.
func reviewFace(cfg config.Config, clip string, duration, claimedCoverage, minCov, maxGap float64, verbose bool) reviewFaceCheck {
	var claimed *float64
	if claimedCoverage > 0 {
		claimed = &claimedCoverage
	}
	if cfg.FaceModelRoot == "" {
		return reviewFaceCheck{OK: true, ClaimedCoverage: claimed, Note: "face model not configured — skipped"}
	}

	sig, err := facesig.Run(cfg, clip, reviewFaceSamplePeriod, cfg.Device)
	if err != nil {
		return reviewFaceCheck{OK: true, ClaimedCoverage: claimed, Note: "face detection unavailable: " + err.Error()}
	}
	// AnyIn, not In: this is a RENDERED short that cuts between people on
	// purpose, so "is one identity continuously present" is the wrong question
	// and fails by construction. See facesig.AnyIn.
	win := sig.AnyIn(0, duration)
	gap, gapStart, gapEnd := longestFaceGap(sig, duration)

	c := reviewFaceCheck{
		Coverage:        win.Coverage,
		Samples:         win.Samples,
		LongestGapS:     gap,
		GapStartS:       gapStart,
		GapEndS:         gapEnd,
		ClaimedCoverage: claimed,
	}
	c.OK = win.Coverage >= minCov && gap <= maxGap
	switch {
	case !c.OK && win.Coverage < minCov:
		c.Note = fmt.Sprintf("measured face coverage %.0f%% is below the %.0f%% bar — the rendered frame does not "+
			"hold the subject as well as the render claimed", win.Coverage*100, minCov*100)
	case !c.OK:
		c.Note = fmt.Sprintf("the subject is off screen for %.1fs in a row at %.1f-%.1fs (limit %.1fs)", gap, gapStart, gapEnd, maxGap)
	}
	if claimed != nil && math.Abs(win.Coverage-*claimed) > 0.15 {
		note := fmt.Sprintf("claimed coverage %.2f vs measured %.2f — a %.0fpp gap between the render's own "+
			"pose-tracking claim and an independent face-detection pass", *claimed, win.Coverage, math.Abs(win.Coverage-*claimed)*100)
		if c.Note == "" {
			c.Note = note
		} else {
			c.Note += "; " + note
		}
	}
	return c
}

// longestFaceGap finds the longest stretch of [0,duration] with NO face
// detected by ANY track — union across tracks, not just the best-covered one,
// because a subject the tracker re-acquires under a new track id is still
// present, and this check asks "was a face there", not "was it the same
// track". Mirrors internal/crop.Path.LongestGap's role in the render's own
// refusal gate, now applied to what actually came out.
func longestFaceGap(sig facesig.Signals, duration float64) (gap, start, end float64) {
	if !sig.OK || len(sig.Tracks) == 0 {
		return duration, 0, duration
	}
	var times []float64
	for _, tr := range sig.Tracks {
		for _, d := range tr.Detections {
			times = append(times, d.Time)
		}
	}
	sort.Float64s(times)

	prev := 0.0
	for _, t := range times {
		if t-prev > gap {
			gap, start, end = t-prev, prev, t
		}
		if t > prev {
			prev = t
		}
	}
	if duration-prev > gap {
		gap, start, end = duration-prev, prev, duration
	}
	return
}

// reviewCaptions compares the burned .srt against a FRESH transcription of
// the rendered file's own audio. Two independent failure modes, both real
// bugs this pipeline has shipped: captions whose WORDS don't match what's
// actually said here (content from elsewhere in the video), and captions
// whose TIMING is off (timed to the source instead of the clip, or left on
// the uncut timeline).
func reviewCaptions(srtPath string, words []subs.Word, wErr error) reviewCaptionCheck {
	cues, cErr := quotes.ParseSRTFile(srtPath)
	if cErr != nil {
		return reviewCaptionCheck{OK: true, Note: "no burned captions to check: " + cErr.Error()}
	}
	if wErr != nil {
		return reviewCaptionCheck{OK: true, Cues: len(cues), Note: "could not transcribe the rendered file: " + wErr.Error()}
	}
	fresh := tokenizeWords(words)

	var offsets []float64
	matched, unmatched := 0, 0
	worstAbs, worstOffset, worstStart := -1.0, 0.0, 0.0
	var worstText string
	for _, cue := range cues {
		cueTokens := tokenizeText(cue.Text)
		matchedStart, score, found := matchCue(cueTokens, fresh, cue.Start)
		if !found || score == 0 {
			unmatched++
			continue
		}
		matched++
		off := matchedStart - cue.Start
		offsets = append(offsets, off)
		if math.Abs(off) > worstAbs {
			worstAbs, worstOffset, worstStart, worstText = math.Abs(off), off, cue.Start, cue.Text
		}
	}

	c := reviewCaptionCheck{Cues: len(cues), Matched: matched, Unmatched: unmatched}
	if matched > 0 {
		c.MedianOffsetS = median(offsets)
		c.WorstOffsetS = worstOffset
		c.WorstCueText = worstText
		c.WorstCueStartS = worstStart
	}
	unmatchedFrac := 0.0
	if len(cues) > 0 {
		unmatchedFrac = float64(unmatched) / float64(len(cues))
	}
	c.OK = unmatchedFrac <= reviewMaxUnmatchedFrac && math.Abs(c.MedianOffsetS) <= reviewCaptionOffsetLimit
	switch {
	case !c.OK && unmatchedFrac > reviewMaxUnmatchedFrac:
		c.Note = fmt.Sprintf("%d/%d burned cues match no words in the rendered audio at all — captions from elsewhere",
			unmatched, len(cues))
	case !c.OK:
		c.Note = fmt.Sprintf("captions run %.2fs off the audio (median) — worst is %.2fs at %.2fs: %q",
			c.MedianOffsetS, worstOffset, worstStart, worstText)
	}
	return c
}

// reviewEnding checks whether the rendered file's own closing words complete
// a thought, reusing internal/moment's structural scoring rather than
// re-implementing sentence detection. endsSentence on the literal last cue is
// the pass/fail gate (quotable evidence); PayoffScore is reported alongside
// as the supporting number moment.Find itself would have used.
func reviewEnding(words []subs.Word, wErr error, fps float64) reviewEndingCheck {
	if wErr != nil {
		return reviewEndingCheck{OK: true, Note: "could not transcribe the rendered file: " + wErr.Error()}
	}
	if len(words) == 0 {
		return reviewEndingCheck{OK: true, Note: "no speech in this clip to check"}
	}
	last := words[len(words)-1]
	opt := subs.ShortOptions()
	opt.GapSeconds = subs.AutoGapSeconds(words)
	opt.FPS = fps
	cues := subs.Build([]subs.Segment{{Source: "clip", Start: words[0].Start, End: last.End, Words: words}}, opt)
	if len(cues) == 0 {
		return reviewEndingCheck{OK: true, Note: "no captionable speech in this clip to check"}
	}

	segs := make([]moment.Segment, len(cues))
	for i, c := range cues {
		segs[i] = moment.Segment{Start: c.Start, End: c.End, Text: c.Text}
	}
	gap := moment.AutoThoughtGap(segs)
	lastText := segs[len(segs)-1].Text
	ends := moment.EndsSentence(lastText)
	payoff := moment.PayoffScore(segs, len(segs)-1, gap)

	e := reviewEndingCheck{OK: ends, LastText: lastText, EndsSentence: ends, PayoffScore: payoff}
	if !ends {
		e.Note = fmt.Sprintf("the clip ends mid-thought, no terminal punctuation on the last words: %q", lastText)
	}
	return e
}

// wordToken is a normalized (lowercase, punctuation-stripped) word plus its
// timestamp in the fresh transcript.
type wordToken struct {
	norm  string
	start float64
}

func normWord(s string) string {
	s = strings.ToLower(s)
	return strings.TrimFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

func tokenizeWords(words []subs.Word) []wordToken {
	out := make([]wordToken, 0, len(words))
	for _, w := range words {
		n := normWord(w.Word)
		if n == "" {
			continue
		}
		out = append(out, wordToken{norm: n, start: w.Start})
	}
	return out
}

// tokenizeText normalizes a burned cue's TEXT the same way tokenizeWords
// normalizes the transcript, with no timestamps (a cue's text has none per
// word) — matchCue only needs the sequence of normalized words.
func tokenizeText(text string) []string {
	fields := strings.Fields(text)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if n := normWord(f); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// matchCue finds where in the fresh transcript a burned cue's words actually
// occur. Two re-transcriptions of the SAME rendered audio should agree almost
// word for word regardless of any timing bug, so a short leading-word match
// is enough to relocate "the same moment" even when it has drifted seconds
// away from the cue's own claimed start. Ties (a common first word occurring
// more than once) break toward the occurrence closest to the cue's own start,
// which is correct whenever the offset is not itself the failure being
// measured, and merely noisy on the deliberately-bad case this exists to
// catch — acceptable, since that case is caught by the OFFSET being large in
// the first place, not by picking the "right" duplicate.
func matchCue(cueWords []string, fresh []wordToken, cueStart float64) (matchedStart float64, score int, found bool) {
	if len(cueWords) == 0 {
		return 0, 0, false
	}
	bestScore := -1
	bestDist := math.Inf(1)
	for i, tok := range fresh {
		if tok.norm != cueWords[0] {
			continue
		}
		s := 0
		for k := 0; k < reviewMatchWindow && k < len(cueWords) && i+k < len(fresh); k++ {
			if fresh[i+k].norm != cueWords[k] {
				break
			}
			s++
		}
		dist := math.Abs(fresh[i].start - cueStart)
		if s > bestScore || (s == bestScore && dist < bestDist) {
			bestScore, bestDist, matchedStart, found = s, dist, fresh[i].start, true
		}
	}
	return matchedStart, bestScore, found
}

// median of a float64 slice. Not sorted in place — the caller may still want
// offsets in original (per-cue) order.
func median(vs []float64) float64 {
	if len(vs) == 0 {
		return 0
	}
	sorted := append([]float64(nil), vs...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

// reviewFlags is registered from main() so --review's own knobs live beside
// the render flags they reuse (--min-coverage, --max-gap) instead of a
// second flag.Parse call.
func reviewFlags() (review, srt *string, claimed *float64) {
	review = flag.String("review", "", "review an already-rendered short instead of making one: "+
		"measure the FILE, not the plan (path to the rendered .mp4)")
	srt = flag.String("review-srt", "", "the .srt burned into --review's file "+
		"(default: found beside it, e.g. clip.srt — becky-short saves it there automatically)")
	claimed = flag.Float64("review-claimed-coverage", 0, "the coverage becky-short's own render reported, "+
		"to check that claim against the measured output (0 = not given, skip the comparison)")
	return
}
