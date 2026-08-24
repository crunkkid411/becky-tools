package main

import (
	"math"
	"testing"

	"becky-go/internal/quotes"
)

// The bad-take detector is asserted on the SHAPES Jordan described in
// WE_TRIED.md, not on invented prose: stop-pause-restart, restarts that begin
// several words in, re-wordings, and chains of chains.

func cue(start, end float64, text string) quotes.Cue {
	return quotes.Cue{Start: start, End: end, Text: text}
}

func TestDetectsSimpleRestartChain(t *testing.T) {
	cues := []quotes.Cue{
		cue(0, 3, "So the FBI opened a file on him last spring"),
		cue(5, 8, "So the FBI opened a file on him last spring and"),
		cue(10, 16, "So the FBI opened a file on him last spring and nobody told us"),
	}
	got := detectBadTakes(cues)
	if len(got) != 1 {
		t.Fatalf("got %d bad takes, want 1: %+v", len(got), got)
	}
	b := got[0]
	if !b.Confident {
		t.Errorf("exact 5x word-run restart must be confident, got %+v", b)
	}
	if math.Abs(b.Start-0) > 0.01 || math.Abs(b.End-(10-0.05)) > 0.01 {
		t.Errorf("chain span = [%v,%v], want [0,9.95]", b.Start, b.End)
	}
	if b.LastCue != 1 {
		t.Errorf("LastCue = %d, want 1 (both abandoned attempts cut)", b.LastCue)
	}
}

func TestRestartBeginningSeveralWordsIn(t *testing.T) {
	cues := []quotes.Cue{
		cue(0, 4, "and at one point she said the whole thing was a joke"),
		cue(6, 10, "she said the whole thing was a joke but then"),
	}
	got := detectBadTakes(cues)
	if len(got) != 1 {
		t.Fatalf("mid-sentence re-start must be flagged, got %+v", got)
	}
	if got[0].Confident {
		t.Errorf("two complete deliveries, no abandonment signal: human decides, got %+v", got[0])
	}
}

func TestRewordingGoesToMarkerNotCut(t *testing.T) {
	cues := []quotes.Cue{
		cue(0, 4, "the restraining order was filed in Elkhart"),
		cue(6, 10, "the restraining order got filed last month"),
	}
	got := detectBadTakes(cues)
	if len(got) != 1 {
		t.Fatalf("re-wording should still be flagged, got %+v", got)
	}
	if got[0].Confident {
		t.Errorf("3-word re-worded restart must NOT be auto-cut: %+v", got[0])
	}
	// Two clean complete alternates of the same statement: flagged, and the
	// human picks in the morning.
	cut := []quotes.Cue{
		cue(0, 4, "and for a little while she thought it was funny"),
		cue(6, 10, "and at one point she thought it was funny"),
	}
	got = detectBadTakes(cut)
	if len(got) != 1 || got[0].Confident {
		t.Fatalf("two clean alternates must be a marker, not a cut: %+v", got)
	}
}

func TestObviousTwoTakeRetakeIsCut(t *testing.T) {
	// Jordan 2026-08-24: "there are absolutely times when 2 takes exist because
	// one is an obvious re-take" - the first attempt dies mid-sentence, he
	// pauses, then delivers the fuller version. That one is NOT his homework.
	cues := []quotes.Cue{
		cue(0, 2, "so the restraining order"),
		cue(4, 9, "so the restraining order was filed in Elkhart county"),
	}
	got := detectBadTakes(cues)
	if len(got) != 1 {
		t.Fatalf("obvious retake must be detected, got %+v", got)
	}
	if !got[0].Confident {
		t.Errorf("abandoned first take + fuller re-delivery must CUT: %+v", got[0])
	}
	if got[0].LastCue != 0 {
		t.Errorf("only the abandoned attempt is cut, got %+v", got[0])
	}
}

