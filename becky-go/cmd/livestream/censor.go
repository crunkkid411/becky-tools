package main

// censor.go - --video: every "fuck" he says gets his censor sound. Jordan,
// 2026-10-14 (user-feedback.md, vegas\censor-example.JPG): "place this sound
// effect ... overtop of the audio where I say 'fuck' ... one video frame after
// the word 'fuck' is spoken so that only a very brief 'f' sound is heard ...
// Shorten the sound effect so that one video frame of the end of the word is
// also audibly heard ... Lower the Gain on that specific Censor 2.mp3 sound
// effect to -12.1 dB". In his example the censor sits ON his speech track, in
// a gap cut out of his speech (BeckyFX.cs "bleep").
//
// The transcript only says WHICH word: its times are on an 80 ms grid and ran
// 5 frames early on his example ("fucking" 194.01 s; he marked 194.167), and
// on "Go fuck yourself" it put "Go" where "fuck" is. The word's edges come from
// the audio, the way he finds them by eye. It starts where its vowel starts,
// right after the "f": a hiss (noise, measured as its share of high pitch) in
// front of a vowel - 13 of the goodbye update's 14; the 14th, a soft "f", by
// the near-silence before its vowel. It ends where its last vowel ends: "fuck"
// and "fucked" stop on a "k", where the sound drops 15 dB; "fucking" goes on
// past the "k" into a second, short vowel that ends where it drops 4 dB (into
// "ng" or the next word). All 14 checked by ear-equivalent on 2026-10-14: the
// censored audio re-transcribed, each censor inside its word.

import (
	"math"
	"regexp"
	"strings"
)

// censorSound is Jordan's censor sound effect.
const (
	censorSound = `X:\Videos\Video Editing Assets\1 - HAIR JORDAN SFX\Censor 2.mp3`
	censorGain  = -12.1 // dB, on the censor event only
)

var swear = regexp.MustCompile(`(?i)fuck`)

// syllables counts a word's vowel groups ("fucked" is one: its -ed is a "t").
func syllables(w string) int {
	w = strings.ToLower(strings.Trim(w, ".,!?;:-\"' "))
	w = strings.TrimSuffix(w, "s")
	if strings.HasSuffix(w, "ked") || strings.HasSuffix(w, "ed") && !strings.HasSuffix(w, "ted") && !strings.HasSuffix(w, "ded") {
		w = strings.TrimSuffix(w, "ed") + "d"
	}
	n, in := 0, false
	for _, r := range w {
		v := strings.ContainsRune("aeiouy", r)
		if v && !in {
			n++
		}
		in = v
	}
	return max(n, 1)
}

const (
	envStep  = 0.01 // the audio is read every 10 ms
	hissMin  = -5.0 // an "f": its high-pitch share is at least this (his vowels: -9 to -20 dB)
	hissRun  = 3    // ... for 30 ms or more (a "k" or "g" burst is 10-20 ms)
	vowelUp  = 6.0  // the vowel after the "f" is 6 dB louder within 30 ms
	stopDrop = 15.0 // "fuck"/"fucked" end on a "k"/"t": 15 dB under the vowel
	tailDrop = 4.0  // the short last vowel of "fucking" ends where it falls 4 dB
	// The transcript's start runs 0.01-0.36 s early on all 14 of the goodbye
	// update, so the vowel starts no earlier than this before it (the "s" of
	// "so" in "so fucked" starts its vowel 0.08 s before).
	notBefore = 0.05
	notAfter  = 0.45
	kClosed   = 0.18 // the "k" of "fucking" closes within this of the vowel (80-130 ms measured)
)

// hissDB is how much of the sound is high-pitched noise, in dB: the level of
// the sample-to-sample change against the level (an "f" about 0, a vowel -10).
func (a *audio) hissDB(t, d float64) float64 {
	lo, hi := int(t*float64(a.sr)), int((t+d)*float64(a.sr))
	lo, hi = max(lo, 1), min(hi, len(a.pcm))
	var s, ds float64
	for i := lo; i < hi; i++ {
		v, dv := float64(a.pcm[i]), float64(a.pcm[i])-float64(a.pcm[i-1])
		s, ds = s+v*v, ds+dv*dv
	}
	return 10 * math.Log10((ds+1e-9)/(s+1e-9))
}

