//go:build gui

// gui_render.go — the fast "edit code -> SEE the canvas" loop.
//
// becky-canvas --render-frame <out.png> renders ONE frame of the startup window to a
// PNG, with NO window shown and no display needed (off-screen GPU via
// gioui.org/gpu/headless). It is the missing iteration tool: an agent edits the Gio
// layout, runs this, and looks at (or becky-vision-audits) the PNG to see the result —
// no human needed to "open the window and tell me what it looks like". It honours
// BECKY_CANVAS_MODE (drum/piano/mixer/audio) and any dropped target path, so you can
// snapshot any panel:
//
//	becky-canvas --render-frame canvas.png
//	BECKY_CANVAS_MODE=drum becky-canvas --render-frame drum.png --size 1280x800
//	becky-canvas --render-frame daw.png path/to/project.json
//
// degrade-never-crash (CLAUDE.md §2): a missing GPU / any panic becomes a plain-language
// error + exit 1, never a crash. The window is never started, so window.Invalidate() is a
// guaranteed no-op (mayInvalidate starts false) and the input source is empty (no events).
package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"gioui.org/app"
	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

const (
	defaultRenderWidth  = 1180 // matches the window's default app.Size in main()
	defaultRenderHeight = 760
	defaultRenderOut    = "canvas-frame.png"
)

// renderFrameCLI runs the one-shot headless render when argv asked for it. It returns
// the process exit code and handled=true when it took over (so main can exit without
// opening a window). Kept here so gui.go's main() stays import-clean.
func renderFrameCLI(args []string) (code int, handled bool) {
	out, ok := renderFrameRequested(args)
	if !ok {
		return 0, false
	}
	w, h := renderFrameSize(args)
	if err := renderFrameToPNG(args, out, w, h); err != nil {
		fmt.Fprintln(os.Stderr, "becky-canvas --render-frame:", err)
		return 1, true
	}
	fmt.Printf("wrote %s (%dx%d)\n", out, w, h)
	return 0, true
}

// renderFrameRequested reports the requested PNG path when argv asks for a one-shot
// headless render (--render-frame [path] or --render-frame=path). When the flag is
// present without a path, it falls back to defaultRenderOut.
func renderFrameRequested(args []string) (out string, ok bool) {
	for i, a := range args {
		switch {
		case a == "--render-frame" || a == "-render-frame":
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				return args[i+1], true
			}
			return defaultRenderOut, true
		case strings.HasPrefix(a, "--render-frame="):
			if v := strings.TrimPrefix(a, "--render-frame="); v != "" {
				return v, true
			}
			return defaultRenderOut, true
		}
	}
	return "", false
}

// renderFrameSize reads --size WxH (e.g. 1280x800); defaults to the window's size.
func renderFrameSize(args []string) (int, int) {
	for i, a := range args {
		var v string
		switch {
		case a == "--size" && i+1 < len(args):
			v = args[i+1]
		case strings.HasPrefix(a, "--size="):
			v = strings.TrimPrefix(a, "--size=")
		}
		if v == "" {
			continue
		}
		if w, h, ok := parseWxH(v); ok {
			return w, h
		}
	}
	return defaultRenderWidth, defaultRenderHeight
}

// parseWxH parses "1280x800" into (1280, 800, true). Bad input degrades to ok=false.
func parseWxH(s string) (int, int, bool) {
	parts := strings.SplitN(strings.ToLower(strings.TrimSpace(s)), "x", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	w, e1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	h, e2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if e1 != nil || e2 != nil || w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

// stripRenderArgs removes the render-only flags so the remaining argv (a dropped target
// path) is adopted normally by adoptArgv.
func stripRenderArgs(args []string) []string {
	keep := make([]string, 0, len(args))
	skipNext := false
	for i, a := range args {
		if skipNext {
			skipNext = false
			continue
		}
		switch {
		case a == "--render-frame" || a == "-render-frame":
			// consume a following non-flag value as the path
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				skipNext = true
			}
		case a == "--size":
			skipNext = true
		case strings.HasPrefix(a, "--render-frame="), strings.HasPrefix(a, "--size="):
			// inline value: nothing to skip
		default:
			keep = append(keep, a)
		}
	}
	return keep
}

// renderFrameToPNG builds the startup App (honouring BECKY_CANVAS_MODE + any argv
// target), lays out ONE frame off-screen, and writes it to out as a PNG. No window is
// shown. Any panic is recovered into an error so a broken layout can never crash the
// render harness.
func renderFrameToPNG(args []string, out string, width, height int) (err error) {
	runtime.LockOSThread() // the GPU context prefers a stable OS thread
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("render-frame panicked (a layout bug to fix): %v", r)
		}
	}()

	a := newApp(new(app.Window)) // window never started -> Invalidate() is a no-op
	a.adoptArgv(stripRenderArgs(args))
	a.applyStartupMode()

	gtx := layout.Context{
		Ops:         new(op.Ops),
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Exact(image.Pt(width, height)),
		Now:         time.Now(),
	}
	a.layoutFrame(gtx)

	win, err := headless.NewWindow(width, height)
	if err != nil {
		return fmt.Errorf("off-screen GPU window (need a desktop GPU/display): %w", err)
	}
	defer win.Release()
	if err := win.Frame(gtx.Ops); err != nil {
		return fmt.Errorf("render frame: %w", err)
	}
	img := image.NewRGBA(image.Rectangle{Max: image.Pt(width, height)})
	if err := win.Screenshot(img); err != nil {
		return fmt.Errorf("read pixels: %w", err)
	}
	f, err := os.Create(out)
	if err != nil {
		return fmt.Errorf("create %s: %w", out, err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		return fmt.Errorf("encode png: %w", err)
	}
	return nil
}
