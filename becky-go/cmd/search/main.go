// becky-search — natural-language semantic search over indexed footage.
//
//	becky-search <query> [--db forensic.db] [--limit 10] [--min-confidence 0.5]
//	             [--format json|txt] [--expand] [--verbose]
//
// It embeds the query string with the SAME model becky-embed indexed with
// (Qwen3-Embedding-0.6B, 1024-dim, L2-normalized) via the shared embed_text.py
// helper, then runs a KNN search through the shared beckydb layer (sqlite-vec
// vec0, cosine). Results are mapped into a forensic-friendly, human-readable
// shape: ranked by similarity, with named speakers (or "unidentified speaker"
// when not yet enriched), confidence scores, and timestamps.
//
// Deterministic retrieval only — NO LLM. JSON to stdout; diagnostics to stderr;
// exit 0 on success (including the no-results case), exit 1 on error.
package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"becky-go/internal/beckydb"
	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/pyhelpers"
)

// unidentifiedSpeaker is the human-facing label for a segment whose speaker has
// not been resolved yet (speaker_name NULL/empty). Jordan's rule: "talk the way
// humans talk" — never surface a raw "Speaker 1" diarization label.
const unidentifiedSpeaker = "unidentified speaker"

// modelIDs maps the friendly --model name to the helper id/label. Must match
// becky-embed so the query is embedded in the SAME vector space the DB indexed.
//   - qwen3-4b : resident llama-server (Qwen3-Embedding-4B), MRL→1024 + L2-norm.
//   - qwen3-0.6b : in-process sentence-transformers fallback.
var modelIDs = map[string]string{
	"qwen3-0.6b": "Qwen/Qwen3-Embedding-0.6B",
	"qwen3-4b":   "Qwen/Qwen3-Embedding-4B",
}

// usesServer reports whether a friendly model name routes through the resident
// llama-server (vs the in-process sentence-transformers path).
func usesServer(modelName string) bool {
	return strings.ToLower(modelName) == "qwen3-4b"
}

// embedResult mirrors embed_text.py's stdout contract (shared with becky-embed).
type embedResult struct {
	Skipped bool        `json:"skipped"`
	Reason  string      `json:"reason"`
	Model   string      `json:"model"`
	Dim     int         `json:"dim"`
	Vectors [][]float64 `json:"vectors"`
}

// result is one ranked search hit in the becky-search output contract. It carries
// both spoken-transcript hits and on-screen OCR hits in ONE ranked list. Kind tells
// them apart ("transcript" | "ocr"); the OCR-only fields (FramePath, FrameIndex,
// OCRConfidence, Category, BBox) are populated only for kind=="ocr", and the
// speaker/duration fields are populated only for transcript hits. This way an
// on-screen address surfaces in the same results[] as a spoken phrase, each labelled
// by source via Matched ("ocr" vs "hybrid"/"vector"/"keyword").
type result struct {
	Rank              int      `json:"rank"`
	Kind              string   `json:"kind"` // "transcript" | "ocr"
	SourceFile        string   `json:"source_file"`
	SourceSHA256      string   `json:"source_sha256"`
	Timestamp         float64  `json:"timestamp"`
	Duration          float64  `json:"duration"`
	Speaker           string   `json:"speaker"`
	SpeakerConfidence float64  `json:"speaker_confidence"`
	Text              string   `json:"text"`
	Similarity        float64  `json:"similarity"`
	Matched           string   `json:"matched"`     // which signal hit: hybrid|vector|keyword|ocr
	FusedScore        float64  `json:"fused_score"` // RRF fused score (the cross-source comparable scale)
	NeedsReview       bool     `json:"needs_review"`
	VerifiedBy        *string  `json:"verified_by"`
	Context           []ctxSeg `json:"context,omitempty"` // populated only with --expand (transcript hits)

	// OCR-hit provenance (populated only when Kind == "ocr"). FramePath is the exact
	// frame image OCR'd; Timestamp reuses the shared field above (Duration is 0 for a
	// frame); OCRConfidence is the 0..1 recognition confidence; Category is the cheap
	// candidate_* triage hint; BBox is [x1,y1,x2,y2] on the frame as JSON.
	FramePath     string  `json:"frame_path,omitempty"`
	FrameIndex    int     `json:"frame_index,omitempty"`
	OCRConfidence float64 `json:"ocr_confidence,omitempty"`
	Category      string  `json:"category,omitempty"`
	BBox          string  `json:"bbox,omitempty"`
}

