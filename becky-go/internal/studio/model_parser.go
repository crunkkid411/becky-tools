package studio

// model_parser.go — the FAST BACKGROUND MODEL parser stub for becky-wire.
//
// This represents a small instruct model (Smol / LFM2-Instruct class — fast
// enough to run on every keystroke in the background while Jordan types) that
// reads the instruction + a COMPACT project summary and emits a strict-JSON
// Intent. It mirrors the house pattern in internal/canvas/model_transformer.go
// (PickTransformer): PickParser() returns the model-backed parser when the
// binary AND weights exist on disk, and SILENTLY DEGRADES to the always-correct
// DeterministicParser otherwise. The deterministic parser already handles every
// shipped example sentence, so becky-wire works fully offline today; the model
// only widens coverage to free-form phrasing.
//
// ─── ENV VARS (resolution order: env → hardcoded default) ──────────────────────
//
//	BECKY_WIRE_BIN    path to the llama.cpp one-shot completion binary
//	                  default: C:/llama.cpp/build/bin/llama-completion.exe
//	BECKY_WIRE_MODEL  path to the small instruct GGUF (Smol/LFM2-Instruct class)
//	                  default: X:/AI-2/becky-tools/models/LFM2-1.2B-Instruct-Q4_K_M.gguf
//
// ─── MODEL I/O CONTRACT (what the local Windows agent must wire) ───────────────
//
// INPUT (prompt): the instruction plus a compact, deterministic project summary —
//
//	{
//	  "tracks": ["drums","bass","lead","vox", ...],   // track ids
//	  "buses":  ["bus.808","bus.drums","bus.master", ...]
//	}
//
// the model is told the legal Action values and the canonical node/bus ids it may
// reference (so it returns ids that already exist in the project, never invents).
//
// OUTPUT (first JSON object in stdout; chatter tolerated) — the strict Intent:
//
//	{
//	  "action":   "sidechain|route|insertChain|setVST|setGain|unknown",
//	  "source":   "<node id or '' >",   // detector/track
//	  "target":   "<bus/node id or '' >",
//	  "band":     "low|''",
//	  "vst":      "<plugin name or '' >",
//	  "gainDb":   <number>,             // 0 if n/a
//	  "hasGain":  <bool>,
//	  "note":     "<plain-English summary>"
//	}
//
// Determinism: --temp 0 --seed 42 are mandatory (becky's offline+deterministic
// invariant). The exec/parse call below is a DOCUMENTED STUB — the local agent
// fills in runModel() exactly as model_transformer.go's execModelRunner does.
// No cgo, no new module deps.

import (
	"os"
	"strings"

	"becky-go/internal/music"
)

const (
	// EnvWireBin overrides the llama.cpp completion-binary path.
	EnvWireBin = "BECKY_WIRE_BIN"
	// EnvWireModel overrides the small-instruct GGUF path.
	EnvWireModel = "BECKY_WIRE_MODEL"

	// DefaultWireBin is the one-shot llama.cpp completion binary on Jordan's PC.
	// (Same binary becky-canvas uses; see model_transformer.go for why
	// llama-completion, not the interactive llama-cli.)
	DefaultWireBin = `C:/llama.cpp/build/bin/llama-completion.exe`

	// DefaultWireModel is a SMALL fast instruct GGUF — the "background model"
	// class. LFM2-1.2B-Instruct is right-sized to parse one short studio command
	// near-instantly; swap to a Smol* GGUF here if preferred.
	DefaultWireModel = `X:/AI-2/becky-tools/models/LFM2-1.2B-Instruct-Q4_K_M.gguf`
)

// ModelParser implements Parser by (eventually) calling the local instruct model.
// It is constructed only by PickParser when the binary + weights resolve.
type ModelParser struct {
	bin   string
	model string
}

// Parse runs the model and decodes its strict-JSON Intent. The model call itself
// is a STUB for the local agent to wire (see runModel below); until then Parse
// returns Intent{Action: Unknown} with a non-nil error so PickParser's caller
// falls through to the deterministic path (degrade-never-crash).
func (mp *ModelParser) Parse(instruction string, proj music.Project) (Intent, error) {
	_ = buildModelPrompt(instruction, proj) // prompt assembly is real + testable
	out, err := mp.runModel(instruction, proj)
	if err != nil {
		return Intent{Action: ActionUnknown}, err
	}
	in, err := decodeModelIntent(out)
	if err != nil {
		return Intent{Action: ActionUnknown}, err
	}
	return in, nil
}

// runModel is the DOCUMENTED STUB the local Windows agent wires to the real
// llama.cpp exec (mirror execModelRunner in model_transformer.go: exec.Command
// with -m model --temp 0 --seed 42 --no-display-prompt -p <prompt>, capture
// stdout). Today it returns notReady so PickParser's selection degrades cleanly.
func (mp *ModelParser) runModel(instruction string, proj music.Project) (string, error) {
	return "", errModelStub
}

// errModelStub signals the unwired model so callers degrade silently.
var errModelStub = errStub("studio model parser: exec stub not wired (local-agent task)")

