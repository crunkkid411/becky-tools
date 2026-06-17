package stemscan

import (
	"math"
	"strings"

	"becky-go/internal/dsp"
)

// role.go is the spectral ROLE heuristic. It is the ONE inference becky makes here,
// and per FORENSIC-OUTPUT-PHILOSOPHY.md it must corroborate-then-conclude: name a
// role only when the audio signal is clear (optionally backed by the filename), and
// otherwise say "unknown" rather than emit a confident wrong guess the producer then
// has to un-sort. Everything else in this package is an exact measurement; this is
// the part to be humble about.
//
// HONEST LIMITS (read before trusting a role):
//   - It is pure spectral-shape + crest + filename. It does NOT understand music.
//   - A bass guitar and a synth bass look identical -> both "bass". A tom and a low
//     floor-tom-heavy loop can read as "kick". A bright distorted guitar and a busy
//     cymbal/hat layer both pile energy up high -> "hats-or-cymbals" vs "guitar-or-keys"
//     is the SHAKIEST split (we lean on crest + filename there).
//   - A doubled/processed vocal, a vocal-chop synth, or a melodic lead can all sit in
//     the same mid band as "vocal" -> easy false positives; we require a mid-forward,
//     non-percussive shape AND fall back to "unknown" when it's marginal.
//   - A reverb tail, a riser, or a sound-effect has no stable role -> "unknown".
//   - A two-bar loop of a full kit will read as "fullmix", which is usually right for
//     a stem export but wrong if it's actually a busy single drum track.
// The filename is a SECONDARY corroborator only: "kick.wav" nudges, it does not decide,
// because session naming is wildly inconsistent ("K", "BD", "Kik_01", "INST").

// bandEnergy holds the fraction of total spectral energy in coarse frequency bands,
// plus the spectral centroid (Hz). Fractions sum to ~1.
type bandEnergy struct {
	sub    float64 // < 60 Hz   (kick/bass fundamental, rumble)
	low    float64 // 60-250 Hz (bass body, kick thump)
	lowMid float64 // 250-2k Hz (vocal/guitar/snare body)
	hiMid  float64 // 2k-6k Hz  (presence, snare crack, vocal sibilance start)
	high   float64 // > 6k Hz   (cymbals, hats, air)
	centHz float64 // spectral centroid
}

// ClassifyRole returns the heuristic role, a 0..1 confidence, and a plain-English
// basis string. crestDB is passed in (already measured) since dynamics separate
// percussive from sustained sources. A clear spectral verdict that the filename also
// supports yields high confidence; a marginal spectral verdict with no filename help
// yields RoleUnknown (NOT a low-confidence name we'd have to walk back).
func ClassifyRole(samples []float64, sr int, name string, crestDB float64) (Role, float64, string) {
	be, ok := spectrum(samples, sr)
	if !ok {
		return RoleUnknown, 0, "too little signal to classify"
	}
	specRole, specConf, specWhy := classifySpectral(be, crestDB)
	fileRole := roleFromName(name)

	// Corroboration: spectrum is the primary signal; filename corroborates or, when the
	// spectrum is genuinely ambiguous, can tip a near-call. It never overrides a clear
	// spectral verdict (a file mislabeled "kick" that is plainly a cymbal stays cymbal).
	switch {
	case specRole != RoleUnknown && fileRole == specRole:
		return specRole, clampConf(specConf + 0.2), specWhy + "; filename agrees"
	case specRole != RoleUnknown && fileRole != RoleUnknown && fileRole != specRole:
		// Disagreement: trust the audio but lower confidence and SAY they disagree.
		return specRole, clampConf(specConf - 0.15), specWhy + "; note: filename suggests " + string(fileRole)
	case specRole != RoleUnknown:
		return specRole, specConf, specWhy
	case fileRole != RoleUnknown && specConf >= 0.25:
		// Spectrum leaned this way weakly and the filename corroborates -> name it, low conf.
		return fileRole, 0.45, "filename says " + string(fileRole) + "; spectrum is consistent but not decisive"
	default:
		// Lone weak signal -> unknown (house rule: don't flood with maybes).
		return RoleUnknown, 0.2, "spectrum not distinctive enough to name confidently"
	}
}

