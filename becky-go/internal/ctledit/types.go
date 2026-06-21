// Package ctledit is the deterministic AI-edit applier for becky-canvas.
// It takes a BeckyEditBatch (a flat, enum-discriminated list of actions that a
// local model proposes) and applies each valid edit to a dawmodel.Arrangement via
// the existing immutable verbs.  Nothing is mutated in place; Apply always returns
// a new *Arrangement.  Illegal edits are dropped with a plain-English reason — the
// package never panics on a bad batch.
//
// The JSON schema defined here mirrors becky-control-schema.md §2 and
// agent-control.md §2.2 exactly.  The field names and op values are the
// canonical AI ABI — do not change them without updating both research docs.
package ctledit

// BeckyEditBatch is the top-level proposal a model emits (or a keyword parser
// constructs).  Summary is the one-line human-readable headline shown in the
// "show me, don't do it" overlay; Edits is the flat action list.
type BeckyEditBatch struct {
	Summary string      `json:"summary"`
	Edits   []BeckyEdit `json:"edits"`
}

// BeckyEdit is one action in the batch.  Op is a closed enum (see Op constants
// below); all other fields are sparse — only the fields relevant to the op are
// populated.  JSON tags use omitempty so the model's output stays compact.
//
// Human-readable references are accepted for Track / Target / BusID; the
// applier resolves them by exact ID then by case-insensitive name match.
//
// Notes rows use [pitch, start_beats, dur_beats, velocity] per
// agent-control.md §1.4 (the AbletonMCP/REMI convention).  The applier converts
// beats to ticks using the arrangement's PPQ.
type BeckyEdit struct {
	// Op is the action discriminator.  Must be one of the Op* constants.
	Op string `json:"op"`

	// ---- piano roll / note ops ----

	// Track identifies the target dawmodel track by ID or name.
	Track string `json:"track,omitempty"`
	// Clip is the clip name within the track; defaults to the first clip.
	Clip string `json:"clip,omitempty"`
	// Notes is [[pitch, start_beats, dur_beats, velocity], ...] for add_notes.
	// Rows with fewer than 4 elements are rejected with a reason.
	Notes [][]float64 `json:"notes,omitempty"`
	// NoteIDs are stable dawmodel.Note IDs for delete / move / resize / set_velocity.
	NoteIDs []uint64 `json:"note_ids,omitempty"`
	// DeltaTicks is the tick offset for move_notes (signed).
	DeltaTicks int `json:"d_ticks,omitempty"`
	// DeltaPitch is the semitone offset for move_notes (signed).
	DeltaPitch int `json:"d_pitch,omitempty"`
	// DeltaDur is the tick delta for resize_notes (signed; clamped to keep dur >= 1).
	DeltaDur int `json:"d_dur,omitempty"`
	// Semitones is the transposition amount for transpose (signed).
	Semitones int `json:"semitones,omitempty"`
	// Velocity is 1..127 for set_velocity.
	Velocity int `json:"velocity,omitempty"`

	// ---- drum grid (set_step) ----

	// LaneIdx is the zero-based lane index within the drum grid.
	LaneIdx int `json:"lane_idx,omitempty"`
	// Step is the zero-based step index within the lane.
	Step int `json:"step,omitempty"`
	// On is the on/off state for set_step.
	On bool `json:"on,omitempty"`
	// StepVel is the hit velocity for set_step (0 = use the dawmodel default).
	StepVel int `json:"step_vel,omitempty"`

	// ---- mixer ops ----

	// Target identifies a track for mixer ops (by ID or name).
	Target string `json:"target,omitempty"`
	// Gain is the linear gain (0..2, 1 = unity) for set_gain.
	Gain float64 `json:"gain,omitempty"`
	// Pan is -1 (L) .. 0 (C) .. 1 (R) for set_pan.
	Pan float64 `json:"pan,omitempty"`
	// Muted is the mute flag for mute.
	Muted bool `json:"muted,omitempty"`
	// Soloed is the solo flag for solo.
	Soloed bool `json:"soloed,omitempty"`
	// BusID is the destination bus ID for route_to and add_sidechain.
	BusID string `json:"bus_id,omitempty"`
	// SidechainSource is the source track/bus ID for add_sidechain.
	SidechainSource string `json:"sidechain_source,omitempty"`

	// ---- transport ----

	// BPM is the new tempo in beats-per-minute for set_tempo.
	BPM int `json:"bpm,omitempty"`
}

// Op constants — the closed enum of supported edit operations.
// A GBNF grammar (agent-control.md §3) can lock a model to only emit these.
const (
	// Piano roll / note operations
	OpAddNotes    = "add_notes"    // insert new notes from a [[pitch,start,dur,vel]...] list
	OpDeleteNotes = "delete_notes" // remove notes by stable ID
	OpMoveNotes   = "move_notes"   // shift notes in time (d_ticks) and/or pitch (d_pitch)
	OpResizeNotes = "resize_notes" // extend/shrink note durations by d_dur
	OpTranspose   = "transpose"    // shift every note in a clip by N semitones
	OpSetVelocity = "set_velocity" // set velocity on specific notes

	// Drum grid operations
	OpSetStep = "set_step" // toggle/set one cell of a drum grid lane

	// Mixer operations
	OpSetGain      = "set_gain"      // set linear fader gain on a track
	OpSetPan       = "set_pan"       // set stereo pan on a track
	OpMute         = "mute"          // set/clear a track's mute flag
	OpSolo         = "solo"          // set/clear a track's solo flag
	OpRouteTo      = "route_to"      // change which bus a track routes to
	OpAddSidechain = "add_sidechain" // declare a sidechain edge on a bus

	// Transport operations
	OpSetTempo = "set_tempo" // change the arrangement's BPM
)

// knownOps is the set of valid op values for fast membership checks.
var knownOps = map[string]bool{
	OpAddNotes:     true,
	OpDeleteNotes:  true,
	OpMoveNotes:    true,
	OpResizeNotes:  true,
	OpTranspose:    true,
	OpSetVelocity:  true,
	OpSetStep:      true,
	OpSetGain:      true,
	OpSetPan:       true,
	OpMute:         true,
	OpSolo:         true,
	OpRouteTo:      true,
	OpAddSidechain: true,
	OpSetTempo:     true,
}

// EditOutcome reports what happened to one BeckyEdit during Apply.
type EditOutcome struct {
	// Op mirrors BeckyEdit.Op for traceability.
	Op string `json:"op"`
	// Index is the 0-based position of this edit in the original batch.
	Index int `json:"index"`
	// Applied is true when the edit was successfully applied.
	Applied bool `json:"applied"`
	// Reason is set when Applied is false; plain English, never empty on skip.
	Reason string `json:"reason,omitempty"`
}

// Result is the aggregate outcome of a single Apply call.
type Result struct {
	Outcomes []EditOutcome `json:"outcomes"`
	Applied  int           `json:"applied"`
	Skipped  int           `json:"skipped"`
}
