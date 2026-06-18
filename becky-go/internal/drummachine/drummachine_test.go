package drummachine

import (
	"bytes"
	"reflect"
	"testing"

	"becky-go/internal/dawmodel"
)

// ---- construction defaults -------------------------------------------------

func TestNewMachineDefaults(t *testing.T) {
	m := NewMachine()
	if m.SchemaVersion != SchemaVersion {
		t.Errorf("schema = %d, want %d", m.SchemaVersion, SchemaVersion)
	}
	if m.Tempo != 120 {
		t.Errorf("tempo = %v, want 120", m.Tempo)
	}
	if len(m.Kit.Pads) != PadCount {
		t.Fatalf("kit pads = %d, want %d", len(m.Kit.Pads), PadCount)
	}
	if m.PatternCount() != 1 {
		t.Errorf("patterns = %d, want 1", m.PatternCount())
	}
	if m.SceneCount() != 1 {
		t.Errorf("scenes = %d, want 1", m.SceneCount())
	}
	p := m.Bank.Patterns[0]
	if p.Steps != DefaultSteps {
		t.Errorf("steps = %d, want %d", p.Steps, DefaultSteps)
	}
	if len(p.Lanes) != PadCount {
		t.Fatalf("lanes = %d, want %d", len(p.Lanes), PadCount)
	}
	for i, ln := range p.Lanes {
		if len(ln) != DefaultSteps {
			t.Errorf("lane %d len = %d, want %d", i, len(ln), DefaultSteps)
		}
		for _, c := range ln {
			if c.On {
				t.Errorf("lane %d has an on cell in a fresh pattern", i)
			}
		}
	}
}

func TestDefaultKitMapping(t *testing.T) {
	k := DefaultKit()
	if len(k.Pads) != PadCount {
		t.Fatalf("pads = %d", len(k.Pads))
	}
	if k.Pads[0].Name != "Kick" || k.Pads[0].MidiNote != 36 {
		t.Errorf("pad0 = %+v, want Kick/36", k.Pads[0])
	}
	if k.Pads[1].MidiNote != 38 {
		t.Errorf("pad1 note = %d, want 38", k.Pads[1].MidiNote)
	}
	if k.Pads[2].MidiNote != 42 || k.Pads[3].MidiNote != 46 {
		t.Errorf("hats = %d/%d, want 42/46", k.Pads[2].MidiNote, k.Pads[3].MidiNote)
	}
	// Hats share a choke group, others have none.
	if k.Pads[2].ChokeGroup == 0 || k.Pads[2].ChokeGroup != k.Pads[3].ChokeGroup {
		t.Errorf("hats choke = %d/%d, want equal non-zero", k.Pads[2].ChokeGroup, k.Pads[3].ChokeGroup)
	}
	if k.Pads[0].ChokeGroup != 0 {
		t.Errorf("kick choke = %d, want 0", k.Pads[0].ChokeGroup)
	}
	for i, p := range k.Pads {
		if p.Index != i {
			t.Errorf("pad %d Index = %d", i, p.Index)
		}
		if p.Level != 1 {
			t.Errorf("pad %d level = %v, want 1", i, p.Level)
		}
	}
}

// ---- immutability of pad edits --------------------------------------------

func TestPadEditsImmutable(t *testing.T) {
	m := NewMachine()
	tests := []struct {
		name  string
		apply func() (*Machine, error)
		check func(p Pad) bool
	}{
		{"sample", func() (*Machine, error) { return m.SetPadSample(0, "kick.wav") }, func(p Pad) bool { return p.SamplePath == "kick.wav" }},
		{"level", func() (*Machine, error) { return m.SetPadLevel(0, 0.5) }, func(p Pad) bool { return p.Level == 0.5 }},
		{"pan", func() (*Machine, error) { return m.SetPadPan(0, -0.5) }, func(p Pad) bool { return p.Pan == -0.5 }},
		{"pitch", func() (*Machine, error) { return m.SetPadPitch(0, 7) }, func(p Pad) bool { return p.PitchSemitones == 7 }},
		{"decay", func() (*Machine, error) { return m.SetPadDecay(0, 0.3) }, func(p Pad) bool { return p.Decay == 0.3 }},
		{"choke", func() (*Machine, error) { return m.SetPadChokeGroup(0, 4) }, func(p Pad) bool { return p.ChokeGroup == 4 }},
		{"mute", func() (*Machine, error) { return m.MutePad(0, true) }, func(p Pad) bool { return p.Mute }},
		{"solo", func() (*Machine, error) { return m.SoloPad(0, true) }, func(p Pad) bool { return p.Solo }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := m.Kit.Pads[0]
			out, err := tc.apply()
			if err != nil {
				t.Fatalf("apply: %v", err)
			}
			if !tc.check(out.Kit.Pads[0]) {
				t.Errorf("edit not applied: %+v", out.Kit.Pads[0])
			}
			if !reflect.DeepEqual(m.Kit.Pads[0], before) {
				t.Errorf("original mutated: %+v -> %+v", before, m.Kit.Pads[0])
			}
		})
	}
}

