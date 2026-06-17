// Package refmatch is becky's deterministic reference-matching brain: it measures
// how one mono audio stem differs from another (a reference that already "sounds
// right") and emits a concrete, plain-English MATCH PLAN — the exact tonal,
// loudness and dynamics moves to make your stem match the reference. It is the
// deterministic heavy-lifting a premium reference-matching plugin does, minus the
// taste: file in -> JSON out, same bytes in -> identical numbers out.
//
// It builds entirely on internal/dsp (the pure-Go WAV decoder + radix-2 FFT) and
// mirrors that package's Hann-windowed STFT framing so the spectrum is computed the
// same way becky already computes one elsewhere. No new dependencies, no cgo, no
// network, no model — just arithmetic.
//
// SCOPE (be honest about it):
//   - This is MONO matching. dsp.DecodeWAV downmixes to mono, so stereo-width /
//     panning / mid-side balance is OUT OF SCOPE. A Phase-2 stereo pass would need a
//     channel-preserving decoder; we do not fake a width number from mono.
//   - Loudness is RMS-based dBFS with an OPTIONAL, clearly-labelled K-weight-ish
//     high-shelf approximation. It is NOT certified ITU-R BS.1770 / LUFS. The output
//     says so. Use it for relative ("you are 2 dB quieter") comparison, which is
//     exactly what reference matching needs — not for compliance metering.
//   - Per-band EQ moves are broad tonal-balance corrections (a band is wide), not a
//     surgical notch-finder. They tell you which region is too dark/bright and by how
//     much, the way an engineer eyeballs a reference curve.
package refmatch

import (
	"math"
	"sort"

	"becky-go/internal/dsp"
)

// Framing constants mirror internal/dsp/analyze.go (frameSize 2048, hopSize 512,
// Hann window) so the magnitude spectrum is accumulated exactly as becky does
// elsewhere. They are fixed (not flags) to keep output deterministic and comparable
// across runs: a profile saved today matches one measured tomorrow.
const (
	frameSize = 2048 // STFT window length (samples)
	hopSize   = 512  // hop between frames (75% overlap)
)

// Band is one fixed, named, log-spaced frequency region of the spectrum and the
// average energy measured in it, in dBFS (0 dB = full scale; values are negative).
type Band struct {
	Name     string  `json:"name"`      // human label, e.g. "presence"
	LoHz     float64 `json:"lo_hz"`     // inclusive low edge
	HiHz     float64 `json:"hi_hz"`     // exclusive high edge
	EnergyDB float64 `json:"energy_db"` // average band energy, dBFS
}

// bandDefs are the FIXED log-spaced band edges (Hz). These are constants, not
// configuration, so two profiles are always directly comparable. The set is the
// vocabulary an engineer actually uses when describing a reference's tonal balance.
// Edges are [Lo, Hi): each FFT bin lands in exactly one band.
var bandDefs = []struct {
	name string
	lo   float64
	hi   float64
}{
	{"sub", 20, 60},             // rumble / sub weight
	{"low", 60, 120},            // kick / bass fundamentals
	{"lowMid", 120, 300},        // body / warmth (and mud)
	{"mid", 300, 800},           // box / fundamentals of most instruments
	{"highMid", 800, 2500},      // attack / nasal honk
	{"presence", 2500, 6000},    // intelligibility / bite
	{"brilliance", 6000, 12000}, // sheen / cymbals
	{"air", 12000, 20000},       // open top / "expensive" air
}

// Profile is the deterministic fingerprint of one stem: its tonal balance (fixed
// bands in dBFS), its loudness, its dynamics (crest factor) and a brightness
// summary. Same WAV bytes -> identical Profile. A degraded measurement still
// returns a Profile (with Degraded set and a Note) rather than panicking.
type Profile struct {
	Source      string  `json:"source,omitempty"` // basename of the stem (provenance only)
	SampleRate  int     `json:"sample_rate"`
	DurationSec float64 `json:"duration_sec"`
	Bands       []Band  `json:"bands"`       // fixed-order tonal balance, dBFS
	LoudnessDB  float64 `json:"loudness_db"` // integrated RMS level, dBFS
	KWeighted   bool    `json:"k_weighted"`  // true if the K-weight approx was applied
	PeakDB      float64 `json:"peak_db"`     // sample peak, dBFS
	CrestDB     float64 `json:"crest_db"`    // peak_db - loudness_db (higher = more dynamic)
	CentroidHz  float64 `json:"centroid_hz"` // spectral centroid (brightness)
	Degraded    bool    `json:"degraded,omitempty"`
	Note        string  `json:"note,omitempty"` // plain-English caveat when degraded/approx
}

// silenceFloorDB is the floor we clamp dB values to, so digital silence (energy 0)
// reports a sane, finite "-120 dB" instead of -Inf and so deltas stay meaningful.
const silenceFloorDB = -120.0

