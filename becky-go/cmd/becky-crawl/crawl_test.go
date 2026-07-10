package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"becky-go/internal/config"
	"becky-go/internal/llmlocal"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestBuildCorpus_MandatoryFilesAlwaysIncluded confirms CLAUDE.md/AGENTS.md/
// README* are gathered from the repo root regardless of the char budget.
func TestBuildCorpus_MandatoryFilesAlwaysIncluded(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "CLAUDE.md"), "claude rules")
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "agent rules")
	writeFile(t, filepath.Join(dir, "README.md"), "readme text")

	cr, err := buildCorpus(dir, 1) // budget of 1 char — should still not drop mandatory files
	if err != nil {
		t.Fatal(err)
	}
	if len(cr.Files) != 3 {
		t.Fatalf("got %d files, want 3 (CLAUDE.md, AGENTS.md, README.md): %+v", len(cr.Files), cr.Files)
	}
	names := relPaths(cr.Files)
	for _, want := range []string{"CLAUDE.md", "AGENTS.md", "README.md"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Errorf("missing mandatory file %s in %v", want, names)
		}
	}
}

// TestBuildCorpus_WorklogExcluded confirms docs/WORKLOG.md never enters the
// corpus (case-insensitive) — see corpus.go's doc comment for why.
func TestBuildCorpus_WorklogExcluded(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "docs", "WORKLOG.md"), "append-only log, huge and growing")
	writeFile(t, filepath.Join(dir, "docs", "HANDOFF.md"), "handoff notes")

	cr, err := buildCorpus(dir, defaultMaxChars)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range cr.Files {
		if strings.EqualFold(filepath.Base(f.RelPath), "WORKLOG.md") {
			t.Fatalf("WORKLOG.md must never be included, got files: %v", relPaths(cr.Files))
		}
	}
	found := false
	for _, f := range cr.Files {
		if f.RelPath == "docs/HANDOFF.md" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected docs/HANDOFF.md in corpus, got %v", relPaths(cr.Files))
	}
}

// TestBuildCorpus_NestedDocsAndSkip confirms docs/**/*.md is walked recursively
// (sorted) and that files which don't fit the remaining budget are recorded in
// SkippedFiles rather than silently dropped.
func TestBuildCorpus_NestedDocsAndSkip(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "docs", "a.md"), strings.Repeat("a", 50))
	writeFile(t, filepath.Join(dir, "docs", "research", "b.md"), strings.Repeat("b", 50))
	writeFile(t, filepath.Join(dir, "docs", "z-too-big.md"), strings.Repeat("z", 500))

	// Budget fits a.md + research/b.md (100 chars) but not the 500-char file.
	cr, err := buildCorpus(dir, 100)
	if err != nil {
		t.Fatal(err)
	}
	names := relPaths(cr.Files)
	wantIncluded := []string{"docs/a.md", "docs/research/b.md"}
	for _, w := range wantIncluded {
		ok := false
		for _, n := range names {
			if n == w {
				ok = true
			}
		}
		if !ok {
			t.Errorf("expected %s to be included, got %v", w, names)
		}
	}
	skippedOK := false
	for _, s := range cr.SkippedFiles {
		if s == "docs/z-too-big.md" {
			skippedOK = true
		}
	}
	if !skippedOK {
		t.Errorf("expected docs/z-too-big.md in SkippedFiles, got %v", cr.SkippedFiles)
	}
}

// TestBuildCorpus_NoDocsDir confirms a repo with no docs/ directory at all (e.g.
// becky-tools itself on master, verified 2026-07-10) degrades to an empty docs
// tier instead of erroring.
func TestBuildCorpus_NoDocsDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "README.md"), "just a readme")
	cr, err := buildCorpus(dir, defaultMaxChars)
	if err != nil {
		t.Fatalf("buildCorpus with no docs/ dir should not error: %v", err)
	}
	if len(cr.Files) != 1 || cr.Files[0].RelPath != "README.md" {
		t.Fatalf("expected only README.md, got %v", relPaths(cr.Files))
	}
}

