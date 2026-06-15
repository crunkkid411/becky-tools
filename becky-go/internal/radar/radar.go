// Package radar turns "things Jordan flagged in Chrome" into becky action.
//
// Jordan does his discovery on his phone, deliberately in Chrome (not Safari) so
// that the model cards / repos / papers he opens land in his Chrome history — his
// de-facto "becky look at this" queue. That queue syncs to the desktop Chrome
// History SQLite DB on the same Google account, so it is already on his PC and
// fully readable offline. This package extracts recent visits, classifies which
// ones name a model/tool, and cross-references becky's freshness manifest so a
// flagged improvement (the PP-OCRv6 miss) is surfaced automatically.
//
// Everything here is offline and deterministic: it reads a LOCAL file only, and
// every slice it returns is stably sorted (last-visit desc, then URL). The
// SQLite read is hidden behind the HistorySource interface so the matching and
// report logic can be unit-tested on synthetic rows with no Chrome, DB, or net.
package radar

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registered as "sqlite"

	"becky-go/internal/freshness"
	"becky-go/internal/pathx"
)

// Visit is one row extracted from a Chrome History `urls` table, normalized.
type Visit struct {
	URL        string    `json:"url"`
	Title      string    `json:"title"`
	LastVisit  time.Time `json:"last_visit"`
	VisitCount int       `json:"visit_count"`
}

// HistorySource yields the visits to consider. The real Chrome SQLite reader
// (ChromeSource, below) implements this; tests inject a synthetic source so they
// need no real browser, DB, or network.
type HistorySource interface {
	Visits(since time.Time) ([]Visit, error)
}

// BeckyMatch is attached to an item whose host+name maps to a tracked becky
// dependency — the corroborated case (two signals: model host AND manifest name).
type BeckyMatch struct {
	DependencyID string   `json:"dependency_id"`
	Name         string   `json:"name"`
	UsedBy       []string `json:"used_by"`
	BeckyPinned  string   `json:"becky_pinned"`
	Verdict      string   `json:"verdict"`
}

// Item is one classified visit in the report.
type Item struct {
	Visit
	Host       string      `json:"host"`
	Class      string      `json:"class"` // hf-model | github-repo | pypi | model-keyword
	BeckyMatch *BeckyMatch `json:"becky_match,omitempty"`
}

// Report is the full deterministic output of a radar run.
type Report struct {
	Tool     string   `json:"tool"`
	Source   string   `json:"source"`
	Since    string   `json:"since"`
	Days     int      `json:"days"`
	Flagged  []Item   `json:"flagged"`        // mapped to a tracked becky dependency (corroborated)
	Seen     []Item   `json:"seen"`           // model/tool sites visited, no manifest hit (candidates)
	Note     string   `json:"note,omitempty"` // plain-language degrade note when source was unreadable
	Degraded bool     `json:"degraded"`
	Profiles []string `json:"profiles,omitempty"` // which Chrome profiles were read
}

// Version is the reported tool version string.
const Version = "v1.0.0"

// modelToolHosts is the conservative built-in set of hosts that publish models
// or tools. A visit only counts as a candidate when its host is in here.
var modelToolHosts = map[string]string{
	"huggingface.co": "hf-model",
	"github.com":     "github-repo",
	"pypi.org":       "pypi",
	"ollama.com":     "model-keyword",
	"ollama.ai":      "model-keyword",
	"arxiv.org":      "model-keyword",
	"modelscope.cn":  "hf-model",
	"kaggle.com":     "model-keyword",
}

// modelKeywords mark a URL/title as clearly naming a model/tool even when the
// host's path alone isn't conclusive (e.g. arxiv abstracts, ollama tags).
var modelKeywords = []string{
	"ocr", "asr", "embedding", "diariz", "vlm", "llama", "whisper",
	"parakeet", "insightface", "sherpa", "qwen", "gemma", "onnx",
	"transformer", "diffusion", "rapidocr", "paddleocr",
}

// Classify returns (host, class, true) when v is a model/tool visit worth
// considering, else ("", "", false). Conservative: host must be a known
// model/tool host. Keyword-gated hosts additionally need a model keyword.
func Classify(v Visit) (string, string, bool) {
	host := hostOf(v.URL)
	if host == "" {
		return "", "", false
	}
	class, ok := modelToolHosts[host]
	if !ok {
		return "", "", false
	}
	// Repo/model hosts always qualify on host alone (path is the model/repo).
	if class == "hf-model" || class == "github-repo" || class == "pypi" {
		return host, class, true
	}
	// Keyword-gated hosts (arxiv, ollama, kaggle) need a model keyword to qualify.
	if mentionsModelKeyword(v.URL + " " + v.Title) {
		return host, "model-keyword", true
	}
	return "", "", false
}

