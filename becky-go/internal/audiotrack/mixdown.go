package audiotrack

// mixdown.go is the OFFLINE renderer: it sums every track's regions onto a single
// stereo timeline, applying per-region gain + fade envelope and per-track
// volume/pan/mute/solo, and returns interleaved stereo float32 (and, via MixdownWAV,
// a real .wav file). It is the deterministic bounce — same Project in, same samples
// out — that proves a vocal/audio edit is real (the cmd tool corroborates the output
// with ffprobe volumedetect).
//
// Signal flow per output frame:
//   region sample  *= region.gainAt(localFrame)        (region gain + fades)
//                  *= track.Volume                       (channel fader)
//   then split to L/R via a constant-power pan law       (track pan)
//   and summed into the stereo bus.
//
// No limiter/normalization is applied by default (the bounce is a faithful sum);
// HardClip is available for callers that want to guarantee no out-of-range samples
// before a 16-bit write. Pure Go, stdlib only.

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
)

// Mixdown renders the whole project to interleaved STEREO float32 (L,R,L,R,...). The
// length is project length (LenFrames) * 2 samples. Solo semantics: if ANY track has
// Solo set, only soloed (and non-muted) tracks are rendered; otherwise all non-muted
// tracks render. A project with no audible content returns a zero-length buffer.
func (p Project) Mixdown() []float32 {
	frames := p.LenFrames()
	if frames <= 0 {
		return nil
	}
	out := make([]float32, frames*2) // stereo

	anySolo := false
	for _, t := range p.Tracks {
		if t.Solo {
			anySolo = true
			break
		}
	}

	for _, t := range p.Tracks {
		if t.Mute {
			continue
		}
		if anySolo && !t.Solo {
			continue
		}
		lGain, rGain := panGains(t.Pan)
		vol := t.Volume
		if vol < 0 {
			vol = 0
		}
		lGain *= vol
		rGain *= vol
		for _, r := range t.Regions {
			mixRegion(out, frames, r, float32(lGain), float32(rGain))
		}
	}
	return out
}

// mixRegion adds one region's contribution into the stereo bus `out` (length
// frames*2). lGain/rGain are the already-combined track volume*pan multipliers for
// the left/right buses. The region's own gain+fade envelope is applied per frame. A
// nil-clip or zero-length region contributes nothing.
func mixRegion(out []float32, frames int, r Region, lGain, rGain float32) {
	if r.Clip == nil {
		return
	}
	r = r.Normalize()
	ch := r.Clip.Channels
	if ch <= 0 {
		return
	}
	n := r.LenFrames()
	src := r.Clip.Samples
	for i := 0; i < n; i++ {
		tlFrame := r.TimelinePos + i
		if tlFrame < 0 || tlFrame >= frames {
			continue // clipped to the project window
		}
		// Mono-collapse this source frame (stereo region summed to mono, then panned
		// by the TRACK — region material is treated as a mono source for the bus).
		srcFrame := r.SourceIn + i
		var sum float32
		base := srcFrame * ch
		for c := 0; c < ch; c++ {
			idx := base + c
			if idx >= 0 && idx < len(src) {
				sum += src[idx]
			}
		}
		mono := (sum / float32(ch)) * float32(r.gainAt(i))
		out[tlFrame*2] += mono * lGain
		out[tlFrame*2+1] += mono * rGain
	}
}

// panGains returns the left/right constant-power pan multipliers for pan in [-1, 1]
// (-1 hard left, 0 center, +1 hard right). Constant-power (sin/cos) is the standard
// DAW pan law: center is -3 dB on each side, so a panned mono source keeps roughly
// constant perceived loudness across the field. Pan is clamped to [-1, 1].
func panGains(pan float64) (left, right float64) {
	pan = clampF(pan, -1, 1)
	// Map [-1,1] -> angle [0, pi/2]: -1 -> 0 (all left), +1 -> pi/2 (all right).
	theta := (pan + 1) * (math.Pi / 4)
	return math.Cos(theta), math.Sin(theta)
}

