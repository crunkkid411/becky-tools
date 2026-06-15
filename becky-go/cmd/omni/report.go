// report.go — becky-omni's JSON-out result + plain-language report (SPEC-OMNIGENT.md §6).
//
// stdout is exactly ONE JSON document (the normalized governed-session result) when --json;
// otherwise a plain-English report. The SHARE URL is surfaced both in the JSON and (in
// main.go) on its own stderr line so Jordan can copy it to his phone. Schema: becky-omnigent/result@1.
//
// Fact-Forcing-Gate self-certification:
//  1. Callers: cmd/omni/main.go calls emit/finalResult/dryRunResult/degradedResult.
//  2. No-dup: no existing omni report file; shape per SPEC-OMNIGENT §6.
//  3. Data shape: resultDoc JSON (schema, goal, harness, model, tools_offered, governance,
//     share_url, answer, degraded, stopped, tool_calls, turns, cost_usd, policy/sandbox paths).
//  4. Verbatim instruction: "use subagents in parallel.. build everything".
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"becky-go/internal/omni"
	"becky-go/internal/pirun"
)

const resultSchema = "becky-omnigent/result@1"

// govSummary is the subset of governance echoed into the result (SPEC §6).
type govSummary struct {
	Sandbox  string `json:"sandbox"`
	FSWrites string `json:"fs_writes"`
	Share    string `json:"share"`
}

// resultDoc is the one JSON document becky-omni prints to stdout.
type resultDoc struct {
	Schema           string                `json:"schema"`
	Goal             string                `json:"goal"`
	Target           string                `json:"target,omitempty"`
	Harness          string                `json:"harness,omitempty"`
	Model            pirun.ModelSpec       `json:"model,omitempty"`
	Auth             string                `json:"auth,omitempty"`
	ToolsOffered     []string              `json:"tools_offered"`
	Governance       govSummary            `json:"governance"`
	ShareURL         string                `json:"share_url,omitempty"`
	Answer           json.RawMessage       `json:"answer,omitempty"`
	Degraded         bool                  `json:"degraded"`
	DegradeReason    string                `json:"degrade_reason,omitempty"`
	Stopped          string                `json:"stopped,omitempty"`
	ToolCalls        []omni.ToolCallRecord `json:"tool_calls,omitempty"`
	Turns            int                   `json:"turns"`
	CostUSD          float64               `json:"cost_usd"`
	TranscriptPath   string                `json:"transcript_path,omitempty"`
	PolicyPath       string                `json:"policy_path,omitempty"`
	SandboxPath      string                `json:"sandbox_path,omitempty"`
	SandboxSupported bool                  `json:"sandbox_supported"`
	SandboxNote      string                `json:"sandbox_note,omitempty"`
	DryRun           bool                  `json:"dry_run,omitempty"`
}

// baseDoc seeds the common fields shared by every result variant.
func baseDoc(req omni.Request, m pirun.Manifest, spec omni.OmniSpec) resultDoc {
	return resultDoc{
		Schema:       resultSchema,
		Goal:         req.Goal,
		Target:       req.Target,
		Harness:      m.BuiltinTools, // placeholder slot; the agent.yaml carries executor.harness
		Model:        req.Model,
		Auth:         spec.Auth,
		ToolsOffered: toolNames(m),
		Governance: govSummary{
			Sandbox:  spec.Gov.Sandbox,
			FSWrites: spec.Gov.FSWrites,
			Share:    spec.Gov.Share,
		},
		PolicyPath:       spec.PolicyPath,
		SandboxPath:      spec.SandboxPath,
		SandboxSupported: spec.SandboxSupported,
		SandboxNote:      spec.SandboxNote,
	}
}

// toolNames returns the manifest's permitted tool names (already sorted).
func toolNames(m pirun.Manifest) []string {
	out := make([]string, 0, len(m.Tools))
	for _, t := range m.Tools {
		out = append(out, t.Tool)
	}
	return out
}

// finalResult normalizes a governed session into the result document.
func finalResult(req omni.Request, m pirun.Manifest, spec omni.OmniSpec, res omni.OmniResult) resultDoc {
	d := baseDoc(req, m, spec)
	d.ShareURL = res.ShareURL
	d.Degraded = res.Degraded
	d.DegradeReason = res.DegradeReason
	d.Stopped = res.Stopped
	d.ToolCalls = res.ToolCalls
	d.CostUSD = res.CostUSD
	if len(res.Structured) > 0 {
		d.Answer = res.Structured
	} else if res.FinalText != "" {
		if b, err := json.Marshal(res.FinalText); err == nil {
			d.Answer = b
		}
	}
	d.TranscriptPath = filepath.Join(spec.RunDir, "transcript.jsonl")
	return d
}

// dryRunResult is the result of --dry-run: config generated, nothing executed.
func dryRunResult(req omni.Request, m pirun.Manifest, spec omni.OmniSpec) resultDoc {
	d := baseDoc(req, m, spec)
	d.DryRun = true
	return d
}

// degradedResult records a degrade (no omni, no auth) without crashing.
func degradedResult(req omni.Request, m pirun.Manifest, spec omni.OmniSpec, reason string) resultDoc {
	d := baseDoc(req, m, spec)
	d.Degraded = true
	d.DegradeReason = reason
	d.Stopped = "error"
	return d
}

// emit writes the result as JSON (one document) or as a plain-language report.
func emit(d resultDoc, asJSON bool) {
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(d)
		return
	}
	printReport(d)
}

// printReport writes a plain-language summary for a non-developer.
func printReport(d resultDoc) {
	fmt.Println("becky-omni — run a becky agent under Omnigent governance")
	fmt.Println(strings.Repeat("=", 64))
	fmt.Printf("goal      : %s\n", d.Goal)
	if d.Target != "" {
		fmt.Printf("target    : %s\n", d.Target)
	}
	fmt.Printf("tools     : %s  (default-deny — only these are allowed)\n", strings.Join(d.ToolsOffered, ", "))
	fmt.Printf("governance: sandbox=%s  writes=%s  share=%s\n", d.Governance.Sandbox, d.Governance.FSWrites, d.Governance.Share)
	if !d.SandboxSupported {
		fmt.Printf("sandbox   : %s\n", d.SandboxNote)
	}
	fmt.Println(strings.Repeat("-", 64))
	switch {
	case d.DryRun:
		fmt.Println("DRY RUN — nothing was executed. The governance config was generated:")
		fmt.Printf("  policy : %s\n", d.PolicyPath)
		fmt.Printf("  sandbox: %s\n", d.SandboxPath)
		fmt.Println("Run without --dry-run to launch the governed session (needs Omnigent installed).")
	case d.Degraded:
		fmt.Printf("DEGRADED — %s\n", d.DegradeReason)
		fmt.Printf("(policy + sandbox written for inspection: %s, %s)\n", d.PolicyPath, d.SandboxPath)
	default:
		fmt.Printf("completed: cost=$%.4f", d.CostUSD)
		if d.Stopped != "" {
			fmt.Printf(" (stopped: %s)", d.Stopped)
		}
		fmt.Println()
		if d.ShareURL != "" {
			fmt.Printf("watch/steer: %s\n", d.ShareURL)
		}
		fmt.Printf("transcript : %s\n", d.TranscriptPath)
	}
}
