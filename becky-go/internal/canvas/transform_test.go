package canvas

import (
	"strings"
	"testing"
)

// TestStubTransformer_Propose covers the deterministic offline Transformer.
func TestStubTransformer_Propose(t *testing.T) {
	t.Parallel()
	tr := StubTransformer{}

	// A scene with one MIDI track that has a pitch contour starting at C4 (note 60).
	scene := NewScene(ModeDAW)
	scene.Tracks = []Track{
		{
			ID:   "t1",
			Name: "Lead",
			Kind: LaneMIDI,
			Lane: Lane{
				Pitch: &PitchLane{
					Lo:     48,
					Hi:     84,
					Points: []PitchPoint{{Tick: 0, Pitch: 60}}, // C4
				},
			},
		},
	}

	tests := []struct {
		name        string
		sel         Selection
		instruction string
		wantErr     bool
		wantKind    ChangeKind
		wantSummary string // substring that MUST appear in Summary
	}{
		{
			name:        "error: empty selection",
			sel:         Selection{Kind: SelectNone},
			instruction: "transpose up",
			wantErr:     true,
		},
		{
			name:        "error: empty instruction",
			sel:         Selection{Kind: SelectClip, ClipID: "c1"},
			instruction: "  ",
			wantErr:     true,
		},
		{
			name:        "pitch up one semitone",
			sel:         Selection{Kind: SelectNote, TrackID: "t1", NoteIdx: 0},
			instruction: "transpose up one semitone",
			wantKind:    ChangePitch,
			wantSummary: "+1",
		},
		{
			name:        "pitch down one semitone",
			sel:         Selection{Kind: SelectNote, TrackID: "t1", NoteIdx: 0},
			instruction: "lower the pitch",
			wantKind:    ChangePitch,
			wantSummary: "-1",
		},
		{
			name:        "octave up",
			sel:         Selection{Kind: SelectClip, TrackID: "t1", ClipID: "c1"},
			instruction: "move up an octave",
			wantKind:    ChangePitch,
			wantSummary: "+12",
		},
		{
			name:        "move later by one beat",
			sel:         Selection{Kind: SelectClip, TrackID: "t1", ClipID: "c1", StartTk: 480},
			instruction: "shift one beat later",
			wantKind:    ChangeTiming,
			wantSummary: "Move",
		},
		{
			name:        "move earlier by one bar",
			sel:         Selection{Kind: SelectRegion, StartTk: 960, EndTk: 1440},
			instruction: "nudge earlier by a bar",
			wantKind:    ChangeTiming,
			wantSummary: "-480",
		},
		{
			name:        "gain louder",
			sel:         Selection{Kind: SelectTrack, TrackID: "kick"},
			instruction: "make it louder",
			wantKind:    ChangeGain,
			wantSummary: "+3.0 dB",
		},
		{
			name:        "gain quieter",
			sel:         Selection{Kind: SelectTrack, TrackID: "kick"},
			instruction: "turn the volume down",
			wantKind:    ChangeGain,
			wantSummary: "-3.0 dB",
		},
		{
			name:        "trim shorter",
			sel:         Selection{Kind: SelectClip, TrackID: "t1", ClipID: "c1", StartTk: 0, EndTk: 480},
			instruction: "shorten by a beat",
			wantKind:    ChangeTrim,
			wantSummary: "Trim",
		},
		{
			name:        "rename with quoted name",
			sel:         Selection{Kind: SelectTrack, TrackID: "t1", Label: "old"},
			instruction: `rename it to "Lead Vox"`,
			wantKind:    ChangeText,
			wantSummary: "Lead Vox",
		},
		{
			name:        "reroute sidechain",
			sel:         Selection{Kind: SelectTrack, TrackID: "kick"},
			instruction: "send it to the sidechain bus",
			wantKind:    ChangeRoute,
			wantSummary: "Reroute",
		},
		{
			name:        "unknown instruction falls back gracefully",
			sel:         Selection{Kind: SelectClip, ClipID: "c1"},
			instruction: "do something cool",
			wantKind:    ChangeUnknown,
			wantSummary: "Proposal",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, err := tr.Propose(scene, tc.sel, tc.instruction)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got proposal %+v", p)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if p == nil {
				t.Fatal("got nil proposal without error")
			}
			if p.Kind != tc.wantKind {
				t.Errorf("Kind = %q, want %q", p.Kind, tc.wantKind)
			}
			if tc.wantSummary != "" && !strings.Contains(p.Summary, tc.wantSummary) {
				t.Errorf("Summary %q does not contain %q", p.Summary, tc.wantSummary)
			}
			// Proposal must always echo the selection and instruction.
			if p.Sel.Kind != tc.sel.Kind {
				t.Errorf("Proposal.Sel.Kind = %q, want %q", p.Sel.Kind, tc.sel.Kind)
			}
			if p.Instruction != tc.instruction {
				t.Errorf("Proposal.Instruction = %q, want %q", p.Instruction, tc.instruction)
			}
		})
	}
}

