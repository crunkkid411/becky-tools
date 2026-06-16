//go:build gui

// gui_waveform.go — the VISUAL-FIRST surface (CLAUDE.md HARD REQUIREMENT). It turns the
// current target into a picture drawn with Gio op/paint/clip:
//   - a .wav target -> decoded via internal/dsp and drawn as a waveform (min/max peaks);
//   - a becky-compose project.json -> loaded via internal/canvas and drawn as DAW track
//     lanes with clip blocks on a timeline;
//   - anything else -> a friendly "drop an audio file or a project.json here" panel.
//
// All decode/parse failures degrade to that friendly panel — never a panic.
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

// visual is the decoded/parsed target ready to draw, computed off the UI thread when
// the target changes. It holds the cheap-to-draw representation only (peaks / scene),
// not the raw audio, so each frame is fast.
type visual struct {
	kind  visualKind
	peaks []canvas.Peak // waveform overview (visualWave)
	dur   float64       // audio duration in seconds (visualWave)
	scene *canvas.Scene // DAW scene (visualScene)
	note  string        // friendly status / error message
}

// wavePeakCount is the fixed number of min/max peak pairs we reduce any WAV to for the
// overview. Drawing a fixed count keeps the waveform fast regardless of file length and
// deterministic for the same input.
const wavePeakCount = 1600

// buildVisual inspects target and produces a drawable representation. It is pure-ish
// (filesystem read only) and safe to call from a worker goroutine. Every failure path
// returns a visualNone with a plain-language note — degrade, never crash.
func buildVisual(target string) visual {
	target = strings.TrimSpace(target)
	if target == "" {
		return visual{kind: visualNone, note: "Paste a path above, or click a tool. Drop in a .wav to see its waveform, or a project.json to see the song lanes."}
	}
	lower := strings.ToLower(target)
	switch {
	case strings.HasSuffix(lower, ".wav"):
		return waveVisual(target)
	case strings.HasSuffix(lower, ".json"):
		return sceneVisual(target)
	default:
		return visual{kind: visualNone, note: "Nothing to draw for " + pathx.Base(target) + " yet. This area shows a waveform for .wav files and song lanes for a project.json."}
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
		// Load still returns a usable (empty) scene on failure — show it with a note.
		return visual{kind: visualScene, scene: &scene, note: "Opened an empty song view (" + err.Error() + ")"}
	}
	return visual{kind: visualScene, scene: &scene, note: scene.Title}
}

// peaksFromSamples reduces mono samples to n min/max peak pairs (the standard waveform
// overview primitive). Fewer samples than buckets => one sample per bucket; zero
// samples => a flat line. Deterministic for the same input.
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

// layoutVisual draws the current visual into the given area. It always fills a dark
// canvas background first so the panel reads as a distinct surface, then draws the
// waveform / lanes / hint on top.
func (a *App) layoutVisual(gtx layout.Context, th *material.Theme) layout.Dimensions {
	size := gtx.Constraints.Max
	// Background surface.
	fillRect(gtx.Ops, image.Rect(0, 0, size.X, size.Y), colCanvasBg)

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
	return layout.Dimensions{Size: size}
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
	half := float32(size.Y)/2 - 8 // leave a small margin

	// Centre line.
	fillRect(gtx.Ops, image.Rect(0, int(mid), size.X, int(mid)+1), colGridLine)

	w := float32(size.X) / float32(len(peaks))
	var path clip.Path
	path.Begin(gtx.Ops)
	for i, p := range peaks {
		x := float32(i) * w
		top := mid - clampUnit(p.Max)*half
		bot := mid + clampUnit(p.Min)*half
		if bot-top < 1 {
			bot = top + 1 // always at least a hairline so silence is visible
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

// drawScene paints DAW track lanes with clip blocks on a simple timeline. Each track is
// a horizontal lane; clips are rounded blocks positioned by the scene's viewport zoom.
func drawScene(gtx layout.Context, scene *canvas.Scene) {
	size := gtx.Constraints.Max
	if scene == nil || size.X <= 0 || size.Y <= 0 {
		return
	}
	if len(scene.Tracks) == 0 {
		return
	}
	laneH := size.Y / len(scene.Tracks)
	if laneH < 14 {
		laneH = 14
	}
	headerW := 132 // lane header strip width on the left

	for i, tr := range scene.Tracks {
		y0 := i * laneH
		if y0 >= size.Y {
			break
		}
		// Alternating lane background for readability.
		laneCol := colLaneA
		if i%2 == 1 {
			laneCol = colLaneB
		}
		fillRect(gtx.Ops, image.Rect(0, y0, size.X, y0+laneH-1), laneCol)
		// Header strip.
		fillRect(gtx.Ops, image.Rect(0, y0, headerW, y0+laneH-1), colLaneHeader)

		// Clip blocks.
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
			}
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

// clampUnit clamps a sample magnitude to [0, 1] (we draw from the centre line out, so
// only the magnitude matters) so a stray spike can't draw off-panel.
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
