// Package sampler defines the multisampling "Sound" model that sits behind every
// drum pad — the SFZ-aligned data model from SPEC-BECKY-DRUM.md §1.
//
// A pad triggers a Sound. A Sound is one or more velocity Layers; each Layer holds
// a set of round-robin sample Variants. This mirrors the open SFZ format
// (https://sfzformat.com/) so that kit import (internal/kitimport) is a direct
// opcode-by-opcode translation rather than a lossy re-interpretation.
//
// Design rules (per CLAUDE.md): pure Go, offline, deterministic, degrade-never-crash.
// Validation clamps out-of-range values instead of erroring; nothing here panics.
package sampler

import (
	"encoding/json"
	"os"
	"strings"
)

// ---------------------------------------------------------------------------
// Enums — marshalled as readable strings, not integers, so saved kits are
// human-readable and stable across versions.
// ---------------------------------------------------------------------------

// LoopMode mirrors the SFZ `loop_mode` opcode
// (https://sfzformat.com/opcodes/loop_mode/).
type LoopMode int

const (
	// NoLoop plays the region once, honoring note-off (SFZ `no_loop`).
	NoLoop LoopMode = iota
	// OneShot plays the whole region ignoring note-off — the drum default
	// (SFZ `one_shot`).
	OneShot
	// LoopContinuous loops between LoopStart/LoopEnd for the note's duration
	// (SFZ `loop_continuous`).
	LoopContinuous
	// LoopSustain loops only while the key is held, then plays to the end
	// (SFZ `loop_sustain`).
	LoopSustain
)

var loopModeNames = map[LoopMode]string{
	NoLoop:         "no_loop",
	OneShot:        "one_shot",
	LoopContinuous: "loop_continuous",
	LoopSustain:    "loop_sustain",
}

// loopModeByName accepts the SFZ token (canonical) and a couple of forgiving
// aliases so hand-written kits still parse.
var loopModeByName = map[string]LoopMode{
	"no_loop":         NoLoop,
	"noloop":          NoLoop,
	"one_shot":        OneShot,
	"oneshot":         OneShot,
	"loop_continuous": LoopContinuous,
	"loopcontinuous":  LoopContinuous,
	"loop_sustain":    LoopSustain,
	"loopsustain":     LoopSustain,
}

// String returns the SFZ token for the mode.
func (m LoopMode) String() string {
	if s, ok := loopModeNames[m]; ok {
		return s
	}
	return "no_loop"
}

// MarshalJSON writes the readable SFZ token.
func (m LoopMode) MarshalJSON() ([]byte, error) {
	return json.Marshal(m.String())
}

// UnmarshalJSON accepts the SFZ token (or a forgiving alias); unknown values
// degrade to NoLoop rather than erroring.
func (m *LoopMode) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if v, ok := loopModeByName[strings.ToLower(strings.TrimSpace(s))]; ok {
		*m = v
		return nil
	}
	*m = NoLoop
	return nil
}

// RRMode selects how a Layer cycles its round-robin variants.
type RRMode int

const (
	// Sequential walks the variants in order via a per-Sound counter
	// (SFZ `seq_length`/`seq_position`).
	Sequential RRMode = iota
	// Random picks a variant by a random band (SFZ `lorand`/`hirand`).
	Random
)

var rrModeNames = map[RRMode]string{
	Sequential: "sequential",
	Random:     "random",
}

var rrModeByName = map[string]RRMode{
	"sequential":  Sequential,
	"seq":         Sequential,
	"round_robin": Sequential,
	"random":      Random,
	"rand":        Random,
}

// String returns the readable token.
func (m RRMode) String() string {
	if s, ok := rrModeNames[m]; ok {
		return s
	}
	return "sequential"
}

// MarshalJSON writes the readable token.
func (m RRMode) MarshalJSON() ([]byte, error) {
	return json.Marshal(m.String())
}

// UnmarshalJSON accepts the readable token; unknown values degrade to Sequential.
func (m *RRMode) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if v, ok := rrModeByName[strings.ToLower(strings.TrimSpace(s))]; ok {
		*m = v
		return nil
	}
	*m = Sequential
	return nil
}

// ChokeMode controls how a choke cuts conflicting voices.
type ChokeMode int

const (
	// Fast cuts the choked voice immediately (closed-hat-cuts-open-hat).
	Fast ChokeMode = iota
	// Normal applies a short release before cutting (less clicky).
	Normal
)

var chokeModeNames = map[ChokeMode]string{
	Fast:   "fast",
	Normal: "normal",
}

