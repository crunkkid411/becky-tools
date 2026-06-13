// vec.go — vector math for becky-cluster. This is the SAME deterministic cosine /
// normalize / averageNormalized that cmd/identify/vec.go uses for matching. It is
// duplicated here (not imported) because each becky tool is its own `package main`
// and Go cannot import one command's package main from another; keeping the math
// byte-for-byte identical means a cluster edge and an identify match agree on what
// "cosine 0.65" means. Inputs are expected L2-normalized (so cosine == dot product)
// with a defensive full-normalization fallback.
package main

import "math"

// cosine returns the cosine similarity of two equal-length vectors. Mismatched or
// empty vectors yield 0. Falls back to full normalization if a magnitude is not
// unit (defensive; embeddings should already be normalized).
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
// compute a cluster centroid (and to fuse enrolled KB prints for the cross-check).
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
