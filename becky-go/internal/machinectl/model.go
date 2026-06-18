package machinectl

// model.go — the cloud/local split for machinectl's instruction parsing, mirroring
// internal/drumcmd/model.go and internal/canvas/model_transformer.go's
// PickParser/PickTransformer convention.
//
// Two parsing paths sit behind the Parser interface (machinectl.go):
//
//  1. DeterministicParser — fully offline, handles every documented phrase NOW
//     (machinectl.go). This is the testable core and the guaranteed fallback.
//  2. ModelParser          — the FAST BACKGROUND MODEL path: a small instruct GGUF
//     run via llama.cpp's one-shot completion binary, for free-form phrasings the
//     keyword parser can't ("can you make the kick hit harder and roll the hats").
//     It is a DOCUMENTED STUB here — the cloud agent does NOT run a model; the
//     local Windows agent wires the exec call. It SILENT-DEGRADES to the
//     deterministic parser on any failure.
//
// ── MODEL CONTRACT (for the local agent to implement execRunModel) ────────────
//
// Binary  : a llama.cpp one-shot completion tool (llama-completion.exe), NOT the
//           interactive llama-cli chat TUI. Same family becky-canvas/becky-drum use.
// Model   : a SMALL instruct GGUF (1–4B Qwen3-Instruct / Gemma-IT class). Mapping a
//           drum-machine instruction to one structured edit is a tiny task, so the
//           fast background model is right-sized.
// Settings: --temp 0 --seed 42  (deterministic: same input → same output). -n 192
//           is plenty for the JSON.
//
// INPUT the model receives (built by buildMachinePrompt):
//   - the user instruction (verbatim)
//   - a COMPACT machine summary (MachineSummary): tempo + the 16 pad names + the
//     active pattern's step count, so the model can resolve "the snare" to a pad
//     and pick sane values.
//
// OUTPUT the model must return — ONE strict-JSON object, schema:
//
//	{
//	  "action": "beat|load_kit|set_pad_sample|set_pad_level|set_pad_pan|set_pad_pitch|
//	             set_pad_decay|set_choke|mute_pad|solo_pad|set_tempo|set_swing|
//	             transport|new_pattern|duplicate_pattern|add_scene|genre_starter|unknown",
//	  "pad":    <int 0-based pad index, -1 if N/A>,
//	  "value":  <float: bpm | level 0-1 | pan -1..1 | semitones | decay s | swing 0.5-0.75>,
//	  "group":  <int choke group, 0 if N/A>,
//	  "on":     <bool: mute/solo state>,
//	  "sample": "<sample name/path for set_pad_sample>",
//	  "kit":    "<kit name for load_kit>",
//	  "transport": "<play|stop|>",
//	  "genre":  "<trap|boom-bap|house|four-on-the-floor for genre_starter>",
//	  "drum":   "<for action=beat: the raw beat instruction to hand to drumcmd>",
//	  "note":   "<one plain-English sentence of what was understood>"
//	}
//
// parseMachineJSON maps that object to an Intent. For action=="beat" the "drum"
// field is re-parsed by drumcmd (so the existing transform engine stays the single
// source of truth for beat edits). mapMachineAction converts the action string to
// the Action enum (unknown strings → Unknown, degrade-safe).
//
// ── ENV VARS (resolution order: env → hardcoded default) ──────────────────────
//
//	BECKY_MACHINE_BIN    path to the llama.cpp one-shot completion binary
//	                     default: C:/llama.cpp/build/bin/llama-completion.exe
//	BECKY_MACHINE_MODEL  path to the small instruct GGUF
//	                     default: X:/AI-2/becky-tools/models/Qwen3-4B-Instruct-2507-Q4_K_M.gguf
//
// execRunModel is the ONLY thing the local agent must fill in. Everything else
// (prompt build, JSON parse, action mapping, degrade) is implemented and tested.

import (
	"encoding/json"
	"errors"
	"os"
	"strings"

	"becky-go/internal/drumcmd"
	"becky-go/internal/drummachine"
)

// ─── env var names + defaults ─────────────────────────────────────────────────

const (
	// EnvMachineBin overrides the llama.cpp one-shot completion binary path.
	EnvMachineBin = "BECKY_MACHINE_BIN"
	// EnvMachineModel overrides the small instruct GGUF path.
	EnvMachineModel = "BECKY_MACHINE_MODEL"

	// DefaultMachineBin is the llama.cpp ONE-SHOT completion binary on Jordan's PC.
	DefaultMachineBin = `C:/llama.cpp/build/bin/llama-completion.exe`
	// DefaultMachineModel is a becky-owned small instruct GGUF already on the PC.
	DefaultMachineModel = `X:/AI-2/becky-tools/models/Qwen3-4B-Instruct-2507-Q4_K_M.gguf`
)

