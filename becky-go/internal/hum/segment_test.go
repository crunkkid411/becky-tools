package hum

import "testing"

// contour builds a run of frames at one F0 (Hz) and voicing, from t0, count frames
// spaced dt seconds apart.
func contour(t0, dt float64, count int, hz, voiced float64) []Frame {
	out := make([]Frame, count)
	for i := range out {
		out[i] = Frame{T: t0 + float64(i)*dt, F0: hz, Voiced: voiced}
	}
	return out
}

func TestSegmentNotes_TwoNotesSplitOnPitchJump(t *testing.T) {
	// 0.0-0.3s on A4 (440), then 0.3-0.6s on C5 (523.25): two notes.
	frames := append(contour(0.0, 0.03, 10, 440.0, 0.9), contour(0.3, 0.03, 10, 523.25, 0.9)...)
	notes := SegmentNotes(frames, nil, DefaultSegmentOptions())
	if len(notes) != 2 {
		t.Fatalf("got %d notes, want 2: %+v", len(notes), notes)
	}
	if notes[0].Midi != 69 { // A4
		t.Errorf("note 0 MIDI = %d, want 69 (A4)", notes[0].Midi)
	}
	if notes[1].Midi != 72 { // C5
		t.Errorf("note 1 MIDI = %d, want 72 (C5)", notes[1].Midi)
	}
}

func TestSegmentNotes_UnvoicedGapBreaksNotes(t *testing.T) {
	voiced := contour(0.0, 0.03, 8, 440.0, 0.9)
	gap := contour(0.24, 0.03, 4, 0, 0.0) // silence/breath
	more := contour(0.36, 0.03, 8, 440.0, 0.9)
	notes := SegmentNotes(append(append(voiced, gap...), more...), nil, DefaultSegmentOptions())
	if len(notes) != 2 {
		t.Fatalf("unvoiced gap should split into 2 notes, got %d", len(notes))
	}
}

func TestSegmentNotes_MinLengthDropsBlips(t *testing.T) {
	opt := DefaultSegmentOptions()
	opt.MinNoteSec = 0.2
	blip := contour(0.0, 0.03, 2, 440.0, 0.9) // ~0.03s, below threshold
	notes := SegmentNotes(blip, nil, opt)
	if len(notes) != 0 {
		t.Errorf("sub-threshold blip should be dropped, got %d notes", len(notes))
	}
}

func TestSegmentNotes_MedianPitchRobustToScoop(t *testing.T) {
	// A held A4 with one scooped-low frame: median ignores the outlier -> still A4.
	frames := contour(0.0, 0.03, 9, 440.0, 0.9)
	frames[0].F0 = 415.30 // a G#4 scoop at the attack
	notes := SegmentNotes(frames, nil, DefaultSegmentOptions())
	if len(notes) != 1 || notes[0].Midi != 69 {
		t.Errorf("median should reject the scoop -> A4 (69); got %+v", notes)
	}
}

func TestSegmentNotes_StubNotesUsedDirectly(t *testing.T) {
	sn := []StubNote{
		{Onset: 0.0, Dur: 0.5, Midi: 60, Confidence: 0.9},
		{Onset: 0.5, Dur: 0.5, Midi: 64, Confidence: 0.8},
	}
	notes := SegmentNotes(nil, sn, DefaultSegmentOptions())
	if len(notes) != 2 || notes[0].Midi != 60 || notes[1].Midi != 64 {
		t.Errorf("stub notes should pass through; got %+v", notes)
	}
	if notes[0].I != 0 || notes[1].I != 1 {
		t.Errorf("note indices not assigned: %+v", notes)
	}
}

func TestQuantize_SnapsToGrid(t *testing.T) {
	// At 120 BPM, 1/16 step = 0.125 s. An onset at 0.13 snaps to 0.125.
	notes := []Note{{OnsetSec: 0.13, DurSec: 0.13}}
	out := quantize(notes, 120, 16)
	if out[0].OnsetSec != 0.125 {
		t.Errorf("onset snapped to %.4f, want 0.125", out[0].OnsetSec)
	}
}

func TestBuildLane_DownsamplesAndStaysEditable(t *testing.T) {
	frames := contour(0.0, 0.01, 2000, 440.0, 0.9)
	lane := BuildLane(frames, nil, 600)
	if len(lane.Curve) == 0 || len(lane.Curve) > 600 {
		t.Fatalf("lane curve length %d, want 1..600", len(lane.Curve))
	}
	if !lane.Curve[0].Editable {
		t.Error("lane points must be editable (visual-first substrate)")
	}
}

func TestSegmentNotes_Deterministic(t *testing.T) {
	frames := append(contour(0.0, 0.03, 10, 440.0, 0.9), contour(0.3, 0.03, 10, 494.0, 0.9)...)
	a := SegmentNotes(frames, nil, DefaultSegmentOptions())
	b := SegmentNotes(frames, nil, DefaultSegmentOptions())
	if len(a) != len(b) {
		t.Fatalf("non-deterministic note count %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("note %d differs: %+v vs %+v", i, a[i], b[i])
		}
	}
}
