package main

// qmd.go integrates the local `qmd` hybrid search engine (BM25 + vector, Vulkan GPU)
// as becky-review's optional "smart" transcript search. The .md corpus qmd indexes is
// a LOCATOR — it carries fewer timestamps than the real transcripts — so every qmd hit
// is resolved back to the actual .srt cue for the PRECISE timecode + text the detective
// plays/extracts (per becky-review-user-feedback2.md). Read-only: it shells qmd over its
// prebuilt index and reads .srt sidecars; it never writes.
//
// Robustness: it tries the semantic HYBRID path first (`qmd query`) and, on ANY failure
// (no parseable JSON — e.g. the GPU is busy and the embed step errors), falls back to the
// keyword BM25 path (`qmd search`). Either way the UI gets results + a mode note, so a
// GPU hiccup degrades to keyword search instead of a blank.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"becky-go/internal/footage"
	"becky-go/internal/sidecar"
)

// qmdHit is one entry of qmd's `--json` result array.
type qmdHit struct {
	Docid   string  `json:"docid"`
	Score   float64 `json:"score"`
	File    string  `json:"file"`    // qmd://transcripts/<slug>.md — a SLUG, not the disk path; don't use to resolve
	Title   string  `json:"title"`   // the source .srt stem (authoritative for mapping)
	Snippet string  `json:"snippet"` // markdown window: frontmatter (source:) and/or **[HH:MM:SS]** text
}

// QmdResult is the qmd_search reply: resolved results (same shape as keyword Search)
// plus which engine actually answered, so the UI can tell the user (hybrid vs keyword
// fallback vs unavailable).
type QmdResult struct {
	Results []SearchResult `json:"results"`
	Mode    string         `json:"mode"` // "hybrid" | "keyword" | "unavailable"
	Note    string         `json:"note,omitempty"`
}

// qmdEnv returns the environment for the qmd child process. qmd locates its index via
// $HOME; under Git Bash HOME is set, but a native-Windows parent (the WPF host, a
// double-clicked launcher) leaves HOME UNSET, so qmd reads an empty index and returns
// nothing. We set HOME=USERPROFILE when it's missing so qmd always finds its real
// index regardless of how becky was launched. An already-set HOME is respected.
func qmdEnv() []string {
	env := os.Environ()
	for _, e := range env {
		if strings.HasPrefix(e, "HOME=") && len(e) > len("HOME=") {
			return env // HOME already set — respect it
		}
	}
	if up := strings.TrimSpace(os.Getenv("USERPROFILE")); up != "" {
		env = append(env, "HOME="+up)
	}
	return env
}

// qmdBin is the qmd executable (override with BECKY_QMD; defaults to "qmd" on PATH).
func qmdBin() string {
	if b := strings.TrimSpace(os.Getenv("BECKY_QMD")); b != "" {
		return b
	}
	return "qmd"
}

// QmdSearch runs the smart transcript search and resolves every hit to a precise .srt
// cue. An empty query returns nothing (not an error).
func (a *App) QmdSearch(query string) QmdResult {
	query = strings.TrimSpace(query)
	if query == "" {
		return QmdResult{Results: []SearchResult{}, Mode: "keyword"}
	}
	hits, mode, note := qmdRun(qmdBin(), query)
	out := make([]SearchResult, 0, len(hits))
	seen := map[string]bool{}
	for _, h := range hits {
		r, ok := a.resolveQmdHit(h)
		if !ok || strings.TrimSpace(r.Text) == "" {
			continue // skip unresolvable or blank (frontmatter-only) hits — no useful row
		}
		key := r.Source + "|" + strconv.FormatFloat(math.Round(r.Start*10)/10, 'f', 1, 64)
		if seen[key] {
			continue // a window can resolve to the same cue as another — keep one
		}
		seen[key] = true
		out = append(out, r)
	}
	return QmdResult{Results: out, Mode: mode, Note: note}
}

// qmdRun tries the hybrid (semantic) path, then the BM25 keyword path. It judges
// success by PARSEABLE JSON, not the exit code, because qmd may print progress/crash
// noise and still exit 0 (or crash mid-run on the GPU). Returns (hits, mode, note).
func qmdRun(bin, query string) ([]qmdHit, string, string) {
	// Hybrid first. --no-rerank skips the reranker model (faster, one less GPU model).
	if hits, err := runQmd(bin, 55*time.Second, "query", "--json", "--no-rerank", query, "-c", "transcripts"); err == nil && len(hits) > 0 {
		return hits, "hybrid", ""
	}
	// Fallback: BM25 keyword (no GPU) — always works when the index is present.
	if hits, err := runQmd(bin, 25*time.Second, "search", "--json", query, "-c", "transcripts"); err == nil {
		return hits, "keyword", "smart (semantic) search was unavailable — showing keyword matches instead"
	}
	return nil, "unavailable", "qmd is not available (is it installed and indexed?)"
}

// runQmd executes one qmd subcommand and parses the JSON array from its stdout.
// Stderr (progress + any crash trace) is discarded. A timeout or unparseable output
// is an error so the caller can fall back.
func runQmd(bin string, timeout time.Duration, args ...string) ([]qmdHit, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = qmdEnv()
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	// cmd.Stderr left nil -> the null device (qmd's progress/crash noise is ignored).
	_ = cmd.Run() // judge by parseable JSON, not exit code
	return parseQmdJSON(stdout.Bytes())
}

