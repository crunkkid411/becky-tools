//go:build gui

// becky-canvas (GUI) — Jordan's VISUAL, icon-first creative front door to the becky
// tools. Built with the `gui` tag so the default `go build ./...` stays green (the
// headless scene model in main.go is `//go:build !gui`).
//
// Toolkit: Gio (gioui.org) — pure Go, no cgo, Direct3D 11 on Windows.
//
//	go run -tags gui ./cmd/canvas
//	go build -tags gui -o bin/becky-canvas.exe ./cmd/canvas
//
// The window (redesigned 2026-06-15, "colours and shapes > text"):
//   - A DOCK of big neon icon buttons (left): record, draw, piano, drum, video, open.
//   - The CANVAS (centre): the star. Shows a dropped file's waveform, a project.json
//     DAW scene, the clickable drum step grid, the piano roll, or pen strokes.
//   - One unobtrusive AGENT box (bottom): type a tool/phrase, Enter routes + runs it.
//   - A SMALL, collapsible, selectable OUTPUT area (hidden until there's output).
//
// Drag-and-drop, BOTH ways:
//   - argv on launch: dropping a file/folder onto becky-canvas.exe sets it as the target
//     (like becky-ask.exe).
//   - in-window OS file drop: dropping a file onto the window sets it as the target.
//
// degrade-never-crash (CLAUDE.md §2): every failure shows a friendly neon line; the
// window never panics. Tool runs happen off-thread and post back with w.Invalidate().
package main

import (
	"image"
	"image/color"
	"io"
	"os"
	"strings"
	"sync"

	"gioui.org/app"
	"gioui.org/font/gofont"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/transfer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"becky-go/internal/canvas"
	"becky-go/internal/dawmodel"
	"becky-go/internal/undo"
	"becky-go/internal/winctx"
)