func TestPadClamps(t *testing.T) {
	m := NewMachine()
	out, _ := m.SetPadLevel(0, 5)
	if out.Kit.Pads[0].Level != 1 {
		t.Errorf("level clamp = %v, want 1", out.Kit.Pads[0].Level)
	}
	out, _ = m.SetPadPan(0, -9)
	if out.Kit.Pads[0].Pan != -1 {
		t.Errorf("pan clamp = %v, want -1", out.Kit.Pads[0].Pan)
	}
	out, _ = m.SetPadDecay(0, -3)
	if out.Kit.Pads[0].Decay != 0 {
		t.Errorf("decay clamp = %v, want 0", out.Kit.Pads[0].Decay)
	}
}

func TestPadOutOfRange(t *testing.T) {
	m := NewMachine()
	for _, pad := range []int{-1, PadCount, 99} {
		if _, err := m.SetPadLevel(pad, 0.5); err == nil {
			t.Errorf("pad %d: expected error", pad)
		}
	}
}

// ---- step edits ------------------------------------------------------------

func TestToggleAndSetStep(t *testing.T) {
	m := NewMachine()
	before := clonePattern(m.Bank.Patterns[0])

	on, err := m.ToggleStep(0, 0, 0)
	if err != nil {
		t.Fatalf("toggle: %v", err)
	}
	if !on.Bank.Patterns[0].Lanes[0][0].On {
		t.Error("cell not turned on")
	}
	if on.Bank.Patterns[0].Lanes[0][0].Vel != defaultVel {
		t.Errorf("vel = %d, want %d", on.Bank.Patterns[0].Lanes[0][0].Vel, defaultVel)
	}
	if !reflect.DeepEqual(m.Bank.Patterns[0], before) {
		t.Error("original mutated by toggle")
	}

	// Toggle again -> off.
	off, _ := on.ToggleStep(0, 0, 0)
	if off.Bank.Patterns[0].Lanes[0][0].On {
		t.Error("cell not turned off")
	}

	// SetStep with explicit velocity, clamped.
	s, _ := m.SetStep(0, 1, 3, true, 200)
	if s.Bank.Patterns[0].Lanes[1][3].Vel != 127 {
		t.Errorf("vel clamp = %d, want 127", s.Bank.Patterns[0].Lanes[1][3].Vel)
	}
	// SetStep off zeroes velocity.
	s2, _ := s.SetStep(0, 1, 3, false, 50)
	if s2.Bank.Patterns[0].Lanes[1][3].On || s2.Bank.Patterns[0].Lanes[1][3].Vel != 0 {
		t.Errorf("off cell = %+v", s2.Bank.Patterns[0].Lanes[1][3])
	}
}

func TestStepBounds(t *testing.T) {
	m := NewMachine()
	cases := [][3]int{{-1, 0, 0}, {0, -1, 0}, {0, 0, -1}, {1, 0, 0}, {0, PadCount, 0}, {0, 0, DefaultSteps}}
	for _, c := range cases {
		if _, err := m.SetStep(c[0], c[1], c[2], true, 100); err == nil {
			t.Errorf("addr %v: expected error", c)
		}
		if _, err := m.ToggleStep(c[0], c[1], c[2]); err == nil {
			t.Errorf("toggle addr %v: expected error", c)
		}
	}
}

// ---- mute / solo query -----------------------------------------------------

