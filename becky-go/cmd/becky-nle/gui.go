//go:build gui

// becky-nle (GUI) — THE WINDOW: Jordan's AI-integrated, Vegas-fast NLE foundation
// (GUI-RULES.md Wave-2). Open a video, see a timeline, SCRUB with a frame-accurate,
// GPU-decoded preview (rendered by the Rust+wgpu becky-video-preview sidecar over the
// NDJSON seam), set in/out marks, and export the marked range to a real MP4.
//
// Toolkit: Gio (gioui.org v0.10.0) — the SAME version cmd/canvas + cmd/drummachine use,
// mirroring their window/loop/theme/click/exec patterns. Pure `-tags gui` build, NO cgo:
// ALL video pixels come from the sidecar (a separate process); the window only displays
// the PNG frames it produces. The seam contract holds: the window READS + EMITS, the
// engine owns state, every slow verb runs OFF the UI thread (GUI-RULES.md §2/§3).
//
//	go run   -tags gui ./cmd/becky-nle [video]
//	go build -tags gui -o bin/becky-nle.exe ./cmd/becky-nle
//
// The window (icon/shape-first, minimal text — Jordan's north star):
//   - TOP BAR: Open / Mark-In / Mark-Out / Export / pop-out GPU window + a readout.
//   - PREVIEW PANE (centre, the star): the current frame, fetched OFF the UI thread.
//   - TIMELINE (bottom): a ruler with hours-aware timecodes, a duration-proportional
//     clip block, a translucent marked-range band, and a playhead. Drag anywhere on it
//     to SCRUB (the playhead follows + a new frame is requested).
//   - ONE quiet AI/command box: plain English -> routeCommand -> an NLE action.
//
// degrade-never-crash (CLAUDE.md §2): a missing sidecar/ffmpeg/video never crashes the
// window — it shows one quiet neon line and stays live. The async bridge means ffmpeg /
// the GPU decode never blocks the frame loop (the becky-clip freeze fix, generalised).
package main

import (
	"context"
	"image"
	"image/color"
	"os"
	"os/exec"
	"strconv"
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

	"becky-go/internal/pathx"
	"becky-go/internal/videopreview"
)

// App holds all GUI state. It lives on the UI goroutine; cross-goroutine state (the
// status line, the current frame image, the in-flight scrub generation) is guarded by mu.
type App struct {
	th     *material.Theme
	window *app.Window
	icons  iconSet

	// project is the LIVE NLE state the whole window renders from (the single source of
	// truth; see nle.go). Every edit goes through its mutators.
	project *Project

	// client is the typed video-preview sidecar client (videopreview). Nil until a
	// successful Open; a nil/missing client degrades to a friendly line.
	client *videopreview.Client
	// ctx/cancel bound the sidecar subprocess to the window lifetime.
	ctx    context.Context
	cancel context.CancelFunc

	// Top-bar + command controls.
	openBtn   widget.Clickable
	markInBtn widget.Clickable
	markOutBt widget.Clickable
	exportBtn widget.Clickable
	windowBtn widget.Clickable
	command   widget.Editor
	runBtn    widget.Clickable

	// timelineTag is the event.Tag for the timeline pointer area (scrub).
	timelineTag bool

	// Cross-goroutine state (guarded by mu).
	mu        sync.Mutex
	status    string        // the one short status line
	frame     paint.ImageOp // the current preview frame, ready to paint (zero = none yet)
	haveFrame bool
	frameMS   int64  // the playhead-ms the current frame is for (avoid redundant fetches)
	scrubGen  uint64 // monotonically increasing; a stale fetch drops its result
	busy      bool   // an Open/Export is running (kept off the UI thread)
}

func main() {
	go func() {
		w := new(app.Window)
		w.Option(app.Title("becky-nle"), app.Size(unit.Dp(1180), unit.Dp(760)))
		a := newApp(w)
		a.adoptArgv(os.Args[1:]) // drag a video onto becky-nle.exe -> open it
		err := a.loop()
		a.shutdown()
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}()
	app.Main()
}