// App holds all GUI state. It lives on the UI goroutine; the only cross-goroutine state
// is guarded by mu (the streamed tool output and the worker-built visual).
type App struct {
	th     *material.Theme
	window *app.Window
	icons  iconSet

	// activeMode is the canvas surface currently shown (ask/video/daw/midi/drum).
	activeMode canvas.Mode

	// Dock buttons (icon-first; no text list).
	dockRecord widget.Clickable
	dockDraw   widget.Clickable
	dockPiano  widget.Clickable
	dockDrum   widget.Clickable
	dockMixer  widget.Clickable
	dockAudio  widget.Clickable
	dockVideo  widget.Clickable
	dockOpen   widget.Clickable

	// hub holds the "open a real tool" launch buttons (becky-canvas is the central
	// hub: Drum Machine / REAPER DAW / Clip / NLE / Ask, each its own window).
	hub *hubLauncher

	// Catalog kept for command routing (the agent box matches a phrase to a tool).
	tools []toolItem

	// Target file/folder the tools act on. Set by argv, file drop, or the open button.
	target string

	// Agent box (one unobtrusive single line at the bottom).
	command widget.Editor
	runBtn  widget.Clickable

	// Output area (read-only, selectable) — small + collapsible, hidden until used.
	output      widget.Editor
	outputList  widget.List
	clearBtn    widget.Clickable
	expandBtn   widget.Clickable
	outExpanded bool

	// Drum mode state (the clickable step grid).
	drum drumGrid

	// Draw mode state (freehand pen strokes over the canvas).
	drawMode  bool
	drawing   bool
	curStroke stroke
	strokes   []stroke

	// canvasTag is the event.Tag for the canvas pointer area (drum clicks / pen drags).
	canvasTag bool

	// Cross-goroutine state.
	mu        sync.Mutex
	logBuf    strings.Builder // accumulated tool output
	running   bool            // a tool run is in flight
	runLabel  string          // friendly name of the running tool
	vis       visual          // current drawable representation of the target
	lastVisOf string          // the target string vis was built for (avoid rework)

	// disableDrop is set by handleGioEvent (dragdrop_windows.go) once IDropTarget
	// is registered on the HWND.  It is called on DestroyEvent to revoke the
	// registration cleanly.  Nil until first Win32ViewEvent arrives.
	disableDrop func()

	// ── Play / Stop controls (drum + piano modes) ─────────────────────────────
	// playBtn / stopBtn are the ▶ / ■ icon buttons shown in drum+piano modes.
	playBtn widget.Clickable
	stopBtn widget.Clickable

	// Toolbar: Save / Load / Undo / Redo — visible buttons so a creative never has
	// to TYPE "save"/"undo"; they call the existing spine methods (gui_toolbar.go).
	saveBtn widget.Clickable
	loadBtn widget.Clickable
	undoBtn widget.Clickable
	redoBtn widget.Clickable
	// playing is true while a becky-daw-engine --play-pattern-audio run is live.
	playing bool
	// playProc is the live becky-daw-engine process (guarded by mu) so ■ Stop can
	// kill it immediately mid-loop. Nil when nothing is playing.
	playProc *os.Process

	// explorerChip holds the folder name pre-filled from winctx on Open, shown as a
	// small neon chip the user can confirm before the full file picker opens.
	explorerChip string

	// ── Select→Ask→Transform loop state (§6 item #2) ──────────────────────
	//
	// arr is the SINGLE SOURCE OF MUSICAL TRUTH (CANVAS-BLUEPRINT.md spine): the
	// rich editable dawmodel.Arrangement the panels (piano/drum/mixer/audio) read
	// and edit via the immutable dawmodel verbs. The panels never own state; they
	// produce a new Arrangement and hand it to applyArr.
	arr *dawmodel.Arrangement

	// hist is the undo/redo history of arrangement snapshots; applyArr pushes every
	// commit, so any edit (panel or agent) is undoable ("undo"/"redo" in the box).
	hist *undo.History

	// sessionPath is where "save" writes the working arrangement (set when a .json/
	// .mid session is loaded; "" until then → save falls back to becky-session.json).
	sessionPath string

	// Panels — each owns its own file + state struct (gui_*panel.go); layoutVisual
	// dispatches to the active one by mode. They read a.arr and emit via applyArr.
	pianoPanel *pianoPanel
	drumPanel  *drumPanelState
	mixerPanel *mixerPanel
	audioPanel *audioPanel

	// scene is the DERIVED render-cache rebuilt from arr after every edit
	// (canvasbridge.SceneFromArrangement) — never edited directly. It feeds the
	// lane-overview surface + the overlay Apply/Reject path.
	scene canvas.Scene

	// selection is what Jordan has currently selected on the canvas. The zero
	// value (Kind=SelectNone) means "nothing selected" — the agent box falls
	// through to the existing keyword tool-routing path in that case.
	selection canvas.Selection

	// transformer is the Propose implementation. It is wired to StubTransformer
	// by default; swap it for a real model implementation when the model binary
	// is present (the local GPU boundary, see internal/canvas/transform.go).
	transformer canvas.Transformer

	// overlay holds the Gio widget state for the "show me, don't do it" preview.
	overlay overlayWidget

	// overlayEnterPressed / overlayEscPressed are set by the Gio keyboard path
	// (the command editor's Submit event) so handleOverlayInput can approve or
	// reject without relying on button clicks.
	overlayEnterPressed bool
	overlayEscPressed   bool

	// incomingProposal is set by the async transform goroutine (the real local
	// model takes ~10-30s to load on first use, so Propose runs OFF the UI thread).
	// It is guarded by mu and drained into the overlay on the next frame by the UI
	// goroutine — overlay.pending itself is only ever touched on the UI thread.
	incomingProposal *canvas.Proposal
}

func main() {
	go func() {
		w := new(app.Window)
		w.Option(app.Title("becky-canvas"), app.Size(unit.Dp(1180), unit.Dp(760)))
		a := newApp(w)
		a.adoptArgv(os.Args[1:]) // drag-onto-exe: argv paths become the target
		a.applyStartupMode()     // BECKY_CANVAS_MODE=drum opens straight into the drum machine
		if err := a.loop(); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}()
	app.Main()
}

// newApp builds the App with its theme, icons, catalog, and widget state initialized.
func newApp(w *app.Window) *App {
	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))

	a := &App{
		th:          th,
		window:      w,
		icons:       loadIcons(),
		activeMode:  canvas.ModeAsk,
		tools:       catalog(),
		scene:       canvas.NewScene(canvas.ModeAsk),
		transformer: canvas.PickTransformer(), // real model when present, stub otherwise
		hub:         newHubLauncher(),
		arr:         dawmodel.New(), // empty editable session; load fills it
		pianoPanel:  newPianoPanel(),
		drumPanel:   newDrumPanelState(),
		mixerPanel:  newMixerPanel(),
		audioPanel:  newAudioPanel(),
	}
	a.outputList.Axis = layout.Vertical
	a.command.SingleLine = true
	a.command.Submit = true
	a.output.ReadOnly = true // selectable + copyable, not editable
	return a
}

