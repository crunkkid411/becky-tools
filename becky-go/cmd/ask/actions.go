// actions.go — the quick-action "buttons" (selectable rows, since buttons aren't
// a native TUI control). When a target is set, becky-ask offers the obvious ops
// for that target with NO typing: press a number key (or pick the row) and the
// matching becky-* command is built against the target and run.
//
// Each action is pure data + a command builder (tool name + args). The builder is
// deterministic and side-effect-free, so a test can assert EXACTLY the command a
// given action+target WOULD run (e.g. "Transcribe" on clip.mp4 -> becky-transcribe
// <clip.mp4>) without spawning anything. Execution lives in run.go.
package main

import "strings"

// actionID names the obvious operations the brief lists (Transcribe, Identify,
// Describe/validate, OCR, Cut). Kept as a typed id so routing and rendering agree.
type actionID string

const (
	actTranscribe actionID = "transcribe"
	actDiarize    actionID = "diarize" // becky-diarize (how many speakers + when each talks)
	actIdentify   actionID = "identify"
	actDescribe   actionID = "describe" // becky-validate (plain-language on-screen description)
	actOCR        actionID = "ocr"
	actCut        actionID = "cut"
)

// quickAction is one selectable row. Tool is the becky-* tool (without the
// "becky-" prefix, matching commonFlags.binPath / cmd/becky's runTool); the args
// are filled against the target by buildArgs.
type quickAction struct {
	ID        actionID
	Label     string            // shown in the row, e.g. "Transcribe"
	Hint      string            // one-line plain-English "what this gives you"
	appliesTo func(Target) bool // gate: only offer when it makes sense for this target
	buildArgs func(Target) (tool string, args []string)
}

// allQuickActions is the full menu in display order. The brief's five ops, each
// mapped to the real tool + the flags cmd/becky uses by convention (kb-final for
// identification; frames-dir for OCR, which reads frames, not raw video).
var allQuickActions = []quickAction{
	{
		ID:        actTranscribe,
		Label:     "Transcribe",
		Hint:      "what's said, with timestamps",
		appliesTo: func(t Target) bool { return t.IsVideoLike() },
		buildArgs: func(t Target) (string, []string) {
			return "transcribe", []string{t.Primary()}
		},
	},
	{
		ID:        actDiarize,
		Label:     "Diarize",
		Hint:      "how many speakers, and when each one talks",
		appliesTo: func(t Target) bool { return t.IsVideoLike() },
		buildArgs: func(t Target) (string, []string) {
			return "diarize", []string{t.Primary()}
		},
	},
	{
		ID:        actIdentify,
		Label:     "Identify",
		Hint:      "which known people are in it (voice + face)",
		appliesTo: func(t Target) bool { return t.IsVideoLike() },
		buildArgs: func(t Target) (string, []string) {
			return "identify", []string{t.Primary(), "--kb", defaultKB}
		},
	},
	{
		ID:        actDescribe,
		Label:     "Describe",
		Hint:      "plain-language description of on-screen actions (validate)",
		appliesTo: func(t Target) bool { return t.IsVideoLike() },
		buildArgs: func(t Target) (string, []string) {
			return "validate", []string{t.Primary(), "--backend", "gemma4-local"}
		},
	},
	{
		ID:    actOCR,
		Label: "OCR",
		Hint:  "read text on screen (signs, IDs, chat)",
		// becky-ocr reads a FOLDER of frames (or a single image), not a raw video.
		appliesTo: func(t Target) bool { return t.Kind == targetDir || t.IsImageLike() },
		buildArgs: func(t Target) (string, []string) {
			return "ocr", []string{"--frames-dir", t.Primary()}
		},
	},
	{
		ID:        actCut,
		Label:     "Cut",
		Hint:      "remove silence / dead air (never overwrites the source)",
		appliesTo: func(t Target) bool { return t.IsVideoLike() },
		buildArgs: func(t Target) (string, []string) {
			return "cut", []string{t.Primary()}
		},
	},
}

// defaultKB is the conventional knowledge-base dir cmd/becky uses; offered without
// asking (matches the minimal-questions rule — try the convention, let the user
// correct). The workflow runner resolves it relative to the working dir.
const defaultKB = "kb-final"

// quickActionsFor returns the actions that apply to the given target, in display
// order. With no target the menu is empty (the buttons only appear once a file or
// folder is in play, per the brief: "When a target is set, show selectable…").
func quickActionsFor(t Target) []quickAction {
	if !t.HasTarget() {
		return nil
	}
	var out []quickAction
	for _, a := range allQuickActions {
		if a.appliesTo == nil || a.appliesTo(t) {
			out = append(out, a)
		}
	}
	return out
}

// actionByID finds an action definition by id (used to map a recognized action
// from the intent step onto its command builder).
func actionByID(id actionID) (quickAction, bool) {
	for _, a := range allQuickActions {
		if a.ID == id {
			return a, true
		}
	}
	return quickAction{}, false
}

// commandFor builds the full command an action would run against the target as a
// single slice: [becky-<tool>, arg, arg, ...]. It returns nil when the action
// does not apply to this target, so callers never emit a bogus command. This is
// the exact, assertable "what it WOULD run" used by the act-vs-discuss routing
// and its tests.
func commandFor(a quickAction, t Target) []string {
	if !t.HasTarget() {
		return nil
	}
	if a.appliesTo != nil && !a.appliesTo(t) {
		return nil
	}
	tool, args := a.buildArgs(t)
	if tool == "" {
		return nil
	}
	return append([]string{"becky-" + tool}, args...)
}

// commandString renders a built command for display in the chat, quoting any
// argument that contains a space so it is copy-pasteable on Windows.
func commandString(cmd []string) string {
	if len(cmd) == 0 {
		return ""
	}
	parts := make([]string, 0, len(cmd))
	for _, c := range cmd {
		if strings.ContainsAny(c, " \t") {
			parts = append(parts, `"`+c+`"`)
		} else {
			parts = append(parts, c)
		}
	}
	return strings.Join(parts, " ")
}