var chokeModeByName = map[string]ChokeMode{
	"fast":   Fast,
	"normal": Normal,
}

// String returns the readable token.
func (m ChokeMode) String() string {
	if s, ok := chokeModeNames[m]; ok {
		return s
	}
	return "fast"
}

// MarshalJSON writes the readable token.
func (m ChokeMode) MarshalJSON() ([]byte, error) {
	return json.Marshal(m.String())
}

// UnmarshalJSON accepts the readable token; unknown values degrade to Fast.
func (m *ChokeMode) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if v, ok := chokeModeByName[strings.ToLower(strings.TrimSpace(s))]; ok {
		*m = v
		return nil
	}
	*m = Fast
	return nil
}

// EnvType selects the amplitude-envelope shape the engine applies.
type EnvType int

const (
	// EnvOneshot plays the sample to its end and ignores note-off (drum default);
	// the engine still applies AmpEnv.R as a release ramp on choke/steal (declick).
	EnvOneshot EnvType = iota
	// EnvAHD is attack-hold-decay with no sustain — a one-shot that can be shortened
	// ("tighten the snare"): A up, hold H, then decay D to silence.
	EnvAHD
	// EnvADSR is the full sustained envelope for melodic/piano-roll content; the
	// voice holds at S until note-off, then releases over R.
	EnvADSR
)

var envTypeNames = map[EnvType]string{EnvOneshot: "oneshot", EnvAHD: "ahd", EnvADSR: "adsr"}
var envTypeByName = map[string]EnvType{"oneshot": EnvOneshot, "ahd": EnvAHD, "adsr": EnvADSR}

// String returns the readable token.
func (t EnvType) String() string {
	if s, ok := envTypeNames[t]; ok {
		return s
	}
	return "oneshot"
}

// MarshalJSON writes the readable token.
func (t EnvType) MarshalJSON() ([]byte, error) { return json.Marshal(t.String()) }

// UnmarshalJSON accepts the readable token; unknown values degrade to EnvOneshot.
func (t *EnvType) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if v, ok := envTypeByName[strings.ToLower(strings.TrimSpace(s))]; ok {
		*t = v
		return nil
	}
	*t = EnvOneshot
	return nil
}

// AmpEnv is a per-Sound amplitude envelope. Times A/H/D/R are in seconds; S is the
// sustain level 0..1 (ADSR only). The zero value is a pure one-shot (no shaping),
// which is the right default for an untouched drum one-shot.
type AmpEnv struct {
	Type EnvType `json:"type"`
	A    float64 `json:"a,omitempty"`
	H    float64 `json:"h,omitempty"`
	D    float64 `json:"d,omitempty"`
	S    float64 `json:"s,omitempty"`
	R    float64 `json:"r,omitempty"`
}

// Normalize clamps the envelope to sane non-negative times and a 0..1 sustain.
func (e AmpEnv) Normalize() AmpEnv {
	out := e
	if out.A < 0 {
		out.A = 0
	}
	if out.H < 0 {
		out.H = 0
	}
	if out.D < 0 {
		out.D = 0
	}
	if out.R < 0 {
		out.R = 0
	}
	out.S = clampFloat(out.S, 0, 1)
	return out
}

// ---------------------------------------------------------------------------
// Core model
// ---------------------------------------------------------------------------

// DefaultKeycenter is the SFZ default for `pitch_keycenter` (middle C, MIDI 60).
const DefaultKeycenter = 60

