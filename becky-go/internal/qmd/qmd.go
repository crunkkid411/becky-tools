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

// hybridTimeout / keywordTimeout bound Search's two attempts. The native UI's
// engineCall gives up waiting after a HARD 25s deadline ("engine timeout / no
// reply" — the exact failure Jordan hit once at 25014ms) and never sees a reply
// that lands after that, so both attempts COMBINED must fit comfortably inside
// it. Measured live on this machine: a structured hybrid query (see the doc
// comment on Search) takes ~5s; these budgets leave 3x headroom on hybrid and
// still land the whole round trip (worst case 15s+8s=23s) under the 25s wall.
const (
	hybridTimeout  = 15 * time.Second
	keywordTimeout = 8 * time.Second
)

// Search runs the smart search and returns (hits, mode, note). mode is "hybrid",
// "keyword" (BM25 fallback), or "unavailable". A blank query returns no hits.
//
// The hybrid attempt sends qmd a STRUCTURED "lex: <query>\nvec: <query>" query
// document instead of a bare query string. A bare query makes qmd run its LLM
// query-EXPANSION pass first (rewriting it into several lex/vec/hyde variants) —
// measured live at 12-14s on top of ~3s of embedding, 16.5-18.6s total, which is
// exactly Jordan's reported "16.6s typical, one 25s timeout" and blows past the
// UI's 25s wait. A structured document searches the literal query directly with
// no expansion pass: measured 4.9s, same hybrid (BM25+vector) engine, no
// measured quality loss on an exact-phrase forensic search.
func Search(query string) ([]Hit, string, string) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, "keyword", ""
	}
	bin := Bin()
	doc := "lex: " + query + "\nvec: " + query
	if hits, err := run(bin, hybridTimeout, "query", "--json", "--no-rerank", doc, "-c", "transcripts"); err == nil && len(hits) > 0 {
		return hits, "hybrid", ""
	}
	if hits, err := run(bin, keywordTimeout, "search", "--json", query, "-c", "transcripts"); err == nil {
		// Worded as fresh info, not a stuck error: this note rides NEXT TO a
		// successful result list, and "was unavailable" read like a dead banner
		// (2026-07-22 4AM driven verification, bug 4).
		return hits, "keyword", "these are keyword matches - smart matching didn't answer this time; search again to retry it"
	}
	return nil, "unavailable", "qmd is not available (is it installed and indexed?)"
}

// Warm pays as much of qmd's per-call model-load cost as safely possible,
// UP FRONT, in the background — right after a case folder opens, the same
// "pay the cost before Jordan's first keystroke" pattern as
// footage.WarmTranscriptCache. It cannot make a LATER qmd process start hot
// (each CLI invocation is its own process — there is no resident daemon to
// warm), but running one real structured hybrid query here pulls the
// embedding model's weights into the OS page cache, so the FIRST real search
// of a session reads them from RAM instead of cold disk. Best-effort and
// silent: the result is discarded, only the side effect matters, and a
// missing/slow qmd must never block folder opening (callers run this in a
// goroutine).
func Warm() {
	_, _, _ = Search("warmup")
}

// Update triggers `qmd update` (BM25 re-index) then `qmd embed` (vector
// embeddings for whatever `update` just found new/changed) over the SAME
// shared index Search() reads (Env() pins it). It is the write-side
// counterpart to Search: after internal/qmdindex writes new/changed .md
// locator files, the collection's index still needs qmd to notice them, or a
// fresh transcript stays invisible to search despite being on disk. Both
// steps are needed for FULL hybrid recall — `update` alone leaves a new
// transcript findable only via the lex/BM25 half of a query (Search's
// structured "lex:...\nvec:..." document), not the vector half — but a search
// still returns something on `update` alone, so a slow/failed `embed` degrades
// recall, it does not blank the result. Best-effort — a missing/failing qmd
// binary returns an error but never panics; callers that must not block on it
// (a transcribe completing) run this in a goroutine, the same fire-and-forget
// pattern as footage.WarmTranscriptCache. Measured live on the real
// E:\TakingBack2007 corpus: update+embed for a 113-doc backlog took ~27s
// combined — cheap relative to the judge stage that follows it.
func Update() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := runQmdSub(ctx, "update"); err != nil {
		return fmt.Errorf("qmd update: %w", err)
	}
	if err := runQmdSub(ctx, "embed"); err != nil {
		return fmt.Errorf("qmd embed: %w", err)
	}
	return nil
}

// runQmdSub runs one qmd maintenance subcommand (no JSON output expected) and
// returns its stderr tail on failure.
func runQmdSub(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, Bin(), args...)
	cmd.Env = Env()
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(errBuf.String()))
	}
	return nil
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
