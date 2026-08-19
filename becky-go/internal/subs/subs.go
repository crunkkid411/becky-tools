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

// CueWords is a Cue plus the words that compose it, each Word's Start/End
// carried onto the same OUTPUT timeline as the Cue. It exists for a caller
// that needs to know WHERE INSIDE a cue a specific word falls — the ASS
// writer's per-word emphasis colour (ass.go) is the only user today — not
// just the merged text Cue.Text carries. A word's Start/End here is its own
// natural span (clamped to its segment), not run through the cue-level
// gap-fill/quantize rules below: precise enough to say "an audio event
// landed near this word", not frame-accurate.
type CueWords struct {
	Cue
	Words []Word
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

// gapEps absorbs float noise when a gap is compared to the pause threshold.
// Parakeet quantises word times to 0.08s, so dozens of gaps are "0.32s" — but
// as float64 subtractions they differ in their last bits (0.3200000000000216
// vs 0.3200000000000074), and the threshold is itself one of those gaps
// (AutoGapSeconds' p90). Strictly-greater on those near-equal floats made the
// SAME spoken gap break in one place and not another: on Jordan's real edit it
// split "a thousand" / "videos" and stranded "i", at spots where silencedetect
// shows no real pause at all. A microsecond is far below anything an ASR can
// distinguish and far above the float noise, so: a gap within gapEps of the
// threshold is NOT a pause.
const gapEps = 1e-6

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
//
// A word that overlaps NO segment at all is rescued, never dropped: Parakeet's
// timing is measurably less accurate than the cut points (2026-07-25, the
// 27_walmart footage: "but" and "I" landed 79-89ms past a kept clip's edge while
// still clearly audible inside it — the cut did not trim them, the transcript's
// clock just drifted). Jordan's rule is that the cut is ground truth and the
// transcript's timing is the less-trustworthy signal, so a stray word is
// retimed onto its nearest same-source cut instead of vanishing from the
// caption.
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
	claimed := map[key]bool{} // every (source, word index) that overlapped >=1 segment

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
			claimed[key{seg.Source, wi}] = true
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
			// A word this segment WON but that hangs over the cut is clamped to
			// the cut, exactly like a rescued word below - the cut wins. Without
			// this, a word straddling the out-point kept its real (outside) end,
			// absorbShortWords then pulled the next word's start out there with
			// it, and the cue came out AFTER its own segment: Jordan's
			// IMG_9624 edit shipped an "and" cue running 00:29,363 --> 00:28,662
			// (an end before its start, which is a broken .srt, not just an ugly
			// one). Found 2026-08-16.
			w := c.word
			w.Start = clampToSpan(w.Start, segments[si].Start, segments[si].End)
			w.End = clampToSpan(w.End, segments[si].Start, segments[si].End)
			if w.End < w.Start {
				w.End = w.Start
			}
			kept = append(kept, w)
		}
		out[si] = kept
	}

	// Rescue pass: a word that missed every segment window goes to its nearest
	// same-source segment, clamped to fit inside it (the cut wins, per Jordan's
	// rule above). Segments sharing a Source share that source's one word slice,
	// so walking it once per source (via the first segment seen) covers every
	// word without re-scanning duplicates.
	bySource := map[string][]int{}
	for si, seg := range segments {
		bySource[seg.Source] = append(bySource[seg.Source], si)
	}
	sourceDone := map[string]bool{}
	for _, seg := range segments {
		if sourceDone[seg.Source] {
			continue
		}
		sourceDone[seg.Source] = true
		segIdxs := bySource[seg.Source]
		for wi, w := range seg.Words {
			if strings.TrimSpace(w.Word) == "" || claimed[key{seg.Source, wi}] {
				continue
			}
			nearest := segIdxs[0]
			nearestGap := segmentGap(segments[nearest], w)
			for _, si := range segIdxs[1:] {
				if g := segmentGap(segments[si], w); g < nearestGap {
					nearest, nearestGap = si, g
				}
			}
			cw := w
			cw.Start = clampToSpan(cw.Start, segments[nearest].Start, segments[nearest].End)
			cw.End = clampToSpan(cw.End, segments[nearest].Start, segments[nearest].End)
			if cw.End < cw.Start {
				cw.End = cw.Start
			}
			out[nearest] = append(out[nearest], cw)
		}
	}
	for si := range out {
		sort.SliceStable(out[si], func(a, b int) bool { return out[si][a].Start < out[si][b].Start })
	}
	return out
}

