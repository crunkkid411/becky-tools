// Package drummachine is the pure-Go, deterministic SPINE of becky's 16-pad,
// Maschine-2-class groovebox. It is the DATA MODEL + LOGIC only — NO GUI, NO
// audio, NO cgo, NO build tags — so it builds and tests green everywhere and the
// GUI/audio layers can be built directly on these types later.
//
// Structure mirrors Maschine 2's real hierarchy so a producer's mental model maps
// 1:1:
//
//	Machine (project)
//	 ├─ Tempo (BPM) + SchemaVersion
//	 ├─ Kit          (16 Pads — a sample + voice settings per pad)
//	 ├─ Bank         (up to 16 Patterns — the pattern bank; each Pattern is a
//	 │                step-sequence: one step-lane per pad)
//	 ├─ Scenes       ([]Scene — each names one Pattern in the bank to play)
//	 └─ Song         (ordered list of Scene indices for song-mode playback)
//
// House rules (becky invariants — see CLAUDE.md):
//   - Immutable: every edit returns a NEW deep-copied value; the receiver is never
//     mutated. (Matches internal/dawmodel.)
//   - Deterministic: same input -> byte-identical machine.json. Fixed ordering, no
//     map-iteration in output.
//   - Degrade-never-crash: a bad index, empty model, or malformed/old JSON yields a
//     typed error or a safe no-op — never a panic. Missing/unknown JSON fields
//     default cleanly.
//
// Bridge: a Pattern converts losslessly (on/off + velocity) to and from
// dawmodel.DrumGrid, so the EXISTING internal/drumcmd transforms (half-time,
// humanize, fill, swing, variations…) apply to a machine pattern unchanged.
package drummachine

import (
	"fmt"

	"becky-go/internal/dawmodel"
	"becky-go/internal/music"
	"becky-go/internal/sampler"
)

// SchemaVersion is the on-disk machine.json schema version. Bump only on a
// breaking change; Load tolerates older/missing values (degrade-never-crash).
const SchemaVersion = 1

// PadCount is the fixed number of pads in a kit (Maschine's 4x4 grid).
const PadCount = 16

// Default pattern sizing and swing bounds.
const (
	DefaultSteps = 16   // one bar of 1/16 cells
	MaxPatterns  = 16   // the pattern bank holds up to 16 patterns
	swingNeutral = 0.5  // 0.5 == no swing (straight)
	swingMax     = 0.75 // hard clamp; beyond this the groove falls apart
	defaultLevel = 1.0  // unity linear gain
	defaultVel   = 100  // a "normal" hit when none supplied
	minSwing     = 0.5
)

// validStepCounts is the set of step lengths a pattern may have (1/16 resolution:
// 1, 2, or 4 bars). SetSteps snaps to the nearest valid value.
var validStepCounts = []int{16, 32, 64}

// Pad is one of the 16 pads: a sample plus its per-voice playback settings. A pad
// is the unit a producer tweaks (swap sample, set level/pan/pitch/decay, assign a
// choke group). It is plain data; the audio engine reads it, this package only
// edits it.
//
// Sound is the rich multisampling model from internal/sampler (velocity layers,
// round-robin variants, choke/off-by, envelope). When non-nil it is the authoritative
// source: SamplePath is kept for backward compatibility with old machine.json files
// and for simple one-sample pads that don't need full SFZ modelling. When both are
// set, Sound wins for playback; SamplePath stays as a human-readable hint.
type Pad struct {
	Index          int            `json:"index"`          // 0..15, fixed position in the 4x4 grid
	Name           string         `json:"name"`           // human label ("Kick", "Snare", …)
	SamplePath     string         `json:"samplePath"`     // path to the sample WAV ("" = use synth/none)
	Sound          *sampler.Sound `json:"sound,omitempty"` // rich multisampling model; nil = simple/none
	Level          float64        `json:"level"`          // linear gain 0..1 (1 = unity)
	Pan            float64        `json:"pan"`            // -1 (hard left) .. +1 (hard right)
	PitchSemitones float64        `json:"pitchSemitones"` // playback transpose in semitones
	Decay          float64        `json:"decay"`          // amp decay in seconds; 0 = one-shot / full length
	ChokeGroup     int            `json:"chokeGroup"`     // 0 = none; pads sharing a non-zero group cut each other
	Mute           bool           `json:"mute"`           // this pad is silenced
	Solo           bool           `json:"solo"`           // this pad is soloed (any solo -> only soloed pads sound)
	MidiNote       int            `json:"midiNote"`       // GM percussion note for channel-9 mapping
}

