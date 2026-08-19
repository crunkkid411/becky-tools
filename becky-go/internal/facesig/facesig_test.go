package facesig

import (
	"testing"

	"becky-go/internal/facetrack"
)

// track builds a facetrack.Track with one detection per given timestamp — the
// bbox/vector don't matter here, only whether In() correctly turns
// timestamps + samplePeriod into a density.
func track(id int, times ...float64) facetrack.Track {
	dets := make([]facetrack.Detection, len(times))
	for i, t := range times {
		dets[i] = facetrack.Detection{Time: t, BBox: [4]float64{0, 0, 10, 10}}
	}
	return facetrack.Track{ID: id, Detections: dets}
}

// This is the regression the handoff calls out by name: a face glimpsed a
// handful of times across a window must NOT score the same as one detected
// throughout it. If In() (or the facetrack.CoverageIn it calls) ever went
// back to (last-first)/(t1-t0) — the SPAN between the outermost sightings —
// both of these would read 1.000, because both tracks' first and last
// sighting sit at the window's edges. Density is what tells them apart.
func TestIn_GlimpsedFaceScoresBelowAPresentOne(t *testing.T) {
	// Sampled every 1s across a 20s window. Glimpsed: only at 0, 10, 20 (3 of
	// 21 possible samples). Present: every second.
	glimpsed := Signals{OK: true, SamplePeriod: 1.0, Tracks: []facetrack.Track{
		track(1, 0, 10, 20),
	}}
	present := Signals{OK: true, SamplePeriod: 1.0, Tracks: []facetrack.Track{
		track(1, seq(0, 20, 1.0)...),
	}}

	g := glimpsed.In(0, 20)
	p := present.In(0, 20)

	// The SPAN-based bug would report both at 1.000. Density must not.
	if g.Coverage >= 0.99 {
		t.Fatalf("glimpsed coverage = %.3f, want well below 1.0 — this is the exact span-vs-density bug HANDOFF-SHORTS-PIPELINE.md fixed once already", g.Coverage)
	}
	// 3 samples * 1.0s period / 20s window = 0.15.
	if got, want := g.Coverage, 0.15; abs(got-want) > 1e-9 {
		t.Errorf("glimpsed coverage = %.4f, want %.4f (3 sightings * 1.0s / 20s)", got, want)
	}
	if p.Coverage < 0.99 {
		t.Errorf("present coverage = %.3f, want ~1.0 (detected at every sample)", p.Coverage)
	}
	if !(p.Coverage > g.Coverage) {
		t.Fatalf("present (%.3f) must score above glimpsed (%.3f)", p.Coverage, g.Coverage)
	}
}

func TestIn_NoTrackDataIsNotAFailure(t *testing.T) {
	for _, s := range []Signals{{}, {OK: true}, {OK: false, Tracks: []facetrack.Track{track(1, 0, 1, 2)}}} {
		w := s.In(0, 20)
		if w.Coverage != 0 || w.Basis == "" {
			t.Errorf("got coverage=%.3f basis=%q; want 0 coverage and a stated reason, not a crash", w.Coverage, w.Basis)
		}
	}
}

// A window with no sightings at all is a real, honest answer — coverage 0 —
// not an empty/zero-value Window indistinguishable from "no data".
func TestIn_NoSightingsInWindowIsZeroWithAReason(t *testing.T) {
	s := Signals{OK: true, SamplePeriod: 1.0, Tracks: []facetrack.Track{track(1, 100, 101, 102)}}
	w := s.In(0, 20)
	if w.Coverage != 0 {
		t.Errorf("coverage = %.3f, want 0 (track only sighted well outside the window)", w.Coverage)
	}
	if w.Basis != "no face detected anywhere in this window" {
		t.Errorf("basis = %q, want the explicit no-detection note", w.Basis)
	}
}

// With two tracks in frame (two people), the window scores on whichever one
// is better covered — this signal answers "is a talking head here", not "who".
func TestIn_TakesTheBestCoveredTrack(t *testing.T) {
	s := Signals{OK: true, SamplePeriod: 1.0, Tracks: []facetrack.Track{
		track(1, 0, 5, 10),           // sparse
		track(2, seq(0, 10, 1.0)...), // dense
	}}
	w := s.In(0, 10)
	if w.Coverage < 0.9 {
		t.Errorf("coverage = %.3f, want the dense track's near-1.0, not the sparse one's", w.Coverage)
	}
}

func seq(start, end, step float64) []float64 {
	var out []float64
	for t := start; t <= end+1e-9; t += step {
		out = append(out, t)
	}
	return out
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
