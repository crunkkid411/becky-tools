// model.go — the bubbletea Model-Update-View for the becky-ask chat window.
// Layout (top to bottom): a pink "Ask Becky:" title bar, an optional cyan
// "Target: <file>" context bar (when a file/folder was dropped or typed), a
// scrollback viewport of the conversation, an optional quick-action row strip
// ([1] Transcribe  [2] Identify …), a green input line, and the footer help.
//
// Three interactions beyond plain chat:
//   - DROP/ARGV target: a Target passed to newModelWith shows in the bar and lights
//     up the quick-action rows. Pasting/typing a path sets the target too.
//   - QUICK ACTIONS: pressing a number key (1..N) when a target is set runs that
//     action's becky-* command immediately (the press is the confirmation — "no
//     typing").
//   - ACT-vs-DISCUSS: a typed clear action ("transcribe this") stages the command
//     and asks y/n; a question/idea/ambiguous turn just replies (no tool call).
//
// Visuals carried over from the original becky-ask; the mechanics add real
// scrollback + input widgets, the target bar, and the action strip.
package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Layout constants. Named rather than magic so the chrome is easy to retune.
const (
	titleHeight  = 1 // the "Ask Becky:" bar
	footerHeight = 2 // blank line + help hint
	inputHeight  = 1 // the textinput line
	chromeHeight = titleHeight + inputHeight + footerHeight
)

// runDoneMsg is delivered when an async becky-* command finishes.
type runDoneMsg struct{ res runResult }

// model is the whole chat state: the rendered transcript, the scrollback viewport,
// the input box, the dropped target, the quick-action menu, any command awaiting
// confirmation, and the local intent client. Update returns a copy (value receiver).
type model struct {
	transcript []string        // each entry is a fully-rendered block (already styled)
	viewport   viewport.Model  // scrollback over the transcript
	input      textinput.Model // the live input line
	width      int
	height     int
	ready      bool // becomes true after the first WindowSizeMsg sizes the viewport

	target  Target        // the dropped/typed file(s)/folder context
	actions []quickAction // quick actions applicable to the current target
	pending []string      // a becky-* command shown and awaiting y/n confirmation
	busy    bool          // a tool is currently running
	cli     *llamaClient  // local Qwen3.5 intent client (nil => deterministic only)
}

// newModel builds an empty-target chat (used by tests and the no-arg launch).
func newModel() model { return newModelWith(Target{}, nil) }

// newModelWith builds the chat with a dropped target and an optional intent client.
func newModelWith(t Target, cli *llamaClient) model {
	ti := textinput.New()
	ti.Placeholder = "Ask Becky anything about your case files…"
	ti.Prompt = inputStyle.Render("> ")
	ti.TextStyle = inputStyle
	ti.Cursor.Style = inputStyle
	ti.CharLimit = 1000
	ti.Focus()

	m := model{
		input:      ti,
		transcript: []string{welcomeBanner()},
		target:     t,
		cli:        cli,
	}
	if t.HasTarget() {
		m.actions = quickActionsFor(t)
		m.transcript = append(m.transcript, targetIntro(t, m.actions))
	}
	return m
}

// Init satisfies tea.Model. The textinput supplies the blinking cursor command.
func (m model) Init() tea.Cmd { return textinput.Blink }