// adoptArgv treats any existing file/folder path in argv as the target — the
// "drag a file onto becky-canvas.exe" workflow, mirroring becky-ask.exe. The FIRST
// existing path wins; non-existent args are ignored (degrade, never crash).
func (a *App) adoptArgv(args []string) {
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg == "" {
			continue
		}
		if _, err := os.Stat(arg); err == nil {
			a.setTarget(arg)
			return
		}
	}
}

// applyStartupMode lets BECKY_CANVAS_MODE pick the opening panel — "drum" opens
// straight into the drum machine with a starter beat, "piano"/"mixer" select those.
// Handy for jumping right in, and for deterministic GUI screenshots in verification.
// Empty/unknown leaves the default (Ask). Never crashes on a bad value.
func (a *App) applyStartupMode() {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("BECKY_CANVAS_MODE"))) {
	case "drum":
		a.applyArr(ensureDrumMachineArr(a.arr))
		a.activeMode = canvas.ModeDrum
	case "piano", "midi":
		a.activeMode = canvas.ModeMIDI
	case "mixer", "daw":
		a.activeMode = canvas.ModeDAW
	case "audio":
		a.activeMode = canvas.ModeAudio
	}
}

// loop is the Gio event loop: handle the window's events, lay out a frame, repeat.
func (a *App) loop() error {
	var ops op.Ops
	for {
		e := a.window.Event()
		// handleGioEvent is platform-split (dragdrop_windows.go / dragdrop_other.go).
		// On Windows it intercepts the first Win32ViewEvent to register IDropTarget;
		// on other platforms it is a no-op.  Called before the type-switch so the
		// ViewEvent is seen even though it is not handled in the switch below.
		handleGioEvent(a, e)
		switch ev := e.(type) {
		case app.DestroyEvent:
			// Revoke IDropTarget registration before the window is gone (Windows only;
			// on other platforms disableDrop is nil and the call is skipped).
			if a.disableDrop != nil {
				a.disableDrop()
			}
			return ev.Err
		case app.FrameEvent:
			gtx := app.NewContext(&ops, ev)
			a.handleInput(gtx)
			a.layoutFrame(gtx)
			ev.Frame(gtx.Ops)
		}
	}
}

// handleInput processes dock clicks, the agent box, and the output controls before
// layout so the frame reflects them. (Canvas pointer events are handled during layout,
// where the canvas area's local coordinate system is in scope.)
func (a *App) handleInput(gtx layout.Context) {
	// Drain an async model proposal (computed off-thread) into the overlay. Only the
	// UI goroutine touches overlay.pending, so this hand-off stays race-free.
	a.mu.Lock()
	if a.incomingProposal != nil {
		a.overlay.show(a.incomingProposal)
		a.incomingProposal = nil
	}
	a.mu.Unlock()

	// Dock buttons.
	if a.dockRecord.Clicked(gtx) {
		a.startRecord()
	}
	if a.dockDraw.Clicked(gtx) {
		a.drawMode = !a.drawMode
	}
	if a.dockPiano.Clicked(gtx) {
		a.activeMode = canvas.ModeMIDI
	}
	if a.dockDrum.Clicked(gtx) {
		a.activeMode = canvas.ModeDrum
		// Opening the Drum machine must OPEN A DRUM MACHINE, not show a "load a
		// file" placeholder: drop in a default starter beat when none exists.
		if next := ensureDrumMachineArr(a.arr); next != a.arr {
			a.applyArr(next)
		}
	}
	if a.dockMixer.Clicked(gtx) {
		a.activeMode = canvas.ModeDAW
	}
	if a.dockAudio.Clicked(gtx) {
		a.activeMode = canvas.ModeAudio
	}
	if a.dockVideo.Clicked(gtx) {
		a.activeMode = canvas.ModeVideo
	}
	if a.dockOpen.Clicked(gtx) {
		a.startExplorerAwareImport()
	}

	// Hub launch buttons (open a real standalone tool window).
	a.handleHubInput(gtx)

	// Play / Stop buttons (drum + piano modes).
	if a.playBtn.Clicked(gtx) {
		a.startPlay()
	}
	if a.stopBtn.Clicked(gtx) {
		a.stopPlay()
	}

	// Toolbar: Save / Load / Undo / Redo — call the existing spine methods.
	if a.saveBtn.Clicked(gtx) {
		a.saveSession("")
	}
	if a.loadBtn.Clicked(gtx) {
		a.startExplorerAwareImport()
	}
	if a.undoBtn.Clicked(gtx) {
		a.undo()
	}
	if a.redoBtn.Clicked(gtx) {
		a.redo()
	}

	// Overlay keyboard: Esc = reject, handled here before the agent box consumes it.
	a.handleOverlayKeys(gtx)

	// Overlay approve/reject (must run before agent-box submit so Enter while an
	// overlay is open approves the proposal rather than re-submitting the box).
	a.handleOverlayInput(gtx)

	// Agent box submit / Run button.
	runCmd := a.runBtn.Clicked(gtx)
	for {
		ev, ok := a.command.Update(gtx)
		if !ok {
			break
		}
		if _, isSubmit := ev.(widget.SubmitEvent); isSubmit {
			runCmd = true
		}
	}
	if runCmd {
		if a.overlay.hasPending() {
			// Run clicked while a proposal is open → approve it (Enter does the same
			// via handleOverlayKeys; the keyboard path holds focus when pending).
			a.applyProposal()
		} else {
			// Normal path: propose (or route a tool keyword).
			a.proposeForInstruction(a.command.Text())
		}
	}

	// Output controls.
	if a.clearBtn.Clicked(gtx) {
		a.mu.Lock()
		a.logBuf.Reset()
		a.mu.Unlock()
		a.window.Invalidate()
	}
	if a.expandBtn.Clicked(gtx) {
		a.outExpanded = !a.outExpanded
	}

	// Keep the visual in sync with the target.
	a.refreshVisual()
}

