// vec.go — vector math for voice identification. Cosine similarity is done here,
// in Go (the Python helper only turns a wav into a 192-float vector). Keeping the
// math in one place keeps matching deterministic and testable.
package main

import "math"

// cosine returns the cosine similarity of two equal-length vectors. Inputs are
// expected to be L2-normalized (see normalize / averageNormalized), so this is a
// dot product; it falls back to full normalization defensively if a magnitude is
// not unit. Mismatched/empty vectors yield 0.
func cosine(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// normalize returns an L2-normalized copy of v (immutable: v is not mutated).
func normalize(v []float64) []float64 {
	var sum float64
	for _, x := range v {
		sum += x * x
	}
	out := make([]float64, len(v))
	if sum == 0 {
		copy(out, v)
		return out
	}
	inv := 1.0 / math.Sqrt(sum)
	for i, x := range v {
		out[i] = x * inv
	}
	return out
}

// averageNormalized averages several vectors then L2-normalizes the mean. Used to
// fuse multiple enrolled clips per name into one robust voice-print embedding.
func averageNormalized(vectors [][]float64) []float64 {
	if len(vectors) == 0 {
		return nil
	}
	dim := len(vectors[0])
	mean := make([]float64, dim)
	for _, v := range vectors {
		if len(v) != dim {
			continue // skip malformed vectors rather than corrupt the mean
		}
		for i, x := range v {
			mean[i] += x
		}
	}
	n := float64(len(vectors))
	for i := range mean {
		mean[i] /= n
	}
	return normalize(mean)
}
