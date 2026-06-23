// propose_run.go — the --propose driver: run scout's autonomous gate (Qwen
// proposes, Gemma agrees), write an intake for every APPROVED proposal, and —
// only with --build — hand each intake to the becky-new-tool factory.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"becky-go/internal/scout"
)

// runPropose runs the model gate over the report's surfaced videos. It returns the
// number of approved proposals. status messages go to `w` (stdout in text mode,
// stderr in JSON mode so stdout stays valid JSON).
func runPropose(rep scout.Report, proposeDir string, build bool, w io.Writer) int {
	items := append(append(append([]scout.Item{}, rep.Relevant...), rep.Candidates...), rep.Useful...)
	if len(items) == 0 {
		fmt.Fprintln(w, "propose: nothing surfaced to consider.")
		return 0
	}

	ctx := context.Background()
	proposer, judges, cleanup, ok, why := PickProposeModels(ctx)
	if !ok {
		fmt.Fprintln(w, "propose: skipped —", why)
		fmt.Fprintln(w, "  (need llama-server + the Qwen and Gemma GGUFs; this runs on Jordan's PC, not in the cloud)")
		return 0
	}
	defer cleanup()

	fmt.Fprintf(w, "\npropose: asking Qwen to pitch tools and Gemma-4 to vote on %d video(s)...\n", len(items))
	decisions := scout.Propose(items, proposer, judges, 1)

	if err := os.MkdirAll(proposeDir, 0o755); err != nil {
		fmt.Fprintln(w, "propose: could not create", proposeDir, "-", err)
		return 0
	}

	approved := 0
	date := time.Now().Format("2006-01-02")
	for _, d := range decisions {
		if !d.Approved {
			fmt.Fprintf(w, "  · held back: %s — %s\n", d.Proposal.Slug, d.Reason)
			continue
		}
		approved++
		intake := d.ToIntake(date)
		path := filepath.Join(proposeDir, intake.Slug+".intake.json")
		if err := writeIntake(path, intake); err != nil {
			fmt.Fprintf(w, "  ! %s approved but could not write intake: %v\n", intake.Slug, err)
			continue
		}
		fmt.Fprintf(w, "  ✓ APPROVED: %s — %s\n      intake: %s\n", intake.Slug, intake.Capability, path)
		if build {
			runFactory(path, w)
		}
	}

	switch {
	case approved == 0:
		fmt.Fprintln(w, "propose: the models didn't agree on anything worth building this time.")
	case build:
		fmt.Fprintf(w, "propose: %d tool(s) approved and sent to becky-new-tool.\n", approved)
	default:
		fmt.Fprintf(w, "propose: %d tool(s) approved. Intakes written to %s.\n", approved, proposeDir)
		fmt.Fprintln(w, "  Re-run with --build to have becky-new-tool build them, or build one with:")
		fmt.Fprintf(w, "  becky-new-tool --intake-file %s --yes\n", filepath.Join(proposeDir, "<slug>.intake.json"))
	}
	return approved
}

// writeIntake saves an intake record as indented JSON.
func writeIntake(path string, intake scout.Intake) error {
	b, err := json.MarshalIndent(intake, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// runFactory invokes becky-new-tool on an approved intake, fully autonomous
// (--yes auto-passes its gates; --offline keeps it to local models). Output is
// streamed through. A factory failure is reported, not fatal (the next approved
// proposal still runs).
func runFactory(intakePath string, w io.Writer) {
	bin := resolveSibling("becky-new-tool")
	cmd := exec.Command(bin, "--intake-file", intakePath, "--yes", "--offline")
	cmd.Stdout = w
	cmd.Stderr = w
	fmt.Fprintf(w, "      building via %s ...\n", bin)
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(w, "      ! becky-new-tool failed for %s: %v (intake kept for a manual run)\n", intakePath, err)
	}
}

// resolveSibling returns the path to a sibling becky tool: next to this
// executable if present, else the bare name (found on PATH).
func resolveSibling(name string) string {
	if exe, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(exe), name)
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
		if _, err := os.Stat(cand + ".exe"); err == nil {
			return cand + ".exe"
		}
	}
	return name
}
