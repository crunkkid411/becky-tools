package ground

import "sort"

// Sample is one grounded instant reduced to the single number a horizontal
// crop needs: where the subject was, as a fraction of frame width.
type Sample struct {
	T float64 // SOURCE-absolute seconds
	X float64 // subject centre, 0..1 across the frame
}

// Samples reduces a Result to the per-frame subject positions, dropping frames
// where nothing was found and frames where the "subject" fills the frame (an
// occlusion carries no position — see OccludedFrac).
//
// The LARGEST box wins in each frame, which on a two-shot is the figure the
// shot is built around.
func Samples(res Result, maxArea float64) []Sample {
	var out []Sample
	for _, d := range res.Detections {
		b, ok := largest(d.Boxes)
		if !ok || (maxArea > 0 && b.Area() >= maxArea) {
			continue
		}
		out = append(out, Sample{T: d.T, X: b.CenterX()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].T < out[j].T })
	return out
}

// PanPath resamples sparse grounded positions into a smooth camera path at
// outFPS, over [start,end] in SOURCE seconds, returning times RELATIVE to start.
//
// WHY THIS EXISTS. Grounding samples about once a second, and ffmpeg's sendcmd
// is a STEP function — it holds a value until the next command. Feeding it the
// raw samples makes the frame jump once per second, which looks worse than not
// moving at all. So the sparse positions are linearly interpolated up to a real
// frame rate and then smoothed.
//
// It is also the answer to a refusal that should never have happened: a subject
// that crosses 46% of the frame during a span has no single static framing, and
// becky refused the whole clip over it. A subject that moves is not unframeable,
// it is a PAN. Jordan, 2026-08-20: "Lots of videos might not have perfect framing
// opportunities, but that doesn't mean to refuse the clip altogether."
//
// CUTS ARE HARD WALLS. cuts are shot boundaries inside [start,end] in SOURCE
// seconds (internal/shotcut). The path never interpolates across one.
//
// Jordan, 2026-08-20: "using some type of scene detection should also be a data
// point for clipping; as jumpcuts are already in the footage, and occasional
// multi-camera clips (such as the mouse-trap prank it couldn't edit)."
//
// That is the correction this rung needed. A subject "moving" 46% of the frame
// between two samples is ambiguous: it is either a pan, or the footage CUT to
// another camera and the subject is simply somewhere else now. Sliding the crop
// smoothly across a hard cut is the worse of the two mistakes — it drags the
// frame through an edit the viewer already accepted. Inside a shot, pan; at a
// cut, jump, exactly as the edit itself does.
//
// Pure: no I/O, no model, so the smoothing is unit-testable on its own.
func PanPath(samples []Sample, start, end, outFPS, smoothSeconds float64, cuts []float64) []Sample {
	if len(samples) == 0 || end <= start {
		return nil
	}
	// Split at cuts and smooth each shot independently, then concatenate. One
	// shot is the common case and costs an extra slice header.
	if inside := cutsInside(cuts, start, end); len(inside) > 0 {
		var out []Sample
		lo := start
		for _, c := range append(inside, end) {
			seg := samplesIn(samples, lo, c)
			if len(seg) > 0 {
				for _, p := range PanPath(seg, lo, c, outFPS, smoothSeconds, nil) {
					out = append(out, Sample{T: p.T + (lo - start), X: p.X})
				}
			}
			lo = c
		}
		return out
	}
	if outFPS <= 0 {
		outFPS = 12
	}
	if len(samples) == 1 {
		// One sighting is a position, not a movement. Hold it.
		return []Sample{{T: 0, X: samples[0].X}}
	}

	n := int((end-start)*outFPS) + 1
	if n < 2 {
		n = 2
	}
	raw := make([]Sample, 0, n)
	j := 0
	for i := 0; i < n; i++ {
		t := start + float64(i)/outFPS
		for j+1 < len(samples)-1 && samples[j+1].T < t {
			j++
		}
		a, b := samples[j], samples[j+1]
		var x float64
		switch {
		case t <= a.T:
			x = a.X
		case t >= b.T:
			x = b.X
		default:
			// Smoothstep rather than linear: a camera that starts and stops
			// abruptly at every sample reads as a machine panning, which is the
			// tell this whole pipeline exists to avoid.
			u := (t - a.T) / (b.T - a.T)
			u = u * u * (3 - 2*u)
			x = a.X + (b.X-a.X)*u
		}
		raw = append(raw, Sample{T: t - start, X: x})
	}
	return movingAverage(raw, int(smoothSeconds*outFPS))
}

// movingAverage smooths x over a centred window of w samples. w <= 1 is a no-op.
func movingAverage(in []Sample, w int) []Sample {
	if w <= 1 || len(in) < 2 {
		return in
	}
	half := w / 2
	out := make([]Sample, len(in))
	for i := range in {
		lo, hi := i-half, i+half
		if lo < 0 {
			lo = 0
		}
		if hi >= len(in) {
			hi = len(in) - 1
		}
		var sum float64
		for k := lo; k <= hi; k++ {
			sum += in[k].X
		}
		out[i] = Sample{T: in[i].T, X: sum / float64(hi-lo+1)}
	}
	return out
}

// Travel is how far the path moves in total, as a fraction of frame width.
// A path that barely travels should be rendered as a STATIC crop instead: a
// crop that drifts a few pixels reads as a wobble, not a camera move.
func Travel(path []Sample) float64 {
	lo, hi := 1.0, 0.0
	for _, s := range path {
		if s.X < lo {
			lo = s.X
		}
		if s.X > hi {
			hi = s.X
		}
	}
	if hi < lo {
		return 0
	}
	return hi - lo
}

// cutsInside returns the shot boundaries strictly inside (start,end), sorted.
func cutsInside(cuts []float64, start, end float64) []float64 {
	var in []float64
	for _, c := range cuts {
		if c > start && c < end {
			in = append(in, c)
		}
	}
	sort.Float64s(in)
	return in
}

// samplesIn returns the samples falling in [lo,hi).
func samplesIn(samples []Sample, lo, hi float64) []Sample {
	var out []Sample
	for _, s := range samples {
		if s.T >= lo && s.T < hi {
			out = append(out, s)
		}
	}
	return out
}
