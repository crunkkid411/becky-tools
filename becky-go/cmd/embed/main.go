// becky-embed — semantic embeddings for transcript segments, stored in SQLite +
// sqlite-vec for KNN search.
//
//	becky-embed <transcript_json> [--db forensic.db] [--model qwen3-0.6b]
//	            [--batch-size N] [--source <video>] [--device cpu|cuda] [--verbose]
//
// It reads becky-transcribe's JSON, embeds each non-empty segment's text with
// Qwen3-Embedding-0.6B (1024-dim, normalized) via the embedded embed_text.py
// helper, then writes a segment row + its vector into the shared beckydb schema.
// A JSON summary goes to stdout; diagnostics go to stderr; exit 0 on success.
//
// Re-runs are idempotent: segment_id is deterministic (sha12(source) + index),
// segments use INSERT OR REPLACE, and vectors are DELETE+INSERT.
package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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
	"becky-go/internal/mediainfo"
	"becky-go/internal/osintexport"
	"becky-go/internal/pyhelpers"
)

// transcript is the subset of becky-transcribe's JSON that becky-embed consumes.
type transcript struct {
	File     string    `json:"file"`
	Duration float64   `json:"duration"`
	Model    string    `json:"model"`
	Language string    `json:"language"`
	Segments []segment `json:"segments"`
}

// segment mirrors one becky-transcribe segment: {start, end, text}.
type segment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

// embedResult mirrors embed_text.py's stdout contract.
type embedResult struct {
	Skipped bool        `json:"skipped"`
	Reason  string      `json:"reason"`
	Model   string      `json:"model"`
	Dim     int         `json:"dim"`
	Vectors [][]float64 `json:"vectors"`
}

// summary is the becky-embed stdout JSON contract.
type summary struct {
	DB               string `json:"db"`
	SourceFile       string `json:"source_file"`
	SourceSHA256     string `json:"source_sha256"`
	Model            string `json:"model"`     // helper's model id/label (e.g. Qwen/Qwen3-Embedding-4B)
	ModelTag         string `json:"model_tag"` // friendly tag stored in the DB (qwen3-4b | qwen3-0.6b)
	Dim              int    `json:"dim"`
	SegmentsTotal    int    `json:"segments_total"`
	SegmentsEmbedded int    `json:"segments_embedded"`
	SegmentsSkipped  int    `json:"segments_skipped"` // empty-text segments
	SegmentsInDB     int    `json:"segments_in_db"`
	VectorsInDB      int    `json:"vectors_in_db"`
	FTSInDB          int    `json:"fts_in_db"`      // FTS5 keyword-index rows (0 if FTS5 unavailable)
	FTS5Available    bool   `json:"fts5_available"` // whether the keyword index was populated
}

// modelIDs maps the friendly --model name to the label/id passed to the helper.
//   - qwen3-4b : served by the resident llama-server (Qwen3-Embedding-4B GGUF),
//     MRL-truncated to 1024 + L2-normalized client-side. This is the DEFAULT.
//   - qwen3-0.6b : the in-process sentence-transformers fallback (different
//     vector space — never mix the two in one DB).
var modelIDs = map[string]string{
	"qwen3-0.6b": "Qwen/Qwen3-Embedding-0.6B",
	"qwen3-4b":   "Qwen/Qwen3-Embedding-4B",
}

// usesServer reports whether a friendly --model name routes through the resident
// llama-server (vs the in-process sentence-transformers path).
func usesServer(modelName string) bool {
	return strings.ToLower(modelName) == "qwen3-4b"
}

