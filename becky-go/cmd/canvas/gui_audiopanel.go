//go:build gui

// gui_audiopanel.go — the in-window AUDIO / vocal tracks (CANVAS-BLUEPRINT.md panel
// 2d). Renders every audio track (Kind==KindAudio) in a.arr as a stacked row of
// min/max waveform lanes drawn from each clip's Peaks — the headless engine for
// peaks/mixdown lives in internal/audiotrack. Read-only for now (trim/move land in
// a later pass); a session with no audio tracks shows a helpful placeholder.
//
// CONTRACT (kept stable for the spine):
//   - type audioPanel + func newAudioPanel() *audioPanel
//   - func (p *audioPanel) layout(gtx, a *App) layout.Dimensions
package main

import (
	"image"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"becky-go/internal/dawmodel"
)

type audioPanel struct{}

func newAudioPanel() *audioPanel { return &audioPanel{} }

// layout renders one waveform lane per audio track. It degrades to a placeholder
// when the session has no audio tracks (the common case for a MIDI beat).
func (p *audioPanel) layout(gtx layout.Context, a *App) layout.Dimensions {
	if a.arr == nil {
		return panelPlaceholder(gtx, a, "audio tracks — drop a project.json or record a take")
	}
	tracks := audioTracks(a.arr)
	if len(tracks) == 0 {
		return panelPlaceholder(gtx, a, "no audio tracks yet — record a take or import a .wav (MIDI beats show in the drum/piano panels)")
	}

	size := gtx.Constraints.Max
	if size.X <= 0 || size.Y <= 0 {
		return layout.Dimensions{Size: size}
	}

	// Header strip.
	capH := gtx.Dp(unit.Dp(22))
	fillRect(gtx.Ops, image.Rect(0, 0, size.X, capH), colHeaderBg)
	a.drawCanvasCaption(gtx, a.th, plural(len(tracks), "audio track", "audio tracks"))

	// One lane per track below the header.
	labelW := gtx.Dp(unit.Dp(110))
	margin := gtx.Dp(unit.Dp(8))
	areaH := size.Y - capH
	if areaH <= 0 {
		return layout.Dimensions{Size: size}
	}
	gap := gtx.Dp(unit.Dp(6))
	laneH := (areaH - (len(tracks)+1)*gap) / len(tracks)
	if laneH < gtx.Dp(unit.Dp(24)) {
		laneH = gtx.Dp(unit.Dp(24))
	}

	for i, t := range tracks {
		y0 := capH + gap + i*(laneH+gap)
		if y0 >= size.Y {
			break
		}
		y1 := y0 + laneH
		laneCol := colLaneA
		if i%2 == 1 {
			laneCol = colLaneB
		}
		fillRect(gtx.Ops, image.Rect(0, y0, size.X, y1), laneCol)
		fillRect(gtx.Ops, image.Rect(0, y0, labelW, y1), colLaneHeader)
		fillRect(gtx.Ops, image.Rect(0, y0, 4, y1), colAccent) // accent edge
		drawLabelAt(gtx, a.th, t.ID, margin+6, y0+laneH/2-gtx.Dp(unit.Dp(8)))

		// Waveform fills the area to the right of the label.
		waveRect := image.Rect(labelW+margin, y0+3, size.X-margin, y1-3)
		drawTrackWaveform(gtx, t, waveRect)
	}

	return layout.Dimensions{Size: size}
}

// audioTracks returns the KindAudio tracks of an arrangement, in order.
func audioTracks(arr *dawmodel.Arrangement) []dawmodel.Track {
	var out []dawmodel.Track
	for _, t := range arr.Tracks {
		if t.Kind == dawmodel.KindAudio {
			out = append(out, t)
		}
	}
	return out
}

// drawLabelAt renders a dim text label at (x,y) within the current ops, using a
// scoped op.Offset (Gio's caption helper can only draw at the ops origin).
func drawLabelAt(gtx layout.Context, th *material.Theme, txt string, x, y int) {
	if txt == "" {
		return
	}
	defer op.Offset(image.Pt(x, y)).Push(gtx.Ops).Pop()
	lbl := material.Body2(th, txt)
	lbl.Color = colTextDim
	lbl.MaxLines = 1
	lbl.Layout(gtx)
}

// drawTrackWaveform paints a track's clip peaks as a min/max waveform within r.
// Peaks from every clip are concatenated left-to-right; a track with no peaks
// draws a flat centre line so the lane still reads as "audio, empty".
func drawTrackWaveform(gtx layout.Context, t dawmodel.Track, r image.Rectangle) {
	w := r.Dx()
	h := r.Dy()
	if w <= 0 || h <= 0 {
		return
	}
	mid := float32(r.Min.Y) + float32(h)/2
	half := float32(h)/2 - 2
	// Centre line.
	fillRect(gtx.Ops, image.Rect(r.Min.X, int(mid), r.Max.X, int(mid)+1), colGridLine)

	peaks := collectPeaks(t)
	if len(peaks) == 0 {
		return
	}
	colW := float32(w) / float32(len(peaks))
	barW := colW - 0.5
	if barW < 0.5 {
		barW = 0.5
	}
	var path clip.Path
	path.Begin(gtx.Ops)
	for i, pk := range peaks {
		x := float32(r.Min.X) + float32(i)*colW
		top := mid - clampUnit64(pk.Max)*half
		bot := mid - clampUnit64(pk.Min)*half
		if bot-top < 1 {
			bot = top + 1
		}
		path.MoveTo(f32.Pt(x, top))
		path.LineTo(f32.Pt(x+barW, top))
		path.LineTo(f32.Pt(x+barW, bot))
		path.LineTo(f32.Pt(x, bot))
		path.Close()
	}
	paint.FillShape(gtx.Ops, colWave, clip.Outline{Path: path.End()}.Op())
}

// collectPeaks concatenates the peaks of every clip on a track.
func collectPeaks(t dawmodel.Track) []dawmodel.Peak {
	var out []dawmodel.Peak
	for _, c := range t.Clips {
		out = append(out, c.Peaks...)
	}
	return out
}

// clampUnit64 clamps a float64 sample value to [-1,1] and returns it as float32.
func clampUnit64(v float64) float32 {
	if v > 1 {
		v = 1
	}
	if v < -1 {
		v = -1
	}
	return float32(v)
}
