package hum

import "testing"

// evenOnsets makes n onsets spaced exactly periodSec apart starting at 0.
func evenOnsets(n int, periodSec float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = float64(i) * periodSec
	}
	return out
}

func TestEstimateTempo_120BPM(t *testing.T) {
	// Quarter notes at 120 BPM = 0.5 s apart.
	got := EstimateTempo(evenOnsets(8, 0.5), TempoOptions{})
	if got.BPM != 120 {
		t.Fatalf("0.5s onsets -> %d BPM, want 120", got.BPM)
	}
}

func TestEstimateTempo_HalfNoteResolvesToNearest120(t *testing.T) {
	// Onsets at 1.0 s apart = 60 BPM, but the double (120) is nearer the center,
	// so the octave rule prefers 120 when no genre window constrains it.
	got := EstimateTempo(evenOnsets(8, 1.0), TempoOptions{})
	if got.BPM != 120 {
		t.Fatalf("1.0s onsets resolved to %d BPM (resolvedBy %s), want 120", got.BPM, got.ResolvedBy)
	}
}

func TestEstimateTempo_GenreWindowResolvesOctave(t *testing.T) {
	// Onsets at 0.8 s = 75 BPM. A 140-160 genre window should prefer the double (150).
	got := EstimateTempo(evenOnsets(8, 0.8), TempoOptions{GenreLo: 140, GenreHi: 160})
	if got.BPM != 150 {
		t.Fatalf("0.8s onsets in 140-160 window -> %d BPM (%s), want 150", got.BPM, got.ResolvedBy)
	}
	if got.ResolvedBy != "genre-window" {
		t.Errorf("resolvedBy = %q, want genre-window", got.ResolvedBy)
	}
}

func TestEstimateTempo_TooFewOnsetsDegrades(t *testing.T) {
	got := EstimateTempo([]float64{0.5}, TempoOptions{})
	if got.BPM != preferCenter || got.Confidence != 0 {
		t.Errorf("single onset should degrade to %d @ 0 confidence, got %+v", preferCenter, got)
	}
}

func TestEstimateTempo_Deterministic(t *testing.T) {
	o := evenOnsets(10, 0.4)
	a := EstimateTempo(o, TempoOptions{})
	b := EstimateTempo(o, TempoOptions{})
	if a.BPM != b.BPM || a.Confidence != b.Confidence || a.ResolvedBy != b.ResolvedBy {
		t.Error("EstimateTempo not deterministic")
	}
}

func TestEstimateTempo_AltOctaves(t *testing.T) {
	got := EstimateTempo(evenOnsets(8, 0.5), TempoOptions{})
	if len(got.Alt) == 0 {
		t.Error("expected octave alternatives reported in Alt")
	}
}