func main() {
	dbPath := flag.String("db", "forensic.db", "SQLite database path")
	modelName := flag.String("model", "qwen3-4b", "embedding model: qwen3-4b (resident server, default), qwen3-0.6b (in-process)")
	batchSize := flag.Int("batch-size", 32, "embedding batch size")
	source := flag.String("source", "", "source video for SHA-256 provenance (optional)")
	device := flag.String("device", "", "device: cpu, cuda (default from config)")
	serverURL := flag.String("server-url", "", "resident embedding server URL (default from config; qwen3-4b only)")
	verbose := flag.Bool("verbose", false, "show progress on stderr")

	input := parsePositional()
	if input == "" {
		beckyio.Fatalf("usage: becky-embed <transcript_json> [options]")
	}
	if _, err := os.Stat(input); err != nil {
		beckyio.Fatalf("transcript not found: %s", input)
	}

	cfg := config.Load()
	dev := cfg.Device
	if *device != "" {
		dev = *device
	}

	tr, err := readTranscript(input)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}
	beckyio.Logf(*verbose, "read %d segments from %s", len(tr.Segments), input)

	// Resolve source provenance: prefer an explicit --source video (hash it),
	// else fall back to the transcript's own file field.
	sourceFile := tr.File
	if *source != "" {
		sourceFile = *source
	}
	sourceSHA := ""
	if *source != "" {
		if _, statErr := os.Stat(*source); statErr == nil {
			beckyio.Logf(*verbose, "hashing source video for provenance: %s", *source)
			sourceSHA, err = osintexport.SHA256File(*source)
			if err != nil {
				beckyio.Fatalf("hash source: %v", err)
			}
		} else {
			beckyio.Fatalf("--source not found: %s", *source)
		}
	} else {
		// No video given: derive a stable id from the transcript's file field so
		// segment_ids stay deterministic across re-runs of the same transcript.
		sourceSHA = sha12(sourceFile)
	}

	// Collect non-empty segment texts to embed; remember their indices so we can
	// map vectors back to the right rows.
	var texts []string
	var idxs []int
	for i, s := range tr.Segments {
		if strings.TrimSpace(s.Text) == "" {
			continue
		}
		texts = append(texts, s.Text)
		idxs = append(idxs, i)
	}
	if len(texts) == 0 {
		beckyio.Fatalf("no non-empty segments to embed in %s", input)
	}

	modelKey := strings.ToLower(*modelName)
	modelID, ok := modelIDs[modelKey]
	if !ok {
		beckyio.Fatalf("unknown model %q (use qwen3-4b or qwen3-0.6b)", *modelName)
	}

	// Build embed options. qwen3-4b routes through the resident llama-server
	// (Qwen3-Embedding-4B GGUF), embedding DOCUMENTS as raw text and MRL-truncating
	// to the schema's 1024 dims + L2-normalizing. qwen3-0.6b stays in-process.
	opts := embedOpts{
		modelID:   modelID,
		batchSize: *batchSize,
		device:    dev,
		mode:      "document", // becky-embed indexes documents (no query prefix)
	}
	if usesServer(modelKey) {
		opts.serverURL = *serverURL
		if opts.serverURL == "" {
			opts.serverURL = cfg.EmbedServerURL
		}
		if opts.serverURL == "" {
			beckyio.Fatalf("qwen3-4b needs an embedding server URL (set --server-url or config.embed_server_url)")
		}
		opts.truncateDim = beckydb.VectorDim // MRL-truncate native 2560 -> 1024
		beckyio.Logf(*verbose, "embedding %d documents via resident server %s (model=%s, truncate=%d)...",
			len(texts), opts.serverURL, modelID, opts.truncateDim)
	} else {
		beckyio.Logf(*verbose, "embedding %d documents in-process with %s (device=%s, batch=%d)...",
			len(texts), modelID, dev, *batchSize)
	}

	res, err := runEmbed(cfg, texts, opts, *verbose)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}
	if res.Skipped {
		// For the server path a skip almost always means the server is down: make
		// the remedy explicit and NEVER silently fall back to the 0.6B model (it
		// is a different vector space, which would corrupt the index).
		if usesServer(modelKey) {
			beckyio.Fatalf("qwen3-4b embedding server unavailable: %s\nstart it with X:\\AI-2\\becky-tools\\start-embed-server.bat (no fallback to 0.6B — different vector space)", res.Reason)
		}
		beckyio.Fatalf("embedding skipped: %s", res.Reason)
	}
	if res.Dim != beckydb.VectorDim {
		beckyio.Fatalf("embedding dim %d != expected %d (schema mismatch)", res.Dim, beckydb.VectorDim)
	}
	if len(res.Vectors) != len(texts) {
		beckyio.Fatalf("got %d vectors for %d texts", len(res.Vectors), len(texts))
	}

	// Open the shared DB and ensure the canonical schema exists.
	db, err := beckydb.Open(cfg, *dbPath)
	if err != nil {
		beckyio.Fatalf("open db: %v", err)
	}
	if err := db.EnsureSchema(); err != nil {
		beckyio.Fatalf("ensure schema: %v", err)
	}

	// Model-tag guard: refuse to mix vector spaces. If this DB was already indexed
	// with a DIFFERENT embedding model, its segments_vec lives in another space and
	// cosine across the two is meaningless — fail loudly rather than corrupt it.
	existingModel, err := db.GetEmbedModel()
	if err != nil {
		beckyio.Fatalf("read indexed model tag: %v", err)
	}
	if existingModel != "" && existingModel != modelKey {
		beckyio.Fatalf("db %s is already indexed with model %q; refusing to add %q (different vector space). Use a fresh --db or re-index everything with one model.",
			*dbPath, existingModel, modelKey)
	}
	if err := db.SetEmbedModel(modelKey); err != nil {
		beckyio.Fatalf("record model tag: %v", err)
	}

	// Probe FTS5 once: if this sqlite3 build supports it we also populate the
	// segments_fts keyword index (the keyword half of becky-search's hybrid
	// retrieval). If not, EnsureSchema already degraded gracefully and we simply
	// index vectors only — embedding still succeeds.
	ftsOK := db.FTS5Available()
	if !ftsOK {
		beckyio.Logf(*verbose, "warn: FTS5 not available in this sqlite3 build; keyword index will be empty (vector-only)")
	}

	// Record media provenance when we have a real source video.
	if *source != "" {
		info, probeErr := mediainfo.Probe(cfg.FFprobe, *source)
		if probeErr == nil {
			if mErr := db.UpsertMedia(sourceFile, sourceSHA, info.Duration, info.FPS); mErr != nil {
				beckyio.Logf(*verbose, "warn: upsert media failed: %v", mErr)
			}
		} else {
			beckyio.Logf(*verbose, "warn: probe source failed: %v", probeErr)
		}
	}

	// Write each segment row + its vector. segment_id is deterministic so re-runs
	// overwrite the same rows rather than duplicating them.
	embedded := 0
	for vi, segIdx := range idxs {
		s := tr.Segments[segIdx]
		segID := segmentID(sourceSHA, segIdx)
		row := beckydb.Segment{
			SegmentID:    segID,
			SourceFile:   sourceFile,
			SourceSHA256: sourceSHA,
			StartTime:    s.Start,
			EndTime:      s.End,
			Text:         s.Text,
			// speaker_* / verified_by left empty; identify+consolidate enrich later.
			NeedsReview: 1,
		}
		if err := db.UpsertSegment(row); err != nil {
			beckyio.Fatalf("upsert segment %s: %v", segID, err)
		}
		if err := db.InsertVector(segID, vecJSON(res.Vectors[vi])); err != nil {
			beckyio.Fatalf("insert vector %s: %v", segID, err)
		}
		// Populate the FTS5 keyword index alongside the vector (delete-then-insert
		// keeps re-runs idempotent — FTS5 has no upsert). Skipped on a no-FTS5
		// build so embedding still succeeds (search degrades to vector-only).
		if ftsOK {
			if err := db.InsertFTS(segID, s.Text); err != nil {
				beckyio.Fatalf("insert fts %s: %v", segID, err)
			}
		}
		embedded++
	}
	beckyio.Logf(*verbose, "wrote %d segments + vectors%s to %s", embedded,
		ftsCol(ftsOK), *dbPath)

	segCount, err := db.CountSegments()
	if err != nil {
		beckyio.Fatalf("count segments: %v", err)
	}
	vecCount, err := db.CountVectors()
	if err != nil {
		beckyio.Fatalf("count vectors: %v", err)
	}
	ftsCount, err := db.CountFTS()
	if err != nil {
		beckyio.Fatalf("count fts: %v", err)
	}

	beckyio.PrintJSON(summary{
		DB:               *dbPath,
		SourceFile:       sourceFile,
		SourceSHA256:     sourceSHA,
		Model:            res.Model,
		ModelTag:         modelKey,
		Dim:              res.Dim,
		SegmentsTotal:    len(tr.Segments),
		SegmentsEmbedded: embedded,
		SegmentsSkipped:  len(tr.Segments) - len(texts),
		SegmentsInDB:     segCount,
		VectorsInDB:      vecCount,
		FTSInDB:          ftsCount,
		FTS5Available:    ftsOK,
	})
}