// TestCorpusHash_DeterministicAndSensitive confirms the same file set hashes
// identically, and any content change changes the hash — this hash IS the cache
// key, so both properties are load-bearing.
func TestCorpusHash_DeterministicAndSensitive(t *testing.T) {
	a := []docFile{{RelPath: "CLAUDE.md", Content: "rule one"}}
	b := []docFile{{RelPath: "CLAUDE.md", Content: "rule one"}}
	c := []docFile{{RelPath: "CLAUDE.md", Content: "rule TWO"}}

	if corpusHash(a) != corpusHash(b) {
		t.Fatal("identical corpora must hash identically")
	}
	if corpusHash(a) == corpusHash(c) {
		t.Fatal("different content must hash differently")
	}
}

func TestRepoSlug(t *testing.T) {
	tests := map[string]string{
		`X:\AI-2\becky-tools`:        "becky-tools",
		`X:\AI-2\hj-mission-control`: "hj-mission-control",
		`/home/only1/Weird Name!`:    "weird-name-",
	}
	for in, want := range tests {
		if got := repoSlug(in); got != want {
			t.Errorf("repoSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestCacheRoundTrip confirms writeCache -> readCache preserves the document, and
// a path with no cached file reports a clean miss (not an error).
func TestCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "repo-deadbeef.json")

	if _, ok := readCache(path); ok {
		t.Fatal("expected cache miss before any write")
	}

	want := Output{OK: true, Tool: toolVersion, Repo: "X:/AI-2/becky-tools", RepoSlug: "becky-tools", Findings: "- quote (CLAUDE.md)"}
	if err := writeCache(path, want); err != nil {
		t.Fatalf("writeCache: %v", err)
	}
	got, ok := readCache(path)
	if !ok {
		t.Fatal("expected cache hit after write")
	}
	if got.Findings != want.Findings || got.RepoSlug != want.RepoSlug {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", got, want)
	}
}

// TestBuildPrompt_CardNeverAffectsCorpus documents and locks the design choice in
// corpus.go's buildPrompt comment: the prompt built from a given file set is
// identical no matter what --card text a caller passes, because card text is
// never threaded into buildPrompt at all. This is what keeps the corpus-hash
// cache key correct.
func TestBuildPrompt_CardNeverAffectsCorpus(t *testing.T) {
	files := []docFile{{RelPath: "CLAUDE.md", Content: "some rule"}}
	sys1, user1 := buildPrompt(files)
	sys2, user2 := buildPrompt(files)
	if sys1 != sys2 || user1 != user2 {
		t.Fatal("buildPrompt must be a pure function of files (no hidden card/global state)")
	}
	if !strings.Contains(user1, "CLAUDE.md") || !strings.Contains(user1, "some rule") {
		t.Fatalf("prompt should embed the file path and content, got: %s", user1)
	}
}

// TestCrawlLive is a real end-to-end smoke test against the actual local model —
// mirrors cmd/ask's intent_llm_test.go pattern: skip (not fail) when the GGUF or
// llama-server binary aren't present on this machine, so CI (which has neither)
// stays green while a real dev machine gets a genuine live check.
func TestCrawlLive(t *testing.T) {
	cfg := config.Load()
	client := llmlocal.NewClientCtx(cfg.GemmaModel, cfg.LlamaServer, defaultCtxLen, nil)
	if err := client.Available(); err != nil {
		t.Skipf("local model/llama-server not available, skipping live test: %v", err)
	}

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "CLAUDE.md"), "RULE: never delete Jordan's files. ALWAYS run tests before committing.")

	cr, err := buildCorpus(dir, defaultMaxChars)
	if err != nil {
		t.Fatal(err)
	}
	system, user := buildPrompt(cr.Files)

	ctx, cancel := context.WithTimeout(context.Background(), crawlTimeout)
	defer cancel()
	reply, err := client.Chat(ctx, system, user, llmlocal.Options{MaxTokens: defaultMaxTokens})
	if err != nil {
		t.Fatalf("live Chat call failed: %v", err)
	}
	if strings.TrimSpace(reply) == "" {
		t.Fatal("live crawl returned an empty reply for a file with a clear constraint in it")
	}
	t.Logf("live crawl reply (%d chars): %s", len(reply), reply)
}
