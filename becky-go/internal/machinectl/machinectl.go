// Package machinectl is the WORDS→EDITS translator for becky's 16-pad drum
// machine. The producer's hard requirement is: "AI controls the GUI unless I
// manually click." So the AI must produce the SAME structured edits a mouse click
// would make against the internal/drummachine model — and the GUI applies them.
// This package is that translator: plain English in → a normalized Intent →
// Apply(machine, intent) → a NEW machine plus a plain-English summary the GUI can
// echo back ("Loaded the 808 kit", "Made it half-time").
//
// It owns NO sound, NO GUI, NO cgo, NO build tags, and adds NO module deps — it is
// pure deterministic Go built on three existing packages:
//
//   - internal/drummachine — the immutable Machine model + edit methods (the spine).
//   - internal/drumcmd     — the EXISTING plain-English → drum-pattern transform
//     engine (half-time, humanize, fill, swing, variations, density, quantize).
//     Beat edits delegate to it via the Machine's Pattern↔DrumGrid bridge; this
//     package does NOT reimplement those transforms.
//   - internal/habits      — preference learning: each applied edit is logged
//     best-effort so becky learns the producer's recurring moves.
//
// Two parsing paths sit behind the Parser interface (see model.go), mirroring
// internal/drumcmd's and internal/canvas's PickParser/PickTransformer convention:
//
//  1. DeterministicParser — fully offline, handles every documented phrase NOW.
//  2. ModelParser          — a fast-background-model STUB the local Windows agent
//     wires; it SILENT-DEGRADES to the deterministic parser on any failure.
//
// Invariants (becky house rules — see CLAUDE.md):
//   - Immutable: Apply never mutates its input Machine; it deep-copies first.
//   - Deterministic: same instruction + same machine ⇒ same Intent and same edit.
//   - Degrade-never-crash: an unrecognised phrase yields Action Unknown with a
//     friendly note and the machine unchanged — never a panic, never an error exit.
package machinectl

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"becky-go/internal/drumcmd"
	"becky-go/internal/drummachine"
	"becky-go/internal/habits"
)

// Action is the kind of edit a plain-English instruction maps to. Unknown is the
// degrade case: an instruction becky doesn't recognise becomes Action Unknown with
// a friendly note and no change.
type Action int

const (
	// Unknown — instruction not recognised; Apply returns the machine unchanged
	// plus a friendly note. Never an error, never a crash.
	Unknown Action = iota

	// ── Beat editing (delegated to internal/drumcmd) ──
	// Beat is the umbrella action for any pattern transform; the specific
	// transform is carried in Intent.Drum (a drumcmd.DrumCommand).
	Beat

	// ── Kit / pad edits ──
	LoadKit      // "load my 808 kit" / "use the <name> kit" — GUI/engine resolves the path
	SetPadSample // "put a clap on pad 5" / "swap the snare"
	SetPadLevel  // "make the kick louder" / "turn the snare down"
	SetPadPan    // "pan the hats left"
	SetPadPitch  // "pitch the kick down"
	SetPadDecay  // "shorten the snare" / "let the kick ring"
	SetChoke     // "choke the hats together"
	MutePad      // "mute the clap"
	SoloPad      // "solo the kick"

	// ── Transport / feel ──
	SetTempo  // "set the tempo to 140"
	SetSwing  // "more swing" / "swing it 60%"
	Transport // "play" / "stop" — machinectl makes no sound; it signals the GUI

	// ── Structure ──
	NewPattern       // "new pattern"
	DuplicatePattern // "duplicate this pattern"
	AddScene         // "add a scene"

	// ── Genre starters (deterministic, built-in templates) ──
	GenreStarter // "make a trap beat" / "four on the floor" / "boom bap"
)

// String renders an Action for logs, summaries, and tests.
func (a Action) String() string {
	switch a {
	case Beat:
		return "beat"
	case LoadKit:
		return "load-kit"
	case SetPadSample:
		return "set-pad-sample"
	case SetPadLevel:
		return "set-pad-level"
	case SetPadPan:
		return "set-pad-pan"
	case SetPadPitch:
		return "set-pad-pitch"
	case SetPadDecay:
		return "set-pad-decay"
	case SetChoke:
		return "set-choke"
	case MutePad:
		return "mute-pad"
	case SoloPad:
		return "solo-pad"
	case SetTempo:
		return "set-tempo"
	case SetSwing:
		return "set-swing"
	case Transport:
		return "transport"
	case NewPattern:
		return "new-pattern"
	case DuplicatePattern:
		return "duplicate-pattern"
	case AddScene:
		return "add-scene"
	case GenreStarter:
		return "genre-starter"
	default:
		return "unknown"
	}
}

// TransportVerb is the requested transport action ("play"/"stop"). machinectl
// never makes sound — Apply returns the machine unchanged and the GUI reads this
// verb to drive the engine.
type TransportVerb string

