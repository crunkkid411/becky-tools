package canvas

// model_transformer.go — real local-model Transformer for the SELECT→ASK→TRANSFORM loop.
//
// Implements Transformer (transform.go) by calling a local llama.cpp text-completion
// binary (e.g. llama-cli.exe or llama-main.exe) via os/exec. The model returns a
// strict JSON object that is parsed and mapped to a Proposal. On any failure (binary
// absent, model absent, parse error) the call returns a non-nil error so PickTransformer
// or the caller can fall back to StubTransformer — degrade-never-crash.
//
// ENV VARS (resolution order: env → hardcoded default):
//
//	BECKY_TRANSFORM_BIN   path to llama.cpp text-completion binary
//	                       default: C:/llama.cpp/build/bin/llama-cli.exe
//	BECKY_TRANSFORM_MODEL path to the text GGUF (Gemma-3-4B-IT-Q8_0 or similar)
//	                       default: X:/AI-2/becky-tools/models/gemma-3-4b-it/gemma-3-4b-it-q8_0.gguf
//
// STRICT JSON CONTRACT the model must return (first JSON object in stdout is used;
// chatter before/after is tolerated):
//
//	{
//	  "kind":    "pitch|timing|trim|gain|route|text|structure|unknown",
//	  "summary": "one sentence (≤20 words)",
//	  "before":  "current state label (e.g. C4, -6 dB, tick 480)",
//	  "after":   "proposed state label (e.g. D4, -3 dB, tick 600)",
//	  "delta":   <number>  // signed magnitude; 0 if not applicable
//	}
//
// Determinism: --temp 0 and --seed 42 are always passed so same input → same output.
// No cgo. No new module deps. Pure stdlib: os, os/exec, encoding/json.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ─── env var names and hardcoded defaults ─────────────────────────────────────

const (
	// EnvTransformBin is the env var that overrides the llama.cpp binary path.
	EnvTransformBin = "BECKY_TRANSFORM_BIN"
	// EnvTransformModel is the env var that overrides the GGUF model path.
	EnvTransformModel = "BECKY_TRANSFORM_MODEL"

	// DefaultTransformBin is the llama.cpp ONE-SHOT completion binary on Jordan's PC.
	// IMPORTANT: in recent llama.cpp builds (verified b9551, 2026-06-15) `llama-cli`
	// became an interactive chat TUI with NO one-shot mode ("--no-conversation is not
	// supported by llama-cli; please use llama-completion instead"). `llama-completion`
	// is the one-shot text-completion tool we need. Distinct from llama-mtmd-cli (vision).
	DefaultTransformBin = `C:/llama.cpp/build/bin/llama-completion.exe`

	// DefaultTransformModel is a becky-owned copy of Qwen3-4B-Instruct-2507 (Q4_K_M) —
	// a strong instruction-following GGUF already on Jordan's PC. Verified live: it
	// returns clean strict-JSON proposals (e.g. C4 + "up 2 semitones" → D4, delta 2).
	DefaultTransformModel = `X:/AI-2/becky-tools/models/Qwen3-4B-Instruct-2507-Q4_K_M.gguf`
)

// ─── modelRunner interface (test seam) ────────────────────────────────────────

// modelRunner abstracts the exec.Command call so tests never spawn a real binary.
type modelRunner interface {
	run(bin string, args []string) (stdout string, err error)
}

// execModelRunner is the production runner: it invokes llama-cli and returns its
// stdout. Stderr (llama.cpp progress chatter) is captured separately and trimmed.
type execModelRunner struct{}

func (execModelRunner) run(bin string, args []string) (string, error) {
	cmd := exec.Command(bin, args...)
	var out, errBuf strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		msg := errBuf.String()
		if len(msg) > 500 {
			msg = msg[len(msg)-500:]
		}
		return out.String(), fmt.Errorf("%w: %s", err, strings.TrimSpace(msg))
	}
	return out.String(), nil
}

// ─── ModelTransformer ─────────────────────────────────────────────────────────

// ModelTransformer implements Transformer by calling a local llama.cpp binary.
// Use PickTransformer() rather than constructing this directly; it guards
// the binary+model existence check and falls back to StubTransformer gracefully.
type ModelTransformer struct {
	bin    string      // path to llama-cli.exe or equivalent
	model  string      // path to the text GGUF
	runner modelRunner // injectable for tests
}