// parseQmdJSON extracts qmd's top-level JSON array from output that may carry leading
// progress text. It decodes from the first '[' and tolerates trailing bytes.
func parseQmdJSON(b []byte) ([]qmdHit, error) {
	i := bytes.IndexByte(b, '[')
	if i < 0 {
		return nil, fmt.Errorf("no qmd json in output")
	}
	var hits []qmdHit
	dec := json.NewDecoder(bytes.NewReader(b[i:]))
	if err := dec.Decode(&hits); err != nil {
		return nil, err
	}
	return hits, nil
}

var (
	// frontmatter source line: source: "<stem>.srt"
	qmdSourceRe = regexp.MustCompile(`(?m)^\s*source:\s*"?([^"\r\n]+\.srt)"?`)
	// a markdown timecode marker: **[H:MM:SS]** (or without bold)
	qmdTcRe = regexp.MustCompile(`\[(\d{1,2}):(\d{2}):(\d{2})\]`)
)

// qmdSourceName resolves a hit's source .srt basename: the snippet frontmatter wins
// (authoritative), else the title + ".srt".
func qmdSourceName(h qmdHit) string {
	if m := qmdSourceRe.FindStringSubmatch(h.Snippet); m != nil {
		return strings.TrimSpace(m[1])
	}
	if t := strings.TrimSpace(h.Title); t != "" {
		return t + ".srt"
	}
	return ""
}

// firstQmdTimecode returns the first **[HH:MM:SS]** in the snippet as seconds, or -1.
func firstQmdTimecode(s string) float64 {
	m := qmdTcRe.FindStringSubmatch(s)
	if m == nil {
		return -1
	}
	h, _ := strconv.Atoi(m[1])
	mn, _ := strconv.Atoi(m[2])
	sc, _ := strconv.Atoi(m[3])
	return float64(h*3600 + mn*60 + sc)
}

// resolveQmdHit maps one qmd hit to a SearchResult: find the indexed video by its
// transcript name, then snap the snippet's coarse timecode to the PRECISE .srt cue.
// A hit whose video isn't in the open folder is returned as a transcript-only result
// (shown, not playable). false only when the source can't be determined at all.
func (a *App) resolveQmdHit(h qmdHit) (SearchResult, bool) {
	srtName := qmdSourceName(h)
	if srtName == "" {
		return SearchResult{}, false
	}
	t := firstQmdTimecode(h.Snippet)
	v, ok := a.videoByTranscript(srtName)
	if !ok {
		return SearchResult{
			Source:         "",
			Name:           strings.TrimSpace(h.Title),
			Start:          maxF(t, 0),
			Text:           cleanQmdSnippet(h.Snippet),
			Timecode:       mmss(maxF(t, 0)),
			Score:          h.Score,
			TranscriptOnly: true,
		}, true
	}
	start, end, text := a.resolveCue(v.TranscriptPath, t)
	if strings.TrimSpace(text) == "" {
		text = cleanQmdSnippet(h.Snippet) // degrade to the qmd window text
	}
	return SearchResult{
		Source:   v.Path,
		Name:     v.Name,
		Date:     v.Meta.Date,
		Start:    start,
		End:      end,
		Text:     text,
		Timecode: mmss(start),
		Score:    h.Score,
	}, true
}

// videoByTranscript finds the indexed video whose transcript sidecar basename matches
// srtName (case-insensitive). This is how a qmd .md hit reaches a playable video.
func (a *App) videoByTranscript(srtName string) (footage.Video, bool) {
	want := strings.ToLower(baseName(srtName))
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, v := range a.index.Videos {
		if v.HasTranscript && strings.ToLower(baseName(v.TranscriptPath)) == want {
			return v, true
		}
	}
	return footage.Video{}, false
}

// resolveCue parses the real .srt and returns the PRECISE cue nearest time t (the .md
// only keeps coarse timecodes, so we snap to the actual transcript). t<0 (no timecode
// in the snippet) returns the first cue. A missing/unparseable .srt degrades to t.
func (a *App) resolveCue(srtPath string, t float64) (float64, float64, string) {
	sub, err := sidecar.ParseSubtitle(srtPath)
	if err != nil || len(sub.Segments) == 0 {
		return maxF(t, 0), 0, ""
	}
	if t < 0 {
		s := sub.Segments[0]
		return s.Start, s.End, s.Text
	}
	best := sub.Segments[0]
	bestD := math.Abs(best.Start - t)
	for _, s := range sub.Segments {
		if d := math.Abs(s.Start - t); d < bestD {
			bestD, best = d, s
		}
	}
	return best.Start, best.End, best.Text
}

// cleanQmdSnippet turns a raw qmd markdown snippet into a one-line preview: drop the
// "@@ ... @@" diff header, the YAML frontmatter, and the **[HH:MM:SS]** markers.
func cleanQmdSnippet(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	var keep []string
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "@@") || ln == "---" {
			continue
		}
		if strings.HasPrefix(ln, "source:") || strings.HasPrefix(ln, "video_id:") || strings.HasPrefix(ln, "date:") || strings.HasPrefix(ln, "title:") {
			continue
		}
		keep = append(keep, ln)
	}
	txt := strings.Join(keep, " ")
	txt = strings.ReplaceAll(txt, "**", "")
	txt = qmdTcRe.ReplaceAllString(txt, "")
	txt = strings.Join(strings.Fields(txt), " ")
	if len(txt) > 240 {
		txt = strings.TrimSpace(txt[:239]) + "…"
	}
	return txt
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
