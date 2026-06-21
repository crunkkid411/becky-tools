package ctledit

import (
	"testing"

	"becky-go/internal/dawmodel"
)

// drumNoteSteps returns the distinct step indices (start/StepTicks) of notes at a
// GM note number in a clip, for asserting generative placement.
func drumNoteSteps(a *dawmodel.Arrangement, trackID, clipName string, note int) map[int]bool {
	steps := map[int]bool{}
	for _, t := range a.Tracks {
		if t.ID != trackID {
			continue
		}
		for _, c := range t.Clips {
			if c.Name != clipName {
				continue
			}
			for _, n := range c.Notes {
				if n.Pitch == note {
					steps[n.Start/120] = true
				}
			}
		}
	}
	return steps
}

func TestApply_GenerateBeat(t *testing.T) {
	a := makeArrangement(t)
	batch := BeckyEditBatch{Edits: []BeckyEdit{
		{Op: OpGenerateBeat, Track: "kick", Clip: "drums", Genre: "house", Seed: 3},
	}}
	out, res, err := Apply(a, batch, nil)
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	if res.Applied != 1 {
		t.Fatalf("expected 1 applied, got %d (skipped %d): %+v", res.Applied, res.Skipped, res.Outcomes)
	}
	// House biases the kick to four-on-the-floor; at minimum the regenerate must
	// produce kick onsets, and not collapse them all onto one step.
	steps := drumNoteSteps(out, "kick", "drums", 36)
	if len(steps) < 2 {
		t.Errorf("generate_beat produced %d distinct kick steps, expected a spread: %v", len(steps), steps)
	}
}

func TestApply_GenerateBeat_Deterministic(t *testing.T) {
	a := makeArrangement(t)
	batch := BeckyEditBatch{Edits: []BeckyEdit{
		{Op: OpGenerateBeat, Track: "kick", Genre: "trap", Seed: 11},
	}}
	out1, _, _ := Apply(a, batch, nil)
	out2, _, _ := Apply(a, batch, nil)
	s1 := drumNoteSteps(out1, "kick", "drums", 36)
	s2 := drumNoteSteps(out2, "kick", "drums", 36)
	if len(s1) != len(s2) {
		t.Fatalf("same seed gave different kick step counts: %d vs %d", len(s1), len(s2))
	}
	for s := range s1 {
		if !s2[s] {
			t.Errorf("same seed produced different placement: step %d missing in second run", s)
		}
	}
}

func TestApply_GenerateBeat_NonDrumTrackSkipped(t *testing.T) {
	a := makeArrangement(t)
	// "lead"/"melody" is empty — no lanes to regenerate; must skip with a reason.
	batch := BeckyEditBatch{Edits: []BeckyEdit{
		{Op: OpGenerateBeat, Track: "lead", Clip: "melody", Genre: "house"},
	}}
	_, res, _ := Apply(a, batch, nil)
	if res.Skipped != 1 || res.Applied != 0 {
		t.Fatalf("empty clip should skip, got applied=%d skipped=%d", res.Applied, res.Skipped)
	}
	if res.Outcomes[0].Reason == "" {
		t.Error("skip must carry a plain-English reason")
	}
}

func TestApply_EuclidLane(t *testing.T) {
	a := makeArrangement(t)
	batch := BeckyEditBatch{Edits: []BeckyEdit{
		{Op: OpEuclidLane, Track: "kick", Clip: "drums", Lane: "kick", Pulses: 4, Rotation: 0},
	}}
	out, res, err := Apply(a, batch, nil)
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	if res.Applied != 1 {
		t.Fatalf("expected 1 applied, got %d: %+v", res.Applied, res.Outcomes)
	}
	steps := drumNoteSteps(out, "kick", "drums", 36)
	// E(4,16) spreads 4 onsets evenly: steps 0,4,8,12.
	want := []int{0, 4, 8, 12}
	if len(steps) != 4 {
		t.Fatalf("E(4,16) kick expected 4 onsets, got %d: %v", len(steps), steps)
	}
	for _, s := range want {
		if !steps[s] {
			t.Errorf("expected kick onset at step %d, got %v", s, steps)
		}
	}
}

