// Package subs builds burn-ready captions whose timing is SNAPPED to the edit's
// cut points, so a caption can never flash on screen for a few frames at a cut.
//
// This is a faithful Go port of the proven cli-cut caption path
// (X:\Videos\video_tools\cli-cut\helpers\render.py: _chunk_words_pass1 +
// build_master_srt), which was left behind when becky-cut was ported from
// ae-vad-wrapper.py. The timing rules are the whole point and are NOT
// heuristics to re-tune casually:
//
//   - The first caption of a cut starts exactly at the cut's start.
//   - The last caption of a cut ends exactly at the cut's end.
//   - Within a cut, caption[i].End == caption[i+1].Start — zero gaps, so nothing
//     can blink off for a frame between two captions.
//   - A caption shorter than MinDuration is floored so it is never a flash.
//   - A short gap BETWEEN cuts is held through by the previous caption.
//
// Chunking is pace-driven, not fixed word counts: break the word stream when the
// speaker pauses (GapSeconds) or the line would get too long (MaxChars). Those
// two constants are what make captions land like TikTok captions instead of
// like subtitles.
//
// Pure Go: no exec, no ffmpeg, no models. Times in, text out.
package subs

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Word is one timed word. It matches becky-transcribe's word records, so a
// transcript's "words" array unmarshals straight into []Word.
type Word struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// Segment is one KEPT span of a source on the output timeline — i.e. one clip of
// the reel. Start/End are seconds into the SOURCE; Words are that source's word
// timings (also source-relative). The caller supplies them in output order; this
// package lays them end to end to derive the output timeline.
type Segment struct {
	Start float64
	End   float64
	Words []Word
}

// Dur is the segment's length in seconds, clamped to >= 0.
func (s Segment) Dur() float64 {
	if d := s.End - s.Start; d > 0 {
		return d
	}
	return 0
}

// Cue is one finished caption, timed on the OUTPUT timeline (the rendered
// video), ready to write as SRT.
type Cue struct {
	Start float64
	End   float64
	Text  string
}

// Options are the pacing and timing knobs. Zero values are not useful; use
// DefaultOptions and adjust.
type Options struct {
	// MaxChars breaks a caption once the line would exceed this many characters.
	// 22 is the shipped TikTok-style value: short enough to read in one glance.
	MaxChars int
	// GapSeconds breaks a caption when the speaker pauses longer than this.
	// 0.120 tracks natural delivery; becky-transcribe's own 0.6 is tuned for
	// readable forensic transcript lines and is far too long for captions.
	GapSeconds float64
	// MinDuration floors a caption's on-screen time so it is never a flash.
	MinDuration float64
	// PostSpeechHold extends the last caption of a cut across a gap to the next
	// cut, when that gap is no longer than this. In a gapless reel (clips butted
	// end to end) cuts never have gaps, so this is a no-op there; it exists for
	// edit lists that do leave holes.
	PostSpeechHold float64
	// Lowercase lowercases caption text and strips trailing .,;: — the old
	// cli-cut look. OFF by default: Jordan's own published captions
	// ("And if you've never", "Their label doesn't want you...") keep sentence
	// case and punctuation, so lowercasing would not match what he ships.
	Lowercase bool
}

// DefaultOptions is the cli-cut-proven timing configuration. The timing numbers
// are what produced captions Jordan was happy with; changing them changes the
// look. GapSeconds is the legacy constant and is normally replaced by
// AutoGapSeconds — see that function for why a constant does not transfer
// between ASRs.
func DefaultOptions() Options {
	return Options{
		MaxChars:       22,
		GapSeconds:     minAutoGap,
		MinDuration:    0.10,
		PostSpeechHold: 0.35,
		Lowercase:      false,
	}
}

// Auto-gap bounds. The floor is cli-cut's original constant, so a transcript
// with tight word boundaries reproduces the shipped behaviour exactly; the
// ceiling stops a pathological transcript from disabling pause-breaking.
const (
	minAutoGap = 0.120
	maxAutoGap = 0.600
)

// AutoGapSeconds derives the pause threshold from the transcript's OWN timing
// instead of assuming a constant.
//
// The 0.120s constant cli-cut shipped was tuned for an ASR that reported tight
// word boundaries. becky-transcribe's Parakeet does not: it quantises to 0.08s
// and leaves ~49% of words with end == start, so its inter-word "gap" in
// ordinary connected speech is 0.16-0.24s — above 0.120s. Applying the constant
// to it breaks after nearly every word.
//
// The 90th percentile of the gaps separates real pauses from ordinary word
// spacing whatever the ASR's timing habits: connected speech is the bulk of the
// distribution, breaths are the tail.
func AutoGapSeconds(words []Word) float64 {
	if len(words) < 8 {
		return minAutoGap
	}
	gaps := make([]float64, 0, len(words))
	for i := 1; i < len(words); i++ {
		g := words[i].Start - words[i-1].End
		if g < 0 {
			g = 0
		}
		gaps = append(gaps, g)
	}
	sort.Float64s(gaps)
	p90 := gaps[int(float64(len(gaps)-1)*0.90)]
	if p90 < minAutoGap {
		return minAutoGap
	}
	if p90 > maxAutoGap {
		return maxAutoGap
	}
	return p90
}

// WordsInRange returns the words that overlap [start,end). A word is included
// when any part of it falls inside the span, so a word straddling a cut point is
// kept rather than silently dropped.
func WordsInRange(words []Word, start, end float64) []Word {
	out := make([]Word, 0, 8)
	for _, w := range words {
		if w.End <= start || w.Start >= end {
			continue
		}
		if strings.TrimSpace(w.Word) == "" {
			continue
		}
		out = append(out, w)
	}
	return out
}

