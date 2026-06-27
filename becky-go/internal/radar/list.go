// list.go — the "archive everything from my phone" feed.
//
// radar's default report is deliberately narrow: only model/tool pages that map
// to a tracked becky dependency. This file adds the complementary mode used by
// the iPhone-history -> markdown archiver: emit EVERY iPhone-synced page (Chrome
// visit_source = SYNCED) in a window, deduped, junk-filtered, and stably sorted,
// as a plain URL feed for becky-web2md. It is still offline + deterministic: a
// local SQLite read only, no network, same input -> same output.
package radar

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ListItem is one iPhone-synced page in the archive feed.
type ListItem struct {
	URL        string    `json:"url"`
	Title      string    `json:"title"`
	LastVisit  time.Time `json:"last_visit"`
	VisitCount int       `json:"visit_count"`
}

// ListReport is the deterministic --list output: every synced page worth
// archiving, plus a count of how many junk/redirect URLs were dropped.
type ListReport struct {
	Tool        string     `json:"tool"`
	Source      string     `json:"source"`
	Since       string     `json:"since"`
	Days        int        `json:"days"`
	Count       int        `json:"count"`
	FilteredOut int        `json:"filtered_out"`
	URLs        []ListItem `json:"urls"`
	Degraded    bool       `json:"degraded"`
	Note        string     `json:"note,omitempty"`
	Profiles    []string   `json:"profiles,omitempty"`
}

// SyncedSource yields visits whose Chrome visit_source is SYNCED (i.e. they came
// from another signed-in device — Jordan's iPhone). ChromeSource implements it;
// tests inject a synthetic source so they need no Chrome/DB/network.
type SyncedSource interface {
	SyncedVisits(since time.Time) ([]Visit, error)
}

// BuildList assembles the archive feed: pull synced visits, dedupe, junk-filter
// (when clean is true), and sort (most recent first, URL tie-break). A source
// error degrades to an empty feed with a plain-language note — never a crash.
func BuildList(src SyncedSource, source string, days int, since time.Time, clean bool, profiles []string) ListReport {
	rep := ListReport{
		Tool:     "becky-radar " + Version,
		Source:   source,
		Since:    since.UTC().Format(time.RFC3339),
		Days:     days,
		URLs:     []ListItem{},
		Profiles: profiles,
	}
	visits, err := src.SyncedVisits(since)
	if err != nil {
		rep.Degraded = true
		rep.Note = "couldn't read your iPhone Chrome history: " + err.Error() +
			" — is Chrome installed and signed in (with sync on) on this PC?"
		return rep
	}
	for _, v := range dedupeByURL(visits) {
		if clean && !IsArchivable(v.URL) {
			rep.FilteredOut++
			continue
		}
		rep.URLs = append(rep.URLs, ListItem{
			URL: v.URL, Title: v.Title, LastVisit: v.LastVisit, VisitCount: v.VisitCount,
		})
	}
	sort.SliceStable(rep.URLs, func(i, j int) bool {
		if !rep.URLs[i].LastVisit.Equal(rep.URLs[j].LastVisit) {
			return rep.URLs[i].LastVisit.After(rep.URLs[j].LastVisit)
		}
		return rep.URLs[i].URL < rep.URLs[j].URL
	})
	rep.Count = len(rep.URLs)
	return rep
}

// junkHosts are hosts whose ROOT/search pages are navigation noise, not content
// worth saving as a standalone markdown note.
var junkHosts = map[string]bool{
	"google.com": true, "www.google.com": true, "duckduckgo.com": true,
	"bing.com": true, "www.bing.com": true, "youtube.com": true, "www.youtube.com": true,
	"l.facebook.com": true, "lm.facebook.com": true, "facebook.com": true,
	"accounts.google.com": true,
}

// IsArchivable reports whether a synced URL is a real content page worth turning
// into a markdown file (vs a redirect, search, login, or tracking hop). Pure and
// conservative: it only drops URLs it is confident are noise.
func IsArchivable(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	hostname := strings.ToLower(u.Hostname())
	host := strings.TrimPrefix(hostname, "www.")
	if host == "" {
		return false
	}
	low := strings.ToLower(raw)
	// Redirect hops (Chrome stores the redirector URL, not the destination).
	if strings.Contains(u.Path, "/redirect") || strings.Contains(low, "redir_token") ||
		strings.Contains(low, "/url?") {
		return false
	}
	// Ad/tracking hosts.
	if strings.HasSuffix(host, "doubleclick.net") || strings.HasPrefix(host, "ad.") {
		return false
	}
	// Bare roots / search pages of known navigation hosts.
	if junkHosts[hostname] || junkHosts[host] {
		p := strings.TrimRight(u.Path, "/")
		if p == "" || p == "/search" || strings.Contains(u.Path, "search") {
			return false
		}
	}
	return true
}

// SyncedVisits returns iPhone-synced visits (visit_source = SYNCED) at/after
// since across all configured Chrome History DBs. A single unreadable DB is
// skipped (degrade, never crash); only when every DB fails does it error.
func (c ChromeSource) SyncedVisits(since time.Time) ([]Visit, error) {
	if len(c.DBPaths) == 0 {
		return nil, fmt.Errorf("no Chrome History database found")
	}
	var all []Visit
	var lastErr error
	opened := 0
	for _, p := range c.DBPaths {
		vs, err := readChromeSyncedDB(p, since)
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

// readChromeSyncedDB copies one History DB to a temp file, opens it read-only,
// and extracts synced visits since the cutoff. The temp copy is always removed.
func readChromeSyncedDB(dbPath string, since time.Time) (visits []Visit, err error) {
	tmp, err := copyToTemp(dbPath)
	if err != nil {
		return nil, fmt.Errorf("copy history db %q: %w", filepath.Base(dbPath), err)
	}
	defer os.Remove(tmp)

	dsn := "file:" + filepath.ToSlash(tmp) + "?mode=ro&immutable=1"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open history copy: %w", err)
	}
	defer db.Close()
	return querySyncedVisits(db, since)
}

// querySyncedVisits joins urls -> visits -> visit_source, keeping only SYNCED
// (source = 0) visits at/after since. One row per URL (most recent synced visit).
func querySyncedVisits(db *sql.DB, since time.Time) ([]Visit, error) {
	cutoff := int64(0)
	if !since.IsZero() {
		cutoff = toChromeMicros(since.UTC())
	}
	const q = `SELECT u.url, u.title, MAX(v.visit_time) AS mvt, u.visit_count
	           FROM urls u
	           JOIN visits v ON v.url = u.id
	           JOIN visit_source vs ON vs.id = v.id
	           WHERE vs.source = 0 AND v.visit_time >= ?
	           GROUP BY u.url
	           ORDER BY mvt DESC`
	rows, err := db.Query(q, cutoff)
	if err != nil {
		return nil, fmt.Errorf("query synced visits: %w", err)
	}
	defer rows.Close()

	var out []Visit
	for rows.Next() {
		var u, title string
		var micros int64
		var count int
		if err := rows.Scan(&u, &title, &micros, &count); err != nil {
			return nil, fmt.Errorf("scan synced row: %w", err)
		}
		out = append(out, Visit{URL: u, Title: title, LastVisit: chromeTime(micros), VisitCount: count})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate synced rows: %w", err)
	}
	return out, nil
}
