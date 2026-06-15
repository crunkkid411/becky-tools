// extension.go — the per-run Pi tool-manifest generator (SPEC-AGENT-HARNESS.md §4).
//
// becky-harness must NOT hand-maintain a parallel description of every becky tool, so
// it GENERATES the Pi extension from the allowlist at run time: one pi.registerTool({...})
// entry per allowlisted tool, each shelling the real becky-*.exe. This file owns only
// the deterministic text rendering; the catalog (tool descriptions/params) is supplied
// by the cmd layer. Output is byte-stable for the same input (tools are rendered in the
// order given, which the cmd layer sorts) so the generated file is reproducible.
//
// Fact-Forcing-Gate self-certification:
//  1. Callers: cmd/harness/main.go calls GenerateExtension/RenderExtension; tested in
//     internal/pirun/pirun_test.go.
//  2. No-dup: no existing Pi-extension generator; split from pirun.go for file size.
//  3. Data shape: emits a TypeScript text file (registerTool entries) — no data files.
//  4. Verbatim instruction: "use subagents in parallel.. build everything".
package pirun

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// extensionHeader is the fixed preamble of the generated extension. It is GENERATED —
// the local agent re-verifies the import paths against the installed Pi version (SPEC
// §4.2 marks the exact import strings as an ASSUMPTION to pin).
const extensionHeader = `// GENERATED per run by becky-harness — do not edit. One entry per allowlisted tool.
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { Type } from "@sinclair/typebox";
import { execFile } from "node:child_process";

// runBeckyTool shells the REAL becky-*.exe and returns its stdout (the tool's own JSON)
// or, on a missing binary / non-zero exit / bad path, a typed degrade result so the
// AGENT sees the failure and routes around it (Pi keeps looping). Degrade, never crash.
function runBeckyTool(tool, args) {
  return new Promise((resolve) => {
    execFile(tool, args, { maxBuffer: 64 * 1024 * 1024 }, (err, stdout, stderr) => {
      const code = err && typeof err.code === "number" ? err.code : err ? 1 : 0;
      resolve({ code, stdout: stdout || "", stderr: stderr || "" });
    });
  });
}

export default function (pi: ExtensionAPI) {
`

const extensionFooter = "}\n"

// jsString renders s as a safe double-quoted TypeScript/JSON string literal.
func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// jsFlags renders a []string as a TypeScript array literal (e.g. ["--kb","kb-final"]).
func jsFlags(flags []string) string {
	parts := make([]string, 0, len(flags))
	for _, f := range flags {
		parts = append(parts, jsString(f))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// renderTool renders one pi.registerTool({...}) block for a tool spec. Params default to
// a single required string `args` (a JSON array of CLI args) when the spec gives none —
// a safe, fully-general fallback the local agent can refine per tool (SPEC §4.4).
func renderTool(t ToolSpec) string {
	label := t.Label
	if label == "" {
		label = t.Name
	}
	desc := t.Description
	if desc == "" {
		desc = "Run the " + t.Name + " becky tool."
	}
	params := strings.TrimSpace(string(t.Params))
	if params == "" {
		params = `Type.Object({ args: Type.Array(Type.String(), { description: "CLI args passed to ` + t.Name + `" }) })`
	}
	var b strings.Builder
	fmt.Fprintf(&b, "  pi.registerTool({\n")
	fmt.Fprintf(&b, "    name: %s,\n", jsString(t.Name))
	fmt.Fprintf(&b, "    label: %s,\n", jsString(label))
	fmt.Fprintf(&b, "    description: %s,\n", jsString(desc))
	fmt.Fprintf(&b, "    parameters: %s,\n", params)
	fmt.Fprintf(&b, "    async execute(toolCallId, params, signal, onUpdate, ctx) {\n")
	fmt.Fprintf(&b, "      const fixed = %s;\n", jsFlags(t.FixedFlags))
	fmt.Fprintf(&b, "      const dyn = Array.isArray(params && params.args) ? params.args.map(String) : [];\n")
	fmt.Fprintf(&b, "      const { code, stdout, stderr } = await runBeckyTool(%s, [...fixed, ...dyn]);\n", jsString(t.Name))
	fmt.Fprintf(&b, "      const text = stdout || stderr || JSON.stringify({ degraded: \"no output\", exit: code });\n")
	fmt.Fprintf(&b, "      return { content: [{ type: \"text\", text }], details: { exit: code, tool: %s, degraded: code !== 0 } };\n", jsString(t.Name))
	fmt.Fprintf(&b, "    },\n")
	fmt.Fprintf(&b, "  });\n")
	return b.String()
}

// RenderExtension returns the full generated extension source for the given tools, in
// the order supplied (the cmd layer sorts them for determinism).
func RenderExtension(tools []ToolSpec) string {
	var b strings.Builder
	b.WriteString(extensionHeader)
	for _, t := range tools {
		b.WriteString(renderTool(t))
	}
	b.WriteString(extensionFooter)
	return b.String()
}

// GenerateExtension writes the per-run TypeScript extension (exposing ONLY spec.Tools)
// into spec.RunDir and returns its path. The directory is created if absent. It never
// panics; a write failure is wrapped with context.
func GenerateExtension(spec PiSpec) (string, error) {
	if spec.RunDir == "" {
		return "", fmt.Errorf("generate extension: empty RunDir")
	}
	if err := os.MkdirAll(spec.RunDir, 0o755); err != nil {
		return "", fmt.Errorf("create run dir: %w", err)
	}
	extPath := filepath.Join(spec.RunDir, "becky-tools.ext.ts")
	if err := os.WriteFile(extPath, []byte(RenderExtension(spec.Tools)), 0o644); err != nil {
		return "", fmt.Errorf("write extension: %w", err)
	}
	return extPath, nil
}
