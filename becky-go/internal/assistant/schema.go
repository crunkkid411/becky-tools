// Package assistant is the becky-clip "Underlord" cost-tiered AI router
// (SPEC-BECKY-CLIP §8, R-AI). A detective types a sentence; a deterministic
// front gate satisfies most turns with zero model tokens; a small local model
// handles fuzzy single-action parsing; only genuinely hard semantic retrieval +
// multi-step planning escalate to a frontier backend (the claude CLI on Jordan's
// Max plan, or the Anthropic API). Every turn that would mutate returns a
// Proposal — nothing executes until the human approves ("show me, don't do it").
//
// The model's ONLY control surface is a fixed, default-deny allowlist of action
// verbs (this file). An unknown verb is rejected, never executed — the same
// posture as SPEC-BECKY-CANVAS §4 ("model emits executable text" dispatches only
// to an allowlisted set; no free-form code).
//
// Every model/network boundary lives behind a Backend.Available() check, so the
// whole package builds and unit-tests GREEN with no model and no network: the
// deterministic Tier-0 path, the DSL/JSON parser, the allowlist validator, and
// the funnel are all pure Go. cgo-free.
package assistant

// Verb is one allowlisted action the AI may emit. The Go backend has exactly one
// deterministic handler per verb (in the GUI); an utterance can never invoke
// anything outside this set.
type Verb string

// The 11 verbs (SPEC-BECKY-CLIP §8 / R-AI §2.1). Read-verbs are side-effect-free;
// mutate-verbs change the timeline and therefore require the approval envelope.
const (
	VerbSearch      Verb = "search"       // run becky-search → hits panel (read)
	VerbFindQuotes  Verb = "find_quotes"  // run becky-quotes / frontier selection (read)
	VerbPreviewClip Verb = "preview_clip" // scrub/preview a region in the player (read)
	VerbGrabFrame   Verb = "grab_frame"   // extract one still to the work dir (new file)
	VerbAddClip     Verb = "add_clip"     // append/insert a clip into the timeline (MUTATE)
	VerbRemoveClip  Verb = "remove_clip"  // drop a clip from the timeline (MUTATE)
	VerbReorder     Verb = "reorder"      // move a clip (MUTATE)
	VerbSetOverlay  Verb = "set_overlay"  // set a clip's overlay field, e.g. date (MUTATE)
	VerbSetMarker   Verb = "set_marker"   // drop a timeline marker (MUTATE)
	VerbSetLabel    Verb = "set_label"    // rename/label a clip (MUTATE)
	VerbExport      Verb = "export"       // render the compilation (new file)
)

// allowlist is the default-deny set: only these verbs are ever dispatched. A verb
// not present here is rejected at validation time.
var allowlist = map[Verb]bool{
	VerbSearch: true, VerbFindQuotes: true, VerbPreviewClip: true, VerbGrabFrame: true,
	VerbAddClip: true, VerbRemoveClip: true, VerbReorder: true, VerbSetOverlay: true,
	VerbSetMarker: true, VerbSetLabel: true, VerbExport: true,
}

// mutateVerbs are the verbs that change the timeline and so REQUIRE the
// propose-then-apply envelope (nothing applies until the human clicks ✓). The
// read/new-file verbs may auto-apply to the results panel for snappiness.
var mutateVerbs = map[Verb]bool{
	VerbAddClip: true, VerbRemoveClip: true, VerbReorder: true,
	VerbSetOverlay: true, VerbSetMarker: true, VerbSetLabel: true,
}

// IsAllowed reports whether v is in the default-deny allowlist.
func IsAllowed(v Verb) bool { return allowlist[v] }

// IsMutating reports whether v changes the timeline (and thus needs approval).
func IsMutating(v Verb) bool { return mutateVerbs[v] }

// Action is one validated, allowlisted instruction the AI emits. Args carries the
// per-verb parameters (R-AI §2.1): e.g. add_clip uses source/in/out/label/at,
// search uses query/mode/limit. Values arrive as strings (from the DSL) or typed
// (from JSON); helpers on Action normalise them for handlers.
type Action struct {
	Verb Verb           `json:"verb"`
	Args map[string]any `json:"args"`
}

