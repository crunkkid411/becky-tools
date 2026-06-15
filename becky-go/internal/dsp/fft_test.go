package dsp

import (
	"math"
	"testing"
)

// makeComplex builds a Complex slice from real samples (imag = 0).
func makeComplex(real []float64) []Complex {
	out := make([]Complex, len(real))
	for i, r := range real {
		out[i] = Complex{Re: r}
	}
	return out
}

func TestFFT_DCSignal(t *testing.T) {
	// A constant (DC) signal puts ALL energy in bin 0; every other bin is ~0.
	a := makeComplex([]float64{1, 1, 1, 1, 1, 1, 1, 1})
	mags := Magnitudes(a)
	if math.Abs(mags[0]-8) > 1e-9 {
		t.Errorf("DC bin = %v, want 8", mags[0])
	}
	for k := 1; k < len(mags); k++ {
		if mags[k] > 1e-9 {
			t.Errorf("bin %d = %v, want ~0 for a DC signal", k, mags[k])
		}
	}
}

func TestFFT_SingleSinusoidBin(t *testing.T) {
	// A pure sinusoid at exactly k cycles over N samples peaks in bins k and N-k.
	const n = 16
	const k = 3
	real := make([]float64, n)
	for i := 0; i < n; i++ {
		real[i] = math.Cos(2 * math.Pi * k * float64(i) / n)
	}
	mags := Magnitudes(makeComplex(real))
	peak := peakBin(mags)
	if peak != k {
		t.Errorf("peak bin = %d, want %d", peak, k)
	}
	if math.Abs(mags[k]-float64(n)/2) > 1e-6 {
		t.Errorf("peak magnitude = %v, want %v", mags[k], float64(n)/2)
	}
}

func TestFFT_NonPowerOfTwoIsNoOp(t *testing.T) {
	// Degrade-never-crash: a non-power-of-two length is returned unchanged.
	a := makeComplex([]float64{1, 2, 3})
	cp := append([]Complex(nil), a...)
	FFT(a)
	for i := range a {
		if a[i] != cp[i] {
			t.Fatalf("non-pow2 FFT mutated input at %d", i)
		}
	}
}

func TestNextPow2(t *testing.T) {
	cases := []struct{ in, want int }{
		{1, 1}, {2, 2}, {3, 4}, {5, 8}, {1024, 1024}, {1025, 2048}, {2048, 2048},
	}
	for _, c := range cases {
		if got := nextPow2(c.in); got != c.want {
			t.Errorf("nextPow2(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// peakBin returns the index of the largest magnitude.
func peakBin(mags []float64) int {
	best, bi := 0.0, 0
	for i, m := range mags {
		if m > best {
			best, bi = m, i
		}
	}
	return bi
}
