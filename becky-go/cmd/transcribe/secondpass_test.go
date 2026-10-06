package main

import (
	"math"
	"strings"
	"testing"
)

func fp(v float64) *float64 { return &v }

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestFillTimesInterpolatesUnalignedWords(t *testing.T) {
	ws := []wxWord{
		{Word: "it's", Start: nil, End: nil},
		{Word: "year", Start: fp(1.0), End: fp(1.4), Score: fp(0.9)},
		{Word: "2007", Start: nil, End: nil},
		{Word: "now", Start: fp(2.0), End: fp(2.2)},
		{Word: "!", Start: nil, End: nil},
	}
	got := fillTimes(ws)
	if len(got) != 5 {
		t.Fatalf("want 5 words, got %d", len(got))
	}
	if !near(got[0].Start, 1.0) || !near(got[2].Start, 1.7) || !near(got[4].Start, 2.2) {
		t.Fatalf("interpolation wrong: %+v", got)
	}
	if !got[1].HasScore || got[2].HasScore || !near(got[1].Score, 0.9) {
		t.Fatalf("scores wrong: %+v", got)
	}
	if fillTimes([]wxWord{{Word: "x"}}) != nil {
		t.Fatal("no timed word at all must give nil")
	}
}

func TestUncoveredRunsFindsOnlyMissedPassages(t *testing.T) {
	p := []Word{{Word: "hello", Start: 0, End: 1}, {Word: "there", Start: 5, End: 6}}
	w := []timedWord{
		{Word: "hello", Start: 0.1, End: 0.9},
		{Word: "I've", Start: 2.0, End: 2.3},
		{Word: "got", Start: 2.4, End: 2.8},
		{Word: "it", Start: 2.9, End: 3.2},
		{Word: "there", Start: 5.1, End: 5.5},
		{Word: "um", Start: 7.0, End: 7.2}, // a lone miss is filler noise
		{Word: "far", Start: 9.0, End: 9.2},
		{Word: "apart", Start: 10.5, End: 10.8}, // > runMaxGap from "far"
	}
	runs := uncoveredRuns(p, w)
	if len(runs) != 1 || runText(runs[0]) != "I've got it" {
		t.Fatalf("want one run \"I've got it\", got %d: %v", len(runs), runs)
	}
}

func TestStartOffsetMeasuresWhisperXLag(t *testing.T) {
	p := []Word{{Word: "Hello,", Start: 1.0}, {Word: "world", Start: 2.0}, {Word: "again", Start: 3.0}, {Word: "a", Start: 3.5}}
	w := []timedWord{{Word: "hello", Start: 1.08}, {Word: "world.", Start: 2.08}, {Word: "again", Start: 3.08}, {Word: "a", Start: 3.9}}
	med, n, agree := startOffset(p, w)
	if !near(med, 0.08) || n != 3 || !near(agree, 100) {
		t.Fatalf("got median %v n %d agree %v", med, n, agree)
	}
}

func TestMergeSecondPassCorroborateThenConclude(t *testing.T) {
	// Parakeet heard "one two three" at 0-3 s and "nine ten" at 20-21 s.
	p := []Word{
		{Word: "one", Start: 0, End: 0.5}, {Word: "two", Start: 1, End: 1.5}, {Word: "three", Start: 2, End: 2.5},
		{Word: "nine", Start: 20, End: 20.4}, {Word: "ten", Start: 20.6, End: 21},
	}
	// WhisperX hears the same words 0.1 s late, plus three passages Parakeet missed.
	w := []timedWord{
		{Word: "one", Start: 0.1, End: 0.6}, {Word: "two", Start: 1.1, End: 1.6}, {Word: "three", Start: 2.1, End: 2.6},
		{Word: "got", Start: 5.1, End: 5.3, Score: 0.6, HasScore: true}, {Word: "lot", Start: 5.4, End: 5.7, Score: 0.6, HasScore: true}, // A: second look hears it
		{Word: "cherry", Start: 10.1, End: 10.5, Score: 0.8, HasScore: true}, {Word: "blair", Start: 10.6, End: 10.9, Score: 0.7, HasScore: true}, // B: strong aligner match
		{Word: "need", Start: 15.1, End: 15.3, Score: 0.2, HasScore: true}, {Word: "some", Start: 15.4, End: 15.6, Score: 0.2, HasScore: true}, // C: weak, alone
		{Word: "nine", Start: 20.1, End: 20.5}, {Word: "ten", Start: 20.7, End: 21.1},
	}
	recheck := func(spans [][2]float64) ([][]Word, error) {
		if len(spans) != 3 {
			t.Fatalf("second look should get 3 spans, got %v", spans)
		}
		return [][]Word{
			{{Word: "two", Start: 1, End: 1.5}, {Word: "got", Start: 5.0, End: 5.2}, {Word: "lot", Start: 5.3, End: 5.6}}, // "two" is a duplicate
			nil,
			nil,
		}, nil
	}
	got, sp := mergeSecondPass(p, w, recheck)
	if !sp.Ran || sp.OffsetMS != 100 || sp.MatchedWords != 5 {
		t.Fatalf("audit wrong: %+v", sp)
	}
	if len(sp.Recovered) != 2 || sp.Recovered[0].Source != "parakeet-recheck" || sp.Recovered[1].Source != "whisperx" {
		t.Fatalf("recovered wrong: %+v", sp.Recovered)
	}
	if len(sp.Unconfirmed) != 1 || sp.Unconfirmed[0].Text != "need some" {
		t.Fatalf("unconfirmed wrong: %+v", sp.Unconfirmed)
	}
	text := wordsText(got)
	if text != "one two three got lot cherry blair nine ten" {
		t.Fatalf("merged text wrong: %q", text)
	}
	for _, x := range got {
		if x.Word == "cherry" && (!near(x.Start, 10.0) || x.Source != "whisperx") {
			t.Fatalf("whisperx word must be shifted onto Parakeet's clock: %+v", x)
		}
		if x.Word == "got" && x.Source != "parakeet-recheck" {
			t.Fatalf("recheck word must say so: %+v", x)
		}
		if x.Word == "one" && x.Source != "" {
			t.Fatalf("main-pass words carry no source: %+v", x)
		}
	}
}

func TestMergeSecondPassWithoutSecondLook(t *testing.T) {
	p := []Word{{Word: "a", Start: 0, End: 0.2}}
	w := []timedWord{{Word: "lost", Start: 3, End: 3.2, Score: 0.9, HasScore: true}, {Word: "words", Start: 3.3, End: 3.6, Score: 0.9, HasScore: true}}
	got, sp := mergeSecondPass(p, w, nil)
	if len(got) != 3 || len(sp.Recovered) != 1 || sp.Recovered[0].Source != "whisperx" {
		t.Fatalf("got %+v / %+v", got, sp)
	}
}

func TestWhisperXArgsAreJordansSettings(t *testing.T) {
	got := strings.Join(whisperXArgs("a.wav", "en", "out"), " ")
	for _, want := range []string{"--model large-v2", "--align_model WAV2VEC2_ASR_LARGE_LV60K_960H",
		"--compute_type float16", "--vad_method silero", "--vad_onset 0.1", "--vad_offset 0.1",
		"--interpolate_method linear", "--output_format json"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
}

func TestCleanEnvDropsUserSiteVars(t *testing.T) {
	got := cleanEnv([]string{"PATH=x", "PYTHONUSERBASE=X:\\p", "pip_target=y", "HOME=h"}, "PYTHONUSERBASE", "PIP_TARGET")
	if strings.Join(got, ";") != "PATH=x;HOME=h" {
		t.Fatalf("got %v", got)
	}
}
