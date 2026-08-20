// wordrescue.go — a jumpcut that deletes a WORD is wrong by construction.
//
// becky-cut's silence threshold is ABSOLUTE (auto-editor's is), and cmd/cut
// already adapts it to a file's mean level. That adaptation cannot serve a file
// whose level moves, and Jordan's raw camera footage moves a lot. MEASURED on
// test-for-clips.mp4:
//
//	whole file       mean -25.2 dBFS  -> becky-cut threshold -24.2 dB
//	window at   30s  mean -21.7 dBFS  (above the threshold, kept)
//	window at  120s  mean -30.9 dBFS  (6.7 dB under it)
//	window at  240s  mean -56.5 dBFS  (32 dB under it)
//
// A 35 dB swing against one global threshold. On the 120s window becky-cut kept
// 4.4 of 30 seconds while becky-transcribe found **11.0 seconds of actual
// speech** in it — six and a half seconds of Jordan talking, deleted, on footage
// ffmpeg's own silencedetect reports as having NO silence at all.
//
// Chasing a better threshold is the wrong fix, because there isn't one number
// that serves both his polished YouTube audio (-16.9 dBFS mean, speech near the
// mean) and quiet raw camera footage (speech in brief peaks far above a low
// mean). becky already knows something better than any threshold: it has
// WORD-LEVEL TIMINGS for the whole file. A span that carries a word is not
// silence, whatever the level says.
//
// So the silence pass keeps its job — it decides where the dead air is — and
// this puts back anything it took that had a word in it.
package main

import (
	"sort"

	"becky-go/internal/subs"
)

// wordRescuePad is how much room each rescued word gets either side, in
// seconds. Enough that the word's own attack and release survive rather than
// being clipped to a stub, and small enough that it does not drag silence back
// in with it. becky-cut's own default margin is "0.04s,0.25s" — the same order.
const wordRescuePad = 0.12

// rescueWords returns spans covering everything in `spans` PLUS every word in
// [in,out] that those spans missed, merged and sorted. rescued is how many
// words had to be put back — zero means the silence pass already agreed with
// the transcript and nothing changed.
//
// Pure, so the whole rule is testable without ffmpeg, auto-editor or audio.
func rescueWords(spans []keepSpan, words []subs.Word, in, out, pad float64) (merged []keepSpan, rescued int) {
	covered := func(t float64) bool {
		for _, s := range spans {
			if t >= s.In && t <= s.Out {
				return true
			}
		}
		return false
	}

	out2 := append([]keepSpan(nil), spans...)
	for _, w := range words {
		if w.End <= in || w.Start >= out {
			continue // not in this window at all
		}
		// A word counts as kept only if BOTH ends survived. A span boundary
		// through the middle of a word is exactly the clipped-stub case.
		if covered(w.Start) && covered(w.End) {
			continue
		}
		s := keepSpan{In: w.Start - pad, Out: w.End + pad}
		if s.In < in {
			s.In = in
		}
		if s.Out > out {
			s.Out = out
		}
		if s.Out > s.In {
			out2 = append(out2, s)
			rescued++
		}
	}
	return mergeSpans(out2), rescued
}

// mergeSpans sorts and unions overlapping or touching spans.
func mergeSpans(spans []keepSpan) []keepSpan {
	if len(spans) == 0 {
		return nil
	}
	s := append([]keepSpan(nil), spans...)
	sort.Slice(s, func(i, j int) bool { return s[i].In < s[j].In })
	out := []keepSpan{s[0]}
	for _, cur := range s[1:] {
		last := &out[len(out)-1]
		if cur.In <= last.Out {
			if cur.Out > last.Out {
				last.Out = cur.Out
			}
			continue
		}
		out = append(out, cur)
	}
	return out
}

// spansDuration is the total time a span list keeps.
func spansDuration(spans []keepSpan) float64 {
	var d float64
	for _, s := range spans {
		d += s.Out - s.In
	}
	return d
}