// Update handles key presses, resizes, the run-done message, and submit. Enter
// submits; q/Ctrl+C/Esc quit; a number key fires a quick action; y/n resolves a
// pending confirmation; everything else flows to the input + viewport widgets.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)

	case runDoneMsg:
		m.busy = false
		m.appendBlock(runResultBlock(msg.res))
		return m, nil

	case tea.KeyMsg:
		if handled, nm, cmd := m.handleKey(msg); handled {
			return nm, cmd
		}
	}

	// Hand the message to the input and viewport widgets (typing, cursor, scrolling).
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	cmds = append(cmds, cmd)
	if m.ready {
		m.viewport, cmd = m.viewport.Update(msg)
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

// handleKey processes the keys becky-ask owns (quit, submit, confirm, quick
// actions). It returns handled=false to let the input/viewport widgets see the key.
func (m model) handleKey(msg tea.KeyMsg) (bool, tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC, tea.KeyEsc:
		return true, m, tea.Quit
	case tea.KeyEnter:
		nm, cmd := m.submit()
		return true, nm, cmd
	}

	// While a tool runs, ignore other input (the run is async; keep it simple).
	if m.busy {
		return true, m, nil
	}

	if msg.Type == tea.KeyRunes && m.input.Value() == "" {
		s := string(msg.Runes)
		// Bare "q" quits only when the input is empty (so "q" can be typed in text).
		if s == "q" {
			return true, m, tea.Quit
		}
		// A pending confirmation: y runs it, n cancels. Only when input is empty so
		// it never hijacks normal typing.
		if len(m.pending) > 0 {
			switch strings.ToLower(s) {
			case "y":
				nm, cmd := m.runPending()
				return true, nm, cmd
			case "n":
				m.pending = nil
				m.appendBlock(systemStyle.Render("Cancelled. Nothing was run."))
				return true, m, nil
			}
		}
		// A number key fires the matching quick action (1-based), if a target is set.
		if idx, ok := digitIndex(s); ok && idx < len(m.actions) {
			nm, cmd := m.runAction(m.actions[idx])
			return true, nm, cmd
		}
	}
	return false, m, nil
}

// submit consumes the current input line: echo it, decide via the gate, append the
// reply, and (for a typed clear action) stage the command for y/n confirmation. A
// pasted/typed path with no other words sets the target instead of routing.
func (m model) submit() (tea.Model, tea.Cmd) {
	q := strings.TrimSpace(m.input.Value())
	if q == "" {
		return m, nil
	}
	m.input.SetValue("")

	// If the whole line is a path to an existing file/folder, treat it as a target
	// drop (the brief: "Also accept paths pasted/typed.").
	if t := resolveTarget([]string{q}); t.HasTarget() {
		m.appendBlock(userStyle.Render(labelUser+": ") + q)
		m.setTarget(t)
		return m, nil
	}

	m.appendBlock(userStyle.Render(labelUser+": ") + q)

	// A typed run-list — "1,2" or "transcribe and diarize" — runs several actions at
	// once against the current target, each saved next to the file. Single/ambiguous
	// requests fall through to the normal act-vs-discuss router below.
	if m.target.HasTarget() {
		if ops := parseRunSelection(q, m.actions); len(ops) > 0 {
			return m.startRunOps(ops)
		}
	}

	r := route(context.Background(), m.cli, q, m.target)
	if r.Reply != "" {
		m.appendBlock(beckyStyle.Render(labelBecky+":") + "\n" + r.Reply)
	}
	if len(r.Pending) > 0 {
		m.pending = r.Pending // armed; y/n in handleKey resolves it
	}
	return m, nil
}

// setTarget updates the target context, refreshes the quick actions, and notes it.
func (m *model) setTarget(t Target) {
	m.target = t
	m.actions = quickActionsFor(t)
	m.pending = nil
	m.appendBlock(targetIntro(t, m.actions))
}

// runAction runs a quick action immediately (the button press is the confirmation —
// "no typing"). It routes through startRunOps so the result is SAVED next to the
// input (video.mp4 -> video.srt), exactly like a typed multi-op request.
func (m model) runAction(a quickAction) (tea.Model, tea.Cmd) {
	return m.startRunOps([]actionID{a.ID})
}

// runPending runs the command staged by a typed clear action after y confirms.
func (m model) runPending() (tea.Model, tea.Cmd) {
	cmd := m.pending
	m.pending = nil
	return m.startRun(cmd)
}

// startRun marks the model busy, echoes the command, and runs ONE built command off
// the UI goroutine — SAVING its output next to the input — reporting back via
// runDoneMsg. Used by the typed act-vs-discuss confirm path.
func (m model) startRun(cmd []string) (tea.Model, tea.Cmd) {
	m.busy = true
	m.appendBlock(busyStyle.Render("Running: ") + systemStyle.Render(commandString(cmd)) +
		systemStyle.Render("\n(this can take a moment; source files are never modified)"))
	target := m.target
	run := func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
		defer cancel()
		return runDoneMsg{res: runAndSave(ctx, target, cmd)}
	}
	return m, run
}