// newModelTransformer constructs a ModelTransformer from explicit paths.
// It does NOT check file existence — that is PickTransformer's job.
func newModelTransformer(bin, model string, r modelRunner) *ModelTransformer {
	return &ModelTransformer{bin: bin, model: model, runner: r}
}

// Propose calls the local model and returns a Proposal. Returns a non-nil error
// on any failure (binary absent, bad JSON, empty output, etc.) so PickTransformer
// or the caller can fall back to StubTransformer — degrade-never-crash.
func (mt *ModelTransformer) Propose(scene Scene, sel Selection, instruction string) (*Proposal, error) {
	if sel.Empty() {
		return nil, fmt.Errorf("model_transformer: nothing selected")
	}
	if strings.TrimSpace(instruction) == "" {
		return nil, fmt.Errorf("model_transformer: empty instruction")
	}

	prompt := buildPrompt(scene, sel, instruction)
	args := buildModelArgs(mt.model, prompt)

	stdout, err := mt.runner.run(mt.bin, args)
	if err != nil {
		return nil, fmt.Errorf("model_transformer: model exec failed: %w", err)
	}

	mr, err := parseModelResponse(stdout)
	if err != nil {
		return nil, fmt.Errorf("model_transformer: response parse failed: %w", err)
	}

	return &Proposal{
		ID:          stubID(sel, instruction), // deterministic ID, same formula as StubTransformer
		Sel:         sel,
		Instruction: instruction,
		Kind:        mapKind(mr.Kind),
		Summary:     mr.Summary,
		Before:      mr.Before,
		After:       mr.After,
		Delta:       mr.Delta,
	}, nil
}

// ─── prompt builder ───────────────────────────────────────────────────────────

// compactSceneJSON is an abbreviated view of Scene used in the prompt. We strip
// the corrections log (irrelevant noise for a one-shot transform) and keep only
// what helps the model classify and describe the change.
type compactSceneJSON struct {
	Title  string  `json:"title"`
	Mode   Mode    `json:"activeMode"`
	Tracks []Track `json:"tracks"`
	Tempo  int     `json:"tempo"`
}

// buildPrompt assembles the instruction prompt the model receives. It is compact
// and exact so the model's one-shot JSON output matches the strict contract.
func buildPrompt(scene Scene, sel Selection, instruction string) string {
	cs := compactSceneJSON{
		Title:  scene.Title,
		Mode:   scene.ActiveMode,
		Tracks: scene.Tracks,
		Tempo:  scene.Transport.BPM,
	}
	sceneJSON, _ := json.Marshal(cs) // these types are always marshallable; error impossible
	selJSON, _ := json.Marshal(sel)

	return strings.Join([]string{
		`You are a DAW editing assistant for becky-canvas. A user has selected an element and typed an instruction.`,
		`Your job is to classify the intended change and describe it concisely.`,
		``,
		`Scene (compact JSON):`,
		string(sceneJSON),
		``,
		`Selection (JSON):`,
		string(selJSON),
		``,
		`User instruction: ` + instruction,
		``,
		`Reply with ONLY a single JSON object and nothing else. Use this exact schema:`,
		`{"kind":"<pitch|timing|trim|gain|route|text|structure|unknown>","summary":"<one sentence>","before":"<current state>","after":"<proposed state>","delta":<number>}`,
		``,
		`Rules:`,
		`- kind must be exactly one of: pitch, timing, trim, gain, route, text, structure, unknown`,
		`- summary is one sentence, ≤20 words, plain English`,
		`- before and after are short labels such as "C4", "-6 dB", "tick 480", or "" if not applicable`,
		`- delta is a signed number (semitones, ticks, or dB) or 0 if not applicable`,
		`- Output ONLY the JSON object. No markdown, no explanation, no extra text.`,
	}, "\n")
}

// buildModelArgs assembles the llama-cli argv for one deterministic text completion.
// --temp 0 and --seed 42 are mandatory for becky's offline+deterministic invariant.
func buildModelArgs(model, prompt string) []string {
	return []string{
		"-m", model,
		"--temp", "0",
		"--seed", "42",
		"-n", "256", // max new tokens — the JSON fits in well under 100
		"--no-display-prompt",
		"-p", prompt,
	}
}

