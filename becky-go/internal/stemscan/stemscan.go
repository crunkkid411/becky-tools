// Package stemscan is becky's deterministic, offline, pure-Go scanner for a folder
// of studio stems (one WAV per recorded element of a session — kick, snare, bass,
// vocal, etc.). It answers the producer's real question — "what's on this drive,
// what's wrong with it, and where do I put the faders to START?" — without him
// opening twenty tracks and eyeballing meters for an hour.
//
// What it measures per stem is HONEST and deterministic (no models, no network):
//   - peak dBFS + a clipping flag (samples pinned at/over full scale — a real defect)
//   - RMS loudness in dBFS (NOT certified LUFS — see Loudness docs below)
//   - crest factor (how dynamic vs. squashed the stem is)
//   - DC offset and a near-silent / empty flag
//   - a spectral ROLE guess (kick / bass / snare-or-clap / ... ) — a HEURISTIC
//   - a starting-balance gain suggestion toward a documented reference loudness
//
// House rule (FORENSIC-OUTPUT-PHILOSOPHY.md — corroborate, then conclude): the role
// guess only NAMES a role when the audio signal is clear, optionally corroborated by
// the filename. A lone weak signal stays "unknown" rather than flooding the producer
// with confident wrong guesses he then has to un-sort. The measurements (peak/RMS/
// clipping) are exact and always stated plainly; only the role is a graded inference.
//
// Same folder in => byte-identical report out: files are processed in sorted order,
// every threshold is a fixed constant, and a malformed/unreadable/short WAV is noted
// and skipped (degrade-never-crash) rather than aborting the scan.
package stemscan

import (
	"math"
	"sort"
	"strings"

	"becky-go/internal/dsp"
	"becky-go/internal/pathx"
)

// Role is the spectral classification of a stem. It is a HEURISTIC label, not a
// certainty — see ClassifyRole for the honest false-positive notes.
type Role string

const (
	RoleKick        Role = "kick"
	RoleBass        Role = "bass"
	RoleSnareClap   Role = "snare-or-clap"
	RoleToms        Role = "toms"
	RoleHatsCymbals Role = "hats-or-cymbals"
	RoleVocal       Role = "vocal"
	RoleGuitarKeys  Role = "guitar-or-keys"
	RoleFullMix     Role = "fullmix"
	RoleUnknown     Role = "unknown" // signal not clear enough to name (house rule)
)

// Reference loudness target for the starting balance. -18 dBFS RMS is a long-standing
// "mix gain-staging" rule of thumb: it leaves comfortable headroom on each stem and
// puts everything in roughly the same ballpark so faders START sane rather than at
// random recorded levels. This is a STARTING POINT, not a mix — the producer rides
// from here. The value is a fixed constant so the suggestion is deterministic.
const TargetLoudnessDBFS = -18.0

// SuggestGainClampDB bounds the suggested gain so a near-silent stem doesn't get a
// +60 dB "suggestion" that would just amplify noise. Beyond this, becky says so.
const SuggestGainClampDB = 12.0

// Fixed measurement thresholds (constants => deterministic, documented defects).
const (
	clipThreshold   = 0.999  // |sample| at/over this counts as clipped (full-scale pin)
	clipFlagFrac    = 0.0001 // fraction of clipped samples to RAISE the clipping flag
	silentRMSDBFS   = -60.0  // RMS quieter than this => treated as near-silent / empty
	minDecodeFrames = 64     // shorter than this (after decode) => too short to analyze
)

