package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"becky-go/internal/dawmodel"
)

func TestDeriveSavePath(t *testing.T) {
	cases := []struct {
		name                       string
		sessionPath, target, asArg string
		want                       string
	}{
		{"overwrite loaded session", "/proj/song.json", "", "", "/proj/song.json"},
		{"save as into session dir", "/proj/song.json", "", "take2", filepath.Join("/proj", "take2.json")},
		{"save as adds .json", "/proj/song.json", "", "take2.json", filepath.Join("/proj", "take2.json")},
		{"no session, target file", "", "/clips/video.mp4", "", filepath.Join("/clips", "becky-session.json")},
		{"no session no target", "", "", "", "becky-session.json"},
		{"save as honors explicit dir", "", "", "/abs/path/mybeat.json", "/abs/path/mybeat.json"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := deriveSavePath(c.sessionPath, c.target, c.asArg); got != c.want {
				t.Errorf("deriveSavePath(%q,%q,%q) = %q, want %q", c.sessionPath, c.target, c.asArg, got, c.want)
			}
		})
	}
}

func TestSaveArrangementJSON_roundTrip(t *testing.T) {
	a := dawmodel.New()
	a.BPM, a.Root, a.Scale = 138, "F", "minor"
	a = a.AddTrack("drums", dawmodel.KindMIDI)
	a.Tracks[0].Clips = append(a.Tracks[0].Clips, dawmodel.Clip{Name: "beat", Channel: 9, Program: -1})
	a, _, _ = a.AddNote("drums", "beat", dawmodel.Note{Start: 0, Dur: 120, Pitch: 36, Vel: 110, Ch: 9})

	path := filepath.Join(t.TempDir(), "s.json")
	if err := saveArrangementJSON(a, path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var back dawmodel.Arrangement
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("saved session is not valid JSON: %v", err)
	}
	if back.BPM != 138 || back.Root != "F" || len(back.Tracks) != 1 {
		t.Errorf("round-trip lost data: bpm=%d root=%s tracks=%d", back.BPM, back.Root, len(back.Tracks))
	}
	if back.Tracks[0].Clips[0].Notes[0].Pitch != 36 {
		t.Error("round-trip lost the note")
	}
}