const (
	// TransportNone means no transport action.
	TransportNone TransportVerb = ""
	// TransportPlay asks the GUI/engine to start playback.
	TransportPlay TransportVerb = "play"
	// TransportStop asks the GUI/engine to stop playback.
	TransportStop TransportVerb = "stop"
)

// Intent is the normalized, resolved edit a Parser produces from one instruction.
// Fields not relevant to the Action carry zero values. It is the structured
// payload the GUI hands to Apply (and may inspect — e.g. read Transport for
// play/stop, or KitName to resolve a kit path).
type Intent struct {
	Action Action `json:"action"`

	// Pad is a resolved pad index (0..15) for pad-scoped actions; -1 when not
	// applicable / unresolved.
	Pad int `json:"pad"`

	// Value is the primary numeric argument: tempo BPM (SetTempo), linear level
	// (SetPadLevel), pan -1..1 (SetPadPan), semitones (SetPadPitch), decay seconds
	// (SetPadDecay), swing ratio 0.5..0.75 (SetSwing).
	Value float64 `json:"value,omitempty"`

	// Group is the choke-group number for SetChoke.
	Group int `json:"group,omitempty"`

	// On is a boolean argument: mute/solo on-or-off (MutePad/SoloPad).
	On bool `json:"on,omitempty"`

	// SamplePath is the new sample path for SetPadSample (may be a bare name the
	// GUI resolves against the sample library).
	SamplePath string `json:"samplePath,omitempty"`

	// KitName is the requested kit name/dir for LoadKit (the GUI/engine resolves
	// it to a real folder — machinectl does not touch the filesystem).
	KitName string `json:"kitName,omitempty"`

	// Transport carries play/stop for Action Transport.
	Transport TransportVerb `json:"transport,omitempty"`

	// Genre is the starter genre name for GenreStarter ("trap", "boom-bap",
	// "four-on-the-floor", "house").
	Genre string `json:"genre,omitempty"`

	// Drum is the parsed drum-pattern transform for Action Beat (delegated to
	// internal/drumcmd). Its drumcmd.Action selects the specific transform.
	Drum drumcmd.DrumCommand `json:"drum,omitempty"`

	// Note is a friendly plain-English explanation, set on Unknown (and useful
	// elsewhere) — what becky understood (or didn't).
	Note string `json:"note,omitempty"`

	// Raw is the original instruction text, retained for logging.
	Raw string `json:"raw,omitempty"`
}

// Parser turns a plain-English instruction (in the context of a Machine, so it can
// resolve pad names to indices) into a normalized Intent.
type Parser interface {
	Parse(instruction string, m *drummachine.Machine) (Intent, error)
}

// DefaultSeed is the fixed seed handed to drumcmd so humanize/variations are
// reproducible (becky's offline+deterministic invariant).
const DefaultSeed = drumcmd.DefaultSeed

// ─── DeterministicParser (the offline core) ───────────────────────────────────

// DeterministicParser is the fully-offline keyword parser. It handles every
// documented phrase NOW with no model and no network, and is the guaranteed
// fallback the model path degrades to.
type DeterministicParser struct{}

// Parse implements Parser.
func (DeterministicParser) Parse(instruction string, m *drummachine.Machine) (Intent, error) {
	return parseDeterministic(instruction, m), nil
}

var (
	tempoRe = regexp.MustCompile(`\b(\d{2,3}(?:\.\d+)?)\s*(?:bpm)?\b`)
	padRe   = regexp.MustCompile(`\bpad\s+(\d{1,2})\b`)
	pctRe   = regexp.MustCompile(`(\d+)\s*%`)
)

