package refmatch

// library.go turns a FOLDER of good-sounding studio stems into a reusable per-role
// "house sound" target — the measuring stick. For each WAV it asks stemscan what the
// stem IS (kick / bass / snare / vocal / ...) and asks Analyze what it SOUNDS like (a
// Profile), then groups by role. When several stems share a role (three different
// kicks across three sessions) it AVERAGES them into one target Profile so the house
// sound is the consensus of what already works, not one lucky take.
//
// AVERAGING (documented + deterministic): per-band energy and the scalar metrics
// (loudness, peak, crest, centroid) are averaged in the dB domain by a plain
// arithmetic mean across the contributing stems, in sorted-by-source order. The dB
// domain is the right place to average here because every downstream comparison
// (Match) works in dB and the corroborate-then-conclude thresholds are dB thresholds;
// averaging the human-meaningful dB numbers keeps the target on the same scale a
// producer reasons about. (We do NOT average linear power then re-log — that would let
// one loud take dominate the consensus, which is the opposite of "the house sound".)
// Degraded contributing profiles are still included but the role target is flagged
// degraded so the honesty propagates. Same folder in -> byte-identical library out.
//
// HONEST LIMITS (carried from Analyze): mono only (dsp downmixes), loudness is RMS
// dBFS (or a labelled K-weight approx), NOT certified LUFS. The library JSON repeats
// these caveats so a target lifted out of it is never mistaken for a calibrated curve.

import (
	"sort"

	"becky-go/internal/dsp"
	"becky-go/internal/stemscan"
)

// LibrarySchemaVersion versions the on-disk HouseSound JSON so a future format change
// is detectable.
const LibrarySchemaVersion = 1

// RoleTarget is the averaged "house sound" for one role: the consensus Profile to
// match a new stem of that role against, plus provenance (which stems built it).
type RoleTarget struct {
	Role              string   `json:"role"`               // stemscan role, e.g. "kick"
	Profile           Profile  `json:"profile"`            // the averaged target fingerprint
	ContributingStems []string `json:"contributing_stems"` // basenames, sorted, that fed the average
	StemCount         int      `json:"stem_count"`         // len(ContributingStems)
	Degraded          bool     `json:"degraded,omitempty"`
	Note              string   `json:"note,omitempty"` // honest caveats (RMS not LUFS, thin audio, etc.)
}

// HouseSound is the whole library: one averaged RoleTarget per role found in the
// folder, sorted by role name for deterministic output, plus the files that becky
// could not read (skipped-with-reason — degrade-never-crash, visible not vanished).
type HouseSound struct {
	SchemaVersion int           `json:"schema_version"`
	Dir           string        `json:"dir"`
	Roles         []RoleTarget  `json:"roles"`
	Skipped       []SkippedStem `json:"skipped,omitempty"`
	Note          string        `json:"note"` // global honesty banner (RMS not LUFS, mono only)
}

// SkippedStem records a file becky could not turn into a target, in plain language, so
// the producer sees it was there and why it was passed over.
type SkippedStem struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// libraryNote is the global honesty banner stamped on every library.
const libraryNote = "house sound is MONO (dsp downmixes); loudness is RMS dBFS (or labelled K-weight approx), NOT certified LUFS — good for relative matching, not compliance"

// StemInput is one candidate reference stem the caller read off disk. Err non-nil means
// the read itself failed; it is reported as skipped (degrade-never-crash). The caller
// does the file I/O so BuildLibrary stays pure and testable.
type StemInput struct {
	Name string // basename (provenance + filename role corroboration)
	Path string // path as given (may be a Windows path); used only for stable sorting
	Data []byte // WAV bytes
	Err  error  // non-nil if the read failed
}

// roleProfile pairs a contributing stem's name with its measured profile, for averaging.
type roleProfile struct {
	name string
	prof Profile
}

// BuildLibrary scans the provided reference stems, classifies each by role (stemscan),
// profiles each (Analyze), groups by role and averages each group into a RoleTarget.
// It is pure and deterministic: inputs are sorted by name first, roles are emitted in
// sorted order, and the average is a fixed arithmetic mean over the sorted contributors.
// Unreadable/short stems are skipped-with-reason, never fatal.
func BuildLibrary(dir string, stems []StemInput, opt Options) HouseSound {
	lib := HouseSound{SchemaVersion: LibrarySchemaVersion, Dir: dir, Note: libraryNote}

	// Sort by name (then path) FIRST so the average order — and thus the output — is
	// byte-identical regardless of OS directory iteration order.
	sorted := make([]StemInput, len(stems))
	copy(sorted, stems)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Name != sorted[j].Name {
			return sorted[i].Name < sorted[j].Name
		}
		return sorted[i].Path < sorted[j].Path
	})

	groups := map[string][]roleProfile{}
	for _, s := range sorted {
		if s.Err != nil {
			lib.Skipped = append(lib.Skipped, SkippedStem{Name: s.Name, Reason: "couldn't open file: " + s.Err.Error()})
			continue
		}
		audio, err := dsp.DecodeWAV(s.Data)
		if err != nil {
			lib.Skipped = append(lib.Skipped, SkippedStem{Name: s.Name, Reason: "couldn't read as WAV: " + err.Error()})
			continue
		}
		if len(audio.Samples) == 0 || audio.SampleRate <= 0 {
			lib.Skipped = append(lib.Skipped, SkippedStem{Name: s.Name, Reason: "too short to analyze (likely empty or truncated)"})
			continue
		}

		prof := Analyze(audio, opt)
		prof.Source = s.Name

		// stemscan owns the role heuristic; crest is the dynamics input it needs.
		role, _, _ := stemscan.ClassifyRole(audio.Samples, audio.SampleRate, s.Name, prof.CrestDB)
		groups[string(role)] = append(groups[string(role)], roleProfile{name: s.Name, prof: prof})
	}

	// Emit role targets in sorted role order for determinism.
	roleNames := make([]string, 0, len(groups))
	for r := range groups {
		roleNames = append(roleNames, r)
	}
	sort.Strings(roleNames)
	for _, r := range roleNames {
		lib.Roles = append(lib.Roles, averageRole(r, groups[r]))
	}
	return lib
}

