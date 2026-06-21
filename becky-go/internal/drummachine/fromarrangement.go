package drummachine

import "becky-go/internal/dawmodel"

// fromarrangement.go bridges the canvas/arranger world (dawmodel.Arrangement) to the
// groovebox world (Machine) so the SAME deterministic sampler engine that powers the
// standalone becky-drummachine can play a beat authored in becky-canvas. This is the
// wire that was missing: the canvas drum panel edits a dawmodel.Arrangement, and this
// turns that arrangement's drum clip into a playable Machine (real kit, velocities,
// choke groups) instead of the weak 4-sample synth path.

// MachineFromArrangement builds a playable Machine from an arrangement's drum clip.
// It starts from the default GM kit (so every pad has a sound mapping + choke
// behaviour) and fills one pattern from the clip's grid, mapping each drum lane onto
// the pad whose MIDI note matches. Tempo follows the arrangement. Degrade-never-crash:
// a nil/drumless arrangement yields a valid empty Machine.
func MachineFromArrangement(a *dawmodel.Arrangement) *Machine {
	m := NewMachine()
	if a == nil {
		return m
	}
	if a.BPM > 0 {
		m.Tempo = float64(a.BPM)
	}
	tid, clip, ok := arrDrumClip(a)
	if !ok {
		return m
	}
	g, err := a.DrumGridOf(tid, clip, 0)
	if err != nil || g == nil || len(g.Lanes) == 0 {
		return m
	}

	steps := snapSteps(g.Steps * maxBars(g.Bars))
	pat := emptyPattern("Canvas", steps)

	// pad index by GM note, from the default kit.
	noteToPad := map[int]int{}
	for i, p := range m.Kit.Pads {
		noteToPad[p.MidiNote] = i
	}

	for _, lane := range g.Lanes {
		pad, ok := noteToPad[lane.Note]
		if !ok {
			pad, ok = noteToPad[nearestGMNote(lane.Note)]
			if !ok {
				continue
			}
		}
		for s := 0; s < len(lane.On) && s < steps; s++ {
			if !lane.On[s] {
				continue
			}
			vel := 100
			if s < len(lane.Vel) && lane.Vel[s] > 0 {
				vel = lane.Vel[s]
			}
			pat.Lanes[pad][s] = Step{On: true, Vel: vel}
		}
	}
	m.Bank = Bank{Patterns: []Pattern{pat}}
	m.Scenes = []Scene{{Name: "Scene 1", PatternIndex: 0}}
	m.Song = Song{Entries: []SongEntry{{SceneIndex: 0, Repeat: 1}}}
	return m
}

// WithDefaultKitSamples points the core pads (kick/snare/hat/clap — GM 36/38/42/39)
// at the becky-owned default kit files in dir, so a canvas beat played through the
// sampler engine SOUNDS (real one-shots) instead of needing a kit loaded first. Pure
// string assignment (the sampler resolves/falls back if a file is absent); returns a
// new Machine. dir == "" leaves the kit untouched.
func (m *Machine) WithDefaultKitSamples(dir string) *Machine {
	if dir == "" {
		return m
	}
	files := map[int]string{36: "kick.wav", 38: "snare.wav", 42: "hat.wav", 39: "clap.wav"}
	out := m.clone()
	for i := range out.Kit.Pads {
		if name, ok := files[out.Kit.Pads[i].MidiNote]; ok && out.Kit.Pads[i].SamplePath == "" {
			out.Kit.Pads[i].SamplePath = dir + "/" + name
		}
	}
	return out
}

// arrDrumClip finds the arrangement's drum clip (channel-9 by clip or by note).
func arrDrumClip(a *dawmodel.Arrangement) (string, string, bool) {
	for _, t := range a.Tracks {
		if t.Kind != "" && t.Kind != dawmodel.KindMIDI {
			continue
		}
		for _, c := range t.Clips {
			if len(c.Notes) == 0 {
				continue
			}
			if c.Channel == 9 {
				return t.ID, c.Name, true
			}
			for _, n := range c.Notes {
				if n.Ch == 9 {
					return t.ID, c.Name, true
				}
			}
		}
	}
	return "", "", false
}

func maxBars(b int) int {
	if b < 1 {
		return 1
	}
	return b
}

// nearestGMNote maps an unusual percussion note to the closest note the default kit
// defines, so a non-standard grid lane still lands on a pad rather than vanishing.
func nearestGMNote(note int) int {
	best, bestDist := 0, 1<<30
	for _, d := range gmDefaults {
		dist := note - d.note
		if dist < 0 {
			dist = -dist
		}
		if dist < bestDist {
			bestDist, best = dist, d.note
		}
	}
	return best
}
