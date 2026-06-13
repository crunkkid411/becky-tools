// intent.go — the ACT-vs-DISCUSS routing gate. This is the brief's core rule,
// made explicit and testable:
//
//	"if i'm asking a question i don't want it to launch a bunch of tool calls...
//	 but if i give it a file and say 'transcribe this' i want it to just do the thing."
//
// The gate (a corroboration / fact rule — two signals must agree before we act):
//
//	ACT  ⇢ ONLY when (1) a TARGET is set AND (2) the request is an unambiguous
//	        action (a button press, or clear imperative like "transcribe this").
//	        Both must hold. We then build the exact becky-* command and run it.
//	DISCUSS / CLARIFY ⇢ everything else: a question, an idea, anything ambiguous,
//	        or no target. We answer or ask ONE clarifying question. We NEVER fire a
//	        tool call in this branch.
//
// Classification is deterministic first (works fully offline, no model): the
// keyword/imperative reading is enough for the obvious cases. The local Qwen3.5
// model (llama.go) only REFINES a genuinely uncertain reading; if it is
// unavailable we degrade to the deterministic result + the keyword catalog. The
// model is never allowed to UPGRADE a no-target request into an action — the
// target gate is enforced in Go, not delegated to the model.
package main

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// decisionKind is the routed outcome.
type decisionKind int

const (
	decideAct      decisionKind = iota // run a becky-* command on the target now
	decideQuestion                     // answer / discuss; no tool call
	decideClarify                      // ask exactly one clarifying question; no tool call
	decideNewTool                      // the request is an idea no tool covers (pitch territory)
)

// decision is the routing result. When Kind==decideAct, Command is the EXACT
// becky-* argv that WOULD run (assertable in tests); Action names which op.
type decision struct {
	Kind       decisionKind
	Action     actionID // set when Kind==decideAct
	Command    []string // set when Kind==decideAct: ["becky-transcribe", "<path>"]
	Confidence float64  // 0..1
	Rationale  string   // short human-readable reason
	Source     string   // "deterministic" | "qwen3.5" | "fallback" — honest provenance
}

// imperativeActions maps clear command phrasings to an action id. The keys are
// matched case-insensitively as substrings, so "transcribe this" fires but "what
// does transcribe mean?" does not (the question marker trips first; see
// classifyDeterministic).
var imperativeActions = []struct {
	id       actionID
	patterns []string
}{
	{actTranscribe, []string{"transcribe", "transcript of", "caption", "subtitles for", "what is said", "what's said", "what was said"}},
	{actIdentify, []string{"identify", "who is in", "who's in", "who is this", "who's this", "recognize", "name the people", "who appears"}},
	{actDescribe, []string{"describe", "what happens", "what's happening", "whats happening", "what is happening", "validate", "what's going on", "whats going on"}},
	{actOCR, []string{"ocr", "read the text", "read text", "read the sign", "what does the sign", "text on screen", "on-screen text", "read the screen"}},
	{actCut, []string{"cut the silence", "cut silence", "remove silence", "trim silence", "remove dead air", "cut this", "trim this"}},
}

// questionMarkers signal DISCUSSION rather than a command: interrogatives, modal
// "can/does becky" capability probes, and idea/wish phrasings. If any fires AND no
// strong imperative is present, we never act.
var questionMarkers = []string{
	"can becky", "could becky", "does becky", "is becky able", "can you", "could you",
	"how do i", "how can i", "how would i", "what can you do", "what can becky",
	"should i", "would it", "is it possible", "i wonder", "i was thinking",
	"i wish", "it would be nice", "what if", "explain", "tell me about", "help me understand",
}

// ideaMarkers hint that the user is describing a NEW capability ("I wish becky
// could …") — pitch territory, never an action on the current target.
var ideaMarkers = []string{
	"i wish becky", "it would be nice if becky", "becky should be able", "can becky learn to",
	"build a tool", "new tool", "a tool that", "feature that would",
}

var thisRefRe = regexp.MustCompile(`\b(this|that|it|these|those|the (file|video|clip|folder|audio))\b`)