// Kit is the sound set: exactly 16 pads. The slice is always length PadCount; Pads
// is kept as a slice (not array) so JSON round-trips cleanly and old files with a
// short/long list still load (Load pads/truncates to 16).
type Kit struct {
	Name string `json:"name"`
	Pads []Pad  `json:"pads"`
}

// Step is one cell in a pad's step-lane. v1 = on + velocity; Probability/micro-
// timing are intentionally left for a later schema bump (the struct has room).
type Step struct {
	On  bool `json:"on"`
	Vel int  `json:"vel"` // 1..127 when On; 0 when off
}

// Pattern is one step-sequence over the 16 pads: Lanes[pad] is that pad's row of
// Steps (length == Steps). Swing biases the off-beat 1/16s. A pattern is what a
// scene plays.
type Pattern struct {
	Name  string   `json:"name"`
	Steps int      `json:"steps"` // cells per lane (16/32/64)
	Swing float64  `json:"swing"` // 0.5 (straight) .. 0.75
	Lanes [][]Step `json:"lanes"` // [PadCount][Steps]; Lanes[pad][step]
}

// Bank is the pattern bank: up to MaxPatterns patterns the producer switches
// between live.
type Bank struct {
	Patterns []Pattern `json:"patterns"`
}

// Scene names one pattern in the bank to play. v1 is single-group (one pattern per
// scene); multi-group scenes (a pattern per pad-group played together) are a
// documented later extension.
type Scene struct {
	Name         string `json:"name"`
	PatternIndex int    `json:"patternIndex"` // index into Bank.Patterns
}

// SongEntry is one step of song-mode playback: a scene index plus how many times to
// repeat it before advancing.
type SongEntry struct {
	SceneIndex int `json:"sceneIndex"`
	Repeat     int `json:"repeat"` // >=1; how many times to play before advancing
}

// Song is the ordered scene list for song-mode playback.
type Song struct {
	Entries []SongEntry `json:"entries"`
}

// Machine is the top-level project that saves/loads as machine.json. Treated as
// immutable — every edit method returns a new deep-copied Machine.
type Machine struct {
	SchemaVersion int     `json:"schemaVersion"`
	Name          string  `json:"name"`
	Tempo         float64 `json:"tempo"` // BPM
	Kit           Kit     `json:"kit"`
	Bank          Bank    `json:"bank"`
	Scenes        []Scene `json:"scenes"`
	Song          Song    `json:"song"`
}

// gmDefaults are the GM-percussion note + label assigned to each pad by DefaultKit.
// Index 0 is the bottom-left pad (kick); a classic 16-pad GM-ish layout.
var gmDefaults = [PadCount]struct {
	name string
	note int
}{
	{"Kick", 36},       // 0
	{"Snare", 38},      // 1
	{"Closed Hat", 42}, // 2
	{"Open Hat", 46},   // 3
	{"Clap", 39},       // 4
	{"Rim", 37},        // 5
	{"Low Tom", 41},    // 6
	{"Mid Tom", 45},    // 7
	{"Hi Tom", 48},     // 8
	{"Crash", 49},      // 9
	{"Ride", 51},       // 10
	{"Shaker", 70},     // 11
	{"Tambourine", 54}, // 12
	{"Cowbell", 56},    // 13
	{"Conga", 63},      // 14
	{"Perc", 75},       // 15
}

// chokeGroupOpenClosed is the choke group shared by the open + closed hats so a
// closed hat cuts an open hat (classic hi-hat behaviour) out of the box.
const chokeGroupHats = 1