// ftsCol returns " + fts" when the keyword index was populated, for the verbose
// progress line, else "" (vector-only build).
func ftsCol(ftsOK bool) string {
	if ftsOK {
		return " + fts"
	}
	return ""
}

// parsePositional mirrors becky-transcribe: take the first positional arg, then
// re-parse flags that came after it (Go's flag stops at the first non-flag).
func parsePositional() string {
	flag.Parse()
	rest := flag.Args()
	if len(rest) == 0 {
		return ""
	}
	input := rest[0]
	if len(rest) > 1 {
		_ = flag.CommandLine.Parse(rest[1:])
	}
	return input
}

func readTranscript(path string) (transcript, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return transcript{}, fmt.Errorf("read transcript: %w", err)
	}
	var tr transcript
	if err := json.Unmarshal(data, &tr); err != nil {
		return transcript{}, fmt.Errorf("parse transcript json: %w", err)
	}
	return tr, nil
}

// embedOpts bundles the knobs runEmbed passes through to embed_text.py. A
// non-empty serverURL routes to the resident llama-server (qwen3-4b); otherwise
// the in-process sentence-transformers path runs (qwen3-0.6b).
type embedOpts struct {
	modelID     string // helper model id/label
	batchSize   int
	device      string // in-process torch device
	serverURL   string // resident embedding server (empty = in-process)
	mode        string // document | query
	truncateDim int    // MRL truncate target (0 = off)
}

