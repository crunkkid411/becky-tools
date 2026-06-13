// integrate.go — S10 INTEGRATE (fully deterministic). The becky integration
// checklist as single-line/append edits: append <name> to the `set TOOLS=` line in
// build-all-tools.bat (idempotent), append the tool's row to PROGRESS.md (its row
// only), verify the build, re-run the tool once post-integration, and write a
// PR-ready summary. No model. Commits/pushes nothing.
//
// SAFETY: this build's task forbids running the FULL build-all-tools.bat, so by
// default S10 verifies via a SCOPED `go build` of the new tool's package only (the
// same compile the bat would run for that one tool). --run-build-all opts into the
// full bat for a real integration.
//
// Fact-Forcing-Gate self-certification:
//  1. Callers: orchestrator.go's stage dispatch calls runS10Integrate.
//  2. No-dup: the factory's own integration stage; the TOOLS-line append + PROGRESS
//     row are unique to it (becky-eval/pipeline don't touch build-all/PROGRESS).
//  3. Data shape: edits build-all-tools.bat (the `set TOOLS=` line) + appends a
//     PROGRESS.md row; writes pr-summary.md; writes state.integrate.
//  4. Verbatim instruction: "OWNED FILES: cmd/new-tool/, internal/agentrun/, the
//     `new-tool` entry in build-all-tools.bat ... Do NOT run the full
//     build-all-tools.bat; verify with `go build`/`go vet`/`go test` scoped to your
//     packages."
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// runS10Integrate performs the deterministic integration edits + verification.
func (o *orchestrator) runS10Integrate(ctx context.Context, s *State) error {
	if s.Integrate != nil {
		o.logf("S10 integrate: already done — skipping")
		return nil
	}
	if s.Test == nil || !s.Test.Passed {
		s.Integrate = &Integrate{Note: "integration skipped: tool did not pass S6 verification"}
		o.logf("S10 integrate: skipped (S6 not passed)")
		return s.save()
	}

	ig := &Integrate{}
	tool := s.Intake.Slug       // e.g. "becky-redact" (binary becky-redact.exe)
	cmdName := cmdDirName(tool) // e.g. "redact" (cmd/redact, the TOOLS-line token)

	// 1) Append cmdName to the `set TOOLS=` line in build-all-tools.bat (idempotent).
	batPath := filepath.Join(o.buildRoot, "build-all-tools.bat")
	updated, err := appendToToolsLine(batPath, cmdName)
	if err != nil {
		ig.Note = "could not update build-all-tools.bat: " + err.Error()
		o.logf("S10 integrate: %s", ig.Note)
	} else {
		ig.ToolsLineUpdated = updated
	}

	// 2) Append the tool's PROGRESS.md row (its row only).
	progPath := filepath.Join(filepath.Dir(o.buildRoot), "PROGRESS.md")
	if added, perr := appendProgressRow(progPath, tool, s); perr != nil {
		ig.Note = strings.TrimSpace(ig.Note + " progress row not added: " + perr.Error())
	} else {
		ig.ProgressRowAdded = added
	}

	// 3) Verify the build (SCOPED by default — the task forbids the full bat).
	if o.runBuildAll {
		out, berr := o.runBatBuildAll(ctx, batPath)
		ig.BuildAllGreen = berr == nil
		if berr != nil {
			ig.Note = strings.TrimSpace(ig.Note + " build-all-tools.bat failed: " + tail2(out, 300))
		}
	} else {
		if out, berr := o.goRun(ctx, "build", "-o", filepath.Join("bin", binName(tool)), "./cmd/"+cmdName); berr != nil {
			ig.BuildAllGreen = false
			ig.Note = strings.TrimSpace(ig.Note + " scoped go build failed: " + tail2(out, 300))
		} else {
			ig.BuildAllGreen = true
			ig.Note = strings.TrimSpace(ig.Note + " (verified via scoped go build; full build-all-tools.bat NOT run per task constraint — re-run it manually before merge)")
		}
	}

	// 4) Re-run the tool once post-integration.
	bin := filepath.Join(o.buildRoot, "bin", binName(tool))
	stdout, _, code := o.runBinary(ctx, bin, o.testAsset)
	if _, ok := extractJSON(stdout); ok && code == 0 {
		ig.PostIntegrateRun = "exit 0, valid JSON"
	} else {
		ig.PostIntegrateRun = fmt.Sprintf("exit %d", code)
	}

	// 5) PR-ready summary (no commit/push).
	prPath := filepath.Join(s.Meta.RunDir, "pr-summary.md")
	_ = os.WriteFile(prPath, []byte(o.buildPRSummary(s, ig)), 0o644)
	ig.PRSummaryPath = prPath

	s.Integrate = ig
	o.logf("S10 integrate: tools_line=%v progress_row=%v build_green=%v post_run=%s",
		ig.ToolsLineUpdated, ig.ProgressRowAdded, ig.BuildAllGreen, ig.PostIntegrateRun)
	return s.save()
}