// DefaultKit returns a sensible 16-pad GM-mapped kit at unity level, centre pan,
// no pitch shift, one-shot decay. The closed + open hats share a choke group.
func DefaultKit() Kit {
	pads := make([]Pad, PadCount)
	for i := 0; i < PadCount; i++ {
		choke := 0
		// Pads 2 (closed hat) and 3 (open hat) choke each other.
		if i == 2 || i == 3 {
			choke = chokeGroupHats
		}
		pads[i] = Pad{
			Index:      i,
			Name:       gmDefaults[i].name,
			Level:      defaultLevel,
			Pan:        0,
			Decay:      0,
			ChokeGroup: choke,
			MidiNote:   gmDefaults[i].note,
		}
	}
	return Kit{Name: "Default Kit", Pads: pads}
}

// emptyPattern builds a fresh pattern of n steps with all cells off.
func emptyPattern(name string, n int) Pattern {
	n = snapSteps(n)
	lanes := make([][]Step, PadCount)
	for p := range lanes {
		lanes[p] = make([]Step, n)
	}
	return Pattern{Name: name, Steps: n, Swing: swingNeutral, Lanes: lanes}
}

// NewMachine returns a fresh project: a DefaultKit, one empty 16-step pattern in
// the bank, and one scene pointing at it. Tempo defaults to 120 BPM.
func NewMachine() *Machine {
	m := &Machine{
		SchemaVersion: SchemaVersion,
		Name:          "Untitled",
		Tempo:         120,
		Kit:           DefaultKit(),
		Bank:          Bank{Patterns: []Pattern{emptyPattern("Pattern 1", DefaultSteps)}},
		Scenes:        []Scene{{Name: "Scene 1", PatternIndex: 0}},
		Song:          Song{Entries: []SongEntry{{SceneIndex: 0, Repeat: 1}}},
	}
	return m
}

// ---- cloning (the immutability boundary) -----------------------------------

func (m *Machine) clone() *Machine {
	out := *m
	out.Kit = cloneKit(m.Kit)
	out.Bank = cloneBank(m.Bank)
	out.Scenes = append([]Scene(nil), m.Scenes...)
	out.Song = cloneSong(m.Song)
	return &out
}

func cloneKit(k Kit) Kit {
	pads := make([]Pad, len(k.Pads))
	copy(pads, k.Pads)
	// Deep-copy Sound pointers so edits never alias across machines.
	for i, p := range pads {
		if p.Sound != nil {
			cp := *p.Sound
			pads[i].Sound = &cp
		}
	}
	k.Pads = pads
	return k
}

func cloneBank(b Bank) Bank {
	pats := make([]Pattern, len(b.Patterns))
	for i, p := range b.Patterns {
		pats[i] = clonePattern(p)
	}
	b.Patterns = pats
	return b
}

func clonePattern(p Pattern) Pattern {
	lanes := make([][]Step, len(p.Lanes))
	for i, ln := range p.Lanes {
		lanes[i] = append([]Step(nil), ln...)
	}
	p.Lanes = lanes
	return p
}

func cloneSong(s Song) Song {
	s.Entries = append([]SongEntry(nil), s.Entries...)
	return s
}

// ---- helpers ---------------------------------------------------------------

// snapSteps snaps n to the nearest valid step count (16/32/64), defaulting to 16.
func snapSteps(n int) int {
	best := DefaultSteps
	bestDist := -1
	for _, v := range validStepCounts {
		d := n - v
		if d < 0 {
			d = -d
		}
		if bestDist < 0 || d < bestDist {
			bestDist = d
			best = v
		}
	}
	return best
}

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampVel(v int) int {
	if v < 1 {
		return 1
	}
	if v > 127 {
		return 127
	}
	return v
}

// patValid reports whether pat is an in-range pattern index.
func (m *Machine) patValid(pat int) bool {
	return pat >= 0 && pat < len(m.Bank.Patterns)
}

// PatternCount returns the number of patterns in the bank.
func (m *Machine) PatternCount() int { return len(m.Bank.Patterns) }

// SceneCount returns the number of scenes.
func (m *Machine) SceneCount() int { return len(m.Scenes) }

