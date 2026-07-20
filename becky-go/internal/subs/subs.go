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
//   - A caption MAY span a cut when the speech is continuous across it
//     (continuesAcrossCut): the cut removed dead air, not a beat in the
//     sentence, so forcing a break there produces a stranded one- or two-word
//     fragment ("can" | "you post") instead of the phrase it actually is. The
//     two invariants above still hold for the SPANNING caption as a whole —
//     it starts where its first clip's cut starts and ends where its last
//     clip's cut ends — the boundary in between is simply covered, not left
//     unclaimed.
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
	// Source identifies which media these words came from. Segments sharing a
	// Source share a word list, so a word straddling a cut can be recognised as
	// the SAME word in both clips and spoken only once. Segments from different
	// sources never compete for a word.
	Source string
	Start  float64
	End    float64
	Words  []Word
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
	// Lowercase lowercases caption text and strips trailing .,;: — the cli-cut
	// look. ON by default: cli-cut is Jordan's actual working tool and its
	// defaults are the reference. Do not deviate from it without being asked.
	Lowercase bool
	// FPS, when > 0, snaps every caption boundary to a whole frame at that rate.
	// Use the media's REAL rate (29.97 = 30000/1001, not 30). A frame at 29.97 is
	// 33.3667ms, which is not a whole number of milliseconds, so anything that
	// stops at millisecond precision drifts — over a 90-cut edit that becomes
	// visible. Quantising here means the .srt every downstream surface loads is
	// already frame-aligned, and a timeline working in frames can snap to it
	// exactly.
	FPS float64
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
		Lowercase:      true,
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

