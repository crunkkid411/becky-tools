// becky-crawl — READ-ONLY local-model doc crawler (AUTOPILOT.md Law 16). Globs
// CLAUDE.md/AGENTS.md/README*/docs/**/*.md in a target repo, feeds them to a
// local instruct GGUF (gemma-4-E4B-it-qat by default, via internal/llmlocal — the
// SAME llama-server transport becky-ask/becky-clip already use), and asks it to
// list every constraint, existing tool, prior decision, or repeated request found
// in those docs, quoted verbatim. This is the tool call that makes Law 16's
// "run a READ-ONLY local-model pass over the docs before building anything"
// deterministic instead of ad hoc every tick.
//
//	becky-crawl --repo <dir> [--card "<text>"] [--output <file>] [--force] [--max-chars N] [--verbose]
//
// Caches the result by a hash of the exact doc corpus fed to the model, so a
// repeat crawl against unchanged docs (the common case — most ticks touch a repo
// whose CLAUDE.md/docs did not change since the last crawl) returns instantly
// with NO model call. The cache lives in ~/.becky/crawl-cache/, never inside the
// scanned repo — this tool is read-only against its target, always.
//
// Degrade-never-crash: a missing model/server keeps exit 0 with degraded:true and
// a plain reason (becky-vision/becky-ocr's convention). A bad --repo path is a
// hard usage error (ok:false, exit 1).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/llmlocal"
)

const toolVersion = "becky-crawl v1.0.0"

// defaultMaxChars caps the docs/*.md tier (CLAUDE.md/AGENTS.md/README are always
// included whole, uncapped). ~100,000 chars is ~25K tokens at a conservative 4
// chars/token, leaving headroom inside defaultCtxLen for the system prompt, the
// card, and the response. ponytail: fixed budget, not adaptive; raise both this
// and defaultCtxLen together if a repo's docs/ genuinely outgrows it (the tool
// reports files_skipped so that's never silent).
const defaultMaxChars = 100000

// defaultCtxLen is the llama-server context window for the crawl call. Sized for
// defaultMaxChars — see its comment.
const defaultCtxLen = 32768

// defaultMaxTokens caps the model's answer length (a bullet list of quotes, not a
// novel).
const defaultMaxTokens = 1500

// crawlTimeout bounds one crawl call (cold model load + a long-corpus prefill).
const crawlTimeout = 5 * time.Minute

// Output is becky-crawl's stdout JSON document (and the exact shape cached).
type Output struct {
	OK           bool     `json:"ok"`
	Tool         string   `json:"tool"`
	Repo         string   `json:"repo"`
	RepoSlug     string   `json:"repo_slug"`
	Card         string   `json:"card,omitempty"`
	FilesScanned []string `json:"files_scanned"`
	FilesSkipped []string `json:"files_skipped,omitempty"`
	CorpusChars  int      `json:"corpus_chars"`
	CorpusHash   string   `json:"corpus_hash"`
	Cached       bool     `json:"cached"`
	Model        string   `json:"model,omitempty"`
	GeneratedAt  string   `json:"generated_at"`
	Findings     string   `json:"findings"`
	Degraded     bool     `json:"degraded"`
	Note         string   `json:"note,omitempty"`
	Error        string   `json:"error,omitempty"`
}

func main() {
	repo := flag.String("repo", "", "target repo directory to crawl (required)")
	card := flag.String("card", "", "optional card/task text, recorded for provenance only (never changes what is extracted — see corpus.go's buildPrompt doc comment)")
	output := flag.String("output", "", "write JSON here instead of stdout")
	force := flag.Bool("force", false, "bypass the cache and re-run the model even if this exact doc corpus was crawled before")
	maxChars := flag.Int("max-chars", defaultMaxChars, "character budget for docs/*.md (CLAUDE.md/AGENTS.md/README are always included whole)")
	verbose := flag.Bool("verbose", false, "progress on stderr")
	flag.Bool("json", false, "no-op: becky-crawl's default output is already JSON (see --output to redirect it to a file)")
	flag.Parse()

	if strings.TrimSpace(*repo) == "" {
		failJSON("usage: becky-crawl --repo <dir> [--card \"<text>\"] [--output <file>] [--force] [--max-chars N] [--verbose]")
	}
	info, err := os.Stat(*repo)
	if err != nil || !info.IsDir() {
		failJSON("--repo not found or not a directory: %s", *repo)
	}
	absRepo, err := filepath.Abs(*repo)
	if err != nil {
		absRepo = *repo
	}

	cr, err := buildCorpus(absRepo, *maxChars)
	if err != nil {
		failJSON("gather docs under %s: %v", absRepo, err)
	}

	slug := repoSlug(absRepo)
	hash := corpusHash(cr.Files)

	out := Output{
		OK: true, Tool: toolVersion, Repo: absRepo, RepoSlug: slug, Card: *card,
		FilesScanned: relPaths(cr.Files), FilesSkipped: cr.SkippedFiles,
		CorpusChars: cr.TotalChars, CorpusHash: hash,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}

	if len(cr.Files) == 0 {
		out.Degraded = true
		out.Note = "no README*/CLAUDE.md/AGENTS.md/docs/**/*.md files found under --repo"
		emit(out, *output)
		return
	}

	cachePath := cacheFilePath(slug, hash)
	if !*force {
		if cached, ok := readCache(cachePath); ok {
			beckyio.Logf(*verbose, "crawl: cache hit for %s (%s...), skipping model call", slug, hash[:12])
			cached.Card = *card
			cached.Cached = true
			emit(cached, *output)
			return
		}
	}

	cfg := config.Load()
	client := llmlocal.NewClientCtx(cfg.GemmaModel, cfg.LlamaServer, defaultCtxLen, func(f string, a ...any) { beckyio.Logf(*verbose, f, a...) })
	if err := client.Available(); err != nil {
		out.Degraded = true
		out.Note = fmt.Sprintf("local model unavailable, crawl skipped: %v", err)
		emit(out, *output)
		return
	}

	system, user := buildPrompt(cr.Files)
	beckyio.Logf(*verbose, "crawl: querying local model over %d doc file(s), %d chars...", len(cr.Files), cr.TotalChars)

	ctx, cancel := context.WithTimeout(context.Background(), crawlTimeout)
	defer cancel()
	reply, err := client.Chat(ctx, system, user, llmlocal.Options{MaxTokens: defaultMaxTokens})
	if err != nil {
		out.Degraded = true
		out.Note = fmt.Sprintf("local model call failed, crawl skipped: %v", err)
		emit(out, *output)
		return
	}

	out.Model = filepath.Base(cfg.GemmaModel)
	out.Findings = strings.TrimSpace(reply)
	out.Cached = false

	if err := writeCache(cachePath, out); err != nil {
		beckyio.Logf(*verbose, "crawl: warning: could not write cache: %v", err)
	}
	emit(out, *output)
}

func emit(o Output, outPath string) {
	if outPath == "" {
		beckyio.PrintJSON(o)
		return
	}
	b, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		failJSON("marshal output: %v", err)
	}
	if err := os.WriteFile(outPath, append(b, '\n'), 0o644); err != nil {
		failJSON("write output: %v", err)
	}
}

// failJSON matches becky-ocr/becky-perceive/search_library's usage-error
// convention: {"ok":false,"error":"..."} to stdout, exit 1 (never a bare stderr
// line for a scriptable tool).
func failJSON(format string, a ...any) {
	beckyio.PrintJSON(Output{OK: false, Tool: toolVersion, Error: fmt.Sprintf(format, a...)})
	os.Exit(1)
}