// ---- pad edits (immutable) -------------------------------------------------

// padErr returns a typed out-of-range error for pad index pad.
func padErr(pad int) error {
	return fmt.Errorf("drummachine: pad index %d out of range [0,%d)", pad, PadCount)
}

// SetPadSample returns a new Machine with pad's SamplePath set. Out-of-range pad ->
// (unchanged copy, error).
func (m *Machine) SetPadSample(pad int, path string) (*Machine, error) {
	out := m.clone()
	if pad < 0 || pad >= len(out.Kit.Pads) {
		return out, padErr(pad)
	}
	out.Kit.Pads[pad].SamplePath = path
	return out, nil
}

// SetPadLevel sets linear level (clamped 0..1).
func (m *Machine) SetPadLevel(pad int, level float64) (*Machine, error) {
	out := m.clone()
	if pad < 0 || pad >= len(out.Kit.Pads) {
		return out, padErr(pad)
	}
	out.Kit.Pads[pad].Level = clampFloat(level, 0, 1)
	return out, nil
}

// SetPadPan sets pan (clamped -1..1).
func (m *Machine) SetPadPan(pad int, pan float64) (*Machine, error) {
	out := m.clone()
	if pad < 0 || pad >= len(out.Kit.Pads) {
		return out, padErr(pad)
	}
	out.Kit.Pads[pad].Pan = clampFloat(pan, -1, 1)
	return out, nil
}

// SetPadPitch sets the playback transpose in semitones (clamped -48..48).
func (m *Machine) SetPadPitch(pad int, semis float64) (*Machine, error) {
	out := m.clone()
	if pad < 0 || pad >= len(out.Kit.Pads) {
		return out, padErr(pad)
	}
	out.Kit.Pads[pad].PitchSemitones = clampFloat(semis, -48, 48)
	return out, nil
}

// SetPadDecay sets amp decay in seconds (negative -> 0 = one-shot).
func (m *Machine) SetPadDecay(pad int, seconds float64) (*Machine, error) {
	out := m.clone()
	if pad < 0 || pad >= len(out.Kit.Pads) {
		return out, padErr(pad)
	}
	if seconds < 0 {
		seconds = 0
	}
	out.Kit.Pads[pad].Decay = seconds
	return out, nil
}

// SetPadChokeGroup sets the choke group (negative -> 0 = none).
func (m *Machine) SetPadChokeGroup(pad, group int) (*Machine, error) {
	out := m.clone()
	if pad < 0 || pad >= len(out.Kit.Pads) {
		return out, padErr(pad)
	}
	if group < 0 {
		group = 0
	}
	out.Kit.Pads[pad].ChokeGroup = group
	return out, nil
}

// MutePad sets the pad's mute flag.
func (m *Machine) MutePad(pad int, mute bool) (*Machine, error) {
	out := m.clone()
	if pad < 0 || pad >= len(out.Kit.Pads) {
		return out, padErr(pad)
	}
	out.Kit.Pads[pad].Mute = mute
	return out, nil
}

// SoloPad sets the pad's solo flag.
func (m *Machine) SoloPad(pad int, solo bool) (*Machine, error) {
	out := m.clone()
	if pad < 0 || pad >= len(out.Kit.Pads) {
		return out, padErr(pad)
	}
	out.Kit.Pads[pad].Solo = solo
	return out, nil
}

// AudiblePads returns the indices of pads that would actually sound: if any pad is
// soloed, only soloed-and-not-muted pads; otherwise all not-muted pads. Pure query
// (used by the engine + for testing the mute/solo logic).
func (m *Machine) AudiblePads() []int {
	anySolo := false
	for _, p := range m.Kit.Pads {
		if p.Solo {
			anySolo = true
			break
		}
	}
	var out []int
	for _, p := range m.Kit.Pads {
		if p.Mute {
			continue
		}
		if anySolo && !p.Solo {
			continue
		}
		out = append(out, p.Index)
	}
	return out
}

// ---- step edits (immutable, bounds-checked) --------------------------------

