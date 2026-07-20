package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"becky-go/internal/proc"
	"becky-go/internal/subs"
)

// The pass-2 caption reviewer goes through fleet-run.ps1 — "THE one reliable way
// to hand a job to a free-fleet model headless" — NOT a direct `claude -p` call.
//
// Calling the CLI directly is what hung Jordan's launcher on "reviewing caption
// grouping with sonnet". The wrapper's own header documents why, from traps that
// were already field-verified here:
//   - a long prompt passed as an argument gets MANGLED crossing a process
//     boundary, so the order lives in a FILE the model reads itself;
//   - streamed stdout TRUNCATES long answers, so the model WRITES its answer to
//     a file and stdout is ignored;
//   - a spawned session stops to "ask permission" nobody can grant, so an
//     UNATTENDED preamble is injected and stdin is closed (” | claude).
//
// Reinventing this was the mistake. Use the wrapper.
const fleetRunner = `X:\AI-2\fleet\fleet-run.ps1`

// fleetRunnerPath allows the wrapper to move without a rebuild.
func fleetRunnerPath() string {
	if p := os.Getenv("BECKY_FLEET_RUN"); p != "" {
		return p
	}
	return fleetRunner
}

// fleetModel returns a subs.ModelFunc that runs one review batch through
// fleet-run.ps1: the prompt goes out as an order file, the answer comes back as
// a file. Nothing large crosses the command line, and nothing is read off stdout.
func fleetModel(model string, verbose bool) subs.ModelFunc {
	if model == "" {
		model = "sonnet"
	}
	var batch int
	return func(ctx context.Context, prompt string) (string, error) {
		batch++
		start := time.Now()
		fmt.Fprintf(os.Stderr, "  reviewing caption grouping, batch %d...\n", batch)
		defer func() {
			fmt.Fprintf(os.Stderr, "  batch %d finished in %.0fs\n", batch, time.Since(start).Seconds())
		}()

		runner := fleetRunnerPath()
		if _, err := os.Stat(runner); err != nil {
			return "", fmt.Errorf("fleet runner not found at %s", runner)
		}

		dir, err := os.MkdirTemp("", "becky-caption-review-")
		if err != nil {
			return "", err
		}
		defer os.RemoveAll(dir)

		orderPath := filepath.Join(dir, "order.md")
		outPath := filepath.Join(dir, "result.json")
		order := prompt + "\n\nWrite ONLY the JSON array to the output file. No prose, no markdown fence.\n"
		if err := os.WriteFile(orderPath, []byte(order), 0o644); err != nil {
			return "", err
		}

		cmd := exec.CommandContext(ctx, "pwsh", "-NoProfile", "-File", runner,
			"-Mode", model,
			"-OrderFile", orderPath,
			"-OutFile", outPath,
			"-MaxAttempts", "1",
			"-TimeoutMin", "5",
			"-StallMin", "3",
			"-MinBytes", "10",
		)
		cmd.WaitDelay = 5 * time.Second
		proc.NoWindow(cmd)
		var errb bytes.Buffer
		cmd.Stderr = &errb
		if verbose {
			cmd.Stdout = os.Stderr
		}
		runErr := cmd.Run()

		// The wrapper's own contract: the ONLY pass signal is the out file
		// existing with content. Exit code proves nothing.
		b, readErr := os.ReadFile(outPath)
		if readErr != nil || len(bytes.TrimSpace(b)) == 0 {
			if runErr != nil {
				return "", fmt.Errorf("%s: %s", model, firstLine(strings.TrimSpace(errb.String()+" "+runErr.Error())))
			}
			return "", fmt.Errorf("%s wrote no result", model)
		}
		return string(b), nil
	}
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}

// capStyle is the per-reel caption style both review apps write when Jordan
// drags a caption up or down. It lives beside the .srt as "<stem>.capstyle.json"
// — a contract the two GUIs already share, so the burn must honour it or the
// height he set on screen would not survive into the render.
type capStyle struct {
	MarginV int `json:"margin_v"`
}

// capStylePath is the sidecar for a given .srt.
func capStylePath(srt string) string {
	return strings.TrimSuffix(srt, filepath.Ext(srt)) + ".capstyle.json"
}

// loadMarginV returns the vertical placement saved by the review apps, or 0 when
// there is none.
func loadMarginV(srt string) int {
	b, err := os.ReadFile(capStylePath(srt))
	if err != nil {
		return 0
	}
	var cs capStyle
	if json.Unmarshal(b, &cs) != nil || cs.MarginV <= 0 {
		return 0
	}
	return cs.MarginV
}

// noteReviewSkipped writes the reason to stderr so a silent fallback is never
// mistaken for a successful review.
func noteReviewSkipped(reason string) {
	fmt.Fprintf(os.Stderr, "caption review skipped (%s) - captions will break on pacing only\n", reason)
}