// --- target -----------------------------------------------------------------------

// setTarget records a new target path and rebuilds the canvas visual for it. A drawable
// file (.wav / project.json) switches the canvas back to a file surface so the drop is
// visible; other modes are left as-is.
func (a *App) setTarget(path string) {
	path = strings.TrimSpace(path)
	a.target = path
	if path == "" {
		return
	}
	a.appendLine("Target: " + path)

	// Load a session into the editable arrangement (the spine) when it's a
	// becky-compose project.json or a .mid, and show it in the DAW view so the
	// panels (mixer/piano/drum) work on the real model.
	if a.maybeLoadArrangement(path) {
		a.activeMode = canvas.ModeDAW
		a.refreshVisual()
		a.window.Invalidate()
		return
	}

	lower := strings.ToLower(path)
	if strings.HasSuffix(lower, ".wav") || strings.HasSuffix(lower, ".json") {
		if a.activeMode == canvas.ModeDrum || a.activeMode == canvas.ModeMIDI {
			a.activeMode = canvas.ModeAsk
		}
	}
	a.refreshVisual()
	a.window.Invalidate()
}

// --- actions ---------------------------------------------------------------------

// startTool launches a tool run if one isn't already in flight.
func (a *App) startTool(t toolItem) {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		a.appendLine("Still running " + a.runLabel + "… let it finish first.")
		return
	}
	a.running = true
	a.runLabel = t.Label
	a.mu.Unlock()

	a.outExpanded = true // show output when a tool runs
	a.appendLine("")
	a.appendLine("=== " + t.Label + " ===")

	results := make(chan runResult, 64)
	go runTool(t, a.target, results)
	go a.drainResults(results)
}

// startRecord runs becky-daw-engine to record a short clip from the mic and sets the new
// WAV as the target. It needs the audio build of becky-daw-engine; if that exe/feature is
// absent the run degrades to a friendly neon line (handled by runRecord on a goroutine).
func (a *App) startRecord() {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		a.appendLine("Still running " + a.runLabel + "… let it finish first.")
		return
	}
	a.running = true
	a.runLabel = "Record"
	a.mu.Unlock()

	a.outExpanded = true
	a.appendLine("")
	a.appendLine("=== Record ===")
	out := recordOutPath()
	results := make(chan runResult, 64)
	go runRecord(out, recordSeconds, results)
	go a.drainRecord(results, out)
}

