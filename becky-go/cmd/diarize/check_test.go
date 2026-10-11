package main

import (
	"testing"

	"becky-go/internal/voiceprint"
)

func clips(spk string, start float64, n int, v []float64) []chunk {
	var out []chunk
	for i := 0; i < n; i++ {
		t := start + float64(i)*3
		out = append(out, chunk{span: voiceprint.Span{Start: t, End: t + 3}, print: voiceprint.Normalize(v)})
	}
	return out
}

func TestDecideMergesOneVoiceAndSplitsTwo(t *testing.T) {
	jordan, other := []float64{1, 0.05, 0}, []float64{0, 1, 0.1}
	never := func(a, b []voiceprint.Span) (string, string) { t.Fatal("no look needed"); return "", "" }

	// one person, two labels -> merged into the one with more speech
	segs := []flatSegment{{0, 30, "SPEAKER_00"}, {30, 42, "SPEAKER_01"}}
	ch := map[string][]chunk{"SPEAKER_00": clips("SPEAKER_00", 0, 10, jordan), "SPEAKER_01": clips("SPEAKER_01", 30, 4, jordan)}
	got, checks, _ := decide(segs, []string{"SPEAKER_00", "SPEAKER_01"}, ch, never)
	if got[1].Speaker != "SPEAKER_00" || len(checks) != 1 || checks[0].Action != "merged" {
		t.Fatalf("merge: %+v %+v", got, checks)
	}

	// two people, one label -> the smaller voice gets its own label
	var segs2 []flatSegment
	var cs []chunk
	for i := 0; i < 10; i++ {
		v := jordan
		if i >= 6 {
			v = other
		}
		s := float64(i) * 3
		segs2 = append(segs2, flatSegment{s, s + 3, "SPEAKER_00"})
		cs = append(cs, chunk{span: voiceprint.Span{Start: s, End: s + 3}, print: voiceprint.Normalize(v)})
	}
	got, checks, _ = decide(segs2, []string{"SPEAKER_00"}, map[string][]chunk{"SPEAKER_00": cs}, never)
	if got[0].Speaker != "SPEAKER_00" || got[9].Speaker != "SPEAKER_01" || checks[0].Action != "split" {
		t.Fatalf("split: %+v %+v", got, checks)
	}
}

func TestDecideLooksWhenUnsure(t *testing.T) {
	a, b := []float64{1, 0}, []float64{0.6, 0.8} // cosine 0.6: between the merge bars
	segs := []flatSegment{{0, 30, "SPEAKER_00"}, {30, 42, "SPEAKER_01"}}
	ch := map[string][]chunk{"SPEAKER_00": clips("SPEAKER_00", 0, 10, a), "SPEAKER_01": clips("SPEAKER_01", 30, 4, b)}
	looked := 0
	_, checks, _ := decide(segs, []string{"SPEAKER_00", "SPEAKER_01"}, ch, func(x, y []voiceprint.Span) (string, string) {
		looked++
		return "unsure", "Gemma-4 E4B"
	})
	if looked != 1 || checks[0].Action != "kept" || checks[0].Looked != "unsure (Gemma-4 E4B)" {
		t.Fatalf("look: %d %+v", looked, checks)
	}
}