// WordsPerSegment picks each segment's words, and — this is the point —
// guarantees a word straddling a cut is spoken by exactly ONE clip.
//
// WordsInRange alone cannot do this. It keeps any word OVERLAPPING the clip, so
// when a cut lands in the middle of a word (which it constantly does: Parakeet
// gives a word a span, and Jordan cuts on the frame he wants, not on word
// boundaries) the word overlaps the clip before the cut AND the clip after it,
// and gets captioned twice. In his post_constantly edit that produced
// "your odds of going viral" / "viral", "maybe" / "maybe", and
// "should you" / "you eat a pound" — a stutter on screen at every such cut.
//
// The rule: a word wholly inside a clip always belongs to it (so a deliberately
// repeated moment still captions twice). A word only PARTLY inside competes, and
// the clip holding the largest share of it wins. Ties go to the earlier clip.
func WordsPerSegment(segments []Segment) [][]Word {
	type claim struct {
		seg     int
		overlap float64
	}
	// key = source + word index; the index is stable because every segment of a
	// source shares that source's one word slice.
	type key struct {
		source string
		idx    int
	}
	best := map[key]claim{}

	type cand struct {
		idx       int
		word      Word
		contained bool
	}
	cands := make([][]cand, len(segments))

	for si, seg := range segments {
		for wi, w := range seg.Words {
			if w.End <= seg.Start || w.Start >= seg.End {
				continue
			}
			if strings.TrimSpace(w.Word) == "" {
				continue
			}
			contained := w.Start >= seg.Start && w.End <= seg.End
			cands[si] = append(cands[si], cand{idx: wi, word: w, contained: contained})
			if contained {
				continue
			}
			ov := min(w.End, seg.End) - max(w.Start, seg.Start)
			k := key{seg.Source, wi}
			if b, ok := best[k]; !ok || ov > b.overlap {
				best[k] = claim{seg: si, overlap: ov}
			}
		}
	}

	out := make([][]Word, len(segments))
	for si := range segments {
		kept := make([]Word, 0, len(cands[si]))
		for _, c := range cands[si] {
			if !c.contained && best[key{segments[si].Source, c.idx}].seg != si {
				continue // a clip with more of this word says it
			}
			kept = append(kept, c.word)
		}
		out[si] = kept
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
	perSeg := WordsPerSegment(segments)
	chunks := make([][][]Word, len(segments))
	for i := range segments {
		chunks[i] = RepairDangling(
			ChunkWords(perSeg[i], opt.MaxChars, opt.GapSeconds),
			opt.MaxChars)
	}
	return BuildFromChunks(segments, chunks, opt)
}

// continuesAcrossCut reports whether the speech closing one clip runs straight
// into the speech opening the next, so a caption for it should not be forced
// to fracture at the cut.
//
// BuildFromChunks chunks each segment independently, so a clip that holds only
// one or two words — because Jordan's cut landed a beat after the word instead
// of a clause later — gets its own one-word caption: "can" | "you post" from a
// clip boundary that split "can you post". The words are still adjacent in the
// OUTPUT audio (the cut removed dead air, not a pause in the sentence), so the
// caption can safely span the cut instead of fracturing there.
//
// Segments are always laid end to end on the output timeline (see the package
// doc), so the OUTPUT-time pause across a cut is exactly the silence trimmed
// off the end of the outgoing clip (after its last word) plus the silence
// trimmed off the start of the incoming one (before its first word) — no
// offset arithmetic needed, both halves are local to their own segment. This
// is the same pause measure ChunkWords already uses to break WITHIN a
// segment, so a cut is treated as no different from any other pause once its
// two sides are known.
func continuesAcrossCut(prevSeg Segment, prevChunks [][]Word, nextSeg Segment, nextChunks [][]Word, gapSeconds float64) bool {
	if len(prevChunks) == 0 || len(nextChunks) == 0 {
		return false // one side is silence (or filtered to nothing) — a real break, not a mid-phrase cut
	}
	last := prevChunks[len(prevChunks)-1]
	first := nextChunks[0]
	if len(last) == 0 || len(first) == 0 {
		return false
	}
	trailing := prevSeg.End - last[len(last)-1].End
	leading := first[0].Start - nextSeg.Start
	if trailing < 0 {
		trailing = 0
	}
	if leading < 0 {
		leading = 0
	}
	return trailing+leading <= gapSeconds
}

// BuildFromChunks is Build with the word grouping already decided — used when
// the LLM review pass (see llm.go) has regrouped the pass-1 chunks. The timing
// rules are identical; only where the lines break differs.
func BuildFromChunks(segments []Segment, chunksPerSeg [][][]Word, opt Options) []Cue {
	// Phase 1: per segment, chunk its words into caption-local times.
	type built struct {
		offset float64
		dur    float64
		cues   []Cue // times are LOCAL to the segment (0..dur)
	}
	prepared := make([]built, 0, len(segments))

	var offset float64
	for si, seg := range segments {
		dur := seg.Dur()
		b := built{offset: offset, dur: dur}

		var segChunks [][]Word
		if si < len(chunksPerSeg) {
			segChunks = chunksPerSeg[si]
		}
		for _, chunk := range segChunks {
			if len(chunk) == 0 {
				continue
			}
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

		// Span the cut into the previous caption when the speech is continuous
		// across it (continuesAcrossCut) — fold this segment's first cue into
		// the last cue already in out rather than starting a new, often
		// one-word, caption. Guarded by the same MaxChars+overflowSlack give
		// repair.go uses for phrase integrity: a merge that would blow the line
		// out is skipped, not forced.
		if i > 0 && len(out) > 0 && i-1 < len(chunksPerSeg) && i < len(chunksPerSeg) &&
			continuesAcrossCut(segments[i-1], chunksPerSeg[i-1], segments[i], chunksPerSeg[i], opt.GapSeconds) {
			joined := normalize(out[len(out)-1].Text+" "+cues[0].Text, opt.Lowercase)
			if opt.MaxChars <= 0 || len(joined) <= opt.MaxChars+overflowSlack {
				out[len(out)-1].End = cues[0].End
				out[len(out)-1].Text = joined
				cues = cues[1:]
			}
		}

		out = append(out, cues...)
	}
	// Clamp AFTER quantising: rounding two boundaries to frames can itself put a
	// caption a frame past the next one's start.
	return clampOverlaps(QuantizeToFrames(out, opt.FPS))
}

// QuantizeToFrames snaps every caption boundary to a whole frame at fps, and is
// a no-op when fps <= 0. The cut points these captions are timed against are
// themselves whole frames (that is what the NLE exported), so this makes the
// captions exactly as frame-accurate as the edit — and lets a timeline that
// works in frames snap to them without re-rounding.
//
// The gap-free invariant survives: two boundaries that were equal round to the
// same frame, and any adjacency broken by the one-frame minimum is restored.
func QuantizeToFrames(cues []Cue, fps float64) []Cue {
	if fps <= 0 || len(cues) == 0 {
		return cues
	}
	frameOf := func(t float64) int64 { return int64(t*fps + 0.5) }

	for i := range cues {
		s := frameOf(cues[i].Start)
		e := frameOf(cues[i].End)
		if e <= s {
			e = s + 1 // never shorter than a single frame
		}
		cues[i].Start = float64(s) / fps
		cues[i].End = float64(e) / fps
	}
	for i := 0; i < len(cues)-1; i++ {
		if cues[i].End < cues[i+1].Start {
			cues[i].End = cues[i+1].Start
		}
	}
	return cues
}

// clampOverlaps guarantees no two captions are on screen at once.
//
// The MinDuration floor runs after gap-filling and can push a caption's end PAST
// the next caption's start; nothing downstream took it back, so the .srt shipped
// overlapping cues and libass stacked two lines. MinDuration exists to stop a
// caption BLINKING OFF for a few frames — when the next caption arrives sooner
// than that, there is no blink to prevent, so the incoming caption wins.
func clampOverlaps(cues []Cue) []Cue {
	for i := 0; i < len(cues)-1; i++ {
		if cues[i].End > cues[i+1].Start {
			cues[i].End = cues[i+1].Start
		}
	}
	return cues
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
