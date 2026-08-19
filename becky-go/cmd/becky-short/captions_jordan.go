// captions_jordan.go — captions.go's twin for --caption-style=jordan, the
// stacked-lines/coloured-emphasis-word look reverse-engineered from Jordan's
// own edit (research/jordan-edit-reverse-engineered.md, the CAPTIONS
// section). OFF by default; captions.go's cli-cut path is untouched.
//
// Part A (internal/subs/ass.go) does the FORMAT: stacked lines, per-word
// colour override, a real .ass file. This file does the CONTENT DECISION
// Part B asks for — "use the audio, not a guess at semantics" — by finding,
// for each caption block, whichever audiosig event (loudness spike or pitch
// rise) coincides with it, and colouring the word nearest that event. A
// block with no event in it gets no coloured word; that is correct, not a
// gap to paper over.
//
// Part C is deliberately NOT here: no profanity red-boxing, no emoji, no
// content-aware placement. All three are taste calls Jordan has not made —
// see the task that added this file. MarginV is the same fixed constant
// cli-cut already uses (subs.DefaultStyle), which is an honest "we don't
// know where to put it yet", not a claim that a fixed margin is right.
package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"becky-go/internal/audiosig"
	"becky-go/internal/config"
	"becky-go/internal/subs"
	"becky-go/internal/transcribex"
)

// jordanMaxChars is Options.MaxChars for the jordan style. cli-cut's 22
// caps a BLOCK at roughly one line's worth of text; a jordan block is 2-3
// STACKED lines, so it needs a bigger character budget to hold enough words
// to stack — the SAME chunker (subs.ChunkWords, called via
// subs.BuildWithWords) just reconfigured, not a second one. Picked as
// roughly jordanWordsPerLine(3) * 3 lines * ~5 chars/word; a taste constant,
// adjustable like the rest of this style.
const jordanMaxChars = 46

// audioSigCache memoises audiosig.Run per source path. audiosig always
// analyses the WHOLE file (same shape as cutCache's becky-cut memoisation in
// jumpcuts.go), so a --reel job cutting several jordan-style shorts from the
// same source must not re-run it once per clip.
type audioSigCache struct {
	sig  map[string]audiosig.Signals
	errs map[string]error
}

func newAudioSigCache() *audioSigCache {
	return &audioSigCache{sig: map[string]audiosig.Signals{}, errs: map[string]error{}}
}

func (c *audioSigCache) get(cfg config.Config, src string) (audiosig.Signals, error) {
	if s, ok := c.sig[src]; ok {
		return s, nil
	}
	if err, ok := c.errs[src]; ok {
		return audiosig.Signals{}, err
	}
	s, err := audiosig.Run(cfg, src)
	if err != nil {
		c.errs[src] = err
		return audiosig.Signals{}, err
	}
	c.sig[src] = s
	return s, nil
}

// captionASS is captionSRT's twin for the jordan style: same word-timed
// transcript, subs.BuildWithWords instead of subs.Build (so each cue keeps
// its words), a bigger MaxChars, and an .ass file instead of an .srt.
//
// Returns TWO paths: assPath is what actually gets burned; srtPath is a
// PLAIN-TEXT sidecar (same timing, no colour codes) saved as the caption
// sidecar so `becky-short --review` — which parses SRT, not ASS — can still
// check the burned words against a fresh transcript. The words shown are
// identical between the two files; only colour differs, which review does
// not check.
func captionASS(cfg config.Config, video string, in, out, fps float64, outW, outH int, dir string,
	asig *audioSigCache, logf transcribex.Logf) (assPath, srtPath string, n int, err error) {
	words, _, err := transcribex.EnsureWords(video, logf)
	if err != nil {
		return "", "", 0, err
	}
	segments := []subs.Segment{{Source: "clip", Start: in, End: out,
		Words: subs.WordsInRange(words, in-capWordPad, out+capWordPad)}}
	if len(segments[0].Words) == 0 {
		return "", "", 0, nil
	}

	opt := subs.DefaultOptions()
	opt.GapSeconds = subs.AutoGapSeconds(segments[0].Words)
	opt.FPS = fps
	opt.MaxChars = jordanMaxChars

	cues := subs.BuildWithWords(segments, opt)
	if len(cues) == 0 {
		return "", "", 0, nil
	}

	sig, _ := asig.get(cfg, video) // best-effort: no signal just means no emphasis anywhere
	emphasis := jordanEmphasis(cues, segments, sig)

	return writeJordanFiles(cues, emphasis, outW, outH, dir)
}

