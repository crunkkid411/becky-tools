package canvas

// transform.go — the SELECT→ASK→TRANSFORM loop model.
//
// This is the pure-Go, tagless model for the "show me, don't do it" overlay.
// No GUI, no Gio, no cgo — so it is unit-testable on headless CI (CLAUDE.md §3).
//
// Design north star (CLAUDE.md §6 / CANVAS-INSPIRATION.md):
//
//	Jordan selects something on the canvas → types a plain-language instruction
//	in the agent box → becky proposes an IN-PLACE change shown as a preview
//	(before/after in colours and shapes, short plain summary) → Jordan approves
//	or rejects EXPLICITLY. Nothing mutates until he approves.
//
// Boundaries:
//   - Selection / Proposal / Transformer are pure data+interface (this file).
//   - StubTransformer is the deterministic offline implementation.
//   - The real Gemma-4 / LFM2.5-VL call is the LOCAL AGENT BOUNDARY; see the
//     docblock on Transformer for how to plug it in.
//   - Apply returns a NEW scene (immutable update, coding-style).
//   - RejectScene returns the original scene unchanged.
//   - All errors degrade gracefully; never panic.
//
// OFFLINE + DETERMINISTIC (CLAUDE.md §2): StubTransformer.Propose is seeded
// only from its inputs — no randomness, no clock — so the same selection +
// instruction always yields the same Proposal.

import (
	"fmt"
	"strings"
)

// ─── Selection ───────────────────────────────────────────────────────────────

// SelectionKind names what class of canvas element is selected.
type SelectionKind string

const (
	// SelectNone means nothing is actively selected.
	SelectNone SelectionKind = "none"
	// SelectRegion is a time-range on the canvas (start/end tick span).
	SelectRegion SelectionKind = "region"
	// SelectTrack is a whole track lane.
	SelectTrack SelectionKind = "track"
	// SelectClip is a specific clip on a track.
	SelectClip SelectionKind = "clip"
	// SelectNote is a MIDI note (used in piano-roll mode).
	SelectNote SelectionKind = "note"
	// SelectStroke is a pen stroke drawn on the canvas.
	SelectStroke SelectionKind = "stroke"
)

// Selection describes WHAT is currently selected on the canvas. It is
// deliberately minimal: only the fields that let a Transformer identify the
// target. The GUI sets this when the user activates a region/clip/note; the
// zero value (Kind=SelectNone) means "nothing selected".
type Selection struct {
	Kind    SelectionKind `json:"kind"`
	TrackID string        `json:"trackId,omitempty"`   // SelectTrack / SelectClip
	ClipID  string        `json:"clipId,omitempty"`    // SelectClip
	NoteIdx int           `json:"noteIdx,omitempty"`   // SelectNote (index in PitchLane.Points)
	StartTk int64         `json:"startTick,omitempty"` // SelectRegion / note start
	EndTk   int64         `json:"endTick,omitempty"`   // SelectRegion / note end
	Label   string        `json:"label,omitempty"`     // human-readable name of the target
}

// Empty reports whether the selection covers nothing.
func (s Selection) Empty() bool {
	return s.Kind == SelectNone || s.Kind == ""
}

// String returns a compact human-readable description for captions and logging.
func (s Selection) String() string {
	switch s.Kind {
	case SelectRegion:
		return fmt.Sprintf("region %d–%d", s.StartTk, s.EndTk)
	case SelectTrack:
		if s.Label != "" {
			return "track " + s.Label
		}
		return "track " + s.TrackID
	case SelectClip:
		if s.Label != "" {
			return "clip " + s.Label
		}
		return "clip " + s.ClipID
	case SelectNote:
		return fmt.Sprintf("note[%d]", s.NoteIdx)
	case SelectStroke:
		return "stroke"
	default:
		return "nothing"
	}
}

// ─── Proposal ────────────────────────────────────────────────────────────────

// ChangeKind names the category of the proposed in-place change.
type ChangeKind string

const (
	ChangeUnknown   ChangeKind = "unknown"   // transformer couldn't classify
	ChangePitch     ChangeKind = "pitch"     // pitch/transpose adjustment
	ChangeTiming    ChangeKind = "timing"    // move/nudge in time
	ChangeTrim      ChangeKind = "trim"      // shorten or extend a clip/region
	ChangeGain      ChangeKind = "gain"      // volume/level change
	ChangeRoute     ChangeKind = "route"     // reroute a bus/sidechain
	ChangeText      ChangeKind = "text"      // rename or relabel something
	ChangeStructure ChangeKind = "structure" // add/remove tracks or clips
)