// ctxSeg is one neighboring segment included for context under --expand.
type ctxSeg struct {
	Timestamp float64 `json:"timestamp"`
	Duration  float64 `json:"duration"`
	Speaker   string  `json:"speaker"`
	Text      string  `json:"text"`
}

// stats summarizes the result set. TranscriptHits/OCRHits split the unified list by
// source so a caller can see at a glance how many hits came from spoken words vs
// on-screen text. AvgConfidence is the mean cosine similarity across TRANSCRIPT
// hits only (OCR hits have no cosine; their recognition confidence is per-row).
type stats struct {
	TotalResults   int     `json:"total_results"`
	TranscriptHits int     `json:"transcript_hits"`
	OCRHits        int     `json:"ocr_hits"`
	NamedSpeakers  int     `json:"named_speakers"`
	Unidentified   int     `json:"unidentified"`
	AvgConfidence  float64 `json:"avg_confidence"` // mean similarity across transcript results
}

// output is the becky-search stdout JSON contract.
type output struct {
	Query   string   `json:"query"`
	DB      string   `json:"db"`
	Mode    string   `json:"mode"` // requested retrieval mode: hybrid|vector|keyword
	Results []result `json:"results"`
	Stats   stats    `json:"stats"`
	Note    string   `json:"note,omitempty"` // e.g. graceful degrade to vector-only when FTS5 absent
}

func main() {
	dbPath := flag.String("db", "forensic.db", "SQLite database path")
	limit := flag.Int("limit", 10, "max results")
	minConf := flag.Float64("min-confidence", 0.5, "minimum similarity (0..1) to keep a vector result")
	format := flag.String("format", "json", "output format: json, txt")
	mode := flag.String("mode", "hybrid", "retrieval mode: hybrid, vector, keyword")
	modelName := flag.String("model", "qwen3-4b", "query embedding model: qwen3-4b (resident server, default), qwen3-0.6b (in-process)")
	serverURL := flag.String("server-url", "", "resident embedding server URL (default from config; qwen3-4b only)")
	rrfK := flag.Int("rrf-k", 60, "Reciprocal Rank Fusion constant (hybrid mode; higher = flatter)")
	expand := flag.Bool("expand", false, "include neighboring segments for context")
	verbose := flag.Bool("verbose", false, "show progress on stderr")

	query := parsePositional()
	if strings.TrimSpace(query) == "" {
		beckyio.Fatalf("usage: becky-search <query> [options]")
	}
	if *limit <= 0 {
		beckyio.Fatalf("--limit must be positive (got %d)", *limit)
	}
	if *format != "json" && *format != "txt" {
		beckyio.Fatalf("--format must be json or txt (got %q)", *format)
	}
	*mode = strings.ToLower(strings.TrimSpace(*mode))
	if *mode != "hybrid" && *mode != "vector" && *mode != "keyword" {
		beckyio.Fatalf("--mode must be hybrid, vector or keyword (got %q)", *mode)
	}
	if *rrfK <= 0 {
		beckyio.Fatalf("--rrf-k must be positive (got %d)", *rrfK)
	}
	modelKey := strings.ToLower(strings.TrimSpace(*modelName))
	if _, ok := modelIDs[modelKey]; !ok {
		beckyio.Fatalf("unknown model %q (use qwen3-4b or qwen3-0.6b)", *modelName)
	}
	// Note: a missing or empty DB is NOT a fatal error. EnsureSchema below creates
	// the canonical tables (sqlite3 makes the file lazily), so an empty/new DB
	// yields valid JSON with empty results[] and exit 0 rather than a crash.

	cfg := config.Load()
	srvURL := *serverURL
	if srvURL == "" {
		srvURL = cfg.EmbedServerURL
	}

	db, err := beckydb.Open(cfg, *dbPath)
	if err != nil {
		beckyio.Fatalf("open db: %v", err)
	}
	// EnsureSchema makes the tool robust against an empty/new DB file: it creates
	// the canonical tables so KNN returns zero rows instead of erroring.
	if err := db.EnsureSchema(); err != nil {
		beckyio.Fatalf("ensure schema: %v", err)
	}

	// Correctness guard: the query must be embedded with the SAME model the DB was
	// indexed with. Cosine across two different embedding spaces is meaningless, so
	// a mismatch FAILS loudly (never returns silent garbage). An untagged DB (empty
	// / pre-existing) skips the check — there are no vectors to be incompatible with.
	indexedModel, err := db.GetEmbedModel()
	if err != nil {
		beckyio.Fatalf("read indexed model tag: %v", err)
	}
	if indexedModel != "" && indexedModel != modelKey {
		beckyio.Fatalf("db %s was indexed with model %q but query model is %q; refusing to compare different vector spaces. Re-run with --model %s (or re-index the DB).",
			*dbPath, indexedModel, modelKey, indexedModel)
	}

	// Retrieve per mode. retrieve() returns the UNIFIED ranked items (transcript
	// segments AND on-screen OCR lines fused into one ranking, each tagged with its
	// matched signal + fused score), the effective mode actually run, and a note when
	// it had to degrade (e.g. FTS5 absent → vector-only).
	ranked, effMode, note := retrieve(db, cfg, query, retrieveParams{
		mode: *mode, limit: *limit, minConf: *minConf, rrfK: *rrfK, verbose: *verbose,
		model: modelKey, serverURL: srvURL,
	})
	beckyio.Logf(*verbose, "mode=%s returned %d result(s)", effMode, len(ranked))

	results := buildResults(db, ranked, *expand, *verbose)
	out := output{
		Query:   query,
		DB:      *dbPath,
		Mode:    effMode,
		Results: results,
		Stats:   buildStats(results),
		Note:    note,
	}

	if *format == "txt" {
		fmt.Print(renderTxt(out))
		return
	}
	beckyio.PrintJSON(out)
}

