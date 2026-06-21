package ctledit

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"becky-go/internal/dawmodel"
	"becky-go/internal/habits"
)

// Apply executes a BeckyEditBatch against the given arrangement and returns:
//   - a new *dawmodel.Arrangement with all valid edits applied in order,
//   - a Result describing per-edit outcomes,
//   - an error only when a is nil (the batch itself is never fatal).
//
// Individual illegal edits (unknown op, unknown target, out-of-range values) are
// dropped with a plain-English reason recorded in Result — they do not cause an
// error return.  The function is deterministic: same arrangement + same batch =>
// same output.  It never mutates a.
//
// opts may be nil; pass &ApplyOpts{HabitsLogPath: p} to enable correction logging.
func Apply(a *dawmodel.Arrangement, batch BeckyEditBatch, opts *ApplyOpts) (*dawmodel.Arrangement, Result, error) {
	if a == nil {
		return nil, Result{}, fmt.Errorf("ctledit.Apply: arrangement is nil")
	}

	var logPath string
	if opts != nil {
		logPath = opts.HabitsLogPath
	}

	res := Result{Outcomes: make([]EditOutcome, len(batch.Edits))}
	cur := a

	for i, ed := range batch.Edits {
		out := EditOutcome{Op: ed.Op, Index: i}

		if !knownOps[ed.Op] {
			out.Reason = fmt.Sprintf("unknown op %q — not in the closed enum", ed.Op)
			res.Outcomes[i] = out
			res.Skipped++
			continue
		}

		next, reason := applyOne(cur, ed)
		if reason != "" {
			out.Reason = reason
			res.Outcomes[i] = out
			res.Skipped++
			continue
		}

		cur = next
		out.Applied = true
		res.Outcomes[i] = out
		res.Applied++

		// Log the applied edit best-effort; never let a log failure abort the apply.
		if logPath != "" {
			_ = habits.AppendCorrectionLog(logPath, "canvas",
				ed.Op, "op", "", ed.Op)
		}
	}

	return cur, res, nil
}

// ApplyOpts carries optional parameters for Apply.
type ApplyOpts struct {
	// HabitsLogPath is the path to the JSONL correction log for preference
	// learning (habits.AppendCorrectionLog).  Empty disables logging.
	HabitsLogPath string
}

// ParseBatch parses raw JSON (a model's output) into a BeckyEditBatch and
// validates the top-level structure.  Returns an error for unparseable input.
// A batch containing edits with unknown op values is valid here — Apply will
// skip those edits with a plain-English reason.
func ParseBatch(raw []byte) (BeckyEditBatch, error) {
	var b BeckyEditBatch
	if err := json.Unmarshal(raw, &b); err != nil {
		return BeckyEditBatch{}, fmt.Errorf("ctledit: parse batch: %w", err)
	}
	return b, nil
}

// ---- internal helpers --------------------------------------------------------

