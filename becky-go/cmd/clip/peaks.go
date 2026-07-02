package main

// peaks.go computes a per-clip audio waveform for the timeline: a small array
// of normalized 0..1 amplitude samples across a [in,out) window, decoded from
// ONLY that window (never the whole source) so a long source with a short
// clip on the timeline stays cheap. The decode (ffmpeg -> raw PCM) and the
// reduction (PCM -> buckets) are split so the reduction is a PURE,
// unit-tested function with no ffmpeg dependency.

import (
	"encoding/binary"
	"fmt"
	"os/exec"

	"becky-go/internal/proc"
)

// defaultPeakBuckets / maxPeakBuckets bound the peaks verb's bucket count:
// <=0 falls back to a sensible resolution for a timeline waveform lane; the
// cap keeps a pathological request from returning a runaway payload.
const (
	defaultPeakBuckets = 200
	maxPeakBuckets     = 2000
)

// peaksSampleRate is the mono PCM decode rate: plenty for a waveform's
// amplitude envelope (we only need the ENVELOPE, not fidelity) and far
// cheaper to decode/parse than the source's native rate.
const peaksSampleRate = 8000

// PeaksResult is the reply for the peaks verb: normalized 0..1 amplitude
// samples across a [in,out) window, for drawing a per-clip waveform. Peaks is
// always a (possibly empty) array — never null — so the UI's renderer never
// needs a null check. Degrades to {peaks:[],count:0} (no error) when ffmpeg is
// unavailable, the window has no audio, or the source isn't in the open
// folder — the waveform lane just stays blank.
type PeaksResult struct {
	Peaks []float64 `json:"peaks"`
	Count int       `json:"count"`
}

// emptyPeaks is the shared degrade reply: an empty (never null) peaks array.
func emptyPeaks() PeaksResult {
	return PeaksResult{Peaks: []float64{}, Count: 0}
}

// Peaks decodes ONLY the [inSec,outSec) window of source's audio to mono PCM
// and reduces it to `buckets` normalized 0..1 amplitude samples (a per-clip
// waveform). The source must be an indexed video in the open folder (path
// security, same as Thumb/ProxyFor) and is opened READ-ONLY — only ffmpeg's
// stdout is read, nothing is written to disk. Results are cached in-memory per
// (source,in,out,buckets) so re-rendering the same clip's waveform (zoom,
// reorder) decodes nothing twice. Degrade-never-crash: an unresolved source, a
// missing ffmpeg, or a window with no audio all yield {peaks:[],count:0}.
func (a *App) Peaks(source string, inSec, outSec float64, buckets int) PeaksResult {
	buckets = resolvePeakBuckets(buckets)
	v, ok := a.resolveSource(source)
	if !ok {
		return emptyPeaks()
	}
	if outSec < inSec {
		inSec, outSec = outSec, inSec
	}
	inSec = clampNonNeg(inSec)

	key := peaksCacheKey(v.Path, inSec, outSec, buckets)
	a.mu.Lock()
	if cached, ok := a.peaksCache[key]; ok {
		a.mu.Unlock()
		return cached
	}
	ffmpeg := a.cfg.FFmpeg
	a.mu.Unlock()
	if ffmpeg == "" {
		return emptyPeaks()
	}

	samples, err := decodePCMWindow(ffmpeg, v.Path, inSec, outSec)
	if err != nil || len(samples) == 0 {
		return emptyPeaks()
	}
	res := PeaksResult{Peaks: bucketPeaks(samples, buckets), Count: buckets}

	a.mu.Lock()
	if a.peaksCache == nil {
		a.peaksCache = map[string]PeaksResult{}
	}
	a.peaksCache[key] = res
	a.mu.Unlock()
	return res
}

// resolvePeakBuckets applies the peaks verb's bucket-count contract: <=0
// becomes the 200-bucket default, and a 2000 cap guards against a runaway
// payload. PURE.
func resolvePeakBuckets(buckets int) int {
	if buckets <= 0 {
		buckets = defaultPeakBuckets
	}
	if buckets > maxPeakBuckets {
		buckets = maxPeakBuckets
	}
	return buckets
}

// peaksCacheKey builds the in-memory cache key for one (source,window,buckets)
// waveform request.
func peaksCacheKey(sourcePath string, inSec, outSec float64, buckets int) string {
	return fmt.Sprintf("%s|%.3f|%.3f|%d", sourcePath, inSec, outSec, buckets)
}

// decodePCMWindow shells ffmpeg to decode ONLY the [inSec,outSec) window of
// source's audio to raw mono 16-bit PCM at peaksSampleRate, reading the
// samples from stdout (nothing touches disk). An accurate input seek (-ss
// before -i) brackets the start; -t bounds the duration. A source with no
// audio stream, or any ffmpeg failure, surfaces as an error (the caller
// degrades). NoWindow suppresses the console flash the GUI would otherwise
// show on every clip-waveform request — same as videoCodec in internal/reel.
func decodePCMWindow(ffmpeg, source string, inSec, outSec float64) ([]int16, error) {
	dur := outSec - inSec
	if dur <= 0 {
		return nil, fmt.Errorf("empty window")
	}
	cmd := exec.Command(ffmpeg,
		"-ss", fmt.Sprintf("%.3f", inSec),
		"-i", source,
		"-t", fmt.Sprintf("%.3f", dur),
		"-vn", "-ac", "1", "-ar", fmt.Sprintf("%d", peaksSampleRate), "-f", "s16le",
		"-hide_banner", "-loglevel", "error",
		"-",
	)
	proc.NoWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg pcm decode: %w", err)
	}
	return pcmBytesToInt16(out), nil
}

// pcmBytesToInt16 reinterprets raw little-endian s16le bytes as samples. A
// trailing odd byte (should not happen with s16le output) is dropped.
func pcmBytesToInt16(b []byte) []int16 {
	n := len(b) / 2
	out := make([]int16, n)
	for i := 0; i < n; i++ {
		out[i] = int16(binary.LittleEndian.Uint16(b[i*2 : i*2+2]))
	}
	return out
}

// bucketPeaks reduces samples into `buckets` values, each the max(abs) sample
// within that bucket's span, normalized 0..1 against the GLOBAL max across all
// buckets (so the loudest moment in the window reads as 1.0). An empty
// samples slice or all-silence input (global max 0) yields all-zero buckets —
// never NaN/Inf. buckets<=0 defensively falls back to 200 (this function must
// never divide by zero even if called directly). PURE (unit-tested without
// ffmpeg).
func bucketPeaks(samples []int16, buckets int) []float64 {
	if buckets <= 0 {
		buckets = defaultPeakBuckets
	}
	out := make([]float64, buckets)
	n := len(samples)
	if n == 0 {
		return out
	}

	// Per-bucket max(abs) first. abs() runs in int32 (not int16) so
	// abs(math.MinInt16) = 32768 can't overflow.
	raw := make([]int32, buckets)
	for i := 0; i < buckets; i++ {
		start := i * n / buckets
		if start >= n {
			continue // more buckets than samples: this bucket is empty
		}
		end := (i + 1) * n / buckets
		if end <= start {
			end = start + 1
		}
		if end > n {
			end = n
		}
		var m int32
		for _, s := range samples[start:end] {
			v := int32(s)
			if v < 0 {
				v = -v
			}
			if v > m {
				m = v
			}
		}
		raw[i] = m
	}

	var global int32
	for _, v := range raw {
		if v > global {
			global = v
		}
	}
	if global == 0 {
		return out // all-silence: zeros
	}
	for i, v := range raw {
		out[i] = float64(v) / float64(global)
	}
	return out
}