// parseDeterministic is the priority-ordered keyword matcher. Order matters:
// more-specific intents are checked before broader ones (e.g. a beat transform is
// recognised before a generic "swing", transport before everything else trivial).
func parseDeterministic(instruction string, m *drummachine.Machine) Intent {
	raw := instruction
	s := strings.ToLower(strings.TrimSpace(instruction))
	in := Intent{Action: Unknown, Pad: -1, Raw: raw}

	if s == "" {
		in.Note = "Tell me what to do — e.g. 'make it half-time', 'make a trap beat', 'load my 808 kit', or 'set the tempo to 140'."
		return in
	}

	switch {
	// ── Transport (cheap, unambiguous) ──
	case isStop(s):
		in.Action = Transport
		in.Transport = TransportStop
		return in
	case isPlay(s):
		in.Action = Transport
		in.Transport = TransportPlay
		return in

	// ── Tempo (explicit number, before generic swing/feel) ──
	case containsAny(s, "tempo", "bpm") || (containsAny(s, "speed up", "slow down") && tempoRe.MatchString(s)):
		if bpm, ok := tempoFrom(s); ok {
			in.Action = SetTempo
			in.Value = bpm
			return in
		}
		// "tempo" with no number is unknown — fall through to default.

	// ── Kit load ──
	case containsAny(s, "kit"):
		in.Action = LoadKit
		in.KitName = kitNameFrom(raw)
		return in

	// ── Structure ──
	case containsAny(s, "duplicate") && containsAny(s, "pattern"):
		in.Action = DuplicatePattern
		return in
	case containsAny(s, "new pattern", "add a pattern", "another pattern", "add pattern", "create a pattern"):
		in.Action = NewPattern
		return in
	case containsAny(s, "add a scene", "new scene", "add scene", "another scene", "create a scene"):
		in.Action = AddScene
		return in

	// ── Mute / solo ──
	case containsAny(s, "unmute"):
		in.Action = MutePad
		in.On = false
		in.Pad = padFrom(s, m)
		return resolvedPad(in, "I couldn't tell which pad to unmute.")
	case containsAny(s, "mute"):
		in.Action = MutePad
		in.On = true
		in.Pad = padFrom(s, m)
		return resolvedPad(in, "I couldn't tell which pad to mute.")
	case containsAny(s, "unsolo", "un-solo"):
		in.Action = SoloPad
		in.On = false
		in.Pad = padFrom(s, m)
		return resolvedPad(in, "I couldn't tell which pad to unsolo.")
	case containsAny(s, "solo"):
		in.Action = SoloPad
		in.On = true
		in.Pad = padFrom(s, m)
		return resolvedPad(in, "I couldn't tell which pad to solo.")

	// ── Choke ──
	case containsAny(s, "choke"):
		in.Action = SetChoke
		in.Group = drummachine.DefaultSteps / 16 // == 1; the canonical hat choke group
		in.Pad = padFrom(s, m)
		return resolvedPad(in, "I couldn't tell which pad to choke.")

	// ── Pad sample edits ──
	case containsAny(s, "put a", "load a", "drop a", "use the", "swap", "replace") && looksLikeSampleEdit(s, m):
		return parseSampleEdit(s, raw, m)

	// ── Pad level ──
	// Match split phrasings too ("turn the snare down" → "turn"+"down").
	case containsAny(s, "louder", "turn up", "level up", "boost") ||
		containsAny(s, "quieter", "turn down", "level down", "softer", "quiet") ||
		(containsAny(s, "turn") && containsAny(s, " up", " down")):
		in.Action = SetPadLevel
		in.Pad = padFrom(s, m)
		in.Value = levelFrom(s, in.Pad, m)
		return resolvedPad(in, "I couldn't tell which pad to change the level of.")

	// ── Pad pan ──
	case containsAny(s, "pan"):
		in.Action = SetPadPan
		in.Pad = padFrom(s, m)
		in.Value = panFrom(s)
		return resolvedPad(in, "I couldn't tell which pad to pan.")

	// ── Pad pitch ──
	case containsAny(s, "pitch", "tune", "transpose"):
		in.Action = SetPadPitch
		in.Pad = padFrom(s, m)
		in.Value = pitchFrom(s)
		return resolvedPad(in, "I couldn't tell which pad to pitch.")

	// ── Pad decay ──
	case containsAny(s, "shorten", "shorter", "tighten the", "let it ring", "longer", "ring out", "decay", "longer tail"):
		// "tighten the X" here means decay; bare "tighten" → beat quantize (below).
		if pad := padFrom(s, m); pad >= 0 || containsAny(s, "decay") {
			in.Action = SetPadDecay
			in.Pad = pad
			in.Value = decayFrom(s)
			return resolvedPad(in, "I couldn't tell which pad to shorten.")
		}

	// ── Genre starters (before beat transforms; "make a trap beat" is a starter) ──
	case genreFrom(s) != "":
		in.Action = GenreStarter
		in.Genre = genreFrom(s)
		return in

	// ── Swing as a feel knob ("more swing", "swing it 60%") ──
	// Routed to SetSwing on the active pattern. (drumcmd also has a swing
	// transform, but on the Machine model swing is a first-class pattern field, so
	// "swing it" sets the pattern's Swing — the same edit the GUI's swing knob makes.)
	case containsAny(s, "swing", "shuffle") && !containsAny(s, "fill", "roll", "variation"):
		in.Action = SetSwing
		in.Value = swingFrom(s)
		return in
	}

	// ── Beat transforms: delegate parsing to drumcmd ──
	if dc := tryDrumParse(raw, m); dc.Action != drumcmd.Unknown {
		in.Action = Beat
		in.Drum = dc
		return in
	}

	// ── Nothing matched ──
	in.Note = "I didn't recognise \"" + strings.TrimSpace(raw) + "\". Try 'make it half-time', 'humanize the snare', 'make a trap beat', 'load my 808 kit', 'mute the clap', 'pan the hats left', or 'set the tempo to 140'."
	return in
}

// resolvedPad returns in unchanged if its Pad resolved, else converts it to a
// friendly Unknown (degrade, never crash on an unresolvable pad name).
func resolvedPad(in Intent, why string) Intent {
	if in.Pad >= 0 && in.Pad < drummachine.PadCount {
		return in
	}
	return Intent{Action: Unknown, Pad: -1, Raw: in.Raw, Note: why + " Try naming it ('the kick') or a pad number ('pad 5')."}
}