// minFrames is the fewest STFT frames a profile needs to be trustworthy. Below this
// the audio is too short for a stable average; we still return a Profile but mark it
// degraded so the caller (and Jordan) knows the numbers are thin.
const minFrames = 4

// Options tunes the measurement. The zero value is the honest default (RMS dBFS, no
// K-weighting). Setting KWeight on enables the labelled high-shelf approximation.
type Options struct {
	KWeight bool // apply the K-weight-ish high-shelf before measuring loudness
}

// Analyze measures a decoded stem into a Profile. It never panics: empty, silent or
// malformed-but-decoded audio yields a Degraded profile with a plain note. The
// caller decodes bytes via dsp.DecodeWAV (which can return its own error for a truly
// unreadable file) and passes the result here.
func Analyze(a dsp.Audio, opt Options) Profile {
	p := Profile{SampleRate: a.SampleRate, DurationSec: a.DurationSec(), KWeighted: opt.KWeight}

	// Always emit the full band list (zeroed to the floor) so the shape of the
	// output is identical even when we can't measure — comparisons never crash on a
	// missing band.
	p.Bands = zeroBands()

	if a.SampleRate <= 0 || len(a.Samples) == 0 {
		p.Degraded = true
		p.LoudnessDB, p.PeakDB, p.CrestDB = silenceFloorDB, silenceFloorDB, 0
		p.Note = "no audio (empty or zero sample-rate) — measured nothing"
		return p
	}

	// Loudness / peak / crest run on the (optionally K-weighted) time series.
	series := a.Samples
	if opt.KWeight {
		series = kWeightApprox(series, a.SampleRate)
		// crest is a property of the perceived signal; peak we still take pre-weight
		// (clipping is about the actual samples), so compute peak separately below.
	}
	p.LoudnessDB = clampDB(toDB(rms(series)))
	p.PeakDB = clampDB(toDB(peak(a.Samples)))
	p.CrestDB = p.PeakDB - p.LoudnessDB
	if p.CrestDB < 0 {
		p.CrestDB = 0 // numerical guard; peak >= rms always, but stay safe
	}

	// Spectrum: averaged Hann-windowed magnitude over overlapping frames.
	avgMag, frames := averageMagnitude(a.Samples)
	if frames == 0 {
		p.Degraded = true
		p.Note = "audio shorter than one analysis frame — tonal balance not measured"
		return p
	}
	p.Bands = bandEnergies(avgMag, a.SampleRate)
	p.CentroidHz = centroid(avgMag, a.SampleRate)

	if frames < minFrames {
		p.Degraded = true
		p.Note = "very short audio — tonal-balance average is from few frames; treat as approximate"
	}
	return p
}

// zeroBands returns the fixed band list with every energy at the silence floor.
func zeroBands() []Band {
	out := make([]Band, len(bandDefs))
	for i, b := range bandDefs {
		out[i] = Band{Name: b.name, LoHz: b.lo, HiHz: b.hi, EnergyDB: silenceFloorDB}
	}
	return out
}

// averageMagnitude accumulates the Hann-windowed magnitude spectrum over all
// overlapping frames and returns the per-bin average plus the frame count. It mirrors
// dsp.Analyze's framing (frameSize/hopSize, zero-pad to next pow2). Returns (nil, 0)
// when the audio is shorter than one frame.
func averageMagnitude(samples []float64) ([]float64, int) {
	if len(samples) < frameSize {
		return nil, 0
	}
	fftN := nextPow2(frameSize)
	win := hann(frameSize)
	half := fftN/2 + 1
	acc := make([]float64, half)
	buf := make([]dsp.Complex, fftN)
	frames := 0
	for start := 0; start+frameSize <= len(samples); start += hopSize {
		for i := range buf {
			buf[i] = dsp.Complex{}
		}
		for i := 0; i < frameSize; i++ {
			buf[i] = dsp.Complex{Re: samples[start+i] * win[i]}
		}
		mag := dsp.Magnitudes(buf) // FFTs buf in place, returns first half+1 bins
		for k := 0; k < half; k++ {
			acc[k] += mag[k]
		}
		frames++
	}
	if frames == 0 {
		return nil, 0
	}
	inv := 1.0 / float64(frames)
	for k := range acc {
		acc[k] *= inv
	}
	return acc, frames
}

