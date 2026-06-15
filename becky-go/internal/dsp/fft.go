// Package dsp is becky's pure-Go, zero-dependency, deterministic audio-analysis
// front-end. It decodes a WAV into mono float samples and extracts the low-level
// features the becky-hum pipeline consumes: a 12-bin chroma (pitch-class) vector
// for key detection and a spectral-flux onset envelope (plus an autocorrelation
// tempo estimate) for tempo detection.
//
// The math mirrors the sibling MIT-licensed dawbase analysis.cpp (radix-2
// Cooley-Tukey FFT, Hann-windowed STFT, chroma accumulation, spectral flux,
// onset-autocorrelation tempo) re-implemented idiomatically in Go. No cgo, no
// third-party libs, stdlib only. Same samples in => same features out (fixed
// window, fixed hop, deterministic loops) so becky-hum stays reproducible.
package dsp

import "math"

const twoPi = 2 * math.Pi

// Complex is a minimal complex value used by the in-place FFT. We use our own
// struct (not the stdlib complex128) so the radix-2 butterfly mirrors dawbase's
// std::complex arithmetic explicitly and stays allocation-free in the hot loop.
type Complex struct {
	Re, Im float64
}

// FFT runs an in-place iterative radix-2 Cooley-Tukey FFT on a, whose length MUST
// be a power of two. A non-power-of-two (or length <= 1) input is returned
// unchanged — degrade-never-crash; callers zero-pad to the next power of two.
func FFT(a []Complex) {
	n := len(a)
	if n <= 1 || n&(n-1) != 0 {
		return
	}
	bitReverse(a)
	for length := 2; length <= n; length <<= 1 {
		ang := -twoPi / float64(length)
		wlen := Complex{Re: math.Cos(ang), Im: math.Sin(ang)}
		for i := 0; i < n; i += length {
			w := Complex{Re: 1, Im: 0}
			half := length >> 1
			for k := 0; k < half; k++ {
				u := a[i+k]
				v := mul(a[i+k+half], w)
				a[i+k] = Complex{Re: u.Re + v.Re, Im: u.Im + v.Im}
				a[i+k+half] = Complex{Re: u.Re - v.Re, Im: u.Im - v.Im}
				w = mul(w, wlen)
			}
		}
	}
}

// bitReverse permutes a into bit-reversed index order (the standard radix-2
// pre-pass), matching dawbase's loop exactly.
func bitReverse(a []Complex) {
	n := len(a)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			a[i], a[j] = a[j], a[i]
		}
	}
}

// Magnitudes returns the magnitude spectrum |X[k]| for the first len(a)/2+1 bins
// (the non-redundant half of a real-input transform). The input is FFT'd in place.
func Magnitudes(a []Complex) []float64 {
	FFT(a)
	half := len(a)/2 + 1
	out := make([]float64, half)
	for k := 0; k < half; k++ {
		out[k] = math.Hypot(a[k].Re, a[k].Im)
	}
	return out
}

// mul multiplies two Complex values.
func mul(a, b Complex) Complex {
	return Complex{Re: a.Re*b.Re - a.Im*b.Im, Im: a.Re*b.Im + a.Im*b.Re}
}

// nextPow2 returns the smallest power of two >= n (>=1).
func nextPow2(n int) int {
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}

// hannWindow returns an n-point symmetric Hann window (matches dawbase: 0.5*(1 -
// cos(2*pi*i/(n-1)))). n<=1 yields all-ones so a degenerate frame is a no-op.
func hannWindow(n int) []float64 {
	w := make([]float64, n)
	if n <= 1 {
		for i := range w {
			w[i] = 1
		}
		return w
	}
	for i := 0; i < n; i++ {
		w[i] = 0.5 * (1 - math.Cos(twoPi*float64(i)/float64(n-1)))
	}
	return w
}