func TestNoCutWithoutPauseOrWithoutMatch(t *testing.T) {
	// No pause: continuous speech that repeats words is emphasis, not a retake.
	noPause := []quotes.Cue{
		cue(0, 3, "that is the fact that is the fact"),
		cue(3.2, 6, "that is the fact that is the fact"),
	}
	if got := detectBadTakes(noPause); len(got) != 0 {
		t.Errorf("no-pause repetition must not cut: %+v", got)
	}
	// Pause but unrelated sentences: nothing to cut.
	unrelated := []quotes.Cue{
		cue(0, 3, "the camera battery died twice"),
		cue(5, 8, "so the FBI opened a file on him"),
	}
	if got := detectBadTakes(unrelated); len(got) != 0 {
		t.Errorf("unrelated cues must not match: %+v", got)
	}
}

func TestNeverExtendsBackwardsAcrossCompletedSentence(t *testing.T) {
	cues := []quotes.Cue{
		cue(0, 3, "I want to be clear about one thing."),
		cue(4, 7, "the restraining order was filed in Elkhart"),
		cue(9, 13, "the restraining order was filed in Elkhart county"),
	}
	got := detectBadTakes(cues)
	if len(got) != 1 {
		t.Fatalf("want 1, got %+v", got)
	}
	if math.Abs(got[0].Start-4) > 0.01 {
		t.Errorf("cut must start at the abandoned attempt (4s), not bleed into the finished sentence: %v", got[0].Start)
	}
}

// --- zero-crossing snap -----------------------------------------------------

// silenceThenTone builds mono 16k audio: quietSec of near-silence then a sine
// burst. The keep boundary sits at the silence/tone edge.
func silenceThenTone(quietSec, toneSec float64, rate int, toneAmp float64) []float32 {
	n := int((quietSec + toneSec) * float64(rate))
	s := make([]float32, n)
	for i := 0; i < n; i++ {
		t := float64(i) / float64(rate)
		if t < quietSec {
			s[i] = 0.001 * float32(math.Sin(2*math.Pi*50*t)) // -60dBFS room tone
		} else {
			s[i] = float32(toneAmp * math.Sin(2*math.Pi*200*(t-quietSec)))
		}
	}
	return s
}

func TestSnapFindsQuietCrossingNearBoundary(t *testing.T) {
	rate := 16000
	s := silenceThenTone(1.0, 1.0, rate, 0.3)
	// ask 5ms INSIDE the tone: the snap must walk back to a crossing in the
	// quiet part, within the 20ms window.
	got, ok := snapBoundary(s, rate, 1.005, -40)
	if !ok {
		t.Fatal("expected a quiet crossing within the window")
	}
	if got > 1.001 || got < 1.005-0.021 {
		t.Errorf("snapped to %v, want within [0.984,1.001]", got)
	}
}

func TestSnapLeavesBoundaryWhenNoQuietNearby(t *testing.T) {
	rate := 16000
	n := rate * 2
	s := make([]float32, n)
	for i := range s { // continuous loud tone, no quiet anywhere
		s[i] = float32(0.5 * math.Sin(2*math.Pi*200*float64(i)/float64(rate)))
	}
	got, ok := snapBoundary(s, rate, 1.0, -40)
	if ok || got != 1.0 {
		t.Errorf("loud audio must leave the boundary untouched, got %v,%v", got, ok)
	}
}

func TestSnapExtendsToQuietPocket(t *testing.T) {
	rate := 16000
	// tone everywhere except a quiet pocket 0.1s after the boundary.
	n := rate * 2
	s := make([]float32, n)
	for i := range s {
		t := float64(i) / float64(rate)
		if t > 1.1 && t < 1.3 {
			s[i] = 0.0005
		} else {
			s[i] = float32(0.4 * math.Sin(2*math.Pi*200*t))
		}
	}
	got, ok := snapBoundary(s, rate, 1.0, -40)
	if !ok {
		t.Fatal("expected extension to the quiet pocket")
	}
	if got < 1.09 || got > 1.31 {
		t.Errorf("extended to %v, want inside the quiet pocket [1.09,1.31]", got)
	}
}
