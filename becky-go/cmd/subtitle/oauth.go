package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"becky-go/internal/proc"
	"becky-go/internal/subs"
)

// Sonnet 5 (and every other Anthropic model) through Jordan's CLAUDE MAX
// SUBSCRIPTION — his OAuth session — which costs NOTHING extra because he
// already pays for it.
//
// This exists because the alternative was theft: routing the caption pass
// through anthropic/claude-sonnet-5 on OpenRouter burned his whole $0.67
// balance in one run, buying a model he already owns. His rule, verbatim:
// "USE MY GODDAMN MOTHER FUCKING OAUTH FOR SONNET 5 - NEVER EVER EVER SPEND MY
// MONEY IN OPENROUTER OR API FOR SONNET 5 - I HAVE MAX".
//
// The transport is fleet-run.ps1, method 2 of the three sanctioned ways to
// reach another model (free-model-launchers.md §0). It wraps `claude --model
// sonnet` with the order-file/out-file contract that stops a headless session
// mangling a long prompt, truncating its answer on stdout, or blocking on a
// permission prompt nobody can grant.
//
// It is ONE job for the whole edit rather than a batch per call: a session
// costs ~60s to spin up regardless of payload, so eleven of them would be
// eleven minutes. hy3:free stays the default for speed; this is the "I want
// Sonnet 5's judgement and I refuse to pay for it" path.
const fleetRunner = `X:\AI-2\fleet\fleet-run.ps1`

func fleetRunnerPath() string {
	if p := os.Getenv("BECKY_FLEET_RUN"); p != "" {
		return p
	}
	return fleetRunner
}

// isOAuthModel reports whether the name asks for an Anthropic model, which must
// come from the subscription rather than any paid API.
func isOAuthModel(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "sonnet", "sonnet5", "sonnet-5", "opus", "haiku", "fable", "oauth":
		return true
	}
	return false
}

// oauthModelMode maps a friendly name onto a fleet-run -Mode.
func oauthModelMode(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "opus":
		return "opus"
	case "haiku":
		return "haiku"
	case "fable":
		return "fable"
	}
	return "sonnet"
}

// oauthModel returns a subs.ModelFunc that runs the review through the Claude
// Max session. Slower than the free HTTP path, free in the sense that matters.
func oauthModel(name string, verbose bool) subs.ModelFunc {
	mode := oauthModelMode(name)
	var call int

	return func(ctx context.Context, prompt string) (string, error) {
		call++
		start := time.Now()
		fmt.Fprintf(os.Stderr, "  reviewing captions via your Claude Max session (%s), call %d - this takes ~1 min...\n", mode, call)
		defer func() {
			fmt.Fprintf(os.Stderr, "  call %d finished in %.0fs (cost: $0, covered by Max)\n", call, time.Since(start).Seconds())
		}()

		runner := fleetRunnerPath()
		if _, err := os.Stat(runner); err != nil {
			return "", fmt.Errorf("fleet runner not found at %s", runner)
		}

		dir, err := os.MkdirTemp("", "becky-caption-oauth-")
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
			"-Mode", mode,
			"-OrderFile", orderPath,
			"-OutFile", outPath,
			"-MaxAttempts", "1",
			"-TimeoutMin", "6",
			"-StallMin", "4",
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

		// The wrapper's contract: the ONLY pass signal is the out file existing
		// with content. An exit code proves nothing.
		b, readErr := os.ReadFile(outPath)
		if readErr != nil || len(bytes.TrimSpace(b)) == 0 {
			if runErr != nil {
				return "", fmt.Errorf("%s: %s", mode, firstLine(strings.TrimSpace(errb.String()+" "+runErr.Error())))
			}
			return "", fmt.Errorf("%s wrote no result", mode)
		}
		return string(b), nil
	}
}