// HardClip returns a copy of an interleaved buffer with every sample clamped to
// [-1, 1]. The mixdown does not clip by default (a faithful sum can exceed unity when
// many loud regions overlap); call this before a 16-bit WAV write if you must
// guarantee no wrap/overflow. It is a separate, explicit step so the default bounce
// stays a faithful sum.
func HardClip(buf []float32) []float32 {
	out := make([]float32, len(buf))
	for i, v := range buf {
		if v > 1 {
			v = 1
		} else if v < -1 {
			v = -1
		}
		out[i] = v
	}
	return out
}

// PeakAbs returns the maximum absolute sample value in an interleaved buffer (0 for an
// empty buffer). Used to report headroom / detect clipping risk before a 16-bit write.
func PeakAbs(buf []float32) float32 {
	var peak float32
	for _, v := range buf {
		if v < 0 {
			v = -v
		}
		if v > peak {
			peak = v
		}
	}
	return peak
}

// MixdownWAV renders the project and writes the result to a 16-bit PCM stereo WAV at
// path. Samples are hard-clipped to [-1, 1] before the 16-bit conversion so the file
// never wraps. An empty project (no audible content) is written as a valid, silent
// (zero-length-data) WAV rather than an error, so a caller always gets a real file.
// Returns a wrapped error on any write failure (degrade-never-crash).
func (p Project) MixdownWAV(path string) error {
	buf := HardClip(p.Mixdown())
	sr := p.SampleRate
	if sr <= 0 {
		sr = DefaultSampleRate
	}
	return WritePCM16WAV(path, buf, sr, 2)
}

// WritePCM16WAV writes interleaved float32 samples (in [-1, 1]) to a 16-bit PCM WAV at
// path with the given sample rate and channel count. This is the canonical mixdown
// output format: 16-bit PCM is universally readable (ffprobe, every DAW, players) and
// keeps the bounce deterministic. Out-of-range samples are clamped during conversion.
// channels<=0 or sampleRate<=0 are corrected to stereo / DefaultSampleRate.
//
// Layout (all little-endian): RIFF/WAVE header, a 16-byte "fmt " chunk (PCM tag 1),
// then a "data" chunk of int16 LE samples.
func WritePCM16WAV(path string, samples []float32, sampleRate, channels int) error {
	if channels <= 0 {
		channels = 2
	}
	if sampleRate <= 0 {
		sampleRate = DefaultSampleRate
	}
	const bitsPerSample = 16
	blockAlign := channels * (bitsPerSample / 8)
	byteRate := sampleRate * blockAlign
	dataSize := len(samples) * (bitsPerSample / 8)
	riffSize := 4 + (8 + 16) + (8 + dataSize) // "WAVE" + fmt chunk + data chunk

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("audiotrack: create %q: %w", path, err)
	}
	defer f.Close()

	le := binary.LittleEndian
	hdr := make([]byte, 0, 44)
	hdr = append(hdr, "RIFF"...)
	hdr = le.AppendUint32(hdr, uint32(riffSize))
	hdr = append(hdr, "WAVE"...)
	hdr = append(hdr, "fmt "...)
	hdr = le.AppendUint32(hdr, 16) // PCM fmt chunk body size
	hdr = le.AppendUint16(hdr, 1)  // wFormatTag = PCM
	hdr = le.AppendUint16(hdr, uint16(channels))
	hdr = le.AppendUint32(hdr, uint32(sampleRate))
	hdr = le.AppendUint32(hdr, uint32(byteRate))
	hdr = le.AppendUint16(hdr, uint16(blockAlign))
	hdr = le.AppendUint16(hdr, bitsPerSample)
	hdr = append(hdr, "data"...)
	hdr = le.AppendUint32(hdr, uint32(dataSize))
	if _, err := f.Write(hdr); err != nil {
		return fmt.Errorf("audiotrack: write %q header: %w", path, err)
	}

	// Sample data: float32 [-1,1] -> int16 LE, in one buffer for a single write.
	data := make([]byte, dataSize)
	for i, s := range samples {
		if s > 1 {
			s = 1
		} else if s < -1 {
			s = -1
		}
		v := int16(math.Round(float64(s) * 32767.0))
		data[i*2] = byte(uint16(v))
		data[i*2+1] = byte(uint16(v) >> 8)
	}
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("audiotrack: write %q data: %w", path, err)
	}
	return nil
}