// runCommand routes a typed phrase to a tool and runs it (v1 = action routing only).
func (a *App) runCommand(phrase string) {
	phrase = strings.TrimSpace(phrase)
	if phrase == "" {
		return
	}
	// AI edit batch: a JSON BeckyEditBatch (emitted by a model, or pasted) applies to
	// the loaded session through the deterministic ctledit applier — the
	// select→ask→transform seam. Falls through to keyword routing when it isn't JSON.
	if strings.HasPrefix(phrase, "{") {
		if a.applyEditBatch(phrase) {
			a.command.SetText("")
			return
		}
	}
	// Plain-English generative beat phrases (deterministic; no model needed):
	// "randomize the beat", "make a house beat", "four on the floor", etc. apply
	// to the loaded drum clip. Falls through to tool routing when unrecognised.
	if a.applyPhrase(phrase) {
		a.command.SetText("")
		return
	}
	// NL arrangement edits via ctlmodel (keyword proposer offline; model proposer
	// when BECKY_CTL_BIN + BECKY_CTL_MODEL are set): "mute the bass", "set tempo
	// to 140", "pan the lead right", etc. Falls through when no edits produced.
	if a.applyNL(phrase) {
		a.command.SetText("")
		return
	}
	t, ok := matchTool(phrase)
	if !ok {
		a.outExpanded = true
		a.appendLine("")
		a.appendLine("I couldn't match \"" + phrase + "\" to a tool. Try a tool name (transcribe, compose, research) or use the icons on the left.")
		a.command.SetText("")
		return
	}
	a.command.SetText("")
	a.appendLine("(matched \"" + phrase + "\" -> " + t.Label + ")")
	a.startTool(t)
}

// drainResults reads streamed lines off the channel and appends them, repainting after
// each so output appears live. Runs on its own goroutine.
func (a *App) drainResults(results <-chan runResult) {
	for r := range results {
		if r.Line != "" {
			a.appendLine(r.Line)
		}
		if r.Done {
			a.mu.Lock()
			a.running = false
			a.runLabel = ""
			a.mu.Unlock()
		}
	}
	a.window.Invalidate()
}

// drainRecord is drainResults for a recording run: on success it adopts the new WAV as
// the target so its waveform appears on the canvas immediately.
func (a *App) drainRecord(results <-chan runResult, out string) {
	for r := range results {
		if r.Line != "" {
			a.appendLine(r.Line)
		}
		if r.Done {
			a.mu.Lock()
			a.running = false
			a.runLabel = ""
			a.mu.Unlock()
			if fileExists(out) {
				a.setTarget(out)
			}
		}
	}
	a.window.Invalidate()
}

// startBrowse opens the native picker on a worker goroutine and sets the chosen path as
// the target. The UI thread is never blocked by the modal dialog.
func (a *App) startBrowse(wantFolder bool) {
	go func() {
		path, err := browseForPath(wantFolder)
		if err != nil {
			a.appendLine(err.Error())
			a.window.Invalidate()
			return
		}
		if path == "" {
			return // cancelled
		}
		a.setTarget(path)
	}()
}

// startExplorerAwareImport is the Explorer-aware version of the Open dock button.
// It tries winctx.ForegroundExplorerFolder() first — if Jordan has an Explorer
// window open, we pre-fill that folder so he can confirm it without a dialog.
// If winctx returns empty or an error (non-Windows, no Explorer open, etc.) we
// fall through to the standard Browse dialog. Degrade, never crash.
func (a *App) startExplorerAwareImport() {
	go func() {
		folder, err := winctx.ForegroundExplorerFolder()
		if err == nil && folder != "" {
			// Store the chip for the UI to render, then ask the user to confirm.
			a.mu.Lock()
			a.explorerChip = folder
			a.mu.Unlock()
			a.window.Invalidate()
			// Confirm: open a file picker scoped to that folder via PowerShell
			// (reuse the existing file picker but start in the Explorer folder).
			go func() {
				path, perr := browseForPathIn(folder)
				if perr != nil {
					// Fall back to the unscoped picker silently.
					path2, _ := browseForPath(false)
					path = path2
				}
				a.mu.Lock()
				a.explorerChip = "" // dismiss chip
				a.mu.Unlock()
				if path != "" {
					a.setTarget(path)
				}
				a.window.Invalidate()
			}()
			return
		}
		// winctx not available or no Explorer window — use the standard picker.
		a.startBrowse(false)
	}()
}