// buildResults maps the (already fused/ranked) UNIFIED items into the ranked output
// contract, re-numbering rank 1..N across BOTH sources. Each item is either a
// transcript segment (kind="transcript") or an on-screen OCR line (kind="ocr"),
// rendered by buildSegResult / buildOCRResult respectively. The interleaved order is
// the fused-score ranking decided in retrieve(), so an address read off a frame can
// sit between two spoken hits. Optionally attaches neighboring-segment context when
// --expand is set (transcript hits only).
func buildResults(db *beckydb.DB, items []rankedItem, expand, verbose bool) []result {
	results := make([]result, 0, len(items))
	for i, it := range items {
		var r result
		switch it.kind {
		case kindOCR:
			r = buildOCRResult(it)
		default:
			r = buildSegResult(db, it, expand, verbose)
		}
		r.Rank = i + 1
		results = append(results, r)
	}
	return results
}

// buildSegResult maps one transcript-segment item into a result row. Similarity is
// the vector cosine when present (0 for a keyword-only hit); Matched is the fused
// signal label ("hybrid"/"vector"/"keyword"); FusedScore is the RRF score.
func buildSegResult(db *beckydb.DB, it rankedItem, expand, verbose bool) result {
	n := it.seg
	sig := it.matched
	if sig == "" {
		sig = "vector"
	}
	r := result{
		Kind:              "transcript",
		SourceFile:        n.SourceFile,
		SourceSHA256:      n.SourceSHA256,
		Timestamp:         n.StartTime,
		Duration:          n.EndTime - n.StartTime,
		Speaker:           speakerLabel(n.SpeakerName),
		SpeakerConfidence: n.SpeakerConfidence,
		Text:              n.Text,
		Similarity:        n.Similarity,
		Matched:           sig,
		FusedScore:        round6(it.fused),
		NeedsReview:       n.NeedsReview != 0,
		VerifiedBy:        nilIfEmpty(n.VerifiedBy),
	}
	if expand {
		r.Context = contextFor(db, n, verbose)
	}
	return r
}