// StemReport is the per-stem analysis. The numeric fields are exact measurements; the
// Role/RoleConfidence/RoleBasis fields are a graded inference (corroborate-then-conclude).
type StemReport struct {
	File        string  `json:"file"`        // path as given (may be a Windows path)
	Name        string  `json:"name"`        // separator-agnostic basename (pathx)
	SampleRate  int     `json:"sample_rate"` // Hz
	DurationSec float64 `json:"duration_sec"`

	PeakDBFS     float64 `json:"peak_dbfs"`     // peak level, dBFS (0 = full scale)
	LoudnessDBFS float64 `json:"loudness_dbfs"` // RMS loudness, dBFS (honest RMS, NOT LUFS)
	CrestDB      float64 `json:"crest_db"`      // peak - RMS, dB (dynamics; low = squashed)
	DCOffset     float64 `json:"dc_offset"`     // mean sample value (should be ~0)

	Clipping    bool    `json:"clipping"`     // full-scale samples found (a real defect)
	ClippedFrac float64 `json:"clipped_frac"` // fraction of samples at/over full scale
	NearSilent  bool    `json:"near_silent"`  // below silentRMSDBFS — likely empty/muted

	Role           Role    `json:"role"`            // heuristic classification (or "unknown")
	RoleConfidence float64 `json:"role_confidence"` // 0..1, graded — low => stated as a guess
	RoleBasis      string  `json:"role_basis"`      // plain-English why (spectrum + filename)

	// SuggestGainDB is a relative trim (dB) toward TargetLoudnessDBFS so the stems sit
	// together as a STARTING balance: positive = turn this stem up, negative = down.
	// Clamped to +/-SuggestGainClampDB; a near-silent stem gets 0 with a note instead
	// of a giant noise-amplifying boost.
	SuggestGainDB float64 `json:"suggest_gain_db"`
	SuggestNote   string  `json:"suggest_note"`

	// Skipped records that this file could NOT be analyzed (unreadable / not a WAV /
	// too short); Reason says why in plain language. A skipped report carries no
	// measurements — it exists so the producer SEES the file was there and why becky
	// passed on it, rather than the file silently vanishing from the count.
	Skipped bool   `json:"skipped,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// AnalyzeStem decodes one WAV byte buffer (named by path, which may be a Windows
// path) and returns its StemReport. It NEVER panics: a malformed/too-short buffer
// yields a Skipped report with a plain reason. The filename is used only as a
// secondary, corroborating signal for the role guess.
func AnalyzeStem(path string, wav []byte) StemReport {
	name := pathx.Base(path)
	r := StemReport{File: path, Name: name}

	audio, err := dsp.DecodeWAV(wav)
	if err != nil {
		r.Skipped = true
		r.Reason = "couldn't read as WAV: " + err.Error()
		return r
	}
	if len(audio.Samples) < minDecodeFrames || audio.SampleRate <= 0 {
		r.Skipped = true
		r.Reason = "too short to analyze (likely empty or truncated)"
		return r
	}

	r.SampleRate = audio.SampleRate
	r.DurationSec = audio.DurationSec()
	fillLevels(&r, audio.Samples)

	role, conf, basis := ClassifyRole(audio.Samples, audio.SampleRate, name, r.CrestDB)
	r.Role, r.RoleConfidence, r.RoleBasis = role, conf, basis

	fillSuggestion(&r)
	return r
}

// fillLevels computes peak/RMS/crest/DC/clipping over the mono samples.
func fillLevels(r *StemReport, s []float64) {
	var peak, sumSq, sum float64
	var clipped int
	for _, x := range s {
		a := math.Abs(x)
		if a > peak {
			peak = a
		}
		if a >= clipThreshold {
			clipped++
		}
		sumSq += x * x
		sum += x
	}
	n := float64(len(s))
	rms := math.Sqrt(sumSq / n)

	r.PeakDBFS = ampToDBFS(peak)
	r.LoudnessDBFS = ampToDBFS(rms)
	r.CrestDB = round1(r.PeakDBFS - r.LoudnessDBFS)
	r.DCOffset = sum / n
	r.ClippedFrac = float64(clipped) / n
	r.Clipping = r.ClippedFrac >= clipFlagFrac
	r.NearSilent = r.LoudnessDBFS <= silentRMSDBFS
}

// fillSuggestion sets the starting-balance gain. A near-silent stem is NOT boosted
// (that would just raise noise) — becky flags it for the human to decide instead.
func fillSuggestion(r *StemReport) {
	if r.NearSilent {
		r.SuggestGainDB = 0
		r.SuggestNote = "near-silent — left alone (boosting this would just raise noise; check if it's muted/empty)"
		return
	}
	gain := TargetLoudnessDBFS - r.LoudnessDBFS
	clamped := false
	if gain > SuggestGainClampDB {
		gain, clamped = SuggestGainClampDB, true
	} else if gain < -SuggestGainClampDB {
		gain, clamped = -SuggestGainClampDB, true
	}
	r.SuggestGainDB = round1(gain)
	switch {
	case clamped && gain > 0:
		r.SuggestNote = "very quiet — capped the boost; may need re-recording or a closer look"
	case clamped:
		r.SuggestNote = "very hot — capped the cut; check the recording gain"
	default:
		r.SuggestNote = "toward a -18 dBFS RMS starting balance"
	}
}

// ampToDBFS converts a linear amplitude (>=0) to dBFS. Zero/negative => -inf, which we
// floor to a large finite negative so JSON stays valid and tables stay readable.
func ampToDBFS(a float64) float64 {
	if a <= 0 {
		return -120.0
	}
	db := 20 * math.Log10(a)
	if db < -120 {
		return -120
	}
	return round1(db)
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// ----- Folder-level report ------------------------------------------------------

// FolderReport is the whole-folder result: the per-stem reports (sorted by name) plus
// a scannable headline a non-developer reads first.
type FolderReport struct {
	Dir          string       `json:"dir"`
	StemCount    int          `json:"stem_count"`    // analyzable stems (excludes skipped)
	SkippedCount int          `json:"skipped_count"` // files becky couldn't read
	Clipping     []string     `json:"clipping"`      // names of clipping stems
	TargetDBFS   float64      `json:"target_dbfs"`   // the starting-balance reference
	Headline     string       `json:"headline"`      // one-line plain-English summary
	Stems        []StemReport `json:"stems"`
}

// BuildFolderReport assembles a FolderReport from per-file (path,bytes) inputs. It
// sorts the inputs by basename FIRST so the output is byte-identical for the same
// folder regardless of OS directory-iteration order. The caller does the actual file
// I/O (so this stays pure + testable); a nil/empty input yields a friendly empty
// report, never an error.
func BuildFolderReport(dir string, files []FileInput) FolderReport {
	sorted := make([]FileInput, len(files))
	copy(sorted, files)
	sort.SliceStable(sorted, func(i, j int) bool {
		ni, nj := pathx.Base(sorted[i].Path), pathx.Base(sorted[j].Path)
		if ni != nj {
			return ni < nj
		}
		return sorted[i].Path < sorted[j].Path
	})

	rep := FolderReport{Dir: dir, TargetDBFS: TargetLoudnessDBFS}
	for _, f := range sorted {
		var sr StemReport
		if f.Err != nil {
			sr = StemReport{File: f.Path, Name: pathx.Base(f.Path), Skipped: true,
				Reason: "couldn't open file: " + f.Err.Error()}
		} else {
			sr = AnalyzeStem(f.Path, f.Data)
		}
		rep.Stems = append(rep.Stems, sr)
		if sr.Skipped {
			rep.SkippedCount++
			continue
		}
		rep.StemCount++
		if sr.Clipping {
			rep.Clipping = append(rep.Clipping, sr.Name)
		}
	}
	rep.Headline = buildHeadline(&rep)
	return rep
}

// FileInput is one candidate stem the caller read off disk. Err non-nil means the read
// itself failed; becky still reports the file (skipped) so it's visible in the count.
type FileInput struct {
	Path string
	Data []byte
	Err  error
}

// buildHeadline writes the scannable one-liner. It surfaces the things a producer
// actually triages on: how many stems, what's CLIPPING (named), and the single
// hottest stem (the most likely fader to pull down first).
func buildHeadline(rep *FolderReport) string {
	if rep.StemCount == 0 && rep.SkippedCount == 0 {
		return "📁 no WAV stems found in this folder."
	}
	var b strings.Builder
	b.WriteString("📁 ")
	b.WriteString(plural(rep.StemCount, "stem", "stems"))
	if rep.SkippedCount > 0 {
		b.WriteString(" (+")
		b.WriteString(plural(rep.SkippedCount, "file skipped", "files skipped"))
		b.WriteString(")")
	}
	if len(rep.Clipping) > 0 {
		b.WriteString(" — ⚠ ")
		b.WriteString(itoa(len(rep.Clipping)))
		b.WriteString(" CLIPPING (")
		b.WriteString(strings.Join(rep.Clipping, ", "))
		b.WriteString(")")
	}
	if hot := hottestNote(rep); hot != "" {
		b.WriteString(" — ")
		b.WriteString(hot)
	}
	b.WriteString(". Suggested starting balance below.")
	return b.String()
}

// hottestNote names the stem most above the target loudness (the obvious first cut),
// e.g. "kick is 6 dB hot". Returns "" when nothing is meaningfully hot.
func hottestNote(rep *FolderReport) string {
	var hot *StemReport
	for i := range rep.Stems {
		s := &rep.Stems[i]
		if s.Skipped || s.NearSilent {
			continue
		}
		over := s.LoudnessDBFS - TargetLoudnessDBFS
		if over < 3 { // <3 dB over target isn't worth a headline call-out
			continue
		}
		if hot == nil || over > (hot.LoudnessDBFS-TargetLoudnessDBFS) {
			hot = s
		}
	}
	if hot == nil {
		return ""
	}
	over := int(math.Round(hot.LoudnessDBFS - TargetLoudnessDBFS))
	label := hot.Name
	if hot.Role != RoleUnknown {
		label = string(hot.Role)
	}
	return label + " is " + itoa(over) + " dB hot"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return itoa(n) + " " + many
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
