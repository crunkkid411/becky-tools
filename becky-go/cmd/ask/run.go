// run.go — execution plumbing: resolve the local intent model path, locate the
// sibling becky-*.exe, and run an approved command. The runner mirrors
// cmd/becky/runner.go (resolve a becky-<tool> binary next to the running exe, run
// it with captured stdout/stderr) so becky-ask drives the same tools the
// orchestrator does — it does NOT reimplement any forensic compute.
//
// Nothing here runs automatically: a command is executed only after the
// act-vs-discuss gate (intent.go) decided to ACT, or the user picked a
// quick-action row. This is the single place that shells out.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// defaultIntentModel is the user's Qwen3.5-4B GGUF, verified on disk 2026-06-08.
// It is the model the brief REQUIRES (no substitution). BECKY_ASK_MODEL overrides
// it for testing / relocation. The mmproj sibling is not needed: intent is
// text-only.
const defaultIntentModel = `X:\HuggingFace\models\unsloth\Qwen3.5-4B-GGUF\Qwen3.5-4B-Q4_K_M.gguf`

// resolveIntentModel returns the GGUF path for the local intent model. Order:
// the BECKY_ASK_MODEL env override, then the verified on-disk default. It does NOT
// fall back to a different model — per the Model Verification Protocol, becky-ask
// uses the user's Qwen3.5 or degrades to the keyword catalog, never a substitute.
func resolveIntentModel() string {
	if v := strings.TrimSpace(os.Getenv("BECKY_ASK_MODEL")); v != "" {
		return v
	}
	return defaultIntentModel
}

// runResult carries a finished command's outcome back to the TUI.
type runResult struct {
	Command  []string // the argv that ran (for the headline)
	Stdout   string   // tool stdout (JSON) — shown trimmed
	ExitCode int
	Err      error    // non-nil on failure (binary missing, non-zero exit, etc.)
	Saved    []string // sidecar paths written next to the input (or a note why not)
}

// binPathFor resolves becky-<tool>(.exe) next to the running becky-ask
// executable, then the working dir / its bin\ — exactly like cmd/becky.
func binPathFor(tool string) (string, error) {
	name := "becky-" + tool
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	var dirs []string
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(exe))
	}
	if wd, err := os.Getwd(); err == nil {
		dirs = append(dirs, wd, filepath.Join(wd, "bin"))
	}
	for _, d := range dirs {
		cand := filepath.Join(d, name)
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			return cand, nil
		}
	}
	return "", fmt.Errorf("%s not found next to becky-ask (build it into bin\\)", name)
}

// runCommand executes a built becky-* command ([becky-<tool>, args...]) and
// returns its result. It never panics; a missing binary or non-zero exit comes
// back as runResult.Err so the chat can surface it plainly.
func runCommand(ctx context.Context, cmd []string) runResult {
	if len(cmd) == 0 {
		return runResult{Err: errors.New("empty command")}
	}
	tool := strings.TrimPrefix(cmd[0], "becky-")
	bin, err := binPathFor(tool)
	if err != nil {
		return runResult{Command: cmd, Err: err}
	}
	c := exec.CommandContext(ctx, bin, cmd[1:]...)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	runErr := c.Run()
	res := runResult{Command: cmd, Stdout: stdout.String(), ExitCode: exitCode(runErr)}
	if runErr != nil {
		res.Err = fmt.Errorf("%s (exit %d): %s", cmd[0], res.ExitCode, tailRun(stderr.String(), 400))
	}
	return res
}

// runTimeout bounds a single tool run so the UI is never wedged indefinitely; the
// underlying tools are themselves resumable / bounded.
const runTimeout = 30 * time.Minute

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

func tailRun(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = "..." + s[len(s)-n:]
	}
	return strings.Join(strings.Fields(s), " ")
}