// PickParser returns a ModelParser when both the llama.cpp binary and the GGUF
// model exist on disk; otherwise it returns the DeterministicParser. This is the
// single call-site for the GUI. Set BECKY_MACHINE_BIN and BECKY_MACHINE_MODEL to
// activate the model path.
//
// NOTE FOR THE LOCAL AGENT: the model exec (execRunModel) is a documented STUB
// returning errModelStub — so even on a machine that HAS the binary+model, the
// parser degrades to deterministic parsing until you implement execRunModel. This
// keeps the cloud build honest (no model is ever run here).
func PickParser() Parser {
	bin := firstNonEmpty(os.Getenv(EnvMachineBin), DefaultMachineBin)
	model := firstNonEmpty(os.Getenv(EnvMachineModel), DefaultMachineModel)
	if !fileExists(bin) || !fileExists(model) {
		return DeterministicParser{}
	}
	return ModelParser{bin: bin, model: model, run: execRunModel}
}

// ─── ModelParser (the stub the local agent wires) ─────────────────────────────

// ModelParser implements Parser by calling a local instruct model, and DEGRADES
// to the DeterministicParser when the model is unavailable, errors, or returns
// Unknown. It never panics and never blocks the user from getting a result.
type ModelParser struct {
	bin   string
	model string
	// run is the model invocation seam; production wires execRunModel, tests
	// inject a fake. When nil, the parser behaves as deterministic-only.
	run func(bin, model, prompt string) (string, error)
}

// Parse asks the model first; on any failure it falls back to the deterministic
// parser.
func (p ModelParser) Parse(instruction string, m *drummachine.Machine) (Intent, error) {
	fallback := parseDeterministic(instruction, m)
	if p.run == nil {
		return fallback, nil
	}
	prompt := buildMachinePrompt(instruction, SummarizeMachine(m))
	out, err := p.run(p.bin, p.model, prompt)
	if err != nil {
		return fallback, nil // degrade silently
	}
	in, perr := parseMachineJSON(out, instruction, m)
	if perr != nil || in.Action == Unknown {
		return fallback, nil // bad/empty/unknown model output → deterministic parser
	}
	return in, nil
}

// errModelStub marks the unimplemented model exec. The local agent replaces the
// body of execRunModel with the real os/exec call (see the contract at the top).
var errModelStub = errors.New("machinectl: model exec is a stub — wire execRunModel on the local machine")

// execRunModel is the STUB the local Windows agent must implement: run the
// llama.cpp one-shot completion binary with the prompt and deterministic flags
// (--temp 0 --seed 42 -n 192 --no-display-prompt -m model -p prompt) and return
// stdout. Until then it returns errModelStub so the parser degrades to keywords.
//
// Reference implementation (the local agent uncomments / adapts this):
//
//	cmd := exec.Command(bin, "-m", model, "--temp", "0", "--seed", "42",
//	    "-n", "192", "--no-display-prompt", "-p", prompt)
//	var out, errb strings.Builder
//	cmd.Stdout, cmd.Stderr = &out, &errb
//	if err := cmd.Run(); err != nil { return out.String(), err }
//	return out.String(), nil
func execRunModel(bin, model, prompt string) (string, error) {
	return "", errModelStub
}

// ─── machine summary (grounds the prompt) ─────────────────────────────────────

// MachineSummary is the compact view of a Machine passed to the model so it can
// resolve pad names and pick sane values. No per-step data — the model only needs
// to classify intent and target a pad.
type MachineSummary struct {
	Tempo    float64  `json:"tempo"`
	Pads     []string `json:"pads"`     // pad index → name (length PadCount)
	Steps    int      `json:"steps"`    // active pattern step count
	Swing    float64  `json:"swing"`    // active pattern swing
	Patterns int      `json:"patterns"` // count
	Scenes   int      `json:"scenes"`   // count
}

// SummarizeMachine builds a MachineSummary from a Machine for the model prompt.
func SummarizeMachine(m *drummachine.Machine) MachineSummary {
	if m == nil {
		return MachineSummary{}
	}
	out := MachineSummary{
		Tempo:    m.Tempo,
		Patterns: m.PatternCount(),
		Scenes:   m.SceneCount(),
	}
	for _, p := range m.Kit.Pads {
		out.Pads = append(out.Pads, p.Name)
	}
	pat := activePattern(m)
	if validPattern(m, pat) {
		out.Steps = m.Bank.Patterns[pat].Steps
		out.Swing = m.Bank.Patterns[pat].Swing
	}
	return out
}

// ─── prompt builder ───────────────────────────────────────────────────────────