// Invalid marks an action that parsed but failed validation (unknown verb, or a
// missing required arg). It is surfaced in the preview as "needs a value" /
// "unknown" and is NEVER executed. Keeping invalid actions visible (rather than
// silently dropping them) is honest UX — the detective sees what the AI tried.
type Invalid struct {
	Action Action `json:"action"`
	Reason string `json:"reason"`
}

// Proposal is the propose-then-apply envelope (R-AI §2.3). Every turn that would
// mutate returns one of these instead of a side effect: the GUI renders
// PreviewText + the before→after Diff and ✓ Apply / ✗ Reject; nothing mutates
// until the human approves.
type Proposal struct {
	// ID is a small handle the GUI echoes on ✓/✗ so the router can log/discard the
	// right pending proposal (assigned by Router.finalize).
	ID string `json:"id,omitempty"`
	// PreviewText is the one human sentence shown above the diff, e.g.
	// "Add 1 clip and date-label it." (the brief's Proposal.PreviewText).
	PreviewText string `json:"preview_text"`
	// Actions is the validated, allowlisted action list to run on ✓.
	Actions []Action `json:"actions"`
	// Invalid lists actions that parsed but won't run (shown, not executed).
	Invalid []Invalid `json:"invalid,omitempty"`
	// Preview is the per-action before→after diff for the overlay.
	Preview []DiffLine `json:"preview,omitempty"`
	// Tier records which tier produced this proposal (a small UI badge).
	Tier Tier `json:"tier"`
	// Sources is the cue/file provenance for any retrieval that fed the proposal.
	Sources []SourceRef `json:"sources,omitempty"`
	// Note is an honest plain-language degrade/escalation message, e.g.
	// "answered locally — turn on the frontier model for deeper search".
	Note string `json:"note,omitempty"`
	// Cost is the tokens/$ note if Tier 2 ran (the budget meter); zero otherwise.
	Cost CostNote `json:"cost,omitempty"`
	// Mutates is true iff any action changes the timeline (the GUI gates ✓/✗ on it).
	Mutates bool `json:"mutates"`
	// ExecCommands are deterministic external commands the GUI runs on ✓ for
	// read-verbs that shell out (search → becky-search; find_quotes →
	// becky-quotes --select-from-json). The router BUILDS the argv; it does not
	// need the binaries present (so unit tests can assert the command shape).
	ExecCommands []ExecCommand `json:"exec_commands,omitempty"`
}

// DiffLine is one before→after row for the preview overlay.
type DiffLine struct {
	Label  string `json:"label"`  // what is changing, e.g. "timeline clip #2"
	Before string `json:"before"` // prior state ("" for an add)
	After  string `json:"after"`  // resulting state ("" for a remove)
}

// SourceRef is cue/file provenance for a retrieval result (verifiability).
type SourceRef struct {
	Source    string  `json:"source"`    // video path the cue came from
	Timestamp float64 `json:"timestamp"` // seconds into source
	Text      string  `json:"text"`      // the cue text
}

// CostNote is the budget meter's per-turn tokens/$ note (populated only on Tier 2).
type CostNote struct {
	Tokens int     `json:"tokens,omitempty"`
	USD    float64 `json:"usd,omitempty"`
	Model  string  `json:"model,omitempty"`
}

// ExecCommand is a deterministic external command the GUI executes on approval.
// The router forms the argv (Bin + Args); whether the binary exists is the GUI's
// concern at run time. Stdin carries a piped payload (e.g. the anchors JSON for
// becky-quotes --select-from-json) when non-empty.
type ExecCommand struct {
	Bin   string   `json:"bin"`             // e.g. "becky-search" | "becky-quotes"
	Args  []string `json:"args"`            // argv after the binary
	Stdin string   `json:"stdin,omitempty"` // piped payload, if any
	Note  string   `json:"note,omitempty"`  // what this command does, for the UI
}