// TestStubTransformer_Determinism verifies the same input always produces the same Proposal.
func TestStubTransformer_Determinism(t *testing.T) {
	t.Parallel()
	tr := StubTransformer{}
	scene := NewScene(ModeDrum)
	sel := Selection{Kind: SelectTrack, TrackID: "kick", Label: "Kick"}

	p1, err := tr.Propose(scene, sel, "make it louder")
	if err != nil {
		t.Fatalf("first Propose: %v", err)
	}
	p2, err := tr.Propose(scene, sel, "make it louder")
	if err != nil {
		t.Fatalf("second Propose: %v", err)
	}
	if p1.ID != p2.ID {
		t.Errorf("IDs differ: %q vs %q", p1.ID, p2.ID)
	}
	if p1.Summary != p2.Summary {
		t.Errorf("Summaries differ: %q vs %q", p1.Summary, p2.Summary)
	}
	if p1.Before != p2.Before || p1.After != p2.After {
		t.Errorf("Before/After differ: %q/%q vs %q/%q", p1.Before, p1.After, p2.Before, p2.After)
	}
	if p1.Kind != p2.Kind {
		t.Errorf("Kind differs: %q vs %q", p1.Kind, p2.Kind)
	}
}

// TestApply_NilProposal verifies Apply degrades gracefully on nil.
func TestApply_NilProposal(t *testing.T) {
	t.Parallel()
	scene := NewScene(ModeAsk)
	_, err := Apply(scene, nil)
	if err == nil {
		t.Fatal("expected error from nil proposal, got none")
	}
}

// TestApply_RecordsCorrection verifies Apply appends to the Corrections log
// without mutating the original scene.
func TestApply_RecordsCorrection(t *testing.T) {
	t.Parallel()
	scene := NewScene(ModeMIDI)
	if scene.Corrections.Len() != 0 {
		t.Fatalf("fresh scene has %d corrections, want 0", scene.Corrections.Len())
	}

	p := &Proposal{
		ID:          "test-1",
		Sel:         Selection{Kind: SelectNote, TrackID: "t1", NoteIdx: 0, StartTk: 0},
		Instruction: "transpose up",
		Kind:        ChangePitch,
		Summary:     "Transpose note[0] by +1 semitone(s): C4 → C#4",
		Before:      "60",
		After:       "61",
		Delta:       1,
	}

	next, err := Apply(scene, p)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if next.Corrections.Len() != 1 {
		t.Errorf("Corrections len = %d, want 1", next.Corrections.Len())
	}
	c := next.Corrections.Entries[0]
	if c.Kind != FixPitch {
		t.Errorf("Correction.Kind = %q, want FixPitch", c.Kind)
	}
	// Original scene must NOT be mutated (immutable update invariant).
	if scene.Corrections.Len() != 0 {
		t.Errorf("original scene was mutated: Corrections.Len() = %d", scene.Corrections.Len())
	}
}