// Proposal is what the Transformer returns: a description of a proposed
// in-place change WITHOUT yet applying it. The GUI renders this as the
// "show me, don't do it" preview overlay.
//
// The proposal is SELF-CONTAINED: all information the GUI needs to draw the
// preview (Before/After labels, the summary, the target selection) is carried
// here. Apply turns it into a new Scene; RejectScene discards it.
type Proposal struct {
	// ID is a deterministic identifier (no randomness, no wall-clock time).
	ID string `json:"id"`

	// Sel is the selection this proposal acts on (echoed from the request).
	Sel Selection `json:"selection"`

	// Instruction is the plain-language text the user typed (echoed for the log).
	Instruction string `json:"instruction"`

	// Kind classifies the change so the overlay can choose a colour/icon.
	Kind ChangeKind `json:"kind"`

	// Summary is a SHORT (≤1 sentence) plain-language description of the
	// change. This is the one line of text shown in the overlay.
	Summary string `json:"summary"`

	// Before is a short human-readable string for the BEFORE state of the
	// selected element (e.g. "C3", "-6 dB", "tick 480").
	Before string `json:"before,omitempty"`

	// After is the proposed new state (e.g. "D3", "-3 dB", "tick 600").
	After string `json:"after,omitempty"`

	// Delta is an optional numeric magnitude of the change (e.g. +2 semitones,
	// -1 beat = -120 ticks). Zero means "unquantified / not applicable".
	Delta float64 `json:"delta,omitempty"`

	// ScenePatch is an optional partial scene describing the changed state.
	// When non-nil, Apply merges it into a new scene.
	// When nil, Apply records the Before→After as a Correction only.
	// The stub never emits a ScenePatch; the real model may.
	ScenePatch *Scene `json:"scenePatch,omitempty"`
}

// ─── Transformer ─────────────────────────────────────────────────────────────

// Transformer is the interface the propose/approve/reject loop uses.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │  LOCAL AGENT BOUNDARY — GPU / real model call goes here                  │
// │                                                                          │
// │  Write a concrete struct that implements Transformer and calls the       │
// │  local model CLI (e.g. gemma-cli, llama-cli) via exec.Command.          │
// │                                                                          │
// │  Input contract:                                                         │
// │    {"scene": <scene>, "selection": <sel>, "instruction": <string>}      │
// │  Output contract:                                                        │
// │    {"kind":…, "summary":…, "before":…, "after":…, "delta":…}           │
// │                                                                          │
// │  Wire it into the GUI by setting App.transformer to your implementation. │
// │  Fall back to StubTransformer on any exec error so the GUI degrades      │
// │  gracefully when the model binary is absent.                             │
// └──────────────────────────────────────────────────────────────────────────┘
type Transformer interface {
	// Propose analyses the selection and instruction and returns a Proposal
	// WITHOUT applying any change. The scene is provided read-only for context.
	// On failure, Propose returns nil and a non-nil error.
	Propose(scene Scene, sel Selection, instruction string) (*Proposal, error)
}

// ─── Apply / Reject ──────────────────────────────────────────────────────────

// Apply returns a NEW scene with the proposal's changes merged in.
// The original scene is never mutated (immutable update, coding-style).
//
// If the proposal carries a ScenePatch, Apply merges it (tracks + routing).
// Otherwise (text-only proposal from the stub), Apply records the approved
// change in the scene's Corrections log and returns the scene otherwise
// unchanged — the structural mutation is the model's job when it supplies a
// full ScenePatch.
//
// degrade-never-crash: a nil proposal returns the original scene + a wrapped
// error (the GUI shows this as a neon "couldn't apply" line, never a crash).
func Apply(scene Scene, p *Proposal) (Scene, error) {
	if p == nil {
		return scene, fmt.Errorf("apply: nil proposal")
	}

	next := scene // start from the original (Scene is a value type)

	if p.ScenePatch != nil {
		next = applyPatch(next, p.ScenePatch)
	}

	// Record the approval as a Correction (the habits/learning hook).
	c := Correction{
		Kind:    correctionKind(p.Kind),
		TrackID: p.Sel.TrackID,
		ClipID:  p.Sel.ClipID,
		At:      p.Sel.StartTk,
		Before:  parseFloat(p.Before),
		After:   parseFloat(p.After),
		Note:    p.Summary,
	}
	next.Corrections = next.Corrections.Append(c)
	return next, nil
}

// RejectScene returns the passed scene unmodified — the proposal is discarded.
// Named with "Scene" so call sites read naturally: "keep the original scene".
func RejectScene(scene Scene, _ *Proposal) Scene { return scene }

