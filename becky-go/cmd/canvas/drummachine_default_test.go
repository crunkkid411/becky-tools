package main

import (
	"testing"

	"becky-go/internal/dawmodel"
)

func TestStarterGrid_isPlayable(t *testing.T) {
	g := starterGrid()
	if g.StepTicks <= 0 {
		t.Fatal("starter grid StepTicks must be positive (else notes collapse to tick 0)")
	}
	if len(g.Lanes) < 4 {
		t.Fatalf("starter grid should have the kit lanes, got %d", len(g.Lanes))
	}
	// Kick must be four-on-the-floor (steps 0,4,8,12) — the recognisable default.
	var kick *dawmodel.Lane
	for i := range g.Lanes {
		if g.Lanes[i].Name == "kick" {
			kick = &g.Lanes[i]
		}
	}
	if kick == nil {
		t.Fatal("no kick lane in the starter grid")
	}
	for _, s := range []int{0, 4, 8, 12} {
		if s >= len(kick.On) || !kick.On[s] {
			t.Errorf("kick should hit step %d in the starter beat", s)
		}
	}
}

func TestDefaultDrumArrangement_hasEditableDrumClip(t *testing.T) {
	arr := defaultDrumArrangement()
	if !hasDrumClip(arr) {
		t.Fatal("default arrangement must contain a drum clip")
	}
	if arr.NoteCount() == 0 {
		t.Error("default arrangement should carry a starter beat (notes)")
	}
	// The drum panel derives its grid via DrumGridOf — that must succeed with lanes.
	g, err := arr.DrumGridOf("drums", "beat", 0)
	if err != nil {
		t.Fatalf("DrumGridOf on the default clip failed: %v", err)
	}
	if len(g.Lanes) == 0 {
		t.Error("default drum grid has no lanes — the machine would render empty")
	}
}

func TestEnsureDrumMachineArr_nil(t *testing.T) {
	out := ensureDrumMachineArr(nil)
	if !hasDrumClip(out) {
		t.Error("ensureDrumMachineArr(nil) must produce a drum machine")
	}
}

func TestEnsureDrumMachineArr_alreadyHasDrums_unchanged(t *testing.T) {
	arr := defaultDrumArrangement()
	out := ensureDrumMachineArr(arr)
	if out != arr {
		t.Error("an arrangement that already has a drum clip must be returned unchanged (no needless rebuild)")
	}
}

func TestEnsureDrumMachineArr_melodicSession_gainsDrums(t *testing.T) {
	// A loaded session with only a melodic track should GAIN a drum machine,
	// keeping its existing track.
	arr := dawmodel.New().AddTrack("lead", dawmodel.KindMIDI)
	arr.Tracks[0].Clips = append(arr.Tracks[0].Clips, dawmodel.Clip{Name: "melody", Channel: 0, Program: 80})
	arr, _, _ = arr.AddNote("lead", "melody", dawmodel.Note{Start: 0, Dur: 96, Pitch: 60, Vel: 90})

	out := ensureDrumMachineArr(arr)
	if !hasDrumClip(out) {
		t.Fatal("melodic session should gain a drum clip")
	}
	if _, ok := out.TrackByID("lead"); !ok {
		t.Error("existing 'lead' track must be preserved")
	}
}
