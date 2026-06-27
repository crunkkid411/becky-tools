package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"becky-go/internal/dawmodel"
)

// writeArrangementJSON writes a small raw dawmodel.Arrangement JSON fixture.
func writeArrangementJSON(t *testing.T) string {
	t.Helper()
	arr := &dawmodel.Arrangement{
		BPM: 140, PPQ: 480, Num: 4, Den: 4,
		Tracks: []dawmodel.Track{
			{ID: "bass", Kind: dawmodel.KindMIDI, Strip: dawmodel.Strip{Gain: 1, Bus: "bus.808"},
				Clips: []dawmodel.Clip{{Name: "bass", Notes: []dawmodel.Note{{ID: 1, Start: 0, Dur: 240, Pitch: 36, Vel: 100}}}}},
			{ID: "lead", Kind: dawmodel.KindMIDI, Strip: dawmodel.Strip{Gain: 1, Bus: "bus.music"},
				Clips: []dawmodel.Clip{{Name: "lead", Notes: []dawmodel.Note{{ID: 2, Start: 0, Dur: 240, Pitch: 60, Vel: 90}}}}},
		},
		Buses:  []dawmodel.Bus{{ID: "bus.808", Out: "master"}, {ID: "bus.music", Out: "master"}},
		NextID: 2,
	}
	data, _ := json.MarshalIndent(arr, "", "  ")
	path := filepath.Join(t.TempDir(), "arr.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write arrangement fixture: %v", err)
	}
	return path
}

func TestRun_ask_exitCodes(t *testing.T) {
	arr := writeArrangementJSON(t)
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"no instruction is usage", []string{"ask", "--in", arr}, exitUsage},
		{"missing file errors", []string{"ask", "--in", "nope.json", "--do", "mute the bass"}, exitErr},
		{"recognized edit ok", []string{"ask", "--in", arr, "--do", "set tempo to 100"}, exitOK},
		{"unrecognized still ok", []string{"ask", "--in", arr, "--do", "make me a sandwich"}, exitOK},
		{"trailing-arg instruction", []string{"ask", "--in", arr, "mute the bass"}, exitOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := run(c.args); got != c.want {
				t.Errorf("run(%v) = %d, want %d", c.args, got, c.want)
			}
		})
	}
}

// TestRun_askWritesEditedArrangement: ask applies NL edits and writes a JSON
// arrangement that re-loads with the edits present.
func TestRun_askWritesEditedArrangement(t *testing.T) {
	in := writeArrangementJSON(t)
	out := filepath.Join(t.TempDir(), "edited.json")
	code := run([]string{"ask", "--in", in,
		"--do", "set tempo to 96",
		"--do", "mute the bass",
		"--do", "pan the lead hard left",
		"--out", out})
	if code != exitOK {
		t.Fatalf("ask run = %d, want 0", code)
	}
	edited, err := loadSession(out)
	if err != nil {
		t.Fatalf("re-load edited: %v", err)
	}
	if edited.BPM != 96 {
		t.Errorf("bpm = %d, want 96", edited.BPM)
	}
	if tr, _ := edited.TrackByID("bass"); !tr.Strip.Mute {
		t.Errorf("bass mute = false, want true")
	}
	if tr, _ := edited.TrackByID("lead"); tr.Strip.Pan != -1 {
		t.Errorf("lead pan = %v, want -1", tr.Strip.Pan)
	}
}

// TestRun_askDryRunWritesNothing: --dry-run never writes a file.
func TestRun_askDryRunWritesNothing(t *testing.T) {
	in := writeArrangementJSON(t)
	out := filepath.Join(t.TempDir(), "should-not-exist.json")
	code := run([]string{"ask", "--in", in, "--do", "set tempo to 120", "--out", out, "--dry-run"})
	if code != exitOK {
		t.Fatalf("ask dry-run = %d, want 0", code)
	}
	if _, err := os.Stat(out); err == nil {
		t.Errorf("dry-run wrote %s, but should not have", out)
	}
}

// TestLoadSession_MidiAndJSON: the loader accepts both .mid and arrangement .json.
func TestLoadSession_MidiAndJSON(t *testing.T) {
	mid := writeFixtureMid(t)
	if arr, err := loadSession(mid); err != nil || arr == nil || len(arr.Tracks) == 0 {
		t.Errorf("loadSession(.mid) = %v, %v", arr, err)
	}
	js := writeArrangementJSON(t)
	if arr, err := loadSession(js); err != nil || arr == nil || len(arr.Tracks) != 2 {
		t.Errorf("loadSession(.json) tracks=%d err=%v", trackCount(arr), err)
	}
	if _, err := loadSession(""); err == nil {
		t.Error("loadSession(\"\") should error")
	}
	if _, err := loadSession("x.txt"); err == nil {
		t.Error("loadSession(unsupported ext) should error")
	}
}

func trackCount(a *dawmodel.Arrangement) int {
	if a == nil {
		return 0
	}
	return len(a.Tracks)
}
