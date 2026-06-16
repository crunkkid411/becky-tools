//go:build gui

// gui_waveform.go — the VISUAL-FIRST canvas surface (CLAUDE.md HARD REQUIREMENT). The
// big central surface shows ONE of:
//   - the drum step grid (drum mode) — clickable squares;
//   - a piano-roll placeholder lane (midi mode);
//   - a .wav waveform (target is a .wav, decoded via internal/dsp);
//   - a becky-compose project.json scene (DAW track lanes + clips);
//   - else a friendly "drop a file" hint.
//
// Draw-mode pen strokes are painted ON TOP of whatever's shown, so Jordan can mark up a
// waveform or a beat. All decode/parse failures degrade to the hint — never a panic.
package main

import (
	"image"
	"image/color"
	"os"
	"strings"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget/material"

	"becky-go/internal/canvas"
	"becky-go/internal/dsp"
	"becky-go/internal/pathx"
)

// visualKind classifies what the target can be drawn as.
type visualKind int

const (
	visualNone  visualKind = iota // nothing usable -> hint panel
	visualWave                    // a WAV waveform
	visualScene                   // a becky-compose project.json DAW scene
)

// visual is the decoded/parsed target ready to draw, computed off the UI thread when the
// target changes. It holds the cheap-to-draw representation only (peaks / scene).
type visual struct {
	kind  visualKind
	peaks []canvas.Peak // waveform overview (visualWave)
	dur   float64       // audio duration in seconds (visualWave)
	scene *canvas.Scene // DAW scene (visualScene)
	note  string        // friendly status / error message
}

// wavePeakCount is the fixed number of min/max peak pairs we reduce any WAV to for the
// overview — fast regardless of length, deterministic for the same input.
const wavePeakCount = 1600

// buildVisual inspects target and produces a drawable representation. Filesystem read
// only — safe from a worker goroutine. Every failure returns a visualNone + a plain note.
func buildVisual(target string) visual {
	target = strings.TrimSpace(target)
	if target == "" {
		return visual{kind: visualNone, note: "Drop a .wav for its waveform, a project.json for song lanes, or pick a mode on the left."}
	}
	lower := strings.ToLower(target)
	switch {
	case strings.HasSuffix(lower, ".wav"):
		return waveVisual(target)
	case strings.HasSuffix(lower, ".json"):
		return sceneVisual(target)
	default:
		return visual{kind: visualNone, note: "Loaded " + pathx.Base(target) + ". Pick a tool below, or a mode on the left."}
	}
}

// waveVisual decodes a WAV and reduces it to a fixed-size peak overview.
func waveVisual(target string) visual {
	data, err := os.ReadFile(target)
	if err != nil {
		return visual{kind: visualNone, note: "Couldn't open that audio file: " + err.Error()}
	}
	audio, err := dsp.DecodeWAV(data)
	if err != nil {
		return visual{kind: visualNone, note: "Couldn't read that as audio: " + err.Error()}
	}
	return visual{
		kind:  visualWave,
		peaks: peaksFromSamples(audio.Samples, wavePeakCount),
		dur:   audio.DurationSec(),
		note:  pathx.Base(target),
	}
}

// sceneVisual loads a becky-compose project.json into a DAW scene to draw as lanes.
func sceneVisual(target string) visual {
	scene, err := canvas.Load(target)
	if err != nil {
		return visual{kind: visualScene, scene: &scene, note: "Opened an empty song view (" + err.Error() + ")"}
	}
	return visual{kind: visualScene, scene: &scene, note: scene.Title}
}

// peaksFromSamples reduces mono samples to n min/max peak pairs (the standard waveform
// overview primitive). Deterministic for the same input.
func peaksFromSamples(samples []float64, n int) []canvas.Peak {
	if n <= 0 {
		return nil
	}
	out := make([]canvas.Peak, n)
	if len(samples) == 0 {
		return out
	}
	per := float64(len(samples)) / float64(n)
	for i := 0; i < n; i++ {
		start := int(float64(i) * per)
		end := int(float64(i+1) * per)
		if end > len(samples) {
			end = len(samples)
		}
		if start >= end {
			start = end - 1
			if start < 0 {
				start = 0
			}
		}
		mn, mx := samples[start], samples[start]
		for _, s := range samples[start:end] {
			if s < mn {
				mn = s
			}
			if s > mx {
				mx = s
			}
		}
		out[i] = canvas.Peak{Min: float32(mn), Max: float32(mx)}
	}
	return out
}

