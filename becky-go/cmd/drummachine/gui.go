//go:build gui

// becky-drummachine (GUI) — THE WINDOW: a real, native 16-pad drum machine
// (a Maschine-2 clone) that Jordan opens, sees 16 pads, clicks them, hears them,
// and where the AI works the controls for him.
//
// Toolkit: Gio (gioui.org v0.10.0) — the SAME version cmd/canvas uses, mirroring its
// proven window creation, event loop, theme, layout, click handling, and
// exec-the-engine-for-sound patterns. Pure `-tags gui` build, NO cgo: sound lives in
// the sibling becky-daw-engine (built `-tags audio`), exec'd like cmd/canvas does.
//
//	go run   -tags gui ./cmd/drummachine
//	go build -tags gui -o bin/becky-drummachine.exe ./cmd/drummachine
//
// The window (icon/shape-first, minimal text — Jordan's design north star):
//   - A 4x4 grid of 16 PADS (Maschine layout), each labelled, that light up when
//     selected/triggered. Click a pad to select it + audition it (exec --play-pad).
//   - A STEP SEQUENCER row for the selected pad's lane: click a cell to toggle a hit.
//   - A TRANSPORT bar: ▶ Play / ■ Stop + tempo + swing readout.
//   - OPEN / SAVE buttons (load/save a machine.json).
//   - ONE quiet AI command box at the bottom: plain English → machinectl.Parse +
//     Apply → swap in the new machine → RE-RENDER (he watches the controls change).
//
// State = a *drummachine.Machine (the live UI state). Every edit replaces it with the
// returned NEW machine and the UI re-renders from it (immutable model, the becky way).
//
// degrade-never-crash (CLAUDE.md §2): a missing engine/kit/bad json never crashes the
// window — it logs one quiet neon line and keeps working.
package main

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"gioui.org/app"
	"gioui.org/font/gofont"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"becky-go/internal/drummachine"
	"becky-go/internal/machinectl"
	"becky-go/internal/samplelib"
)

// App holds all GUI state. It lives on the UI goroutine; cross-goroutine state (the
// status line + the live play process) is guarded by mu.
type App struct {
	th     *material.Theme
	window *app.Window
	icons  iconSet

	// machine is the LIVE UI state — the single source of truth the whole window
	// renders from. Every edit (pad click, step toggle, AI instruction) replaces it
	// with a NEW machine (immutable model).
	machine *drummachine.Machine

	// parser is the words→edits translator (machinectl). It is the deterministic
	// parser unless a local model binary+weights are present (PickParser).
	parser machinectl.Parser

	// selected is the pad whose step-lane the sequencer row edits (0..15).
	selected int

	// 16 pad buttons + the step-sequencer cell buttons for the selected lane.
	pads     [drummachine.PadCount]widget.Clickable
	stepBtns []widget.Clickable

	// Transport + file controls.
	playBtn widget.Clickable
	stopBtn widget.Clickable
	openBtn widget.Clickable
	saveBtn widget.Clickable

	// The single AI command box.
	command widget.Editor
	runBtn  widget.Clickable

	// Cross-goroutine state.
	mu       sync.Mutex
	status   string      // the one short status line (AI summaries / errors)
	playing  bool        // a --play-machine run is live
	playProc *os.Process // the live engine process so ■ Stop can kill it
	curPath  string      // the machine.json path (set by Open/Save), "" if unsaved

	// kit / sample browser — wired in gui_kit.go.
	kitFolderBtn    widget.Clickable
	kitSFZBtn       widget.Clickable
	browserToggle   widget.Clickable
	browserScanBtn  widget.Clickable
	browserSearch   widget.Editor
	browserList     widget.List
	browserShowing  bool               // whether the side panel is visible
	browserLoading  bool               // scan goroutine is running (guarded by mu)
	browserSamples  []samplelib.Sample // full index from last scan (guarded by mu)
	browserFiltered []samplelib.Sample // filtered view (guarded by mu)
	browserBtns     []widget.Clickable // one per browserFiltered entry
}

func main() {
	go func() {
		w := new(app.Window)
		w.Option(app.Title("becky-drummachine"), app.Size(unit.Dp(1040), unit.Dp(720)))
		a := newApp(w)
		a.adoptArgv(os.Args[1:]) // drag a machine.json onto the .exe -> load it
		if err := a.loop(); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}()
	app.Main()
}

// newApp builds the App with its theme, icons, a default machine, and the parser.
func newApp(w *app.Window) *App {
	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))
	a := &App{
		th:      th,
		window:  w,
		icons:   loadIcons(),
		machine: drummachine.NewMachine(),
		parser:  machinectl.PickParser(),
	}
	a.command.SingleLine = true
	a.command.Submit = true
	a.browserSearch.SingleLine = true
	a.syncStepButtons()
	a.status = "16 pads. Click a pad to hear it; click steps to build a beat; or just tell becky what you want."
	return a
}

// adoptArgv loads the first existing machine.json path passed on the command line
// (the "drag a file onto becky-drummachine.exe" workflow). Degrade, never crash.
func (a *App) adoptArgv(args []string) {
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg == "" {
			continue
		}
		if _, err := os.Stat(arg); err == nil {
			a.loadMachineFile(arg)
			return
		}
	}
}