func TestApply_EuclidLane_Validation(t *testing.T) {
	a := makeArrangement(t)
	cases := []struct {
		name string
		ed   BeckyEdit
	}{
		{"no lane", BeckyEdit{Op: OpEuclidLane, Track: "kick", Pulses: 4}},
		{"no pulses", BeckyEdit{Op: OpEuclidLane, Track: "kick", Lane: "kick"}},
		{"missing lane name", BeckyEdit{Op: OpEuclidLane, Track: "kick", Lane: "cowbell", Pulses: 3}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, res, _ := Apply(a, BeckyEditBatch{Edits: []BeckyEdit{tc.ed}}, nil)
			if res.Skipped != 1 || res.Outcomes[0].Reason == "" {
				t.Errorf("%s should skip with a reason, got %+v", tc.name, res.Outcomes[0])
			}
		})
	}
}

func TestApply_GenerativeOps_Immutable(t *testing.T) {
	a := makeArrangement(t)
	before := drumNoteSteps(a, "kick", "drums", 36)
	Apply(a, BeckyEditBatch{Edits: []BeckyEdit{{Op: OpGenerateBeat, Track: "kick", Genre: "dnb", Seed: 5}}}, nil)
	after := drumNoteSteps(a, "kick", "drums", 36)
	if len(before) != len(after) {
		t.Error("Apply must not mutate the input arrangement")
	}
}