// Variant is a single sample with its in-sample edits. SFZ opcode mapping:
//
//	SamplePath     -> sample
//	StartFrame     -> offset
//	EndFrame       -> end
//	LoopMode       -> loop_mode
//	LoopStart      -> loop_start
//	LoopEnd        -> loop_end
//	PitchKeycenter -> pitch_keycenter (default 60)
//	Transpose      -> transpose (semitones)
//	Tune           -> tune (cents)
//	Gain           -> volume (dB; 0 = unity)
//	Pan            -> pan (-1..1; SFZ pan is -100..100, see kitimport)
//
// Missing indicates the resolved SamplePath did not exist on disk at import time.
// The Variant is kept (so the mapping is preserved) but flagged for the UI/loader,
// per the degrade-never-crash rule.
type Variant struct {
	SamplePath     string   `json:"sample_path"`
	StartFrame     int64    `json:"start_frame,omitempty"`
	EndFrame       int64    `json:"end_frame,omitempty"`
	LoopMode       LoopMode `json:"loop_mode"`
	LoopStart      int64    `json:"loop_start,omitempty"`
	LoopEnd        int64    `json:"loop_end,omitempty"`
	PitchKeycenter int      `json:"pitch_keycenter"`
	Transpose      int      `json:"transpose,omitempty"`
	Tune           int      `json:"tune,omitempty"`
	Gain           float64  `json:"gain,omitempty"`
	Pan            float64  `json:"pan,omitempty"`
	Missing        bool     `json:"missing,omitempty"`
	// Reverse plays the sample backwards (engine concern; metadata here).
	Reverse bool `json:"reverse,omitempty"`
	// RandLo/RandHi are the SFZ lorand/hirand band (0..1) for RANDOM round-robin:
	// a variant plays when RandLo <= r < RandHi. Adjacent variants should tile [0,1)
	// with no gaps. Only consulted by SelectVariantRandom. Default 0/0 means "unset",
	// which SelectVariantRandom treats as an even split across the variant list.
	RandLo float64 `json:"rand_lo,omitempty"`
	RandHi float64 `json:"rand_hi,omitempty"`
}

// Layer is a velocity-bounded set of round-robin Variants.
//
//	VelLo/VelHi -> lovel/hivel (1..127)
//	RRMode      -> sequential (seq_*) or random (lorand/hirand)
type Layer struct {
	VelLo      int       `json:"vel_lo"`
	VelHi      int       `json:"vel_hi"`
	RoundRobin []Variant `json:"round_robin"`
	RRMode     RRMode    `json:"rr_mode"`
}

// Sound is everything behind one pad: velocity Layers plus choke/key behavior.
//
//	ChokeGroup -> group (SFZ): voices in the same group can cut each other
//	OffBy      -> off_by (SFZ): groups this Sound silences when it fires
//	OneShot    -> drum default (loop_mode=one_shot; ignores note-off)
//	KeyLo/KeyHi/Root -> chromatic/keygroup mapping (0 disables)
type Sound struct {
	Name       string    `json:"name"`
	Layers     []Layer   `json:"layers"`
	ChokeGroup int       `json:"choke_group,omitempty"`
	OffBy      []int     `json:"off_by,omitempty"`
	ChokeMode  ChokeMode `json:"choke_mode"`
	OneShot    bool      `json:"one_shot,omitempty"`
	KeyLo      int       `json:"key_lo,omitempty"`
	KeyHi      int       `json:"key_hi,omitempty"`
	Root       int       `json:"root,omitempty"`

	// AmpEnv is the amplitude envelope the engine applies to every voice of this
	// Sound. For drums the default is a one-shot (play to end); AHD lets a hit be
	// shortened ("tighten the kick"); ADSR is for sustained/melodic pads.
	AmpEnv AmpEnv `json:"amp_env"`
	// AmpVelTrack is how strongly MIDI velocity scales amplitude, 0..1. 0 = velocity
	// only selects a layer (no dynamics); 1 = full velocity→loudness. THIS is what
	// makes a hard hit louder than a ghost note even on a single-layer pad. Use
	// VelGain(vel) to get the multiplier. NewDrumSound defaults it to 1.
	AmpVelTrack float64 `json:"amp_vel_track,omitempty"`
	// Polyphony caps simultaneous voices of this Sound (0 = unlimited); the engine
	// steals the oldest voice past the cap.
	Polyphony int `json:"polyphony,omitempty"`
	// DeclickMs is the minimum release ramp (ms) the engine MUST apply on any hard
	// stop — choke, voice-steal, or a one-shot ending mid-cycle — so cutting a voice
	// never clicks. Even ChokeMode=Fast honors this floor. Default via NewDrumSound.
	DeclickMs float64 `json:"declick_ms,omitempty"`
}

// Kit16 is the convenience container for the 16-pad machine.
type Kit16 struct {
	Name string    `json:"name"`
	Pads [16]Sound `json:"pads"`
}

// ---------------------------------------------------------------------------
// Selection logic (deterministic)
// ---------------------------------------------------------------------------

