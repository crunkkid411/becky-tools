// Package datetri is the date-triangulation engine for becky-dates: it answers
// the forensic question "when was this captured?" per clip by combining several
// INDEPENDENT date signals (a container capture tag, the untrusted filesystem
// mtime, a filename date token, and an optional burned-in on-screen OCR date)
// and reaching a corroborated verdict.
//
// It implements FORENSIC-OUTPUT-PHILOSOPHY.md's TOP PRINCIPLE in code: a lone
// weak signal stays a candidate/unknown, but >=2 independent signals agreeing
// (or one real capture tag) yield a stated verdict. A sync-rewritten mtime that
// disagrees with a real capture tag is a NOTE, not a conflict.
//
// The engine is pure: it takes a []Signal and emits a Verdict. It does not read
// files, run exiftool/ffprobe, or call the OCR model — those live in the cmd
// layer and feed Signals in. That keeps the heart of the tool deterministic,
// offline, and fully unit-testable with no hardware.
package datetri

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Trust is the base weight of a single date signal.
type Trust string

const (
	TrustStrong Trust = "strong" // a real capture tag (exif/quicktime/ffprobe) or a high-confidence burned-in OCR date
	TrustMedium Trust = "medium" // a filename date token: usually survives copy, but is editable text
	TrustWeak   Trust = "weak"   // filesystem mtime, or a low-confidence OCR read — never a verdict alone
)

// Source labels the provenance of a signal. These are the only allowed values
// for Signal.Source; they map onto the JSON schema in SPEC-BECKY-DATES.md.
const (
	SourceEXIF      = "exif"             // EXIF DateTimeOriginal / CreateDate
	SourceQuickTime = "quicktime"        // QuickTime/MP4 mvhd creation_time
	SourceFFprobe   = "ffprobe"          // ffprobe container creation_time
	SourceFilename  = "filename"         // date token parsed from the basename
	SourceMTime     = "mtime(untrusted)" // filesystem modification time — NOT a capture time
	SourceOCR       = "ocr_burned_in"    // a date burned into the pixels, read by OCR
)

// Status is the verdict tag, mapping onto FORENSIC-OUTPUT-PHILOSOPHY.md's tags.
type Status string

const (
	StatusDocumented Status = "DOCUMENTED" // stated plainly: >=2 independent signals agree, or 1 strong tag
	StatusCandidate  Status = "CANDIDATE"  // weak-only best guess, surfaced for review
	StatusConflict   Status = "CONFLICT"   // >=2 comparable-trust clusters disagree; dissent named
	StatusUnknown    Status = "UNKNOWN"    // no trustworthy signal at all
)

// Signal is one independent observation of when a clip was captured.
type Signal struct {
	Source string    // one of the Source* constants
	Trust  Trust     // base weight
	Time   time.Time // the parsed instant; its calendar day is the unit of agreement
	// Raw is the original textual value of the signal (e.g. "20250704_181431",
	// "2024-03-01T00:00:00Z", "07/04/2025 6:14 PM") — carried for display.
	Raw string
	// OCRConfidence is set (>0) only for SourceOCR signals; it records the OCR
	// read confidence so the basis/notes can explain a weak OCR read.
	OCRConfidence float64
	// FrameTimestamp is set only for SourceOCR signals: the clip-relative time
	// (seconds) of the frame the date was read from.
	FrameTimestamp float64
	// TimePrecise is true when Time carries a meaningful wall-clock (not just a
	// day). A filename token like "20250704" with no HHMMSS is day-only.
	TimePrecise bool
}