// TestApply_WithScenePatch verifies Apply merges a ScenePatch into the new scene.
func TestApply_WithScenePatch(t *testing.T) {
	t.Parallel()
	scene := NewScene(ModeDAW)
	scene.Tracks = []Track{
		{ID: "t1", Name: "Bass", Kind: LaneAudio},
		{ID: "t2", Name: "Drum", Kind: LaneMIDI},
	}

	// Patch: rename t1, add new track t3.
	patch := &Scene{
		Tracks: []Track{
			{ID: "t1", Name: "Bass (edited)", Kind: LaneAudio},
			{ID: "t3", Name: "Synth", Kind: LaneMIDI},
		},
	}
	p := &Proposal{
		ID:          "patch-1",
		Sel:         Selection{Kind: SelectTrack, TrackID: "t1"},
		Instruction: "rename bass and add synth",
		Kind:        ChangeStructure,
		Summary:     "Rename Bass and add Synth",
		ScenePatch:  patch,
	}

	next, err := Apply(scene, p)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(next.Tracks) != 3 {
		t.Fatalf("expected 3 tracks, got %d", len(next.Tracks))
	}
	var t1Name string
	for _, tr := range next.Tracks {
		if tr.ID == "t1" {
			t1Name = tr.Name
		}
	}
	if t1Name != "Bass (edited)" {
		t.Errorf("t1 Name = %q, want %q", t1Name, "Bass (edited)")
	}
	// Original scene untouched.
	if scene.Tracks[0].Name != "Bass" {
		t.Errorf("original t1 Name mutated to %q", scene.Tracks[0].Name)
	}
}

// TestRejectScene verifies the original scene is returned unchanged.
func TestRejectScene(t *testing.T) {
	t.Parallel()
	scene := NewScene(ModeAsk)
	scene.Tracks = []Track{{ID: "t1", Name: "Bass", Kind: LaneAudio}}

	p := &Proposal{ID: "rej-1", Kind: ChangePitch, Summary: "some change"}
	result := RejectScene(scene, p)

	if len(result.Tracks) != 1 || result.Tracks[0].Name != "Bass" {
		t.Errorf("RejectScene returned wrong tracks: %+v", result.Tracks)
	}
	if result.Corrections.Len() != 0 {
		t.Errorf("RejectScene added corrections unexpectedly: %d", result.Corrections.Len())
	}
}

// TestMIDINoteLabelRoundTrip verifies midiNoteLabel and parseMIDILabel are inverses.
func TestMIDINoteLabelRoundTrip(t *testing.T) {
	t.Parallel()
	cases := []struct {
		n     int
		label string
	}{
		{0, "C-1"},
		{12, "C0"},
		{60, "C4"},
		{61, "C#4"},
		{69, "A4"},
		{72, "C5"},
		{127, "G9"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()
			got := midiNoteLabel(tc.n)
			if got != tc.label {
				t.Errorf("midiNoteLabel(%d) = %q, want %q", tc.n, got, tc.label)
			}
			back := parseMIDILabel(tc.label)
			if back != tc.n {
				t.Errorf("parseMIDILabel(%q) = %d, want %d", tc.label, back, tc.n)
			}
		})
	}
}

// TestApplyPatch_NilPatch verifies a nil patch returns the scene untouched.
func TestApplyPatch_NilPatch(t *testing.T) {
	t.Parallel()
	scene := NewScene(ModeDAW)
	scene.Tracks = []Track{{ID: "t1", Name: "Bass", Kind: LaneAudio}}
	next := applyPatch(scene, nil)
	if len(next.Tracks) != 1 || next.Tracks[0].Name != "Bass" {
		t.Errorf("applyPatch with nil patch changed scene: %+v", next.Tracks)
	}
}

// TestSelection_Empty covers the Empty() predicate.
func TestSelection_Empty(t *testing.T) {
	t.Parallel()
	cases := []struct {
		sel   Selection
		empty bool
	}{
		{Selection{}, true},
		{Selection{Kind: SelectNone}, true},
		{Selection{Kind: SelectClip, ClipID: "c1"}, false},
		{Selection{Kind: SelectRegion, StartTk: 0, EndTk: 480}, false},
	}
	for _, tc := range cases {
		got := tc.sel.Empty()
		if got != tc.empty {
			t.Errorf("Selection{Kind:%q}.Empty() = %v, want %v", tc.sel.Kind, got, tc.empty)
		}
	}
}