// segmentGap is 0 when w overlaps seg, else the time distance from w to
// whichever edge of seg is nearer — how WordsPerSegment picks the nearest
// segment to rescue a stray word onto.
func segmentGap(seg Segment, w Word) float64 {
	if w.End <= seg.Start {
		return seg.Start - w.End
	}
	if w.Start >= seg.End {
		return w.Start - seg.End
	}
	return 0
}

// clampToSpan pins x inside [lo,hi]. Used to retime a rescued word onto its new
// segment: a word rescued for being entirely past the segment's end needs BOTH
// its Start and End brought back inside, not just one relative to the other.
func clampToSpan(x, lo, hi float64) float64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

// PrepareWords is THE per-segment word preparation every caption path runs
// before chunking. It exists because there were TWO copies of this recipe —
// Build's and PlanChunks' — and becky-subtitle only ever calls PlanChunks. A
// rule fixed in Build (this is how estimateMissingEnds first shipped, 2026-08-16)
// therefore never reached a single caption Jordan saw, which is a large part of
// why "the hard rules keep getting lost". One function, both callers, no drift.
func PrepareWords(words []Word, fps float64) []Word {
	return absorbShortWords(estimateMissingEnds(words), fps)
}

// perCharSeconds is how long one character of speech takes at a normal talking
// pace. Measured on Jordan's own transcripts: of the words that DO carry a
// duration, the median is 0.040 s/char - and those durations are themselves
// short (see estimateMissingEnds), so 0.05 is the honest middle. It is only ever
// used to fill in a MISSING end time, never to override a real one.
const perCharSeconds = 0.05

// estimateMissingEnds gives a word that came back with NO duration a plausible
// one, capped by when the next word starts.
//
// WHY (2026-08-16). Parakeet via onnx-asr reports a per-TOKEN START time, and
// merge_tokens_to_words sets a word's end to its LAST TOKEN'S START — so every
// single-token word arrives with end == start. That is 98 of the 184 words (53%)
// in Jordan's IMG_9624 transcript. Everything downstream then measures the pause
// before the next word from this word's START, which counts the time he spent
// SAYING the word as silence. Pace is caption rule #1, so those phantom pauses
// became wrong line breaks: 20 gaps in that clip crossed the 0.4s threshold and
// 9 of them vanish once a word is allowed to have a length ("your last | three"
// split because "last" measured 0.00s long).
//
// This does NOT invent timing where the transcript has some: a word with a real
// duration is returned untouched, and an estimate never runs past the next word.
func estimateMissingEnds(words []Word) []Word {
	if len(words) == 0 {
		return words
	}
	out := make([]Word, len(words))
	copy(out, words)
	for i := range out {
		if out[i].End > out[i].Start {
			continue // the transcript knows; leave it alone
		}
		est := out[i].Start + perCharSeconds*float64(len([]rune(strings.TrimSpace(out[i].Word))))
		if i+1 < len(out) && est > out[i+1].Start {
			est = out[i+1].Start // a word never runs into the next one
		}
		if est > out[i].Start {
			out[i].End = est
		}
	}
	return out
}

// maxAbsorbFrames is Jordan's threshold (2026-07-25): a word lasting this many
// video frames or less is absorbed into its nearest neighbour rather than left
// to stand alone — this is how cli-cut has always handled it.
const maxAbsorbFrames = 2

// fallbackAbsorbFPS is used only when the caller has not set a real frame rate
// (Options.FPS <= 0) — the project's own documented real rate.
const fallbackAbsorbFPS = 30000.0 / 1001.0