// handleOverlayKeys gives the pending "show me" proposal keyboard control:
//
//	Esc   → reject (discard the proposal, scene unchanged)
//	Enter → approve (apply the proposal)
//
// While a proposal is pending we claim a window-sized key area and focus it (the
// agent editor was cleared on submit, so the live decision is approve/reject). When
// no proposal is pending this is a no-op and focus returns to normal. Gio v0.10 key
// API: register the tag with event.Op, request focus with key.FocusCmd, read with
// gtx.Event(key.Filter{...}).
func (a *App) handleOverlayKeys(gtx layout.Context) {
	if !a.overlay.hasPending() {
		return
	}
	tag := &a.overlayEscPressed
	defer clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, tag)
	gtx.Execute(key.FocusCmd{Tag: tag})

	for {
		ev, ok := gtx.Event(
			key.Filter{Focus: tag, Name: key.NameEscape},
			key.Filter{Focus: tag, Name: key.NameReturn},
		)
		if !ok {
			break
		}
		ke, ok := ev.(key.Event)
		if !ok || ke.State != key.Press {
			continue
		}
		switch ke.Name {
		case key.NameEscape:
			a.rejectProposal()
			return
		case key.NameReturn:
			a.applyProposal()
			return
		}
	}
}

// startPlay serialises the current drum/piano pattern to a temp project.json and
// execs becky-daw-engine --play-pattern-audio <json> beside the canvas exe. All
// failures degrade to a quiet neon line — never a crash, never a block on the UI.
func (a *App) startPlay() {
	a.mu.Lock()
	if a.playing {
		a.mu.Unlock()
		return // already playing
	}
	a.playing = true
	a.mu.Unlock()

	a.appendLine("")
	a.appendLine("▶ Play …")

	go func() {
		if err := a.execPlay(a.target, a.activeMode, &a.drum); err != nil {
			a.appendLine(err.Error())
		}
		a.mu.Lock()
		a.playing = false
		a.mu.Unlock()
		a.window.Invalidate()
	}()
}

// stopPlay kills the live becky-daw-engine process so the loop stops immediately,
// and clears the playing indicator. execPlay treats a killed process as a clean stop
// (not an error). Safe to call when nothing is playing.
func (a *App) stopPlay() {
	a.mu.Lock()
	proc := a.playProc
	a.playProc = nil
	a.playing = false
	a.mu.Unlock()
	if proc != nil {
		_ = proc.Kill()
	}
	a.appendLine("■ Stopped.")
	a.window.Invalidate()
}

// refreshVisual rebuilds the drawable representation when the target changed. The decode
// runs on a worker goroutine for non-trivial files; the result is stored under mu.
func (a *App) refreshVisual() {
	target := strings.TrimSpace(a.target)
	a.mu.Lock()
	if target == a.lastVisOf {
		a.mu.Unlock()
		return
	}
	a.lastVisOf = target
	a.mu.Unlock()

	go func() {
		v := buildVisual(target)
		a.mu.Lock()
		if target == a.lastVisOf { // only apply if the target hasn't changed again
			a.vis = v
		}
		a.mu.Unlock()
		a.window.Invalidate()
	}()
}

// appendLine adds a line to the output buffer (thread-safe) and repaints.
func (a *App) appendLine(s string) {
	a.mu.Lock()
	a.logBuf.WriteString(s)
	a.logBuf.WriteByte('\n')
	a.mu.Unlock()
	a.window.Invalidate()
}

// --- layout ----------------------------------------------------------------------

// layoutFrame draws the whole window: the dock (left) + the work column (canvas on top,
// agent box + collapsible output beneath).
func (a *App) layoutFrame(gtx layout.Context) {
	paint.Fill(gtx.Ops, colWindowBg)
	a.registerWindowDrop(gtx) // in-window OS file drop -> target
	layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
		layout.Rigid(a.layoutDock),
		layout.Flexed(1, a.layoutWorkColumn),
	)
}

// layoutWorkColumn stacks the big canvas, then the "show me" overlay (zero
// height when no proposal is pending), then the agent box, then the output.
func (a *App) layoutWorkColumn(gtx layout.Context) layout.Dimensions {
	return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Flexed(1, a.layoutCanvas),
			layout.Rigid(a.layoutToolbar),   // Save / Load / Undo / Redo row
			layout.Rigid(a.layoutTransport), // ▶ / ■ for drum + piano — zero height otherwise
			layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			layout.Rigid(a.layoutOverlay), // "show me, don't do it" — zero height when idle
			layout.Rigid(a.layoutAgentBox),
			layout.Rigid(a.layoutOutput),
		)
	})
}

// layoutCanvas frames the visual surface with a mode-coloured border and captures pointer
// input over it (drum clicks / pen drags).
func (a *App) layoutCanvas(gtx layout.Context) layout.Dimensions {
	accent := modeAccent(a.activeMode)
	return borderBox(gtx, accent, func(gtx layout.Context) layout.Dimensions {
		dims := a.layoutVisual(gtx, a.th)
		a.captureCanvasPointer(gtx, dims.Size)
		return dims
	})
}