// appendToToolsLine adds cmdName to the `set TOOLS=...` line of build-all-tools.bat if
// not already present. Idempotent; preserves the rest of the file verbatim.
func appendToToolsLine(batPath, cmdName string) (bool, error) {
	data, err := os.ReadFile(batPath)
	if err != nil {
		return false, err
	}
	lines := strings.Split(string(data), "\n")
	for i, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "set TOOLS=") {
			if containsWord(ln, cmdName) {
				return false, nil // already present — idempotent
			}
			lines[i] = strings.TrimRight(ln, "\r") + " " + cmdName
			out := strings.Join(lines, "\n")
			if err := os.WriteFile(batPath, []byte(out), 0o644); err != nil {
				return false, err
			}
			return true, nil
		}
	}
	return false, fmt.Errorf("no `set TOOLS=` line found")
}

// appendProgressRow appends a single PROGRESS.md table row for the new tool. It does
// NOT reformat or touch any existing row.
func appendProgressRow(progPath, tool string, s *State) (bool, error) {
	data, err := os.ReadFile(progPath)
	if err != nil {
		return false, err
	}
	if strings.Contains(string(data), "| "+tool+" |") {
		return false, nil // already has a row — idempotent
	}
	recall := "n/a"
	if s.Finetune != nil && !s.Finetune.Skipped {
		recall = fmt.Sprintf("eval recall %.3f", s.Finetune.TrainRecall)
	}
	row := fmt.Sprintf("| NEW | %s | BUILT by becky-new-tool %s | %s; S6 passed; %s |\n",
		tool, todayISO(), oneLine(s.Intake.Capability), recall)
	f, err := os.OpenFile(progPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return false, err
	}
	defer f.Close()
	if _, err := f.WriteString(row); err != nil {
		return false, err
	}
	return true, nil
}

// runBatBuildAll runs the full build-all-tools.bat (opt-in only).
func (o *orchestrator) runBatBuildAll(ctx context.Context, batPath string) (string, error) {
	out, err := o.goRunCmd(ctx, batPath)
	return out, err
}

// buildPRSummary drafts a PR body from the run-state (analyze run, summary, test-plan
// TODOs — per the git-workflow rule). It does not commit or push.
func (o *orchestrator) buildPRSummary(s *State, ig *Integrate) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Add %s (built by becky-new-tool)\n\n", s.Intake.Slug)
	fmt.Fprintf(&b, "_Generated %s. Factory run dir: %s_\n\n", todayISO(), s.Meta.RunDir)
	fmt.Fprintf(&b, "## What\n%s\n\n", s.Intake.Capability)
	b.WriteString("## How it was built (deterministic factory pipeline)\n")
	fmt.Fprintf(&b, "- S2 research: %d model checks (Model Verification Protocol, runtime-verified)\n", len(s.Research.ModelChecks))
	fmt.Fprintf(&b, "- S3 redundancy: %s (confidence %.2f)\n", s.Redundancy.Verdict, s.Redundancy.Confidence)
	if s.Spec != nil {
		fmt.Fprintf(&b, "- S4 spec: %s (%s)\n", s.Spec.SpecPath, s.Spec.AuthoredBy)
	}
	if s.Build != nil {
		fmt.Fprintf(&b, "- S5 build: headless agent, %d turns, $%.4f, fact-forcing facts found=%v\n", s.Build.TurnsUsed, s.Build.CostUSD, s.Build.FactForcing.Found)
	}
	if s.Test != nil {
		fmt.Fprintf(&b, "- S6 test: passed=%v (built=%v vet=%v json=%v exit=%d)\n", s.Test.Passed, s.Test.Built, s.Test.Vet, s.Test.JSONValid, s.Test.ExitCode)
	}
	if s.Review != nil {
		fmt.Fprintf(&b, "- S7 second-AI: reviewer=%s, %d findings (%d blocking)\n", s.Review.Reviewer, len(s.Review.Findings), s.Review.BlockingCount)
	}
	if s.Finetune != nil && !s.Finetune.Skipped {
		fmt.Fprintf(&b, "- S9 finetune: train recall %.3f, holdout %.3f (%s)\n", s.Finetune.TrainRecall, s.Finetune.HoldoutRecall, s.Finetune.GeneralizationCaveat)
	}
	b.WriteString("\n## Test plan\n- [ ] Re-run build-all-tools.bat and confirm green\n- [ ] Run the tool on a second real asset\n- [ ] Human review of the spec + real output + second-AI findings\n")
	fmt.Fprintf(&b, "- [ ] Total Claude Agent-SDK spend this run: $%.4f\n", s.Meta.ClaudeCostUSDTotal)
	return b.String()
}
