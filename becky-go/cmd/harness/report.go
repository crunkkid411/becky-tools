// report.go — becky-harness's JSON-out result + plain-language report (SPEC-AGENT-HARNESS.md §6).
//
// stdout is exactly ONE JSON document (the normalized run result) when --json; otherwise a
// plain-English report a non-developer can read. stderr (in main.go) carries headlines. The
// result schema is becky-harness/result@1.
//
// Fact-Forcing-Gate self-certification:
//  1. Callers: cmd/harness/main.go calls emit/finalResult/dryRunResult/emitResult/degradedResultDoc.
//  2. No-dup: no existing harness report file; shape per SPEC §6.
//  3. Data shape: resultDoc JSON (schema, goal, target, model, tools_offered, answer,
//     degraded, stopped, tool_calls, turns, cost_usd, transcript_path, manifest_path, dry_run).
//  4. Verbatim instruction: "use subagents in parallel.. build everything".
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"becky-go/internal/pirun"
)

const resultSchema = "becky-harness/result@1"

// resultDoc is the one JSON document becky-harness prints to stdout.
type resultDoc struct {
	Schema         string                 `json:"schema"`
	Goal           string                 `json:"goal"`
	Target         string                 `json:"target,omitempty"`
	Model          pirun.ModelSpec        `json:"model,omitempty"`
	ToolsOffered   []string               `json:"tools_offered"`
	Answer         json.RawMessage        `json:"answer,omitempty"`
	Degraded       bool                   `json:"degraded"`
	DegradeReason  string                 `json:"degrade_reason,omitempty"`
	Stopped        string                 `json:"stopped,omitempty"`
	ToolCalls      []pirun.ToolCallRecord `json:"tool_calls,omitempty"`
	Turns          int                    `json:"turns"`
	CostUSD        float64                `json:"cost_usd"`
	TranscriptPath string                 `json:"transcript_path,omitempty"`
	ManifestPath   string                 `json:"manifest_path,omitempty"`
	ExtensionPath  string                 `json:"extension_path,omitempty"`
	DryRun         bool                   `json:"dry_run,omitempty"`
}

// baseDoc seeds the common fields shared by every result variant.
func baseDoc(req pirun.Request, m pirun.Manifest, manifestPath, extPath string) resultDoc {
	return resultDoc{
		Schema:        resultSchema,
		Goal:          req.Goal,
		Target:        req.Target,
		Model:         req.Model,
		ToolsOffered:  toolNames(m),
		ManifestPath:  manifestPath,
		ExtensionPath: extPath,
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

// finalResult normalizes a Pi run into the result document.
func finalResult(req pirun.Request, m pirun.Manifest, res pirun.PiResult, manifestPath, extPath string) resultDoc {
	d := baseDoc(req, m, manifestPath, extPath)
	d.Degraded = res.Degraded
	d.DegradeReason = res.DegradeReason
	d.Stopped = res.Stopped
	d.ToolCalls = res.ToolCalls
	d.Turns = res.Turns
	d.CostUSD = res.CostUSD
	if len(res.StructuredOutput) > 0 {
		d.Answer = res.StructuredOutput
	} else if res.FinalText != "" {
		// Wrap free text as a JSON string so `answer` is always valid JSON.
		if b, err := json.Marshal(res.FinalText); err == nil {
			d.Answer = b
		}
	}
	d.TranscriptPath = filepath.Join(filepath.Dir(manifestPath), "transcript.jsonl")
	return d
}

// dryRunResult is the result of --dry-run: everything generated, nothing executed.
func dryRunResult(req pirun.Request, m pirun.Manifest, manifestPath, extPath string) resultDoc {
	d := baseDoc(req, m, manifestPath, extPath)
	d.DryRun = true
	return d
}

// emitResult is the result of --emit-omnigent: the manifest is written for becky-omni.
func emitResult(req pirun.Request, m pirun.Manifest, manifestPath, extPath string) resultDoc {
	return baseDoc(req, m, manifestPath, extPath)
}

// degradedResultDoc records a degrade (no Pi, no auth) without crashing.
func degradedResultDoc(req pirun.Request, m pirun.Manifest, manifestPath, extPath, reason string) resultDoc {
	d := baseDoc(req, m, manifestPath, extPath)
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
	fmt.Println("becky-harness — run a Pi agent over a declared becky toolbox")
	fmt.Println(strings.Repeat("=", 64))
	fmt.Printf("goal   : %s\n", d.Goal)
	if d.Target != "" {
		fmt.Printf("target : %s\n", d.Target)
	}
	fmt.Printf("tools  : %s  (default-deny — only these are allowed)\n", strings.Join(d.ToolsOffered, ", "))
	if d.Model.ID != "" || d.Model.Provider != "" {
		fmt.Printf("model  : %s %s\n", d.Model.Provider, d.Model.ID)
	}
	fmt.Println(strings.Repeat("-", 64))
	switch {
	case d.DryRun:
		fmt.Println("DRY RUN — nothing was executed. The manifest + Pi extension were generated:")
		fmt.Printf("  manifest : %s\n", d.ManifestPath)
		fmt.Printf("  extension: %s\n", d.ExtensionPath)
		fmt.Println("Run without --dry-run to launch the agent (needs Pi installed).")
	case d.Degraded:
		fmt.Printf("DEGRADED — %s\n", d.DegradeReason)
		fmt.Printf("(manifest written for inspection: %s)\n", d.ManifestPath)
	default:
		fmt.Printf("completed: turns=%d cost=$%.4f", d.Turns, d.CostUSD)
		if d.Stopped != "" {
			fmt.Printf(" (stopped: %s)", d.Stopped)
		}
		fmt.Println()
		fmt.Printf("transcript: %s\n", d.TranscriptPath)
	}
}