// averageRole averages a group of same-role profiles into one RoleTarget. The members
// arrive already sorted by name (BuildLibrary sorts the inputs). Averaging is the
// arithmetic mean in the dB domain per band + per scalar metric (see file header for
// why dB-domain). Degradation propagates: if ANY contributor was degraded the target
// is flagged degraded.
func averageRole(role string, members []roleProfile) RoleTarget {
	rt := RoleTarget{Role: role, StemCount: len(members)}
	if len(members) == 0 {
		rt.Profile = Profile{Bands: zeroBands()}
		rt.Note = libraryNote
		return rt
	}

	names := make([]string, 0, len(members))
	for _, m := range members {
		names = append(names, m.name)
	}
	sort.Strings(names)
	rt.ContributingStems = names

	n := float64(len(members))
	avg := Profile{Bands: zeroBands(), Source: role}

	// Scalar metrics: arithmetic mean in dB. Sample rate is taken as the first member's
	// (they should match in a session; we don't average an integer rate).
	avg.SampleRate = members[0].prof.SampleRate
	var sumLoud, sumPeak, sumCrest, sumCentroid, sumDur float64
	anyKW := false
	for _, m := range members {
		sumLoud += m.prof.LoudnessDB
		sumPeak += m.prof.PeakDB
		sumCrest += m.prof.CrestDB
		sumCentroid += m.prof.CentroidHz
		sumDur += m.prof.DurationSec
		if m.prof.KWeighted {
			anyKW = true
		}
		if m.prof.Degraded {
			rt.Degraded = true
		}
	}
	avg.LoudnessDB = round1(sumLoud / n)
	avg.PeakDB = round1(sumPeak / n)
	avg.CrestDB = round1(sumCrest / n)
	avg.CentroidHz = round1(sumCentroid / n)
	avg.DurationSec = round1(sumDur / n)
	avg.KWeighted = anyKW

	// Per-band: average each band's dB across contributors. Bands are aligned by name
	// (BandByName) so a hand-built/loaded profile out of canonical order still averages
	// correctly. Missing bands (shouldn't happen — Analyze always emits the full set)
	// contribute the silence floor.
	for bi := range avg.Bands {
		name := avg.Bands[bi].Name
		var sum float64
		for _, m := range members {
			if b, ok := m.prof.BandByName(name); ok {
				sum += b.EnergyDB
			} else {
				sum += silenceFloorDB
			}
		}
		avg.Bands[bi].EnergyDB = round1(sum / n)
	}

	avg.Degraded = rt.Degraded
	rt.Profile = avg
	rt.Profile.Note = roleAveragingNote(len(members), rt.Degraded)
	rt.Note = rt.Profile.Note
	return rt
}

// roleAveragingNote builds the honest per-role caveat: how many stems averaged, plus
// the global RMS/mono banner and a degraded flag if any contributor was thin.
func roleAveragingNote(count int, degraded bool) string {
	base := libraryNote
	if count > 1 {
		base = "averaged from " + itoa(count) + " stems; " + base
	}
	if degraded {
		base = base + "; one or more contributing stems were thin/short — treat as approximate"
	}
	return base
}

// TargetForRole returns the averaged Profile for a role in the library and true, or a
// zero Profile and false when the library has no target for that role. Used by
// `becky-ref match --library` to auto-select by the role of YOUR stem.
func (h HouseSound) TargetForRole(role string) (Profile, bool) {
	for _, rt := range h.Roles {
		if rt.Role == role {
			return rt.Profile, true
		}
	}
	return Profile{}, false
}

// RoleNames returns the roles present in the library, sorted, for friendly error
// messages ("I have: bass, kick, vocal — but not 'snare-or-clap'").
func (h HouseSound) RoleNames() []string {
	out := make([]string, 0, len(h.Roles))
	for _, rt := range h.Roles {
		out = append(out, rt.Role)
	}
	sort.Strings(out)
	return out
}

// itoa is a tiny dependency-free int->string (the package already avoids extra deps).
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