// newApp builds the App with its theme, icons, an empty project, and the sidecar
// lifetime context. The sidecar itself is started lazily on the first Open.
func newApp(w *app.Window) *App {
	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))
	ctx, cancel := context.WithCancel(context.Background())
	a := &App{
		th:      th,
		window:  w,
		icons:   loadIcons(),
		project: NewProject(),
		ctx:     ctx,
		cancel:  cancel,
	}
	a.command.SingleLine = true
	a.command.Submit = true
	a.status = "Open a video (drag onto the window's exe, or the Open button), then scrub the timeline."
	return a
}

// adoptArgv opens the first existing video path passed on the command line (the "drag a
// video onto becky-nle.exe" workflow). Degrade, never crash.
func (a *App) adoptArgv(args []string) {
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg == "" {
			continue
		}
		if _, err := os.Stat(arg); err == nil {
			a.openVideo(arg)
			return
		}
	}
}

// shutdown tears down the sidecar (the window is closing).
func (a *App) shutdown() {
	if a.client != nil {
		a.client.Close()
	}
	if a.cancel != nil {
		a.cancel()
	}
}

// loop is the Gio event loop: handle events, lay out a frame, repeat.
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

// handleInput processes the top-bar buttons + the command box before layout so the frame
// reflects them. Timeline scrub (pointer) is handled during layout, where the timeline's
// local coordinates are in scope.
func (a *App) handleInput(gtx layout.Context) {
	if a.openBtn.Clicked(gtx) {
		a.startOpen()
	}
	if a.markInBtn.Clicked(gtx) {
		a.doMarkIn()
	}
	if a.markOutBt.Clicked(gtx) {
		a.doMarkOut()
	}
	if a.exportBtn.Clicked(gtx) {
		a.startExport()
	}
	if a.windowBtn.Clicked(gtx) {
		a.popOutWindow()
	}

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
		a.command.SetText("")
	}
}

// --- actions ---------------------------------------------------------------------

// startOpen opens the native file picker on a worker goroutine, then opens the chosen
// video. The UI thread is never blocked by the modal dialog.
func (a *App) startOpen() {
	go func() {
		path, err := browseForVideo()
		if err != nil {
			a.setStatus(err.Error())
			return
		}
		if path == "" {
			return // cancelled
		}
		a.openVideo(path)
	}()
}

// openVideo (re)starts the sidecar if needed, opens path, records the Info on the project,
// and requests the first frame. All slow work runs on a goroutine; the window stays live.
func (a *App) openVideo(path string) {
	a.mu.Lock()
	if a.busy {
		a.mu.Unlock()
		a.setStatus("busy… let the current operation finish")
		return
	}
	a.busy = true
	a.mu.Unlock()
	a.setStatus("opening " + pathx.Base(path) + "…")

	go func() {
		defer func() {
			a.mu.Lock()
			a.busy = false
			a.mu.Unlock()
			a.window.Invalidate()
		}()

		if a.client == nil {
			c, err := videopreview.Start(a.ctx, "")
			if err != nil {
				a.setStatus("preview engine unavailable: " + friendlySidecarErr(err))
				return
			}
			a.client = c
		}
		info, err := a.client.Open(a.ctx, path)
		if err != nil {
			a.setStatus("couldn't open that video: " + err.Error())
			return
		}
		a.project.LoadInfo(path, info)
		a.clearFrame()
		a.setStatus(infoLine(path, info))
		// First frame at t=0.
		a.requestFrame(0)
	}()
}

// doMarkIn / doMarkOut set the marks at the current playhead and repaint.
func (a *App) doMarkIn() {
	if !a.project.IsOpen() {
		a.setStatus("open a video first")
		return
	}
	a.project.SetIn(a.project.Play)
	a.setStatus("in-mark " + formatTC(a.project.In) + "   (range " + formatTC(a.project.MarkDur()) + ")")
	a.window.Invalidate()
}

func (a *App) doMarkOut() {
	if !a.project.IsOpen() {
		a.setStatus("open a video first")
		return
	}
	a.project.SetOut(a.project.Play)
	a.setStatus("out-mark " + formatTC(a.project.Out) + "   (range " + formatTC(a.project.MarkDur()) + ")")
	a.window.Invalidate()
}

