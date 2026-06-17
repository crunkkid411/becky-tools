package drumcmd

// model.go — the cloud/local split for becky-drum's instruction parsing.
//
// Two parsing paths sit behind the Parser interface, mirroring
// internal/canvas/model_transformer.go's PickTransformer() pattern:
//
//  1. keywordParser — fully deterministic, offline, handles the documented
//     example sentences NOW (parse.go). This is the testable core and the
//     guaranteed fallback.
//  2. modelParser    — the FAST BACKGROUND MODEL path: a small instruct GGUF run
//     via llama.cpp's one-shot completion binary, used to understand free-form
//     phrasings the keyword parser can't ("can you make the groove breathe a
//     bit"). It is a DOCUMENTED STUB here — the cloud agent does NOT run a model;
//     the local Windows agent wires the exec call. It SILENT-DEGRADES to the
//     keyword parser on any failure.
//
// ── MODEL CONTRACT (for the local agent to implement runModel) ────────────────
//
// Binary  : a llama.cpp one-shot completion tool (llama-completion.exe), NOT the
//           interactive llama-cli chat TUI. Same family becky-canvas uses.
// Model   : a SMALL instruct GGUF (a 1–4B Qwen3-Instruct / Gemma-IT class model)
//           — drum-command parsing is a tiny classification task, so the fast
//           background model is right-sized; no need for the big model.
// Settings: --temp 0 --seed 42  (deterministic: same input → same output, the
//           offline+deterministic invariant). -n 128 is plenty for the JSON.
//
// INPUT the model receives (built by buildPrompt):
//   - the user instruction (verbatim)
//   - a COMPACT grid summary (GridSummary): lanes present + hit counts + bars,
//     so the model can resolve "the snare" to a real lane and pick a sane beat.
//
// OUTPUT the model must return — ONE strict-JSON object, schema:
//
//	{
//	  "action": "half_time|double_time|humanize|fill|swing|variations|density|quantize|unknown",
//	  "lane":   "snare|hat|kick|... or empty for all",
//	  "beat":   <int 1-based, 0 if N/A>,
//	  "count":  <int, variation count; 0 if N/A>,
//	  "up":     <bool, density direction; true=busier>,
//	  "swing":  <float 0.5-0.75; 0 = default>,
//	  "note":   "<one plain-English sentence of what was understood>"
//	}
//
// parseModelJSON maps that object to a DrumCommand. mapAction() converts the
// action string to the Action enum (unknown strings → Unknown, degrade-safe).
//
// ── ENV VARS (resolution order: env → hardcoded default) ──────────────────────
//
//	BECKY_DRUM_BIN    path to the llama.cpp one-shot completion binary
//	                  default: C:/llama.cpp/build/bin/llama-completion.exe
//	BECKY_DRUM_MODEL  path to the small instruct GGUF
//	                  default: X:/AI-2/becky-tools/models/Qwen3-4B-Instruct-2507-Q4_K_M.gguf
//
// runModel is the ONLY thing the local agent must fill in. Everything else
// (prompt build, JSON parse, action mapping, degrade) is implemented and tested.

import (
	"encoding/json"
	"errors"
	"os"
	"strings"

	"becky-go/internal/dawmodel"
)

// Parser turns a plain-English instruction + grid summary into a DrumCommand.
type Parser interface {
	Parse(instruction string, summary GridSummary, seed int64) DrumCommand
}

// GridSummary is the compact view of a grid passed to the model so it can ground
// lane names and beat numbers. It carries no per-cell data (the model only needs
// to classify intent and pick a target), keeping the prompt small and fast.
type GridSummary struct {
	Bars  int           `json:"bars"`
	Steps int           `json:"steps"`
	Lanes []LaneSummary `json:"lanes"`
}

// LaneSummary is one lane in a GridSummary: its name and how many hits it has.
type LaneSummary struct {
	Name string `json:"name"`
	Note int    `json:"note"`
	Hits int    `json:"hits"`
}

// ─── env var names + defaults (mirrors canvas.model_transformer) ──────────────