// loop is the Gio event loop: handle events, lay out a frame, repeat. Mirrors
// cmd/canvas/gui.go's loop exactly.
func (a *App) loop() error {
	var ops op.Ops
	for {
		switch ev := a.window.Event().(type) {
		case app.DestroyEvent:
			return ev.Err
		case app.FrameEvent:
			gtx := app.NewContext(&ops, ev)
			a.handleInput(gtx)
			a.layoutFrame(gtx)
			ev.Frame(gtx.Ops)
		}
	}
}

// handleInput processes all clicks + the AI box before layout so the frame reflects
// them. Pad/step clicks are handled here against the current machine, then the model
// is re-rendered from the (possibly new) machine.
func (a *App) handleInput(gtx layout.Context) {
	// Pad clicks: select + audition.
	for i := range a.pads {
		if a.pads[i].Clicked(gtx) {
			a.selectPad(i)
		}
	}

	// Step-sequencer cells for the selected lane: toggle a hit.
	for i := range a.stepBtns {
		if a.stepBtns[i].Clicked(gtx) {
			a.toggleStep(i)
		}
	}

	// Transport + file controls.
	if a.playBtn.Clicked(gtx) {
		a.startPlay()
	}
	if a.stopBtn.Clicked(gtx) {
		a.stopPlay()
	}
	if a.openBtn.Clicked(gtx) {
		a.startOpen()
	}
	if a.saveBtn.Clicked(gtx) {
		a.startSave()
	}

	// Kit load + sample browser controls.
	a.handleKitInput(gtx)

	// The AI command box: Enter or the run icon submits the instruction.
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
		a.runInstruction(a.command.Text())
	}
}

// --- layout ----------------------------------------------------------------------

// layoutFrame fills the window black and stacks: a top bar (transport + file), the
// big pad grid, the selected-pad step sequencer, then the AI box + status line.
// When the sample browser is open it splits the area horizontally: main content on
// the left, the browser panel on the right.
func (a *App) layoutFrame(gtx layout.Context) {
	paint.Fill(gtx.Ops, colWindowBg)
	layout.UniformInset(unit.Dp(10)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		main := func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(a.layoutTopBar),
				layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
				layout.Flexed(1, a.layoutPads),
				layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
				layout.Rigid(a.layoutSequencer),
				layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
				layout.Rigid(a.layoutAIBox),
				layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
				layout.Rigid(a.layoutStatus),
			)
		}
		if !a.browserShowing {
			return main(gtx)
		}
		// Show browser panel to the right.
		return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
			layout.Flexed(1, main),
			layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
			layout.Rigid(a.layoutBrowserPanel),
		)
	})
}

// layoutTopBar draws the transport (▶ / ■), the tempo + swing readout, kit load
// buttons, and the Open / Save buttons in one row.
func (a *App) layoutTopBar(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.iconBtn(gtx, &a.playBtn, a.icons.play, "▶", colNeonGreen)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.iconBtn(gtx, &a.stopBtn, a.icons.stop, "■", colCrimson)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(16)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.caption(gtx, a.transportReadout())
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Dimensions{Size: gtx.Constraints.Min}
		}),
		// Kit load controls (Load Folder | Load SFZ | Browse samples).
		layout.Rigid(a.layoutKitButtons),
		layout.Rigid(layout.Spacer{Width: unit.Dp(16)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.iconBtn(gtx, &a.openBtn, a.icons.folder, "open", colElecBlue)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.iconBtn(gtx, &a.saveBtn, a.icons.save, "save", colElecBlue)
		}),
	)
}

// layoutAIBox draws the SINGLE, quiet AI command line + a run icon. One box, the
// way Jordan wants it.
func (a *App) layoutAIBox(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return fieldBox(gtx, func(gtx layout.Context) layout.Dimensions {
				ed := material.Editor(a.th, &a.command,
					"tell becky… (e.g. make it half-time, load my 808 kit, put a clap on pad 5, set tempo 140, play)")
				ed.Color = colText
				ed.HintColor = colTextDim
				ed.TextSize = unit.Sp(13)
				return ed.Layout(gtx)
			})
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.iconBtn(gtx, &a.runBtn, a.icons.run, "go", colNeonGreen)
		}),
	)
}

// layoutStatus draws the one short status line (AI summaries + errors).
func (a *App) layoutStatus(gtx layout.Context) layout.Dimensions {
	a.mu.Lock()
	s := a.status
	a.mu.Unlock()
	if strings.TrimSpace(s) == "" {
		return layout.Dimensions{}
	}
	lbl := material.Body2(a.th, s)
	lbl.Color = colTextDim
	lbl.TextSize = unit.Sp(12)
	return lbl.Layout(gtx)
}

// transportReadout is the short tempo + swing + selected-pad readout beside the
// transport buttons.
func (a *App) transportReadout() string {
	a.mu.Lock()
	playing := a.playing
	a.mu.Unlock()
	m := a.machine
	swing := machinectl.SummarizeMachine(m).Swing
	swingPct := int((swing - 0.5) / 0.25 * 100)
	prefix := ""
	if playing {
		prefix = "▶ "
	}
	return prefix + fmt.Sprintf("%g BPM   swing %d%%   pad: %s", m.Tempo, swingPct, a.padLabel(a.selected))
}

// --- status helper ---------------------------------------------------------------

// setStatus replaces the one status line (thread-safe) and repaints.
func (a *App) setStatus(s string) {
	a.mu.Lock()
	a.status = s
	a.mu.Unlock()
	a.window.Invalidate()
}