// tryDrumParse runs drumcmd's keyword parser over the instruction with a grid
// summary derived from the machine's ACTIVE pattern, so "the snare"/beat numbers
// ground against real lanes. Returns the parsed DrumCommand (Action Unknown when
// drumcmd doesn't recognise it).
func tryDrumParse(raw string, m *drummachine.Machine) drumcmd.DrumCommand {
	return drumcmd.ParseKeyword(raw, DefaultSeed)
}

// ─── field extractors ─────────────────────────────────────────────────────────

func isPlay(s string) bool {
	return s == "play" || s == "go" || s == "start" ||
		containsAny(s, "play it", "press play", "start playback", "start playing", "hit play")
}

func isStop(s string) bool {
	return s == "stop" || s == "halt" || s == "pause" ||
		containsAny(s, "stop it", "stop playback", "stop playing", "hit stop")
}

// tempoFrom extracts a BPM (40..300 sanity window).
func tempoFrom(s string) (float64, bool) {
	if m := tempoRe.FindStringSubmatch(s); m != nil {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil && v >= 40 && v <= 300 {
			return v, true
		}
	}
	return 0, false
}

// kitNameFrom pulls the kit name out of "load my 808 kit" / "use the trap kit".
// It strips framing words and the trailing "kit"; "" means "no specific name".
func kitNameFrom(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	// Drop everything up to and including the lead-in verb where present.
	for _, lead := range []string{"load the", "load my", "load a", "load", "use the", "use my", "use a", "use", "switch to the", "switch to", "give me the", "give me"} {
		if idx := strings.Index(s, lead); idx >= 0 {
			s = s[idx+len(lead):]
			break
		}
	}
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "kit")
	s = strings.TrimSpace(s)
	// Drop a leading possessive that survived ("my 808" when verb wasn't "load my").
	s = strings.TrimPrefix(s, "my ")
	return strings.TrimSpace(s)
}

// padNames is the spoken-name → default-pad-index map, derived from the default
// GM kit labels. Used when the instruction names a pad ("the kick") instead of a
// number. Names are matched against the LIVE machine first (renamed pads win);
// this is the fallback vocabulary.
var padNames = map[string]int{
	"kick": 0, "bd": 0, "bass drum": 0,
	"snare": 1, "sd": 1,
	"closed hat": 2, "closed hihat": 2, "hat": 2, "hats": 2, "hihat": 2, "hi-hat": 2, "hi hat": 2, "ch": 2,
	"open hat": 3, "open hihat": 3, "ohat": 3, "oh": 3,
	"clap": 4,
	"rim":  5, "rimshot": 5,
	"low tom": 6, "mid tom": 7, "hi tom": 8, "tom": 7,
	"crash": 9, "ride": 10,
	"shaker": 11, "tambourine": 12, "tamb": 12, "cowbell": 13, "cow bell": 13,
	"conga": 14, "perc": 15, "percussion": 15,
}

// padFrom resolves a pad reference to an index: an explicit "pad N" (1-based in
// speech → 0-based index), or a named pad matched against the live kit first then
// the default vocabulary. Returns -1 when nothing resolves.
func padFrom(s string, m *drummachine.Machine) int {
	// Explicit "pad N" — producers say pad 1..16, the model is 0..15.
	if mm := padRe.FindStringSubmatch(s); mm != nil {
		if n, err := strconv.Atoi(mm[1]); err == nil {
			idx := n - 1
			if idx >= 0 && idx < drummachine.PadCount {
				return idx
			}
		}
	}
	// Named pad against the LIVE kit (renamed pads win) — longest name first.
	if m != nil {
		best, bestLen := -1, 0
		for _, p := range m.Kit.Pads {
			name := strings.ToLower(strings.TrimSpace(p.Name))
			if name == "" {
				continue
			}
			if strings.Contains(s, name) && len(name) > bestLen {
				best, bestLen = p.Index, len(name)
			}
		}
		if best >= 0 {
			return best
		}
	}
	// Fallback vocabulary, longest key first so "open hat" beats "hat".
	keys := sortedKeysByLenDesc(padNames)
	for _, k := range keys {
		if strings.Contains(s, k) {
			return padNames[k]
		}
	}
	return -1
}

// levelFrom returns a target linear level for a louder/quieter request, relative
// to the pad's current level (a fixed, deterministic ±0.2 step, clamped 0..1).
func levelFrom(s string, pad int, m *drummachine.Machine) float64 {
	cur := 1.0
	if m != nil && pad >= 0 && pad < len(m.Kit.Pads) {
		cur = m.Kit.Pads[pad].Level
	}
	step := 0.2
	if containsAny(s, "way", "much", "a lot", "lots") {
		step = 0.4
	}
	if containsAny(s, "louder", "turn up", "level up", "boost") {
		return clamp01(cur + step)
	}
	return clamp01(cur - step) // quieter
}