// startExport renders the marked range to a new MP4 next to the source, OFF the UI thread
// (ffmpeg can take seconds). Status reflects progress + the final path.
func (a *App) startExport() {
	if !a.project.IsOpen() {
		a.setStatus("open a video first")
		return
	}
	a.mu.Lock()
	if a.busy {
		a.mu.Unlock()
		a.setStatus("busy… let the current operation finish")
		return
	}
	a.busy = true
	a.mu.Unlock()
	a.setStatus("exporting the marked range (" + formatTC(a.project.MarkDur()) + ")…")

	// Snapshot the project so the goroutine doesn't race UI edits.
	snap := *a.project
	go func() {
		defer func() {
			a.mu.Lock()
			a.busy = false
			a.mu.Unlock()
			a.window.Invalidate()
		}()
		res, err := exportRange(&snap, "")
		if err != nil {
			a.setStatus("export failed: " + err.Error())
			return
		}
		msg := "✓ exported " + pathx.Base(res.Output) + "  (" + formatTC(res.DurationSec) + ", " + res.Codec + ")"
		if res.Note != "" {
			msg += "  — " + res.Note
		}
		a.setStatus(msg)
	}()
}

// popOutWindow spawns the dedicated on-screen GPU preview window (the sidecar's own
// winit+wgpu window with arrow-key scrubbing). It runs as a separate process so its
// blocking event loop never touches this window. Degrade-never-crash.
func (a *App) popOutWindow() {
	if !a.project.IsOpen() {
		a.setStatus("open a video first")
		return
	}
	exePath, args, err := videopreview.WindowArgs(a.project.Source)
	if err != nil {
		a.setStatus("can't open the GPU window: " + friendlySidecarErr(err))
		return
	}
	cmd := exec.Command(exePath, args...)
	if err := cmd.Start(); err != nil {
		a.setStatus("couldn't launch the GPU window: " + err.Error())
		return
	}
	a.setStatus("opened the GPU preview window (arrow keys scrub, Esc closes)")
}

// runInstruction routes one command-box line to an NLE action (keyword-routed for now).
func (a *App) runInstruction(text string) {
	res := routeCommand(text, a.project)
	switch {
	case res.OpenPicker:
		a.startOpen()
	case res.MarkIn:
		a.doMarkIn()
		return
	case res.MarkOut:
		a.doMarkOut()
		return
	case res.Export:
		a.startExport()
		return
	case res.WantWindow:
		a.popOutWindow()
		return
	case res.Seek:
		a.project.SetPlayhead(res.SeekTo)
		a.requestFrame(res.SeekTo)
		a.window.Invalidate()
	}
	a.setStatus(res.Reply)
}

// --- preview frame fetch (OFF the UI thread) -------------------------------------

// requestFrame asks the sidecar for the frame at timeSec on a goroutine and stores the
// result for the next repaint. It carries a generation token so that, when the user
// scrubs fast, only the LATEST request's result is applied (stale frames are dropped) —
// this keeps scrubbing responsive without ever blocking the frame loop (GUI-RULES.md §2.4).
func (a *App) requestFrame(timeSec float64) {
	if a.client == nil || !a.project.IsOpen() {
		return
	}
	ms := int64(timeSec*1000 + 0.5)

	a.mu.Lock()
	if a.haveFrame && a.frameMS == ms {
		a.mu.Unlock()
		return // already showing this frame
	}
	a.scrubGen++
	gen := a.scrubGen
	a.mu.Unlock()

	go func() {
		png, err := a.client.Frame(a.ctx, timeSec)
		if err != nil {
			a.setStatus("preview: " + err.Error())
			return
		}
		img, derr := loadPNG(png)
		if derr != nil {
			a.setStatus("preview decode: " + derr.Error())
			return
		}
		a.mu.Lock()
		// Drop the result if a newer scrub has superseded it.
		if gen == a.scrubGen {
			a.frame = paint.NewImageOp(img)
			a.haveFrame = true
			a.frameMS = ms
		}
		a.mu.Unlock()
		a.window.Invalidate()
	}()
}

// clearFrame drops any displayed frame (used when a new video opens).
func (a *App) clearFrame() {
	a.mu.Lock()
	a.haveFrame = false
	a.frame = paint.ImageOp{}
	a.frameMS = -1
	a.mu.Unlock()
}

