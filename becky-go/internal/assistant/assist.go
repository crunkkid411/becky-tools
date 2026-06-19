package assistant

import (
	"context"
	"strings"

	"becky-go/internal/footage"
)

// assist.go is the becky-clip CHAT brain. The GUI's "ask becky" box calls
// Router.Assist (NOT Handle): a detective wants a real assistant they can talk to
// and debug WITH — not a keyword grep wearing a chat costume.
//
// Assist's contract:
//   - An explicit Tier-0 command ("export", "add clip 3", "find the word cat") is
//     executed deterministically, with ZERO model tokens — instant, as before.
//   - A semantic "find every time X" / multi-step ask runs the retrieval funnel
//     (viaFrontier) which is purpose-built to assemble matching clips.
//   - ANYTHING ELSE (a question, a fuzzy request) is ANSWERED by the best available
//     capable model — Claude via the CLI (the user's Claude Code OAuth) or the
//     Anthropic API, else the local model. The model may answer in prose AND append
//     allowlisted actions; both are surfaced.
//   - With NO model available, it falls back to an honest keyword search and TELLS
//     the user how to enable the real assistant. The chat is never a dead end.
//
// The user explicitly asked to drive becky with "an api key or my claude code
// oauth" — converseBackend reaches both. The online toggle is still respected: with
// online OFF, Assist never calls a frontier backend (only the local model / the
// keyword floor), so pure-offline forensic work stays offline.

// noModelNote is shown when becky has no AI backend at all: it degrades to a
// keyword search AND tells the user exactly how to turn the real assistant on.
const noModelNote = "becky has no AI model available right now — sign into Claude Code (the `claude` command must be on your PATH) or set ANTHROPIC_API_KEY, then reopen becky-clip. Showing a keyword search for now."

// Assist is the chat entry point the GUI calls (App.Ask → r.Assist). See the file
// header for the routing contract. It never errors in practice (every path
// degrades to a usable Proposal); the error return is kept for symmetry with
// Handle and future use.
func (r *Router) Assist(ctx context.Context, utt string, cx Context, searchHits []footage.Candidate) (Proposal, error) {
	d := classifyTier(utt, cx)

	var p Proposal
	switch d.Tier {
	case TierDeterministic:
		if len(d.Actions) > 0 {
			// A fully-parsed command or literal search — run it, no model tokens.
			p = r.deterministic(d, cx)
		} else {
			p = r.assistFallback(ctx, utt, cx, searchHits)
		}
	case TierFrontier:
		// Semantic "find every time X" / multi-step plan → the retrieval funnel,
		// which already gates on online+budget and self-degrades to local / keyword.
		p = r.viaFrontier(ctx, utt, d, cx, searchHits)
	default: // TierLocal — a fuzzy single action OR a general question.
		p = r.assistFallback(ctx, utt, cx, searchHits)
	}

	r.finalize(&p)
	return p, nil
}

// assistFallback answers conversationally with the best available capable model,
// or — if none is available — degrades to the honest keyword-search floor.
func (r *Router) assistFallback(ctx context.Context, utt string, cx Context, searchHits []footage.Candidate) Proposal {
	if be := r.converseBackend(cx); be != nil {
		return r.converse(ctx, be, utt, cx, searchHits)
	}
	return r.degradeToRetrieval(utt, cx, noModelNote)
}

// converseBackend picks the model that answers a chat turn:
//   - the frontier (Claude CLI → Anthropic API) when online is ON and the budget
//     isn't exhausted — this is the "use my api key or claude code oauth" path;
//   - otherwise the local model, if it's available;
//   - otherwise nil (caller degrades to a keyword search).
//
// With online OFF it deliberately never returns a frontier backend, so the toggle
// is real and offline forensic work stays offline.
func (r *Router) converseBackend(cx Context) Backend {
	if cx.Online && !cx.Budget.Exhausted() {
		if be := r.frontier(); be != nil {
			return be
		}
	}
	if IsAvailable(r.local) {
		return r.local
	}
	return nil
}

// converse runs ONE conversational turn against be and returns a Proposal carrying
// the model's prose answer (PreviewText) plus any allowlisted actions it appended.
// The model only ever sees the compact timeline + a BOUNDED set of transcript cues
// (retrieval-funnelled) — never the whole 500 GB folder. A backend error degrades
// to the keyword floor with an honest note. The Note names the backend so the user
// can SEE which model answered (anti-"are you lying to me").
func (r *Router) converse(ctx context.Context, be Backend, utt string, cx Context, searchHits []footage.Candidate) Proposal {
	index := footage.FolderIndex{}
	if cx.Index != nil {
		index = *cx.Index
	}
	cands := r.funnel.Retrieve(index, retrievalTerms(utt), searchHits)

	tier := TierFrontier
	if be == r.local {
		tier = TierLocal
	}

	out, err := be.Complete(ctx, Request{
		System:    assistSystemPrompt,
		User:      assistUserPrompt(cx.Timeline, utt, cands),
		Tier:      tier,
		MaxTokens: 1024,
	})
	if err != nil {
		return r.degradeToRetrieval(utt, cx,
			"becky couldn't reach "+friendlyBackend(be.Name())+" ("+errLine(err)+") — showing a keyword search instead.")
	}

	prose, valid, invalid := splitProseAndActions(out)
	text := prose
	if text == "" {
		if len(valid) > 0 {
			text = summarize(valid)
		} else {
			text = strings.TrimSpace(out) // last resort: surface whatever came back
		}
	}

	p := Proposal{
		Actions:     valid,
		Invalid:     invalid,
		Tier:        tier,
		PreviewText: text,
		Note:        "via " + friendlyBackend(be.Name()),
		Cost:        CostNote{Model: be.Name()},
	}
	r.attachExec(&p, cx)
	if len(valid) > 0 {
		r.attachPreview(&p, cx)
	}
	if len(cands) > 0 {
		p.Sources = sourceRefs(cands)
	}
	return p
}

