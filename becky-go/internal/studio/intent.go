// Package studio turns a plain-English studio-setup instruction into concrete
// ROUTING/MIX edits on becky's existing project.json graph — killing the
// "click-engineer" grunt-work of studio setup. The producer says
// "sidechain the bass to the kick" and becky wires it: one declared
// {from,to,kind:"sidechain"} edge on the routing graph, not 40 clicks.
//
// It operates ENTIRELY on the existing data model (internal/music.Project,
// internal/mixplan) — it does NOT invent a new schema. An instruction is parsed
// into a normalized, structured Intent; Apply turns that Intent into a NEW,
// immutably-patched Project plus a plain-English summary of what changed.
//
// Two parser paths sit behind the Parser interface (the becky cloud/local split):
//   - DeterministicParser — a keyword/grammar parser that works fully offline
//     RIGHT NOW for the example sentences. This is the testable core.
//   - ModelParser — a stub for the FAST BACKGROUND instruct model (Smol/LFM2
//     class). It SILENTLY DEGRADES to the deterministic parser when the model
//     binary/weights are absent (see PickParser, model_parser.go).
//
// Invariants (CLAUDE.md): pure Go, stdlib only, deterministic (same input ->
// byte-identical output: edges are sorted, ordering fixed), degrade-never-crash
// (an unintelligible instruction yields Intent{Action: Unknown} + a friendly
// note, never a panic).
package studio

import "becky-go/internal/music"

// Action is the normalized class of edit an instruction maps to.
type Action string

const (
	// ActionUnknown means the instruction could not be understood. Apply is a
	// no-op and the summary explains why (degrade-never-crash).
	ActionUnknown Action = "unknown"
	// ActionSidechain adds a sidechain control edge: the source ducks the target
	// (e.g. "sidechain the bass to the kick" -> kick ducks the bass bus).
	ActionSidechain Action = "sidechain"
	// ActionRoute adds an audio routing edge: send a track to a bus
	// (e.g. "route the lead guitar to the guitar bus").
	ActionRoute Action = "route"
	// ActionInsertChain inserts the standard FX chain on a bus
	// (e.g. "put my usual chain on the drum bus" / "set up the drum bus").
	ActionInsertChain Action = "insertChain"
	// ActionSetVST sets a per-bus VST preference (e.g. "use Odin II on the lead").
	ActionSetVST Action = "setVST"
	// ActionSetGain sets a gain-staging preference (e.g. "gain stage the kick to -7").
	ActionSetGain Action = "setGain"
)

// Intent is a normalized, structured edit request — the single value both parser
// paths produce and Apply consumes. Source/Target are resolved node ids that
// already exist in (or map cleanly onto) the loaded Project; raw nouns are kept
// for the human-facing summary and the corrections log.
//
// Field meaning per Action:
//
//	Sidechain   : Source = detector node (e.g. "src.drums.kick"), Target = bus
//	              being ducked (e.g. "bus.808"); Band optional ("low").
//	Route       : Source = track node (e.g. "src.lead"), Target = bus.
//	InsertChain : Target = bus the standard chain is inserted on.
//	SetVST      : Target = bus, VST = plugin name (e.g. "The Odin II").
//	SetGain     : Target = node (track or bus), GainDB = target level, HasGain set.
type Intent struct {
	Action Action `json:"action"`

	Source string `json:"source,omitempty"` // resolved node id (detector / track)
	Target string `json:"target,omitempty"` // resolved node id (bus / node)

	Band string `json:"band,omitempty"` // "low" for frequency-selective ducks
	VST  string `json:"vst,omitempty"`  // plugin name for SetVST

	GainDB  float64 `json:"gainDb,omitempty"` // target level for SetGain
	HasGain bool    `json:"hasGain,omitempty"`

	// Raw nouns the user actually said, before id resolution — used for the
	// human summary and the habits corrections log. Never load-bearing for Apply.
	SourceWord string `json:"sourceWord,omitempty"`
	TargetWord string `json:"targetWord,omitempty"`

	// Note is a friendly explanation, especially for ActionUnknown ("couldn't
	// understand 'foo' — try 'sidechain the bass to the kick'").
	Note string `json:"note,omitempty"`
}

// Parser turns a plain-English instruction into a structured Intent, resolving
// nouns against the supplied Project. Both DeterministicParser and ModelParser
// satisfy it; PickParser chooses which one is live.
type Parser interface {
	// Parse never returns an error — it degrades to Intent{Action: Unknown} with
	// a friendly Note (degrade-never-crash). The error return exists only so the
	// ModelParser can signal "I couldn't run; fall back" to PickParser internals.
	Parse(instruction string, proj music.Project) (Intent, error)
}