// panFrom returns a pan position: "left"→-0.75, "right"→+0.75, "hard left"→-1,
// "hard right"→+1, "center"→0, optional explicit "N%" overrides.
func panFrom(s string) float64 {
	if mm := pctRe.FindStringSubmatch(s); mm != nil {
		if pct, err := strconv.Atoi(mm[1]); err == nil {
			v := float64(pct) / 100
			if containsAny(s, "left") {
				v = -v
			}
			return clampFloat(v, -1, 1)
		}
	}
	switch {
	case containsAny(s, "center", "centre", "middle"):
		return 0
	case containsAny(s, "hard left", "full left"):
		return -1
	case containsAny(s, "hard right", "full right"):
		return 1
	case containsAny(s, "left"):
		return -0.75
	case containsAny(s, "right"):
		return 0.75
	}
	return 0
}

// pitchFrom returns a transpose in semitones. "down"→-, "up"→+; an explicit number
// of semitones/octaves is honoured; default move is one octave (12) in the named
// direction, or +0 if no direction.
func pitchFrom(s string) float64 {
	down := containsAny(s, "down", "lower", "deeper")
	mag := 12.0 // a clear, audible default move (one octave)
	if mm := regexp.MustCompile(`(\d+)\s*(semitone|semi|st|half step|halfstep)`).FindStringSubmatch(s); mm != nil {
		if n, err := strconv.Atoi(mm[1]); err == nil {
			mag = float64(n)
		}
	} else if mm := regexp.MustCompile(`(\d+)\s*octave`).FindStringSubmatch(s); mm != nil {
		if n, err := strconv.Atoi(mm[1]); err == nil {
			mag = float64(n) * 12
		}
	}
	if down {
		return -mag
	}
	return mag
}

// decayFrom returns a decay time in seconds. "shorten"/"tighten"/"shorter"→a short
// 0.1s one-shot-ish decay; "longer"/"ring"→a long 1.5s tail; explicit "N seconds"
// honoured.
func decayFrom(s string) float64 {
	if mm := regexp.MustCompile(`(\d+(?:\.\d+)?)\s*(?:s|sec|secs|seconds)\b`).FindStringSubmatch(s); mm != nil {
		if v, err := strconv.ParseFloat(mm[1], 64); err == nil && v >= 0 {
			return v
		}
	}
	if containsAny(s, "longer", "ring", "tail") {
		return 1.5
	}
	return 0.1 // shorten / tighten → short decay
}

// swingFrom returns a swing ratio (0.5..0.75). An explicit "N%" maps linearly;
// "more swing" leans to 0.62; a bare "swing it" uses a light musical default 0.58.
func swingFrom(s string) float64 {
	if mm := pctRe.FindStringSubmatch(s); mm != nil {
		if pct, err := strconv.Atoi(mm[1]); err == nil {
			r := 0.5 + float64(pct)/100*0.25
			return clampFloat(r, 0.5, 0.75)
		}
	}
	if containsAny(s, "more swing", "heavy swing", "hard swing", "lots of swing", "extra swing", "swing harder") {
		return 0.62
	}
	return 0.58
}

// looksLikeSampleEdit reports whether the instruction is plausibly a "put a <sound>
// on <pad>"/"swap the <pad>" sample edit (it names a target pad or pad number).
func looksLikeSampleEdit(s string, m *drummachine.Machine) bool {
	return padFrom(s, m) >= 0 || padRe.MatchString(s)
}

// parseSampleEdit handles "put a clap on pad 5" / "swap the snare [for a rim]".
// The chosen sound becomes the SamplePath (a bare name the GUI resolves against
// the sample library — machinectl does not touch the filesystem).
func parseSampleEdit(s, raw string, m *drummachine.Machine) Intent {
	in := Intent{Action: SetPadSample, Pad: -1, Raw: raw}

	// "put a <sound> on pad N / on the kick"
	if mm := regexp.MustCompile(`(?:put|load|drop|use)\s+(?:a|an|the)?\s*([a-z0-9 \-]+?)\s+on\s+(.+)`).FindStringSubmatch(s); mm != nil {
		in.SamplePath = strings.TrimSpace(mm[1])
		in.Pad = padFrom(mm[2], m)
		if rest := strings.TrimSpace(mm[2]); in.Pad < 0 {
			if pmm := padRe.FindStringSubmatch(rest); pmm != nil {
				if n, err := strconv.Atoi(pmm[1]); err == nil {
					in.Pad = n - 1
				}
			}
		}
		return resolvedPad(in, "I couldn't tell which pad to put that on.")
	}

	// "swap/replace the <pad> [for/with a <sound>]"
	if mm := regexp.MustCompile(`(?:swap|replace)\s+(?:the\s+)?([a-z0-9 \-]+?)(?:\s+(?:for|with)\s+(?:a|an|the)?\s*([a-z0-9 \-]+))?$`).FindStringSubmatch(s); mm != nil {
		in.Pad = padFrom(mm[1], m)
		if len(mm) > 2 && strings.TrimSpace(mm[2]) != "" {
			in.SamplePath = strings.TrimSpace(mm[2])
		} else {
			in.SamplePath = strings.TrimSpace(mm[1]) // "swap the snare" → a fresh snare sample
		}
		return resolvedPad(in, "I couldn't tell which pad to swap.")
	}

	in.Action = Unknown
	in.Note = "I couldn't work out which sound goes on which pad. Try 'put a clap on pad 5' or 'swap the snare'."
	in.Pad = -1
	return in
}

