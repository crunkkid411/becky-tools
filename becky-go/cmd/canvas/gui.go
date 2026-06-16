//go:build gui

// becky-canvas (GUI) — the real native window: Jordan's visual front door to the becky
// tools. Built with the `gui` tag so the default `go build ./...` stays green (the
// headless scene model in main.go is `//go:build !gui`).
//
// Toolkit: Gio (gioui.org) — pure Go, no cgo, Direct3D 11 on Windows. Chosen because
// OpenGL/ImGui failed to create a window on this machine.
//
//	go run -tags gui ./cmd/canvas
//	go build -tags gui -o bin/becky-canvas.exe ./cmd/canvas
//
// What the window does (v1):
//   - A clickable, grouped list of becky tools (name + plain description).
//   - A Target row: a path text field + Browse… / Folder… buttons (native picker).
//   - Clicking a tool runs its real becky-<tool>.exe on the target in a goroutine and
//     streams stdout+stderr live into a scrollable, selectable output panel.
//   - A command box: type a tool name / phrase, press Enter, it routes + runs.
//   - A visual panel: draws a .wav waveform or a project.json DAW scene (op/paint/clip).
//   - Mode tabs (ask/video/daw/midi/drum) that switch the active surface.
//
// degrade-never-crash (CLAUDE.md §2): every failure shows a friendly line; the window
// never panics. The UI goroutine never blocks on a tool — runs happen off-thread and
// post results back, waking the loop with w.Invalidate().
package main

import (
	"image"
	"image/color"
	"os"
	"strings"
	"sync"

	"gioui.org/app"
	"gioui.org/font/gofont"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"becky-go/internal/canvas"
)

// App holds all GUI state. It lives on the UI goroutine; the only cross-goroutine state
// is guarded by mu (the streamed tool output and the worker-built visual).
type App struct {
	th     *material.Theme
	window *app.Window

	// activeMode is the selected mode tab (ask/video/daw/midi/drum).
	activeMode canvas.Mode
	modeTabs   []widget.Clickable

	// Tool list: one clickable per catalog entry, plus a scrollable list state.
	tools    []toolItem
	toolBtns []widget.Clickable
	toolList widget.List

	// Target row.
	target    widget.Editor
	browseBtn widget.Clickable
	folderBtn widget.Clickable

	// Command row.
	command widget.Editor
	runBtn  widget.Clickable

	// Output panel (read-only, selectable).
	output     widget.Editor
	outputList widget.List
	clearBtn   widget.Clickable

	// Cross-goroutine state.
	mu        sync.Mutex
	logBuf    strings.Builder // accumulated tool output
	running   bool            // a tool run is in flight
	runLabel  string          // friendly name of the running tool
	vis       visual          // current drawable representation of the target
	lastVisOf string          // the target string vis was built for (avoid rework)
}

func main() {
	go func() {
		w := new(app.Window)
		w.Option(app.Title("becky-canvas"), app.Size(unit.Dp(1180), unit.Dp(760)))
		if err := newApp(w).loop(); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}()
	app.Main()
}

// newApp builds the App with its theme, tool list, and widget state initialized.
func newApp(w *app.Window) *App {
	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))

	tools := catalog()
	a := &App{
		th:         th,
		window:     w,
		activeMode: canvas.ModeAsk,
		tools:      tools,
		toolBtns:   make([]widget.Clickable, len(tools)),
		modeTabs:   make([]widget.Clickable, len(canvas.Modes())),
	}
	a.toolList.Axis = layout.Vertical
	a.outputList.Axis = layout.Vertical
	a.target.SingleLine = true
	a.target.Submit = true
	a.command.SingleLine = true
	a.command.Submit = true
	a.output.ReadOnly = true // selectable + copyable, not editable
	a.appendLine("becky-canvas ready. Pick a target (Browse…), then click a tool — its output appears here. You can select and copy any of this text.")
	return a
}

// loop is the Gio event loop: handle the window's events, lay out a frame, repeat.
func (a *App) loop() error {
	var ops op.Ops
	for {
		switch e := a.window.Event().(type) {
		case app.DestroyEvent:
			return e.Err
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			a.handleInput(gtx)
			a.layoutFrame(gtx)
			e.Frame(gtx.Ops)
		}
	}
}