// captureCanvasPointer registers the canvas area as a pointer target and routes events:
// in DRAW mode, press/drag/release build a pen stroke; in DRUM mode, a press toggles the
// clicked step cell. Coordinates are canvas-local (the area is pushed at the origin).
func (a *App) captureCanvasPointer(gtx layout.Context, size image.Point) {
	if size.X <= 0 || size.Y <= 0 {
		return
	}
	tag := &a.canvasTag
	area := clip.Rect{Max: size}.Push(gtx.Ops)
	event.Op(gtx.Ops, tag)
	area.Pop()

	for {
		ev, ok := gtx.Event(pointer.Filter{
			Target: tag,
			Kinds:  pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel,
		})
		if !ok {
			break
		}
		pe, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		a.onCanvasPointer(pe, size)
	}
}

// onCanvasPointer handles one pointer event over the canvas for the active interaction.
func (a *App) onCanvasPointer(pe pointer.Event, size image.Point) {
	switch {
	case a.drawMode:
		switch pe.Kind {
		case pointer.Press:
			a.beginStroke(pe.Position)
		case pointer.Drag:
			a.extendStroke(pe.Position)
		case pointer.Release, pointer.Cancel:
			a.endStroke()
		}
		a.window.Invalidate()
	case a.activeMode == canvas.ModeDrum:
		if pe.Kind == pointer.Press {
			if lane, step, ok := drumCellAt(pe.Position, size); ok {
				was := a.drum.cells[lane][step]
				now := !was
				a.drum.cells[lane][step] = now
				a.logDrumEdit(lane, step, was, now) // learn his by-eye beat fixes
				a.window.Invalidate()
			}
		}
	}
}

// layoutAgentBox draws the single, unobtrusive agent input line + a small run icon. It is
// deliberately quiet — one line, dim hint, out of the way (per the spec).
func (a *App) layoutAgentBox(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return fieldBox(gtx, func(gtx layout.Context) layout.Dimensions {
				ed := material.Editor(a.th, &a.command, "ask becky… (e.g. transcribe, compose, research)")
				ed.Color = colText
				ed.HintColor = colTextDim
				ed.TextSize = unit.Sp(13)
				return ed.Layout(gtx)
			})
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
		layout.Rigid(a.iconBtn(&a.runBtn, a.icons.run, colNeonGreen)),
	)
}

// layoutOutput draws the SMALL, collapsible, selectable output area. It is hidden when
// empty; when there's output a thin header bar with an expand/clear control sits above a
// short (or expanded) selectable log. It never dominates the window.
func (a *App) layoutOutput(gtx layout.Context) layout.Dimensions {
	a.mu.Lock()
	logText := a.logBuf.String()
	a.mu.Unlock()
	if strings.TrimSpace(logText) == "" {
		return layout.Dimensions{} // nothing yet -> take no space
	}
	if a.output.Text() != logText {
		a.output.SetText(logText)
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
		layout.Rigid(a.layoutOutputHeader),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			// Short by default; taller when expanded. Capped so it never eats the canvas.
			h := gtx.Dp(unit.Dp(72))
			if a.outExpanded {
				h = gtx.Dp(unit.Dp(220))
			}
			gtx.Constraints.Min.Y = h
			gtx.Constraints.Max.Y = h
			return borderBox(gtx, colGridLine, func(gtx layout.Context) layout.Dimensions {
				return widgetBg(gtx, colCanvasBg, func(gtx layout.Context) layout.Dimensions {
					return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return material.List(a.th, &a.outputList).Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
							ed := material.Editor(a.th, &a.output, "")
							ed.Color = colText
							ed.TextSize = unit.Sp(12)
							return ed.Layout(gtx)
						})
					})
				})
			})
		}),
	)
}

// layoutOutputHeader draws the thin output bar: a dim "output" label + expand/collapse
// and clear icon buttons. The only chrome the output gets.
func (a *App) layoutOutputHeader(gtx layout.Context) layout.Dimensions {
	chevron := a.icons.expand
	if !a.outExpanded {
		chevron = a.icons.collapse
	}
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			lbl := material.Caption(a.th, "output")
			lbl.Color = colTextDim
			return lbl.Layout(gtx)
		}),
		layout.Rigid(a.iconBtn(&a.expandBtn, chevron, colTextDim)),
		layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
		layout.Rigid(a.iconBtn(&a.clearBtn, a.icons.clear, colTextDim)),
	)
}