// captionASSJumpcut is captionASS for a jumpcut short: cues span every kept
// span the same way captionSRTJumpcut does, and emphasis is matched per span
// via the same segments slice (segmentAt below handles > 1 segment already).
func captionASSJumpcut(cfg config.Config, video string, winIn, winOut, fps float64, spans []keepSpan, outW, outH int,
	dir string, asig *audioSigCache, logf transcribex.Logf) (assPath, srtPath string, n int, err error) {
	words, _, err := transcribex.EnsureWords(video, logf)
	if err != nil {
		return "", "", 0, err
	}
	words = wordsInSpans(words, spans)
	if len(words) == 0 {
		return "", "", 0, nil
	}
	segments := make([]subs.Segment, len(spans))
	for i, sp := range spans {
		segments[i] = subs.Segment{Source: "clip", Start: sp.In, End: sp.Out, Words: words}
	}

	opt := subs.DefaultOptions()
	opt.GapSeconds = subs.AutoGapSeconds(words)
	opt.FPS = fps
	opt.MaxChars = jordanMaxChars

	cues := subs.BuildWithWords(segments, opt)
	if len(cues) == 0 {
		return "", "", 0, nil
	}

	sig, _ := asig.get(cfg, video)
	emphasis := jordanEmphasis(cues, segments, sig)

	return writeJordanFiles(cues, emphasis, outW, outH, dir)
}

// writeJordanFiles is the file-writing tail shared by captionASS and
// captionASSJumpcut: the .ass to burn plus the plain .srt sidecar for review.
func writeJordanFiles(cues []subs.CueWords, emphasis []int, outW, outH int, dir string) (assPath, srtPath string, n int, err error) {
	st := subs.DefaultJordanStyle(outH)

	assPath = filepath.Join(dir, "captions.ass")
	af, err := os.Create(assPath)
	if err != nil {
		return "", "", 0, err
	}
	if err = subs.WriteASS(af, cues, emphasis, st, outW, outH); err != nil {
		af.Close()
		return "", "", 0, err
	}
	if err = af.Close(); err != nil {
		return "", "", 0, err
	}

	plain := make([]subs.Cue, len(cues))
	for i, c := range cues {
		plain[i] = subs.Cue{Start: c.Start, End: c.End, Text: subs.PlainStackedText(c.Words)}
	}
	srtPath = filepath.Join(dir, "captions_plain.srt")
	sf, serr := os.Create(srtPath)
	if serr != nil {
		// The burn itself does not need the sidecar — degrade to "no sidecar"
		// rather than fail the whole caption pass over a file --review reads.
		return assPath, "", len(cues), nil
	}
	_ = subs.WriteSRT(sf, plain)
	sf.Close()

	return assPath, srtPath, len(cues), nil
}

// captionFilterASS is captionFilter's twin for the jordan style: an .ass
// file carries its own style and inline overrides, so libass needs no
// force_style — there is nothing left to force.
func captionFilterASS(assPath string) string {
	return fmt.Sprintf("subtitles=%s", subs.EscapeFilterPath(assPath))
}