// applyOne applies a single BeckyEdit to cur and returns the next arrangement.
// On success reason is ""; on failure reason is a plain-English explanation and
// the returned arrangement equals cur (unchanged).
func applyOne(a *dawmodel.Arrangement, ed BeckyEdit) (*dawmodel.Arrangement, string) {
	switch ed.Op {

	// ---- piano roll / note ops ------------------------------------------------

	case OpAddNotes:
		trackID, reason := resolveTrack(a, ed.Track)
		if reason != "" {
			return a, reason
		}
		clipName, reason := resolveClip(a, trackID, ed.Clip)
		if reason != "" {
			return a, reason
		}
		if len(ed.Notes) == 0 {
			return a, "add_notes: notes list is empty"
		}
		ppq := a.PPQ
		if ppq <= 0 {
			ppq = 96
		}
		cur := a
		for rowIdx, row := range ed.Notes {
			if len(row) < 4 {
				return a, fmt.Sprintf(
					"add_notes: row %d has %d elements (need 4: pitch, start_beats, dur_beats, velocity)",
					rowIdx, len(row))
			}
			pitch := int(math.Round(row[0]))
			if pitch < 0 || pitch > 127 {
				return a, fmt.Sprintf("add_notes: row %d pitch %d out of range 0..127", rowIdx, pitch)
			}
			startTicks := int(math.Round(row[1] * float64(ppq)))
			if startTicks < 0 {
				startTicks = 0
			}
			durTicks := int(math.Round(row[2] * float64(ppq)))
			if durTicks < 1 {
				durTicks = 1
			}
			vel := int(math.Round(row[3]))
			if vel < 1 {
				vel = 1
			}
			if vel > 127 {
				vel = 127
			}
			n := dawmodel.Note{
				Start: startTicks,
				Dur:   durTicks,
				Pitch: pitch,
				Vel:   vel,
			}
			next, _, err := cur.AddNote(trackID, clipName, n)
			if err != nil {
				return a, fmt.Sprintf("add_notes: row %d: %v", rowIdx, err)
			}
			cur = next
		}
		return cur, ""

	case OpDeleteNotes:
		trackID, reason := resolveTrack(a, ed.Track)
		if reason != "" {
			return a, reason
		}
		clipName, reason := resolveClip(a, trackID, ed.Clip)
		if reason != "" {
			return a, reason
		}
		if len(ed.NoteIDs) == 0 {
			return a, "delete_notes: note_ids list is empty"
		}
		next, err := a.DeleteNotes(trackID, clipName, ed.NoteIDs)
		if err != nil {
			return a, fmt.Sprintf("delete_notes: %v", err)
		}
		return next, ""

	case OpMoveNotes:
		trackID, reason := resolveTrack(a, ed.Track)
		if reason != "" {
			return a, reason
		}
		clipName, reason := resolveClip(a, trackID, ed.Clip)
		if reason != "" {
			return a, reason
		}
		if len(ed.NoteIDs) == 0 {
			return a, "move_notes: note_ids list is empty"
		}
		next, err := a.MoveNotes(trackID, clipName, ed.NoteIDs, ed.DeltaTicks, ed.DeltaPitch)
		if err != nil {
			return a, fmt.Sprintf("move_notes: %v", err)
		}
		return next, ""

	case OpResizeNotes:
		trackID, reason := resolveTrack(a, ed.Track)
		if reason != "" {
			return a, reason
		}
		clipName, reason := resolveClip(a, trackID, ed.Clip)
		if reason != "" {
			return a, reason
		}
		if len(ed.NoteIDs) == 0 {
			return a, "resize_notes: note_ids list is empty"
		}
		next, err := a.ResizeNotes(trackID, clipName, ed.NoteIDs, ed.DeltaDur)
		if err != nil {
			return a, fmt.Sprintf("resize_notes: %v", err)
		}
		return next, ""

	case OpTranspose:
		trackID, reason := resolveTrack(a, ed.Track)
		if reason != "" {
			return a, reason
		}
		clipName, reason := resolveClip(a, trackID, ed.Clip)
		if reason != "" {
			return a, reason
		}
		next, err := a.Transpose(trackID, clipName, ed.Semitones)
		if err != nil {
			return a, fmt.Sprintf("transpose: %v", err)
		}
		return next, ""

	case OpSetVelocity:
		trackID, reason := resolveTrack(a, ed.Track)
		if reason != "" {
			return a, reason
		}
		clipName, reason := resolveClip(a, trackID, ed.Clip)
		if reason != "" {
			return a, reason
		}
		if len(ed.NoteIDs) == 0 {
			return a, "set_velocity: note_ids list is empty"
		}
		if ed.Velocity < 1 || ed.Velocity > 127 {
			return a, fmt.Sprintf("set_velocity: velocity %d out of range 1..127", ed.Velocity)
		}
		next, err := a.SetVelocity(trackID, clipName, ed.NoteIDs, ed.Velocity)
		if err != nil {
			return a, fmt.Sprintf("set_velocity: %v", err)
		}
		return next, ""

	// ---- drum grid ops --------------------------------------------------------

	case OpSetStep:
		trackID, reason := resolveTrack(a, ed.Track)
		if reason != "" {
			return a, reason
		}
		clipName, reason := resolveClip(a, trackID, ed.Clip)
		if reason != "" {
			return a, reason
		}
		grid, err := a.DrumGridOf(trackID, clipName, 0)
		if err != nil {
			return a, fmt.Sprintf("set_step: derive grid: %v", err)
		}
		if len(grid.Lanes) == 0 {
			return a, "set_step: drum grid has no lanes"
		}
		if ed.LaneIdx < 0 || ed.LaneIdx >= len(grid.Lanes) {
			return a, fmt.Sprintf("set_step: lane_idx %d out of range 0..%d",
				ed.LaneIdx, len(grid.Lanes)-1)
		}
		cells := grid.Steps * grid.Bars
		if ed.Step < 0 || ed.Step >= cells {
			return a, fmt.Sprintf("set_step: step %d out of range 0..%d", ed.Step, cells-1)
		}
		newGrid := grid.SetStep(ed.LaneIdx, ed.Step, ed.On, ed.StepVel)
		next, err := a.ApplyDrumGrid(trackID, clipName, newGrid)
		if err != nil {
			return a, fmt.Sprintf("set_step: apply grid: %v", err)
		}
		return next, ""

	// ---- mixer ops ------------------------------------------------------------

	case OpSetGain:
		trackID, reason := resolveTrack(a, ed.Target)
		if reason != "" {
			return a, reason
		}
		if ed.Gain == nil {
			return a, "set_gain: gain field required (omitting it would silence the track)"
		}
		if *ed.Gain < 0 || *ed.Gain > 2 {
			return a, fmt.Sprintf("set_gain: gain %.4f out of range 0..2", *ed.Gain)
		}
		next, err := a.SetGain(trackID, *ed.Gain)
		if err != nil {
			return a, fmt.Sprintf("set_gain: %v", err)
		}
		return next, ""

	case OpSetPan:
		trackID, reason := resolveTrack(a, ed.Target)
		if reason != "" {
			return a, reason
		}
		if ed.Pan < -1 || ed.Pan > 1 {
			return a, fmt.Sprintf("set_pan: pan %.4f out of range -1..1", ed.Pan)
		}
		next, err := a.SetPan(trackID, ed.Pan)
		if err != nil {
			return a, fmt.Sprintf("set_pan: %v", err)
		}
		return next, ""

	case OpMute:
		trackID, reason := resolveTrack(a, ed.Target)
		if reason != "" {
			return a, reason
		}
		next, err := a.SetMute(trackID, ed.Muted)
		if err != nil {
			return a, fmt.Sprintf("mute: %v", err)
		}
		return next, ""

	case OpSolo:
		trackID, reason := resolveTrack(a, ed.Target)
		if reason != "" {
			return a, reason
		}
		next, err := a.SetSolo(trackID, ed.Soloed)
		if err != nil {
			return a, fmt.Sprintf("solo: %v", err)
		}
		return next, ""

	case OpRouteTo:
		trackID, reason := resolveTrack(a, ed.Target)
		if reason != "" {
			return a, reason
		}
		if ed.BusID == "" {
			return a, "route_to: bus_id is empty"
		}
		next, err := a.RouteTo(trackID, ed.BusID)
		if err != nil {
			return a, fmt.Sprintf("route_to: %v", err)
		}
		return next, ""

	case OpAddSidechain:
		if ed.BusID == "" {
			return a, "add_sidechain: bus_id is empty"
		}
		if ed.SidechainSource == "" {
			return a, "add_sidechain: sidechain_source is empty"
		}
		next, err := a.AddSidechain(ed.BusID, ed.SidechainSource)
		if err != nil {
			return a, fmt.Sprintf("add_sidechain: %v", err)
		}
		return next, ""

	// ---- transport ops --------------------------------------------------------

	case OpSetTempo:
		if ed.BPM <= 0 {
			return a, fmt.Sprintf("set_tempo: bpm %d must be > 0", ed.BPM)
		}
		if ed.BPM > 999 {
			return a, fmt.Sprintf("set_tempo: bpm %d exceeds 999 (unreasonable tempo)", ed.BPM)
		}
		// Arrangement.clone() is unexported; use JSON round-trip for a deep copy.
		// This is intentional: ctledit may not edit dawmodel.
		next, err := cloneArrangement(a)
		if err != nil {
			return a, fmt.Sprintf("set_tempo: clone arrangement: %v", err)
		}
		next.BPM = ed.BPM
		return next, ""

	// ---- generative drum ops (internal/beatgen) -------------------------------

	case OpGenerateBeat:
		return applyGenerateBeat(a, ed)

	case OpEuclidLane:
		return applyEuclidLane(a, ed)

	default:
		// Should not be reached because knownOps guards above, but be defensive.
		return a, fmt.Sprintf("unhandled op %q", ed.Op)
	}
}

