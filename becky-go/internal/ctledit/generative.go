package ctledit

import (
	"fmt"
	"strings"

	"becky-go/internal/arrange"
	"becky-go/internal/beatgen"
	"becky-go/internal/dawmodel"
	"becky-go/internal/music"
)

// generative.go wires the canvas AI edit box to internal/beatgen — the
// Playbeat-class generative rhythm engine — so a plain-English request like
// "randomize the beat" or "make the kick four on the floor" becomes a concrete,
// previewable arrangement edit (the "show me, don't do it" loop). Both ops keep
// ctledit's contract: immutable, deterministic, degrade-with-a-reason (never a
// panic). They operate on a drum clip's existing lanes — regenerating the
// pattern while keeping the kit the user already loaded.

// applyGenerateBeat regenerates a drum clip's pattern via beatgen, biased by an
// optional genre or a flat density override, seeded for determinism.
func applyGenerateBeat(a *dawmodel.Arrangement, ed BeckyEdit) (*dawmodel.Arrangement, string) {
	trackID, reason := resolveTrack(a, drumRef(ed))
	if reason != "" {
		return a, reason
	}
	clipName, reason := resolveClip(a, trackID, ed.Clip)
	if reason != "" {
		return a, reason
	}
	grid, err := a.DrumGridOf(trackID, clipName, 0)
	if err != nil {
		return a, fmt.Sprintf("generate_beat: derive grid: %v", err)
	}
	if len(grid.Lanes) == 0 {
		return a, "generate_beat: drum clip has no lanes to regenerate"
	}

	pat := beatgen.FromDrumGrid(grid)
	switch {
	case ed.Density > 0:
		d := clamp01f(ed.Density)
		for _, ln := range pat.Lanes {
			pat = pat.SetDensity(ln.Name, d)
		}
		pat = pat.Generate(beatgen.DefaultGenerateOptions(), ed.Seed)
	default:
		// Genre (empty/unknown degrades to the neutral profile inside beatgen).
		pat = pat.GenerateGenre(ed.Genre, ed.Seed)
	}

	next, reason := applyPatternToClip(a, trackID, clipName, pat)
	if reason != "" {
		return a, reason
	}
	return next, ""
}

// applyEuclidLane sets one named drum lane to a euclidean rhythm (pulses spread
// evenly across the lane, optionally rotated).
func applyEuclidLane(a *dawmodel.Arrangement, ed BeckyEdit) (*dawmodel.Arrangement, string) {
	if ed.Lane == "" {
		return a, "euclid_lane: lane name is required (e.g. \"kick\")"
	}
	if ed.Pulses <= 0 {
		return a, fmt.Sprintf("euclid_lane: pulses %d must be > 0", ed.Pulses)
	}
	trackID, reason := resolveTrack(a, drumRef(ed))
	if reason != "" {
		return a, reason
	}
	clipName, reason := resolveClip(a, trackID, ed.Clip)
	if reason != "" {
		return a, reason
	}
	grid, err := a.DrumGridOf(trackID, clipName, 0)
	if err != nil {
		return a, fmt.Sprintf("euclid_lane: derive grid: %v", err)
	}

	pat := beatgen.FromDrumGrid(grid)
	if _, ok := pat.Lane(ed.Lane); !ok {
		return a, fmt.Sprintf("euclid_lane: lane %q not found in the drum clip", ed.Lane)
	}
	pat = pat.ApplyEuclidean(ed.Lane, ed.Pulses, ed.Rotation)

	next, reason := applyPatternToClip(a, trackID, clipName, pat)
	if reason != "" {
		return a, reason
	}
	return next, ""
}

// applyAddLayer adds a stem-aware complementary layer (bass/chords/melody) via
// internal/arrange — the deterministic LEGO engine. The layer reads the existing
// stems (the kick, the key) and fits them; no model, no tokens.
func applyAddLayer(a *dawmodel.Arrangement, ed BeckyEdit) (*dawmodel.Arrangement, string) {
	if ed.Layer == "" {
		return a, "add_layer: which layer? (bass, chords, or melody)"
	}
	next, err := arrange.AddLayer(a, ed.Layer, arrange.Options{Genre: ed.Genre, Seed: ed.Seed})
	if err != nil {
		return a, fmt.Sprintf("add_layer: %v", err)
	}
	return next, ""
}

