// prompts.go — the prompt construction for the Claude-driven stages, plus the
// deterministic spec skeleton used when no author model is available.
//
// The build prompt is deliberately spec + task ON STDIN (agentrun delivers it there);
// the briefing is the SYSTEM layer (--append-system-prompt-file). The Model
// Verification Protocol is baked into the prompt TEXT (not as fixed ids) so a model
// that picks a sub-model still re-verifies it live.
//
// Fact-Forcing-Gate self-certification:
//  1. Callers: claude_stages.go calls buildSpecPrompt / buildBuildPrompt /
//     deterministicSpecSkeleton and uses specSystemPrompt.
//  2. No-dup: new factory-specific prompt construction; no existing equivalent
//     (cmd/review's systemPrompt is a fixed annotation prompt, unrelated + not importable).
//  3. Data shape: reads *State (intake/research/spec) to build prompt strings;
//     writes no data files.
//  4. Verbatim instruction: "Bake the protocol into the research stage's prompt/logic,
//     not as fixed model ids."
package main

import (
	"fmt"
	"strings"
)

// specSystemPrompt is the system layer for the S4 spec author.
const specSystemPrompt = `You write a build SPEC for a new "becky" forensic-video CLI tool, in the project's
proven house structure. The spec is human-reviewed before any code is written, so be
concrete and honest. Required sections, in order:
1. A Fact-Forcing block (4 facts: callers, no-dup, data shape, verbatim spec line).
2. TL;DR / verdict.
3. What it is + why (one paragraph).
4. CLI contract: a flags table (name, default, purpose). JSON path(s) on argv; JSON to
   stdout; diagnostics to stderr; exit codes. No TUI/web/interactive prompts.
5. Backends + graceful degradation plan.
6. JSON output contract with ONE synthetic example.
7. Where it fits the existing pipeline.
8. Honest limits + dated sources.
House rules to bake in: JSON in/out; h264_nvenc (never libx264); offline-first; reuse
the internal/ packages (beckyio, config, mediainfo, etc.); never modify source videos;
no LLM between deterministic pipeline steps. For any tool that names an AI model, the
spec MUST require RUNTIME model verification (hf CLI for local, OpenRouter live free
list for hosted-free, official site for hosted APIs) and FORBID hardcoding a model id
from a blog/training data. Output the spec as Markdown ONLY (no preamble, no fences).`

// buildSpecPrompt assembles the S4 user prompt from the run-state.
func (o *orchestrator) buildSpecPrompt(s *State) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Write the spec for a new becky tool.\n\n")
	fmt.Fprintf(&b, "## Intake\n- slug: %s\n- capability: %s\n- input: %s, output: %s\n- constraints: %s\n\n",
		s.Intake.Slug, s.Intake.Capability, s.Intake.InputKind, s.Intake.OutputKind, strings.Join(s.Intake.Constraints, ", "))
	fmt.Fprintf(&b, "## Redundancy verdict\n- %s (confidence %.2f); closest existing: %s\n- why not covered: %s\n\n",
		s.Redundancy.Verdict, s.Redundancy.Confidence, strings.Join(s.Redundancy.ClosestExisting, ", "), s.Redundancy.WhyNotCovered)
	b.WriteString("## Model verification record (from S2 — DO NOT hardcode any stale model id; re-verify at runtime)\n")
	for _, c := range s.Research.ModelChecks {
		mark := "unverified"
		if c.Verified {
			mark = "verified"
		}
		fmt.Fprintf(&b, "- [%s] %s (%s): %s\n", mark, c.ModelID, c.Channel, c.SourceURL)
	}
	b.WriteString("\nThe research brief is at: " + s.Research.BriefPath + "\n")
	b.WriteString("\nWrite the full Markdown spec now.")
	return b.String()
}

