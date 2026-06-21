package drummachine

import (
	"testing"

	"becky-go/internal/dawmodel"
	"becky-go/internal/music"
)

func drumArrangement(t *testing.T) *dawmodel.Arrangement {
	t.Helper()
	a := dawmodel.New()
	a.BPM = 140
	a = a.AddTrack("drums", dawmodel.KindMIDI)
	a.Tracks[0].Clips = append(a.Tracks[0].Clips, dawmodel.Clip{Name: "beat", Channel: 9, Program: -1})
	add := func(note, step, vel int) {
		a, _, _ = a.AddNote("drums", "beat", dawmodel.Note{
			Start: step * music.StepTicks, Dur: music.StepTicks, Pitch: note, Vel: vel, Ch: 9,
		})
	}
	// Kick (36) on 0,4,8,12; snare (38) on 4,12.
	for _, s := range []int{0, 4, 8, 12} {
		add(36, s, 110)
	}
	for _, s := range []int{4, 12} {
		add(38, s, 95)
	}
	return a
}

func TestMachineFromArrangement_mapsGridToPads(t *testing.T) {
	a := drumArrangement(t)
	m := MachineFromArrangement(a)

	if m.Tempo != 140 {
		t.Errorf("tempo should follow the arrangement (140), got %v", m.Tempo)
	}
	if len(m.Bank.Patterns) != 1 {
		t.Fatalf("expected one pattern, got %d", len(m.Bank.Patterns))
	}
	pat := m.Bank.Patterns[0]

	// Pad 0 is the kick (GM 36): on at 0,4,8,12.
	kick := pat.Lanes[0]
	for _, s := range []int{0, 4, 8, 12} {
		if !kick[s].On {
			t.Errorf("kick pad should be ON at step %d", s)
		}
	}
	if kick[2].On {
		t.Error("kick should be OFF at step 2")
	}
	if kick[0].Vel != 110 {
		t.Errorf("kick velocity should carry through (110), got %d", kick[0].Vel)
	}

	// Pad 1 is the snare (GM 38): on at 4,12 only.
	snare := pat.Lanes[1]
	if !snare[4].On || !snare[12].On {
		t.Error("snare should be ON at 4 and 12")
	}
	if snare[0].On {
		t.Error("snare should be OFF at 0")
	}
}

func TestMachineFromArrangement_nilAndDrumless(t *testing.T) {
	if m := MachineFromArrangement(nil); m == nil || len(m.Bank.Patterns) == 0 {
		t.Error("nil arrangement should still yield a valid empty machine")
	}
	// A melodic-only arrangement (no channel-9 clip) → valid empty machine.
	a := dawmodel.New()
	a = a.AddTrack("lead", dawmodel.KindMIDI)
	a.Tracks[0].Clips = append(a.Tracks[0].Clips, dawmodel.Clip{Name: "m", Channel: 0, Program: 80})
	a, _, _ = a.AddNote("lead", "m", dawmodel.Note{Start: 0, Dur: 96, Pitch: 60, Vel: 90, Ch: 0})
	m := MachineFromArrangement(a)
	if m == nil || m.Tempo <= 0 {
		t.Error("drumless arrangement should yield a valid machine")
	}
}

func TestMachineFromArrangement_keepsDefaultKitSounds(t *testing.T) {
	// The machine must carry the full default kit so the sampler engine has a sound
	// per pad (the whole point of routing through the sampler, not the synth).
	m := MachineFromArrangement(drumArrangement(t))
	if len(m.Kit.Pads) != PadCount {
		t.Fatalf("machine should keep the full %d-pad kit, got %d", PadCount, len(m.Kit.Pads))
	}
	if m.Kit.Pads[0].MidiNote != 36 {
		t.Errorf("pad 0 should be the kick (GM 36), got %d", m.Kit.Pads[0].MidiNote)
	}
}