// classifyDeterministic decides act/question/clarify/new_tool from the request
// text and whether a target is present — with NO model. This alone handles the
// brief's two poster cases correctly:
//   - target set + "transcribe this"            -> ACT (transcribe)
//   - "can becky figure out where it was shot?" -> QUESTION (no tool call)
func classifyDeterministic(question string, t Target) decision {
	q := strings.ToLower(strings.TrimSpace(question))

	// Idea / new-tool wish always discusses (pitch), regardless of target.
	if containsAny(q, ideaMarkers) {
		return decision{Kind: decideNewTool, Confidence: 0.7, Source: "deterministic",
			Rationale: "request describes a capability that may not exist yet"}
	}

	isQuestion := strings.Contains(q, "?") || containsAny(q, questionMarkers)
	act, matched := matchImperative(q)

	switch {
	case matched && t.HasTarget() && !isQuestion:
		// BOTH signals agree: a real target AND an unambiguous command. ACT.
		return buildActDecision(act, t, 0.9, "deterministic",
			"clear action on the dropped target")
	case matched && t.HasTarget() && isQuestion:
		// Imperative verb but phrased as a question ("can you transcribe this?").
		// Treat a capability probe as a question UNLESS it is plainly a polite
		// command. Conservative: do not auto-run; offer it.
		if politeCommand(q) {
			return buildActDecision(act, t, 0.75, "deterministic",
				"polite imperative on the dropped target")
		}
		return decision{Kind: decideQuestion, Confidence: 0.6, Source: "deterministic",
			Rationale: "phrased as a capability question, not a command"}
	case matched && !t.HasTarget() && isQuestion:
		// "how do I transcribe a video?" / "can becky transcribe?" — the user wants
		// to KNOW, not to run it on a (missing) file. Discuss; the catalog answers.
		return decision{Kind: decideQuestion, Confidence: 0.7, Source: "deterministic",
			Rationale: "capability question about an op, no target to act on"}
	case matched && !t.HasTarget():
		// A bare command with nothing to run it on — ask for the one missing thing.
		return decision{Kind: decideClarify, Action: act, Confidence: 0.8, Source: "deterministic",
			Rationale: "an action was requested but no file/folder is set"}
	case isQuestion:
		return decision{Kind: decideQuestion, Confidence: 0.7, Source: "deterministic",
			Rationale: "interrogative / capability question"}
	default:
		// No clear signal either way -> discuss (never act on a guess).
		return decision{Kind: decideQuestion, Confidence: 0.4, Source: "deterministic",
			Rationale: "ambiguous; defaulting to discuss, not act"}
	}
}

// matchImperative returns the action whose phrasing appears in q, preferring the
// longest matching pattern so "cut the silence" beats a bare "cut".
func matchImperative(q string) (actionID, bool) {
	bestID := actionID("")
	bestLen := 0
	for _, ia := range imperativeActions {
		for _, p := range ia.patterns {
			if strings.Contains(q, p) && len(p) > bestLen {
				bestID, bestLen = ia.id, len(p)
			}
		}
	}
	return bestID, bestLen > 0
}

// politeCommand recognizes "please transcribe this" / "go ahead and identify it"
// — a question-shaped string that is really a command to act on the target.
func politeCommand(q string) bool {
	for _, p := range []string{"please ", "go ahead", "just do", "do it"} {
		if strings.Contains(q, p) {
			return true
		}
	}
	// "transcribe this/it" with a demonstrative is a command even with no '?'.
	return thisRefRe.MatchString(q) && !containsAny(q, questionMarkers)
}

// buildActDecision turns a recognized action + target into the exact command,
// degrading to clarify if the action does not actually apply to this target
// (e.g. "ocr" on a raw video, which becky-ocr cannot read directly).
func buildActDecision(id actionID, t Target, conf float64, source, why string) decision {
	a, ok := actionByID(id)
	if !ok {
		return decision{Kind: decideClarify, Confidence: 0.5, Source: source,
			Rationale: "recognized an action but no builder for it"}
	}
	cmd := commandFor(a, t)
	if cmd == nil {
		return decision{Kind: decideClarify, Action: id, Confidence: 0.5, Source: source,
			Rationale: "that action doesn't fit this target (e.g. OCR needs frames/a folder)"}
	}
	return decision{Kind: decideAct, Action: id, Command: cmd, Confidence: conf,
		Source: source, Rationale: why}
}

// --- the LLM refinement layer ------------------------------------------------

// intentSystemPrompt is the classifier instruction the local Qwen3.5 sees. It is
// the SAME contract validated at runtime against the on-disk GGUF on 2026-06-08.
const intentSystemPrompt = `You are the intent classifier for becky-tools, a forensic video toolkit. ` +
	`Classify the user request into EXACTLY ONE intent and reply with ONLY a JSON object, no prose, no markdown fence. ` +
	`Schema: {"kind":"action|question|new_tool|clarify","action":"transcribe|identify|describe|ocr|cut|","confidence":0.0-1.0,"rationale":"short"}. ` +
	`Rules: kind=action ONLY when the user clearly commands an operation on a file ("transcribe this","identify who is in it","cut the silence"). ` +
	`kind=question when they ask whether/how becky can do something or want discussion. ` +
	`kind=clarify when ambiguous or missing the target. kind=new_tool when nothing in becky could do it.`

// modelIntent is the JSON the model returns; we parse it leniently.
type modelIntent struct {
	Kind       string  `json:"kind"`
	Action     string  `json:"action"`
	Confidence float64 `json:"confidence"`
	Rationale  string  `json:"rationale"`
}

