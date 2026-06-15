//go:build gui

// becky-canvas (GUI) — the real native window: Jordan's visual front door to the
// becky tools. Built with the `gui` tag so the default `go build ./...` stays green.
//
// Uses Gio (gioui.org): pure Go, no cgo, Direct3D 11 on Windows (with a software
// fallback) — chosen over an OpenGL toolkit because OpenGL failed to create a
// window in headless/non-interactive sessions.
//
//	go run -tags gui ./cmd/canvas
//	go build -tags gui -o bin/becky-canvas.exe ./cmd/canvas
//
// Minimal proving skeleton; fleshed out into the tool launcher + waveform canvas.
package main

import (
	"os"

	"gioui.org/app"
	"gioui.org/font/gofont"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/text"
	"gioui.org/widget/material"
)

func main() {
	go func() {
		w := new(app.Window)
		w.Option(app.Title("becky-canvas"))
		if err := loop(w); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}()
	app.Main()
}

func loop(w *app.Window) error {
	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))
	var ops op.Ops
	for {
		switch e := w.Event().(type) {
		case app.DestroyEvent:
			return e.Err
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			layout.Center.Layout(gtx,
				material.H4(th, "becky-canvas — window is alive").Layout)
			e.Frame(gtx.Ops)
		}
	}
}