// classifySpectral is the audio-only verdict. Thresholds are fixed constants and the
// order matters (most distinctive shapes first). Returns RoleUnknown when no shape is
// clearly dominant — that "unknown" is a FEATURE, not a failure.
func classifySpectral(be bandEnergy, crestDB float64) (Role, float64, string) {
	lowTotal := be.sub + be.low
	highTotal := be.hiMid + be.high
	percussive := crestDB >= 12 // punchy/transient-heavy vs. sustained

	switch {
	// Kick: energy dominated by sub+low, very little up top, punchy.
	case lowTotal >= 0.6 && be.high < 0.08 && be.centHz < 250 && percussive:
		return RoleKick, 0.85, "energy is almost all sub/low with a punchy transient (kick-like)"

	// Bass: low-dominant but more sustained (lower crest) than a kick, still dark.
	case lowTotal >= 0.55 && be.high < 0.12 && be.centHz < 400 && !percussive:
		return RoleBass, 0.75, "low-dominant and sustained (bass-like)"

	// Hats/cymbals: energy piled up high and bright, percussive.
	case highTotal >= 0.6 && be.centHz > 4000 && percussive:
		return RoleHatsCymbals, 0.8, "energy concentrated up high and bright (hats/cymbals-like)"

	// Snare/clap: broadband with a strong hi-mid crack AND a punchy transient.
	case be.hiMid >= 0.25 && be.lowMid >= 0.2 && percussive && be.centHz > 1200 && be.centHz < 5000:
		return RoleSnareClap, 0.7, "broadband with a hi-mid crack and a sharp transient (snare/clap-like)"

	// Toms: low-mid-dominant pitched drum, punchy, darker than a snare.
	case be.low >= 0.2 && be.lowMid >= 0.35 && percussive && be.centHz > 150 && be.centHz < 1200:
		return RoleToms, 0.6, "low-mid pitched body with a transient (tom-like)"

	// Vocal: mid-forward, sustained (not percussive), centroid in the voice range.
	case be.lowMid >= 0.45 && be.centHz > 300 && be.centHz < 3500 && !percussive && highTotal < 0.4:
		return RoleVocal, 0.6, "mid-forward and sustained in the voice range (vocal-like)"

	// Guitar/keys: broad mid-rich, sustained, more high content than a vocal.
	case be.lowMid >= 0.35 && highTotal >= 0.2 && !percussive && be.centHz > 500 && be.centHz < 4500:
		return RoleGuitarKeys, 0.55, "broad mid-rich and sustained with some top (guitar/keys-like)"

	// Full mix: energy spread fairly evenly across ALL bands (a printed mix/bus).
	case be.low > 0.12 && be.lowMid > 0.18 && be.hiMid > 0.1 && be.high > 0.08 && lowTotal < 0.55 && highTotal < 0.55:
		return RoleFullMix, 0.55, "energy spread across all bands (looks like a full mix/bus, not one element)"

	default:
		return RoleUnknown, 0.2, ""
	}
}

// spectrum runs one Hann-windowed FFT over the LOUDEST n-sample slice of the stem and
// bins the magnitude spectrum into the coarse bands + centroid. Picking the loudest
// (highest-energy) slice — rather than the head (often a count-in) or the geometric
// center (often a decayed tail on a single-hit sample) — keeps a sparse stem like a
// one-shot kick from reading as silence. The slice choice is deterministic (first
// max-energy window in a fixed stride scan). Returns ok=false when there's not enough
// above-floor energy anywhere to say anything.
func spectrum(samples []float64, sr int) (bandEnergy, bool) {
	const n = 4096
	if len(samples) < n || sr <= 0 {
		// Short stems: analyze the whole thing zero-padded to the next pow2.
		return spectrumWhole(samples, sr)
	}
	start := loudestSliceStart(samples, n)
	win := make([]dsp.Complex, n)
	hann := hannLike(n)
	for i := 0; i < n; i++ {
		win[i] = dsp.Complex{Re: samples[start+i] * hann[i]}
	}
	mags := dsp.Magnitudes(win)
	return binSpectrum(mags, n, sr)
}