// absorbShortWords merges a word lasting <= maxAbsorbFrames into whichever
// neighbour is nearer, closing the gap between them to zero. Must run BEFORE
// ChunkWords: a word this short can be a rescued word clamped onto a cut edge
// (WordsPerSegment) or just Parakeet's own timing, and either way, once it is
// on its own, real speaker pauses around it make it its own chunk — a caption
// with ~0 duration that flashes invisibly even though its text is right there.
// Closing its gap to the nearer neighbour here means ChunkWords's own pause
// detection groups them before the 22-char cap ever sees the run, so the word
// lands on that neighbour's line instead of alone. One rule, nothing more.
func absorbShortWords(words []Word, fps float64) []Word {
	if len(words) < 2 {
		return words
	}
	if fps <= 0 {
		fps = fallbackAbsorbFPS
	}
	maxDur := maxAbsorbFrames / fps
	out := make([]Word, len(words))
	copy(out, words)
	for i := range out {
		if out[i].End-out[i].Start > maxDur {
			continue
		}
		hasPrev, hasNext := i > 0, i+1 < len(out)
		if !hasPrev && !hasNext {
			continue
		}
		toPrev := hasPrev && (!hasNext || out[i].Start-out[i-1].End <= out[i+1].Start-out[i].End)
		if toPrev {
			out[i].Start = out[i-1].End
		} else {
			out[i].End = out[i+1].Start
		}
	}
	return out
}

// ChunkWords is the deterministic pass-1 chunker. Two rules, in order: break at
// every real speaker pause (gap > gapSeconds), then split any pause-free run
// still longer than maxChars at its own biggest internal pauses
// (splitAtBiggestPause) rather than filling greedily up to the cap.
//
// It used to fill greedily with no lookahead, which was the root cause of the
// stranded one-word captions on Jordan's real edit ("media", "videos",
// "fundamentals", ...): whatever word landed after the cap became its own line
// even though nothing about the speech justified a break there. cli-cut's own
// rule for a cap-length line (its SKILL.md pass 2) is "Split at strong clause
// boundaries where the pause was suppressed by the 22-char limit" — and the
// deterministic stand-in for a clause boundary is the run's biggest pause,
// which is exactly what splitAtBiggestPause picks.
// breaksAfter reports whether a word ENDS a caption chunk. Jordan's cli-cut rule
// (2026-07-24): "? and ! are the only punctuation allowed; where a period or comma
// would be, a split occurs." So a word ending in ? ! . or , closes its line - the ?
// and ! stay (endsSentence), the . and , are dropped by normalize. This is WHERE to
// break; normalize decides what punctuation survives.
func breaksAfter(word string) bool {
	s := strings.TrimRight(strings.TrimSpace(word), `"'’”)]}`)
	return strings.HasSuffix(s, "?") || strings.HasSuffix(s, "!") ||
		strings.HasSuffix(s, ".") || strings.HasSuffix(s, ",")
}

// ChunkWords is the ONE deterministic caption chunker — Jordan's rules (2026-07-24),
// grounded in cli-cut's _chunk_words_pass1. Two clean passes:
//
//  1. PHRASE RUNS. Walk the words and close a run at every natural boundary: a real
//     speaker PAUSE (gap > gapSeconds — pace is rule #1), and any word ending in
//     ? ! . or , (? and ! are kept by normalize, . and , are dropped — a period or
//     comma is a split point, not a printed mark). Words with no pause and no
//     punctuation between them stay together — that is a phrase.
//
//  2. FIT THE CAP. A run longer than 22 chars is broken at its biggest INTERNAL
//     pause (splitToFit) — never a dumb character wrap — and no line is left ending
//     on a/the/to/and... (pushDanglers). 22 is a hard failsafe; a one-word line is
//     fine.
func ChunkWords(words []Word, maxChars int, gapSeconds float64) [][]Word {
	var runs [][]Word
	var cur []Word
	for _, w := range words {
		if strings.TrimSpace(w.Word) == "" {
			continue
		}
		if len(cur) > 0 && w.Start-cur[len(cur)-1].End > gapSeconds+gapEps {
			runs = append(runs, cur)
			cur = nil
		}
		cur = append(cur, w)
		if breaksAfter(w.Word) {
			runs = append(runs, cur)
			cur = nil
		}
	}
	if len(cur) > 0 {
		runs = append(runs, cur)
	}

	var chunks [][]Word
	for _, run := range runs {
		chunks = append(chunks, splitToFit(run, maxChars)...)
	}
	return pushDanglers(chunks, maxChars)
}