// buildMachinePrompt assembles the one-shot prompt. Compact and explicit so the
// small model returns clean strict JSON.
func buildMachinePrompt(instruction string, summary MachineSummary) string {
	sumJSON, _ := json.Marshal(summary) // always marshallable
	return strings.Join([]string{
		`You are becky's drum-machine control parser. Convert a producer's plain-English`,
		`instruction into ONE JSON edit command for the 16-pad drum machine described below.`,
		``,
		`Machine summary (JSON):`,
		string(sumJSON),
		``,
		`Instruction: ` + instruction,
		``,
		`Reply with ONLY one JSON object, this exact schema:`,
		`{"action":"<beat|load_kit|set_pad_sample|set_pad_level|set_pad_pan|set_pad_pitch|set_pad_decay|set_choke|mute_pad|solo_pad|set_tempo|set_swing|transport|new_pattern|duplicate_pattern|add_scene|genre_starter|unknown>",`,
		`"pad":<int 0-based, -1 if N/A>,"value":<float>,"group":<int>,"on":<bool>,`,
		`"sample":"<name or empty>","kit":"<name or empty>","transport":"<play|stop|>",`,
		`"genre":"<trap|boom-bap|house|four-on-the-floor or empty>","drum":"<raw beat instruction or empty>","note":"<one sentence>"}`,
		``,
		`Rules: action MUST be one of the listed values. pad is 0-based (producer "pad 1" = 0).`,
		`For a beat/pattern transform (half-time, humanize, fill, swing groove, variations, busier/sparser,`,
		`tighten), use action "beat" and put the original instruction in "drum".`,
		`Output ONLY the JSON object — no markdown, no explanation.`,
	}, "\n")
}

// ─── response parser ──────────────────────────────────────────────────────────

// machineJSON mirrors the strict JSON contract the model must return.
type machineJSON struct {
	Action    string  `json:"action"`
	Pad       int     `json:"pad"`
	Value     float64 `json:"value"`
	Group     int     `json:"group"`
	On        bool    `json:"on"`
	Sample    string  `json:"sample"`
	Kit       string  `json:"kit"`
	Transport string  `json:"transport"`
	Genre     string  `json:"genre"`
	Drum      string  `json:"drum"`
	Note      string  `json:"note"`
}

// parseMachineJSON extracts the first JSON object from the model's stdout and maps
// it to an Intent. Chatter before/after the object is tolerated. For action=="beat"
// the "drum" text is re-parsed by drumcmd so the transform engine stays the single
// source of truth.
func parseMachineJSON(stdout, raw string, m *drummachine.Machine) (Intent, error) {
	obj := extractFirstJSONObject(stdout)
	if obj == "" {
		return Intent{Action: Unknown, Pad: -1, Raw: raw}, errors.New("no JSON object in model output")
	}
	var mj machineJSON
	mj.Pad = -1 // default to unresolved
	if err := json.Unmarshal([]byte(obj), &mj); err != nil {
		return Intent{Action: Unknown, Pad: -1, Raw: raw}, err
	}

	in := Intent{
		Action:     mapMachineAction(mj.Action),
		Pad:        mj.Pad,
		Value:      mj.Value,
		Group:      mj.Group,
		On:         mj.On,
		SamplePath: strings.TrimSpace(mj.Sample),
		KitName:    strings.TrimSpace(mj.Kit),
		Transport:  mapTransport(mj.Transport),
		Genre:      strings.ToLower(strings.TrimSpace(mj.Genre)),
		Note:       mj.Note,
		Raw:        raw,
	}
	if in.Action == Beat {
		drumText := mj.Drum
		if strings.TrimSpace(drumText) == "" {
			drumText = raw // fall back to the original instruction
		}
		in.Drum = drumcmd.ParseKeyword(drumText, DefaultSeed)
		if in.Drum.Action == drumcmd.Unknown {
			in.Action = Unknown // a beat with no recognised transform degrades
		}
	}
	return in, nil
}

// mapMachineAction converts the model's action string to the Action enum
// (degrade-safe: unknown strings → Unknown).
func mapMachineAction(s string) Action {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "beat":
		return Beat
	case "load_kit", "load-kit", "loadkit":
		return LoadKit
	case "set_pad_sample", "set-pad-sample":
		return SetPadSample
	case "set_pad_level", "set-pad-level", "level":
		return SetPadLevel
	case "set_pad_pan", "set-pad-pan", "pan":
		return SetPadPan
	case "set_pad_pitch", "set-pad-pitch", "pitch":
		return SetPadPitch
	case "set_pad_decay", "set-pad-decay", "decay":
		return SetPadDecay
	case "set_choke", "set-choke", "choke":
		return SetChoke
	case "mute_pad", "mute-pad", "mute":
		return MutePad
	case "solo_pad", "solo-pad", "solo":
		return SoloPad
	case "set_tempo", "set-tempo", "tempo":
		return SetTempo
	case "set_swing", "set-swing", "swing":
		return SetSwing
	case "transport", "play", "stop":
		return Transport
	case "new_pattern", "new-pattern":
		return NewPattern
	case "duplicate_pattern", "duplicate-pattern":
		return DuplicatePattern
	case "add_scene", "add-scene":
		return AddScene
	case "genre_starter", "genre-starter", "genre":
		return GenreStarter
	default:
		return Unknown
	}
}

// mapTransport normalises a transport verb string.
func mapTransport(s string) TransportVerb {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "play":
		return TransportPlay
	case "stop":
		return TransportStop
	default:
		return TransportNone
	}
}

// extractFirstJSONObject returns the first balanced {…} object in s, or "".
// Mirrors drumcmd.extractFirstJSONObject (kept local to avoid a cross-package
// import).
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

// ─── tiny helpers (mirror drumcmd/model.go) ───────────────────────────────────

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