// Build runs the full pipeline: pull visits, classify, cross-reference the
// freshness manifest, and assemble a stably-sorted report. A source error never
// crashes — it degrades to an empty report with a plain-language note.
func Build(src HistorySource, deps []freshness.Dependency, source string, days int, since time.Time, profiles []string) Report {
	rep := Report{
		Tool:     "becky-radar " + Version,
		Source:   source,
		Since:    since.UTC().Format(time.RFC3339),
		Days:     days,
		Flagged:  []Item{},
		Seen:     []Item{},
		Profiles: profiles,
	}
	visits, err := src.Visits(since)
	if err != nil {
		rep.Degraded = true
		rep.Note = "couldn't read Chrome history: " + err.Error() +
			" — nothing to report. Is Chrome installed and signed in on this PC?"
		return rep
	}
	for _, v := range dedupeByURL(visits) {
		host, class, ok := Classify(v)
		if !ok {
			continue
		}
		item := Item{Visit: v, Host: host, Class: class}
		if m := matchDependency(v, deps); m != nil {
			item.BeckyMatch = m
			rep.Flagged = append(rep.Flagged, item)
		} else {
			rep.Seen = append(rep.Seen, item)
		}
	}
	sortItems(rep.Flagged)
	sortItems(rep.Seen)
	return rep
}

// matchDependency returns a BeckyMatch when v plausibly names a tracked becky
// dependency: the manifest entry's upstream ref (or name) appears in the
// URL/title. This is the corroborated, two-signal case (already on a model host).
func matchDependency(v Visit, deps []freshness.Dependency) *BeckyMatch {
	hay := strings.ToLower(v.URL + " " + v.Title)
	for _, d := range deps {
		if refMatches(d, hay) {
			return &BeckyMatch{
				DependencyID: d.ID,
				Name:         d.Name,
				UsedBy:       d.UsedBy,
				BeckyPinned:  d.Pinned,
				Verdict: "UPGRADE CANDIDATE — you viewed something matching a tool/model " +
					"becky already tracks (" + strings.Join(d.UsedBy, ", ") + "). Compare to what becky pins.",
			}
		}
	}
	return nil
}

// refMatches reports whether dependency d is named in the lower-cased haystack.
// It checks the upstream ref's basename (e.g. "PaddleOCR-VL" from the HF ref) and
// any meaningful word of the dependency name, using pathx.Base so a ref that
// looks like an "org/repo" path is split correctly on any OS.
func refMatches(d freshness.Dependency, hay string) bool {
	if base := strings.ToLower(pathx.Base(d.Upstream.Ref)); base != "" && strings.Contains(hay, base) {
		return true
	}
	for _, w := range strings.Fields(strings.ToLower(d.Name)) {
		if len(w) >= 5 && strings.Contains(hay, w) {
			return true
		}
	}
	return false
}

// hostOf returns the lower-cased host of a URL without scheme/port/"www.",
// without importing net/url (the inputs are simple http(s) URLs).
func hostOf(raw string) string {
	s := raw
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, '@'); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.IndexByte(s, ':'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimPrefix(strings.ToLower(s), "www.")
}

// mentionsModelKeyword reports whether s contains any model/tool keyword.
func mentionsModelKeyword(s string) bool {
	low := strings.ToLower(s)
	for _, k := range modelKeywords {
		if strings.Contains(low, k) {
			return true
		}
	}
	return false
}

// dedupeByURL keeps one Visit per URL (the one with the most recent LastVisit,
// summing visit counts), so repeated visits don't bloat the report.
func dedupeByURL(visits []Visit) []Visit {
	byURL := make(map[string]Visit, len(visits))
	for _, v := range visits {
		prev, ok := byURL[v.URL]
		if !ok {
			byURL[v.URL] = v
			continue
		}
		merged := prev
		merged.VisitCount = prev.VisitCount + v.VisitCount
		if v.LastVisit.After(prev.LastVisit) {
			merged.LastVisit = v.LastVisit
			if v.Title != "" {
				merged.Title = v.Title
			}
		}
		byURL[v.URL] = merged
	}
	out := make([]Visit, 0, len(byURL))
	for _, v := range byURL {
		out = append(out, v)
	}
	return out
}

// sortItems orders items stably: most recent visit first, then URL ascending.
func sortItems(items []Item) {
	sort.SliceStable(items, func(i, j int) bool {
		if !items[i].LastVisit.Equal(items[j].LastVisit) {
			return items[i].LastVisit.After(items[j].LastVisit)
		}
		return items[i].URL < items[j].URL
	})
}

// chromeToUnixOffsetSec is the number of seconds between Chrome's epoch
// (1601-01-01 UTC) and the Unix epoch (1970-01-01 UTC).
const chromeToUnixOffsetSec = 11644473600

// chromeTime converts a Chrome last_visit_time (microseconds since 1601-01-01
// UTC) into a UTC time.Time. A zero/negative value (never-visited) maps to the
// zero time. We can't add the full 1601->now span via time.Duration (int64 ns
// overflows at ~292 years), so we rebase onto the Unix epoch and use time.Unix,
// which takes seconds + nanoseconds directly and has no such limit.
func chromeTime(micros int64) time.Time {
	if micros <= 0 {
		return time.Time{}
	}
	unixMicros := micros - chromeToUnixOffsetSec*1_000_000
	secs := unixMicros / 1_000_000
	nsec := (unixMicros % 1_000_000) * 1_000
	return time.Unix(secs, nsec).UTC()
}