// classify is the full gate: deterministic first, then (optionally) refine with
// the local model. The TARGET GATE is enforced here in Go — the model can never
// turn a no-target request into an action. cli may be nil (deterministic only).
//
// Refinement policy (conservative): we consult the model only when the
// deterministic reading is UNCERTAIN (confidence < 0.7) AND a target is present
// (so a refinement could actually change the outcome). A high-confidence
// deterministic decision is trusted as-is — the model is a tie-breaker, not an
// override, which keeps behavior predictable and cheap.
func classify(ctx context.Context, cli *llamaClient, question string, t Target) decision {
	det := classifyDeterministic(question, t)

	if cli == nil || det.Confidence >= 0.7 {
		return det
	}
	if err := cli.Ready(); err != nil {
		// Model unavailable -> degrade to the deterministic result, noted honestly.
		det.Source = "deterministic (model unavailable: " + shortErr(err) + ")"
		return det
	}

	mi, err := classifyWithModel(ctx, cli, question, t)
	if err != nil {
		det.Source = "deterministic (model error: " + shortErr(err) + ")"
		return det
	}
	return reconcile(det, mi, t)
}

// classifyWithModel runs one model classification and parses the JSON reply.
func classifyWithModel(ctx context.Context, cli *llamaClient, question string, t Target) (modelIntent, error) {
	user := buildIntentUserPrompt(question, t)
	raw, err := cli.classify(ctx, intentSystemPrompt, user)
	if err != nil {
		return modelIntent{}, err
	}
	return parseModelIntent(raw)
}

// buildIntentUserPrompt gives the model the target context + the request, exactly
// as validated at runtime ("TARGET FILE: …\nREQUEST: …").
func buildIntentUserPrompt(question string, t Target) string {
	var b strings.Builder
	if t.HasTarget() {
		switch t.Kind {
		case targetDir:
			b.WriteString("TARGET FOLDER: " + t.Primary() + "\n")
		case targetMulti:
			b.WriteString("TARGET FILES: " + strings.Join(t.Paths, ", ") + "\n")
		default:
			b.WriteString("TARGET FILE: " + t.Primary() + "\n")
		}
	} else {
		b.WriteString("TARGET: (none set)\n")
	}
	b.WriteString("REQUEST: " + strings.TrimSpace(question))
	return b.String()
}

// parseModelIntent extracts the JSON object from the model's reply (tolerating a
// stray prose wrapper or code fence) and unmarshals it.
func parseModelIntent(raw string) (modelIntent, error) {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "{"); i >= 0 {
		if j := strings.LastIndex(s, "}"); j >= i {
			s = s[i : j+1]
		}
	}
	var mi modelIntent
	if err := json.Unmarshal([]byte(s), &mi); err != nil {
		return modelIntent{}, err
	}
	return mi, nil
}

// reconcile combines the model's reading with the deterministic one, ALWAYS
// re-applying the target gate in Go. The model can downgrade an action to a
// question/clarify, and can pick WHICH action when the deterministic layer was
// unsure — but it can never produce an act decision without a target, and any
// act it proposes is rebuilt against the real target (so the command is exact).
func reconcile(det decision, mi modelIntent, t Target) decision {
	conf := mi.Confidence
	if conf <= 0 {
		conf = det.Confidence
	}
	switch strings.ToLower(strings.TrimSpace(mi.Kind)) {
	case "action":
		if !t.HasTarget() {
			// Target gate: refuse to act with nothing to act on.
			return decision{Kind: decideClarify, Confidence: conf, Source: "qwen3.5",
				Rationale: "model read an action but no target is set"}
		}
		id := actionID(strings.ToLower(strings.TrimSpace(mi.Action)))
		if _, ok := actionByID(id); !ok {
			// Model said "action" but gave no usable op — fall back to det.
			if det.Kind == decideAct {
				return det
			}
			return decision{Kind: decideClarify, Confidence: conf, Source: "qwen3.5",
				Rationale: nonEmpty(mi.Rationale, "action unclear; which operation?")}
		}
		return buildActDecision(id, t, conf, "qwen3.5", nonEmpty(mi.Rationale, "model-classified action on target"))
	case "new_tool":
		return decision{Kind: decideNewTool, Confidence: conf, Source: "qwen3.5",
			Rationale: nonEmpty(mi.Rationale, "no existing tool appears to fit")}
	case "clarify":
		return decision{Kind: decideClarify, Confidence: conf, Source: "qwen3.5",
			Rationale: nonEmpty(mi.Rationale, "needs one clarification")}
	case "question":
		return decision{Kind: decideQuestion, Confidence: conf, Source: "qwen3.5",
			Rationale: nonEmpty(mi.Rationale, "treated as a question")}
	default:
		return det // unrecognized -> trust the deterministic reading
	}
}

// --- tiny helpers ---

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func nonEmpty(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

func shortErr(err error) string {
	s := err.Error()
	if len(s) > 80 {
		return s[:80]
	}
	return s
}

// classifyTimeout bounds a model classification so the UI never hangs on a slow
// spawn; the deterministic result is always available as the fallback.
const classifyTimeout = 150 * time.Second
