//go:build gui

// gui_preview.go — THE PREVIEW PANE (the star of the window). It displays the current
// frame PNG that the becky-video-preview sidecar rendered on the GPU, scaled to fit the
// pane while preserving aspect ratio. The PNG is fetched + decoded OFF the UI thread (in
// gui.go requestFrame); this file only draws the already-decoded paint.ImageOp, so the
// frame loop never blocks on a decode (GUI-RULES.md §2.4).
//
// When no frame is available yet (nothing open, or the sidecar is missing) it shows a
// branded empty state with a one-line hint — colour and shape over text, the becky way.
package main

import (
	"image"
	"image/png"
	"os"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

// loadPNG reads a PNG file (the frame the sidecar wrote) and decodes it to an image.Image
// for paint.NewImageOp. A read/decode failure returns the error (the caller shows a quiet
// line); it never panics. Lives here (gui build) because it is only used by the window.
func loadPNG(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return png.Decode(f)
}

// layoutPreview draws the preview pane: a dark, neon-framed surface that shows the current
// frame letterboxed to fit, or an empty-state hint when there is no frame.
func (a *App) layoutPreview(gtx layout.Context) layout.Dimensions {
	return borderBox(gtx, colGridLine, func(gtx layout.Context) layout.Dimensions {
		size := gtx.Constraints.Max
		// Fill the pane (letterbox bars).
		fillRect(gtx.Ops, image.Rect(0, 0, size.X, size.Y), colCanvasBg)

		a.mu.Lock()
		have := a.haveFrame
		imgOp := a.frame
		a.mu.Unlock()

		if !have {
			a.drawPreviewEmpty(gtx, size)
			return layout.Dimensions{Size: size}
		}

		a.drawFrameFitted(gtx, imgOp, size)
		return layout.Dimensions{Size: size}
	})
}

// drawFrameFitted paints imgOp scaled to fit size while preserving aspect ratio, centred
// (letterboxed). Gio's paint.ImageOp draws at the image's native pixel size; we apply a
// uniform scale (about the origin) + an offset transform so it fills the pane without
// distortion. The image is clipped to its own rect so a fractional scale leaves no fringe.
func (a *App) drawFrameFitted(gtx layout.Context, imgOp paint.ImageOp, size image.Point) {
	src := imgOp.Size()
	if src.X <= 0 || src.Y <= 0 || size.X <= 0 || size.Y <= 0 {
		return
	}
	// Uniform scale to fit (the smaller of the two ratios), so the whole frame is visible.
	sx := float32(size.X) / float32(src.X)
	sy := float32(size.Y) / float32(src.Y)
	scale := sx
	if sy < sx {
		scale = sy
	}
	dstW := int(float32(src.X) * scale)
	dstH := int(float32(src.Y) * scale)
	offX := (size.X - dstW) / 2
	offY := (size.Y - dstH) / 2

	// Scale about the origin, then translate to the centred offset.
	aff := f32.Affine2D{}.
		Scale(f32.Pt(0, 0), f32.Pt(scale, scale)).
		Offset(f32.Pt(float32(offX), float32(offY)))
	defer op.Affine(aff).Push(gtx.Ops).Pop()
	defer clip.Rect{Max: src}.Push(gtx.Ops).Pop()
	imgOp.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
}

// drawPreviewEmpty paints the branded empty state: a faint neon inset frame + one line of
// dim text, centred.
func (a *App) drawPreviewEmpty(gtx layout.Context, size image.Point) {
	inset := 24
	if size.X > 2*inset && size.Y > 2*inset {
		strokeRect(gtx.Ops, image.Rect(inset, inset, size.X-inset, size.Y-inset), colGridLine)
	}
	layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		lbl := material.Body1(a.th, "open a video to preview it here")
		lbl.Color = colTextDim
		lbl.TextSize = unit.Sp(14)
		return lbl.Layout(gtx)
	})
}