// buildOCRResult maps one on-screen OCR line into a result row, tagged matched="ocr".
// It carries the FRAME provenance a reviewer needs to verify the read: the source
// file + SHA, the timestamp into the video, the exact frame image path + index, the
// recognized text, the 0..1 recognition confidence, the candidate_* category, and
// the bbox. Similarity is 0 (cosine is meaningless for a literal frame-text hit);
// the cross-source comparable rank lives in FusedScore. Per the forensic philosophy
// the candidate_* category is plainly a triage hint, never a conclusion.
func buildOCRResult(it rankedItem) result {
	h := it.ocr
	return result{
		Kind:          "ocr",
		SourceFile:    h.SourceFile,
		SourceSHA256:  h.SourceSHA256,
		Timestamp:     h.Timestamp,
		Speaker:       "", // not applicable to on-screen text
		Text:          h.Text,
		Similarity:    0,
		Matched:       "ocr",
		FusedScore:    round6(it.fused),
		FramePath:     h.FramePath,
		FrameIndex:    h.FrameIndex,
		OCRConfidence: h.Confidence,
		Category:      h.Category,
		BBox:          h.BBoxJSON,
	}
}

// contextFor fetches the immediately adjacent segments (same source) around a
// hit for best-effort context. Failures are non-fatal: context is a convenience.
func contextFor(db *beckydb.DB, n beckydb.Neighbor, verbose bool) []ctxSeg {
	segs, err := db.NeighborSegments(n.SourceSHA256, n.SegmentID, 1)
	if err != nil {
		beckyio.Logf(verbose, "warn: context lookup failed for %s: %v", n.SegmentID, err)
		return nil
	}
	if len(segs) == 0 {
		return nil
	}
	ctx := make([]ctxSeg, 0, len(segs))
	for _, s := range segs {
		ctx = append(ctx, ctxSeg{
			Timestamp: s.StartTime,
			Duration:  s.EndTime - s.StartTime,
			Speaker:   speakerLabel(s.SpeakerName),
			Text:      s.Text,
		})
	}
	return ctx
}

// buildStats computes the summary block over the UNIFIED result set. It splits hits
// by source (TranscriptHits/OCRHits), counts named vs unidentified speakers among
// TRANSCRIPT hits only (OCR text has no speaker), and reports avg_confidence as the
// mean cosine similarity across transcript hits only (OCR hits carry no cosine — a
// per-row recognition confidence is in ocr_confidence instead). avg is 0 when there
// are no transcript hits.
func buildStats(results []result) stats {
	st := stats{TotalResults: len(results)}
	var simSum float64
	for _, r := range results {
		if r.Kind == "ocr" {
			st.OCRHits++
			continue
		}
		st.TranscriptHits++
		simSum += r.Similarity
		if r.Speaker == unidentifiedSpeaker {
			st.Unidentified++
		} else {
			st.NamedSpeakers++
		}
	}
	if st.TranscriptHits > 0 {
		st.AvgConfidence = round6(simSum / float64(st.TranscriptHits))
	}
	return st
}

// speakerLabel returns the resolved speaker name, or the human-facing
// "unidentified speaker" label when the name is empty (not yet enriched).
func speakerLabel(name string) string {
	if strings.TrimSpace(name) == "" {
		return unidentifiedSpeaker
	}
	return name
}

// nilIfEmpty maps an empty verified_by to JSON null (matches the spec example,
// where unverified rows show "verified_by": null).
func nilIfEmpty(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}