type errStub string

func (e errStub) Error() string { return string(e) }

// buildModelPrompt assembles the deterministic prompt. Exposed (lower-case but
// unit-tested in-package) so the contract above is verifiable without a model.
func buildModelPrompt(instruction string, proj music.Project) string {
	tracks := make([]string, 0, len(proj.Tracks))
	for _, t := range proj.Tracks {
		tracks = append(tracks, t.ID)
	}
	buses := make([]string, 0, len(proj.Buses))
	for _, b := range proj.Buses {
		buses = append(buses, b.ID)
	}
	summary, _ := marshalCompact(map[string]any{"tracks": tracks, "buses": buses})
	return strings.Join([]string{
		"You are a studio-setup assistant. Map the instruction to ONE edit on the project graph.",
		"Project (compact JSON):",
		summary,
		"Instruction: " + instruction,
		"Reply with ONLY this JSON object:",
		`{"action":"<sidechain|route|insertChain|setVST|setGain|unknown>","source":"","target":"","band":"","vst":"","gainDb":0,"hasGain":false,"note":""}`,
		"Use only track/bus ids from the project. action must be one of the listed values.",
	}, "\n")
}

// decodeModelIntent parses the model's strict-JSON object into an Intent. It
// reuses the same first-balanced-object scan style as model_transformer.go and
// validates the Action; an unknown action degrades to ActionUnknown.
func decodeModelIntent(stdout string) (Intent, error) {
	obj := extractFirstJSONObject(stdout)
	if obj == "" {
		return Intent{Action: ActionUnknown}, errStub("studio model parser: no JSON object in output")
	}
	var raw struct {
		Action  string  `json:"action"`
		Source  string  `json:"source"`
		Target  string  `json:"target"`
		Band    string  `json:"band"`
		VST     string  `json:"vst"`
		GainDB  float64 `json:"gainDb"`
		HasGain bool    `json:"hasGain"`
		Note    string  `json:"note"`
	}
	if err := jsonUnmarshal([]byte(obj), &raw); err != nil {
		return Intent{Action: ActionUnknown}, err
	}
	return Intent{
		Action:  validAction(raw.Action),
		Source:  raw.Source,
		Target:  raw.Target,
		Band:    raw.Band,
		VST:     raw.VST,
		GainDB:  raw.GainDB,
		HasGain: raw.HasGain,
		Note:    raw.Note,
	}, nil
}

// validAction maps a model string to a known Action, degrading to ActionUnknown.
func validAction(s string) Action {
	switch Action(strings.ToLower(strings.TrimSpace(s))) {
	case ActionSidechain:
		return ActionSidechain
	case ActionRoute:
		return ActionRoute
	case ActionInsertChain:
		return ActionInsertChain
	case ActionSetVST:
		return ActionSetVST
	case ActionSetGain:
		return ActionSetGain
	default:
		return ActionUnknown
	}
}

// ─── PickParser ────────────────────────────────────────────────────────────────

// PickParser returns a *ModelParser when both the llama.cpp binary and the
// instruct GGUF exist on disk; otherwise it returns DeterministicParser so
// becky-wire works everywhere (headless CI, Jordan's PC before the model is
// downloaded, any missing-binary edge case). This is the single call-site for
// cmd/wire. Set BECKY_WIRE_BIN and BECKY_WIRE_MODEL to activate the model.
//
// Note: even when the model is present, the SAFE production wiring is to try the
// model and fall back to DeterministicParser on any per-call error — see the
// FallbackParser the cmd uses.
func PickParser() Parser {
	bin, model := resolveWirePaths()
	if _, err := os.Stat(bin); err != nil {
		return DeterministicParser{}
	}
	if _, err := os.Stat(model); err != nil {
		return DeterministicParser{}
	}
	return &ModelParser{bin: bin, model: model}
}

// resolveWirePaths returns (bin, model) using env vars first, then defaults.
func resolveWirePaths() (bin, model string) {
	bin = firstNonEmpty(os.Getenv(EnvWireBin), DefaultWireBin)
	model = firstNonEmpty(os.Getenv(EnvWireModel), DefaultWireModel)
	return bin, model
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// FallbackParser wraps a primary parser (typically a *ModelParser) and falls back
// to a secondary (the DeterministicParser) whenever the primary errors OR returns
// ActionUnknown. This is the robust production combination: the model widens
// coverage, the deterministic grammar guarantees the shipped sentences.
type FallbackParser struct {
	Primary   Parser
	Secondary Parser
}

// Parse tries Primary, then Secondary. It never returns a non-nil error — the
// final fallback is the deterministic parser, which always yields a clean Intent.
func (fp FallbackParser) Parse(instruction string, proj music.Project) (Intent, error) {
	if fp.Primary != nil {
		if in, err := fp.Primary.Parse(instruction, proj); err == nil && in.Action != ActionUnknown {
			return in, nil
		}
	}
	if fp.Secondary != nil {
		return fp.Secondary.Parse(instruction, proj)
	}
	return DeterministicParser{}.Parse(instruction, proj)
}
