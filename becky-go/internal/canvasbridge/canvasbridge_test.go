package canvasbridge

import (
	"encoding/json"
	"testing"

	"becky-go/internal/canvas"
	"becky-go/internal/dawmodel"
)

// sampleArr builds a small editable arrangement: a bass MIDI track (2 notes) on
// bus.808 and a muted drums track, plus a bus with a sidechain source.
func sampleArr() *dawmodel.Arrangement {
	a := dawmodel.New()
	a.Genre = "crunkcore"
	a.BPM = 150
	a.PPQ = 480
	a.NextID = 1
	a.Tracks = []dawmodel.Track{
		{
			ID:   "bass",
			Kind: dawmodel.KindMIDI,
			Clips: []dawmodel.Clip{{
				Name: "bass", Channel: 0, Program: 38,
				Notes: []dawmodel.Note{
					{ID: 1, Start: 0, Dur: 480, Pitch: 33, Vel: 100, Ch: 0},
					{ID: 2, Start: 480, Dur: 480, Pitch: 45, Vel: 100, Ch: 0},
				},
			}},
			Strip: dawmodel.Strip{Gain: 1, Bus: "bus.808"},
		},
		{
			ID:    "drums",
			Kind:  dawmodel.KindMIDI,
			Clips: []dawmodel.Clip{{Name: "drums", Channel: 9}},
			Strip: dawmodel.Strip{Gain: 1, Bus: "bus.drums", Mute: true},
		},
	}
	a.Buses = []dawmodel.Bus{
		{ID: "bus.808", Out: "bus.master", Sidechain: []string{"src.drums.kick"}},
		{ID: "bus.drums", Out: "bus.master"},
	}
	return a
}

func TestSceneFromArrangement_Transport(t *testing.T) {
	s := SceneFromArrangement(sampleArr())
	if s.Transport.BPM != 150 || s.Transport.PPQ != 480 {
		t.Errorf("transport = %d/%d, want 150/480", s.Transport.BPM, s.Transport.PPQ)
	}
	if s.Title != "crunkcore" {
		t.Errorf("title = %q, want crunkcore", s.Title)
	}
	if s.ActiveMode != canvas.ModeDAW {
		t.Errorf("active mode = %q, want daw", s.ActiveMode)
	}
}

func TestSceneFromArrangement_TracksAndPitchLane(t *testing.T) {
	s := SceneFromArrangement(sampleArr())
	if len(s.Tracks) != 2 {
		t.Fatalf("tracks = %d, want 2", len(s.Tracks))
	}
	// Tracks sorted by ID: bass, drums.
	bass := s.Tracks[0]
	if bass.ID != "bass" || bass.Kind != canvas.LaneMIDI || bass.Bus != "bus.808" {
		t.Errorf("bass lane wrong: %+v", bass)
	}
	if bass.Lane.Pitch == nil || len(bass.Lane.Pitch.Points) != 2 {
		t.Fatalf("bass pitch points = %v, want 2", bass.Lane.Pitch)
	}
	// Real note-derived clip length: last note ends at 960 ticks.
	if len(bass.Clips) != 1 || bass.Clips[0].Len != 960 {
		t.Errorf("bass clip len = %v, want 960", bass.Clips)
	}
	// Pitch span covers the two notes (33..45).
	if bass.Lane.Pitch.Lo != 33 || bass.Lane.Pitch.Hi != 45 {
		t.Errorf("bass pitch span = %d..%d, want 33..45", bass.Lane.Pitch.Lo, bass.Lane.Pitch.Hi)
	}
	drums := s.Tracks[1]
	if !drums.Muted {
		t.Error("drums lane should be muted")
	}
}

func TestSceneFromArrangement_Routing(t *testing.T) {
	s := SceneFromArrangement(sampleArr())
	if len(s.Routing) != 1 {
		t.Fatalf("routing edges = %d, want 1", len(s.Routing))
	}
	e := s.Routing[0]
	if e.From != "src.drums.kick" || e.To != "bus.808" || e.Kind != "sidechain" {
		t.Errorf("routing edge = %+v, want kick->bus.808 sidechain", e)
	}
}

func TestSceneFromArrangement_Deterministic(t *testing.T) {
	a := sampleArr()
	s1 := SceneFromArrangement(a)
	s2 := SceneFromArrangement(a)
	j1, _ := json.Marshal(s1)
	j2, _ := json.Marshal(s2)
	if string(j1) != string(j2) {
		t.Error("SceneFromArrangement not deterministic for identical input")
	}
}

func TestSceneFromArrangement_NilDegrades(t *testing.T) {
	s := SceneFromArrangement(nil) // must not panic
	if len(s.Tracks) != 0 || s.Tool != "becky-canvas" {
		t.Errorf("nil arrangement should yield an empty-but-valid scene, got %+v", s)
	}
}