// wordEdges finds an f-word's start and end in the audio, on the frame grid.
// floor is the recording's noise floor. ok=false when no "f" is found.
func wordEdges(au *audio, w Word, floor, fps float64) (float64, float64, bool) {
	lvl := func(t float64) float64 { return au.levelDB(t, envStep) }
	vowel := func(t float64) float64 { return math.Max(math.Max(lvl(t), lvl(t+envStep)), lvl(t+2*envStep)) }
	lo, hi := w.Start-notBefore, w.Start+notAfter
	// the start: the first "f" (hiss, 30 ms or more, above the room noise)
	// with a vowel right after it; the word starts where the vowel does
	start, run, runMax := -1.0, 0, -120.0
	for k := 0; start < 0; k++ {
		t := w.Start - 0.15 + float64(k)*envStep
		if t > hi {
			break
		}
		if au.hissDB(t, envStep) >= hissMin {
			run, runMax = run+1, math.Max(runMax, lvl(t))
			continue
		}
		if t >= lo && run >= hissRun && runMax >= floor+6 && vowel(t) >= runMax+vowelUp {
			start = t
		}
		run, runMax = 0, -120
	}
	// a soft "f" with no clear hiss ("want to fucking live"): the first jump
	// out of a near-silence 20 dB under the vowel that follows it
	for k := 0; start < 0 && lo+float64(k)*envStep <= hi; k++ {
		t := lo + float64(k)*envStep
		v := vowel(t)
		if lvl(t)-lvl(t-envStep) >= vowelUp && lvl(t-envStep) <= v-20 && lvl(t-2*envStep) <= v-20 {
			start = t
		}
	}
	if start < 0 {
		return 0, 0, false
	}
	// the end: its first vowel stops on the "k", where the sound drops; each
	// later vowel ("-ing", "-er") starts again and ends where it falls off
	peak, peakAt := -120.0, start
	for k := 0; k < 12; k++ {
		if l := lvl(start + float64(k)*envStep); l > peak {
			peak, peakAt = l, start+float64(k)*envStep
		}
	}
	t, end := peakAt, -1.0
	for ; t < peakAt+0.4; t += envStep {
		if lvl(t) <= peak-stopDrop {
			end = t
			break
		}
	}
	m := swear.FindStringIndex(w.Word)
	need := syllables(w.Word[m[0]:])
	if end-start > kClosed {
		need = 1 // the "k" never closed ("fuckin'" in one breath): that drop ends the word
	}
	for v := 1; end >= 0 && v < need; v++ {
		onset := -1.0
		for ; t < end+0.25; t += envStep {
			if lvl(t) >= peak-12 {
				onset = t
				break
			}
		}
		if onset < 0 {
			break // no next vowel: the word ended on the "k"
		}
		top := -120.0
		end = -1
		for ; t < onset+0.4; t += envStep {
			top = math.Max(top, lvl(t))
			if lvl(t) <= top-tailDrop || lvl(t) <= peak-stopDrop {
				end = t
				break
			}
		}
	}
	if end < 0 {
		end = start + 0.12*float64(need)
	}
	// the frame the vowel starts in; the frame line nearest the end (half up)
	a, b := math.Floor(start*fps+1e-6)/fps, math.Floor(end*fps+0.5+1e-6)/fps
	return a, b, b > a
}

// bleep is one censor on the timeline, in frames.
type bleep struct {
	F0, Len int64
	Word    string
	At      float64 // source seconds, for the report
}

// censorJob places a censor on every kept f-word: from one frame after the
// word starts to one frame before it ends. A quick "fuck" is only 3 frames
// long; its censor still gets 2, running into the silent "k" (one frame would
// leave the word plain to hear).
func censorJob(words []Word, keep []bool, au *audio, ps []piece, fps float64) []bleep {
	var out []bleep
	floor := au.noiseFloor()
	for i, w := range words {
		if !keep[i] || !swear.MatchString(w.Word) {
			continue
		}
		a, b, ok := wordEdges(au, w, floor, fps)
		if !ok {
			a, b = math.Round(w.Start*fps)/fps, math.Round(w.End*fps)/fps
		}
		t0, t1, on := toTimeline(ps, a, b)
		if !on || t1-t0 < (b-a)-1e-3 { // the whole word must be in the edit
			continue
		}
		f0, f1 := int64(math.Round(t0*fps))+1, int64(math.Round(t1*fps))-1
		out = append(out, bleep{F0: f0, Len: max(f1-f0, 2), Word: w.Word, At: a})
	}
	return out
}