// stepErr returns a typed out-of-range error for a (pattern,pad,step) address.
func stepErr(pat, pad, step int) error {
	return fmt.Errorf("drummachine: step address out of range (pattern=%d pad=%d step=%d)", pat, pad, step)
}

// stepInRange reports whether (pat,pad,step) addresses a real cell.
func (m *Machine) stepInRange(pat, pad, step int) bool {
	if !m.patValid(pat) {
		return false
	}
	p := m.Bank.Patterns[pat]
	if pad < 0 || pad >= len(p.Lanes) {
		return false
	}
	return step >= 0 && step < len(p.Lanes[pad])
}

// ToggleStep flips one cell on/off and returns a new Machine. Turning a cell on
// uses a default velocity. Out-of-range -> (unchanged copy, error).
func (m *Machine) ToggleStep(pat, pad, step int) (*Machine, error) {
	out := m.clone()
	if !out.stepInRange(pat, pad, step) {
		return out, stepErr(pat, pad, step)
	}
	cell := &out.Bank.Patterns[pat].Lanes[pad][step]
	if cell.On {
		cell.On = false
		cell.Vel = 0
	} else {
		cell.On = true
		cell.Vel = defaultVel
	}
	return out, nil
}

// SetStep sets one cell explicitly and returns a new Machine. When on, vel is
// clamped to 1..127 (<=0 -> default); when off, Vel is zeroed. Out-of-range ->
// (unchanged copy, error).
func (m *Machine) SetStep(pat, pad, step int, on bool, vel int) (*Machine, error) {
	out := m.clone()
	if !out.stepInRange(pat, pad, step) {
		return out, stepErr(pat, pad, step)
	}
	cell := &out.Bank.Patterns[pat].Lanes[pad][step]
	cell.On = on
	if on {
		if vel <= 0 {
			vel = defaultVel
		}
		cell.Vel = clampVel(vel)
	} else {
		cell.Vel = 0
	}
	return out, nil
}

// ---- pattern / bank / scene / song management (immutable) ------------------

// SetSwing sets a pattern's swing (clamped 0.5..0.75).
func (m *Machine) SetSwing(pat int, swing float64) (*Machine, error) {
	out := m.clone()
	if !out.patValid(pat) {
		return out, fmt.Errorf("drummachine: pattern index %d out of range", pat)
	}
	out.Bank.Patterns[pat].Swing = clampFloat(swing, minSwing, swingMax)
	return out, nil
}

// SetSteps resizes a pattern to n steps (snapped to 16/32/64). Growing pads each
// lane with off cells; shrinking truncates. Returns a new Machine.
func (m *Machine) SetSteps(pat, n int) (*Machine, error) {
	out := m.clone()
	if !out.patValid(pat) {
		return out, fmt.Errorf("drummachine: pattern index %d out of range", pat)
	}
	n = snapSteps(n)
	p := &out.Bank.Patterns[pat]
	for li := range p.Lanes {
		ln := p.Lanes[li]
		if n <= len(ln) {
			p.Lanes[li] = append([]Step(nil), ln[:n]...)
		} else {
			grown := make([]Step, n)
			copy(grown, ln)
			p.Lanes[li] = grown
		}
	}
	p.Steps = n
	return out, nil
}

// AddPattern appends a new empty pattern (named) to the bank, capped at
// MaxPatterns. At the cap it returns (unchanged copy, error).
func (m *Machine) AddPattern(name string, steps int) (*Machine, error) {
	out := m.clone()
	if len(out.Bank.Patterns) >= MaxPatterns {
		return out, fmt.Errorf("drummachine: pattern bank full (max %d)", MaxPatterns)
	}
	out.Bank.Patterns = append(out.Bank.Patterns, emptyPattern(name, steps))
	return out, nil
}

// DuplicatePattern appends a deep copy of pattern pat (named name; "" -> "<src> copy").
func (m *Machine) DuplicatePattern(pat int, name string) (*Machine, error) {
	out := m.clone()
	if !out.patValid(pat) {
		return out, fmt.Errorf("drummachine: pattern index %d out of range", pat)
	}
	if len(out.Bank.Patterns) >= MaxPatterns {
		return out, fmt.Errorf("drummachine: pattern bank full (max %d)", MaxPatterns)
	}
	dup := clonePattern(out.Bank.Patterns[pat])
	if name == "" {
		name = dup.Name + " copy"
	}
	dup.Name = name
	out.Bank.Patterns = append(out.Bank.Patterns, dup)
	return out, nil
}