// buildBuildPrompt assembles the S5 build task prompt. feedback (from S6) is appended
// for a resume iteration so the agent fixes exactly what failed.
func (o *orchestrator) buildBuildPrompt(s *State, feedback string) string {
	var b strings.Builder
	dir := cmdDirName(s.Intake.Slug) // bare name, no becky- prefix (house convention)
	if feedback == "" {
		fmt.Fprintf(&b, "Build the becky tool `%s` per the approved spec below.\n\n", s.Intake.Slug)
		b.WriteString("You are running the Ralph loop from the briefing (your system prompt): build -> run on real input -> inspect -> fix -> repeat until ALL definition-of-done items hold.\n\n")
		fmt.Fprintf(&b, "## Definition of done\n")
		for _, d := range s.Intake.DefinitionOfDone {
			fmt.Fprintf(&b, "- %s\n", d)
		}
		fmt.Fprintf(&b, "\n## Where to put it\n- Package: cmd/%s/main.go (package main) — the directory is the BARE name WITHOUT the becky- prefix, matching cmd/transcribe, cmd/cut, etc.\n- Binary: bin/%s\n- Build root: %s (already on --add-dir)\n",
			dir, binName(s.Intake.Slug), o.buildRoot)
		fmt.Fprintf(&b, "- Real test asset: %s\n\n", o.testAsset)
		b.WriteString("## Reuse (do NOT reinvent)\n- internal/beckyio (PrintJSON/Fatalf/Logf), internal/config (paths), internal/mediainfo (Probe).\n")
		b.WriteString("- Match the comment density + JSON-in/JSON-out shape of cmd/transcribe and cmd/cut.\n\n")
		b.WriteString("## Hard rules\n- JSON to stdout, diagnostics to stderr, exit codes. --verbose gates progress.\n- Degrade gracefully (skipped/reason + exit 0) if an optional dep/model is missing; never half-JSON.\n- If you name any AI model, VERIFY it live (hf CLI / OpenRouter live free list / official site); never hardcode a model id from memory.\n- Do NOT edit build-all-tools.bat or PROGRESS.md — the factory's deterministic S10 stage owns integration; you only write cmd/" + dir + "/ and reuse internal/.\n\n")
		fmt.Fprintf(&b, "## Spec\nThe approved spec is at: %s\nRead it, then build. When you finish, STOP and report what you built, the exact build+test commands, a real JSON snippet, and any degradations.\n", s.Spec.SpecPath)
		// Also inline the spec so the agent has it even if file read is constrained.
		if body := readFileBest(s.Spec.SpecPath, 12000); body != "" {
			b.WriteString("\n--- SPEC CONTENT (inline copy) ---\n")
			b.WriteString(body)
		}
	} else {
		fmt.Fprintf(&b, "Your previous build of `%s` FAILED the deterministic S6 verification. Fix it.\n\n", s.Intake.Slug)
		fmt.Fprintf(&b, "## S6 failure\n%s\n\n", feedback)
		fmt.Fprintf(&b, "Re-run the Ralph loop in cmd/%s/ until: go build clean, go vet clean, the binary runs on %s, stdout is one valid JSON document, exit 0, stderr quiet without --verbose. Then STOP and report.\n",
			dir, o.testAsset)
	}
	return b.String()
}

// deterministicSpecSkeleton writes a minimal but honest spec when no author model is
// available, so the run can still pause at GATE B for a human.
func (o *orchestrator) deterministicSpecSkeleton(s *State) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# SPEC — becky-%s (deterministic skeleton)\n\n", s.Intake.Slug)
	b.WriteString("> Authored deterministically (no author model reachable). A human MUST flesh this out at GATE B.\n\n")
	b.WriteString("## Fact-Forcing block\n")
	fmt.Fprintf(&b, "1. Callers: invoked from the CLI as `becky-%s`; may be chained by the pipeline.\n", s.Intake.Slug)
	b.WriteString("2. No-dup: S3 redundancy verdict was " + s.Redundancy.Verdict + ".\n")
	fmt.Fprintf(&b, "3. Data shape: reads %s input on argv; emits a single JSON document to stdout.\n", s.Intake.InputKind)
	fmt.Fprintf(&b, "4. Verbatim: capability = %q.\n\n", s.Intake.Capability)
	fmt.Fprintf(&b, "## TL;DR\n%s\n\n", s.Intake.Capability)
	b.WriteString("## CLI contract\n| flag | default | purpose |\n|---|---|---|\n| (input path) | — | the input to process |\n| --verbose | false | progress to stderr |\n\n")
	b.WriteString("## Degradation\n- Missing optional model/dep -> JSON with skipped/reason, exit 0. Never half-JSON.\n\n")
	b.WriteString("## JSON output (synthetic)\n```json\n{\"input\":\"...\",\"result\":[],\"skipped\":null}\n```\n\n")
	b.WriteString("## Model verification (REQUIRED at runtime — no hardcoded ids)\n")
	for _, c := range s.Research.ModelChecks {
		fmt.Fprintf(&b, "- %s (%s): %s\n", c.ModelID, c.Channel, c.SourceURL)
	}
	b.WriteString("\n## Honest limits\n- Skeleton spec; expand before building.\n")
	return b.String()
}