// PickLayer returns the first Layer whose [VelLo,VelHi] range contains vel.
// vel is clamped to 1..127. If no layer matches (e.g. ranges leave a gap) the
// closest layer by range distance is returned with ok=true so a hit always makes
// a sound; ok is false only when the Sound has no layers at all.
func PickLayer(s Sound, vel int) (Layer, bool) {
	if len(s.Layers) == 0 {
		return Layer{}, false
	}
	if vel < 1 {
		vel = 1
	}
	if vel > 127 {
		vel = 127
	}
	for _, l := range s.Layers {
		lo, hi := normVel(l.VelLo, l.VelHi)
		if vel >= lo && vel <= hi {
			return l, true
		}
	}
	// No exact match: pick the nearest layer so a pad hit is never silent.
	best := s.Layers[0]
	bestDist := velDistance(best, vel)
	for _, l := range s.Layers[1:] {
		if d := velDistance(l, vel); d < bestDist {
			best, bestDist = l, d
		}
	}
	return best, true
}

// velDistance is how far vel sits outside a layer's (normalized) range; 0 inside.
func velDistance(l Layer, vel int) int {
	lo, hi := normVel(l.VelLo, l.VelHi)
	switch {
	case vel < lo:
		return lo - vel
	case vel > hi:
		return vel - hi
	default:
		return 0
	}
}

// normVel clamps a velocity range to 1..127 and orders lo<=hi. A zero VelHi
// (the Go zero value) is treated as the full upper bound 127.
func normVel(lo, hi int) (int, int) {
	if lo < 1 {
		lo = 1
	}
	if hi <= 0 || hi > 127 {
		hi = 127
	}
	if lo > hi {
		lo, hi = hi, lo
	}
	return lo, hi
}

// SelectVariant returns the round-robin Variant for a Layer given the current
// per-Sound counter, and the next counter value to store. Selection is fully
// deterministic: Sequential cycles in order (SFZ seq_position 1..seq_length) and
// wraps; Random is mapped to a deterministic position derived from the counter so
// the engine stays reproducible (true randomness, if ever wanted, is the caller's
// to inject). The returned counter always advances by one, modulo the variant
// count, so callers can persist a single integer per Sound.
//
// An empty RoundRobin yields the zero Variant with nextCounter unchanged.
func SelectVariant(layer Layer, rrCounter int) (Variant, int) {
	n := len(layer.RoundRobin)
	if n == 0 {
		return Variant{}, rrCounter
	}
	// Normalize a possibly-negative or large counter into [0,n).
	idx := ((rrCounter % n) + n) % n
	v := layer.RoundRobin[idx]
	next := (idx + 1) % n
	return v, next
}

// SelectVariantRandom honestly implements RANDOM round-robin (SFZ lorand/hirand):
// r is a caller-supplied random value in [0,1) (the engine injects its RNG so this
// stays reproducible under a fixed seed). A variant plays when RandLo <= r < RandHi.
// If a layer's variants have no explicit bands (all zero), [0,1) is split evenly
// across them. Empty RoundRobin yields the zero Variant. This is the function the
// engine must call when Layer.RRMode == Random; the deterministic SelectVariant is
// SEQUENTIAL ONLY and never produces random order.
func SelectVariantRandom(layer Layer, r float64) Variant {
	n := len(layer.RoundRobin)
	if n == 0 {
		return Variant{}
	}
	if r < 0 {
		r = 0
	}
	if r >= 1 {
		r = 0.999999
	}
	// If explicit bands are set on any variant, honor them.
	banded := false
	for _, v := range layer.RoundRobin {
		if v.RandLo != 0 || v.RandHi != 0 {
			banded = true
			break
		}
	}
	if banded {
		for _, v := range layer.RoundRobin {
			lo, hi := v.RandLo, v.RandHi
			if hi <= 0 {
				hi = 1
			}
			if r >= lo && r < hi {
				return v
			}
		}
		return layer.RoundRobin[n-1] // r fell in a gap: last variant
	}
	// No bands: even split.
	idx := int(r * float64(n))
	if idx >= n {
		idx = n - 1
	}
	return layer.RoundRobin[idx]
}

// VelGain returns the linear amplitude multiplier (0..1) for a MIDI velocity, given
// this Sound's AmpVelTrack. track=0 => velocity does not affect loudness (returns 1);
// track=1 => full velocity dynamics on a square-law curve (perceptually closer than
// linear). THIS is the fix for "a ghost note and an accent sound identical": the
// engine multiplies each voice's amplitude by VelGain(vel). A single-layer pad is now
// dynamic as long as AmpVelTrack > 0 (NewDrumSound defaults it to 1).
func (s Sound) VelGain(vel int) float64 {
	if vel < 1 {
		vel = 1
	}
	if vel > 127 {
		vel = 127
	}
	t := clampFloat(s.AmpVelTrack, 0, 1)
	norm := float64(vel) / 127.0
	curved := norm * norm
	return (1-t)*1.0 + t*curved
}