// DeletePattern removes pattern pat. Scenes pointing at it are clamped to a valid
// index; scenes after it shift down. Refuses to delete the last pattern (a machine
// always has at least one) -> (unchanged copy, error).
func (m *Machine) DeletePattern(pat int) (*Machine, error) {
	out := m.clone()
	if !out.patValid(pat) {
		return out, fmt.Errorf("drummachine: pattern index %d out of range", pat)
	}
	if len(out.Bank.Patterns) <= 1 {
		return out, fmt.Errorf("drummachine: cannot delete the last pattern")
	}
	out.Bank.Patterns = append(out.Bank.Patterns[:pat], out.Bank.Patterns[pat+1:]...)
	// Re-point scenes: indices after pat shift down; an index == pat clamps to 0.
	for i := range out.Scenes {
		switch {
		case out.Scenes[i].PatternIndex > pat:
			out.Scenes[i].PatternIndex--
		case out.Scenes[i].PatternIndex == pat:
			out.Scenes[i].PatternIndex = 0
		}
	}
	return out, nil
}

// AddScene appends a scene pointing at pattern patternIndex (clamped into range).
func (m *Machine) AddScene(name string, patternIndex int) (*Machine, error) {
	out := m.clone()
	if len(out.Bank.Patterns) == 0 {
		return out, fmt.Errorf("drummachine: no patterns to point a scene at")
	}
	if patternIndex < 0 {
		patternIndex = 0
	}
	if patternIndex >= len(out.Bank.Patterns) {
		patternIndex = len(out.Bank.Patterns) - 1
	}
	out.Scenes = append(out.Scenes, Scene{Name: name, PatternIndex: patternIndex})
	return out, nil
}

// SetScenePattern points scene sc at pattern patternIndex (must be in range).
func (m *Machine) SetScenePattern(sc, patternIndex int) (*Machine, error) {
	out := m.clone()
	if sc < 0 || sc >= len(out.Scenes) {
		return out, fmt.Errorf("drummachine: scene index %d out of range", sc)
	}
	if !out.patValid(patternIndex) {
		return out, fmt.Errorf("drummachine: pattern index %d out of range", patternIndex)
	}
	out.Scenes[sc].PatternIndex = patternIndex
	return out, nil
}

// SetSongOrder replaces the song's entry list. Entries referencing an out-of-range
// scene are rejected; repeat <1 is normalised to 1. Returns a new Machine.
func (m *Machine) SetSongOrder(entries []SongEntry) (*Machine, error) {
	out := m.clone()
	norm := make([]SongEntry, 0, len(entries))
	for _, e := range entries {
		if e.SceneIndex < 0 || e.SceneIndex >= len(out.Scenes) {
			return out, fmt.Errorf("drummachine: song entry scene index %d out of range", e.SceneIndex)
		}
		if e.Repeat < 1 {
			e.Repeat = 1
		}
		norm = append(norm, e)
	}
	out.Song.Entries = norm
	return out, nil
}

// PatternForScene returns a copy of the pattern played by scene sc, or
// (zero, false) if the scene or its pattern is out of range.
func (m *Machine) PatternForScene(sc int) (Pattern, bool) {
	if sc < 0 || sc >= len(m.Scenes) {
		return Pattern{}, false
	}
	pi := m.Scenes[sc].PatternIndex
	if !m.patValid(pi) {
		return Pattern{}, false
	}
	return clonePattern(m.Bank.Patterns[pi]), true
}

// ---- choke resolution ------------------------------------------------------

// Trigger is a pad being hit at the same instant (used by ResolveChokes). Order in
// the input slice is the trigger order; later triggers in the same choke group cut
// earlier ones.
type Trigger struct {
	Pad int // pad index
}