// Build turns kept source segments into output-timeline cues with cut-snapped,
// gap-free timing. Segments are laid end to end in the order given: segment i
// occupies [offset, offset+Dur) on the output timeline.
//
// A segment with no words contributes its duration to the timeline but emits no
// cue — silence stays silent, and the following segment still lands at the right
// offset.
func Build(segments []Segment, opt Options) []Cue {
	return stripWords(BuildWithWords(segments, opt))
}

// BuildWithWords is Build, but keeps each cue's constituent words (their own
// Start/End, lifted onto the output timeline) attached — see CueWords.
func BuildWithWords(segments []Segment, opt Options) []CueWords {
	perSeg := WordsPerSegment(segments)
	chunks := make([][][]Word, len(segments))
	for i := range segments {
		words := PrepareWords(perSeg[i], opt.FPS)
		chunks[i] = Pass1Chunks(words, opt.MaxChars, opt.GapSeconds)
	}
	return BuildFromChunksWithWords(segments, chunks, opt)
}

// BuildFromChunks is Build with the word grouping already decided — used when
// the LLM review pass (see llm.go) has regrouped the pass-1 chunks. The timing
// rules are identical; only where the lines break differs.
func BuildFromChunks(segments []Segment, chunksPerSeg [][][]Word, opt Options) []Cue {
	return stripWords(BuildFromChunksWithWords(segments, chunksPerSeg, opt))
}

// BuildFromChunksWithWords is BuildFromChunks, but returns CueWords instead of
// Cue — same pipeline, same timing rules, only the return type carries each
// cue's words along with it. The two are ONE implementation (buildFromChunks
// below); BuildFromChunks just strips the extra field, so the default
// (cli-cut) caption path is byte-identical to before this existed.
func BuildFromChunksWithWords(segments []Segment, chunksPerSeg [][][]Word, opt Options) []CueWords {
	return buildFromChunks(segments, chunksPerSeg, opt)
}

func stripWords(cws []CueWords) []Cue {
	out := make([]Cue, len(cws))
	for i, cw := range cws {
		out[i] = cw.Cue
	}
	return out
}