// --- small layout helpers --------------------------------------------------------

// smallIconSize is the side length of the inline (non-dock) icon buttons.
const smallIconSize = 30

// iconBtn returns a widget drawing a compact square icon button (used by the agent run
// control and the output header). Hover brightens the icon; a nil icon falls back to a
// neon square so it's still clickable.
func (a *App) iconBtn(btn *widget.Clickable, ic *widget.Icon, col color.NRGBA) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		side := gtx.Dp(unit.Dp(smallIconSize))
		gtx.Constraints = layout.Exact(image.Pt(side, side))
		return btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			c := col
			bg := colCanvasBg
			if btn.Hovered() {
				c = colNeonGreen
				bg = colHeaderBg
			}
			fillRRect(gtx.Ops, image.Rect(0, 0, side, side), 6, bg)
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				isz := gtx.Dp(unit.Dp(18))
				gtx.Constraints = layout.Exact(image.Pt(isz, isz))
				if ic != nil {
					return ic.Layout(gtx, c)
				}
				fillRRect(gtx.Ops, image.Rect(0, 0, isz, isz), 3, c)
				return layout.Dimensions{Size: image.Pt(isz, isz)}
			})
		})
	}
}

// widgetBg fills the widget's area with a background colour, then draws w on top (the
// standard Gio "background under a widget" idiom).
func widgetBg(gtx layout.Context, bg color.NRGBA, w layout.Widget) layout.Dimensions {
	macro := op.Record(gtx.Ops)
	dims := w(gtx)
	call := macro.Stop()

	rect := clip.Rect{Max: dims.Size}
	defer rect.Push(gtx.Ops).Pop()
	paint.ColorOp{Color: bg}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	call.Add(gtx.Ops)
	return dims
}

// fieldBox wraps a text editor in a padded, canvas-coloured box so it reads as an input.
func fieldBox(gtx layout.Context, w layout.Widget) layout.Dimensions {
	return widgetBg(gtx, colCanvasBg, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(unit.Dp(8)).Layout(gtx, w)
	})
}

// borderBox draws a coloured 1px frame around w (used to give the canvas a mode-coloured
// edge and the output a dark frame).
func borderBox(gtx layout.Context, edge color.NRGBA, w layout.Widget) layout.Dimensions {
	macro := op.Record(gtx.Ops)
	dims := w(gtx)
	call := macro.Stop()
	strokeRect(gtx.Ops, image.Rect(0, 0, dims.Size.X, dims.Size.Y), edge)
	call.Add(gtx.Ops)
	return dims
}

// --- in-window file drop ---------------------------------------------------------

// registerWindowDrop registers the whole window as a transfer target and adopts the first
// dropped/pasted path as the target.
//
// IMPORTANT (verified against Gio v0.10.0): Gio does NOT deliver OS *file* drops on
// Windows — there is no WM_DROPFILES / IDropTarget path in its Windows backend, and on
// every platform the only transfer.DataEvent it emits is type "application/text". So this
// handler best-effort accepts a TEXT transfer (e.g. a dropped/pasted path string) and, if
// it resolves to a real file/folder, sets it as the target. On Windows the RELIABLE
// drag-and-drop is the argv path (drop a file onto becky-canvas.exe — see adoptArgv),
// plus the Open button. This handler never blocks and never crashes if nothing arrives.
func (a *App) registerWindowDrop(gtx layout.Context) {
	tag := a.window // a stable per-window tag
	defer clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, tag)

	for {
		ev, ok := gtx.Event(transfer.TargetFilter{Target: tag, Type: "application/text"})
		if !ok {
			break
		}
		de, ok := ev.(transfer.DataEvent)
		if !ok {
			continue
		}
		rc := de.Open()
		data, _ := io.ReadAll(rc)
		_ = rc.Close()
		if path := firstDroppedPath(string(data)); path != "" {
			a.setTarget(path)
		}
	}
}

// firstDroppedPath extracts the first usable filesystem path from dropped transfer data.
// Drops may arrive as one path, a newline-separated list, or file:// URIs; we take the
// first existing path. Returns "" when nothing usable is found.
func firstDroppedPath(data string) string {
	for _, line := range strings.Split(strings.ReplaceAll(data, "\r\n", "\n"), "\n") {
		p := strings.TrimSpace(line)
		p = strings.TrimPrefix(p, "file://")
		p = strings.TrimPrefix(p, "file:")
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