// loudestSliceStart scans the stem in fixed strides and returns the start index of the
// n-sample window with the most energy (first one wins ties => deterministic). This is
// where the actual SOUND is, which is what we want to classify.
func loudestSliceStart(samples []float64, n int) int {
	stride := n / 2
	if stride < 1 {
		stride = 1
	}
	best, bestE := 0, -1.0
	for start := 0; start+n <= len(samples); start += stride {
		var e float64
		for i := start; i < start+n; i++ {
			e += samples[i] * samples[i]
		}
		if e > bestE {
			bestE, best = e, start
		}
	}
	return best
}

// spectrumWhole handles short stems: zero-pad to the next power of two and FFT all of
// it. Degrade-never-crash for tiny inputs.
func spectrumWhole(samples []float64, sr int) (bandEnergy, bool) {
	if len(samples) == 0 || sr <= 0 {
		return bandEnergy{}, false
	}
	n := 1
	for n < len(samples) {
		n <<= 1
	}
	if n < 2 {
		return bandEnergy{}, false
	}
	win := make([]dsp.Complex, n)
	hann := hannLike(len(samples))
	for i := 0; i < len(samples); i++ {
		win[i] = dsp.Complex{Re: samples[i] * hann[i]}
	}
	mags := dsp.Magnitudes(win)
	return binSpectrum(mags, n, sr)
}

// binSpectrum turns a magnitude spectrum (length n/2+1, for FFT size n at sr) into the
// banded energy fractions + centroid. Total energy near zero => ok=false.
func binSpectrum(mags []float64, n, sr int) (bandEnergy, bool) {
	var be bandEnergy
	var total, centNum float64
	hzPerBin := float64(sr) / float64(n)
	for k := 1; k < len(mags); k++ { // skip DC bin
		mag := mags[k]
		e := mag * mag
		freq := float64(k) * hzPerBin
		total += e
		centNum += freq * e
		switch {
		case freq < 60:
			be.sub += e
		case freq < 250:
			be.low += e
		case freq < 2000:
			be.lowMid += e
		case freq < 6000:
			be.hiMid += e
		default:
			be.high += e
		}
	}
	if total <= 1e-9 {
		return bandEnergy{}, false
	}
	be.sub /= total
	be.low /= total
	be.lowMid /= total
	be.hiMid /= total
	be.high /= total
	be.centHz = centNum / total
	return be, true
}

// hannLike returns an n-point Hann window (re-implemented here to avoid depending on
// dsp's unexported window; matches the same 0.5*(1-cos) shape).
func hannLike(n int) []float64 {
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

// roleFromName maps common (messy) stem-naming conventions to a role. SECONDARY signal
// only. Matching is case-insensitive substring/token; ordered so more specific tokens
// win (e.g. "overhead" -> cymbals before a bare "o").
func roleFromName(name string) Role {
	n := strings.ToLower(name)
	// strip extension
	if dot := strings.LastIndex(n, "."); dot > 0 {
		n = n[:dot]
	}
	contains := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(n, s) {
				return true
			}
		}
		return false
	}
	switch {
	case contains("kick", "_bd", "bassdrum", "bass drum", "kik"):
		return RoleKick
	case contains("snare", "snr", "clap", "clp", "_sd", "rimshot", "rim"):
		return RoleSnareClap
	case contains("overhead", "_oh", "ohl", "ohr", "cymbal", "ride", "crash", "hat", "hihat", "hi-hat"):
		return RoleHatsCymbals
	case contains("tom", "floortom", "racktom"):
		return RoleToms
	case contains("vocal", "vox", "_bv", "lead vox", "backing", "harmon", "_lv", "_vx"):
		return RoleVocal
	case contains("bass", "_bg", "sub", "808"): // after kick check so "bassdrum" doesn't land here
		return RoleBass
	case contains("guitar", "gtr", "keys", "piano", "synth", "rhodes", "organ", "pad", "lead"):
		return RoleGuitarKeys
	case contains("mix", "master", "fullmix", "2track", "stereo out", "print"):
		return RoleFullMix
	default:
		return RoleUnknown
	}
}

func clampConf(c float64) float64 {
	if c < 0 {
		return 0
	}
	if c > 1 {
		return 1
	}
	return round2(c)
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
