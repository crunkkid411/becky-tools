package ctledit

import (
	"encoding/json"
	"testing"

	"becky-go/internal/dawmodel"
)

// ---- test helpers ------------------------------------------------------------

// makeArrangement returns a small arrangement with:
//   - track "bass" with one MIDI clip "clip1" containing two notes
//   - track "kick" with one MIDI clip "drums" containing two percussion notes
//   - track "lead" with one MIDI clip "melody" containing no notes initially
//   - BPM 120, PPQ 96
func makeArrangement(t *testing.T) *dawmodel.Arrangement {
	t.Helper()
	a := dawmodel.New()
	a.BPM = 120

	// bass track
	a = a.AddTrack("bass", dawmodel.KindMIDI)
	a = mustAddClip(t, a, "bass", "clip1")
	var err error
	note1 := dawmodel.Note{Start: 0, Dur: 48, Pitch: 48, Vel: 80}
	note2 := dawmodel.Note{Start: 96, Dur: 48, Pitch: 55, Vel: 90}
	a, _, err = a.AddNote("bass", "clip1", note1)
	if err != nil {
		t.Fatalf("setup AddNote bass/clip1 note1: %v", err)
	}
	a, _, err = a.AddNote("bass", "clip1", note2)
	if err != nil {
		t.Fatalf("setup AddNote bass/clip1 note2: %v", err)
	}

	// kick track (drum grid — percussion notes at MIDI note 36)
	a = a.AddTrack("kick", dawmodel.KindMIDI)
	a = mustAddClip(t, a, "kick", "drums")
	kickNote1 := dawmodel.Note{Start: 0, Dur: 12, Pitch: 36, Vel: 100, Ch: 9}
	kickNote2 := dawmodel.Note{Start: 384, Dur: 12, Pitch: 36, Vel: 100, Ch: 9}
	a, _, err = a.AddNote("kick", "drums", kickNote1)
	if err != nil {
		t.Fatalf("setup AddNote kick/drums kick1: %v", err)
	}
	a, _, err = a.AddNote("kick", "drums", kickNote2)
	if err != nil {
		t.Fatalf("setup AddNote kick/drums kick2: %v", err)
	}

	// lead track (empty — for add_notes tests)
	a = a.AddTrack("lead", dawmodel.KindMIDI)
	a = mustAddClip(t, a, "lead", "melody")

	return a
}

// mustAddClip injects a named clip into a track via JSON round-trip.
// dawmodel has no public AddClip verb; this is the minimal shim for test setup.
func mustAddClip(t *testing.T, a *dawmodel.Arrangement, trackID, clipName string) *dawmodel.Arrangement {
	t.Helper()
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("mustAddClip marshal: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("mustAddClip unmarshal top: %v", err)
	}
	var tracks []json.RawMessage
	if err := json.Unmarshal(raw["tracks"], &tracks); err != nil {
		t.Fatalf("mustAddClip unmarshal tracks: %v", err)
	}
	for i, tb := range tracks {
		var tr map[string]json.RawMessage
		if err := json.Unmarshal(tb, &tr); err != nil {
			continue
		}
		var id string
		if err := json.Unmarshal(tr["id"], &id); err != nil || id != trackID {
			continue
		}
		// Append a new empty clip.
		var clips []json.RawMessage
		_ = json.Unmarshal(tr["clips"], &clips)
		newClip := map[string]interface{}{
			"name":    clipName,
			"channel": 0,
			"program": -1,
			"offset":  0,
		}
		newClipB, _ := json.Marshal(newClip)
		clips = append(clips, json.RawMessage(newClipB))
		clipsB, _ := json.Marshal(clips)
		tr["clips"] = clipsB
		trB, _ := json.Marshal(tr)
		tracks[i] = trB
		break
	}
	tracksB, _ := json.Marshal(tracks)
	raw["tracks"] = tracksB
	fullB, _ := json.Marshal(raw)
	var out dawmodel.Arrangement
	if err := json.Unmarshal(fullB, &out); err != nil {
		t.Fatalf("mustAddClip final unmarshal: %v", err)
	}
	return &out
}

// noteIDs returns the IDs of all notes in a clip.
func noteIDs(t *testing.T, a *dawmodel.Arrangement, trackID, clipName string) []uint64 {
	t.Helper()
	tr, ok := a.TrackByID(trackID)
	if !ok {
		t.Fatalf("noteIDs: track %q not found", trackID)
	}
	for _, c := range tr.Clips {
		if c.Name == clipName {
			ids := make([]uint64, len(c.Notes))
			for i, n := range c.Notes {
				ids[i] = n.ID
			}
			return ids
		}
	}
	t.Fatalf("noteIDs: clip %q not found in track %q", clipName, trackID)
	return nil
}