// bandEnergies sums the average magnitude into the fixed bands and converts each to
// dBFS. The per-band value is the RMS-style average magnitude across the band's bins
// (energy-mean then sqrt), normalized by the FFT window so it is comparable across
// files at the same sample rate. Empty bands clamp to the silence floor.
func bandEnergies(avgMag []float64, sr int) []Band {
	fftN := nextPow2(frameSize)
	binHz := float64(sr) / float64(fftN)
	// Hann window coherent-gain normalization: a full-scale tone produces a peak of
	// ~sum(win)/2. Normalizing by sum(win) keeps the dB scale stable and below 0.
	norm := windowSum(frameSize)
	out := make([]Band, len(bandDefs))
	for bi, bd := range bandDefs {
		var sumSq float64
		var n int
		for k := 1; k < len(avgMag); k++ { // skip DC bin 0
			f := float64(k) * binHz
			if f >= bd.lo && f < bd.hi {
				m := avgMag[k] / norm
				sumSq += m * m
				n++
			}
		}
		e := silenceFloorDB
		if n > 0 {
			e = clampDB(toDB(math.Sqrt(sumSq / float64(n))))
		}
		out[bi] = Band{Name: bd.name, LoHz: bd.lo, HiHz: bd.hi, EnergyDB: e}
	}
	return out
}

// centroid returns the magnitude-weighted mean frequency (the brightness scalar):
// sum(f*|X|) / sum(|X|). A silent/empty spectrum yields 0.
func centroid(avgMag []float64, sr int) float64 {
	fftN := nextPow2(frameSize)
	binHz := float64(sr) / float64(fftN)
	var num, den float64
	for k := 1; k < len(avgMag); k++ {
		num += float64(k) * binHz * avgMag[k]
		den += avgMag[k]
	}
	if den <= 0 {
		return 0
	}
	return num / den
}

// rms returns the root-mean-square amplitude of the series ([0,1] for [-1,1] input).
func rms(s []float64) float64 {
	if len(s) == 0 {
		return 0
	}
	var sum float64
	for _, v := range s {
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(s)))
}

// peak returns the maximum absolute sample value.
func peak(s []float64) float64 {
	var m float64
	for _, v := range s {
		if a := math.Abs(v); a > m {
			m = a
		}
	}
	return m
}

// toDB converts a linear amplitude (0..1) to dBFS (20*log10). Zero/negative -> -Inf;
// callers clampDB the result to the silence floor.
func toDB(amp float64) float64 {
	if amp <= 0 {
		return math.Inf(-1)
	}
	return 20 * math.Log10(amp)
}

// clampDB pins a dB value into [silenceFloorDB, 0] and replaces NaN/-Inf with the
// floor, so output is always a finite, sane number.
func clampDB(db float64) float64 {
	if math.IsNaN(db) || math.IsInf(db, -1) || db < silenceFloorDB {
		return silenceFloorDB
	}
	if db > 0 {
		return 0 // a downmixed sum can momentarily exceed FS; report as 0 dBFS
	}
	return db
}

// kWeightApprox applies a crude high-shelf (the "K" in K-weighting de-emphasizes
// low end / lifts highs to track perceived loudness). This is a ONE-pole high-shelf
// approximation, NOT the ITU-R BS.1770 cascade — it is honestly labelled everywhere
// it surfaces. It exists only to nudge the relative loudness number toward perception
// when the caller opts in; it is deterministic. Returns a new slice (input untouched).
func kWeightApprox(s []float64, sr int) []float64 {
	if len(s) == 0 || sr <= 0 {
		return s
	}
	// Simple first-order high-shelf: y[n] = s[n] + g*(s[n]-s[n-1]) emphasises HF.
	// g chosen small (0.3) so it tilts, not transforms. Documented approximation.
	const g = 0.3
	out := make([]float64, len(s))
	prev := s[0]
	for i, v := range s {
		out[i] = v + g*(v-prev)
		prev = v
	}
	return out
}

// --- small local DSP helpers (kept here so refmatch owns its framing; they mirror
// the unexported helpers in internal/dsp, which we cannot import) ---

func nextPow2(n int) int {
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}

func hann(n int) []float64 {
	w := make([]float64, n)
	if n <= 1 {
		for i := range w {
			w[i] = 1
		}
		return w
	}
	for i := 0; i < n; i++ {
		w[i] = 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(n-1)))
	}
	return w
}

func windowSum(n int) float64 {
	var s float64
	for _, v := range hann(n) {
		s += v
	}
	if s <= 0 {
		return 1
	}
	return s
}

// BandByName returns the band with the given name and true, or a zero Band and
// false. Used by the matcher to align two profiles by name (order is fixed, but this
// is robust to it). Bands are kept sorted by their fixed definition order.
func (p Profile) BandByName(name string) (Band, bool) {
	for _, b := range p.Bands {
		if b.Name == name {
			return b, true
		}
	}
	return Band{}, false
}

// sortBandsByDef ensures a band slice is in the canonical fixed order (defensive;
// Analyze already produces them in order, but a hand-built/loaded profile might not).
func sortBandsByDef(bands []Band) {
	order := make(map[string]int, len(bandDefs))
	for i, b := range bandDefs {
		order[b.name] = i
	}
	sort.SliceStable(bands, func(i, j int) bool {
		return order[bands[i].Name] < order[bands[j].Name]
	})
}