// ─── Apply (immutable; words→edits done, here we run the edit) ────────────────

// LogPath is the conventional corrections-log path machinectl appends to (best
// effort) so becky-habits learns the producer's recurring edits. The GUI may set
// this to <output-dir>/machine.corrections.jsonl; when empty, logging is skipped.
var LogPath string

// Apply runs the Intent against m and returns a NEW machine, a plain-English
// summary the GUI echoes ("Loaded the 808 kit", "Made it half-time"), and an
// error only for a genuinely impossible edit (the machine is still returned, a
// safe copy). It NEVER mutates m and NEVER panics.
//
// Transport intents return the machine unchanged plus a summary; the GUI reads
// in.Transport to drive playback (machinectl makes no sound).
//
// Each applied edit is logged best-effort via habits.AppendCorrectionLog so becky
// learns the producer's habits; a logging failure never affects the result.
func Apply(m *drummachine.Machine, in Intent) (*drummachine.Machine, string, error) {
	if m == nil {
		m = drummachine.NewMachine()
	}

	switch in.Action {
	case Transport:
		// No edit — signal only.
		verb := string(in.Transport)
		if verb == "" {
			verb = "play"
		}
		summary := "Press play."
		if in.Transport == TransportStop {
			summary = "Stop."
		}
		logEdit("transport", "transport", "", verb)
		return cloneMachine(m), summary, nil

	case SetTempo:
		out := withTempo(m, in.Value)
		summary := fmt.Sprintf("Set the tempo to %g BPM.", out.Tempo)
		logEdit("tempo", "bpm", fmt.Sprintf("%g", m.Tempo), fmt.Sprintf("%g", out.Tempo))
		return out, summary, nil

	case SetSwing:
		pat := activePattern(m)
		out, err := m.SetSwing(pat, in.Value)
		if err != nil {
			return out, "Couldn't set the swing: " + err.Error(), nil
		}
		summary := fmt.Sprintf("Set the swing to %.0f%%.", swingPct(in.Value))
		logEdit("swing", "ratio", fmt.Sprintf("%.2f", m.Bank.Patterns[pat].Swing), fmt.Sprintf("%.2f", in.Value))
		return out, summary, nil

	case LoadKit:
		// machinectl does NOT resolve kit files — it records the request on the
		// kit name so the GUI/engine loads the actual samples. The edit becky can
		// safely make to the model is naming the kit.
		out := cloneMachine(m)
		name := in.KitName
		if name == "" {
			name = "the requested kit"
		} else {
			out.Kit.Name = titleKit(in.KitName)
		}
		logEdit("kit", "name", m.Kit.Name, out.Kit.Name)
		return out, "Loaded " + kitPhrase(in.KitName) + ".", nil

	case SetPadSample:
		out, err := m.SetPadSample(in.Pad, in.SamplePath)
		if err != nil {
			return out, padFail(err), nil
		}
		logEdit(padScope(m, in.Pad), "sample", m.Kit.Pads[in.Pad].SamplePath, in.SamplePath)
		return out, fmt.Sprintf("Put %q on %s.", in.SamplePath, padLabel(m, in.Pad)), nil

	case SetPadLevel:
		out, err := m.SetPadLevel(in.Pad, in.Value)
		if err != nil {
			return out, padFail(err), nil
		}
		dir := "up"
		if in.Value < m.Kit.Pads[in.Pad].Level {
			dir = "down"
		}
		logEdit(padScope(m, in.Pad), "level", fmt.Sprintf("%.2f", m.Kit.Pads[in.Pad].Level), fmt.Sprintf("%.2f", out.Kit.Pads[in.Pad].Level))
		return out, fmt.Sprintf("Turned %s the %s.", dir, strings.ToLower(padLabel(m, in.Pad))), nil

	case SetPadPan:
		out, err := m.SetPadPan(in.Pad, in.Value)
		if err != nil {
			return out, padFail(err), nil
		}
		logEdit(padScope(m, in.Pad), "pan", fmt.Sprintf("%.2f", m.Kit.Pads[in.Pad].Pan), fmt.Sprintf("%.2f", out.Kit.Pads[in.Pad].Pan))
		return out, fmt.Sprintf("Panned the %s %s.", strings.ToLower(padLabel(m, in.Pad)), panWord(in.Value)), nil

	case SetPadPitch:
		out, err := m.SetPadPitch(in.Pad, in.Value)
		if err != nil {
			return out, padFail(err), nil
		}
		logEdit(padScope(m, in.Pad), "pitch", fmt.Sprintf("%.1f", m.Kit.Pads[in.Pad].PitchSemitones), fmt.Sprintf("%.1f", out.Kit.Pads[in.Pad].PitchSemitones))
		dir := "up"
		if out.Kit.Pads[in.Pad].PitchSemitones < 0 {
			dir = "down"
		}
		return out, fmt.Sprintf("Pitched the %s %s %g semitones.", strings.ToLower(padLabel(m, in.Pad)), dir, absF(out.Kit.Pads[in.Pad].PitchSemitones)), nil

	case SetPadDecay:
		out, err := m.SetPadDecay(in.Pad, in.Value)
		if err != nil {
			return out, padFail(err), nil
		}
		logEdit(padScope(m, in.Pad), "decay", fmt.Sprintf("%.2f", m.Kit.Pads[in.Pad].Decay), fmt.Sprintf("%.2f", out.Kit.Pads[in.Pad].Decay))
		verb := "Shortened"
		if in.Value >= 1.0 {
			verb = "Lengthened"
		}
		return out, fmt.Sprintf("%s the %s's tail.", verb, strings.ToLower(padLabel(m, in.Pad))), nil

	case SetChoke:
		out, err := m.SetPadChokeGroup(in.Pad, in.Group)
		if err != nil {
			return out, padFail(err), nil
		}
		logEdit(padScope(m, in.Pad), "choke", strconv.Itoa(m.Kit.Pads[in.Pad].ChokeGroup), strconv.Itoa(in.Group))
		return out, fmt.Sprintf("Put the %s in choke group %d.", strings.ToLower(padLabel(m, in.Pad)), in.Group), nil

	case MutePad:
		out, err := m.MutePad(in.Pad, in.On)
		if err != nil {
			return out, padFail(err), nil
		}
		logEdit(padScope(m, in.Pad), "mute", boolStr(m.Kit.Pads[in.Pad].Mute), boolStr(in.On))
		state := "Muted"
		if !in.On {
			state = "Unmuted"
		}
		return out, fmt.Sprintf("%s the %s.", state, strings.ToLower(padLabel(m, in.Pad))), nil

	case SoloPad:
		out, err := m.SoloPad(in.Pad, in.On)
		if err != nil {
			return out, padFail(err), nil
		}
		logEdit(padScope(m, in.Pad), "solo", boolStr(m.Kit.Pads[in.Pad].Solo), boolStr(in.On))
		state := "Soloed"
		if !in.On {
			state = "Unsoloed"
		}
		return out, fmt.Sprintf("%s the %s.", state, strings.ToLower(padLabel(m, in.Pad))), nil

	case NewPattern:
		name := fmt.Sprintf("Pattern %d", m.PatternCount()+1)
		out, err := m.AddPattern(name, drummachine.DefaultSteps)
		if err != nil {
			return out, "Couldn't add a pattern: " + err.Error(), nil
		}
		logEdit("structure", "pattern", strconv.Itoa(m.PatternCount()), strconv.Itoa(out.PatternCount()))
		return out, "Added " + name + ".", nil

	case DuplicatePattern:
		pat := activePattern(m)
		out, err := m.DuplicatePattern(pat, "")
		if err != nil {
			return out, "Couldn't duplicate the pattern: " + err.Error(), nil
		}
		logEdit("structure", "duplicate", strconv.Itoa(m.PatternCount()), strconv.Itoa(out.PatternCount()))
		return out, "Duplicated the current pattern.", nil

	case AddScene:
		name := fmt.Sprintf("Scene %d", m.SceneCount()+1)
		out, err := m.AddScene(name, activePattern(m))
		if err != nil {
			return out, "Couldn't add a scene: " + err.Error(), nil
		}
		logEdit("structure", "scene", strconv.Itoa(m.SceneCount()), strconv.Itoa(out.SceneCount()))
		return out, "Added " + name + ".", nil

	case GenreStarter:
		out, summary, err := applyGenreStarter(m, in.Genre)
		if err != nil {
			return out, summary, nil
		}
		logEdit("genre", "starter", "", in.Genre)
		return out, summary, nil

	case Beat:
		return applyBeat(m, in)

	default: // Unknown
		note := in.Note
		if note == "" {
			note = "I didn't understand that. Try 'make it half-time', 'load my 808 kit', 'mute the clap', or 'set the tempo to 140'."
		}
		return cloneMachine(m), note, nil
	}
}