// layoutVisual draws the current canvas surface. It fills the dark canvas background,
// then draws the surface for the active mode (drum grid / piano roll / waveform / scene
// / hint), and finally overlays any pen strokes from draw mode.
func (a *App) layoutVisual(gtx layout.Context, th *material.Theme) layout.Dimensions {
	size := gtx.Constraints.Max
	fillRect(gtx.Ops, image.Rect(0, 0, size.X, size.Y), colCanvasBg)

	switch a.activeMode {
	case canvas.ModeDrum:
		a.drawDrumGrid(gtx)
		a.drawCanvasCaption(gtx, th, "drum machine  •  click the squares")
	case canvas.ModeMIDI:
		a.drawPianoRoll(gtx)
		a.drawCanvasCaption(gtx, th, "piano roll")
	default:
		a.drawTargetSurface(gtx, th)
	}

	// Pen strokes ride on top of everything in draw mode.
	a.drawStrokes(gtx)
	if a.drawMode {
		a.drawCanvasCaption(gtx, th, "draw  •  press and drag to mark up the canvas")
	}
	return layout.Dimensions{Size: size}
}

// drawTargetSurface draws the waveform / scene / hint for the current target (used in
// ask/video/daw modes — the surfaces that come from a loaded file).
func (a *App) drawTargetSurface(gtx layout.Context, th *material.Theme) {
	switch a.vis.kind {
	case visualWave:
		drawWaveform(gtx, a.vis.peaks)
		a.drawCanvasCaption(gtx, th, captionForWave(a.vis))
	case visualScene:
		drawScene(gtx, a.vis.scene)
		a.drawCanvasCaption(gtx, th, captionForScene(a.vis))
	default:
		a.drawCanvasHint(gtx, th, a.vis.note)
	}
}

// captionForWave / captionForScene build the small status line drawn over the canvas.
func captionForWave(v visual) string {
	if v.dur > 0 {
		return v.note + "  •  waveform  •  " + secs(v.dur)
	}
	return v.note + "  •  waveform"
}

func captionForScene(v visual) string {
	if v.scene == nil {
		return v.note
	}
	return v.note + "  •  " + plural(len(v.scene.Tracks), "track", "tracks")
}

// drawWaveform paints the min/max peaks as vertical bars centred on the panel's mid-line.
func drawWaveform(gtx layout.Context, peaks []canvas.Peak) {
	size := gtx.Constraints.Max
	if size.X <= 0 || size.Y <= 0 || len(peaks) == 0 {
		return
	}
	mid := float32(size.Y) / 2
	half := float32(size.Y)/2 - 8

	fillRect(gtx.Ops, image.Rect(0, int(mid), size.X, int(mid)+1), colGridLine) // centre line

	w := float32(size.X) / float32(len(peaks))
	var path clip.Path
	path.Begin(gtx.Ops)
	for i, p := range peaks {
		x := float32(i) * w
		top := mid - clampUnit(p.Max)*half
		bot := mid + clampUnit(p.Min)*half
		if bot-top < 1 {
			bot = top + 1
		}
		barW := maxF(w-0.5, 0.5)
		path.MoveTo(f32.Pt(x, top))
		path.LineTo(f32.Pt(x+barW, top))
		path.LineTo(f32.Pt(x+barW, bot))
		path.LineTo(f32.Pt(x, bot))
		path.Close()
	}
	paint.FillShape(gtx.Ops, colWave, clip.Outline{Path: path.End()}.Op())
}