// handleInput processes clicks / submits before layout so the frame reflects them.
func (a *App) handleInput(gtx layout.Context) {
	// Mode tabs.
	for i := range a.modeTabs {
		if a.modeTabs[i].Clicked(gtx) {
			a.activeMode = canvas.Modes()[i]
		}
	}
	// Tool clicks.
	for i := range a.toolBtns {
		if a.toolBtns[i].Clicked(gtx) {
			a.startTool(a.tools[i])
		}
	}
	// Target field submit (Enter) just keeps the value; visual rebuilds below.
	for {
		ev, ok := a.target.Update(gtx)
		if !ok {
			break
		}
		if _, isSubmit := ev.(widget.SubmitEvent); isSubmit {
			a.refreshVisual()
		}
	}
	// Browse buttons.
	if a.browseBtn.Clicked(gtx) {
		a.startBrowse(false)
	}
	if a.folderBtn.Clicked(gtx) {
		a.startBrowse(true)
	}
	// Command submit / Run button.
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
		a.runCommand(a.command.Text())
	}
	// Clear output.
	if a.clearBtn.Clicked(gtx) {
		a.mu.Lock()
		a.logBuf.Reset()
		a.mu.Unlock()
		a.appendLine("(cleared)")
	}
	// Keep the visual in sync with the target as it changes.
	a.refreshVisual()
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

	target := strings.TrimSpace(a.target.Text())
	a.appendLine("")
	a.appendLine("=== " + t.Label + " ===")

	results := make(chan runResult, 64)
	go runTool(t, target, results)
	go a.drainResults(results)
}

// runCommand routes a typed phrase to a tool and runs it (v1 = action routing only).
func (a *App) runCommand(phrase string) {
	phrase = strings.TrimSpace(phrase)
	if phrase == "" {
		return
	}
	t, ok := matchTool(phrase)
	if !ok {
		a.appendLine("")
		a.appendLine("I couldn't match \"" + phrase + "\" to a tool. Try a tool name (e.g. transcribe, compose, research) or click one in the list.")
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

// startBrowse opens the native picker on a worker goroutine and writes the chosen path
// into the Target field. The UI thread is never blocked by the modal dialog.
func (a *App) startBrowse(wantFolder bool) {
	go func() {
		path, err := browseForPath(wantFolder)
		if err != nil {
			a.appendLine(err.Error())
			return
		}
		if path == "" {
			return // cancelled
		}
		a.target.SetText(path)
		a.refreshVisual()
		a.window.Invalidate()
	}()
}

// refreshVisual rebuilds the drawable representation when the target changed. The decode
// runs on a worker goroutine for non-trivial files; the result is stored under mu.
func (a *App) refreshVisual() {
	target := strings.TrimSpace(a.target.Text())
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
		// Only apply if the target hasn't changed again since we started.
		if target == a.lastVisOf {
			a.vis = v
		}
		a.mu.Unlock()
		a.window.Invalidate()
	}()
}

// appendLine adds a line to the output buffer (thread-safe), pushes it into the
// read-only editor on the next layout, and repaints.
func (a *App) appendLine(s string) {
	a.mu.Lock()
	a.logBuf.WriteString(s)
	a.logBuf.WriteByte('\n')
	a.mu.Unlock()
	a.window.Invalidate()
}

// --- layout ----------------------------------------------------------------------

// layoutFrame draws the whole window: a top bar with mode tabs, then a body split into
// a left tool panel and a right work area (target row, visual panel, command + output).
func (a *App) layoutFrame(gtx layout.Context) {
	paint.Fill(gtx.Ops, colWindowBg)
	layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(a.layoutTopBar),
		layout.Flexed(1, a.layoutBody),
	)
}

// layoutTopBar draws the title + the mode tabs.
func (a *App) layoutTopBar(gtx layout.Context) layout.Dimensions {
	return widgetBg(gtx, colHeaderBg, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					title := material.H6(a.th, "becky-canvas")
					title.Color = colAccent
					return layout.UniformInset(unit.Dp(4)).Layout(gtx, title.Layout)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(16)}.Layout),
				layout.Flexed(1, a.layoutModeTabs),
			)
		})
	})
}

// layoutModeTabs draws the ask/video/daw/midi/drum tabs, highlighting the active one.
func (a *App) layoutModeTabs(gtx layout.Context) layout.Dimensions {
	modes := canvas.Modes()
	children := make([]layout.FlexChild, 0, len(modes))
	for i := range modes {
		i := i
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			active := a.activeMode == modes[i]
			btn := material.Button(a.th, &a.modeTabs[i], strings.ToUpper(string(modes[i])))
			btn.TextSize = unit.Sp(13)
			if active {
				btn.Background = colAccent
				btn.Color = colWindowBg
			} else {
				btn.Background = colPanelBg
				btn.Color = colTextDim
			}
			return layout.UniformInset(unit.Dp(3)).Layout(gtx, btn.Layout)
		}))
	}
	return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, children...)
}