// correctionKind maps a ChangeKind to the CorrectionKind the corrections log uses.
func correctionKind(k ChangeKind) CorrectionKind {
	switch k {
	case ChangePitch:
		return FixPitch
	case ChangeTiming, ChangeTrim:
		return FixTiming
	case ChangeGain:
		return FixGain
	case ChangeRoute:
		return FixRoute
	default:
		return FixOther
	}
}

// ─── Scene patch merge ───────────────────────────────────────────────────────

// applyPatch returns a new Scene with the patch's tracks merged in. Tracks
// present in the patch (by ID) replace matching scene tracks; new patch tracks
// are appended. Patch routing edges are appended. Pure: original scene
// is never mutated.
func applyPatch(scene Scene, patch *Scene) Scene {
	if patch == nil {
		return scene
	}

	byID := make(map[string]int, len(scene.Tracks))
	for i, t := range scene.Tracks {
		byID[t.ID] = i
	}

	tracks := make([]Track, len(scene.Tracks))
	copy(tracks, scene.Tracks)

	for _, pt := range patch.Tracks {
		if idx, ok := byID[pt.ID]; ok {
			tracks[idx] = pt
		} else {
			tracks = append(tracks, pt)
		}
	}

	routing := make([]RouteEdge, len(scene.Routing), len(scene.Routing)+len(patch.Routing))
	copy(routing, scene.Routing)
	routing = append(routing, patch.Routing...)

	sortTracks(tracks)
	sortRouting(routing)

	next := scene
	next.Tracks = tracks
	next.Routing = routing
	return next
}

// ─── StubTransformer ─────────────────────────────────────────────────────────

// StubTransformer is the deterministic offline Transformer. It generates
// structured Proposals from the instruction text using simple keyword matching
// — no model, no network, no randomness. This lets the full loop work on
// headless CI and in the GUI before the real model is wired in.
//
// Replace or wrap it with a real model implementation (see Transformer doc).
type StubTransformer struct{}

// Propose produces a deterministic Proposal from the instruction.
func (StubTransformer) Propose(scene Scene, sel Selection, instruction string) (*Proposal, error) {
	if sel.Empty() {
		return nil, fmt.Errorf("propose: nothing selected — select a region, clip, track, or note first")
	}
	if strings.TrimSpace(instruction) == "" {
		return nil, fmt.Errorf("propose: empty instruction")
	}

	kind, before, after, delta, summary := stubAnalyse(scene, sel, instruction)
	return &Proposal{
		ID:          stubID(sel, instruction),
		Sel:         sel,
		Instruction: instruction,
		Kind:        kind,
		Summary:     summary,
		Before:      before,
		After:       after,
		Delta:       delta,
	}, nil
}

// stubAnalyse classifies the instruction and computes before/after/delta.
// All outputs are deterministic: same inputs → same outputs.
func stubAnalyse(scene Scene, sel Selection, instruction string) (
	kind ChangeKind, before, after string, delta float64, summary string,
) {
	q := strings.ToLower(instruction)

	switch {
	case containsAny(q, "pitch", "transpose", "semitone", "octave", "higher", "lower"):
		kind = ChangePitch
		currentPitch := currentPitchLabel(scene, sel)
		d := stubPitchDelta(q)
		before = currentPitch
		after = shiftedPitchLabel(currentPitch, d)
		delta = d
		summary = fmt.Sprintf("Transpose %s by %+.0f semitone(s): %s → %s", sel.String(), d, before, after)

	case containsAny(q, "move", "shift", "nudge", "earlier", "later"):
		kind = ChangeTiming
		d := stubTimingDelta(q)
		before = fmt.Sprintf("tick %d", sel.StartTk)
		after = fmt.Sprintf("tick %d", sel.StartTk+int64(d))
		delta = d
		summary = fmt.Sprintf("Move %s by %+.0f tick(s)", sel.String(), d)

	case containsAny(q, "trim", "shorten", "extend", "longer", "shorter"):
		kind = ChangeTrim
		span := sel.EndTk - sel.StartTk
		d := stubTimingDelta(q)
		before = fmt.Sprintf("len %d", span)
		after = fmt.Sprintf("len %d", span+int64(d))
		delta = d
		summary = fmt.Sprintf("Trim %s by %+.0f tick(s)", sel.String(), d)

	case containsAny(q, "gain", "volume", "louder", "quieter", "boost", "level", "db"):
		kind = ChangeGain
		d := stubGainDelta(q)
		before = "0 dB"
		after = fmt.Sprintf("%+.1f dB", d)
		delta = d
		summary = fmt.Sprintf("Adjust gain of %s by %+.1f dB", sel.String(), d)

	case containsAny(q, "route", "bus", "sidechain", "send"):
		kind = ChangeRoute
		before = "current routing"
		after = "proposed routing"
		summary = fmt.Sprintf("Reroute %s (review routing manually)", sel.String())

	case containsAny(q, "rename", "name", "label", "call it"):
		kind = ChangeText
		before = sel.Label
		after = extractQuoted(instruction)
		if after == "" {
			after = instruction
		}
		summary = fmt.Sprintf("Rename %s to %q", sel.String(), after)

	default:
		kind = ChangeUnknown
		summary = fmt.Sprintf("Proposal for %s: %s", sel.String(), instruction)
	}
	return
}

