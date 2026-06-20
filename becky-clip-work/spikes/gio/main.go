// Spike A — Gio (pure Go, native Direct3D 11 on Windows).
//
// Goal: prove a standard-widget editor layout (list + button + a "video area"
// rectangle) COMPILES and OPENS on this PC, using the same toolkit + version the
// becky-canvas GUI already uses (gioui.org v0.10.0, no cgo for the window).
//
// Build/run (canvas convention — the `gui` tag keeps a default build trivial):
//
//	go build -tags gui -o becky-clip-gio-spike.exe .
//	./becky-clip-gio-spike.exe
//
// It auto-closes after ~2.5s (SCREENSHOT_MS env overrides) so a screenshot can be
// taken unattended, then exits 0 — proving the window really opened and rendered.
package main

import (
	"image"
	"image/color"
	"os"
	"strconv"
	"time"

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
)

// transcript lines stand in for becky search-result quotes (the left list).
var transcript = []string{
	"00:01  - and then he walked toward the door",
	"00:07  - I never said that to anyone",
	"00:12  - the package was on the table",
	"00:19  - she handed me the keys at noon",
	"00:24  - we left before the alarm went off",
	"00:31  - that's not what happened, officer",
	"00:38  - the car was parked across the street",
	"00:45  - he called me twice that morning",
}

type ui struct {
	th       *material.Theme
	list     widget.List
	rows     []widget.Clickable
	addClip  widget.Clickable
	selected int
	clips    int // count of clips appended to the "timeline"
}

func main() {
	go func() {
		w := new(app.Window)
		w.Option(app.Title("becky-clip - Gio spike"), app.Size(unit.Dp(1100), unit.Dp(640)))
		if err := loop(w); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}()

	// Auto-close timer so the run is unattended-screenshot friendly.
	go func() {
		ms := 2500
		if v := os.Getenv("SCREENSHOT_MS"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				ms = n
			}
		}
		time.Sleep(time.Duration(ms) * time.Millisecond)
		os.Exit(0)
	}()

	app.Main()
}

func loop(w *app.Window) error {
	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))
	u := &ui{th: th, selected: 0, rows: make([]widget.Clickable, len(transcript))}
	u.list.Axis = layout.Vertical

	var ops op.Ops
	for {
		switch e := w.Event().(type) {
		case app.DestroyEvent:
			return e.Err
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			u.update(gtx)
			u.layout(gtx)
			e.Frame(gtx.Ops)
		}
	}
}

func (u *ui) update(gtx layout.Context) {
	for i := range u.rows {
		if u.rows[i].Clicked(gtx) {
			u.selected = i // click a quote -> "seek preview"
		}
	}
	if u.addClip.Clicked(gtx) {
		u.clips++ // double-click stand-in -> append a clip to the timeline
	}
}

var (
	bg       = color.NRGBA{R: 0x0d, G: 0x0d, B: 0x10, A: 0xff}
	panel    = color.NRGBA{R: 0x16, G: 0x18, B: 0x1d, A: 0xff}
	neon     = color.NRGBA{R: 0x39, G: 0xff, B: 0x14, A: 0xff}
	dim      = color.NRGBA{R: 0x9a, G: 0xa0, B: 0xa6, A: 0xff}
	textCol  = color.NRGBA{R: 0xe8, G: 0xea, B: 0xec, A: 0xff}
	clipFill = color.NRGBA{R: 0x1e, G: 0x4a, B: 0x2a, A: 0xff}
)

func (u *ui) layout(gtx layout.Context) {
	paint.Fill(gtx.Ops, bg)
	layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
		// Left: transcript / search-results list.
		layout.Flexed(0.34, u.layoutTranscript),
		layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
		// Right: preview rect on top, timeline strip beneath.
		layout.Flexed(0.66, u.layoutRight),
	)
}

func (u *ui) layoutTranscript(gtx layout.Context) layout.Dimensions {
	return fill(gtx, panel, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(unit.Dp(10)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					h := material.H6(u.th, "Transcript / search")
					h.Color = neon
					return h.Layout(gtx)
				}),
				layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return material.List(u.th, &u.list).Layout(gtx, len(transcript), func(gtx layout.Context, i int) layout.Dimensions {
						return u.rows[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							rowBg := panel
							if i == u.selected {
								rowBg = color.NRGBA{R: 0x22, G: 0x33, B: 0x22, A: 0xff}
							}
							return fill(gtx, rowBg, func(gtx layout.Context) layout.Dimensions {
								return layout.UniformInset(unit.Dp(7)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									lbl := material.Body2(u.th, transcript[i])
									lbl.Color = textCol
									if i == u.selected {
										lbl.Color = neon
									}
									return lbl.Layout(gtx)
								})
							})
						})
					})
				}),
			)
		})
	})
}

func (u *ui) layoutRight(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		// "Video preview" area - a black rect with a label (mpv would render here).
		layout.Flexed(0.7, func(gtx layout.Context) layout.Dimensions {
			return fill(gtx, color.NRGBA{A: 0xff}, func(gtx layout.Context) layout.Dimensions {
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							l := material.H6(u.th, "[ VIDEO PREVIEW AREA ]")
							l.Color = dim
							return l.Layout(gtx)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							l := material.Caption(u.th, "mpv would composite a frame texture here - selected: "+transcript[u.selected])
							l.Color = dim
							return l.Layout(gtx)
						}),
					)
				})
			})
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
		// Controls + timeline strip.
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					b := material.Button(u.th, &u.addClip, "+ append clip")
					b.Background = neon
					b.Color = color.NRGBA{A: 0xff}
					return b.Layout(gtx)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := material.Body2(u.th, "timeline clips: "+strconv.Itoa(u.clips))
					l.Color = dim
					return l.Layout(gtx)
				}),
			)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
		// Timeline strip of clip blocks.
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return fill(gtx, panel, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.Y = gtx.Dp(unit.Dp(64))
				return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					n := u.clips
					if n == 0 {
						n = 3 // show a few example clips so the strip reads as a timeline
					}
					children := make([]layout.FlexChild, 0, n*2)
					for i := 0; i < n; i++ {
						children = append(children, layout.Rigid(clipBlock(i)))
						children = append(children, layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout))
					}
					return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, children...)
				})
			})
		}),
	)
}

func clipBlock(i int) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		w := gtx.Dp(unit.Dp(110))
		h := gtx.Dp(unit.Dp(40))
		defer clip.RRect{Rect: image.Rect(0, 0, w, h), SE: 4, SW: 4, NW: 4, NE: 4}.Push(gtx.Ops).Pop()
		paint.ColorOp{Color: clipFill}.Add(gtx.Ops)
		paint.PaintOp{}.Add(gtx.Ops)
		return layout.Dimensions{Size: image.Pt(w, h)}
	}
}

// fill paints bg behind a widget across its full area.
func fill(gtx layout.Context, bgc color.NRGBA, w layout.Widget) layout.Dimensions {
	macro := op.Record(gtx.Ops)
	dims := w(gtx)
	call := macro.Stop()
	defer clip.Rect{Max: dims.Size}.Push(gtx.Ops).Pop()
	paint.ColorOp{Color: bgc}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	call.Add(gtx.Ops)
	return dims
}