// drawScene paints DAW track lanes with clip blocks on a simple timeline. Each track is a
// horizontal lane; clips are neon-edged blocks positioned by the scene's viewport zoom.
func drawScene(gtx layout.Context, scene *canvas.Scene) {
	size := gtx.Constraints.Max
	if scene == nil || size.X <= 0 || size.Y <= 0 || len(scene.Tracks) == 0 {
		return
	}
	laneH := size.Y / len(scene.Tracks)
	if laneH < 14 {
		laneH = 14
	}
	headerW := 132

	for i, tr := range scene.Tracks {
		y0 := i * laneH
		if y0 >= size.Y {
			break
		}
		laneCol := colLaneA
		if i%2 == 1 {
			laneCol = colLaneB
		}
		fillRect(gtx.Ops, image.Rect(0, y0, size.X, y0+laneH-1), laneCol)
		fillRect(gtx.Ops, image.Rect(0, y0, headerW, y0+laneH-1), colLaneHeader)

		for _, c := range tr.Clips {
			x0 := headerW + int(scene.Viewport.TickToPixel(c.Start))
			x1 := headerW + int(scene.Viewport.TickToPixel(c.End()))
			if x1 <= headerW {
				continue
			}
			if x0 < headerW {
				x0 = headerW
			}
			if x1 > size.X {
				x1 = size.X
			}
			if x1-x0 < 2 {
				x1 = x0 + 2
			}
			rr := image.Rect(x0, y0+4, x1, y0+laneH-5)
			if rr.Max.Y > rr.Min.Y {
				fillRRect(gtx.Ops, rr, 4, colClip)
				strokeRect(gtx.Ops, rr, colClipEdge)
			}
		}
	}
}

// drawPianoRoll draws a placeholder piano-roll surface: alternating key rows (white/black
// key bands) with faint bar lines. The real note grid is a later phase; this gives MIDI
// mode a recognisable, branded surface now (shapes, not text).
func (a *App) drawPianoRoll(gtx layout.Context) {
	size := gtx.Constraints.Max
	if size.X <= 0 || size.Y <= 0 {
		return
	}
	const rows = 24 // two octaves of visible key rows
	rowH := size.Y / rows
	if rowH < 4 {
		rowH = 4
	}
	black := map[int]bool{1: true, 3: true, 6: true, 8: true, 10: true} // black-key semitones
	for r := 0; r < rows; r++ {
		y0 := r * rowH
		col := colLaneB
		if black[r%12] {
			col = colLaneA
		}
		fillRect(gtx.Ops, image.Rect(0, y0, size.X, y0+rowH-1), col)
	}
	if step := size.X / 8; step > 0 {
		for x := 0; x < size.X; x += step {
			fillRect(gtx.Ops, image.Rect(x, 0, x+1, size.Y), colGridLine)
		}
	}
}

// --- small drawing helpers -------------------------------------------------------

// fillRect fills an axis-aligned rectangle with a solid colour.
func fillRect(ops *op.Ops, r image.Rectangle, c color.NRGBA) {
	defer clip.Rect(r).Push(ops).Pop()
	paint.ColorOp{Color: c}.Add(ops)
	paint.PaintOp{}.Add(ops)
}

// fillRRect fills a rounded rectangle with a solid colour.
func fillRRect(ops *op.Ops, r image.Rectangle, radius int, c color.NRGBA) {
	defer clip.UniformRRect(r, radius).Push(ops).Pop()
	paint.ColorOp{Color: c}.Add(ops)
	paint.PaintOp{}.Add(ops)
}

// strokeRect draws a 1px rectangle outline in colour c (four hairline edges). Gives a
// shape a crisp neon border without a fill.
func strokeRect(ops *op.Ops, r image.Rectangle, c color.NRGBA) {
	edges := []image.Rectangle{
		{Min: r.Min, Max: image.Pt(r.Max.X, r.Min.Y+1)},
		{Min: image.Pt(r.Min.X, r.Max.Y-1), Max: r.Max},
		{Min: r.Min, Max: image.Pt(r.Min.X+1, r.Max.Y)},
		{Min: image.Pt(r.Max.X-1, r.Min.Y), Max: r.Max},
	}
	for _, e := range edges {
		func() {
			defer clip.Rect(e).Push(ops).Pop()
			paint.ColorOp{Color: c}.Add(ops)
			paint.PaintOp{}.Add(ops)
		}()
	}
}

// clampUnit clamps a sample magnitude to [0, 1] (we draw from the centre line out).
func clampUnit(v float32) float32 {
	if v < 0 {
		v = -v
	}
	if v > 1 {
		return 1
	}
	return v
}

// maxF returns the larger of two float32s.
func maxF(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}