// noteCount returns how many notes are in a clip.
func noteCount(t *testing.T, a *dawmodel.Arrangement, trackID, clipName string) int {
	t.Helper()
	return len(noteIDs(t, a, trackID, clipName))
}

// ---- ParseBatch tests --------------------------------------------------------

func TestParseBatch_Valid(t *testing.T) {
	raw := []byte(`{"summary":"test","edits":[{"op":"set_tempo","bpm":140}]}`)
	b, err := ParseBatch(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.Summary != "test" {
		t.Errorf("summary: got %q, want %q", b.Summary, "test")
	}
	if len(b.Edits) != 1 || b.Edits[0].Op != "set_tempo" {
		t.Errorf("edits: got %+v", b.Edits)
	}
}

func TestParseBatch_InvalidJSON(t *testing.T) {
	_, err := ParseBatch([]byte("{not json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestParseBatch_EmptyBatch(t *testing.T) {
	raw := []byte(`{"summary":"","edits":[]}`)
	b, err := ParseBatch(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(b.Edits) != 0 {
		t.Errorf("expected 0 edits, got %d", len(b.Edits))
	}
}

// ---- Apply nil guard ---------------------------------------------------------

func TestApply_NilArrangement(t *testing.T) {
	_, _, err := Apply(nil, BeckyEditBatch{}, nil)
	if err == nil {
		t.Fatal("expected error for nil arrangement")
	}
}

// ---- unknown op is skipped ---------------------------------------------------

func TestApply_UnknownOp_Skipped(t *testing.T) {
	a := makeArrangement(t)
	batch := BeckyEditBatch{
		Summary: "unknown",
		Edits:   []BeckyEdit{{Op: "fly_to_the_moon"}},
	}
	out, res, err := Apply(a, batch, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Applied != 0 || res.Skipped != 1 {
		t.Errorf("want 0 applied 1 skipped, got %d/%d", res.Applied, res.Skipped)
	}
	if res.Outcomes[0].Applied {
		t.Error("expected outcome.Applied=false")
	}
	if res.Outcomes[0].Reason == "" {
		t.Error("expected non-empty reason for skipped edit")
	}
	// Arrangement should be unchanged.
	if out.NoteCount() != a.NoteCount() {
		t.Errorf("note count changed: %d -> %d", a.NoteCount(), out.NoteCount())
	}
}

// ---- set_tempo ---------------------------------------------------------------

func TestApply_SetTempo_OK(t *testing.T) {
	a := makeArrangement(t)
	batch := BeckyEditBatch{
		Summary: "change tempo",
		Edits:   []BeckyEdit{{Op: OpSetTempo, BPM: 142}},
	}
	out, res, err := Apply(a, batch, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Applied != 1 || res.Skipped != 0 {
		t.Errorf("want 1/0, got %d/%d", res.Applied, res.Skipped)
	}
	if out.BPM != 142 {
		t.Errorf("BPM: got %d, want 142", out.BPM)
	}
	// Original unchanged.
	if a.BPM != 120 {
		t.Errorf("original BPM mutated: %d", a.BPM)
	}
}

func TestApply_SetTempo_ZeroBPM_Skipped(t *testing.T) {
	a := makeArrangement(t)
	batch := BeckyEditBatch{Edits: []BeckyEdit{{Op: OpSetTempo, BPM: 0}}}
	_, res, err := Apply(a, batch, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Applied != 0 || res.Skipped != 1 {
		t.Errorf("want 0/1, got %d/%d", res.Applied, res.Skipped)
	}
}

func TestApply_SetTempo_TooHighBPM_Skipped(t *testing.T) {
	a := makeArrangement(t)
	batch := BeckyEditBatch{Edits: []BeckyEdit{{Op: OpSetTempo, BPM: 1000}}}
	_, res, _ := Apply(a, batch, nil)
	if res.Skipped != 1 {
		t.Error("expected skip for BPM>999")
	}
}

// ---- add_notes ---------------------------------------------------------------

func TestApply_AddNotes_OK(t *testing.T) {
	a := makeArrangement(t)
	before := noteCount(t, a, "lead", "melody")
	batch := BeckyEditBatch{
		Edits: []BeckyEdit{{
			Op:    OpAddNotes,
			Track: "lead",
			Clip:  "melody",
			Notes: [][]float64{
				{60, 0.0, 0.5, 100},
				{63, 0.5, 0.5, 90},
			},
		}},
	}
	out, res, err := Apply(a, batch, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Applied != 1 {
		t.Errorf("want 1 applied, got %d (reason: %s)", res.Applied, res.Outcomes[0].Reason)
	}
	after := noteCount(t, out, "lead", "melody")
	if after != before+2 {
		t.Errorf("note count: got %d, want %d", after, before+2)
	}
}

func TestApply_AddNotes_DefaultClip(t *testing.T) {
	a := makeArrangement(t)
	before := noteCount(t, a, "lead", "melody")
	batch := BeckyEditBatch{
		Edits: []BeckyEdit{{
			Op:    OpAddNotes,
			Track: "lead",
			// Clip intentionally empty — should default to "melody"
			Notes: [][]float64{{72, 1.0, 1.0, 100}},
		}},
	}
	out, res, _ := Apply(a, batch, nil)
	if res.Applied != 1 {
		t.Fatalf("want 1 applied, got %d: %s", res.Applied, res.Outcomes[0].Reason)
	}
	if noteCount(t, out, "lead", "melody") != before+1 {
		t.Error("note not added to default clip")
	}
}

func TestApply_AddNotes_BadRow_Skipped(t *testing.T) {
	a := makeArrangement(t)
	batch := BeckyEditBatch{
		Edits: []BeckyEdit{{
			Op:    OpAddNotes,
			Track: "lead",
			Clip:  "melody",
			Notes: [][]float64{{60, 0.0}}, // only 2 elements, need 4
		}},
	}
	_, res, _ := Apply(a, batch, nil)
	if res.Skipped != 1 {
		t.Error("expected skip for malformed note row")
	}
}

func TestApply_AddNotes_PitchOutOfRange_Skipped(t *testing.T) {
	a := makeArrangement(t)
	batch := BeckyEditBatch{
		Edits: []BeckyEdit{{
			Op:    OpAddNotes,
			Track: "lead",
			Clip:  "melody",
			Notes: [][]float64{{200, 0.0, 0.5, 100}}, // pitch 200 > 127
		}},
	}
	_, res, _ := Apply(a, batch, nil)
	if res.Skipped != 1 {
		t.Error("expected skip for out-of-range pitch")
	}
}

func TestApply_AddNotes_UnknownTrack_Skipped(t *testing.T) {
	a := makeArrangement(t)
	batch := BeckyEditBatch{
		Edits: []BeckyEdit{{
			Op:    OpAddNotes,
			Track: "nonexistent",
			Notes: [][]float64{{60, 0, 0.5, 100}},
		}},
	}
	_, res, _ := Apply(a, batch, nil)
	if res.Skipped != 1 {
		t.Error("expected skip for unknown track")
	}
}

// ---- delete_notes ------------------------------------------------------------

func TestApply_DeleteNotes_OK(t *testing.T) {
	a := makeArrangement(t)
	ids := noteIDs(t, a, "bass", "clip1")
	before := len(ids)
	if before == 0 {
		t.Skip("no notes to delete")
	}
	batch := BeckyEditBatch{
		Edits: []BeckyEdit{{
			Op:      OpDeleteNotes,
			Track:   "bass",
			Clip:    "clip1",
			NoteIDs: []uint64{ids[0]},
		}},
	}
	out, res, err := Apply(a, batch, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Applied != 1 {
		t.Errorf("want 1 applied: %s", res.Outcomes[0].Reason)
	}
	if noteCount(t, out, "bass", "clip1") != before-1 {
		t.Errorf("note not removed")
	}
}

func TestApply_DeleteNotes_EmptyIDs_Skipped(t *testing.T) {
	a := makeArrangement(t)
	batch := BeckyEditBatch{
		Edits: []BeckyEdit{{Op: OpDeleteNotes, Track: "bass", Clip: "clip1", NoteIDs: nil}},
	}
	_, res, _ := Apply(a, batch, nil)
	if res.Skipped != 1 {
		t.Error("expected skip for empty note_ids")
	}
}

// ---- move_notes --------------------------------------------------------------

func TestApply_MoveNotes_OK(t *testing.T) {
	a := makeArrangement(t)
	ids := noteIDs(t, a, "bass", "clip1")
	if len(ids) == 0 {
		t.Skip("no notes")
	}
	// Get original start of first note.
	tr, _ := a.TrackByID("bass")
	origStart := tr.Clips[0].Notes[0].Start
	movedID := tr.Clips[0].Notes[0].ID

	batch := BeckyEditBatch{
		Edits: []BeckyEdit{{
			Op:         OpMoveNotes,
			Track:      "bass",
			Clip:       "clip1",
			NoteIDs:    []uint64{movedID},
			DeltaTicks: 48,
			DeltaPitch: 2,
		}},
	}
	out, res, err := Apply(a, batch, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Applied != 1 {
		t.Fatalf("want 1 applied: %s", res.Outcomes[0].Reason)
	}
	// Verify the note moved.
	trOut, _ := out.TrackByID("bass")
	for _, n := range trOut.Clips[0].Notes {
		if n.ID == movedID {
			if n.Start != origStart+48 {
				t.Errorf("Start: got %d, want %d", n.Start, origStart+48)
			}
			return
		}
	}
	t.Error("moved note not found")
}

// ---- resize_notes ------------------------------------------------------------

func TestApply_ResizeNotes_OK(t *testing.T) {
	a := makeArrangement(t)
	ids := noteIDs(t, a, "bass", "clip1")
	if len(ids) == 0 {
		t.Skip("no notes")
	}
	tr, _ := a.TrackByID("bass")
	origDur := tr.Clips[0].Notes[0].Dur
	resizedID := tr.Clips[0].Notes[0].ID

	batch := BeckyEditBatch{
		Edits: []BeckyEdit{{
			Op:       OpResizeNotes,
			Track:    "bass",
			Clip:     "clip1",
			NoteIDs:  []uint64{resizedID},
			DeltaDur: 24,
		}},
	}
	out, res, _ := Apply(a, batch, nil)
	if res.Applied != 1 {
		t.Fatalf("want 1 applied: %s", res.Outcomes[0].Reason)
	}
	trOut, _ := out.TrackByID("bass")
	for _, n := range trOut.Clips[0].Notes {
		if n.ID == resizedID {
			if n.Dur != origDur+24 {
				t.Errorf("Dur: got %d, want %d", n.Dur, origDur+24)
			}
			return
		}
	}
	t.Error("resized note not found")
}

// ---- transpose ---------------------------------------------------------------

func TestApply_Transpose_OK(t *testing.T) {
	a := makeArrangement(t)
	tr, _ := a.TrackByID("bass")
	origPitch := tr.Clips[0].Notes[0].Pitch
	transposedID := tr.Clips[0].Notes[0].ID

	batch := BeckyEditBatch{
		Edits: []BeckyEdit{{
			Op:        OpTranspose,
			Track:     "bass",
			Clip:      "clip1",
			Semitones: 12,
		}},
	}
	out, res, _ := Apply(a, batch, nil)
	if res.Applied != 1 {
		t.Fatalf("want 1 applied: %s", res.Outcomes[0].Reason)
	}
	trOut, _ := out.TrackByID("bass")
	wantPitch := origPitch + 12
	if wantPitch > 127 {
		wantPitch = 127
	}
	for _, n := range trOut.Clips[0].Notes {
		if n.ID == transposedID {
			if n.Pitch != wantPitch {
				t.Errorf("Pitch: got %d, want %d", n.Pitch, wantPitch)
			}
			return
		}
	}
	t.Errorf("note %d not found after transpose", transposedID)
}

func TestApply_Transpose_UnknownClip_Skipped(t *testing.T) {
	a := makeArrangement(t)
	batch := BeckyEditBatch{
		Edits: []BeckyEdit{{
			Op:        OpTranspose,
			Track:     "bass",
			Clip:      "nosuchclip",
			Semitones: 7,
		}},
	}
	_, res, _ := Apply(a, batch, nil)
	if res.Skipped != 1 {
		t.Error("expected skip for unknown clip")
	}
}

// ---- set_velocity ------------------------------------------------------------

func TestApply_SetVelocity_OK(t *testing.T) {
	a := makeArrangement(t)
	ids := noteIDs(t, a, "bass", "clip1")
	if len(ids) == 0 {
		t.Skip("no notes")
	}
	batch := BeckyEditBatch{
		Edits: []BeckyEdit{{
			Op:       OpSetVelocity,
			Track:    "bass",
			Clip:     "clip1",
			NoteIDs:  ids,
			Velocity: 64,
		}},
	}
	out, res, _ := Apply(a, batch, nil)
	if res.Applied != 1 {
		t.Fatalf("want 1 applied: %s", res.Outcomes[0].Reason)
	}
	tr, _ := out.TrackByID("bass")
	for _, n := range tr.Clips[0].Notes {
		if n.Vel != 64 {
			t.Errorf("note %d velocity: got %d, want 64", n.ID, n.Vel)
		}
	}
}

func TestApply_SetVelocity_OutOfRange_Skipped(t *testing.T) {
	a := makeArrangement(t)
	ids := noteIDs(t, a, "bass", "clip1")
	batch := BeckyEditBatch{
		Edits: []BeckyEdit{{
			Op:       OpSetVelocity,
			Track:    "bass",
			Clip:     "clip1",
			NoteIDs:  ids,
			Velocity: 0, // invalid: must be 1..127
		}},
	}
	_, res, _ := Apply(a, batch, nil)
	if res.Skipped != 1 {
		t.Error("expected skip for velocity 0")
	}
}

// ---- set_step ----------------------------------------------------------------

func TestApply_SetStep_TurnOn(t *testing.T) {
	a := makeArrangement(t)
	// step 4 on lane 0 should be off initially; turn it on
	batch := BeckyEditBatch{
		Edits: []BeckyEdit{{
			Op:      OpSetStep,
			Track:   "kick",
			Clip:    "drums",
			LaneIdx: 0,
			Step:    4,
			On:      true,
			StepVel: 100,
		}},
	}
	out, res, err := Apply(a, batch, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Applied != 1 {
		t.Fatalf("want 1 applied: %s", res.Outcomes[0].Reason)
	}
	// Derive the grid from the output and confirm step 4 is on.
	grid, err := out.DrumGridOf("kick", "drums", 0)
	if err != nil {
		t.Fatalf("derive grid: %v", err)
	}
	if len(grid.Lanes) == 0 {
		t.Fatal("no lanes in output grid")
	}
	if len(grid.Lanes[0].On) <= 4 || !grid.Lanes[0].On[4] {
		t.Errorf("step 4 not on after set_step: lane0.On=%v", grid.Lanes[0].On)
	}
}

func TestApply_SetStep_LaneOutOfRange_Skipped(t *testing.T) {
	a := makeArrangement(t)
	batch := BeckyEditBatch{
		Edits: []BeckyEdit{{
			Op:      OpSetStep,
			Track:   "kick",
			Clip:    "drums",
			LaneIdx: 99, // way out of range
			Step:    0,
			On:      true,
		}},
	}
	_, res, _ := Apply(a, batch, nil)
	if res.Skipped != 1 {
		t.Error("expected skip for out-of-range lane_idx")
	}
}

func TestApply_SetStep_StepOutOfRange_Skipped(t *testing.T) {
	a := makeArrangement(t)
	batch := BeckyEditBatch{
		Edits: []BeckyEdit{{
			Op:      OpSetStep,
			Track:   "kick",
			Clip:    "drums",
			LaneIdx: 0,
			Step:    9999,
			On:      true,
		}},
	}
	_, res, _ := Apply(a, batch, nil)
	if res.Skipped != 1 {
		t.Error("expected skip for out-of-range step")
	}
}

// ---- set_gain ----------------------------------------------------------------

func TestApply_SetGain_OK(t *testing.T) {
	a := makeArrangement(t)
	batch := BeckyEditBatch{
		Edits: []BeckyEdit{{Op: OpSetGain, Target: "bass", Gain: gp(0.8)}},
	}
	out, res, _ := Apply(a, batch, nil)
	if res.Applied != 1 {
		t.Fatalf("want 1 applied: %s", res.Outcomes[0].Reason)
	}
	tr, ok := out.TrackByID("bass")
	if !ok {
		t.Fatal("track not found")
	}
	if tr.Strip.Gain != 0.8 {
		t.Errorf("Gain: got %v, want 0.8", tr.Strip.Gain)
	}
}

// gp returns a pointer to a float64. set_gain's Gain is a pointer so an omitted JSON
// "gain" (nil) is distinguishable from an explicit 0.0.
func gp(v float64) *float64 { return &v }

func TestApply_SetGain_OmittedIsRejected(t *testing.T) {
	a := makeArrangement(t)
	// A model that forgets "gain" must NOT silence the track — the edit is skipped
	// with a reason, not applied as gain 0.
	batch := BeckyEditBatch{Edits: []BeckyEdit{{Op: OpSetGain, Target: "bass"}}}
	_, res, _ := Apply(a, batch, nil)
	if res.Applied != 0 || res.Skipped != 1 {
		t.Fatalf("omitted gain must be skipped, not applied: applied=%d skipped=%d", res.Applied, res.Skipped)
	}
}

func TestApply_SetGain_CaseInsensitive(t *testing.T) {
	a := makeArrangement(t)
	batch := BeckyEditBatch{
		Edits: []BeckyEdit{{Op: OpSetGain, Target: "BASS", Gain: gp(1.2)}},
	}
	_, res, _ := Apply(a, batch, nil)
	if res.Applied != 1 {
		t.Fatalf("case-insensitive resolve failed: %s", res.Outcomes[0].Reason)
	}
}

func TestApply_SetGain_OutOfRange_Skipped(t *testing.T) {
	a := makeArrangement(t)
	batch := BeckyEditBatch{
		Edits: []BeckyEdit{{Op: OpSetGain, Target: "bass", Gain: gp(3.0)}},
	}
	_, res, _ := Apply(a, batch, nil)
	if res.Skipped != 1 {
		t.Error("expected skip for gain > 2")
	}
}

// ---- set_pan -----------------------------------------------------------------

func TestApply_SetPan_OK(t *testing.T) {
	a := makeArrangement(t)
	batch := BeckyEditBatch{
		Edits: []BeckyEdit{{Op: OpSetPan, Target: "bass", Pan: -0.5}},
	}
	out, res, _ := Apply(a, batch, nil)
	if res.Applied != 1 {
		t.Fatalf("want 1 applied: %s", res.Outcomes[0].Reason)
	}
	tr, _ := out.TrackByID("bass")
	if tr.Strip.Pan != -0.5 {
		t.Errorf("Pan: got %v, want -0.5", tr.Strip.Pan)
	}
}

func TestApply_SetPan_OutOfRange_Skipped(t *testing.T) {
	a := makeArrangement(t)
	batch := BeckyEditBatch{
		Edits: []BeckyEdit{{Op: OpSetPan, Target: "bass", Pan: 2.0}},
	}
	_, res, _ := Apply(a, batch, nil)
	if res.Skipped != 1 {
		t.Error("expected skip for pan > 1")
	}
}

// ---- mute / solo -------------------------------------------------------------

func TestApply_Mute_OK(t *testing.T) {
	a := makeArrangement(t)
	batch := BeckyEditBatch{
		Edits: []BeckyEdit{{Op: OpMute, Target: "kick", Muted: true}},
	}
	out, res, _ := Apply(a, batch, nil)
	if res.Applied != 1 {
		t.Fatalf("want 1 applied: %s", res.Outcomes[0].Reason)
	}
	tr, _ := out.TrackByID("kick")
	if !tr.Strip.Mute {
		t.Error("track should be muted")
	}
}

func TestApply_Solo_OK(t *testing.T) {
	a := makeArrangement(t)
	batch := BeckyEditBatch{
		Edits: []BeckyEdit{{Op: OpSolo, Target: "lead", Soloed: true}},
	}
	out, res, _ := Apply(a, batch, nil)
	if res.Applied != 1 {
		t.Fatalf("want 1 applied: %s", res.Outcomes[0].Reason)
	}
	tr, _ := out.TrackByID("lead")
	if !tr.Strip.Solo {
		t.Error("track should be soloed")
	}
}

// ---- route_to ----------------------------------------------------------------

func TestApply_RouteTo_OK(t *testing.T) {
	a := makeArrangement(t)
	batch := BeckyEditBatch{
		Edits: []BeckyEdit{{Op: OpRouteTo, Target: "bass", BusID: "bus.master"}},
	}
	out, res, _ := Apply(a, batch, nil)
	if res.Applied != 1 {
		t.Fatalf("want 1 applied: %s", res.Outcomes[0].Reason)
	}
	tr, _ := out.TrackByID("bass")
	if tr.Strip.Bus != "bus.master" {
		t.Errorf("Bus: got %q, want bus.master", tr.Strip.Bus)
	}
}

func TestApply_RouteTo_EmptyBus_Skipped(t *testing.T) {
	a := makeArrangement(t)
	batch := BeckyEditBatch{
		Edits: []BeckyEdit{{Op: OpRouteTo, Target: "bass", BusID: ""}},
	}
	_, res, _ := Apply(a, batch, nil)
	if res.Skipped != 1 {
		t.Error("expected skip for empty bus_id")
	}
}

// ---- add_sidechain -----------------------------------------------------------

func TestApply_AddSidechain_OK(t *testing.T) {
	a := makeArrangement(t)
	batch := BeckyEditBatch{
		Edits: []BeckyEdit{{
			Op:              OpAddSidechain,
			BusID:           "bus.bass",
			SidechainSource: "kick",
		}},
	}
	out, res, _ := Apply(a, batch, nil)
	if res.Applied != 1 {
		t.Fatalf("want 1 applied: %s", res.Outcomes[0].Reason)
	}
	// Find the bus.
	found := false
	for _, bus := range out.Buses {
		if bus.ID == "bus.bass" {
			for _, sc := range bus.Sidechain {
				if sc == "kick" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("sidechain edge not created")
	}
}

func TestApply_AddSidechain_EmptyBus_Skipped(t *testing.T) {
	a := makeArrangement(t)
	batch := BeckyEditBatch{
		Edits: []BeckyEdit{{Op: OpAddSidechain, BusID: "", SidechainSource: "kick"}},
	}
	_, res, _ := Apply(a, batch, nil)
	if res.Skipped != 1 {
		t.Error("expected skip for empty bus_id")
	}
}

func TestApply_AddSidechain_EmptySource_Skipped(t *testing.T) {
	a := makeArrangement(t)
	batch := BeckyEditBatch{
		Edits: []BeckyEdit{{Op: OpAddSidechain, BusID: "bus.bass", SidechainSource: ""}},
	}
	_, res, _ := Apply(a, batch, nil)
	if res.Skipped != 1 {
		t.Error("expected skip for empty sidechain_source")
	}
}

// ---- mixed batch (good + bad) ------------------------------------------------

func TestApply_MixedBatch_GoodAndBadEdits(t *testing.T) {
	a := makeArrangement(t)
	ids := noteIDs(t, a, "bass", "clip1")

	batch := BeckyEditBatch{
		Summary: "mixed test",
		Edits: []BeckyEdit{
			{Op: OpSetTempo, BPM: 132},                                                   // good
			{Op: "unknown_op"},                                                           // bad — unknown
			{Op: OpSetGain, Target: "bass", Gain: gp(0.9)},                               // good
			{Op: OpSetGain, Target: "nonexistent", Gain: gp(1.0)},                        // bad — no track
			{Op: OpDeleteNotes, Track: "bass", Clip: "clip1", NoteIDs: []uint64{ids[0]}}, // good
			{Op: OpSetVelocity, Track: "bass", Clip: "clip1", NoteIDs: ids, Velocity: 0}, // bad — vel 0
		},
	}

	out, res, err := Apply(a, batch, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Applied != 3 {
		t.Errorf("want 3 applied, got %d", res.Applied)
	}
	if res.Skipped != 3 {
		t.Errorf("want 3 skipped, got %d", res.Skipped)
	}
	// The good edits should have taken effect.
	if out.BPM != 132 {
		t.Errorf("BPM not applied: got %d", out.BPM)
	}
	tr, _ := out.TrackByID("bass")
	if tr.Strip.Gain != 0.9 {
		t.Errorf("Gain not applied: got %v", tr.Strip.Gain)
	}
	// One note deleted.
	if noteCount(t, out, "bass", "clip1") != len(ids)-1 {
		t.Errorf("delete_notes not applied")
	}
}

// ---- determinism -------------------------------------------------------------

func TestApply_Determinism(t *testing.T) {
	a := makeArrangement(t)
	ids := noteIDs(t, a, "bass", "clip1")
	batch := BeckyEditBatch{
		Summary: "determinism test",
		Edits: []BeckyEdit{
			{Op: OpSetTempo, BPM: 150},
			{Op: OpSetGain, Target: "lead", Gain: gp(1.5)},
			{Op: OpTranspose, Track: "bass", Clip: "clip1", Semitones: -2},
			{Op: OpSetVelocity, Track: "bass", Clip: "clip1", NoteIDs: ids, Velocity: 70},
		},
	}

	out1, res1, _ := Apply(a, batch, nil)
	out2, res2, _ := Apply(a, batch, nil)

	// Results must be identical.
	if res1.Applied != res2.Applied || res1.Skipped != res2.Skipped {
		t.Errorf("results differ: %d/%d vs %d/%d", res1.Applied, res1.Skipped, res2.Applied, res2.Skipped)
	}

	// Marshal both outputs and compare bytes.
	b1, _ := json.Marshal(out1)
	b2, _ := json.Marshal(out2)
	if string(b1) != string(b2) {
		t.Error("outputs differ between two identical Apply calls (not deterministic)")
	}
}

// ---- immutability: original arrangement is never modified --------------------

func TestApply_OriginalUnchanged(t *testing.T) {
	a := makeArrangement(t)
	origBPM := a.BPM
	origNoteCount := a.NoteCount()

	batch := BeckyEditBatch{
		Edits: []BeckyEdit{
			{Op: OpSetTempo, BPM: 200},
			{Op: OpAddNotes, Track: "lead", Notes: [][]float64{{60, 0, 1, 100}}},
			{Op: OpSetGain, Target: "bass", Gain: gp(0.1)},
		},
	}
	_, _, err := Apply(a, batch, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.BPM != origBPM {
		t.Errorf("original BPM mutated: %d -> %d", origBPM, a.BPM)
	}
	if a.NoteCount() != origNoteCount {
		t.Errorf("original note count mutated: %d -> %d", origNoteCount, a.NoteCount())
	}
}

// ---- empty track ref / missing clip -----------------------------------------

func TestApply_EmptyTargetRef_Skipped(t *testing.T) {
	a := makeArrangement(t)
	batch := BeckyEditBatch{
		Edits: []BeckyEdit{{Op: OpSetGain, Target: "", Gain: gp(1.0)}},
	}
	_, res, _ := Apply(a, batch, nil)
	if res.Skipped != 1 {
		t.Error("expected skip for empty target")
	}
}

func TestApply_TrackWithNoClips_Skipped(t *testing.T) {
	a := dawmodel.New()
	a = a.AddTrack("empty-track", dawmodel.KindMIDI)
	// No clip added — track exists but has no clips.
	batch := BeckyEditBatch{
		Edits: []BeckyEdit{{
			Op:    OpAddNotes,
			Track: "empty-track",
			Notes: [][]float64{{60, 0, 1, 100}},
		}},
	}
	_, res, _ := Apply(a, batch, nil)
	if res.Skipped != 1 {
		t.Errorf("expected skip for track with no clips, got applied=%d skipped=%d",
			res.Applied, res.Skipped)
	}
}

// ---- result outcome indexing is correct --------------------------------------

func TestApply_OutcomeIndexing(t *testing.T) {
	a := makeArrangement(t)
	batch := BeckyEditBatch{
		Edits: []BeckyEdit{
			{Op: "bad1"},
			{Op: OpSetTempo, BPM: 120},
			{Op: "bad2"},
		},
	}
	_, res, _ := Apply(a, batch, nil)
	if len(res.Outcomes) != 3 {
		t.Fatalf("want 3 outcomes, got %d", len(res.Outcomes))
	}
	for i, o := range res.Outcomes {
		if o.Index != i {
			t.Errorf("outcome[%d].Index = %d, want %d", i, o.Index, i)
		}
	}
	if res.Outcomes[0].Applied || !res.Outcomes[1].Applied || res.Outcomes[2].Applied {
		t.Errorf("unexpected outcome.Applied: %v %v %v",
			res.Outcomes[0].Applied, res.Outcomes[1].Applied, res.Outcomes[2].Applied)
	}
}

// ---- ParseBatch + Apply round-trip -------------------------------------------

func TestParseBatch_ApplyRoundTrip(t *testing.T) {
	raw := []byte(`{
		"summary": "round trip test",
		"edits": [
			{"op":"set_tempo","bpm":110},
			{"op":"set_gain","target":"bass","gain":1.1},
			{"op":"set_pan","target":"kick","pan":0.3}
		]
	}`)
	a := makeArrangement(t)
	batch, err := ParseBatch(raw)
	if err != nil {
		t.Fatalf("ParseBatch: %v", err)
	}
	out, res, err := Apply(a, batch, nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Applied != 3 {
		t.Errorf("want 3 applied, got %d", res.Applied)
		for _, o := range res.Outcomes {
			if !o.Applied {
				t.Logf("skipped: %s", o.Reason)
			}
		}
	}
	if out.BPM != 110 {
		t.Errorf("BPM: got %d", out.BPM)
	}
}