// runEmbed materializes and execs embed_text.py with the texts base64-encoded.
func runEmbed(cfg config.Config, texts []string, opts embedOpts, verbose bool) (embedResult, error) {
	script, err := pyhelpers.Materialize("embed_text.py", pyhelpers.EmbedText)
	if err != nil {
		return embedResult{}, fmt.Errorf("materialize embed helper: %w", err)
	}

	payload, err := json.Marshal(texts)
	if err != nil {
		return embedResult{}, fmt.Errorf("marshal texts: %w", err)
	}
	b64 := base64.StdEncoding.EncodeToString(payload)

	args := []string{script}
	// Windows caps a process command line at ~32 KB; a large transcript (e.g. a
	// reused YouTube subtitle with hundreds of segments) overflows it as a single
	// --texts-b64 arg. Above a safe threshold, hand the payload off via a temp
	// file (--texts-b64-file) instead. Small payloads stay inline (no temp I/O).
	const inlineB64Limit = 28000
	if len(b64) > inlineB64Limit {
		tmp, terr := os.CreateTemp("", "becky_embed_b64_*.txt")
		if terr != nil {
			return embedResult{}, fmt.Errorf("create texts temp file: %w", terr)
		}
		tmpPath := tmp.Name()
		defer os.Remove(tmpPath)
		if _, werr := tmp.WriteString(b64); werr != nil {
			tmp.Close()
			return embedResult{}, fmt.Errorf("write texts temp file: %w", werr)
		}
		tmp.Close()
		args = append(args, "--texts-b64-file", tmpPath)
	} else {
		args = append(args, "--texts-b64", b64)
	}
	args = append(args,
		"--model", opts.modelID,
		"--batch-size", strconv.Itoa(opts.batchSize),
	)
	if opts.mode != "" {
		args = append(args, "--mode", opts.mode)
	}
	if opts.truncateDim > 0 {
		args = append(args, "--truncate-dim", strconv.Itoa(opts.truncateDim))
	}
	if opts.serverURL != "" {
		// Resident-server path: no model cache / device needed.
		args = append(args, "--server-url", opts.serverURL)
	} else {
		if cfg.EmbedModelCache != "" {
			args = append(args, "--cache-dir", cfg.EmbedModelCache)
		}
		if opts.device != "" {
			args = append(args, "--device", opts.device)
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
		return embedResult{}, fmt.Errorf("embed helper failed: %v\n%s", err, tail(stderr.String()))
	}

	res, ok := parseEmbedJSON(stdout.String())
	if !ok {
		return embedResult{}, fmt.Errorf("could not parse embed helper output:\n%s", tail(stdout.String()))
	}
	return res, nil
}

// parseEmbedJSON tolerates leading log noise by scanning lines bottom-up for the
// first that unmarshals into the expected shape.
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

// segmentID is a deterministic key for a segment: 12 hex chars of the source's
// SHA-256 plus the segment index. Stable across re-runs => idempotent writes.
func segmentID(sourceSHA string, index int) string {
	prefix := sourceSHA
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	return fmt.Sprintf("%s:%d", prefix, index)
}

// sha12 returns the first 12 hex chars of the SHA-256 of s. Used to derive a
// stable pseudo-source id when no --source video is supplied.
func sha12(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])[:12]
}

// vecJSON renders a vector as a compact JSON array for sqlite-vec.
func vecJSON(v []float64) string {
	parts := make([]string, len(v))
	for i, x := range v {
		parts[i] = strconv.FormatFloat(x, 'g', -1, 64)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 800 {
		return s[len(s)-800:]
	}
	return s
}
