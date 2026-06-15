package dsp

import "math"

// pitch.go derives a monophonic per-frame pitch contour from the STFT — the FLOOR
// f0 track becky-hum segments into notes. This is NOT a high-accuracy pitch tracker:
// it picks the strongest in-band spectral peak per frame and refines it with
// parabolic interpolation. It is good enough to recover a clear hummed/sung melody
// and to give the existing segmenter a real contour; precise f0 (pYIN/basic-pitch
// for scoops, vibrato, low SNR) remains the model-helper boundary documented in
// internal/hum (Extractor). Same samples => same contour (deterministic).

// PitchFrame is one frame of the contour: time (seconds), estimated fundamental
// (Hz, 0 when no confident pitch), and a 0..1 voicing-ish strength.
type PitchFrame struct {
	T      float64
	F0     float64
	Voiced float64
}

// PitchContour runs the same Hann-windowed STFT as Analyze and, per frame, returns
// the dominant in-band frequency as the f0 estimate plus a normalized strength.
// Too-short/empty audio yields an empty contour (never a panic).
func PitchContour(samples []float64, sr int) []PitchFrame {
	if len(samples) < frameSize || sr <= 0 {
		return nil
	}
	fftN := nextPow2(frameSize)
	win := hannWindow(frameSize)
	buf := make([]Complex, fftN)
	secsPerHop := float64(hopSize) / float64(sr)
	globalMax := 0.0
	var frames []PitchFrame

	for start := 0; start+frameSize <= len(samples); start += hopSize {
		loadFrame(buf, samples[start:start+frameSize], win)
		mags := Magnitudes(buf)
		f0, strength := dominantPitch(mags, sr, fftN)
		if strength > globalMax {
			globalMax = strength
		}
		frames = append(frames, PitchFrame{
			T:      float64(len(frames)) * secsPerHop,
			F0:     f0,
			Voiced: strength,
		})
	}
	normalizeVoicing(frames, globalMax)
	return frames
}

// dominantPitch finds the strongest magnitude bin in [minHz, maxHz] and returns its
// parabolically-interpolated frequency and raw magnitude strength. No in-band peak
// => (0, 0).
func dominantPitch(mags []float64, sr, fftN int) (f0, strength float64) {
	loBin := int(math.Ceil(minHz * float64(fftN) / float64(sr)))
	hiBin := int(math.Floor(maxHz * float64(fftN) / float64(sr)))
	if loBin < 1 {
		loBin = 1
	}
	if hiBin >= len(mags)-1 {
		hiBin = len(mags) - 2
	}
	bestK, bestMag := -1, 0.0
	for k := loBin; k <= hiBin; k++ {
		if mags[k] > bestMag {
			bestMag, bestK = mags[k], k
		}
	}
	if bestK < 1 || bestMag <= 0 {
		return 0, 0
	}
	refined := float64(bestK) + parabolicOffset(mags[bestK-1], mags[bestK], mags[bestK+1])
	return refined * float64(sr) / float64(fftN), bestMag
}

// parabolicOffset returns the sub-bin peak offset (-0.5..0.5) from a 3-point
// parabolic fit around a magnitude peak. A flat/degenerate triple => 0.
func parabolicOffset(left, center, right float64) float64 {
	denom := left - 2*center + right
	if denom == 0 {
		return 0
	}
	off := 0.5 * (left - right) / denom
	if off < -0.5 {
		off = -0.5
	}
	if off > 0.5 {
		off = 0.5
	}
	return off
}

// normalizeVoicing scales each frame's raw magnitude strength to 0..1 by the clip's
// peak strength, so the segmenter's voicing gate is on a stable scale.
func normalizeVoicing(frames []PitchFrame, globalMax float64) {
	if globalMax <= 0 {
		return
	}
	for i := range frames {
		frames[i].Voiced = frames[i].Voiced / globalMax
	}
}
