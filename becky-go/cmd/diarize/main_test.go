package main

import "testing"

// nemo-speech numbers speakers from 1 in arrival order; becky names them SPEAKER_00, SPEAKER_01, ...
// so the first voice heard is SPEAKER_00. Log noise before the JSON and empty/invalid spans are skipped.
func TestParseNemoJSONNamesSpeakersFromZero(t *testing.T) {
	raw := "diarize: model=x mode=streaming\n" + `{
  "file": "C:\\tmp\\a.wav",
  "segments": [
    {"start": 0.160, "end": 2.400, "speaker": 1},
    {"start": 2.500, "end": 4.000, "speaker": 2},
    {"start": 4.000, "end": 4.000, "speaker": 2},
    {"start": 5.000, "end": 6.500, "speaker": 1}
  ]
}
`
	got, err := parseNemoJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := []flatSegment{
		{Start: 0.16, End: 2.4, Speaker: "SPEAKER_00"},
		{Start: 2.5, End: 4.0, Speaker: "SPEAKER_01"},
		{Start: 5.0, End: 6.5, Speaker: "SPEAKER_00"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d segments, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("segment %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// No speech: nemo-speech prints an empty list; that is zero segments, not an error.
func TestParseNemoJSONEmpty(t *testing.T) {
	got, err := parseNemoJSON("{\n  \"file\": \"a.wav\",\n  \"segments\": []\n}\n")
	if err != nil || len(got) != 0 {
		t.Fatalf("empty result: got %+v, err %v", got, err)
	}
	if _, err := parseNemoJSON("error: model not found"); err == nil {
		t.Fatal("output with no JSON must be an error")
	}
}

// --max-speakers 2 over three voices: the voice with the least speech (SPEAKER_02, 1s) is handed
// to the kept speaker talking nearest in time (SPEAKER_01 at 10-14s), the two big voices survive.
func TestCapSpeakersMergesSmallestIntoNearest(t *testing.T) {
	segs := []flatSegment{
		{Start: 0, End: 8, Speaker: "SPEAKER_00"},
		{Start: 10, End: 14, Speaker: "SPEAKER_01"},
		{Start: 14.5, End: 15.5, Speaker: "SPEAKER_02"},
		{Start: 20, End: 26, Speaker: "SPEAKER_00"},
	}
	got := capSpeakers(segs, 2)
	if got[2].Speaker != "SPEAKER_01" {
		t.Errorf("smallest voice went to %s, want SPEAKER_01 (nearest in time)", got[2].Speaker)
	}
	if countSpeakers(got) != 2 {
		t.Errorf("got %d speakers after cap 2", countSpeakers(got))
	}
	if segs[2].Speaker != "SPEAKER_02" {
		t.Error("capSpeakers must not modify its input")
	}
	if n := countSpeakers(capSpeakers(segs, 0)); n != 3 {
		t.Errorf("cap 0 means no cap, got %d speakers", n)
	}
}

// Festival clip: the model kept its slots 1 and 3 and dropped 2, and slot 3's first span came
// after slot 1's. Output ids must be contiguous and follow first appearance; input untouched.
func TestRenumberContiguousByFirstAppearance(t *testing.T) {
	segs := []flatSegment{
		{Start: 5, End: 7, Speaker: "SPEAKER_02"},
		{Start: 0, End: 2, Speaker: "SPEAKER_00"},
		{Start: 11, End: 13, Speaker: "SPEAKER_00"},
		{Start: 20, End: 25, Speaker: "SPEAKER_02"},
	}
	got := renumber(segs)
	want := []string{"SPEAKER_00", "SPEAKER_01", "SPEAKER_00", "SPEAKER_01"}
	for i, w := range want {
		if got[i].Speaker != w {
			t.Errorf("segment %d (start %.0f) = %s, want %s", i, got[i].Start, got[i].Speaker, w)
		}
	}
	if segs[0].Speaker != "SPEAKER_02" {
		t.Error("renumber must not modify its input")
	}
}

// groupBySpeaker turns the flat (start,end,speaker) list into the schema's
// per-speaker grouping, ordered by speaker id, each speaker's segments by start.
func TestGroupBySpeakerOrdersAndGroups(t *testing.T) {
	flat := []flatSegment{
		{Start: 5.0, End: 6.0, Speaker: "SPEAKER_01"},
		{Start: 0.0, End: 2.0, Speaker: "SPEAKER_00"},
		{Start: 2.0, End: 3.0, Speaker: "SPEAKER_01"},
		{Start: 3.0, End: 4.0, Speaker: "SPEAKER_00"},
	}
	got := groupBySpeaker(flat)
	if len(got) != 2 {
		t.Fatalf("expected 2 speakers, got %d", len(got))
	}
	if got[0].ID != "SPEAKER_00" || got[1].ID != "SPEAKER_01" {
		t.Fatalf("speakers not ordered by id: %s, %s", got[0].ID, got[1].ID)
	}
	// SPEAKER_01's segments must be start-sorted (2.0 before 5.0).
	if got[1].Segments[0].Start != 2.0 || got[1].Segments[1].Start != 5.0 {
		t.Errorf("SPEAKER_01 segments not start-sorted: %+v", got[1].Segments)
	}
	// Every segment carries the documented fixed confidence.
	for _, sp := range got {
		for _, s := range sp.Segments {
			if s.Confidence != segmentConfidence {
				t.Errorf("segment confidence = %v, want %v", s.Confidence, segmentConfidence)
			}
		}
	}
}

// A single-speaker flat list groups to exactly ONE speaker (no phantom split in the grouping layer).
func TestGroupBySpeakerSingleSpeaker(t *testing.T) {
	flat := []flatSegment{
		{Start: 0.0, End: 10.0, Speaker: "SPEAKER_00"},
		{Start: 11.0, End: 20.0, Speaker: "SPEAKER_00"},
	}
	got := groupBySpeaker(flat)
	if len(got) != 1 {
		t.Fatalf("single-speaker input must group to 1 speaker, got %d", len(got))
	}
	if len(got[0].Segments) != 2 {
		t.Errorf("expected both segments under the one speaker, got %d", len(got[0].Segments))
	}
}