// splitProseAndActions separates a conversational model reply into its human prose
// and any allowlisted actions it appended. It is deliberately CONSERVATIVE so an
// ordinary sentence is never mangled into actions: only a pure JSON action payload,
// or lines that begin with an allowlisted verb token AND carry a "key=value" pair
// (the DSL shape), become actions. Everything else stays prose.
func splitProseAndActions(raw string) (prose string, valid []Action, invalid []Invalid) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil, nil
	}
	// A pure JSON action list/object → all actions, no prose.
	if looksJSON(stripFences(s)) {
		v, inv := Parse(s)
		return "", v, inv
	}

	var proseLines, actionLines []string
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if t != "" && isActionLine(t) {
			actionLines = append(actionLines, t)
		} else {
			proseLines = append(proseLines, line)
		}
	}
	if len(actionLines) > 0 {
		valid, invalid = Parse(strings.Join(actionLines, "\n"))
	}
	return strings.TrimSpace(strings.Join(proseLines, "\n")), valid, invalid
}

// isActionLine reports whether a line is a DSL action: its first whitespace token
// is an allowlisted verb AND the line contains a "key=value" pair. A prose sentence
// that merely starts with a word like "Search" (no '=') is NOT an action.
func isActionLine(line string) bool {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return false
	}
	if !IsAllowed(Verb(strings.ToLower(fields[0]))) {
		return false
	}
	return strings.Contains(line, "=")
}

// friendlyBackend renders a backend name for the chat note so the user can see,
// plainly, which model produced an answer.
func friendlyBackend(name string) string {
	switch name {
	case "claude-cli":
		return "Claude (Claude Code login)"
	case "anthropic-api":
		return "Claude API"
	case "local":
		return "the local model"
	default:
		return name
	}
}

// errLine returns the first line of an error message (compact, for chat notes).
func errLine(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// assistSystemPrompt is the conversational role for the chat. Unlike the strict
// action prompts (localSystemPrompt / planSystemPrompt), it lets becky ANSWER the
// user in plain language, and only emit actions when the user actually asks to
// change the timeline.
const assistSystemPrompt = `You are "becky", the assistant inside becky-clip — a forensic, transcript-based
video COMPILATION editor an investigator uses to assemble evidence clips from a
large case folder.

Answer the user's message directly, concisely, and honestly in plain language.
You can:
- Explain how to use becky-clip: search the transcripts (left box), click a quote
  to preview that exact moment, double-click a quote to add it to the timeline,
  drag a clip's left/right edge to add context before/after a quote, toggle the
  forensic lower-third (original filename + running ORIGINAL-file timecode), and
  Export one compilation MP4 (+ EDL + re-based SRT).
- Reason about the case ONLY from the transcript cues provided below. Never claim a
  transcript says something it does not. If you are unsure, say so.

If — and ONLY if — the user is asking you to CHANGE the timeline, you may append
allowlisted actions AFTER your sentence(s), one per line, in this DSL:
` + actionCatalog + `

Never modify originals. Copy any timestamp verbatim from a cue below; never invent
one. Keep your answer short unless the user asks for detail.`

// BackendStatus reports which assistant backends are usable right now, so the GUI
// can tell the user — plainly — what is powering the chat (anti-"are you lying to
// me") and how to enable more. Online mirrors the GUI toggle (set by the caller,
// since it lives on the App, not the Router).
type BackendStatus struct {
	ClaudeCLI bool   `json:"claude_cli"` // the `claude` command (Claude Code OAuth) is on PATH
	API       bool   `json:"api"`        // an Anthropic API key is set
	Local     bool   `json:"local"`      // a local GGUF + llama-server is available
	Online    bool   `json:"online"`     // the GUI online toggle (set by the App)
	Summary   string `json:"summary"`    // one human sentence for the chat intro
}

// Status reports each backend's current Available() plus a friendly one-line
// summary for the chat intro. It spends no tokens (Available() never calls a model).
func (r *Router) Status() BackendStatus {
	s := BackendStatus{
		ClaudeCLI: IsAvailable(r.claudeCLI),
		API:       IsAvailable(r.api),
		Local:     IsAvailable(r.local),
	}
	switch {
	case s.ClaudeCLI:
		s.Summary = "Connected to Claude (your Claude Code login). Ask me anything, or tell me what to compile."
	case s.API:
		s.Summary = "Connected to the Claude API. Ask me anything, or tell me what to compile."
	case s.Local:
		s.Summary = "Using the local model. Ask me anything, or tell me what to compile."
	default:
		s.Summary = "No AI model connected yet — sign into Claude Code (the `claude` command on your PATH) or set ANTHROPIC_API_KEY to chat. Keyword search still works."
	}
	return s
}

// assistUserPrompt builds the chat turn payload: the current timeline + a bounded
// block of relevant transcript cues (when retrieval found any) + the user's words.
func assistUserPrompt(ts TimelineState, utt string, cands []footage.Candidate) string {
	tl := timelineBlock(ts)
	if len(cands) > 0 {
		return tl + "\n\nRELEVANT TRANSCRIPT CUES:\n" + candidatesBlock(cands) + "\n\nUSER: " + utt
	}
	return tl + "\n\nUSER: " + utt
}