// day returns the local calendar day of a signal, normalized to midnight, so two
// signals "agree" when they fall on the same day regardless of clock time.
func (s Signal) day() time.Time {
	t := s.Time
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// dateStr is the YYYY-MM-DD form of a signal's day.
func (s Signal) dateStr() string { return s.day().Format("2006-01-02") }

// independent reports whether the signal is treated as an independent source for
// corroboration counting. Every distinct Source is independent of every other;
// the caller is responsible for not passing two reads of the SAME tag as two
// signals (e.g. don't add both an exiftool quicktime read and an ffprobe read of
// the identical mvhd time as two — that is handled in the cmd layer).
func (s Signal) independent() bool { return true }

// strong reports whether this signal alone can carry a DOCUMENTED verdict.
func (s Signal) strong() bool { return s.Trust == TrustStrong }

// Conflict names two signals whose dates genuinely disagree.
type Conflict struct {
	A     string `json:"a"`
	ADate string `json:"a_date"`
	B     string `json:"b"`
	BDate string `json:"b_date"`
	Note  string `json:"note"`
}

// SignalView is the per-signal record emitted in the verdict (JSON-facing).
type SignalView struct {
	Source            string  `json:"source"`
	Trust             string  `json:"trust"`
	Date              string  `json:"date"`
	Value             string  `json:"value"`
	OCRConfidence     float64 `json:"ocr_confidence,omitempty"`
	FrameTimestamp    float64 `json:"frame_timestamp,omitempty"`
	AgreesWithVerdict bool    `json:"agrees_with_verdict"`
}

// Verdict is the triangulated answer for one clip.
type Verdict struct {
	VerdictDate    string       // YYYY-MM-DD, empty only for UNKNOWN
	VerdictTimeLoc string       // best wall-clock from the strongest agreeing signal, empty when day-only
	Status         Status       // DOCUMENTED | CANDIDATE | CONFLICT | UNKNOWN
	Confidence     float64      // 0..1
	Basis          string       // plain-English one-phrase reason
	SingleSignal   bool         // a lone strong tag concluded the verdict
	Signals        []SignalView // every signal that was considered
	Conflicts      []Conflict   // dissenting signals when status == CONFLICT
	Notes          []string     // degrade/provenance notes (incl. the mtime-disagreement note)
}

// cluster is a set of signals whose days agree within tolerance.
type cluster struct {
	day      time.Time
	signals  []Signal
	maxTrust Trust
}

// trustRank maps a Trust to a comparable number (higher = stronger).
func trustRank(t Trust) int {
	switch t {
	case TrustStrong:
		return 3
	case TrustMedium:
		return 2
	case TrustWeak:
		return 1
	default:
		return 0
	}
}

// Triangulate fuses the gathered signals into a single verdict using
// corroborate-then-conclude. toleranceDays is the calendar-day slop within which
// two signals are considered to agree (default 1 at the cmd layer).
//
// The verdict is deterministic: signals are sorted before clustering, ties are
// broken by trust then source name, so the same input always yields the same
// output.
func Triangulate(signals []Signal, toleranceDays int) Verdict {
	if toleranceDays < 0 {
		toleranceDays = 0
	}

	// No signal at all -> UNKNOWN. (mtime is always supplied by the cmd layer, so
	// this branch is mainly for the pure-engine case.)
	if len(signals) == 0 {
		return Verdict{
			Status:     StatusUnknown,
			Confidence: 0,
			Basis:      "no date signal of any kind was available",
			Signals:    nil,
		}
	}

	// Deterministic order: by day, then strongest trust first, then source name.
	ordered := make([]Signal, len(signals))
	copy(ordered, signals)
	sort.SliceStable(ordered, func(i, j int) bool {
		di, dj := ordered[i].day(), ordered[j].day()
		if !di.Equal(dj) {
			return di.Before(dj)
		}
		if ti, tj := trustRank(ordered[i].Trust), trustRank(ordered[j].Trust); ti != tj {
			return ti > tj
		}
		return ordered[i].Source < ordered[j].Source
	})

	clusters := buildClusters(ordered, toleranceDays)

	v := Verdict{Signals: nil}

	// Determine the dominant cluster (highest aggregate trust, then most members).
	dominant := pickDominant(clusters)

	// Detect a genuine conflict: another cluster of comparable trust exists that
	// is NOT explained by an untrusted-mtime disagreement.
	v.Conflicts, v.Notes = analyzeDissent(clusters, dominant)

	// Decide status from the dominant cluster's evidence.
	strongCount, indepCount := countCluster(dominant)

	switch {
	case len(v.Conflicts) > 0:
		v.Status = StatusConflict
		v.VerdictDate = dominant.day.Format("2006-01-02")
		v.VerdictTimeLoc = bestWallClock(dominant)
		v.Confidence = conflictConfidence(dominant, clusters)
		v.Basis = conflictBasis(dominant, v.Conflicts)
	case strongCount >= 1 && indepCount >= 2:
		// >=2 independent signals, at least one strong, agree.
		v.Status = StatusDocumented
		v.VerdictDate = dominant.day.Format("2006-01-02")
		v.VerdictTimeLoc = bestWallClock(dominant)
		v.Confidence = documentedConfidence(dominant)
		v.Basis = agreementBasis(dominant)
	case indepCount >= 2:
		// >=2 independent signals agree, but all medium/weak (e.g. filename +
		// mtime on the same day). Corroboration lifts this above a lone weak
		// guess, but with no real capture tag it stays a strong CANDIDATE.
		v.Status = StatusCandidate
		v.VerdictDate = dominant.day.Format("2006-01-02")
		v.VerdictTimeLoc = bestWallClock(dominant)
		v.Confidence = 0.6
		v.Basis = agreementBasis(dominant) + " (no container capture tag — candidate)"
	case strongCount >= 1:
		// A lone strong tag with no contradiction concludes (philosophy allows a
		// single strong signal to conclude).
		v.Status = StatusDocumented
		v.SingleSignal = true
		v.VerdictDate = dominant.day.Format("2006-01-02")
		v.VerdictTimeLoc = bestWallClock(dominant)
		v.Confidence = 0.85
		v.Basis = singleStrongBasis(dominant)
	case hasOnlyMTime(clusters):
		// Worst case: nothing but the untrusted mtime. Never a confident date.
		v.Status = StatusUnknown
		v.Confidence = 0
		v.Basis = "no trustworthy date signal: only the untrusted file mtime is available"
		v.Notes = append(v.Notes,
			"only the untrusted file mtime is available; run becky-ocr for a burned-in date, or treat as undated")
	default:
		// A lone medium/weak signal (filename token only, or a lone low-conf OCR
		// read) -> a surfaced candidate.
		v.Status = StatusCandidate
		v.VerdictDate = dominant.day.Format("2006-01-02")
		v.VerdictTimeLoc = bestWallClock(dominant)
		v.Confidence = 0.45
		v.Basis = weakBasis(dominant)
	}

	// Build the per-signal views (every signal, agreement flagged against the verdict).
	v.Signals = buildSignalViews(ordered, v.VerdictDate)
	return v
}

// buildClusters groups signals whose days are within toleranceDays of each other.
// A signal joins the first cluster whose representative day is within tolerance;
// because input is sorted by day this is a simple greedy sweep.
func buildClusters(ordered []Signal, toleranceDays int) []cluster {
	var clusters []cluster
	tol := time.Duration(toleranceDays) * 24 * time.Hour
	for _, s := range ordered {
		placed := false
		for i := range clusters {
			d := clusters[i].day.Sub(s.day())
			if d < 0 {
				d = -d
			}
			if d <= tol {
				clusters[i].signals = append(clusters[i].signals, s)
				if trustRank(s.Trust) > trustRank(clusters[i].maxTrust) {
					clusters[i].maxTrust = s.Trust
				}
				placed = true
				break
			}
		}
		if !placed {
			clusters = append(clusters, cluster{
				day:      s.day(),
				signals:  []Signal{s},
				maxTrust: s.Trust,
			})
		}
	}
	return clusters
}

// pickDominant returns the cluster with the highest aggregate strength: by max
// trust, then by independent-source count, then earliest day for determinism.
func pickDominant(clusters []cluster) cluster {
	if len(clusters) == 0 {
		return cluster{}
	}
	best := 0
	for i := 1; i < len(clusters); i++ {
		if dominates(clusters[i], clusters[best]) {
			best = i
		}
	}
	return clusters[best]
}

// dominates reports whether cluster a outranks cluster b.
func dominates(a, b cluster) bool {
	if ra, rb := trustRank(a.maxTrust), trustRank(b.maxTrust); ra != rb {
		return ra > rb
	}
	if na, nb := independentCount(a), independentCount(b); na != nb {
		return na > nb
	}
	// Tie -> the earlier day wins (stable, deterministic).
	return a.day.Before(b.day)
}

// analyzeDissent finds clusters that genuinely conflict with the dominant one.
// A non-dominant cluster is a CONFLICT only when it carries comparable trust
// (>=medium, and not weaker than the dominant by more than one rank) — a lone
// untrusted-mtime cluster that disagrees is recorded as a NOTE, never a conflict.
func analyzeDissent(clusters []cluster, dominant cluster) ([]Conflict, []string) {
	var conflicts []Conflict
	var notes []string
	for _, c := range clusters {
		if sameDay(c.day, dominant.day) {
			continue
		}
		// An mtime-only dissenting cluster is the classic sync-rewrite trap: note it.
		if isOnlyMTimeCluster(c) {
			notes = append(notes, fmt.Sprintf(
				"file mtime (%s) is UNTRUSTED (rewritten by copy/sync) and disagrees with the verdict — expected, not a conflict",
				c.day.Format("2006-01-02")))
			continue
		}
		// A real conflict: the dissenting cluster has medium+ trust comparable to
		// the dominant. (Weak-only non-mtime dissent — a lone low-conf OCR read —
		// is noted, not escalated.)
		if trustRank(c.maxTrust) >= trustRank(TrustMedium) {
			a, b := dominantRep(dominant), dominantRep(c)
			conflicts = append(conflicts, Conflict{
				A:     a.Source,
				ADate: dominant.day.Format("2006-01-02"),
				B:     b.Source,
				BDate: c.day.Format("2006-01-02"),
				Note:  conflictPairNote(a, b),
			})
		} else {
			notes = append(notes, fmt.Sprintf(
				"a weak %s signal reads %s, disagreeing with the verdict — noted, not treated as a conflict",
				dominantRep(c).Source, c.day.Format("2006-01-02")))
		}
	}
	return conflicts, notes
}

// conflictPairNote returns a plain explanation for a specific conflicting pair.
func conflictPairNote(a, b Signal) string {
	if (a.Source == SourceOCR) != (b.Source == SourceOCR) {
		return "container mux time vs burned-in overlay disagree by >1 day; a re-mux can reset creation_time"
	}
	return "two capture signals disagree by more than the tolerance — review the source"
}

// dominantRep returns the highest-trust representative signal of a cluster.
func dominantRep(c cluster) Signal {
	if len(c.signals) == 0 {
		return Signal{}
	}
	best := c.signals[0]
	for _, s := range c.signals[1:] {
		if trustRank(s.Trust) > trustRank(best.Trust) {
			best = s
		}
	}
	return best
}

// countCluster returns (strong-signal count, independent-source count) for a cluster.
func countCluster(c cluster) (strong, indep int) {
	seen := map[string]bool{}
	for _, s := range c.signals {
		if s.strong() {
			strong++
		}
		if s.independent() && !seen[s.Source] {
			seen[s.Source] = true
			indep++
		}
	}
	return strong, indep
}

// independentCount returns the number of distinct independent sources in a cluster.
func independentCount(c cluster) int {
	_, n := countCluster(c)
	return n
}

// bestWallClock returns the precise local wall-clock from the strongest signal in
// the cluster that carries one; empty when the verdict is day-only.
func bestWallClock(c cluster) string {
	var best *Signal
	for i := range c.signals {
		s := c.signals[i]
		if !s.TimePrecise {
			continue
		}
		if best == nil || trustRank(s.Trust) > trustRank(best.Trust) {
			cp := s
			best = &cp
		}
	}
	if best == nil {
		return ""
	}
	return best.Time.Format(time.RFC3339)
}

// hasOnlyMTime reports the worst case: every cluster is mtime-only.
func hasOnlyMTime(clusters []cluster) bool {
	for _, c := range clusters {
		if !isOnlyMTimeCluster(c) {
			return false
		}
	}
	return len(clusters) > 0
}

// isOnlyMTimeCluster reports whether a cluster contains nothing but mtime signals.
func isOnlyMTimeCluster(c cluster) bool {
	for _, s := range c.signals {
		if s.Source != SourceMTime {
			return false
		}
	}
	return len(c.signals) > 0
}

func sameDay(a, b time.Time) bool { return a.Equal(b) }

// --- confidence + basis helpers -------------------------------------------

func documentedConfidence(c cluster) float64 {
	// Base for a corroborated DOCUMENTED verdict; each extra independent source
	// nudges confidence up, capped at 0.98.
	conf := 0.9
	indep := independentCount(c)
	conf += 0.03 * float64(indep-2)
	if conf > 0.98 {
		conf = 0.98
	}
	return conf
}

func conflictConfidence(dominant cluster, all []cluster) float64 {
	// Conflicts are uncertain by definition; the verdict is the higher-trust side
	// but confidence is capped low so the reviewer reads it as contested.
	return 0.45
}

func agreementBasis(c cluster) string {
	srcs := distinctSources(c)
	return fmt.Sprintf("%s agree on %s", humanList(srcs), c.day.Format("2006-01-02"))
}

func singleStrongBasis(c cluster) string {
	s := dominantRep(c)
	return fmt.Sprintf("a lone container capture tag (%s) gives %s with no contradiction", s.Source, c.day.Format("2006-01-02"))
}

func weakBasis(c cluster) string {
	s := dominantRep(c)
	switch s.Source {
	case SourceFilename:
		return fmt.Sprintf("only the filename date token (%s) is available — candidate, not corroborated", c.day.Format("2006-01-02"))
	case SourceOCR:
		return fmt.Sprintf("only a low-confidence burned-in date (%s) is available — candidate", c.day.Format("2006-01-02"))
	default:
		return fmt.Sprintf("only a weak %s signal (%s) is available — candidate", s.Source, c.day.Format("2006-01-02"))
	}
}

func conflictBasis(dominant cluster, conflicts []Conflict) string {
	if len(conflicts) == 0 {
		return "signals conflict — REVIEW"
	}
	c := conflicts[0]
	return fmt.Sprintf("%s says %s but %s says %s — REVIEW", c.A, c.ADate, c.B, c.BDate)
}

// distinctSources returns the distinct source labels in a cluster, deterministically.
func distinctSources(c cluster) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range c.signals {
		if !seen[s.Source] {
			seen[s.Source] = true
			out = append(out, s.Source)
		}
	}
	sort.Strings(out)
	return out
}

// humanList joins source labels into a readable phrase: "a and b", "a, b and c".
func humanList(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	default:
		return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
	}
}

// buildSignalViews renders every considered signal, flagging agreement against
// the chosen verdict date.
func buildSignalViews(ordered []Signal, verdictDate string) []SignalView {
	out := make([]SignalView, 0, len(ordered))
	for _, s := range ordered {
		out = append(out, SignalView{
			Source:            s.Source,
			Trust:             string(s.Trust),
			Date:              s.dateStr(),
			Value:             s.Raw,
			OCRConfidence:     s.OCRConfidence,
			FrameTimestamp:    s.FrameTimestamp,
			AgreesWithVerdict: verdictDate != "" && s.dateStr() == verdictDate,
		})
	}
	return out
}