// jordanEmphasis picks, for each cue, which word (index into cue.Words)
// coincides with the STRONGEST audio event inside that block. -1 means no
// event landed in the block: the block renders with no coloured word.
//
// "Strongest" mixes two different units (loudness dB rise vs. pitch ratio)
// with no principled common scale, so this is a simple, documented
// tie-break rather than a fabricated one: a loudness SPIKE wins over a pitch
// rise whenever a block has both (loudness is the more direct signal for
// "hit hard"), and within one type the biggest value wins.
//
// cue.Words are on the OUTPUT timeline (subs.BuildWithWords); audiosig
// always analyses the whole SOURCE file, so events are on the SOURCE
// timeline. segmentAt converts one cue at a time using the exact segment it
// came from — a cue never spans a cut (internal/subs: "A CUT ENDS THE
// CHUNK"), so one segment is always enough to do this correctly.
func jordanEmphasis(cues []subs.CueWords, segments []subs.Segment, sig audiosig.Signals) []int {
	out := make([]int, len(cues))
	for i := range out {
		out[i] = -1
	}
	if !sig.OK {
		return out
	}
	for i, cue := range cues {
		if len(cue.Words) == 0 {
			continue
		}
		seg, offset, ok := segmentAt(segments, (cue.Start+cue.End)/2)
		if !ok {
			continue
		}
		srcStart := seg.Start + (cue.Start - offset)
		srcEnd := seg.Start + (cue.End - offset)

		bestSrcT, bestMag, have := 0.0, -1.0, false
		for _, e := range sig.Spikes {
			if e.T < srcStart || e.T > srcEnd {
				continue
			}
			if !have || e.RiseDB > bestMag {
				bestSrcT, bestMag, have = e.T, e.RiseDB, true
			}
		}
		if !have {
			for _, e := range sig.PitchRises {
				if e.T < srcStart || e.T > srcEnd {
					continue
				}
				if !have || e.Ratio > bestMag {
					bestSrcT, bestMag, have = e.T, e.Ratio, true
				}
			}
		}
		if !have {
			continue
		}
		out[i] = nearestWord(cue.Words, offset+(bestSrcT-seg.Start))
	}
	return out
}

// nearestWord returns the index of the word in words (output-timeline)
// whose span is closest to t (0 if t falls inside a word).
// unstressable are words a viewer never reads as the emphasis of a line.
//
// The audio says WHERE the stress landed; it cannot say which word carries the
// meaning, and English routinely puts a loudness peak on the syllable of a
// function word next to the real one. First render on his footage coloured
// "ON THE ON >>THE<< BEAUTY MIRROR" and "I JUST..." - each technically the word
// nearest the spike, each looking like a bug.
//
// Every coloured word in Jordan's own edit is a content word: GUYS, SORRY,
// REALLY, OKAY, CHANGING, NO, YES, ANYWAYS, FRENCH FRY FIRST, spit it out,
// BURGER DOWN, switch (research/jordan-edit-reverse-engineered.md). So the
// nearest word to the spike is taken UNLESS it is one of these, in which case
// the nearest one that is not is used instead. Pronouns like YOU are kept - he
// colours "thank YOU".
var unstressable = map[string]bool{
	"a": true, "an": true, "the": true, "and": true, "but": true, "or": true,
	"of": true, "to": true, "in": true, "on": true, "at": true, "for": true,
	"with": true, "from": true, "by": true, "as": true, "is": true, "are": true,
	"was": true, "were": true, "be": true, "been": true, "am": true,
	"that": true, "this": true, "there": true, "here": true, "just": true,
	"so": true, "if": true, "then": true, "than": true, "into": true,
}

func stressable(w subs.Word) bool {
	t := strings.ToLower(strings.Trim(strings.TrimSpace(w.Word), ".,!?;:\"'()"))
	return t != "" && !unstressable[t]
}

// nearestWord returns the index of the word closest to output time t, skipping
// words no viewer would read as the emphasis (see unstressable). If EVERY word
// in the block is unstressable, the nearest one is returned anyway - colouring
// something is better than a block that silently loses its emphasis.
func nearestWord(words []subs.Word, t float64) int {
	best, bestDist := -1, math.Inf(1)
	fallback, fallbackDist := 0, math.Inf(1)
	for wi, w := range words {
		d := 0.0
		switch {
		case t < w.Start:
			d = w.Start - t
		case t > w.End:
			d = t - w.End
		}
		if d < fallbackDist {
			fallback, fallbackDist = wi, d
		}
		if stressable(w) && d < bestDist {
			best, bestDist = wi, d
		}
	}
	if best < 0 {
		return fallback
	}
	return best
}

// segmentAt returns the segment containing OUTPUT time t and that segment's
// own offset on the output timeline (the same running offset subs.Build
// accumulates internally) — enough to convert t between the output and
// source timelines in either direction.
func segmentAt(segments []subs.Segment, t float64) (seg subs.Segment, offset float64, ok bool) {
	var acc float64
	for _, s := range segments {
		dur := s.Dur()
		if t < acc+dur+1e-6 {
			return s, acc, true
		}
		acc += dur
	}
	if len(segments) > 0 {
		last := segments[len(segments)-1]
		return last, acc - last.Dur(), true
	}
	return subs.Segment{}, 0, false
}