func TestApply_AddLayer_bass(t *testing.T) {
	// A drums-only arrangement (makeArrangement already has a bass track, which
	// AddBass would correctly refuse).
	a := dawmodel.New()
	a.Root, a.Scale = "A", "minor"
	a = a.AddTrack("drums", dawmodel.KindMIDI)
	a.Tracks[0].Clips = append(a.Tracks[0].Clips, dawmodel.Clip{Name: "beat", Channel: 9, Program: -1})
	a, _, _ = a.AddNote("drums", "beat", dawmodel.Note{Start: 0, Dur: 120, Pitch: 36, Vel: 110, Ch: 9})
	a, _, _ = a.AddNote("drums", "beat", dawmodel.Note{Start: 480, Dur: 120, Pitch: 36, Vel: 110, Ch: 9})

	out, res, err := Apply(a, BeckyEditBatch{Edits: []BeckyEdit{
		{Op: OpAddLayer, Layer: "bass", Genre: "house", Seed: 1},
	}}, nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Applied != 1 {
		t.Fatalf("add_layer bass not applied: %+v", res.Outcomes)
	}
	if _, ok := out.TrackByID("bass"); !ok {
		t.Error("expected a bass track after add_layer")
	}
}

func TestApply_AddLayer_validation(t *testing.T) {
	a := makeArrangement(t)
	// missing layer
	_, res, _ := Apply(a, BeckyEditBatch{Edits: []BeckyEdit{{Op: OpAddLayer}}}, nil)
	if res.Skipped != 1 || res.Outcomes[0].Reason == "" {
		t.Error("add_layer without a layer should skip with a reason")
	}
	// unknown layer
	_, res2, _ := Apply(a, BeckyEditBatch{Edits: []BeckyEdit{{Op: OpAddLayer, Layer: "kazoo"}}}, nil)
	if res2.Skipped != 1 {
		t.Error("unknown layer should skip")
	}
}

func TestParsePhrase_addLayer(t *testing.T) {
	a := makeArrangement(t)
	cases := map[string]string{
		"add a bassline":       "bass",
		"lay down some chords": "chords",
		"add a melody":         "melody",
		"give me a lead":       "melody",
	}
	for phrase, want := range cases {
		b, ok := ParsePhrase(phrase, a)
		if !ok || len(b.Edits) != 1 || b.Edits[0].Op != OpAddLayer {
			t.Fatalf("%q should produce add_layer, got ok=%v %+v", phrase, ok, b.Edits)
		}
		if b.Edits[0].Layer != want {
			t.Errorf("%q → layer %q, want %q", phrase, b.Edits[0].Layer, want)
		}
	}
}

func TestApply_AddTrack(t *testing.T) {
	a := dawmodel.New()
	out, res, err := Apply(a, BeckyEditBatch{Edits: []BeckyEdit{
		{Op: OpAddTrack, Track: "lead", Clip: "riff"},
	}}, nil)
	if err != nil || res.Applied != 1 {
		t.Fatalf("add_track failed: %+v err=%v", res.Outcomes, err)
	}
	tr, ok := out.TrackByID("lead")
	if !ok || len(tr.Clips) != 1 || tr.Clips[0].Name != "riff" {
		t.Errorf("lead track/clip not created right: %+v", tr)
	}
	// Duplicate is refused.
	_, res2, _ := Apply(out, BeckyEditBatch{Edits: []BeckyEdit{{Op: OpAddTrack, Track: "lead"}}}, nil)
	if res2.Skipped != 1 {
		t.Error("adding a duplicate track id should skip")
	}
	// Missing id is refused.
	_, res3, _ := Apply(a, BeckyEditBatch{Edits: []BeckyEdit{{Op: OpAddTrack}}}, nil)
	if res3.Skipped != 1 || res3.Outcomes[0].Reason == "" {
		t.Error("add_track without an id should skip with a reason")
	}
}

func TestParsePhrase_transportAndMixer(t *testing.T) {
	a := dawmodel.New()
	a = a.AddTrack("bass", dawmodel.KindMIDI)
	a = a.AddTrack("drums", dawmodel.KindMIDI)

	// tempo
	b, ok := ParsePhrase("set bpm to 128", a)
	if !ok || b.Edits[0].Op != OpSetTempo || b.Edits[0].BPM != 128 {
		t.Errorf("set bpm phrase failed: ok=%v %+v", ok, b.Edits)
	}
	if b2, ok := ParsePhrase("make it 140 bpm", a); !ok || b2.Edits[0].BPM != 140 {
		t.Errorf("'140 bpm' phrase failed: %+v", b2.Edits)
	}
	// mute / unmute
	b3, ok := ParsePhrase("mute the bass", a)
	if !ok || b3.Edits[0].Op != OpMute || b3.Edits[0].Target != "bass" || !b3.Edits[0].Muted {
		t.Errorf("mute phrase failed: ok=%v %+v", ok, b3.Edits)
	}
	b4, _ := ParsePhrase("unmute the bass", a)
	if b4.Edits[0].Op != OpMute || b4.Edits[0].Muted {
		t.Errorf("unmute should set Muted=false: %+v", b4.Edits)
	}
	// solo
	b5, ok := ParsePhrase("solo the drums", a)
	if !ok || b5.Edits[0].Op != OpSolo || b5.Edits[0].Target != "drums" || !b5.Edits[0].Soloed {
		t.Errorf("solo phrase failed: %+v", b5.Edits)
	}
}

func TestDescribe(t *testing.T) {
	a := dawmodel.New()
	a.BPM, a.Root, a.Scale = 140, "F", "minor"
	a = a.AddTrack("drums", dawmodel.KindMIDI)
	a.Tracks[0].Clips = append(a.Tracks[0].Clips, dawmodel.Clip{Name: "beat", Channel: 9, Program: -1})
	a, _, _ = a.AddNote("drums", "beat", dawmodel.Note{Start: 0, Dur: 120, Pitch: 36, Vel: 100, Ch: 9})

	si := Describe(a)
	if si.BPM != 140 || si.Root != "F" {
		t.Errorf("scene header wrong: %+v", si)
	}
	if len(si.Tracks) != 1 || si.Tracks[0].ID != "drums" {
		t.Fatalf("tracks wrong: %+v", si.Tracks)
	}
	c := si.Tracks[0].Clips[0]
	if !c.IsDrum || len(c.Lanes) == 0 {
		t.Errorf("drum clip should report IsDrum + lanes: %+v", c)
	}
	if Describe(nil).BPM != 0 {
		t.Error("Describe(nil) should be safe")
	}
}

func TestApply_DuplicateNotes_doublesLoop(t *testing.T) {
	// A 1-bar drum clip; duplicate with no offset should append a copy at bar 2.
	a := dawmodel.New()
	a = a.AddTrack("drums", dawmodel.KindMIDI)
	a.Tracks[0].Clips = append(a.Tracks[0].Clips, dawmodel.Clip{Name: "beat", Channel: 9, Program: -1})
	for _, s := range []int{0, 4, 8, 12} {
		a, _, _ = a.AddNote("drums", "beat", dawmodel.Note{Start: s * 120, Dur: 120, Pitch: 36, Vel: 110, Ch: 9})
	}
	before := len(clipNotesOf(a, "drums", "beat"))

	out, res, err := Apply(a, BeckyEditBatch{Edits: []BeckyEdit{
		{Op: OpDuplicateNotes, Track: "drums", Clip: "beat"},
	}}, nil)
	if err != nil || res.Applied != 1 {
		t.Fatalf("duplicate failed: %+v err=%v", res.Outcomes, err)
	}
	got := clipNotesOf(out, "drums", "beat")
	if len(got) != before*2 {
		t.Fatalf("expected the loop to double (%d→%d), got %d", before, before*2, len(got))
	}
	// The copies must land one bar (1920 ticks) later.
	starts := map[int]bool{}
	for _, n := range got {
		starts[n.Start] = true
	}
	for _, s := range []int{0, 4, 8, 12} {
		if !starts[s*120+1920] {
			t.Errorf("expected a duplicated hit at tick %d", s*120+1920)
		}
	}
}

func TestApply_DuplicateNotes_byIDsWithOffset(t *testing.T) {
	a := dawmodel.New()
	a = a.AddTrack("lead", dawmodel.KindMIDI)
	a.Tracks[0].Clips = append(a.Tracks[0].Clips, dawmodel.Clip{Name: "m", Channel: 0, Program: 80})
	var id1 uint64
	a, id1, _ = a.AddNote("lead", "m", dawmodel.Note{Start: 0, Dur: 240, Pitch: 60, Vel: 90, Ch: 0})
	a, _, _ = a.AddNote("lead", "m", dawmodel.Note{Start: 480, Dur: 240, Pitch: 64, Vel: 90, Ch: 0})

	out, res, _ := Apply(a, BeckyEditBatch{Edits: []BeckyEdit{
		{Op: OpDuplicateNotes, Track: "lead", Clip: "m", NoteIDs: []uint64{id1}, DeltaTicks: 960},
	}}, nil)
	if res.Applied != 1 {
		t.Fatalf("duplicate-by-id failed: %+v", res.Outcomes)
	}
	got := clipNotesOf(out, "lead", "m")
	if len(got) != 3 { // only the one selected note was duplicated
		t.Fatalf("expected 3 notes (2 + 1 dup), got %d", len(got))
	}
	found := false
	for _, n := range got {
		if n.Start == 960 && n.Pitch == 60 {
			found = true
		}
	}
	if !found {
		t.Error("the duplicated note should appear at start 960, pitch 60")
	}
}

func TestApply_DuplicateNotes_emptyClip(t *testing.T) {
	a := dawmodel.New()
	a = a.AddTrack("lead", dawmodel.KindMIDI)
	a.Tracks[0].Clips = append(a.Tracks[0].Clips, dawmodel.Clip{Name: "m", Channel: 0, Program: 80})
	_, res, _ := Apply(a, BeckyEditBatch{Edits: []BeckyEdit{{Op: OpDuplicateNotes, Track: "lead", Clip: "m"}}}, nil)
	if res.Skipped != 1 || res.Outcomes[0].Reason == "" {
		t.Error("duplicating an empty clip should skip with a reason")
	}
}
