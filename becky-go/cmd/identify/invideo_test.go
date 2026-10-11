package main

import (
	"reflect"
	"strings"
	"testing"
)

func lab(id string, sim, talk float64, emb ...float64) labelScore {
	return labelScore{ID: id, Sim: sim, Talk: talk, Emb: emb}
}

func TestDecideInVideo(t *testing.T) {
	a, b := []float64{1, 0}, []float64{0, 1} // two different voices
	cases := []struct {
		name   string
		scores []labelScore
		pick   *contextPick
		owners []string
		why    string
	}{
		{"one talker: the old bars decide", []labelScore{lab("S0", 0.6, 30, a...), lab("S1", 0.5, 1, b...)}, nil, nil, ""},
		{"clearly closest of two", []labelScore{lab("S0", 0.36, 24, a...), lab("S1", 0.1, 7, b...)}, nil, []string{"S0"}, ""},
		{"two different voices equally close: nobody", []labelScore{lab("S0", 0.81, 60, a...), lab("S1", 0.78, 60, b...)}, nil, nil, "sound about equally"},
		{"one voice split across two labels", []labelScore{lab("S0", 0.70, 60, 1, 0.1), lab("S1", 0.68, 60, 1, 0.12), lab("S2", 0.2, 60, b...)}, nil, []string{"S0", "S1"}, ""},
		{"nobody sounds like them", []labelScore{lab("S0", 0.2, 60, a...), lab("S1", 0.1, 60, b...)}, nil, nil, "no speaker"},
		{"the words decide a close call", []labelScore{lab("S0", 0.81, 60, a...), lab("S1", 0.78, 60, b...)},
			&contextPick{Labels: []string{"S1"}, Confidence: "high"}, []string{"S1"}, ""},
		{"a clear voice beats the words, and the dispute is checked", []labelScore{lab("S0", 0.80, 60, a...), lab("S1", 0.40, 60, b...)},
			&contextPick{Labels: []string{"S1"}, Confidence: "high"}, []string{"S0"}, "sounds clearly more like"},
		{"a low-confidence read is ignored", []labelScore{lab("S0", 0.81, 60, a...), lab("S1", 0.78, 60, b...)},
			&contextPick{Labels: []string{"S1"}, Confidence: "low"}, nil, "sound about equally"},
	}
	for _, c := range cases {
		v := decideInVideo(c.scores, c.pick, 0.30, "redimnet")
		if !reflect.DeepEqual(v.Owners, c.owners) || (c.why != "" && !strings.Contains(v.Why, c.why)) {
			t.Errorf("%s: got %+v", c.name, v)
		}
	}
}

func TestApplyInVideoTakesNameFromWrongSpeaker(t *testing.T) {
	jordan := enrolledVoice{name: "Hair Jordan", embedding: []float64{1, 0, 0}}
	speakers := []speakerAudio{
		{id: "SPEAKER_00", segments: []SpeakerSpan{{0, 60}}, embedding: normalize([]float64{0.8, 0, 0.6})},
		{id: "SPEAKER_01", segments: []SpeakerSpan{{60, 120}}, embedding: normalize([]float64{0.95, 0, -0.31})},
	}
	// the old per-speaker bars named the wrong one
	ids := []Identification{{Type: "voice", SpeakerID: "SPEAKER_00", Name: "Hair Jordan", Confidence: 0.8, Match: "redimnet"}}
	picks := []contextPick{{Name: "Hair Jordan", Labels: []string{"SPEAKER_01"}, Confidence: "high"}}
	opts := voiceOptions{threshold: 0.30}
	gotIDs, gotUnids, _, _ := applyInVideo(speakers, []enrolledVoice{jordan}, ids, nil, picks, opts, "redimnet")
	if len(gotIDs) != 1 || gotIDs[0].SpeakerID != "SPEAKER_01" || gotIDs[0].Match != "redimnet+context" {
		t.Fatalf("ids: %+v", gotIDs)
	}
	if len(gotUnids) != 1 || gotUnids[0].SpeakerID != "SPEAKER_00" || gotUnids[0].WhyUnnamed != whyOtherSpeaker {
		t.Fatalf("unids: %+v", gotUnids)
	}
}

func TestContextTranscriptKeepsNameMentions(t *testing.T) {
	var lines []tLine
	for i := 0; i < 80; i++ {
		spk := []string{"SPEAKER_00", "SPEAKER_01"}[i%2]
		lines = append(lines, tLine{float64(i), float64(i + 1), spk, "filler words here"})
	}
	lines[60].Text = "thanks Jordan for having me"
	got := contextTranscript(lines, []personHint{{Name: "Hair Jordan"}})
	if !strings.Contains(got, "thanks Jordan") || !strings.Contains(got, "...") {
		t.Errorf("transcript:\n%s", got)
	}
	picks, ok := parseContext("sure: {\"people\":[{\"name\":\"Hair Jordan\",\"labels\":[\"SPEAKER_01\"],\"confidence\":\"High\",\"evidence\":\"x\"}]}")
	if !ok || !picks[0].sure() || picks[0].Labels[0] != "SPEAKER_01" {
		t.Errorf("parse: %+v %v", picks, ok)
	}
}

func TestTieIsReportedForEscalation(t *testing.T) {
	a, b := []float64{1, 0}, []float64{0, 1}
	v := decideInVideo([]labelScore{lab("S0", 0.59, 12, a...), lab("S1", 0.50, 8, b...), lab("S2", 0.36, 14, b...)}, nil, 0.30, "redimnet")
	if !reflect.DeepEqual(v.Tied, []string{"S0", "S1"}) || len(v.Owners) != 0 {
		t.Fatalf("got %+v", v)
	}
	v = decideInVideo([]labelScore{lab("S0", 0.59, 12, a...), lab("S1", 0.50, 8, b...)},
		&contextPick{Labels: []string{"S0"}, Confidence: "high", Via: "look"}, 0.30, "redimnet")
	if !reflect.DeepEqual(v.Owners, []string{"S0"}) || v.Via != "look" {
		t.Fatalf("a look pick must settle the tie: %+v", v)
	}
}

func TestWordsVersusVoiceEscalates(t *testing.T) {
	a, b := []float64{1, 0}, []float64{0, 1}
	scores := []labelScore{lab("S0", 0.12, 200, a...), lab("S1", 0.05, 50, b...)}
	words := &contextPick{Labels: []string{"S0"}, Confidence: "high"}
	v := decideInVideo(scores, words, 0.30, "redimnet")
	if len(v.Owners) != 0 || !reflect.DeepEqual(v.Tied, []string{"S0"}) {
		t.Fatalf("a dispute must go to the escalation, not be dropped: %+v", v)
	}
	looked := &contextPick{Labels: []string{"S0"}, Confidence: "high", Via: "look"}
	if v = decideInVideo(scores, looked, 0.30, "redimnet"); !reflect.DeepEqual(v.Owners, []string{"S0"}) || v.Via != "look" {
		t.Fatalf("the look decides a dispute: %+v", v)
	}
}
