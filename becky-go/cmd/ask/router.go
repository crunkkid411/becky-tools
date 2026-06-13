// router.go — turns a typed request (plus any dropped target) into a chat reply
// and, when the request is a CLEAR action, the exact becky-* command to run.
//
// This is where the act-vs-discuss gate (intent.go) meets the chat surface:
//   - decideAct      -> show the command + a "run it? (y/n)" prompt; the model
//     runs it on confirm (a quick-action button is itself the
//     confirm, so it runs immediately — see model.go).
//   - decideClarify  -> ONE clarifying question, no tool call.
//   - decideQuestion -> answer from the offline catalog when it matches, else an
//     honest "I'm a router" reply. Never a tool call.
//   - decideNewTool  -> name it as a new-tool idea (pitch territory), no tool call.
//
// The offline keyword catalog (catalog.go) still answers "can becky do X?" with
// no model, and is the graceful fallback when the local model is unavailable.
package main

import (
	"context"
	"fmt"
	"strings"
)

// routed is what the router hands back to the TUI: a styled reply to append, and
// (optionally) a command the user can confirm to run. When Pending is non-empty
// the model arms a confirm prompt; nothing runs without that explicit step.
type routed struct {
	Reply   string
	Pending []string // becky-* argv awaiting y/n confirmation (empty = nothing to run)
}

// route is the single entry point the TUI calls on submit. cli may be nil (no
// model -> deterministic + catalog only). It NEVER executes a command itself; it
// only decides and, for an action, stages the command for confirmation.
func route(ctx context.Context, cli *llamaClient, question string, t Target) routed {
	q := strings.TrimSpace(question)
	if q == "" {
		return routed{}
	}

	// Shell built-ins, answered instantly.
	switch strings.ToLower(q) {
	case "help", "?", "/help", "what can you do", "what can you do?":
		return routed{Reply: helpReply()}
	}

	d := classify(ctx, cli, q, t)
	switch d.Kind {
	case decideAct:
		return routed{Reply: actReply(d, t), Pending: d.Command}
	case decideClarify:
		return routed{Reply: clarifyReply(d, q, t)}
	case decideNewTool:
		return routed{Reply: newToolReply(d, q)}
	default: // decideQuestion
		return routed{Reply: questionReply(q, d)}
	}
}

// routeQuestion is the legacy deterministic entry (no target, no model). It is
// kept so existing behavior/tests for the catalog answer remain valid; it now
// delegates to the offline classifier with an empty target.
func routeQuestion(question string) string {
	return route(context.Background(), nil, question, Target{}).Reply
}

// actReply shows the exact command the gate decided to run and asks for a single
// confirmation. Plain words; the command is copy-pasteable. (A quick-action
// button bypasses this prompt — the press is the confirmation.)
func actReply(d decision, t Target) string {
	var b strings.Builder
	b.WriteString(beckyStyle.Render(fmt.Sprintf("On it — I'll %s ", verbPhrase(d.Action))) +
		userStyle.Render(t.Label()) + beckyStyle.Render("."))
	b.WriteString("\n\n")
	b.WriteString(systemStyle.Render("  $ " + commandString(d.Command)))
	b.WriteString("\n\n")
	b.WriteString(beckyStyle.Render("Run it? ") + systemStyle.Render("(y = run · n = cancel)  ["+d.Source+"]"))
	return b.String()
}

// clarifyReply asks exactly ONE question — the missing piece — and nothing else.
func clarifyReply(d decision, question string, t Target) string {
	var b strings.Builder
	if !t.HasTarget() && d.Action != "" {
		b.WriteString(beckyStyle.Render("I can " + verbPhrase(d.Action) + " — which file or folder? "))
		b.WriteString("\n")
		b.WriteString(systemStyle.Render("Drag it onto becky-ask, or paste/type its path."))
		return b.String()
	}
	b.WriteString(beckyStyle.Render("One thing first: "))
	b.WriteString(d.Rationale)
	return b.String()
}

// questionReply answers a discussion turn. It prefers the offline catalog match
// (so "can becky transcribe?" still names the tool + example), else an honest
// router reply. No tool call, ever.
func questionReply(question string, d decision) string {
	if hits := matchCapabilities(question); len(hits) > 0 {
		return capabilityReply(question, hits)
	}
	return placeholderReply(question)
}