// --- status helper ---------------------------------------------------------------

// setStatus replaces the one status line (thread-safe) and repaints.
func (a *App) setStatus(s string) {
	a.mu.Lock()
	a.status = s
	a.mu.Unlock()
	a.window.Invalidate()
}

// infoLine builds the "opened" status line from probe metadata.
func infoLine(path string, info videopreview.Info) string {
	return pathx.Base(path) + "  •  " +
		formatTCShort(info.DurationSec) + "  •  " +
		dimensions(info) + "  •  " + fpsStr(info.FPS) + " fps"
}

func dimensions(info videopreview.Info) string {
	if info.Width <= 0 || info.Height <= 0 {
		return "?x?"
	}
	return strconv.Itoa(info.Width) + "x" + strconv.Itoa(info.Height)
}

func fpsStr(f float64) string {
	if f <= 0 {
		return "?"
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// --- top bar + chrome layout (preview + timeline live in their own files) --------

// layoutFrame fills the window and stacks: top bar, the big preview pane, the timeline,
// then the one command box + status line.
func (a *App) layoutFrame(gtx layout.Context) {
	paint.Fill(gtx.Ops, colWindowBg)
	layout.UniformInset(unit.Dp(10)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(a.layoutTopBar),
			layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			layout.Flexed(1, a.layoutPreview),
			layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			layout.Rigid(a.layoutTimeline),
			layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			layout.Rigid(a.layoutCommandBox),
			layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
			layout.Rigid(a.layoutStatus),
		)
	})
}

// layoutTopBar draws the action buttons (Open / Mark-In / Mark-Out / Export / pop-out)
// and a readout of the open clip + marks.
func (a *App) layoutTopBar(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.iconBtn(gtx, &a.openBtn, a.icons.open, "open", colElecBlue)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.iconBtn(gtx, &a.markInBtn, a.icons.markIn, "in", colNeonGreen)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.iconBtn(gtx, &a.markOutBt, a.icons.markOut, "out", colNeonGreen)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.iconBtn(gtx, &a.exportBtn, a.icons.export, "export", colNeonPink)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.iconBtn(gtx, &a.windowBtn, a.icons.window, "GPU window", colYellow)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Dimensions{Size: gtx.Constraints.Min}
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.caption(gtx, a.readout())
		}),
	)
}

// readout is the short "playhead / in→out / duration" line beside the top bar.
func (a *App) readout() string {
	if !a.project.IsOpen() {
		return "no video"
	}
	p := a.project
	return formatTC(p.Play) + "   in " + formatTCShort(p.In) + " -> out " + formatTCShort(p.Out) +
		"   range " + formatTC(p.MarkDur())
}

// layoutCommandBox draws the SINGLE, quiet command line + a run icon (one box).
func (a *App) layoutCommandBox(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return fieldBox(gtx, func(gtx layout.Context) layout.Dimensions {
				ed := material.Editor(a.th, &a.command,
					"tell becky… (e.g. mark in, mark out, export, go to 1:23, open)")
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

// layoutStatus draws the one short status line.
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

// iconBtn draws a compact labelled icon button: a neon-bordered box with the icon + a
// small caption beneath. Hover brightens it. A nil icon falls back to a coloured square.
func (a *App) iconBtn(gtx layout.Context, btn *widget.Clickable, ic *widget.Icon, label string, col color.NRGBA) layout.Dimensions {
	return btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		bg := colCanvasBg
		fg := col
		if btn.Hovered() {
			bg = colHeaderBg
			fg = colNeonGreen
		}
		return widgetBg(gtx, bg, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(7)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						isz := gtx.Dp(unit.Dp(18))
						gtx.Constraints = layout.Exact(image.Pt(isz, isz))
						if ic != nil {
							return ic.Layout(gtx, fg)
						}
						fillRRect(gtx.Ops, image.Rect(0, 0, isz, isz), 3, fg)
						return layout.Dimensions{Size: image.Pt(isz, isz)}
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(5)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						lbl := material.Body2(a.th, label)
						lbl.Color = fg
						lbl.TextSize = unit.Sp(12)
						return lbl.Layout(gtx)
					}),
				)
			})
		})
	})
}
