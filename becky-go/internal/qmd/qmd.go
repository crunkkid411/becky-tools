// Package qmd is the shared Stage-1 RECALL client for the local `qmd` hybrid search
// engine (BM25 + vector, Vulkan GPU). It shells the qmd CLI over its prebuilt index and
// returns candidate chunks; it never decides relevance (that is Stage 2, the judge).
// Two consumers use it: becky-review's smart search (cmd/clip) and the forensic judge
// (cmd/becky-judge). Read-only: it runs qmd and reads its stdout; it writes nothing.
//
// Robustness: Search tries the semantic HYBRID path first and falls back to BM25 on any
// failure (so a busy GPU degrades to keyword, never a blank). Env() FORCES the settings
// qmd needs (Vulkan backend + the shared index/config pins) when they are missing, so it
// works regardless of how the parent process was launched — see tools/qmd-index/README.md.
package qmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Hit is one entry of qmd's `--json` result array.
type Hit struct {
	Docid   string  `json:"docid"`
	Score   float64 `json:"score"`
	File    string  `json:"file"`    // qmd://transcripts/<slug>.md — a SLUG, not the disk path
	Title   string  `json:"title"`   // the source .srt stem (authoritative for mapping)
	Snippet string  `json:"snippet"` // markdown window: frontmatter (source:) and/or **[HH:MM:SS]** text
}

// Bin is the qmd executable (override with BECKY_QMD; defaults to "qmd" on PATH).
func Bin() string {
	if b := strings.TrimSpace(os.Getenv("BECKY_QMD")); b != "" {
		return b
	}
	return "qmd"
}

// Env returns the environment for the qmd child, FORCING what qmd needs to work the same
// no matter how the parent was launched (the user env vars exist, but a process started
// before they were pinned — or a fresh machine — won't inherit them):
//   - QMD_LLAMA_GPU=vulkan : qmd's CUDA backend hard-aborts on this box, so the hybrid
//     path crashes unless Vulkan is forced.
//   - XDG_CACHE_HOME / QMD_CONFIG_DIR / HOME : pin the ONE shared index + config dir so
//     qmd never falls back to an empty per-shell cache (the "0 results" bug).
//
// Each is only filled when MISSING, so an explicit user env value is respected.
func Env() []string {
	env := os.Environ()
	up := strings.TrimSpace(os.Getenv("USERPROFILE"))
	if up == "" {
		up = strings.TrimSpace(os.Getenv("HOME"))
	}
	defaults := [][2]string{
		{"QMD_LLAMA_GPU", "vulkan"},
		{"HOME", up},
		{"XDG_CACHE_HOME", filepath.Join(up, ".cache")},
		{"QMD_CONFIG_DIR", filepath.Join(up, ".config", "qmd")},
	}
	for _, kv := range defaults {
		if kv[1] != "" && !envHas(env, kv[0]) {
			env = append(env, kv[0]+"="+kv[1])
		}
	}
	return env
}

func envHas(env []string, key string) bool {
	p := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, p) && len(e) > len(p) {
			return true
		}
	}
	return false
}

// Search runs the smart search and returns (hits, mode, note). mode is "hybrid",
// "keyword" (BM25 fallback), or "unavailable". A blank query returns no hits.
func Search(query string) ([]Hit, string, string) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, "keyword", ""
	}
	bin := Bin()
	if hits, err := run(bin, 55*time.Second, "query", "--json", "--no-rerank", query, "-c", "transcripts"); err == nil && len(hits) > 0 {
		return hits, "hybrid", ""
	}
	if hits, err := run(bin, 25*time.Second, "search", "--json", query, "-c", "transcripts"); err == nil {
		return hits, "keyword", "smart (semantic) search was unavailable — showing keyword matches instead"
	}
	return nil, "unavailable", "qmd is not available (is it installed and indexed?)"
}

// run executes one qmd subcommand and parses the JSON array from its stdout. Stderr
// (progress + any crash trace) is discarded. Judged by parseable JSON, not exit code.
func run(bin string, timeout time.Duration, args ...string) ([]Hit, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = Env()
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	_ = cmd.Run()
	return ParseJSON(stdout.Bytes())
}

// ParseJSON extracts qmd's top-level JSON array from output that may carry leading
// progress text. It decodes from the first '[' and tolerates trailing bytes.
func ParseJSON(b []byte) ([]Hit, error) {
	i := bytes.IndexByte(b, '[')
	if i < 0 {
		return nil, fmt.Errorf("no qmd json in output")
	}
	var hits []Hit
	dec := json.NewDecoder(bytes.NewReader(b[i:]))
	if err := dec.Decode(&hits); err != nil {
		return nil, err
	}
	return hits, nil
}

var (
	sourceRe = regexp.MustCompile(`(?m)^\s*source:\s*"?([^"\r\n]+\.srt)"?`)
	// TcRe matches a markdown timecode marker: **[H:MM:SS]** (or without bold).
	TcRe = regexp.MustCompile(`\[(\d{1,2}):(\d{2}):(\d{2})\]`)
)

// SourceName resolves a hit's source .srt basename: the snippet frontmatter wins
// (authoritative), else the title + ".srt".
func SourceName(h Hit) string {
	if m := sourceRe.FindStringSubmatch(h.Snippet); m != nil {
		return strings.TrimSpace(m[1])
	}
	if t := strings.TrimSpace(h.Title); t != "" {
		return t + ".srt"
	}
	return ""
}

// FirstTimecode returns the first **[HH:MM:SS]** in the snippet as seconds, or -1.
func FirstTimecode(s string) float64 {
	m := TcRe.FindStringSubmatch(s)
	if m == nil {
		return -1
	}
	h, _ := strconv.Atoi(m[1])
	mn, _ := strconv.Atoi(m[2])
	sc, _ := strconv.Atoi(m[3])
	return float64(h*3600 + mn*60 + sc)
}

// CleanSnippet turns a raw qmd markdown snippet into a one-line preview: drop the
// "@@ ... @@" diff header, the YAML frontmatter, and the **[HH:MM:SS]** markers.
func CleanSnippet(s string) string {
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
	txt = TcRe.ReplaceAllString(txt, "")
	txt = strings.Join(strings.Fields(txt), " ")
	if len(txt) > 240 {
		txt = strings.TrimSpace(txt[:239]) + "…"
	}
	return txt
}
