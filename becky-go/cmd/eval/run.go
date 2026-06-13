// run.go — executes the becky tool under test for one (case x config), captures
// its stdout/output JSON, flattens it to searchable text, and scores it. Results
// are cached to disk so a re-run is resumable (skips completed (case,config) pairs
// unless --force).
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"becky-go/internal/beckyio"
)

// stderrTailBytes caps stored stderr for a failed run.
const stderrTailBytes = 1500

// binName returns the platform-correct becky tool binary file name.
func binName(tool string) string {
	name := "becky-" + tool
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// cacheKey is the on-disk filename for a (case,config) result.
func cacheKey(caseID, config string) string {
	safe := func(s string) string {
		return strings.NewReplacer("/", "_", "\\", "_", " ", "_", ":", "_").Replace(s)
	}
	return safe(caseID) + "__" + safe(config) + ".json"
}

// runner executes becky tools and scores their output.
type runner struct {
	binDir    string
	serverURL string
	cacheDir  string
	force     bool
	verbose   bool
}

// runCase runs the tool for one case+config (or loads the cached result) and
// returns the scored CaseResult. It never panics: a failed tool run yields a
// status:"failed" result with the stderr tail, so the eval continues.
func (r *runner) runCase(c Case, cfg Config, tool string) CaseResult {
	res := CaseResult{CaseID: c.ID, Config: cfg.Name, Tool: tool, Holdout: c.Holdout}

	// Resume: reuse a cached result unless forced.
	cachePath := filepath.Join(r.cacheDir, cacheKey(c.ID, cfg.Name))
	if !r.force {
		if cached, ok := loadCached(cachePath); ok {
			beckyio.Logf(r.verbose, "  [cache] %s / %s (recall=%.3f)", c.ID, cfg.Name, cached.Recall)
			return cached
		}
	}

	if _, err := os.Stat(c.Input); err != nil {
		res.Status = "failed"
		res.Error = "input not found: " + c.Input
		r.cache(cachePath, res)
		return res
	}
	bin := filepath.Join(r.binDir, binName(tool))
	if _, err := os.Stat(bin); err != nil {
		res.Status = "failed"
		res.Error = "binary not found: " + bin
		r.cache(cachePath, res)
		return res
	}

	outFile, err := os.CreateTemp("", "becky_eval_out_*.json")
	if err != nil {
		res.Status = "failed"
		res.Error = "cannot create temp output: " + err.Error()
		r.cache(cachePath, res)
		return res
	}
	outPath := outFile.Name()
	outFile.Close()
	defer os.Remove(outPath)

	args := []string{c.Input}
	args = append(args, cfg.Args...)
	if r.serverURL != "" && !hasFlag(args, "--server-url") && toolUsesServer(tool) {
		args = append(args, "--server-url", r.serverURL)
	}
	if !hasFlag(args, "--output") {
		args = append(args, "--output", outPath)
	}

	beckyio.Logf(r.verbose, "  [run ] %s %s", binName(tool), strings.Join(args, " "))
	start := time.Now()
	stdout, stderrTail, runErr := execTool(bin, args)
	res.DurationMS = time.Since(start).Milliseconds()

	if runErr != nil {
		res.Status = "failed"
		res.Error = failureDetail(runErr, stderrTail)
		r.cache(cachePath, res)
		return res
	}

	// Output may be in the --output file or on stdout (some tools print JSON).
	outputText := readOutputText(outPath, stdout)
	hits, recall := scoreFacts(outputText, c.AnswerKey)
	res.Status = "ok"
	res.Recall = recall
	res.HitCount = hitCount(hits)
	res.FactCount = len(c.AnswerKey)
	res.OutputLen = len(outputText)
	res.FactHits = hits
	r.cache(cachePath, res)
	beckyio.Logf(r.verbose, "  [ ok ] %s / %s recall=%.3f (%d/%d facts, %dms)",
		c.ID, cfg.Name, recall, res.HitCount, res.FactCount, res.DurationMS)
	return res
}

// execTool runs the tool, returning its stdout, a stderr tail, and the run error.
func execTool(bin string, args []string) (stdout, stderrTail string, err error) {
	cmd := exec.Command(bin, args...)
	var so, se strings.Builder
	cmd.Stdout = &so
	cmd.Stderr = &se
	runErr := cmd.Run()
	return so.String(), tail(se.String(), stderrTailBytes), runErr
}

// readOutputText loads the JSON output (from the --output file, falling back to
// captured stdout) and flattens all string values into one searchable blob.
func readOutputText(outPath, stdout string) string {
	data, err := os.ReadFile(outPath)
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		data = []byte(stdout)
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		// Not JSON: score the raw text directly.
		return string(data)
	}
	var b strings.Builder
	flatten(v, &b)
	return b.String()
}

// flatten walks a decoded JSON value and appends every string (and stringified
// number) to b, separated by newlines — a recall-search haystack of all output text.
func flatten(v any, b *strings.Builder) {
	switch t := v.(type) {
	case string:
		b.WriteString(t)
		b.WriteByte('\n')
	case float64:
		fmt.Fprintf(b, "%g\n", t)
	case bool:
		fmt.Fprintf(b, "%v\n", t)
	case []any:
		for _, e := range t {
			flatten(e, b)
		}
	case map[string]any:
		for _, e := range t {
			flatten(e, b)
		}
	}
}

// cache writes a result to disk for resume (best-effort; a write error is logged
// but never fatal — the in-memory result is still returned).
func (r *runner) cache(path string, res CaseResult) {
	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		beckyio.Logf(r.verbose, "  [warn] cache write failed: %v", err)
	}
}

// loadCached reads a previously-cached result, if present and valid.
func loadCached(path string) (CaseResult, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return CaseResult{}, false
	}
	var res CaseResult
	if json.Unmarshal(data, &res) != nil {
		return CaseResult{}, false
	}
	return res, true
}

// hasFlag reports whether args already contain flag (so case Args can override
// eval-injected defaults like --output / --server-url).
func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// toolUsesServer reports whether a tool accepts --server-url (only the
// model-backed validate tool does today).
func toolUsesServer(tool string) bool {
	return tool == "validate"
}

// failureDetail combines the run error with the captured stderr tail.
func failureDetail(runErr error, stderrTail string) string {
	detail := runErr.Error()
	var ee *exec.ExitError
	if errors.As(runErr, &ee) {
		detail = fmt.Sprintf("exit %d", ee.ExitCode())
	}
	if stderrTail != "" {
		detail += ": " + stderrTail
	}
	return detail
}

// tail returns at most n trailing bytes of s with whitespace collapsed.
func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = "..." + s[len(s)-n:]
	}
	return strings.Join(strings.Fields(s), " ")
}
