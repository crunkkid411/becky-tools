package vox

import (
	"math"
	"testing"
)

func TestMatchPitch_StackModeTargetsGuide(t *testing.T) {
	// Stack mode: a flat alt note (218 Hz) over a guide A4 (220 Hz) -> target 220,
	// a small upward move, PSOLA engine (small shift), not flagged.
	guide := []DetectedNote{{StartMs: 0, EndMs: 500, Hz: 220.0, Confidence: 0.95}}
	alt := []DetectedNote{{StartMs: 0, EndMs: 500, Hz: 218.0, Confidence: 0.92}}
	notes := MatchPitch(guide, alt, nil, DefaultAlignOptions())
	if len(notes) != 1 {
		t.Fatalf("got %d notes, want 1", len(notes))
	}
	if math.Abs(notes[0].TargetHz-220.0) > 0.5 {
		t.Errorf("target = %.2f Hz, want ~220 (the guide note)", notes[0].TargetHz)
	}
	if notes[0].MoveCents <= 0 {
		t.Errorf("a flat note should move UP, got %.2f cents", notes[0].MoveCents)
	}
	if notes[0].EngineUsed != "psola" {
		t.Errorf("small shift should use psola, got %q", notes[0].EngineUsed)
	}
}

func TestMatchPitch_TuneModeSnapsToScale(t *testing.T) {
	// Tune mode in A minor: a 233 Hz note (~A#3, out of A minor) snaps to a scale tone.
	opt := DefaultAlignOptions()
	opt.Mode = "tune"
	opt.Key = "Aminor"
	alt := []DetectedNote{{StartMs: 0, EndMs: 400, Hz: 233.0, Confidence: 0.9}}
	notes := MatchPitch(nil, alt, nil, opt)
	if len(notes) != 1 {
		t.Fatalf("got %d notes, want 1", len(notes))
	}
	midi := int(math.Round(hzToMidiF(notes[0].TargetHz)))
	pc := ((midi % 12) + 12) % 12
	// A minor scale pitch classes: A=9, B=11, C=0, D=2, E=4, F=5, G=7.
	inAmin := map[int]bool{9: true, 11: true, 0: true, 2: true, 4: true, 5: true, 7: true}
	if !inAmin[pc] {
		t.Errorf("tune target pitch class %d not in A minor", pc)
	}
}

func TestMatchPitch_CrossCheckDisagreementFlags(t *testing.T) {
	// pYIN says 220 Hz, Praat cross-check says 440 Hz (an octave error): becky must
	// FLAG it, not silently tune — corroborate-then-conclude (SPEC §3.1.3).
	guide := []DetectedNote{{StartMs: 0, EndMs: 400, Hz: 220.0, Confidence: 0.9}}
	alt := []DetectedNote{{StartMs: 0, EndMs: 400, Hz: 220.0, Confidence: 0.9}}
	notes := MatchPitch(guide, alt, []float64{440.0}, DefaultAlignOptions())
	if !notes[0].Flagged {
		t.Error("octave disagreement between F0 and cross-check must be flagged")
	}
}

func TestMatchPitch_ClampsToMaxShift(t *testing.T) {
	// A wild alt (110 Hz) under a guide A4 (220 Hz) is +1200 cents; with max-shift 2
	// semitones the move is clamped to +200 cents.
	opt := DefaultAlignOptions()
	opt.MaxShiftSemi = 2
	guide := []DetectedNote{{StartMs: 0, EndMs: 400, Hz: 220.0, Confidence: 0.9}}
	alt := []DetectedNote{{StartMs: 0, EndMs: 400, Hz: 110.0, Confidence: 0.9}}
	notes := MatchPitch(guide, alt, nil, opt)
	if notes[0].MoveCents > 200.0001 {
		t.Errorf("move not clamped: %.2f cents (max 200)", notes[0].MoveCents)
	}
}

func TestMatchPitch_LowConfidenceFlags(t *testing.T) {
	guide := []DetectedNote{{StartMs: 0, EndMs: 400, Hz: 220.0, Confidence: 0.9}}
	alt := []DetectedNote{{StartMs: 0, EndMs: 400, Hz: 220.0, Confidence: 0.3}}
	notes := MatchPitch(guide, alt, nil, DefaultAlignOptions())
	if !notes[0].Flagged {
		t.Error("a low-confidence F0 note must be flagged, not moved silently")
	}
}

func TestMatchPitch_Deterministic(t *testing.T) {
	guide := []DetectedNote{{StartMs: 0, EndMs: 400, Hz: 220.0, Confidence: 0.9}}
	alt := []DetectedNote{{StartMs: 0, EndMs: 400, Hz: 219.0, Confidence: 0.9}}
	a := MatchPitch(guide, alt, nil, DefaultAlignOptions())
	b := MatchPitch(guide, alt, nil, DefaultAlignOptions())
	if a[0] != b[0] {
		t.Errorf("MatchPitch not deterministic: %+v vs %+v", a[0], b[0])
	}
}
