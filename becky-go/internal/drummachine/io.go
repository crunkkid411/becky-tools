package drummachine

import (
	"encoding/json"
	"io"
)

// io.go is the machine.json reader/writer. Output is deterministic and byte-stable
// (json.MarshalIndent over fixed-order structs — no maps in the serialized shape),
// so the same Machine always saves to identical bytes. Load is forgiving: unknown
// fields are ignored, missing/zero fields are filled with defaults, and a malformed
// or short kit/pattern is repaired rather than panicking (degrade-never-crash).

// Save writes m as deterministic, indented machine.json to w. The Machine is first
// normalised (defaults + structure repair) so saving a hand-edited model is stable.
func (m *Machine) Save(w io.Writer) error {
	out := m.clone()
	out.normalize()
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	// Trailing newline for clean diffs / POSIX text.
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}

// MarshalBytes returns the deterministic machine.json bytes for m (handy for tests
// and callers that don't have an io.Writer).
func (m *Machine) MarshalBytes() ([]byte, error) {
	out := m.clone()
	out.normalize()
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Load reads a machine.json from r and returns a normalised Machine. Unknown JSON
// fields are ignored; missing fields are defaulted; a structurally invalid file is
// repaired (kit padded to 16, lane lengths fixed, scene/song indices clamped) so
// callers always get a usable, internally-consistent Machine. A syntax error in the
// JSON returns (nil, err); everything else degrades to defaults.
func Load(r io.Reader) (*Machine, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var m Machine
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	m.normalize()
	return &m, nil
}

// normalize repairs and defaults a Machine in place so it is internally consistent
// and serialization is deterministic. Safe to call on a fresh, hand-edited, or
// old-schema Machine.
func (m *Machine) normalize() {
	if m.SchemaVersion <= 0 {
		m.SchemaVersion = SchemaVersion
	}
	if m.Name == "" {
		m.Name = "Untitled"
	}
	if m.Tempo <= 0 {
		m.Tempo = 120
	}

	m.normalizeKit()
	m.normalizeBank()
	m.normalizeScenes()
	m.normalizeSong()
}

// normalizeKit forces the kit to exactly PadCount pads, filling missing pads from
// DefaultKit and clamping every pad's numeric fields into range. Indices are set to
// their slot so the array is self-describing.
func (m *Machine) normalizeKit() {
	def := DefaultKit()
	if m.Kit.Name == "" {
		m.Kit.Name = def.Name
	}
	pads := make([]Pad, PadCount)
	for i := 0; i < PadCount; i++ {
		if i < len(m.Kit.Pads) {
			pads[i] = m.Kit.Pads[i]
		} else {
			pads[i] = def.Pads[i]
		}
		pads[i].Index = i
		if pads[i].MidiNote <= 0 {
			pads[i].MidiNote = def.Pads[i].MidiNote
		}
		if pads[i].Name == "" {
			pads[i].Name = def.Pads[i].Name
		}
		// Level: a zero from an old file that never wrote it -> unity; otherwise clamp.
		if pads[i].Level == 0 {
			pads[i].Level = defaultLevel
		}
		pads[i].Level = clampFloat(pads[i].Level, 0, 1)
		pads[i].Pan = clampFloat(pads[i].Pan, -1, 1)
		pads[i].PitchSemitones = clampFloat(pads[i].PitchSemitones, -48, 48)
		if pads[i].Decay < 0 {
			pads[i].Decay = 0
		}
		if pads[i].ChokeGroup < 0 {
			pads[i].ChokeGroup = 0
		}
	}
	m.Kit.Pads = pads
}

// normalizeBank ensures at least one pattern and fixes every pattern's shape: a
// valid step count, exactly PadCount lanes, each lane == Steps long, swing clamped,
// velocities consistent with on/off.
func (m *Machine) normalizeBank() {
	if len(m.Bank.Patterns) == 0 {
		m.Bank.Patterns = []Pattern{emptyPattern("Pattern 1", DefaultSteps)}
	}
	if len(m.Bank.Patterns) > MaxPatterns {
		m.Bank.Patterns = m.Bank.Patterns[:MaxPatterns]
	}
	for pi := range m.Bank.Patterns {
		p := &m.Bank.Patterns[pi]
		if p.Name == "" {
			p.Name = "Pattern"
		}
		if p.Steps <= 0 {
			// Infer from the longest existing lane, else default.
			maxLen := 0
			for _, ln := range p.Lanes {
				if len(ln) > maxLen {
					maxLen = len(ln)
				}
			}
			if maxLen > 0 {
				p.Steps = snapSteps(maxLen)
			} else {
				p.Steps = DefaultSteps
			}
		} else {
			p.Steps = snapSteps(p.Steps)
		}
		if p.Swing == 0 {
			p.Swing = swingNeutral
		}
		p.Swing = clampFloat(p.Swing, minSwing, swingMax)

		// Force exactly PadCount lanes, each Steps long.
		lanes := make([][]Step, PadCount)
		for li := 0; li < PadCount; li++ {
			src := []Step(nil)
			if li < len(p.Lanes) {
				src = p.Lanes[li]
			}
			ln := make([]Step, p.Steps)
			for s := 0; s < p.Steps && s < len(src); s++ {
				cell := src[s]
				if cell.On {
					if cell.Vel <= 0 {
						cell.Vel = defaultVel
					}
					cell.Vel = clampVel(cell.Vel)
				} else {
					cell.Vel = 0
				}
				ln[s] = cell
			}
			lanes[li] = ln
		}
		p.Lanes = lanes
	}
}

// normalizeScenes ensures at least one scene and clamps each scene's PatternIndex
// into the bank.
func (m *Machine) normalizeScenes() {
	if len(m.Scenes) == 0 {
		m.Scenes = []Scene{{Name: "Scene 1", PatternIndex: 0}}
	}
	last := len(m.Bank.Patterns) - 1
	for i := range m.Scenes {
		if m.Scenes[i].Name == "" {
			m.Scenes[i].Name = "Scene"
		}
		if m.Scenes[i].PatternIndex < 0 {
			m.Scenes[i].PatternIndex = 0
		}
		if m.Scenes[i].PatternIndex > last {
			m.Scenes[i].PatternIndex = last
		}
	}
}

// normalizeSong drops song entries that reference a missing scene and normalises
// repeat counts; an empty song gets a single entry playing the first scene.
func (m *Machine) normalizeSong() {
	lastScene := len(m.Scenes) - 1
	kept := make([]SongEntry, 0, len(m.Song.Entries))
	for _, e := range m.Song.Entries {
		if e.SceneIndex < 0 || e.SceneIndex > lastScene {
			continue
		}
		if e.Repeat < 1 {
			e.Repeat = 1
		}
		kept = append(kept, e)
	}
	if len(kept) == 0 {
		kept = []SongEntry{{SceneIndex: 0, Repeat: 1}}
	}
	m.Song.Entries = kept
}
