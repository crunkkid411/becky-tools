package bounce

import (
	"path/filepath"
	"testing"

	"becky-go/internal/dawmodel"
)

// midiArr builds a small arrangement: one MIDI track "lead" with 3 notes plus a
// drums track, with a non-default mixer strip on lead so we can assert it survives.
func midiArr() *dawmodel.Arrangement {
	a := dawmodel.New()
	a.Genre, a.Scale, a.BPM, a.PPQ = "crunkcore", "minor", 140, 480
	a = a.AddTrack("lead", dawmodel.KindMIDI)
	a = a.AddTrack("drums", dawmodel.KindMIDI)
	for i := range a.Tracks {
		if a.Tracks[i].ID == "lead" {
			a.Tracks[i].Clips = []dawmodel.Clip{{
				Name:    "lead-clip",
				Channel: 0,
				Program: 80,
				Notes: []dawmodel.Note{
					{ID: 1, Start: 0, Dur: 240, Pitch: 60, Vel: 100, Ch: 0},
					{ID: 2, Start: 240, Dur: 240, Pitch: 64, Vel: 100, Ch: 0},
					{ID: 3, Start: 480, Dur: 240, Pitch: 67, Vel: 100, Ch: 0},
				},
			}}
			a.Tracks[i].Strip = dawmodel.Strip{Gain: 0.75, Pan: -0.5, Mute: true, Bus: "bus.music"}
		}
	}
	return a
}

func TestPlanBounce_WavPathAndCounts(t *testing.T) {
	a := midiArr()
	p, err := PlanBounce(a, "lead", "/out/render")
	if err != nil {
		t.Fatalf("PlanBounce: %v", err)
	}
	want := filepath.Join("/out/render", "lead.bounce.wav")
	if p.WavPath != want {
		t.Errorf("WavPath = %q, want %q", p.WavPath, want)
	}
	if p.Track != "lead" {
		t.Errorf("Track = %q, want lead", p.Track)
	}
	if p.NoteCount != 3 {
		t.Errorf("NoteCount = %d, want 3", p.NoteCount)
	}
	if p.BPM != 140 {
		t.Errorf("BPM = %d, want 140", p.BPM)
	}
	if p.PPQ != 480 {
		t.Errorf("PPQ = %d, want 480", p.PPQ)
	}
	if p.SourceKind != dawmodel.KindMIDI {
		t.Errorf("SourceKind = %q, want midi", p.SourceKind)
	}
	if p.Note != "" {
		t.Errorf("Note = %q, want empty", p.Note)
	}
}

func TestPlanBounce_EmptyOutDir(t *testing.T) {
	a := midiArr()
	p, err := PlanBounce(a, "lead", "")
	if err != nil {
		t.Fatalf("PlanBounce: %v", err)
	}
	if p.WavPath != "lead.bounce.wav" {
		t.Errorf("WavPath = %q, want lead.bounce.wav", p.WavPath)
	}
}

func TestPlanBounce_MissingTrack(t *testing.T) {
	a := midiArr()
	_, err := PlanBounce(a, "nope", "/out")
	if err == nil {
		t.Fatal("expected error for missing track")
	}
}

func TestPlanBounce_NilArrangement(t *testing.T) {
	if _, err := PlanBounce(nil, "lead", "/out"); err == nil {
		t.Fatal("expected error for nil arrangement")
	}
}

func TestPlanBounce_AlreadyAudio(t *testing.T) {
	a := midiArr().AddTrack("vox", dawmodel.KindAudio)
	p, err := PlanBounce(a, "vox", "/out")
	if err != nil {
		t.Fatalf("PlanBounce: %v", err)
	}
	if p.SourceKind != dawmodel.KindAudio {
		t.Errorf("SourceKind = %q, want audio", p.SourceKind)
	}
	if p.Note == "" {
		t.Error("expected a no-op note for an already-audio track")
	}
}