// ResolveChokes applies choke-group cutoffs to a set of simultaneous triggers and
// returns the pads that actually sound, in the SAME order they appear in the input
// (deterministic).
//
// Choke model — single source of truth is sampler.Sound (when non-nil):
//   - Sound.ChokeGroup: pads sharing a non-zero group use last-wins (same group cuts
//     the earlier one). Mirrors SFZ `group` opcode.
//   - Sound.OffBy: a triggered pad is immediately cut by any later trigger whose pad
//     belongs to any listed off_by group. Mirrors SFZ `off_by` opcode.
//
// Fallback: if Sound is nil, the pad-level ChokeGroup field is used with the same
// last-wins rule (backward compatibility with old kits that don't have a Sound).
//
// Pads in choke group 0 (none) never choke and are always kept. A pad index that is
// out of range is dropped (degrade-never-crash). The input is not mutated.
func (m *Machine) ResolveChokes(triggers []Trigger) []int {
	// padChokeGroup returns the effective choke group for a pad (Sound wins over Pad.ChokeGroup).
	padChokeGroup := func(pad int) int {
		p := m.Kit.Pads[pad]
		if p.Sound != nil {
			return p.Sound.ChokeGroup
		}
		return p.ChokeGroup
	}
	// padOffBy returns the off_by group list for a pad (Sound only; Pad has none).
	padOffBy := func(pad int) []int {
		p := m.Kit.Pads[pad]
		if p.Sound != nil {
			return p.Sound.OffBy
		}
		return nil
	}

	// Pass 1: for each choke group, record the trigger-list position of the LAST triggered pad.
	lastInGroup := map[int]int{} // chokeGroup -> position of last trigger in that group
	for i, t := range triggers {
		if t.Pad < 0 || t.Pad >= len(m.Kit.Pads) {
			continue
		}
		g := padChokeGroup(t.Pad)
		if g == 0 {
			continue
		}
		lastInGroup[g] = i
	}

	// Pass 2: keep only triggers that are not choked.
	// A trigger at position i is choked when:
	//  (a) its choke group is non-zero AND it is not the last in that group (last-wins), OR
	//  (b) a LATER trigger has it in its off_by list (off_by kills the earlier one).
	killed := make([]bool, len(triggers))
	for i, t := range triggers {
		if t.Pad < 0 || t.Pad >= len(m.Kit.Pads) {
			killed[i] = true
			continue
		}
		g := padChokeGroup(t.Pad)
		if g != 0 && lastInGroup[g] != i {
			killed[i] = true // later pad in same group chokes this one
		}
	}
	// Apply off_by: later triggers may kill earlier ones that belong to the listed groups.
	for later, t := range triggers {
		if t.Pad < 0 || t.Pad >= len(m.Kit.Pads) {
			continue
		}
		for _, offGrp := range padOffBy(t.Pad) {
			if offGrp == 0 {
				continue
			}
			for earlier := 0; earlier < later; earlier++ {
				if killed[earlier] {
					continue
				}
				if t2 := triggers[earlier]; t2.Pad >= 0 && t2.Pad < len(m.Kit.Pads) {
					if padChokeGroup(t2.Pad) == offGrp {
						killed[earlier] = true
					}
				}
			}
		}
	}

	out := make([]int, 0, len(triggers))
	for i, t := range triggers {
		if !killed[i] && t.Pad >= 0 && t.Pad < len(m.Kit.Pads) {
			out = append(out, t.Pad)
		}
	}
	return out
}

// ---- bridge to dawmodel.DrumGrid (so internal/drumcmd transforms apply) ----