// cloneArrangement returns a deep copy of a via JSON round-trip.
// Used by set_tempo (and any future op that needs to change a top-level field
// without going through a dedicated dawmodel verb) because Arrangement.clone()
// is unexported and ctledit must not edit the dawmodel package.
func cloneArrangement(a *dawmodel.Arrangement) (*dawmodel.Arrangement, error) {
	b, err := json.Marshal(a)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	var out dawmodel.Arrangement
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return &out, nil
}

// resolveTrack returns the canonical track ID for a human-readable reference.
// It tries exact ID match first, then case-insensitive match.
// Returns a plain-English reason when nothing matches.
func resolveTrack(a *dawmodel.Arrangement, ref string) (string, string) {
	if ref == "" {
		return "", "track reference is empty"
	}
	// exact match
	for _, t := range a.Tracks {
		if t.ID == ref {
			return t.ID, ""
		}
	}
	// case-insensitive match
	lower := strings.ToLower(ref)
	for _, t := range a.Tracks {
		if strings.ToLower(t.ID) == lower {
			return t.ID, ""
		}
	}
	return "", fmt.Sprintf("track %q not found (available: %s)", ref, trackList(a))
}

// resolveClip returns the clip name for a (trackID, clipRef) pair.
// When clipRef is empty the first clip is used.
// Returns a plain-English reason when no clip is available.
func resolveClip(a *dawmodel.Arrangement, trackID, clipRef string) (string, string) {
	t, ok := a.TrackByID(trackID)
	if !ok {
		return "", fmt.Sprintf("clip lookup: track %q not found", trackID)
	}
	if len(t.Clips) == 0 {
		return "", fmt.Sprintf("track %q has no clips", trackID)
	}
	if clipRef == "" {
		return t.Clips[0].Name, ""
	}
	for _, c := range t.Clips {
		if c.Name == clipRef {
			return c.Name, ""
		}
	}
	lower := strings.ToLower(clipRef)
	for _, c := range t.Clips {
		if strings.ToLower(c.Name) == lower {
			return c.Name, ""
		}
	}
	return "", fmt.Sprintf("clip %q not found in track %q", clipRef, trackID)
}

// trackList returns a compact comma-separated list of track IDs for error messages.
func trackList(a *dawmodel.Arrangement) string {
	if len(a.Tracks) == 0 {
		return "(none)"
	}
	ids := make([]string, len(a.Tracks))
	for i, t := range a.Tracks {
		ids[i] = t.ID
	}
	return strings.Join(ids, ", ")
}