// ChromeSource reads visits from one or more on-disk Chrome History DB files.
// Each DB is locked while Chrome runs, so it is copied to a temp file and opened
// read-only; the temp copy is removed before returning. Implements HistorySource.
type ChromeSource struct {
	DBPaths []string // absolute paths to Chrome "History" SQLite files
}

// Visits returns model/tool-relevant visits at or after since across all DBs.
// A single unreadable DB is skipped (degrade, never crash); only when every DB
// fails AND none could be opened does it return an error.
func (c ChromeSource) Visits(since time.Time) ([]Visit, error) {
	if len(c.DBPaths) == 0 {
		return nil, fmt.Errorf("no Chrome History database found")
	}
	var all []Visit
	var lastErr error
	opened := 0
	for _, p := range c.DBPaths {
		vs, err := readChromeDB(p, since)
		if err != nil {
			lastErr = err
			continue
		}
		opened++
		all = append(all, vs...)
	}
	if opened == 0 {
		return nil, fmt.Errorf("could not read any Chrome History database: %w", lastErr)
	}
	return all, nil
}

// readChromeDB copies one History DB to a temp file, opens it read-only, and
// extracts visits since the cutoff. The temp copy is always cleaned up.
func readChromeDB(dbPath string, since time.Time) (visits []Visit, err error) {
	tmp, err := copyToTemp(dbPath)
	if err != nil {
		return nil, fmt.Errorf("copy history db %q: %w", pathx.Base(dbPath), err)
	}
	defer os.Remove(tmp)

	dsn := "file:" + filepath.ToSlash(tmp) + "?mode=ro&immutable=1"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open history copy: %w", err)
	}
	defer db.Close()
	return queryVisits(db, since)
}

// toChromeMicros is the inverse of chromeTime: a UTC time -> Chrome's
// microseconds-since-1601. Rebased through the Unix epoch to avoid the same
// int64-nanosecond overflow that affects a direct Sub against the 1601 epoch.
func toChromeMicros(t time.Time) int64 {
	return t.Unix()*1_000_000 + int64(t.Nanosecond())/1_000 + chromeToUnixOffsetSec*1_000_000
}

// queryVisits reads the urls table and returns visits at/after since.
func queryVisits(db *sql.DB, since time.Time) ([]Visit, error) {
	cutoff := int64(0)
	if !since.IsZero() {
		cutoff = toChromeMicros(since.UTC())
	}
	const q = `SELECT url, title, last_visit_time, visit_count
	           FROM urls WHERE last_visit_time >= ? ORDER BY last_visit_time DESC`
	rows, err := db.Query(q, cutoff)
	if err != nil {
		return nil, fmt.Errorf("query urls: %w", err)
	}
	defer rows.Close()

	var out []Visit
	for rows.Next() {
		var url, title string
		var micros int64
		var count int
		if err := rows.Scan(&url, &title, &micros, &count); err != nil {
			return nil, fmt.Errorf("scan url row: %w", err)
		}
		out = append(out, Visit{URL: url, Title: title, LastVisit: chromeTime(micros), VisitCount: count})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate url rows: %w", err)
	}
	return out, nil
}

// copyToTemp copies src to a fresh temp file and returns its path. Never touches
// the live DB beyond a read-only file copy.
func copyToTemp(src string) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	tmp, err := os.CreateTemp("", "becky-radar-history-*.db")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

// DiscoverDBs returns the History DB paths to read for the given profile filter.
// userDataDir is %LOCALAPPDATA%\Google\Chrome\User Data. When profile is "" it
// scans "Default" plus any "Profile N" directory that has a History file;
// otherwise only the named profile. Returns (paths, profileNames).
func DiscoverDBs(userDataDir, profile string) ([]string, []string) {
	candidates := []string{"Default"}
	if profile != "" {
		candidates = []string{profile}
	} else {
		if entries, err := os.ReadDir(userDataDir); err == nil {
			for _, e := range entries {
				if e.IsDir() && strings.HasPrefix(e.Name(), "Profile ") {
					candidates = append(candidates, e.Name())
				}
			}
		}
	}
	sort.Strings(candidates)
	var paths, names []string
	for _, name := range candidates {
		p := filepath.Join(userDataDir, name, "History")
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			paths = append(paths, p)
			names = append(names, name)
		}
	}
	return paths, names
}

// DefaultUserDataDir returns the Chrome "User Data" directory from LOCALAPPDATA
// (Windows). Empty when LOCALAPPDATA is unset (e.g. on Linux/CI).
func DefaultUserDataDir() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		return ""
	}
	return filepath.Join(base, "Google", "Chrome", "User Data")
}