// startRunOps runs one or more quick actions against the current target in sequence,
// saving each result to a deterministic sidecar next to the input (video.mp4 ->
// video.srt, video.diarize.json, ...). The source is never modified.
func (m model) startRunOps(ops []actionID) (tea.Model, tea.Cmd) {
	if len(ops) == 0 {
		return m, nil
	}
	m.busy = true
	labels := make([]string, 0, len(ops))
	for _, op := range ops {
		if a, ok := actionByID(op); ok {
			labels = append(labels, a.Label)
		}
	}
	m.appendBlock(busyStyle.Render("Running: ") + systemStyle.Render(strings.Join(labels, " + ")) +
		systemStyle.Render("\n(saving results next to the file; the source is never modified)"))
	target := m.target
	run := func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
		defer cancel()
		return runDoneMsg{res: runWorkflow(ctx, target, ops)}
	}
	return m, run
}

// resize lays out the viewport against the current chrome (incl. the optional
// target bar and action strip), preserving scroll-to-bottom behavior.
func (m *model) resize(w, h int) {
	m.width = w
	m.height = h
	vpHeight := h - chromeHeight - m.extraChromeLines()
	if vpHeight < 1 {
		vpHeight = 1
	}
	if !m.ready {
		m.viewport = viewport.New(w, vpHeight)
		m.viewport.SetContent(m.renderTranscript())
		m.viewport.GotoBottom()
		m.ready = true
	} else {
		m.viewport.Width = w
		m.viewport.Height = vpHeight
		m.viewport.SetContent(m.renderTranscript())
	}
	m.input.Width = w - 4
}

// extraChromeLines counts the optional rows (target bar + action strip) so the
// viewport height stays correct whether or not a target is set.
func (m model) extraChromeLines() int {
	n := 0
	if m.target.HasTarget() {
		n++ // target bar
	}
	if len(m.actions) > 0 {
		n++ // action strip
	}
	return n
}

// appendBlock adds a rendered block to the transcript and scrolls to it.
func (m *model) appendBlock(block string) {
	m.transcript = append(m.transcript, block)
	if m.ready {
		m.viewport.SetContent(m.renderTranscript())
		m.viewport.GotoBottom()
	}
}

// renderTranscript joins the conversation blocks with a blank line between each.
func (m model) renderTranscript() string {
	wrap := m.width
	if wrap <= 0 {
		wrap = 80
	}
	style := lipgloss.NewStyle().Width(wrap)
	var b strings.Builder
	for i, block := range m.transcript {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(style.Render(block))
	}
	return b.String()
}

// View composes the title bar, optional target bar, scrollback, optional action
// strip, the input line, and the footer help. Before the first resize it shows a
// minimal placeholder (no crash on 0x0).
func (m model) View() string {
	if !m.ready {
		return titleBarStyle.Render("Ask Becky:") + "\n\n" + helpStyle.Render("starting…")
	}

	var b strings.Builder
	b.WriteString(titleBarStyle.Render("Ask Becky:"))
	b.WriteString("\n")
	if m.target.HasTarget() {
		b.WriteString(m.targetBar())
		b.WriteString("\n")
	}
	b.WriteString(m.viewport.View())
	b.WriteString("\n")
	if len(m.actions) > 0 {
		b.WriteString(m.actionStrip())
		b.WriteString("\n")
	}
	b.WriteString(m.input.View())
	b.WriteString("\n\n")
	b.WriteString(helpStyle.Render(m.helpLine()))
	return b.String()
}

// targetBar renders the "Target: <file>" context line.
func (m model) targetBar() string {
	return targetBarStyle.Render("Target: ") + targetBarStyle.Render(m.target.Label())
}

// actionStrip renders the quick-action rows as "[1] Transcribe  [2] Identify …".
func (m model) actionStrip() string {
	parts := make([]string, 0, len(m.actions))
	for i, a := range m.actions {
		parts = append(parts, actionRowStyle.Render(fmt.Sprintf("[%d] %s", i+1, a.Label)))
	}
	return actionRowStyle.Render("Quick: ") + strings.Join(parts, "  ")
}

