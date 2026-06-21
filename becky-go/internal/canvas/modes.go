// Package canvas is the pure-Go, deterministic FOUNDATION for becky-canvas — the
// native creative GUI (SPEC-BECKY-CANVAS.md). It builds the SCENE MODEL that the
// later Phase-2 native GUI (giu/Dear ImGui over cgo) renders: mode tabs, track
// lanes, clips on a timeline, a viewport/transport, and a pitch-/waveform-lane
// representation whose pixel data the GUI fills in.
//
// This package contains NO GUI, NO cgo, NO window — only the data model + the
// pure math the GUI needs (time<->pixel, lane layout). That keeps `go build ./...`
// and `go test ./...` green on model-free, headless CI per CLAUDE.md §3, while the
// native window is built later as an explicit Phase-2 step.
//
// VISUAL-FIRST (CLAUDE.md HARD REQUIREMENT): the surface is waveform tracks + pitch
// lanes; Jordan fixes things BY EYE and becky LEARNS from his corrections. So the
// model is explicitly lane-centric (Track.Lane carries a WaveLane/PitchLane data
// placeholder the GUI fills) and every Scene carries a Corrections log hook that
// records the manual edits becky will learn from.
package canvas

// Mode is one canvas surface. becky-canvas is becky-ask by default and morphs into
// a creative surface on demand: video timeline/player, audio DAW, MIDI piano roll,
// or drum machine — all on the one canvas (SPEC §1).
type Mode string

// The planned modes (SPEC §1/§6). ModeAsk is the default; the others are creative
// surfaces the canvas morphs into. These are the values the `--mode` flag accepts.
const (
	ModeAsk   Mode = "ask"   // becky-ask chat (the default surface)
	ModeVideo Mode = "video" // frame-accurate video timeline/player (SPEC Phase 3)
	ModeDAW   Mode = "daw"   // mixer + deterministic sidechain routing (SPEC §5)
	ModeMIDI  Mode = "midi"  // piano-roll editor (SPEC Phase 2)
	ModeDrum  Mode = "drum"  // step-sequencer / drum machine (SPEC §6 MVP mode #2)
	ModeAudio Mode = "audio" // audio/vocal waveform tracks (CANVAS-BLUEPRINT panel 2d)
)

// Modes returns the planned modes in a fixed, deterministic order. The order is the
// tab order the GUI renders and never depends on map iteration.
func Modes() []Mode {
	return []Mode{ModeAsk, ModeVideo, ModeDAW, ModeMIDI, ModeDrum, ModeAudio}
}

// ValidMode reports whether s names a planned mode.
func ValidMode(s string) bool {
	for _, m := range Modes() {
		if string(m) == s {
			return true
		}
	}
	return false
}

// ParseMode validates and returns the Mode for s, or false if s is not a planned
// mode. Empty input is NOT a valid mode (callers default explicitly to ModeAsk).
func ParseMode(s string) (Mode, bool) {
	if ValidMode(s) {
		return Mode(s), true
	}
	return "", false
}

// ModeNames returns the planned mode names as plain strings (for help text / a
// CLI enumeration). Deterministic order, mirrors Modes().
func ModeNames() []string {
	ms := Modes()
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = string(m)
	}
	return out
}