func TestAudiblePads(t *testing.T) {
	m := NewMachine()
	// No mute/solo -> all 16.
	if got := len(m.AudiblePads()); got != PadCount {
		t.Errorf("audible = %d, want %d", got, PadCount)
	}
	// Mute pad 0 -> 15.
	mm, _ := m.MutePad(0, true)
	if got := mm.AudiblePads(); len(got) != PadCount-1 || got[0] == 0 {
		t.Errorf("after mute audible = %v", got)
	}
	// Solo pad 5 -> only 5.
	sm, _ := m.SoloPad(5, true)
	got := sm.AudiblePads()
	if len(got) != 1 || got[0] != 5 {
		t.Errorf("after solo audible = %v, want [5]", got)
	}
	// Solo 5 + mute 5 -> none.
	sm2, _ := sm.MutePad(5, true)
	if got := sm2.AudiblePads(); len(got) != 0 {
		t.Errorf("solo+mute audible = %v, want none", got)
	}
}

// ---- choke resolution ------------------------------------------------------

func TestResolveChokes(t *testing.T) {
	m := NewMachine() // pads 2,3 in choke group 1
	// Open hat (3) then closed hat (2): closed wins (last in group).
	got := m.ResolveChokes([]Trigger{{Pad: 3}, {Pad: 2}})
	if !reflect.DeepEqual(got, []int{2}) {
		t.Errorf("hat choke = %v, want [2]", got)
	}
	// Reverse order: open then... only closed listed last wins; here 2 then 3 -> 3.
	got = m.ResolveChokes([]Trigger{{Pad: 2}, {Pad: 3}})
	if !reflect.DeepEqual(got, []int{3}) {
		t.Errorf("hat choke = %v, want [3]", got)
	}
	// Non-choke pads always survive, order preserved.
	got = m.ResolveChokes([]Trigger{{Pad: 0}, {Pad: 1}, {Pad: 3}, {Pad: 2}})
	if !reflect.DeepEqual(got, []int{0, 1, 2}) {
		t.Errorf("mixed choke = %v, want [0 1 2]", got)
	}
	// Out-of-range triggers dropped.
	got = m.ResolveChokes([]Trigger{{Pad: 99}, {Pad: 0}})
	if !reflect.DeepEqual(got, []int{0}) {
		t.Errorf("bad trigger = %v, want [0]", got)
	}
}

// ---- pattern / bank / scene / song -----------------------------------------

func TestPatternManagement(t *testing.T) {
	m := NewMachine()
	// Add pattern.
	m2, err := m.AddPattern("Pattern 2", 32)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if m2.PatternCount() != 2 {
		t.Fatalf("count = %d, want 2", m2.PatternCount())
	}
	if m2.Bank.Patterns[1].Steps != 32 {
		t.Errorf("steps = %d, want 32", m2.Bank.Patterns[1].Steps)
	}
	if m.PatternCount() != 1 {
		t.Error("original bank mutated")
	}

	// Duplicate.
	mset, _ := m2.SetStep(0, 0, 0, true, 90)
	dup, _ := mset.DuplicatePattern(0, "")
	if dup.PatternCount() != 3 {
		t.Fatalf("dup count = %d", dup.PatternCount())
	}
	if !dup.Bank.Patterns[2].Lanes[0][0].On {
		t.Error("dup did not copy steps")
	}
	if dup.Bank.Patterns[2].Name != "Pattern 1 copy" {
		t.Errorf("dup name = %q", dup.Bank.Patterns[2].Name)
	}

	// Delete pattern 0; scene pointing at it clamps to 0.
	del, err := dup.DeletePattern(1)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if del.PatternCount() != 2 {
		t.Errorf("after delete = %d, want 2", del.PatternCount())
	}

	// Can't delete the last pattern.
	one := NewMachine()
	if _, err := one.DeletePattern(0); err == nil {
		t.Error("expected error deleting last pattern")
	}
}