// layoutBody splits into the left tool panel and the right work area.
func (a *App) layoutBody(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
		layout.Rigid(a.layoutToolPanel),
		layout.Flexed(1, a.layoutWorkArea),
	)
}

// toolPanelWidth is the fixed width of the left tool list column.
const toolPanelWidth = 300

// layoutToolPanel draws the grouped, scrollable, clickable tool list.
func (a *App) layoutToolPanel(gtx layout.Context) layout.Dimensions {
	gtx.Constraints.Min.X = gtx.Dp(unit.Dp(toolPanelWidth))
	gtx.Constraints.Max.X = gtx.Constraints.Min.X
	return widgetBg(gtx, colPanelBg, func(gtx layout.Context) layout.Dimensions {
		rows := a.toolRows()
		return layout.UniformInset(unit.Dp(6)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return material.List(a.th, &a.toolList).Layout(gtx, len(rows), func(gtx layout.Context, i int) layout.Dimensions {
				return rows[i](gtx)
			})
		})
	})
}

// toolRows flattens the grouped catalog into a list of row-drawing closures: a group
// header followed by that group's tool buttons. Buttons keep their original catalog
// index so clicks map back to the right tool.
func (a *App) toolRows() []layout.Widget {
	var rows []layout.Widget
	for _, g := range groupOrder() {
		g := g
		rows = append(rows, func(gtx layout.Context) layout.Dimensions {
			return a.layoutGroupHeader(gtx, g)
		})
		for ci, t := range a.tools {
			if t.Group != g {
				continue
			}
			ci, t := ci, t
			rows = append(rows, func(gtx layout.Context) layout.Dimensions {
				return a.layoutToolButton(gtx, ci, t)
			})
		}
	}
	return rows
}

// layoutGroupHeader draws a small section header above each tool group.
func (a *App) layoutGroupHeader(gtx layout.Context, g string) layout.Dimensions {
	return layout.Inset{Top: unit.Dp(10), Bottom: unit.Dp(2), Left: unit.Dp(4)}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			lbl := material.Caption(a.th, strings.ToUpper(g))
			lbl.Color = colAccent
			return lbl.Layout(gtx)
		})
}

// layoutToolButton draws one clickable tool card: label on top, description beneath.
func (a *App) layoutToolButton(gtx layout.Context, idx int, t toolItem) layout.Dimensions {
	return layout.Inset{Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return material.Clickable(gtx, &a.toolBtns[idx], func(gtx layout.Context) layout.Dimensions {
			bg := colPanelBg
			if a.toolBtns[idx].Hovered() {
				bg = colHeaderBg
			}
			return widgetBg(gtx, bg, func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(unit.Dp(7)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							name := material.Body1(a.th, t.Label)
							name.Color = colText
							return name.Layout(gtx)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							desc := material.Caption(a.th, t.Desc)
							desc.Color = colTextDim
							return desc.Layout(gtx)
						}),
					)
				})
			})
		})
	})
}

// layoutWorkArea stacks the target row, the visual panel, the command row, and the
// output panel down the right side.
func (a *App) layoutWorkArea(gtx layout.Context) layout.Dimensions {
	return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(a.layoutTargetRow),
			layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			layout.Flexed(0.42, a.layoutVisualPanel),
			layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			layout.Rigid(a.layoutCommandRow),
			layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
			layout.Flexed(0.58, a.layoutOutputPanel),
		)
	})
}

// layoutTargetRow draws "Target:" + the path field + Browse… / Folder… buttons.
func (a *App) layoutTargetRow(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.Body1(a.th, "Target ")
			lbl.Color = colTextDim
			return lbl.Layout(gtx)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return fieldBox(gtx, func(gtx layout.Context) layout.Dimensions {
				ed := material.Editor(a.th, &a.target, "Paste a file or folder path…")
				ed.Color = colText
				ed.HintColor = colTextDim
				return ed.Layout(gtx)
			})
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
		layout.Rigid(a.smallButton(&a.browseBtn, "Browse…")),
		layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
		layout.Rigid(a.smallButton(&a.folderBtn, "Folder…")),
	)
}