// helpLine reproduces the original footer copy, adapting to state (busy, a pending
// confirmation, quick actions available, scroll position).
func (m model) helpLine() string {
	switch {
	case m.busy:
		return "Running a tool… please wait | Ctrl+C: quit"
	case len(m.pending) > 0:
		return "Press y to run · n to cancel | q: quit"
	}
	base := "Type your question and press Enter | q: quit"
	if len(m.actions) > 0 {
		base = "Press 1-" + itoaSmall(len(m.actions)) + " for a quick action, or type a question | q: quit"
	}
	if m.ready && m.viewport.TotalLineCount() > m.viewport.Height {
		return fmt.Sprintf("%s | PgUp/PgDn: scroll (%3.0f%%)", base, m.viewport.ScrollPercent()*100)
	}
	return base
}

// targetIntro is the chat block shown when a target is set: what it is and the
// one-key actions available for it.
func targetIntro(t Target, actions []quickAction) string {
	var b strings.Builder
	b.WriteString(beckyStyle.Render("Target set: ") + userStyle.Render(t.Label()))
	if len(t.Missing) > 0 {
		b.WriteString("\n")
		b.WriteString(systemStyle.Render("(couldn't find: " + strings.Join(t.Missing, ", ") + ")"))
	}
	if len(actions) == 0 {
		b.WriteString("\n")
		b.WriteString(systemStyle.Render("No one-key action fits this exact target; ask me in plain words."))
		return b.String()
	}
	b.WriteString("\n")
	b.WriteString(beckyStyle.Render("One-key actions (no typing): "))
	b.WriteString("\n")
	for i, a := range actions {
		b.WriteString("\n")
		b.WriteString(actionRowStyle.Render(fmt.Sprintf("  [%d] %-10s", i+1, a.Label)))
		b.WriteString(systemStyle.Render(" — " + a.Hint))
	}
	b.WriteString("\n\n")
	b.WriteString(systemStyle.Render("Press a number to run it now, or just tell me what you want."))
	return b.String()
}

// runResultBlock renders a finished command's outcome plainly. JSON stdout is
// shown trimmed (the heavy detail lives in the tool's own output / files).
func runResultBlock(res runResult) string {
	var b strings.Builder
	if res.Err != nil {
		hdr := commandString(res.Command)
		if hdr == "" {
			hdr = "the request"
		}
		b.WriteString(busyStyle.Render("Couldn't finish ") + systemStyle.Render(hdr))
		b.WriteString("\n")
		b.WriteString(systemStyle.Render(res.Err.Error()))
		for _, s := range res.Saved {
			b.WriteString("\n" + beckyStyle.Render("Saved: ") + systemStyle.Render(s))
		}
		return b.String()
	}
	hdr := commandString(res.Command)
	if hdr == "" {
		hdr = "your request"
	}
	b.WriteString(beckyStyle.Render("Done: ") + systemStyle.Render(hdr))
	for _, s := range res.Saved {
		b.WriteString("\n" + beckyStyle.Render("Saved: ") + systemStyle.Render(s))
	}
	out := strings.TrimSpace(res.Stdout)
	if out != "" {
		b.WriteString("\n")
		b.WriteString(systemStyle.Render(headOf(out, 1200)))
	}
	return b.String()
}

// modelFromArgs builds the model from argv paths + a Qwen3.5 intent client. main
// calls this so a dropped/passed path lands as the Target before the TUI starts.
func modelFromArgs(args []string) model {
	t := resolveTarget(args)
	cli := newLlamaClient(resolveIntentModel(), resolveLlamaServer(), nil)
	return newModelWith(t, cli)
}

// --- tiny render helpers ---

func digitIndex(s string) (int, bool) {
	if len(s) != 1 || s[0] < '1' || s[0] > '9' {
		return 0, false
	}
	return int(s[0] - '1'), true
}

func itoaSmall(n int) string { return fmt.Sprintf("%d", n) }

func headOf(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + " …(truncated)"
}