func TestDeletePatternRepointsScenes(t *testing.T) {
	m := NewMachine()
	m, _ = m.AddPattern("P2", 16)
	m, _ = m.AddPattern("P3", 16)
	m, _ = m.AddScene("S2", 1)
	m, _ = m.AddScene("S3", 2)
	// Delete pattern 1: scene pointing at 1 -> 0; scene at 2 -> 1.
	out, _ := m.DeletePattern(1)
	if out.Scenes[1].PatternIndex != 0 {
		t.Errorf("scene1 -> %d, want 0", out.Scenes[1].PatternIndex)
	}
	if out.Scenes[2].PatternIndex != 1 {
		t.Errorf("scene2 -> %d, want 1", out.Scenes[2].PatternIndex)
	}
}

func TestBankCap(t *testing.T) {
	m := NewMachine()
	var err error
	for i := 1; i < MaxPatterns; i++ {
		m, err = m.AddPattern("p", 16)
		if err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	if m.PatternCount() != MaxPatterns {
		t.Fatalf("count = %d", m.PatternCount())
	}
	if _, err := m.AddPattern("over", 16); err == nil {
		t.Error("expected cap error")
	}
}

func TestSceneAndSong(t *testing.T) {
	m := NewMachine()
	m, _ = m.AddPattern("P2", 16)
	m, err := m.AddScene("S2", 1)
	if err != nil {
		t.Fatalf("add scene: %v", err)
	}
	if m.SceneCount() != 2 {
		t.Fatalf("scenes = %d", m.SceneCount())
	}
	// SetScenePattern in range.
	m, err = m.SetScenePattern(0, 1)
	if err != nil {
		t.Fatalf("set scene pattern: %v", err)
	}
	if m.Scenes[0].PatternIndex != 1 {
		t.Errorf("scene0 pattern = %d", m.Scenes[0].PatternIndex)
	}
	// Out of range scene pattern.
	if _, err := m.SetScenePattern(0, 99); err == nil {
		t.Error("expected error")
	}

	// Song order.
	m, err = m.SetSongOrder([]SongEntry{{SceneIndex: 1, Repeat: 4}, {SceneIndex: 0, Repeat: 0}})
	if err != nil {
		t.Fatalf("song: %v", err)
	}
	if len(m.Song.Entries) != 2 || m.Song.Entries[1].Repeat != 1 {
		t.Errorf("song = %+v (repeat should normalise to 1)", m.Song.Entries)
	}
	// Bad scene in song.
	if _, err := m.SetSongOrder([]SongEntry{{SceneIndex: 99, Repeat: 1}}); err == nil {
		t.Error("expected song error")
	}

	// PatternForScene.
	p, ok := m.PatternForScene(0)
	if !ok || p.Name != "P2" {
		t.Errorf("PatternForScene = %q, %v", p.Name, ok)
	}
	if _, ok := m.PatternForScene(99); ok {
		t.Error("PatternForScene(99) should be false")
	}
}

func TestSetSwingAndSteps(t *testing.T) {
	m := NewMachine()
	sw, _ := m.SetSwing(0, 0.9) // clamps to 0.75
	if sw.Bank.Patterns[0].Swing != 0.75 {
		t.Errorf("swing = %v, want 0.75", sw.Bank.Patterns[0].Swing)
	}
	// Mark a step then grow; data preserved.
	m2, _ := m.SetStep(0, 0, 0, true, 100)
	grown, _ := m2.SetSteps(0, 32)
	if grown.Bank.Patterns[0].Steps != 32 {
		t.Fatalf("steps = %d", grown.Bank.Patterns[0].Steps)
	}
	if len(grown.Bank.Patterns[0].Lanes[0]) != 32 {
		t.Errorf("lane len = %d", len(grown.Bank.Patterns[0].Lanes[0]))
	}
	if !grown.Bank.Patterns[0].Lanes[0][0].On {
		t.Error("step lost on grow")
	}
	// Shrink truncates.
	shrunk, _ := grown.SetSteps(0, 16)
	if len(shrunk.Bank.Patterns[0].Lanes[0]) != 16 {
		t.Errorf("shrunk len = %d", len(shrunk.Bank.Patterns[0].Lanes[0]))
	}
	// Odd value snaps to nearest valid.
	snap, _ := m.SetSteps(0, 30)
	if snap.Bank.Patterns[0].Steps != 32 {
		t.Errorf("snap = %d, want 32", snap.Bank.Patterns[0].Steps)
	}
}

// ---- DrumGrid bridge -------------------------------------------------------

func TestDrumGridRoundTripLossless(t *testing.T) {
	m := NewMachine()
	// Make a non-trivial pattern: kick on 0/8, snare on 4/12, hat on every odd, varied vel.
	edits := []struct {
		pad, step, vel int
	}{
		{0, 0, 110}, {0, 8, 100},
		{1, 4, 95}, {1, 12, 90},
		{2, 1, 60}, {2, 3, 70}, {2, 5, 64}, {2, 7, 80},
	}
	for _, e := range edits {
		var err error
		m, err = m.SetStep(0, e.pad, e.step, true, e.vel)
		if err != nil {
			t.Fatalf("setstep: %v", err)
		}
	}
	orig := m.Bank.Patterns[0]
	grid := orig.ToDrumGrid(m.Kit)
	back := PatternFromDrumGrid(grid, m.Kit, orig.Name)

	if back.Steps != orig.Steps {
		t.Fatalf("steps %d -> %d", orig.Steps, back.Steps)
	}
	for pad := 0; pad < PadCount; pad++ {
		for s := 0; s < orig.Steps; s++ {
			a := orig.Lanes[pad][s]
			b := back.Lanes[pad][s]
			if a.On != b.On || a.Vel != b.Vel {
				t.Errorf("pad %d step %d: %+v -> %+v", pad, s, a, b)
			}
		}
	}
}

func TestToDrumGridShape(t *testing.T) {
	m := NewMachine()
	m, _ = m.SetStep(0, 0, 0, true, 100)
	g := m.Bank.Patterns[0].ToDrumGrid(m.Kit)
	if g.Channel != 9 {
		t.Errorf("channel = %d, want 9", g.Channel)
	}
	if g.Steps != DefaultSteps {
		t.Errorf("steps = %d", g.Steps)
	}
	// All 16 pads have GM notes by default -> 16 lanes.
	if len(g.Lanes) != PadCount {
		t.Errorf("lanes = %d, want %d", len(g.Lanes), PadCount)
	}
	// Lanes in pad order: first lane is the kick (note 36).
	if g.Lanes[0].Note != 36 {
		t.Errorf("lane0 note = %d, want 36", g.Lanes[0].Note)
	}
}

func TestDrumcmdStyleTransformViaGrid(t *testing.T) {
	// Simulate what internal/drumcmd does: edit the grid, map back. Here we clear a
	// cell on the grid and confirm it reflects back losslessly.
	m := NewMachine()
	m, _ = m.SetStep(0, 0, 0, true, 100)
	m, _ = m.SetStep(0, 0, 4, true, 100)
	g := m.Bank.Patterns[0].ToDrumGrid(m.Kit)
	g2 := g.SetStep(0, 4, false, 0) // dawmodel's immutable grid edit
	back := PatternFromDrumGrid(*g2, m.Kit, "x")
	if back.Lanes[0][4].On {
		t.Error("grid edit not reflected back")
	}
	if !back.Lanes[0][0].On {
		t.Error("untouched cell lost")
	}
}

func TestPatternFromEmptyGrid(t *testing.T) {
	p := PatternFromDrumGrid(dawmodel.DrumGrid{}, DefaultKit(), "empty")
	if p.Steps != DefaultSteps || len(p.Lanes) != PadCount {
		t.Errorf("empty grid -> %d steps, %d lanes", p.Steps, len(p.Lanes))
	}
}

// ---- JSON: determinism + back-compat --------------------------------------

func TestJSONRoundTrip(t *testing.T) {
	m := NewMachine()
	m, _ = m.SetPadSample(0, "kick.wav")
	m, _ = m.SetStep(0, 0, 0, true, 110)
	m, _ = m.AddPattern("P2", 32)
	m, _ = m.AddScene("S2", 1)

	var buf bytes.Buffer
	if err := m.Save(&buf); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := Load(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Kit.Pads[0].SamplePath != "kick.wav" {
		t.Error("sample path lost")
	}
	if !loaded.Bank.Patterns[0].Lanes[0][0].On {
		t.Error("step lost")
	}
	if loaded.PatternCount() != 2 || loaded.SceneCount() != 2 {
		t.Errorf("counts = %d/%d", loaded.PatternCount(), loaded.SceneCount())
	}
}

func TestJSONDeterministic(t *testing.T) {
	m := NewMachine()
	m, _ = m.SetStep(0, 0, 0, true, 100)
	b1, err := m.MarshalBytes()
	if err != nil {
		t.Fatal(err)
	}
	// Save -> load -> save must be byte-identical (stable).
	loaded, err := Load(bytes.NewReader(b1))
	if err != nil {
		t.Fatal(err)
	}
	b2, err := loaded.MarshalBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b1, b2) {
		t.Errorf("not byte-stable:\n--- first ---\n%s\n--- second ---\n%s", b1, b2)
	}
	// And two fresh machines produce identical bytes.
	b3, _ := NewMachine().MarshalBytes()
	b4, _ := NewMachine().MarshalBytes()
	if !bytes.Equal(b3, b4) {
		t.Error("fresh machines differ")
	}
}

func TestLoadBackCompatAndDegrade(t *testing.T) {
	// Old/partial JSON: no schema, no tempo, short kit, unknown field, lane shorter
	// than steps, scene/song pointing out of range.
	j := []byte(`{
		"name": "Legacy",
		"unknownField": 42,
		"kit": {"pads": [{"name":"K","midiNote":36}]},
		"bank": {"patterns": [{"name":"P","steps":16,"lanes":[[{"on":true}]]}]},
		"scenes": [{"name":"S","patternIndex":9}],
		"song": {"entries":[{"sceneIndex":9,"repeat":0}]}
	}`)
	m, err := Load(bytes.NewReader(j))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if m.SchemaVersion != SchemaVersion {
		t.Errorf("schema defaulted = %d", m.SchemaVersion)
	}
	if m.Tempo != 120 {
		t.Errorf("tempo defaulted = %v", m.Tempo)
	}
	if len(m.Kit.Pads) != PadCount {
		t.Errorf("kit padded to %d, want %d", len(m.Kit.Pads), PadCount)
	}
	// pad0 kept its note; pad1.. filled from defaults.
	if m.Kit.Pads[0].MidiNote != 36 {
		t.Errorf("pad0 note = %d", m.Kit.Pads[0].MidiNote)
	}
	if m.Kit.Pads[1].MidiNote == 0 {
		t.Error("pad1 not defaulted")
	}
	// pad0 level defaulted to unity (was zero/missing).
	if m.Kit.Pads[0].Level != 1 {
		t.Errorf("pad0 level = %v, want 1", m.Kit.Pads[0].Level)
	}
	// Lane padded to 16; the on cell preserved with a default velocity.
	ln := m.Bank.Patterns[0].Lanes[0]
	if len(ln) != 16 {
		t.Errorf("lane len = %d", len(ln))
	}
	if !ln[0].On || ln[0].Vel == 0 {
		t.Errorf("on cell vel not defaulted: %+v", ln[0])
	}
	if len(m.Bank.Patterns[0].Lanes) != PadCount {
		t.Errorf("lanes = %d", len(m.Bank.Patterns[0].Lanes))
	}
	// Scene pattern index clamped into range.
	if m.Scenes[0].PatternIndex != 0 {
		t.Errorf("scene clamped = %d", m.Scenes[0].PatternIndex)
	}
	// Bad song entry dropped, replaced by a default.
	if len(m.Song.Entries) != 1 || m.Song.Entries[0].SceneIndex != 0 {
		t.Errorf("song = %+v", m.Song.Entries)
	}
}

func TestLoadMalformedJSON(t *testing.T) {
	if _, err := Load(bytes.NewReader([]byte("{not json"))); err == nil {
		t.Error("expected JSON syntax error")
	}
}

func TestEmptyMachineNormalizes(t *testing.T) {
	// A totally empty object must become a valid 1-pattern, 1-scene machine.
	m, err := Load(bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatal(err)
	}
	if m.PatternCount() != 1 || m.SceneCount() != 1 || len(m.Kit.Pads) != PadCount {
		t.Errorf("empty not normalised: pats=%d scenes=%d pads=%d", m.PatternCount(), m.SceneCount(), len(m.Kit.Pads))
	}
	if len(m.Song.Entries) != 1 {
		t.Errorf("song = %+v", m.Song.Entries)
	}
}
