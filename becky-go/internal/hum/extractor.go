package hum

import (
	"fmt"
	"os"

	"becky-go/internal/dsp"
)

// extractor.go is the REAL audio→features front-end for becky-hum, replacing the
// stub path. It decodes a WAV with the pure-Go internal/dsp package, runs the ported
// dawbase STFT to get a monophonic pitch contour (the deterministic FLOOR) plus a
// spectral-flux onset envelope, and returns the EXISTING Features struct the rest of
// the hum pipeline already consumes — chroma/key via keyfind.go, onsets/tempo via
// tempo.go, contour→notes via segment.go. No Python, no model, no ffmpeg, offline.
//
// FLOOR vs. MODEL BOUNDARY: the contour here is a strongest-spectral-peak f0 track —
// enough to recover a clear hummed melody, detect the key, and estimate tempo. It is
// NOT high-accuracy f0: scoops, vibrato, polyphony, and low-SNR takes still want
// pYIN/basic-pitch, which stays the model-helper boundary (FixtureExtractor /
// --features and the documented pitch pyhelper). DSPExtractor is the offline default;
// the precise path remains opt-in.

// peakLanePoints caps the downsampled waveform-envelope length so a long take
// doesn't bloat the visual lane (deterministic stride).
const peakLanePoints = 600

// DSPExtractor implements Extractor with the pure-Go DSP front-end (no model).
type DSPExtractor struct{}

// Extract decodes wavPath and returns the deterministic FLOOR features. A missing
// file is a real I/O error; an undecodable/silent take returns Skipped=true features
// (degrade-never-crash), not an error. engine/device are echoed for traceability.
func (DSPExtractor) Extract(wavPath, engine, device string) (Features, error) {
	if wavPath == "" {
		return Features{}, fmt.Errorf("dsp extract: empty wav path")
	}
	raw, err := os.ReadFile(wavPath)
	if err != nil {
		return Features{}, fmt.Errorf("dsp extract: read %s: %w", wavPath, err)
	}
	audio, err := dsp.DecodeWAV(raw)
	if err != nil {
		// A malformed/unsupported WAV is a degrade, not a crash: report a partial,
		// skipped result so the CLI exits cleanly and tells Jordan what happened.
		return Features{
			Engine:  engineLabel(engine),
			Device:  device,
			Skipped: true,
			Reason:  fmt.Sprintf("could not decode %s: %v", wavPath, err),
		}, nil
	}
	return featuresFromAudio(audio, engine, device), nil
}

// featuresFromAudio runs the DSP front-end over decoded mono samples and packs the
// result into the existing Features struct. Silent/too-short audio degrades cleanly.
func featuresFromAudio(audio dsp.Audio, engine, device string) Features {
	contour := dsp.PitchContour(audio.Samples, audio.SampleRate)
	analysis := dsp.Analyze(audio.Samples, audio.SampleRate)
	onsets := dsp.OnsetTimes(analysis.OnsetEnv, analysis.FramesPerSec)

	feat := Features{
		Engine:      engineLabel(engine),
		Version:     "dsp-1",
		Frames:      framesFrom(contour),
		Onsets:      onsets,
		DurationSec: audio.DurationSec(),
		Device:      device,
		Peaks:       waveformPeaks(audio.Samples, peakLanePoints),
	}
	if len(feat.Frames) == 0 {
		feat.Skipped = true
		feat.Reason = "audio too short or silent for analysis"
	}
	return feat
}

// framesFrom converts the DSP pitch contour into the hum Frame contour the segmenter
// consumes (the same per-frame {t, f0, voiced} shape the model stub would emit).
func framesFrom(contour []dsp.PitchFrame) []Frame {
	out := make([]Frame, len(contour))
	for i, p := range contour {
		out[i] = Frame{T: round4(p.T), F0: round2(p.F0), Voiced: round2(p.Voiced)}
	}
	return out
}

// waveformPeaks downsamples the absolute-amplitude envelope to at most maxPoints
// (the visual waveform lane). Deterministic block-max stride; empty input => nil.
func waveformPeaks(samples []float64, maxPoints int) []float64 {
	if len(samples) == 0 || maxPoints <= 0 {
		return nil
	}
	block := (len(samples) + maxPoints - 1) / maxPoints
	if block < 1 {
		block = 1
	}
	var peaks []float64
	for i := 0; i < len(samples); i += block {
		end := i + block
		if end > len(samples) {
			end = len(samples)
		}
		peaks = append(peaks, round4(blockPeak(samples[i:end])))
	}
	return peaks
}

// blockPeak returns the largest absolute sample in a block.
func blockPeak(block []float64) float64 {
	peak := 0.0
	for _, s := range block {
		if a := absF(s); a > peak {
			peak = a
		}
	}
	return peak
}

func absF(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// engineLabel keeps the requested engine label when given, else marks the offline
// DSP floor so the report is honest about which path ran.
func engineLabel(engine string) string {
	if engine != "" {
		return engine
	}
	return "dsp-floor"
}
