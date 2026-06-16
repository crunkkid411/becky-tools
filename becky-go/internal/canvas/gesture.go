package canvas

// gesture.go — the BEFORE→AFTER correction mapping for Jordan's by-eye drum/piano edits.
//
// When Jordan toggles a drum step or moves a note, the GUI records what changed
// (auto = value before the gesture, fixed = value after) and logs it for the habits
// learner. This file is the pure-Go, tagless, zero-cgo mapping layer:
//
//   - DrumCellEdit describes one step toggle (lane, step, was-on, is-now-on).
//   - MapDrumToggle maps a DrumCellEdit to the five habits log fields (scope,
//     field, auto, fixed) that CorrectionLogFunc expects.
//   - AppendDrumEdit is the stateless log helper the GUI calls: map → log.
//
// No Gio, no audio, no exec — safe for headless CI and unit tests.
// degrade-never-crash: any error from the underlying log function is passed back;
// callers ignore it (best-effort, never crash on a log failure).

import "fmt"

// laneNames maps drum lane index → the instrument label used as the habits scope.
// These are the same four lanes the drum grid renders (kick/snare/hat/clap).
var laneNames = [...]string{"kick", "snare", "hat", "clap"}

// LaneName returns the canonical instrument name for a drum lane index.
// Out-of-range indices return a numbered fallback ("lane-N") so the log is
// never empty — degrade, never return "".
func LaneName(lane int) string {
	if lane >= 0 && lane < len(laneNames) {
		return laneNames[lane]
	}
	return fmt.Sprintf("lane-%d", lane)
}

// DrumCellEdit records a single drum-step toggle gesture.
//
//   - Lane  : 0-based drum lane index (kick=0, snare=1, hat=2, clap=3, …).
//   - Step  : 0-based step index within the bar (0–15 for a 16-step grid).
//   - WasOn : the cell's state BEFORE Jordan's click/drag.
//   - IsOn  : the cell's state AFTER Jordan's click/drag.
type DrumCellEdit struct {
	Lane  int
	Step  int
	WasOn bool
	IsOn  bool
}

// DrumToggleArgs is the before→after mapping for habits.AppendCorrectionLog.
// The fields match the (scope, field, auto, fixed) contract.
type DrumToggleArgs struct {
	// Scope is the instrument label (e.g. "kick", "snare").
	Scope string
	// Field is "step/<N>" (the step position in the bar).
	Field string
	// Auto is the step value BEFORE the gesture ("on" or "off").
	Auto string
	// Fixed is the step value AFTER the gesture ("on" or "off").
	Fixed string
}

// stepState converts a bool step-cell value to the canonical text representation
// used in the habits log ("on" / "off"). Deterministic.
func stepState(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// MapDrumToggle maps a DrumCellEdit to DrumToggleArgs for the habits log.
// Pure function — deterministic, no side effects.
func MapDrumToggle(edit DrumCellEdit) DrumToggleArgs {
	return DrumToggleArgs{
		Scope: LaneName(edit.Lane),
		Field: fmt.Sprintf("step/%d", edit.Step),
		Auto:  stepState(edit.WasOn),
		Fixed: stepState(edit.IsOn),
	}
}

// CorrectionLogFunc matches the signature of habits.AppendCorrectionLog so the
// GUI can inject the real function without importing the habits package here.
// (Keeps this file free of non-canvas deps, testable with a simple stub.)
type CorrectionLogFunc func(path, tool, scope, field, auto, fixed string) error

// AppendDrumEdit maps a DrumCellEdit to its habits args and calls logFn
// with the right arguments. Returns the error from logFn, or nil.
// logPath is the .jsonl file path (canvas.corrections.jsonl next to the output).
//
// The GUI calls this after every drum cell toggle:
//
//	canvas.AppendDrumEdit(logPath, edit, habits.AppendCorrectionLog)
//
// Best-effort: callers should ignore the error (degrade, never crash on a log failure).
func AppendDrumEdit(logPath string, edit DrumCellEdit, logFn CorrectionLogFunc) error {
	args := MapDrumToggle(edit)
	return logFn(logPath, "canvas", args.Scope, args.Field, args.Auto, args.Fixed)
}
