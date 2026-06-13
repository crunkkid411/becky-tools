// becky-eval — an offline, deterministic, resumable evaluation harness for the
// becky tools.
//
// It reads an eval MANIFEST that pairs each raw video with its wiki "answer key"
// (the facts a human documented for that clip) and a config/prompt search space,
// runs the relevant becky tool once per (case x config) across the corpus, scores
// each output against the answer key by RECALL (recall-weighted; false positives
// not penalized, per the forensic spec — humans + cross-corroboration filter
// them), ranks the configs on the non-holdout ("train") cases, picks the best,
// and re-measures it on the HELD-OUT cases to check generalization.
//
//	becky-eval <manifest.json> [options]
//	  --bin-dir <dir>     where the becky-*.exe binaries live (default: this exe's dir)
//	  --server-url <url>  reuse a running llama-server for model-backed tools (validate)
//	  --cache <dir>       per-(case,config) result cache for resume (default: <manifest>.cache)
//	  --force             ignore the cache and re-run every (case,config)
//	  --output <file>     write the ranked report JSON here (default: stdout)
//	  --verbose           progress to stderr
//
// Output is a single JSON document: per-(case,config) recall + per-fact hit/miss,
// a config ranking on the train split, the best config, and its holdout recall.
// Exit 0 on success; non-zero only on a usage/manifest error.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"becky-go/internal/beckyio"
)

func main() {
	binDir := flag.String("bin-dir", "", "directory holding the becky-*.exe binaries (default: this exe's dir)")
	serverURL := flag.String("server-url", "", "reuse a running llama-server for model-backed tools")
	cacheDir := flag.String("cache", "", "result cache dir for resume (default: <manifest>.cache)")
	force := flag.Bool("force", false, "ignore the cache and re-run every (case,config)")
	out := flag.String("output", "", "write report JSON here (default: stdout)")
	verbose := flag.Bool("verbose", false, "progress to stderr")

	manifestPath := parsePositional()
	if manifestPath == "" {
		beckyio.Fatalf("usage: becky-eval <manifest.json> [--bin-dir dir] [--server-url url] [--cache dir] [--force] [--output file] [--verbose]")
	}

	m, err := loadManifest(manifestPath)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}

	resolvedBin := resolveBinDir(*binDir, m.BinDir)
	resolvedCache := *cacheDir
	if resolvedCache == "" {
		resolvedCache = manifestPath + ".cache"
	}
	if err := os.MkdirAll(resolvedCache, 0o755); err != nil {
		beckyio.Fatalf("cannot create cache dir %s: %v", resolvedCache, err)
	}
	resolvedServer := firstNonEmpty(*serverURL, m.ServerURL)

	r := &runner{
		binDir:    resolvedBin,
		serverURL: resolvedServer,
		cacheDir:  resolvedCache,
		force:     *force,
		verbose:   *verbose,
	}

	report := Report{
		EvaluatedAt: time.Now().UTC().Format(time.RFC3339),
		Tool:        m.Tool,
		BinDir:      resolvedBin,
	}
	if len(m.Cases) == 0 {
		report.Notes = append(report.Notes, "manifest has no cases")
	}

	// Run every (case x its-config-set).
	for _, c := range m.Cases {
		tool := firstNonEmpty(c.Tool, m.Tool)
		if tool == "" {
			report.CaseResults = append(report.CaseResults, CaseResult{
				CaseID: c.ID, Status: "failed", Error: "no tool specified (manifest.tool or case.tool)",
			})
			continue
		}
		configs := c.Configs
		if len(configs) == 0 {
			configs = m.Configs
		}
		if len(configs) == 0 {
			configs = []Config{{Name: "default"}} // run the tool with no extra args
		}
		beckyio.Logf(*verbose, "case %s (%s): %d config(s)", c.ID, tool, len(configs))
		for _, cfg := range configs {
			report.CaseResults = append(report.CaseResults, r.runCase(c, cfg, tool))
		}
	}

	// Rank configs on the train split; re-measure the best on holdout.
	report.Ranking = rankConfigs(report.CaseResults, false)
	if len(report.Ranking) > 0 {
		best := report.Ranking[0]
		report.Best = &best
		holdout := rankConfigs(report.CaseResults, true)
		for _, h := range holdout {
			if h.Config == best.Config {
				report.Holdout = []ConfigScore{h}
				break
			}
		}
		if len(report.Holdout) == 0 && len(holdout) > 0 {
			report.Notes = append(report.Notes,
				"best train config has no holdout cases; reporting all holdout scores instead")
			report.Holdout = holdout
		}
	} else {
		report.Notes = append(report.Notes, "no train (non-holdout) cases to rank")
	}

	if err := emit(report, *out); err != nil {
		beckyio.Fatalf("%v", err)
	}
	beckyio.Logf(*verbose, "done: %d case-result(s), %d config(s) ranked",
		len(report.CaseResults), len(report.Ranking))
}

// parsePositional pulls the manifest path and re-parses any flags after it.
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

// loadManifest reads and validates the eval manifest JSON.
func loadManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("parse manifest json: %w", err)
	}
	return m, nil
}

// resolveBinDir prefers the CLI flag, then the manifest, then this exe's dir.
func resolveBinDir(flagDir, manifestDir string) string {
	if flagDir != "" {
		return flagDir
	}
	if manifestDir != "" {
		return manifestDir
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Dir(exe)
	}
	return "."
}

func emit(report Report, outPath string) error {
	if outPath == "" {
		beckyio.PrintJSON(report)
		return nil
	}
	b, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	if err := os.WriteFile(outPath, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