const (
	// EnvDrumBin overrides the llama.cpp one-shot completion binary path.
	EnvDrumBin = "BECKY_DRUM_BIN"
	// EnvDrumModel overrides the small instruct GGUF path.
	EnvDrumModel = "BECKY_DRUM_MODEL"

	// DefaultDrumBin is the llama.cpp ONE-SHOT completion binary on Jordan's PC.
	DefaultDrumBin = `C:/llama.cpp/build/bin/llama-completion.exe`
	// DefaultDrumModel is a becky-owned small instruct GGUF already on the PC.
	DefaultDrumModel = `X:/AI-2/becky-tools/models/Qwen3-4B-Instruct-2507-Q4_K_M.gguf`
)

// ─── keywordParser (the deterministic core) ───────────────────────────────────

// keywordParser implements Parser using the offline ParseKeyword.
type keywordParser struct{}

func (keywordParser) Parse(instruction string, _ GridSummary, seed int64) DrumCommand {
	return ParseKeyword(instruction, seed)
}

// KeywordParser returns the always-available deterministic parser.
func KeywordParser() Parser { return keywordParser{} }

// ─── modelParser (the stub the local agent wires) ─────────────────────────────

// modelParser implements Parser by calling a local instruct model, and DEGRADES
// to the keyword parser when the model is unavailable, errors, or returns
// Unknown. It never panics and never blocks the user from getting a result.
type modelParser struct {
	bin   string
	model string
	// run is the model invocation seam; production wires execRunModel, tests
	// inject a fake. When nil, the parser behaves as keyword-only.
	run func(bin, model, prompt string) (string, error)
}

// Parse asks the model first; on any failure it falls back to ParseKeyword.
func (m modelParser) Parse(instruction string, summary GridSummary, seed int64) DrumCommand {
	fallback := ParseKeyword(instruction, seed)
	if m.run == nil {
		return fallback
	}
	prompt := buildPrompt(instruction, summary)
	out, err := m.run(m.bin, m.model, prompt)
	if err != nil {
		return fallback // degrade silently to the keyword parser
	}
	cmd, err := parseModelJSON(out, seed, instruction)
	if err != nil || cmd.Action == Unknown {
		return fallback // bad/empty/unknown model output → keyword parser
	}
	return cmd
}

// PickParser returns a modelParser when both the llama.cpp binary and the GGUF
// model exist on disk; otherwise it returns the deterministic KeywordParser.
// This is the single call-site for cmd/drum. Set BECKY_DRUM_BIN and
// BECKY_DRUM_MODEL to activate the model path.
//
// NOTE FOR THE LOCAL AGENT: the model exec (execRunModel) is a documented STUB
// returning errModelStub — so even on a machine that HAS the binary+model, the
// parser degrades to keyword parsing until you implement execRunModel. This keeps
// the cloud build honest (no model is ever run here).
func PickParser() Parser {
	bin := firstNonEmpty(os.Getenv(EnvDrumBin), DefaultDrumBin)
	model := firstNonEmpty(os.Getenv(EnvDrumModel), DefaultDrumModel)
	if !fileExists(bin) || !fileExists(model) {
		return KeywordParser()
	}
	return modelParser{bin: bin, model: model, run: execRunModel}
}

// errModelStub marks the unimplemented model exec. The local agent replaces the
// body of execRunModel with the real os/exec call (see the contract at the top).
var errModelStub = errors.New("drumcmd: model exec is a stub — wire execRunModel on the local machine")

// execRunModel is the STUB the local Windows agent must implement: run the
// llama.cpp one-shot completion binary with the prompt and deterministic flags
// (--temp 0 --seed 42 -n 128 --no-display-prompt -m model -p prompt) and return
// stdout. Until then it returns errModelStub so the parser degrades to keywords.
//
// Reference implementation (the local agent uncomments / adapts this):
//
//	cmd := exec.Command(bin, "-m", model, "--temp", "0", "--seed", "42",
//	    "-n", "128", "--no-display-prompt", "-p", prompt)
//	var out, errb strings.Builder
//	cmd.Stdout, cmd.Stderr = &out, &errb
//	if err := cmd.Run(); err != nil { return out.String(), err }
//	return out.String(), nil
func execRunModel(bin, model, prompt string) (string, error) {
	return "", errModelStub
}

// ─── prompt + response (implemented + tested; the model boundary is the only stub) ─