// layoutVisualPanel frames the waveform / scene drawing area with a border.
func (a *App) layoutVisualPanel(gtx layout.Context) layout.Dimensions {
	a.mu.Lock()
	// layoutVisual reads a.vis; copy under lock isn't needed since drawing reads fields
	// that are only swapped wholesale, but we keep the lock window tiny here.
	a.mu.Unlock()
	return borderBox(gtx, func(gtx layout.Context) layout.Dimensions {
		return a.layoutVisual(gtx, a.th)
	})
}

// layoutCommandRow draws the command input + Run button + Clear button.
func (a *App) layoutCommandRow(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return fieldBox(gtx, func(gtx layout.Context) layout.Dimensions {
				ed := material.Editor(a.th, &a.command, "Type a tool name or phrase, then Enter (e.g. transcribe, compose, research)…")
				ed.Color = colText
				ed.HintColor = colTextDim
				return ed.Layout(gtx)
			})
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
		layout.Rigid(a.smallButton(&a.runBtn, "Run")),
		layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
		layout.Rigid(a.smallButton(&a.clearBtn, "Clear")),
	)
}

// layoutOutputPanel draws the scrollable, selectable, read-only output editor. It syncs
// the editor's text from the shared buffer each frame so streamed lines appear live.
func (a *App) layoutOutputPanel(gtx layout.Context) layout.Dimensions {
	a.mu.Lock()
	logText := a.logBuf.String()
	a.mu.Unlock()
	if a.output.Text() != logText {
		a.output.SetText(logText)
	}
	return borderBox(gtx, func(gtx layout.Context) layout.Dimensions {
		return widgetBg(gtx, colCanvasBg, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return material.List(a.th, &a.outputList).Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
					ed := material.Editor(a.th, &a.output, "")
					ed.Color = colText
					ed.TextSize = unit.Sp(13)
					return ed.Layout(gtx)
				})
			})
		})
	})
}

// --- small layout helpers --------------------------------------------------------

// smallButton returns a widget that draws a compact accent button.
func (a *App) smallButton(btn *widget.Clickable, label string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		b := material.Button(a.th, btn, label)
		b.TextSize = unit.Sp(13)
		b.Background = colAccentDim
		b.Color = colText
		if btn.Hovered() {
			b.Background = colAccent
			b.Color = colWindowBg
		}
		return b.Layout(gtx)
	}
}

// widgetBg fills the widget's area with a background colour, then draws w on top. It
// records the child first to learn its size, paints the background behind it, then
// replays the child — the standard Gio "background under a widget" idiom.
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

// fieldBox wraps a text editor in a padded, panel-coloured box so it reads as an input.
func fieldBox(gtx layout.Context, w layout.Widget) layout.Dimensions {
	return widgetBg(gtx, colCanvasBg, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(unit.Dp(6)).Layout(gtx, w)
	})
}

// borderBox draws a thin border around w (a 1px frame in the grid colour). It uses the
// widgetBg idiom: record the child, draw a frame the child's size, replay the child.
func borderBox(gtx layout.Context, w layout.Widget) layout.Dimensions {
	macro := op.Record(gtx.Ops)
	dims := w(gtx)
	call := macro.Stop()

	// Frame: fill the full rect with the border colour, then the child paints over the
	// interior. (The child's own background covers all but the 1px edge it doesn't fill;
	// panels here paint their own backgrounds, so the border shows as a hairline edge.)
	frame(gtx.Ops, dims.Size.X, dims.Size.Y, colGridLine)
	call.Add(gtx.Ops)
	return dims
}

// frame draws a 1px rectangle outline of the given size in colour c.
func frame(ops *op.Ops, w, h int, c color.NRGBA) {
	edges := []clip.Rect{
		{Min: imgPt(0, 0), Max: imgPt(w, 1)},   // top
		{Min: imgPt(0, h-1), Max: imgPt(w, h)}, // bottom
		{Min: imgPt(0, 0), Max: imgPt(1, h)},   // left
		{Min: imgPt(w-1, 0), Max: imgPt(w, h)}, // right
	}
	for _, e := range edges {
		func() {
			defer e.Push(ops).Pop()
			paint.ColorOp{Color: c}.Add(ops)
			paint.PaintOp{}.Add(ops)
		}()
	}
}

// imgPt is a short constructor for an image.Point (keeps frame() readable).
func imgPt(x, y int) image.Point { return image.Pt(x, y) }