// embedQuery embeds a single query string with the SAME model becky-embed
// indexed with, returning its 1024-dim normalized vector. Queries use the Qwen3
// "Instruct/Query" prefix (--mode query) — the asymmetry vs documents (raw text)
// is what lifts retrieval. For qwen3-4b it routes through the resident
// llama-server (--server-url, MRL-truncate to 1024 + L2-norm); for qwen3-0.6b it
// runs the in-process sentence-transformers path. It reuses the embedded
// embed_text.py helper verbatim (single-element batch).
func embedQuery(cfg config.Config, query, model, serverURL string, verbose bool) ([]float64, error) {
	script, err := pyhelpers.Materialize("embed_text.py", pyhelpers.EmbedText)
	if err != nil {
		return nil, fmt.Errorf("materialize embed helper: %w", err)
	}

	payload, err := json.Marshal([]string{query})
	if err != nil {
		return nil, fmt.Errorf("marshal query: %w", err)
	}
	b64 := base64.StdEncoding.EncodeToString(payload)

	args := []string{script,
		"--texts-b64", b64,
		"--model", modelIDs[model],
		"--batch-size", "1",
		"--mode", "query", // queries get the Instruct/Query prefix
	}
	if usesServer(model) {
		if serverURL == "" {
			return nil, fmt.Errorf("qwen3-4b needs an embedding server URL (set --server-url or config.embed_server_url)")
		}
		args = append(args, "--server-url", serverURL)
		args = append(args, "--truncate-dim", strconv.Itoa(beckydb.VectorDim))
	} else {
		if cfg.EmbedModelCache != "" {
			args = append(args, "--cache-dir", cfg.EmbedModelCache)
		}
		if cfg.Device != "" {
			args = append(args, "--device", cfg.Device)
		}
	}

	cmd := exec.Command(cfg.Python, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	if verbose {
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stderr = &stderr
	}
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("embed helper failed: %v\n%s", err, tail(stderr.String()))
	}

	res, ok := parseEmbedJSON(stdout.String())
	if !ok {
		return nil, fmt.Errorf("could not parse embed helper output:\n%s", tail(stdout.String()))
	}
	if res.Skipped {
		if usesServer(model) {
			return nil, fmt.Errorf("qwen3-4b embedding server unavailable: %s\nstart it with X:\\AI-2\\becky-tools\\start-embed-server.bat (no fallback to 0.6B — different vector space)", res.Reason)
		}
		return nil, fmt.Errorf("query embedding skipped: %s", res.Reason)
	}
	if len(res.Vectors) != 1 {
		return nil, fmt.Errorf("expected 1 query vector, got %d", len(res.Vectors))
	}
	if res.Dim != beckydb.VectorDim || len(res.Vectors[0]) != beckydb.VectorDim {
		return nil, fmt.Errorf("query embedding dim %d != indexed dim %d (model mismatch)",
			len(res.Vectors[0]), beckydb.VectorDim)
	}
	return res.Vectors[0], nil
}

// parseEmbedJSON tolerates leading log noise by scanning lines bottom-up for the
// first that unmarshals into the expected shape (same as becky-embed).
func parseEmbedJSON(s string) (embedResult, bool) {
	if r, ok := tryUnmarshal(strings.TrimSpace(s)); ok {
		return r, true
	}
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if r, ok := tryUnmarshal(line); ok {
			return r, true
		}
	}
	return embedResult{}, false
}

func tryUnmarshal(s string) (embedResult, bool) {
	var r embedResult
	if json.Unmarshal([]byte(s), &r) == nil && (r.Skipped || r.Dim > 0 || len(r.Vectors) > 0) {
		return r, true
	}
	return embedResult{}, false
}

// parsePositional takes the first positional arg (the query), then re-parses
// flags that came after it (Go's flag package stops at the first non-flag).
// Mirrors becky-embed/becky-transcribe so flag ordering is consistent.
func parsePositional() string {
	flag.Parse()
	rest := flag.Args()
	if len(rest) == 0 {
		return ""
	}
	first := rest[0]
	if len(rest) > 1 {
		_ = flag.CommandLine.Parse(rest[1:])
	}
	return first
}

// vecJSON renders a vector as a compact JSON array for sqlite-vec MATCH.
func vecJSON(v []float64) string {
	parts := make([]string, len(v))
	for i, x := range v {
		parts[i] = strconv.FormatFloat(x, 'g', -1, 64)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// round6 rounds to 6 decimals for stable, readable stat output.
func round6(f float64) float64 {
	return float64(int64(f*1e6+0.5)) / 1e6
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 800 {
		return s[len(s)-800:]
	}
	return s
}