// applyBeat delegates the beat transform to internal/drumcmd via the Machine's
// Pattern↔DrumGrid bridge: active Pattern → ToDrumGrid → drumcmd.Apply →
// PatternFromDrumGrid → write the transformed pattern back. The drumcmd transforms
// (half-time, humanize, fill, swing, variations, density, quantize) are NOT
// reimplemented here.
func applyBeat(m *drummachine.Machine, in Intent) (*drummachine.Machine, string, error) {
	pat := activePattern(m)
	if !validPattern(m, pat) {
		return cloneMachine(m), "There's no pattern to change.", nil
	}
	src := m.Bank.Patterns[pat]
	grid := src.ToDrumGrid(m.Kit)

	res, err := drumcmd.Apply(&grid, in.Drum)
	if err != nil || res == nil {
		return cloneMachine(m), "Couldn't change the beat.", nil
	}
	if !res.Changed || res.After == nil {
		return cloneMachine(m), res.Summary, nil
	}

	newPat := drummachine.PatternFromDrumGrid(*res.After, m.Kit, src.Name)
	// Write the transformed pattern back immutably: rebuild via the spine's step
	// edits would be O(n) calls; instead, clone the machine and replace the lanes
	// of the active pattern (still immutable — m is untouched, out is a deep copy).
	out := cloneMachine(m)
	out.Bank.Patterns[pat] = newPat

	logEdit("beat", in.Drum.Action.String(), src.Name, in.Drum.Action.String())
	return out, res.Summary, nil
}