// ChunkWords is the deterministic pass-1 chunker: start a new caption when the
// pause before a word exceeds gapSeconds, or when adding the word would push the
// line past maxChars.
func ChunkWords(words []Word, maxChars int, gapSeconds float64) [][]Word {
	var chunks [][]Word
	var cur []Word
	curLen := 0

	for _, w := range words {
		text := strings.TrimSpace(w.Word)
		if text == "" {
			continue
		}
		addedLen := len(text)
		if len(cur) > 0 {
			addedLen++ // the joining space
			prevEnd := cur[len(cur)-1].End
			gap := w.Start - prevEnd
			if gap < 0 {
				gap = 0
			}
			if gap > gapSeconds || curLen+addedLen > maxChars {
				chunks = append(chunks, cur)
				cur = []Word{w}
				curLen = len(text)
				continue
			}
		}
		cur = append(cur, w)
		curLen += addedLen
	}
	if len(cur) > 0 {
		chunks = append(chunks, cur)
	}
	return chunks
}

// Build turns kept source segments into output-timeline cues with cut-snapped,
// gap-free timing. Segments are laid end to end in the order given: segment i
// occupies [offset, offset+Dur) on the output timeline.
//
// A segment with no words contributes its duration to the timeline but emits no
// cue — silence stays silent, and the following segment still lands at the right
// offset.
func Build(segments []Segment, opt Options) []Cue {
	// Phase 1: per segment, chunk its words into caption-local times.
	type built struct {
		offset float64
		dur    float64
		cues   []Cue // times are LOCAL to the segment (0..dur)
	}
	prepared := make([]built, 0, len(segments))

	var offset float64
	for _, seg := range segments {
		dur := seg.Dur()
		b := built{offset: offset, dur: dur}

		for _, chunk := range ChunkWords(WordsInRange(seg.Words, seg.Start, seg.End), opt.MaxChars, opt.GapSeconds) {
			localStart := chunk[0].Start - seg.Start
			localEnd := chunk[len(chunk)-1].End - seg.Start
			if localStart < 0 {
				localStart = 0
			}
			if localEnd > dur {
				localEnd = dur
			}
			words := make([]string, 0, len(chunk))
			for _, w := range chunk {
				words = append(words, strings.TrimSpace(w.Word))
			}
			text := normalize(strings.Join(words, " "), opt.Lowercase)
			if text == "" {
				continue
			}
			b.cues = append(b.cues, Cue{Start: localStart, End: localEnd, Text: text})
		}

		// Snap the segment's outer edges: the first caption starts with the cut,
		// the last one ends with it. This is what kills the leading/trailing flash.
		if n := len(b.cues); n > 0 {
			b.cues[0].Start = 0
			b.cues[n-1].End = dur
		}

		prepared = append(prepared, b)
		offset += dur
	}

	// Phase 2: lift to output-timeline times and close every gap.
	var out []Cue
	for i, b := range prepared {
		if len(b.cues) == 0 {
			continue
		}
		cues := make([]Cue, len(b.cues))
		for j, c := range b.cues {
			cues[j] = Cue{Start: b.offset + c.Start, End: b.offset + c.End, Text: c.Text}
		}

		// Hard-snap to the cut boundaries (guards against float drift above).
		cues[0].Start = b.offset
		cues[len(cues)-1].End = b.offset + b.dur

		// Gap-fill: each caption runs until the next one begins.
		for j := 0; j < len(cues)-1; j++ {
			cues[j].End = cues[j+1].Start
		}

		// Floor every caption so none is a flash. Ported faithfully from cli-cut:
		// this runs AFTER gap-fill, so a caption whose gap-filled span was under
		// MinDuration can end a few ms past the next caption's start. At <=100ms
		// that overlap is not visible; it is the shipped behaviour, not a bug to
		// "fix" without seeing the change on screen first.
		for j := range cues {
			if cues[j].End-cues[j].Start < opt.MinDuration {
				cues[j].End = cues[j].Start + opt.MinDuration
			}
		}

		// Post-speech hold across a short gap to the next cut.
		if i+1 < len(prepared) {
			gap := prepared[i+1].offset - (b.offset + b.dur)
			if gap > 0 && gap <= opt.PostSpeechHold {
				cues[len(cues)-1].End = prepared[i+1].offset
			}
		}

		out = append(out, cues...)
	}
	return out
}

// normalize applies the cli-cut caption look: collapse whitespace, drop trailing
// sentence punctuation, and (optionally) lowercase.
func normalize(s string, lower bool) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.TrimRight(s, ".,;:")
	if lower {
		s = strings.ToLower(s)
	}
	return strings.TrimSpace(s)
}

// WriteSRT emits the cues as SubRip. CRLF matches becky's other SRT writer
// (internal/edl.WriteSRT) so every .srt becky produces looks the same.
func WriteSRT(w io.Writer, cues []Cue) error {
	bw := bufio.NewWriter(w)
	for i, c := range cues {
		fmt.Fprintf(bw, "%d\r\n", i+1)
		fmt.Fprintf(bw, "%s --> %s\r\n", SRTTime(c.Start), SRTTime(c.End))
		fmt.Fprintf(bw, "%s\r\n\r\n", c.Text)
	}
	return bw.Flush()
}

// SRTTime renders seconds as SubRip's HH:MM:SS,mmm.
func SRTTime(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	totalMS := int(sec*1000 + 0.5)
	h := totalMS / 3600000
	totalMS -= h * 3600000
	m := totalMS / 60000
	totalMS -= m * 60000
	s := totalMS / 1000
	ms := totalMS - s*1000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}