// ─── response parser ──────────────────────────────────────────────────────────

// modelResponse mirrors the strict JSON contract the model must return.
type modelResponse struct {
	Kind    string  `json:"kind"`
	Summary string  `json:"summary"`
	Before  string  `json:"before"`
	After   string  `json:"after"`
	Delta   float64 `json:"delta"`
}

// parseModelResponse extracts the first complete JSON object from stdout and
// decodes it. Leading/trailing chatter (timing lines, BOS token echoes from some
// llama.cpp builds) is tolerated by scanning for the first '{' … '}' pair.
func parseModelResponse(stdout string) (*modelResponse, error) {
	obj := extractFirstJSON(stdout)
	if obj == "" {
		return nil, fmt.Errorf("no JSON object found in model output (stdout: %s)", truncate(stdout, 200))
	}
	var mr modelResponse
	if err := json.Unmarshal([]byte(obj), &mr); err != nil {
		return nil, fmt.Errorf("JSON unmarshal: %w (raw: %s)", err, truncate(obj, 200))
	}
	if mr.Kind == "" {
		return nil, fmt.Errorf("model returned empty kind field (raw: %s)", truncate(obj, 200))
	}
	if mr.Summary == "" {
		return nil, fmt.Errorf("model returned empty summary field (raw: %s)", truncate(obj, 200))
	}
	return &mr, nil
}

// extractFirstJSON scans s for the first balanced '{' … '}' pair (depth 1) and
// returns it. Handles nested objects and quoted braces correctly. Returns "" if
// no complete object is found.
func extractFirstJSON(s string) string {
	start := strings.Index(s, "{")
	if start < 0 {
		return ""
	}
	depth := 0
	inStr := false
	escape := false
	for i := start; i < len(s); i++ {
		c := s[i]
		switch {
		case escape:
			escape = false
		case c == '\\' && inStr:
			escape = true
		case c == '"':
			inStr = !inStr
		case !inStr && c == '{':
			depth++
		case !inStr && c == '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

// mapKind converts the model's string kind to the ChangeKind constants defined in
// transform.go. Unrecognised strings degrade gracefully to ChangeUnknown.
func mapKind(s string) ChangeKind {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "pitch":
		return ChangePitch
	case "timing":
		return ChangeTiming
	case "trim":
		return ChangeTrim
	case "gain":
		return ChangeGain
	case "route":
		return ChangeRoute
	case "text":
		return ChangeText
	case "structure":
		return ChangeStructure
	default:
		return ChangeUnknown
	}
}

// truncate caps s at n bytes for error messages so logs stay readable.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ─── PickTransformer ──────────────────────────────────────────────────────────

// resolveTransformPaths returns the (bin, model) pair using env vars first, then
// hardcoded defaults. Mirrors the pattern in internal/vision.withDefaults.
func resolveTransformPaths() (bin, model string) {
	bin = firstNonEmptyStr(os.Getenv(EnvTransformBin), DefaultTransformBin)
	model = firstNonEmptyStr(os.Getenv(EnvTransformModel), DefaultTransformModel)
	return bin, model
}

// firstNonEmptyStr returns the first non-empty string from vals, or "" if all empty.
// (Package-local variant; mirrors vision.firstNonEmpty without cross-package import.)
func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// PickTransformer returns a *ModelTransformer when both the llama.cpp binary and
// the GGUF model exist on disk. Otherwise it returns StubTransformer so the full
// SELECT→ASK→TRANSFORM loop works everywhere — on headless CI, on Jordan's PC before
// the model is downloaded, and in any edge case where the binary is missing.
//
// This is the single call-site for cmd/canvas. Set BECKY_TRANSFORM_BIN and
// BECKY_TRANSFORM_MODEL env vars to activate the real model.
func PickTransformer() Transformer {
	bin, model := resolveTransformPaths()
	if _, err := os.Stat(bin); err != nil {
		return StubTransformer{} // binary absent — silent degrade
	}
	if _, err := os.Stat(model); err != nil {
		return StubTransformer{} // model GGUF absent — silent degrade
	}
	return newModelTransformer(bin, model, execModelRunner{})
}