func TestApplyBounce_TransformsTrack(t *testing.T) {
	a := midiArr()
	wav := "/out/render/lead.bounce.wav"
	out, err := ApplyBounce(a, "lead", wav)
	if err != nil {
		t.Fatalf("ApplyBounce: %v", err)
	}

	lead, ok := out.TrackByID("lead")
	if !ok {
		t.Fatal("lead track missing after bounce")
	}
	if lead.Kind != dawmodel.KindAudio {
		t.Errorf("Kind = %q, want audio", lead.Kind)
	}
	if len(lead.Clips) != 1 {
		t.Fatalf("clip count = %d, want 1", len(lead.Clips))
	}
	if lead.Clips[0].Name != wav {
		t.Errorf("clip name = %q, want wav path %q", lead.Clips[0].Name, wav)
	}
	if len(lead.Clips[0].Notes) != 0 {
		t.Errorf("notes after bounce = %d, want 0 (baked)", len(lead.Clips[0].Notes))
	}
	// Mixer placement preserved.
	if lead.Strip.Gain != 0.75 || lead.Strip.Pan != -0.5 || !lead.Strip.Mute || lead.Strip.Bus != "bus.music" {
		t.Errorf("strip not preserved: %+v", lead.Strip)
	}
	// Bounce recorded.
	if !IsBounced(out, "lead") {
		t.Error("IsBounced(lead) = false, want true")
	}
	got := out.CorrectionsByKind(CorrectionKindBounce)
	if len(got) != 1 {
		t.Fatalf("bounce corrections = %d, want 1", len(got))
	}
	if got[0].Clip != "lead" || got[0].Fixed != wav {
		t.Errorf("correction = %+v, want clip=lead fixed=%q", got[0], wav)
	}
}

func TestApplyBounce_OtherTracksUntouched(t *testing.T) {
	a := midiArr()
	out, err := ApplyBounce(a, "lead", "/out/lead.bounce.wav")
	if err != nil {
		t.Fatalf("ApplyBounce: %v", err)
	}
	drums, ok := out.TrackByID("drums")
	if !ok {
		t.Fatal("drums track missing")
	}
	if drums.Kind != dawmodel.KindMIDI {
		t.Errorf("drums Kind = %q, want midi (untouched)", drums.Kind)
	}
	if IsBounced(out, "drums") {
		t.Error("drums should not be marked bounced")
	}
}

func TestApplyBounce_Immutable(t *testing.T) {
	a := midiArr()
	beforeNotes := a.NoteCount()
	beforeCorr := a.CorrectionCount()

	_, err := ApplyBounce(a, "lead", "/out/lead.bounce.wav")
	if err != nil {
		t.Fatalf("ApplyBounce: %v", err)
	}

	// Input arrangement must be unchanged.
	if a.NoteCount() != beforeNotes {
		t.Errorf("input note count changed: %d -> %d", beforeNotes, a.NoteCount())
	}
	if a.CorrectionCount() != beforeCorr {
		t.Errorf("input corrections changed: %d -> %d", beforeCorr, a.CorrectionCount())
	}
	lead, _ := a.TrackByID("lead")
	if lead.Kind != dawmodel.KindMIDI {
		t.Errorf("input lead Kind mutated to %q", lead.Kind)
	}
	if len(lead.Clips) != 1 || len(lead.Clips[0].Notes) != 3 {
		t.Errorf("input lead notes mutated: %+v", lead.Clips)
	}
}

func TestApplyBounce_Errors(t *testing.T) {
	a := midiArr()
	cases := []struct {
		name  string
		arr   *dawmodel.Arrangement
		track string
		wav   string
	}{
		{"nil arr", nil, "lead", "/w.wav"},
		{"empty track", a, "", "/w.wav"},
		{"empty wav", a, "lead", ""},
		{"missing track", a, "nope", "/w.wav"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ApplyBounce(tc.arr, tc.track, tc.wav); err == nil {
				t.Errorf("%s: expected error, got nil", tc.name)
			}
		})
	}
}

func TestApplyBounce_Deterministic(t *testing.T) {
	a := midiArr()
	o1, _ := ApplyBounce(a, "lead", "/out/lead.bounce.wav")
	o2, _ := ApplyBounce(a, "lead", "/out/lead.bounce.wav")
	l1, _ := o1.TrackByID("lead")
	l2, _ := o2.TrackByID("lead")
	if l1.Clips[0].Name != l2.Clips[0].Name || l1.Kind != l2.Kind {
		t.Error("ApplyBounce not deterministic")
	}
	if o1.CorrectionCount() != o2.CorrectionCount() {
		t.Error("correction count differs between runs")
	}
}