// stubID builds a deterministic ID from the selection kind and instruction
// length+prefix (no randomness, no wall-clock time).
func stubID(sel Selection, instruction string) string {
	prefix := instruction
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	safe := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(prefix)), " ", "-")
	return fmt.Sprintf("prop-%s-%d-%s", string(sel.Kind), len(instruction), safe)
}

// ─── stub helpers ─────────────────────────────────────────────────────────────

// containsAny reports whether s contains any of the keywords.
func containsAny(s string, keywords ...string) bool {
	for _, kw := range keywords {
		if strings.Contains(s, kw) {
			return true
		}
	}
	return false
}

// stubPitchDelta infers a semitone delta from common instruction phrases.
func stubPitchDelta(q string) float64 {
	if containsAny(q, "down", "lower") {
		if containsAny(q, "octave") {
			return -12
		}
		return -1
	}
	if containsAny(q, "octave") {
		return 12
	}
	return 1 // default: up one semitone
}

// stubTimingDelta infers a tick delta from common instruction phrases.
// Heuristic: "bar"=480 ticks, "beat"=120 ticks, else 120.
func stubTimingDelta(q string) float64 {
	d := 120.0
	if containsAny(q, "bar") {
		d = 480
	} else if containsAny(q, "beat") {
		d = 120
	}
	if containsAny(q, "earlier", "back") {
		return -d
	}
	return d
}

// stubGainDelta infers a dB delta from common instruction phrases.
func stubGainDelta(q string) float64 {
	if containsAny(q, "quieter", "down", "cut") {
		return -3
	}
	return 3
}

// currentPitchLabel returns the current pitch label for the selected element,
// or "C4" as the sensible default when no pitch data is available.
func currentPitchLabel(scene Scene, sel Selection) string {
	for _, t := range scene.Tracks {
		if t.ID != sel.TrackID {
			continue
		}
		if t.Lane.Pitch != nil && sel.Kind == SelectNote {
			if sel.NoteIdx >= 0 && sel.NoteIdx < len(t.Lane.Pitch.Points) {
				return midiNoteLabel(int(t.Lane.Pitch.Points[sel.NoteIdx].Pitch))
			}
		}
	}
	return "C4"
}

// shiftedPitchLabel shifts a MIDI note label by delta semitones.
func shiftedPitchLabel(label string, delta float64) string {
	return midiNoteLabel(parseMIDILabel(label) + int(delta))
}

// midiNoteLabel returns the standard name for MIDI note n (0=C-1 … 127=G9).
func midiNoteLabel(n int) string {
	if n < 0 {
		n = 0
	}
	if n > 127 {
		n = 127
	}
	names := [12]string{"C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"}
	return fmt.Sprintf("%s%d", names[n%12], n/12-1)
}

// parseMIDILabel parses a label like "C4" → 60. Returns 60 (middle C) on failure.
func parseMIDILabel(label string) int {
	noteNames := map[string]int{
		"C": 0, "C#": 1, "Db": 1, "D": 2, "D#": 3, "Eb": 3,
		"E": 4, "F": 5, "F#": 6, "Gb": 6, "G": 7, "G#": 8, "Ab": 8,
		"A": 9, "A#": 10, "Bb": 10, "B": 11,
	}
	for noteLen := 2; noteLen >= 1; noteLen-- {
		if noteLen >= len(label) {
			continue
		}
		notePart := label[:noteLen]
		octStr := label[noteLen:]
		if base, ok := noteNames[notePart]; ok {
			var oct int
			if _, err := fmt.Sscanf(octStr, "%d", &oct); err == nil {
				return (oct+1)*12 + base
			}
		}
	}
	return 60
}

// extractQuoted extracts the first double-quoted substring from s, or "".
func extractQuoted(s string) string {
	start := strings.Index(s, `"`)
	if start < 0 {
		return ""
	}
	end := strings.Index(s[start+1:], `"`)
	if end < 0 {
		return ""
	}
	return s[start+1 : start+1+end]
}

// parseFloat parses the leading numeric value from s. Returns 0 on failure.
func parseFloat(s string) float64 {
	var v float64
	fmt.Sscanf(s, "%f", &v) //nolint:errcheck — zero-on-failure is the intended degrade
	return v
}
