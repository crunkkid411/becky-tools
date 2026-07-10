// corpus.go — gathers the doc corpus becky-crawl feeds to the local model: which
// files, in what order, and how the character budget is enforced.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"becky-go/internal/pathx"
)

// docFile is one included doc, path relative to the repo root (forward-slash).
type docFile struct {
	RelPath string
	Content string
}

// corpusResult is what buildCorpus found: the files that made it in, the files
// that were found but dropped for budget reasons, and the running char total.
type corpusResult struct {
	Files        []docFile
	SkippedFiles []string
	TotalChars   int
}

// buildCorpus gathers CLAUDE.md, AGENTS.md, README* (repo root, always included in
// full — these are Jordan's own top-level constraint docs and never silently
// truncated) plus docs/**/*.md (sorted, budget-capped by maxChars).
//
// WORKLOG.md is deliberately EXCLUDED from the docs/ walk: it is an append-only,
// ever-growing log (50KB+ on hj-mission-control alone, climbing every ~2h tick),
// not a stable constraints doc — the per-tick procedure already reads its tail
// directly (AUTOPILOT.md step 2). Including it here would make the cache
// invalidate on almost every tick, defeating the point of caching.
// ponytail: one fixed exclusion name; revisit if another ever-growing log file
// shows up under docs/.
func buildCorpus(repo string, maxChars int) (corpusResult, error) {
	var result corpusResult

	for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
		p := filepath.Join(repo, name)
		if content, err := os.ReadFile(p); err == nil {
			result.Files = append(result.Files, docFile{RelPath: name, Content: string(content)})
			result.TotalChars += len(content)
		}
	}

	entries, err := os.ReadDir(repo)
	if err != nil {
		return result, err
	}
	var readmes []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasPrefix(strings.ToUpper(e.Name()), "README") {
			readmes = append(readmes, e.Name())
		}
	}
	sort.Strings(readmes)
	for _, name := range readmes {
		content, err := os.ReadFile(filepath.Join(repo, name))
		if err != nil {
			continue
		}
		result.Files = append(result.Files, docFile{RelPath: name, Content: string(content)})
		result.TotalChars += len(content)
	}

	docsRoot := filepath.Join(repo, "docs")
	var docPaths []string
	_ = filepath.WalkDir(docsRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".md") {
			return nil
		}
		if strings.EqualFold(d.Name(), "WORKLOG.md") {
			return nil
		}
		docPaths = append(docPaths, path)
		return nil
	})
	sort.Strings(docPaths)

	for _, p := range docPaths {
		rel, err := filepath.Rel(repo, p)
		if err != nil {
			rel = p
		}
		rel = filepath.ToSlash(rel)
		content, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if result.TotalChars+len(content) > maxChars {
			result.SkippedFiles = append(result.SkippedFiles, rel)
			continue
		}
		result.Files = append(result.Files, docFile{RelPath: rel, Content: string(content)})
		result.TotalChars += len(content)
	}

	return result, nil
}

// corpusHash is a stable fingerprint of exactly what was fed to the model — the
// crawl cache key. Any change to file set, order, or content produces a new hash.
func corpusHash(files []docFile) string {
	h := sha256.New()
	for _, f := range files {
		h.Write([]byte(f.RelPath))
		h.Write([]byte{0})
		h.Write([]byte(f.Content))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// relPaths extracts the RelPath of each file, in order, for the output's
// files_scanned list.
func relPaths(files []docFile) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.RelPath)
	}
	return out
}

// repoSlug derives a filesystem/cache-safe short name from a repo path, e.g.
// "X:\AI-2\becky-tools" -> "becky-tools". Uses pathx.Base (not filepath.Base):
// CLAUDE.md's Non-obvious-decisions section is explicit that a path may be a
// Windows path even when the code executing it is not running on Windows (this
// repo's CI runs Linux, see .github/workflows/ci.yml) — filepath.Base silently
// fails to split "X:\AI-2\becky-tools" on '/'-only hosts, which would have made
// this exact function's own unit test (TestRepoSlug, hardcoded Windows paths)
// pass on a dev box but fail in CI. Caught by this tool's own Law 16 crawl
// against becky-tools' CLAUDE.md before it shipped.
func repoSlug(absRepo string) string {
	base := pathx.Base(absRepo)
	if base == "" || base == "." {
		base = "repo"
	}
	var b strings.Builder
	for _, r := range strings.ToLower(base) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}

// crawlSystemPrompt is the fixed auditor persona (AUTOPILOT.md Law 16's ask,
// generalized to "every constraint" rather than one card — see buildPrompt's
// doc comment for why card text is NOT baked into the extraction itself).
const crawlSystemPrompt = `You are a careful, literal document auditor for an autonomous coding agent. You will be given the contents of a codebase's own constraint documents (CLAUDE.md, AGENTS.md, README, and docs/*.md). List every constraint, existing tool, prior decision, or repeated request in these files. Quote verbatim (short exact quotes, each under roughly 200 characters) and cite which file each came from. Group by file. Be concise: a bullet list, no preamble, no restating these instructions. If a file has nothing relevant, skip it silently.`

// buildPrompt assembles the system + user prompt from the gathered files.
//
// Deliberately card-agnostic: the extraction is always a general, exhaustive
// pass over the docs (every constraint/tool/decision/repeated-request), not
// filtered to one card's wording. Two reasons: (1) it keeps the cache correct —
// the cache key is the doc corpus hash only, so a --card change never needs a
// re-crawl; (2) Law 16 itself warns that missing a documented constraint is a
// review-level failure, and a general exhaustive pass is safer against that
// than a narrow card-scoped query that might filter something out. --card is
// still accepted by the CLI and recorded in the output for provenance, it just
// never changes what gets extracted.
func buildPrompt(files []docFile) (system, user string) {
	var b strings.Builder
	for _, f := range files {
		b.WriteString("=== FILE: ")
		b.WriteString(f.RelPath)
		b.WriteString(" ===\n")
		b.WriteString(f.Content)
		b.WriteString("\n\n")
	}
	b.WriteString("List every constraint, existing tool, prior decision, or repeated request in the files above. Quote verbatim, cite the file.")
	return crawlSystemPrompt, b.String()
}
