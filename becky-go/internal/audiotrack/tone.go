package audiotrack

// tone.go provides a deterministic sine-tone Clip generator. It is NOT fabricated
// "recorded" audio (that would violate the honesty mandate — see record.go); it is an
// explicit synthesized test signal, the audio analog of a color-bar test pattern. It
// exists so the verification tool and unit tests can produce a real, non-silent WAV
// to import / mix / peak WITHOUT depending on an external sample file. The GUI may
// also use it for an audible "click" placeholder.

import "math"

// SortedRegions returns the track's regions ordered by TimelinePos (then by ID for a
// stable tie-break), as a fresh slice — for the UI to draw left-to-right. The track's
// own Regions slice is left untouched (immutability).
func (t Track) SortedRegions() []Region {
	out := cloneRegions(t.Regions)
	// Simple insertion sort keeps it dependency-free and stable.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0; j-- {
			a, b := out[j-1], out[j]
			if a.TimelinePos < b.TimelinePos || (a.TimelinePos == b.TimelinePos && a.ID <= b.ID) {
				break
			}
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// ToneClip synthesizes a mono sine Clip of `frames` frames at `sampleRate` Hz, at the
// given frequency (Hz) and linear amplitude (0..1). It is deterministic (same args ->
// identical samples). amplitude is clamped to [0,1]; freq<=0 or frames<=0 yields an
// empty (silent) clip. The Path is set to a descriptive synthetic label, not a real
// file, so callers can tell it apart from an imported clip.
func ToneClip(freqHz float64, amplitude float64, frames, sampleRate int) *Clip {
	if sampleRate <= 0 {
		sampleRate = DefaultSampleRate
	}
	if amplitude < 0 {
		amplitude = 0
	}
	if amplitude > 1 {
		amplitude = 1
	}
	if frames < 0 {
		frames = 0
	}
	samples := make([]float32, frames)
	if freqHz > 0 {
		step := 2 * math.Pi * freqHz / float64(sampleRate)
		for i := 0; i < frames; i++ {
			samples[i] = float32(amplitude * math.Sin(step*float64(i)))
		}
	}
	return &Clip{
		Path:       "synthetic:tone",
		Channels:   1,
		SampleRate: sampleRate,
		Samples:    samples,
	}
}
