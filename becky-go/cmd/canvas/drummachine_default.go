package main

// drummachine_default.go (no build tag — compiled in both the gui and headless
// builds, and unit-tested headlessly) makes the "Drum machine" button OPEN A
// WORKING DRUM MACHINE instead of an "open a project.json" placeholder. When the
// session has no drum clip, ensureDrumMachineArr drops in a default starter beat
// (a standard kit on channel 9) so the grid, the Random/House/Trap buttons, and
// ▶ Play all work immediately. Pure functions over dawmodel/beatgen — no GUI here.

import (
	"becky-go/internal/beatgen"
	"becky-go/internal/dawmodel"
	"becky-go/internal/music"
)

// hasDrumClip reports whether arr already contains an editable drum clip: a MIDI
// clip whose own channel is 9, or that has notes on channel 9. Mirrors the
// detection used by the drum panel / ctledit so "do we have a drum machine yet?"
// answers the same way everywhere.
func hasDrumClip(arr *dawmodel.Arrangement) bool {
	if arr == nil {
		return false
	}
	for _, t := range arr.Tracks {
		if t.Kind != "" && t.Kind != dawmodel.KindMIDI {
			continue
		}
		for _, c := range t.Clips {
			if c.Channel == 9 {
				return true
			}
			for _, n := range c.Notes {
				if n.Ch == 9 {
					return true
				}
			}
		}
	}
	return false
}

// starterGrid builds the default drum machine pattern: a clean, recognisable
// house-leaning groove (four-on-the-floor kick, backbeat snare, eighth-note hats,
// offbeat open hats) so the machine opens with something playable. It is fully
// deterministic — the same starter every time — and every lane carries hits so
// none vanish when the grid round-trips through the note model.
func starterGrid() *dawmodel.DrumGrid {
	p := beatgen.NewPattern(16,
		beatgen.Lane{Name: "kick", Role: "kick"},
		beatgen.Lane{Name: "snare", Role: "snare"},
		beatgen.Lane{Name: "hat", Role: "hat"},
		beatgen.Lane{Name: "ohat", Role: "ohat"},
	)
	for _, s := range []int{0, 4, 8, 12} {
		p = p.SetStep("kick", s, true, 112)
	}
	for _, s := range []int{4, 12} {
		p = p.SetStep("snare", s, true, 108)
	}
	for _, s := range []int{0, 2, 4, 6, 8, 10, 12, 14} {
		p = p.SetStep("hat", s, true, 90)
	}
	for _, s := range []int{2, 6, 10, 14} {
		p = p.SetStep("ohat", s, true, 84)
	}
	g := beatgen.ToDrumGrid(p)
	if g.StepTicks <= 0 {
		g.StepTicks = music.StepTicks
	}
	return g
}

// defaultDrumArrangement is a fresh one-track arrangement holding the starter
// beat — what the Drum machine button loads when nothing is open.
func defaultDrumArrangement() *dawmodel.Arrangement {
	arr := dawmodel.New().AddTrack("drums", dawmodel.KindMIDI)
	arr.Tracks[0].Clips = append(arr.Tracks[0].Clips, dawmodel.Clip{
		Name: "beat", Channel: 9, Program: -1,
	})
	out, err := arr.ApplyDrumGrid("drums", "beat", starterGrid())
	if err != nil {
		return arr
	}
	return out
}

// ensureDrumMachineArr returns an arrangement guaranteed to have a drum clip:
//   - already has one          → returned unchanged (pointer-identical),
//   - nil / no tracks          → a fresh defaultDrumArrangement,
//   - has tracks but no drums   → the same arrangement plus a "drums" track
//     carrying the starter beat (so a loaded melodic session gains a usable
//     drum machine without disturbing its other tracks).
//
// The caller applies the result only when it differs from the input, so an
// existing drum session is never rebuilt needlessly.
func ensureDrumMachineArr(arr *dawmodel.Arrangement) *dawmodel.Arrangement {
	if hasDrumClip(arr) {
		return arr
	}
	if arr == nil || len(arr.Tracks) == 0 {
		return defaultDrumArrangement()
	}
	out := arr.AddTrack("drums", dawmodel.KindMIDI)
	li := len(out.Tracks) - 1
	out.Tracks[li].Clips = append(out.Tracks[li].Clips, dawmodel.Clip{
		Name: "beat", Channel: 9, Program: -1,
	})
	next, err := out.ApplyDrumGrid("drums", "beat", starterGrid())
	if err != nil {
		return arr
	}
	return next
}
