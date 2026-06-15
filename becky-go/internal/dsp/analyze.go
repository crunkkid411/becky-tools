package dsp

import "math"

// analyze.go is the STFT front-end ported from dawbase's detect(): a Hann-windowed
// short-time Fourier transform over the mono samples that accumulates a 12-bin
// chroma (pitch-class energy) for key detection and a per-frame spectral-flux value
// (positive magnitude differences) that becomes the onset envelope for tempo. The
// becky-hum pipeline already owns the Krumhansl key math (keyfind.go) and the
// onset-autocorrelation tempo math (tempo.go); this file's job is only to turn
// samples into the chroma + onset signals those stages consume.

const (
	frameSize = 2048   // STFT window length (dawbase: 2048)
	hopSize   = 512    // hop between frames (dawbase: 512)
	minHz     = 55.0   // chroma low cutoff (dawbase vocal-ish range)
	maxHz     = 5000.0 // chroma high cutoff
)

// Features is the low-level audio analysis the hum front-end needs: a normalized
// 12-bin chroma (index 0 = C), the spectral-flux onset envelope (one value per
// STFT frame), and the frame rate (frames per second) so onset-envelope indices can
// be turned into onset times in seconds. DurationSec echoes the clip length.
type Features struct {
	Chroma       [12]float64
	OnsetEnv     []float64
	FramesPerSec float64
	DurationSec  float64
}

// Analyze runs the windowed STFT over mono samples at sr Hz and returns the chroma
// + onset envelope. Empty/too-short audio yields zeroed features (not a panic) so a
// silent take degrades cleanly upstream.
func Analyze(samples []float64, sr int) Features {
	feat := Features{}
	if len(samples) < frameSize || sr <= 0 {
		return feat
	}
	feat.DurationSec = float64(len(samples)) / float64(sr)
	feat.FramesPerSec = float64(sr) / float64(hopSize)

	fftN := nextPow2(frameSize)
	win := hannWindow(frameSize)
	prevMag := make([]float64, fftN/2)
	buf := make([]Complex, fftN)

	for start := 0; start+frameSize <= len(samples); start += hopSize {
		loadFrame(buf, samples[start:start+frameSize], win)
		flux := accumulateFrame(buf, prevMag, sr, fftN, &feat.Chroma)
		feat.OnsetEnv = append(feat.OnsetEnv, flux)
	}
	normalizeChroma(&feat.Chroma)
	return feat
}

// loadFrame copies a windowed frame into the complex FFT buffer, zero-padding the
// tail to fftN (buf is reused across frames, so the tail is cleared each time).
func loadFrame(buf []Complex, frame, win []float64) {
	for i := range buf {
		buf[i] = Complex{}
	}
	for i := 0; i < len(frame); i++ {
		buf[i] = Complex{Re: frame[i] * win[i]}
	}
}

// accumulateFrame FFTs one frame, accumulates chroma over the in-band bins, and
// returns this frame's spectral flux (sum of positive magnitude increases vs. the
// previous frame). prevMag is updated in place for the next call.
func accumulateFrame(buf []Complex, prevMag []float64, sr, fftN int, chroma *[12]float64) float64 {
	FFT(buf)
	var flux float64
	for k := 1; k < fftN/2; k++ {
		mag := math.Hypot(buf[k].Re, buf[k].Im)
		if d := mag - prevMag[k]; d > 0 {
			flux += d
		}
		prevMag[k] = mag
		freq := float64(k) * float64(sr) / float64(fftN)
		if freq < minHz || freq > maxHz {
			continue
		}
		midi := int(math.Round(69 + 12*math.Log2(freq/440.0)))
		if midi < 0 {
			continue
		}
		chroma[((midi%12)+12)%12] += mag
	}
	return flux
}

// normalizeChroma scales the chroma so its largest bin is 1 (matching dawbase),
// leaving an all-zero vector untouched.
func normalizeChroma(chroma *[12]float64) {
	maxc := 0.0
	for _, c := range chroma {
		if c > maxc {
			maxc = c
		}
	}
	if maxc <= 0 {
		return
	}
	for i := range chroma {
		chroma[i] /= maxc
	}
}

// OnsetTimes converts the onset envelope into onset times (seconds) by half-wave
// rectifying around the mean and picking local peaks above an adaptive threshold.
// These feed becky-hum's EstimateTempo (onset-autocorrelation). A flat/empty
// envelope yields no onsets (tempo then degrades to its default).
func OnsetTimes(env []float64, framesPerSec float64) []float64 {
	if len(env) < 3 || framesPerSec <= 0 {
		return nil
	}
	mean, peak := meanPeak(env)
	if peak <= 0 {
		return nil
	}
	thresh := mean + 0.3*(peak-mean) // adaptive: 30% of the way from mean to peak
	var onsets []float64
	for i := 1; i < len(env)-1; i++ {
		if env[i] >= thresh && env[i] >= env[i-1] && env[i] > env[i+1] {
			onsets = append(onsets, float64(i)/framesPerSec)
		}
	}
	return onsets
}

// meanPeak returns the mean and maximum of env in one pass.
func meanPeak(env []float64) (mean, peak float64) {
	var sum float64
	for _, v := range env {
		sum += v
		if v > peak {
			peak = v
		}
	}
	return sum / float64(len(env)), peak
}