// ─── machine helpers (immutable) ──────────────────────────────────────────────

// cloneMachine returns a deep copy of m via its deterministic JSON round-trip
// (Save/Load), so machinectl never mutates the caller's Machine and never reaches
// into drummachine's unexported clone. Falls back to the input on the (impossible)
// marshal error so we degrade rather than crash.
func cloneMachine(m *drummachine.Machine) *drummachine.Machine {
	b, err := m.MarshalBytes()
	if err != nil {
		return m
	}
	out, err := drummachine.Load(bytes.NewReader(b))
	if err != nil {
		return m
	}
	return out
}

// withTempo returns a deep copy of m with Tempo set (clamped to a sane 40..300
// window). drummachine has no SetTempo, and machinectl must not edit that package,
// so the tempo edit is an immutable local copy here.
func withTempo(m *drummachine.Machine, bpm float64) *drummachine.Machine {
	out := cloneMachine(m)
	out.Tempo = clampFloat(bpm, 40, 300)
	return out
}

// activePattern returns the index of the pattern the first scene plays (the
// "current" pattern in v1, which is single-scene-focused). Falls back to 0.
func activePattern(m *drummachine.Machine) int {
	if m == nil || len(m.Scenes) == 0 {
		return 0
	}
	pi := m.Scenes[0].PatternIndex
	if pi < 0 || pi >= m.PatternCount() {
		return 0
	}
	return pi
}

func validPattern(m *drummachine.Machine, pat int) bool {
	return m != nil && pat >= 0 && pat < m.PatternCount()
}

// padLabel returns the spoken label for a pad ("Kick", "pad 5") for summaries.
func padLabel(m *drummachine.Machine, pad int) string {
	if m != nil && pad >= 0 && pad < len(m.Kit.Pads) && m.Kit.Pads[pad].Name != "" {
		return m.Kit.Pads[pad].Name
	}
	return fmt.Sprintf("pad %d", pad+1)
}

// padScope is the habit-log scope for a pad (its lowercase name, or "pad-N").
func padScope(m *drummachine.Machine, pad int) string {
	if m != nil && pad >= 0 && pad < len(m.Kit.Pads) && m.Kit.Pads[pad].Name != "" {
		return strings.ToLower(m.Kit.Pads[pad].Name)
	}
	return fmt.Sprintf("pad-%d", pad+1)
}

func padFail(err error) string {
	return "That pad doesn't exist (" + err.Error() + ")."
}

// logEdit appends one correction-log line best-effort (becky learns the habit).
// A nil/empty LogPath or any write failure is silently ignored — logging never
// affects the edit result.
func logEdit(scope, field, auto, fixed string) {
	if LogPath == "" {
		return
	}
	_ = habits.AppendCorrectionLog(LogPath, "machine", scope, field, auto, fixed)
}

// ─── tiny string/number helpers ───────────────────────────────────────────────

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func clamp01(v float64) float64 { return clampFloat(v, 0, 1) }

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func absF(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func swingPct(swing float64) float64 {
	if swing <= 0.5 {
		swing = 0.58
	}
	return (swing - 0.5) / 0.25 * 100
}

func panWord(v float64) string {
	switch {
	case v <= -0.99:
		return "hard left"
	case v < 0:
		return "left"
	case v >= 0.99:
		return "hard right"
	case v > 0:
		return "right"
	default:
		return "center"
	}
}

// kitPhrase renders the kit name for a summary ("the 808 kit" / "the requested kit").
func kitPhrase(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "the kit"
	}
	return "the " + name + " kit"
}

// titleKit makes a pretty kit name ("808" → "808 Kit", "trap" → "Trap Kit").
func titleKit(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Kit"
	}
	// Capitalise the first letter of each word for a tidy display name.
	parts := strings.Fields(name)
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ") + " Kit"
}

// sortedKeysByLenDesc returns the map keys ordered longest-first (ties broken
// lexicographically) for deterministic longest-match resolution.
func sortedKeysByLenDesc(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// insertion-free stable order: sort by (-len, key).
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0; j-- {
			a, b := keys[j-1], keys[j]
			if len(b) > len(a) || (len(b) == len(a) && b < a) {
				keys[j-1], keys[j] = keys[j], keys[j-1]
			} else {
				break
			}
		}
	}
	return keys
}