// ToDrumGrid renders a pattern as a dawmodel.DrumGrid: one Lane per pad that has at
// least one MIDI note assigned, keyed by the pad's MidiNote, with on/velocity
// copied per step. The grid is one "bar" wide covering all Steps (StepTicks at the
// becky-compose 1/16 resolution, channel 9 = GM drums). Lossless for on/off+vel.
//
// Lanes are emitted in PAD ORDER (0..15) — deterministic, independent of map
// iteration. Pads without a MidiNote (<=0) are skipped (no GM key to address).
func (p Pattern) ToDrumGrid(kit Kit) dawmodel.DrumGrid {
	steps := p.Steps
	if steps <= 0 {
		steps = DefaultSteps
	}
	g := dawmodel.DrumGrid{
		Steps:     DefaultSteps,
		Bars:      bars(steps),
		StepTicks: stepTicks(),
		Channel:   9,
	}
	for pad := 0; pad < PadCount && pad < len(p.Lanes); pad++ {
		note := padNote(kit, pad)
		if note <= 0 {
			continue
		}
		lane := dawmodel.Lane{
			Name: name(kit, pad),
			Note: note,
			On:   make([]bool, steps),
			Vel:  make([]int, steps),
		}
		for s := 0; s < steps && s < len(p.Lanes[pad]); s++ {
			cell := p.Lanes[pad][s]
			lane.On[s] = cell.On
			if cell.On {
				lane.Vel[s] = clampVel(cell.Vel)
			}
		}
		// Every assigned pad becomes a lane (an empty lane is still addressable).
		g.Lanes = append(g.Lanes, lane)
	}
	return g
}

// PatternFromDrumGrid maps a DrumGrid (e.g. the output of an internal/drumcmd
// transform) back onto a pattern over the given kit: each grid lane's Note is
// matched to the pad whose MidiNote equals it, and that pad's step-lane is filled
// from the grid lane's on/vel. Pads with no matching grid lane are left empty.
// Lossless for on/off+velocity on the round-trip ToDrumGrid -> transform ->
// PatternFromDrumGrid. The grid's total cells (Steps*Bars) become the pattern's
// step count (snapped to 16/32/64).
//
// name carries through to the pattern name. The kit is read-only (note->pad map).
func PatternFromDrumGrid(g dawmodel.DrumGrid, kit Kit, name string) Pattern {
	cells := g.Steps * g.Bars
	if cells <= 0 {
		// Fall back to the longest lane present.
		for _, ln := range g.Lanes {
			if len(ln.On) > cells {
				cells = len(ln.On)
			}
		}
	}
	if cells <= 0 {
		cells = DefaultSteps
	}
	n := snapSteps(cells)
	p := emptyPattern(name, n)
	// Build note -> pad index from the kit (first pad wins on a duplicate note).
	noteToPad := map[int]int{}
	for i, pad := range kit.Pads {
		if pad.MidiNote > 0 {
			if _, exists := noteToPad[pad.MidiNote]; !exists {
				noteToPad[pad.MidiNote] = i
			}
		}
	}
	for _, ln := range g.Lanes {
		pad, ok := noteToPad[ln.Note]
		if !ok || pad < 0 || pad >= len(p.Lanes) {
			continue
		}
		for s := 0; s < n && s < len(ln.On); s++ {
			if ln.On[s] {
				vel := defaultVel
				if s < len(ln.Vel) && ln.Vel[s] > 0 {
					vel = clampVel(ln.Vel[s])
				}
				p.Lanes[pad][s] = Step{On: true, Vel: vel}
			}
		}
	}
	return p
}

// bars returns the number of 16-step bars covering steps (>=1).
func bars(steps int) int {
	if steps <= 0 {
		return 1
	}
	b := steps / DefaultSteps
	if steps%DefaultSteps != 0 {
		b++
	}
	if b < 1 {
		b = 1
	}
	return b
}

// stepTicks is the tick length of one 1/16 cell at the becky-compose resolution.
func stepTicks() int { return music.StepTicks }

// padNote returns the GM MIDI note for pad in kit (0 if out of range).
func padNote(kit Kit, pad int) int {
	if pad < 0 || pad >= len(kit.Pads) {
		return 0
	}
	return kit.Pads[pad].MidiNote
}

// name returns the pad's label in kit (or a fallback).
func name(kit Kit, pad int) string {
	if pad < 0 || pad >= len(kit.Pads) {
		return fmt.Sprintf("pad%d", pad)
	}
	if kit.Pads[pad].Name != "" {
		return kit.Pads[pad].Name
	}
	return fmt.Sprintf("pad%d", pad)
}