// newToolReply names the request as a new-tool idea (the pitch path) without
// firing anything. It stays honest that building is a separate, approved step.
func newToolReply(d decision, question string) string {
	// If the idea actually maps onto an existing capability, answer that instead
	// of pitching — cheaper and avoids a needless build.
	if hits := matchCapabilities(question); len(hits) > 0 {
		return capabilityReply(question, hits)
	}
	var b strings.Builder
	b.WriteString(beckyStyle.Render("That sounds like a NEW capability becky doesn't have yet."))
	b.WriteString("\n\n")
	b.WriteString(systemStyle.Render("I can draft a becky-new-tool pitch for it (a separate, approved"))
	b.WriteString("\n")
	b.WriteString(systemStyle.Render("build step — see SPEC-BECKY-ASK.md §3.4). I won't run anything now."))
	return b.String()
}

// verbPhrase renders an action id as a plain verb phrase for the chat copy.
func verbPhrase(id actionID) string {
	switch id {
	case actTranscribe:
		return "transcribe"
	case actDiarize:
		return "find the speakers in"
	case actIdentify:
		return "identify who's in"
	case actDescribe:
		return "describe what's happening in"
	case actOCR:
		return "read the on-screen text of"
	case actCut:
		return "cut the silence out of"
	}
	return "process"
}

// capabilityReply formats the offline catalog answer: yes, becky can do this, here
// is the command. Plain words, copy-pasteable example — matching the house philosophy.
func capabilityReply(question string, hits []capability) string {
	var b strings.Builder
	b.WriteString(beckyStyle.Render("Yes — becky can help with that. Closest match(es):"))
	b.WriteString("\n")
	for _, c := range hits {
		b.WriteString("\n")
		b.WriteString(beckyStyle.Render("  • "+c.Verb) + " — " + c.Summary + "\n")
		b.WriteString(systemStyle.Render("      " + c.Example))
	}
	b.WriteString("\n\n")
	b.WriteString(systemStyle.Render("(Offline catalog answer. Drop a file and I can run one of these for you."))
	b.WriteString("\n")
	b.WriteString(systemStyle.Render(" Type ? for everything I can do.)"))
	return b.String()
}

// placeholderReply is the honest reply when nothing in the catalog matches and the
// request is a question/idea. It does not pretend to a capability.
func placeholderReply(question string) string {
	var b strings.Builder
	b.WriteString(beckyStyle.Render("I hear you: ") + userStyle.Render(question))
	b.WriteString("\n\n")
	b.WriteString(systemStyle.Render("That doesn't match a becky capability I know. I'm the chat front-door —"))
	b.WriteString("\n")
	b.WriteString(systemStyle.Render("I route to becky's tools; I don't answer general questions. Try a file"))
	b.WriteString("\n")
	b.WriteString(systemStyle.Render("drop + a quick action, or ask \"can becky …?\". Type ? for the menu."))
	b.WriteString("\n")
	b.WriteString(systemStyle.Render("(The full chat brain is specced in SPEC-BECKY-ASK.md.)"))
	return b.String()
}

// helpReply lists what becky can do today (the orchestrator ops) plus the few shell
// commands. Shown on `?`/`help` and folded into the welcome banner.
func helpReply() string {
	var b strings.Builder
	b.WriteString(beckyStyle.Render("becky can do these things (I'm the chat front-door to them):"))
	b.WriteString("\n")
	for _, c := range allOpsList() {
		b.WriteString("\n")
		b.WriteString(beckyStyle.Render("  • "+c.Verb) + " — " + c.Summary)
	}
	b.WriteString("\n\n")
	b.WriteString(systemStyle.Render("Drop a file/folder onto becky-ask for one-key actions, or ask in plain"))
	b.WriteString("\n")
	b.WriteString(systemStyle.Render("language (\"can becky find where Shelby appears?\"). Type q to quit."))
	return b.String()
}

// welcomeBanner is the first thing shown in the scrollback, mirroring the original
// becky-ask greeting but inviting a real question and mentioning the drop target.
func welcomeBanner() string {
	return fmt.Sprintf("%s\n%s",
		beckyStyle.Render("Hi — I'm Becky. Drop a video/audio file (or folder) onto me for one-key"),
		beckyStyle.Render("actions, or ask what you want to do. Type ? to see what I can do, q to quit."),
	)
}
