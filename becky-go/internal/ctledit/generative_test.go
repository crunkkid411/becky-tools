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