// buildPrompt assembles the one-shot prompt. Compact and explicit so the small
// model returns clean strict JSON.
func buildPrompt(instruction string, summary GridSummary) string {
	sumJSON, _ := json.Marshal(summary) // always marshallable
	return strings.Join([]string{
		`You are becky-drum's instruction parser. Convert a producer's plain-English drum`,
		`instruction into one JSON command. The current beat is summarized below.`,
		``,
		`Grid summary (JSON):`,
		string(sumJSON),
		``,
		`Instruction: ` + instruction,
		``,
		`Reply with ONLY one JSON object, this exact schema:`,
		`{"action":"<half_time|double_time|humanize|fill|swing|variations|density|quantize|unknown>",`,
		`"lane":"<lane name or empty for all>","beat":<int 0 if N/A>,"count":<int 0 if N/A>,`,
		`"up":<bool>,"swing":<float 0.5-0.75, 0=default>,"note":"<one sentence>"}`,
		``,
		`Rules: action MUST be one of the listed values. lane is "snare","hat","kick", etc., or "" for all.`,
		`Output ONLY the JSON object — no markdown, no explanation.`,
	}, "\n")
}

// modelJSON mirrors the strict JSON contract the model must return.
type modelJSON struct {
	Action string  `json:"action"`
	Lane   string  `json:"lane"`
	Beat   int     `json:"beat"`
	Count  int     `json:"count"`
	Up     bool    `json:"up"`
	Swing  float64 `json:"swing"`
	Note   string  `json:"note"`
}

// parseModelJSON extracts the first JSON object from the model's stdout and maps
// it to a DrumCommand. Chatter before/after the object is tolerated.
func parseModelJSON(stdout string, seed int64, raw string) (DrumCommand, error) {
	if seed <= 0 {
		seed = DefaultSeed
	}
	obj := extractFirstJSONObject(stdout)
	if obj == "" {
		return DrumCommand{Action: Unknown, Seed: seed, Raw: raw}, errors.New("no JSON object in model output")
	}
	var mj modelJSON
	if err := json.Unmarshal([]byte(obj), &mj); err != nil {
		return DrumCommand{Action: Unknown, Seed: seed, Raw: raw}, err
	}
	return DrumCommand{
		Action: mapAction(mj.Action),
		Lane:   strings.ToLower(strings.TrimSpace(mj.Lane)),
		Beat:   mj.Beat,
		Count:  mj.Count,
		Up:     mj.Up,
		Swing:  mj.Swing,
		Note:   mj.Note,
		Seed:   seed,
		Raw:    raw,
	}, nil
}

// mapAction converts the model's action string to the Action enum (degrade-safe).
func mapAction(s string) Action {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "half_time", "half-time", "halftime":
		return HalfTime
	case "double_time", "double-time", "doubletime":
		return DoubleTime
	case "humanize", "humanise":
		return Humanize
	case "fill", "roll":
		return Fill
	case "swing", "shuffle":
		return Swing
	case "variations", "variation":
		return Variations
	case "density":
		return Density
	case "quantize", "quantise":
		return Quantize
	default:
		return Unknown
	}
}

// extractFirstJSONObject returns the first balanced {…} object in s, or "".
// Mirrors canvas.extractFirstJSON (kept local to avoid a cross-package import).
func extractFirstJSONObject(s string) string {
	start := strings.Index(s, "{")
	if start < 0 {
		return ""
	}
	depth, inStr, esc := 0, false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		switch {
		case esc:
			esc = false
		case c == '\\' && inStr:
			esc = true
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

// ─── tiny helpers ─────────────────────────────────────────────────────────────

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func fileExists(p string) bool {
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}

// SummarizeGrid builds a GridSummary from a DrumGrid for the model prompt.
func SummarizeGrid(g *dawmodel.DrumGrid) GridSummary {
	if g == nil {
		return GridSummary{}
	}
	out := GridSummary{Bars: g.Bars, Steps: g.Steps}
	for _, ln := range g.Lanes {
		hits := 0
		for _, on := range ln.On {
			if on {
				hits++
			}
		}
		out.Lanes = append(out.Lanes, LaneSummary{Name: ln.Name, Note: ln.Note, Hits: hits})
	}
	return out
}