// applyDuplicateNotes copies notes forward in time — copy/paste/duplicate in one op
// (GAP-ANALYSIS #8). With note_ids it duplicates that selection; with none it
// duplicates the whole clip. d_ticks sets the offset; 0 defaults to the clip's
// bar-rounded length, so "duplicate" on a 1-bar loop appends a copy at bar 2 (doubles
// it). New notes get fresh IDs; immutable.
func applyDuplicateNotes(a *dawmodel.Arrangement, ed BeckyEdit) (*dawmodel.Arrangement, string) {
	trackID, reason := resolveTrack(a, ed.Track)
	if reason != "" {
		return a, reason
	}
	clipName, reason := resolveClip(a, trackID, ed.Clip)
	if reason != "" {
		return a, reason
	}
	src := clipNotesOf(a, trackID, clipName)
	if len(src) == 0 {
		return a, "duplicate_notes: the clip has no notes to copy"
	}
	notes := src
	if len(ed.NoteIDs) > 0 {
		want := map[uint64]bool{}
		for _, id := range ed.NoteIDs {
			want[id] = true
		}
		notes = nil
		for _, n := range src {
			if want[n.ID] {
				notes = append(notes, n)
			}
		}
		if len(notes) == 0 {
			return a, "duplicate_notes: none of the note_ids are in the clip"
		}
	}
	delta := ed.DeltaTicks
	if delta == 0 {
		delta = barRoundedSpan(notes)
	}
	if delta <= 0 {
		delta = music.BarTicks
	}
	out := a
	for _, n := range notes {
		cp := dawmodel.Note{Start: n.Start + delta, Dur: n.Dur, Pitch: n.Pitch, Vel: n.Vel, Ch: n.Ch}
		var err error
		out, _, err = out.AddNote(trackID, clipName, cp)
		if err != nil {
			return a, fmt.Sprintf("duplicate_notes: %v", err)
		}
	}
	return out, ""
}

func clipNotesOf(a *dawmodel.Arrangement, trackID, clipName string) []dawmodel.Note {
	for _, t := range a.Tracks {
		if t.ID != trackID {
			continue
		}
		for _, c := range t.Clips {
			if c.Name == clipName {
				return c.Notes
			}
		}
	}
	return nil
}

// barRoundedSpan returns the notes' end span rounded UP to a whole bar.
func barRoundedSpan(notes []dawmodel.Note) int {
	maxEnd := 0
	for _, n := range notes {
		if e := n.Start + n.Dur; e > maxEnd {
			maxEnd = e
		}
	}
	if maxEnd <= 0 {
		return music.BarTicks
	}
	bars := (maxEnd + music.BarTicks - 1) / music.BarTicks
	return bars * music.BarTicks
}

// applyAddTrack creates a new (empty) track so anything a panel can add, an agent
// can add too (the dual-operability rule). Kind defaults to MIDI; an optional Clip
// name seeds an empty clip. Refuses a duplicate id.
func applyAddTrack(a *dawmodel.Arrangement, ed BeckyEdit) (*dawmodel.Arrangement, string) {
	id := strings.TrimSpace(ed.Track)
	if id == "" {
		return a, "add_track: a track id/name is required"
	}
	if _, ok := a.TrackByID(id); ok {
		return a, fmt.Sprintf("add_track: a track %q already exists", id)
	}
	kind := dawmodel.KindMIDI
	if strings.EqualFold(ed.Kind, "audio") {
		kind = dawmodel.KindAudio
	}
	out := a.AddTrack(id, kind)
	if clip := strings.TrimSpace(ed.Clip); clip != "" {
		li := len(out.Tracks) - 1
		ch := 0
		if strings.EqualFold(id, "drums") {
			ch = 9
		}
		out.Tracks[li].Clips = append(out.Tracks[li].Clips, dawmodel.Clip{Name: clip, Channel: ch})
	}
	return out, ""
}

// applyPatternToClip compiles a beatgen pattern back into the clip's notes. It
// pins the grid's step resolution (beatgen leaves it rate-agnostic) so notes do
// not collapse onto tick 0, then applies it through the immutable verb.
func applyPatternToClip(a *dawmodel.Arrangement, trackID, clipName string, pat *beatgen.Pattern) (*dawmodel.Arrangement, string) {
	g := beatgen.ToDrumGrid(pat)
	if g.StepTicks <= 0 {
		g.StepTicks = music.StepTicks
	}
	next, err := a.ApplyDrumGrid(trackID, clipName, g)
	if err != nil {
		return a, fmt.Sprintf("apply pattern: %v", err)
	}
	return next, ""
}

// drumRef returns the track reference for a generative op, accepting either the
// note-style Track field or the mixer-style Target field (whichever the model
// populated) so the op is forgiving about which name the proposal used.
func drumRef(ed BeckyEdit) string {
	if ed.Track != "" {
		return ed.Track
	}
	return ed.Target
}

// clamp01f bounds a float to [0,1].
func clamp01f(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}