func buildFromChunks(segments []Segment, chunksPerSeg [][][]Word, opt Options) []CueWords {
	// Phase 1: per segment, chunk its words into caption-local times.
	type built struct {
		offset float64
		dur    float64
		cues   []CueWords // Cue times are LOCAL to the segment (0..dur); Words are already output-absolute
	}
	prepared := make([]built, 0, len(segments))

	var offset float64
	for si, seg := range segments {
		dur := seg.Dur()
		b := built{offset: offset, dur: dur}

		var segChunks [][]Word
		if si < len(chunksPerSeg) {
			segChunks = joinCollidingChunks(chunksPerSeg[si], opt.MaxChars)
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
			if localStart > dur {
				localStart = dur // a cue can never begin after its own cut
			}
			if localEnd > dur {
				localEnd = dur
			}
			if localEnd < localStart {
				localEnd = localStart
			}
			words := make([]string, 0, len(chunk))
			outWords := make([]Word, 0, len(chunk))
			for _, w := range chunk {
				words = append(words, strings.TrimSpace(w.Word))
				ow := w
				ow.Start = b.offset + clampToSpan(w.Start, seg.Start, seg.End) - seg.Start
				ow.End = b.offset + clampToSpan(w.End, seg.Start, seg.End) - seg.Start
				outWords = append(outWords, ow)
			}
			text := normalize(strings.Join(words, " "), opt.Lowercase)
			if text == "" {
				continue
			}
			b.cues = append(b.cues, CueWords{Cue: Cue{Start: localStart, End: localEnd, Text: text}, Words: outWords})
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
	var out []CueWords
	for i, b := range prepared {
		if len(b.cues) == 0 {
			continue
		}
		cues := make([]CueWords, len(b.cues))
		for j, c := range b.cues {
			cues[j] = CueWords{Cue: Cue{Start: b.offset + c.Start, End: b.offset + c.End, Text: c.Text}, Words: c.Words}
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

		// A CUT ENDS THE CHUNK (Jordan 2026-07-24: "when there's a cut, that's also
		// the end of the chunk"). Captions never span a cut — each segment's chunks
		// stay as they are. (This used to fold the next segment's first caption back
		// into the previous one when speech ran on; that merge is gone.)
		out = append(out, cues...)
	}
	// Clamp AFTER quantising: rounding two boundaries to frames can itself put a
	// caption a frame past the next one's start. Words are not re-timed by this —
	// see CueWords — so run these on the Cue half only, then re-attach.
	timed := make([]Cue, len(out))
	for i, cw := range out {
		timed[i] = cw.Cue
	}
	timed = ensureVisible(clampOverlaps(QuantizeToFrames(timed, opt.FPS)), opt.FPS)
	for i := range out {
		out[i].Cue = timed[i]
	}
	return out
}

// joinCollidingChunks merges a chunk into the previous one when the two begin at
// the SAME instant, as long as the joined line still fits the cap.
//
// Two captions cannot share a start time: the gap-fill gives the earlier one a
// span of zero and it is never drawn — the word is simply missing from the video,
// which is indistinguishable from the cut eating it. It happens because Parakeet
// hands back several words stamped at the identical time (its token timings
// collapse, see estimateMissingEnds), so two chunks legitimately start together.
// Jordan's IMG_9624 edit shipped 2 of these in 66 cues ("you", "and").
// Joining them is also the better caption: "you" + "keep scrolling" reads as
// "you keep scrolling", one line, 18 characters.
func joinCollidingChunks(chunks [][]Word, maxChars int) [][]Word {
	out := make([][]Word, 0, len(chunks))
	for _, c := range chunks {
		if len(c) == 0 {
			continue
		}
		if n := len(out); n > 0 {
			prev := out[n-1]
			if len(prev) > 0 && c[0].Start <= prev[0].Start+1e-9 {
				joined := append(append([]Word{}, prev...), c...)
				if maxChars <= 0 || lineLen(joined) <= maxChars {
					out[n-1] = joined
					continue
				}
			}
		}
		out = append(out, c)
	}
	return out
}

// ensureVisible is the last-resort floor: a caption that survived every timing
// rule with less than one frame on screen borrows a frame from the next one.
// A caption nobody can read is the same as a lost word, and losing words is the
// whole complaint this pass exists to end. Never crushes the next caption below
// a frame - if there is no room, the timing stands rather than cascade damage.
func ensureVisible(cues []Cue, fps float64) []Cue {
	if fps <= 0 || len(cues) == 0 {
		return cues
	}
	frame := 1 / fps
	for i := range cues {
		if cues[i].End-cues[i].Start >= frame-1e-9 {
			continue
		}
		want := cues[i].Start + frame
		if i+1 < len(cues) {
			if cues[i+1].End-want < frame-1e-9 {
				continue // no room next door
			}
			cues[i+1].Start = want
		}
		cues[i].End = want
	}
	return cues
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

// normalize applies the caption look: only ? and ! survive as punctuation (Jordan
// 2026-07-24) — a period, comma, semicolon or colon is a SPLIT point in chunking,
// never a printed mark, so strip them everywhere. Collapse whitespace, and
// (optionally) lowercase.
func normalize(s string, lower bool) string {
	s = strings.Map(func(r rune) rune {
		switch r {
		case '.', ',', ';', ':':
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
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