// NewDrumSound returns a Sound with musically-responsive defaults: a one-shot
// envelope, full velocity tracking (so hits respond out of the box), and a small
// declick floor so cuts/chokes never pop. kitimport and the GUI should use this (or
// set AmpVelTrack/DeclickMs explicitly) rather than the zero-value Sound, whose
// AmpVelTrack=0 would be velocity-inert.
func NewDrumSound(name string) Sound {
	return Sound{
		Name:        name,
		AmpEnv:      AmpEnv{Type: EnvOneshot},
		AmpVelTrack: 1,
		DeclickMs:   3,
		OneShot:     true,
	}
}

// ---------------------------------------------------------------------------
// Validation / normalization (degrade-never-crash)
// ---------------------------------------------------------------------------

// Normalize returns a clamped, sane copy of the Variant. It never errors; out-of-
// range values are corrected rather than rejected.
func (v Variant) Normalize() Variant {
	out := v
	if out.PitchKeycenter == 0 {
		out.PitchKeycenter = DefaultKeycenter
	}
	out.PitchKeycenter = clampInt(out.PitchKeycenter, 0, 127)
	out.Transpose = clampInt(out.Transpose, -127, 127)
	out.Tune = clampInt(out.Tune, -100, 100)
	out.Pan = clampFloat(out.Pan, -1, 1)
	if out.StartFrame < 0 {
		out.StartFrame = 0
	}
	if out.EndFrame < 0 {
		out.EndFrame = 0
	}
	if out.LoopStart < 0 {
		out.LoopStart = 0
	}
	if out.LoopEnd < 0 {
		out.LoopEnd = 0
	}
	out.RandLo = clampFloat(out.RandLo, 0, 1)
	out.RandHi = clampFloat(out.RandHi, 0, 1)
	return out
}

// Normalize returns a clamped copy of the Layer (velocity range + variants).
func (l Layer) Normalize() Layer {
	out := l
	out.VelLo, out.VelHi = normVel(l.VelLo, l.VelHi)
	if len(l.RoundRobin) > 0 {
		rr := make([]Variant, len(l.RoundRobin))
		for i, v := range l.RoundRobin {
			rr[i] = v.Normalize()
		}
		out.RoundRobin = rr
	}
	return out
}

// Normalize returns a clamped copy of the Sound (layers + key range).
func (s Sound) Normalize() Sound {
	out := s
	if len(s.Layers) > 0 {
		ls := make([]Layer, len(s.Layers))
		for i, l := range s.Layers {
			ls[i] = l.Normalize()
		}
		out.Layers = ls
	}
	out.KeyLo = clampInt(out.KeyLo, 0, 127)
	out.KeyHi = clampInt(out.KeyHi, 0, 127)
	out.Root = clampInt(out.Root, 0, 127)
	if out.KeyHi != 0 && out.KeyLo > out.KeyHi {
		out.KeyLo, out.KeyHi = out.KeyHi, out.KeyLo
	}
	out.AmpEnv = out.AmpEnv.Normalize()
	out.AmpVelTrack = clampFloat(out.AmpVelTrack, 0, 1)
	if out.Polyphony < 0 {
		out.Polyphony = 0
	}
	if out.DeclickMs < 0 {
		out.DeclickMs = 0
	}
	return out
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
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

// ---------------------------------------------------------------------------
// JSON persistence
// ---------------------------------------------------------------------------

// Save writes the kit as indented JSON. The output is deterministic (stable field
// order via struct tags; readable enum strings).
func (k Kit16) Save(path string) error {
	b, err := json.MarshalIndent(k, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// Load reads a kit previously written by Save and normalizes it.
func Load(path string) (Kit16, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Kit16{}, err
	}
	var k Kit16
	if err := json.Unmarshal(b, &k); err != nil {
		return Kit16{}, err
	}
	for i := range k.Pads {
		k.Pads[i] = k.Pads[i].Normalize()
	}
	return k, nil
}

// MarshalJSON / UnmarshalJSON for a single Sound, used by kitimport round-trip
// tests and any caller persisting one pad in isolation.

// SaveSound writes one Sound as indented JSON.
func SaveSound(path string, s Sound) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// LoadSound reads and normalizes one Sound.
func LoadSound(path string) (Sound, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Sound{}, err
	}
	var s Sound
	if err := json.Unmarshal(b, &s); err != nil {
		return Sound{}, err
	}
	return s.Normalize(), nil
}
