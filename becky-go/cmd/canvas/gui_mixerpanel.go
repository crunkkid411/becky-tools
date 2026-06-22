//go:build gui

// gui_mixerpanel.go — in-window MIXER + routing panel (CANVAS-BLUEPRINT.md §2.4).
//
// One vertical channel strip per a.arr track in a horizontal scrollable row.
// Fader    → a.arr.SetGain  (0..2 linear; 1 = unity)
// Pan      → a.arr.SetPan   (-1..1)
// Mute     → a.arr.SetMute  (red when active)
// Solo     → a.arr.SetSolo  (yellow when active)
// BusCycle → a.arr.RouteTo  (cycles through a.arr.Buses IDs)
//
// All edits are immutable: each verb returns a NEW *Arrangement forwarded to
// a.applyArr — never mutating a.arr in place. Widget state is keyed by track ID
// and persists across redraws.
package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"becky-go/internal/autoroute"
	"becky-go/internal/fxchain"
)

// stripState holds widget state for ONE channel strip. Created on first use, keyed by
// track ID. Fields persist across frames so Gio can track drag/click continuity.
type stripState struct {
	fader    widget.Float // horizontal slider mapped to gain 0..2 via value 0..1
	pan      widget.Float // horizontal slider mapped to pan -1..1 via value 0..1
	lastGain float64      // previous gain written; sentinel -999 = not yet synced
	lastPan  float64      // previous pan written;  sentinel -999 = not yet synced
	mute     widget.Clickable
	solo     widget.Clickable
	busCycle widget.Clickable
}

type mixerPanel struct {
	strips    map[string]*stripState
	stripList layout.List
	route     widget.Clickable // "Route" button: runs autoroute.Apply in one shot
}

func newMixerPanel() *mixerPanel {
	m := &mixerPanel{strips: make(map[string]*stripState)}
	m.stripList.Axis = layout.Horizontal
	return m
}

// layout is called each frame from layoutVisual when the canvas is in DAW mode.
func (m *mixerPanel) layout(gtx layout.Context, a *App) layout.Dimensions {
	if a.arr == nil || len(a.arr.Tracks) == 0 {
		return panelPlaceholder(gtx, a, "mixer — drop a project.json to see channel strips")
	}

	// Load FX chains once per frame so every strip can query its bus chain cheaply.
	chains := fxchain.Load()

	// Process widget events before drawing so edits take effect this frame.
	m.handleStripEvents(gtx, a)

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return m.mixerHeader(gtx, a)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			tracks := a.arr.Tracks
			return m.stripList.Layout(gtx, len(tracks), func(gtx layout.Context, i int) layout.Dimensions {
				t := tracks[i]
				st := m.stripFor(t.ID)
				return m.layoutStrip(gtx, a, chains, t.ID,
					t.Strip.Gain, t.Strip.Pan,
					t.Strip.Mute, t.Strip.Solo, t.Strip.Bus, st)
			})
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return m.layoutRoutingSummary(gtx, a)
		}),
	)
}

// handleStripEvents processes accumulated click/drag events and calls the matching
// dawmodel verb when a value change is detected.
func (m *mixerPanel) handleStripEvents(gtx layout.Context, a *App) {
	if a.arr == nil {
		return
	}

	// Route button: apply the deterministic label→bus ruleset in one shot.
	if m.route.Clicked(gtx) && a.arr != nil {
		next, _ := autoroute.Apply(a.arr, autoroute.Load())
		a.applyArr(next)
		return
	}

	for _, t := range a.arr.Tracks {
		st := m.stripFor(t.ID)

		// fader: widget range 0..1 maps to gain 0..2.
		newGain := float64(st.fader.Value) * 2.0
		if st.lastGain == -999 {
			// First frame: push arrangement value into widget widget, skip change emit.
			st.fader.Value = float32(t.Strip.Gain / 2.0)
			st.lastGain = t.Strip.Gain
		} else if abs64(newGain-st.lastGain) > 1e-4 {
			if next, err := a.arr.SetGain(t.ID, newGain); err == nil {
				st.lastGain = newGain
				a.applyArr(next)
				return // arr swapped; restart next frame
			}
		}

		// pan: widget range 0..1 maps to pan -1..1.
		newPan := float64(st.pan.Value)*2.0 - 1.0
		if st.lastPan == -999 {
			st.pan.Value = float32((t.Strip.Pan + 1.0) / 2.0)
			st.lastPan = t.Strip.Pan
		} else if abs64(newPan-st.lastPan) > 1e-4 {
			if next, err := a.arr.SetPan(t.ID, newPan); err == nil {
				st.lastPan = newPan
				a.applyArr(next)
				return
			}
		}

		if st.mute.Clicked(gtx) {
			if next, err := a.arr.SetMute(t.ID, !t.Strip.Mute); err == nil {
				a.applyArr(next)
				return
			}
		}

		if st.solo.Clicked(gtx) {
			if next, err := a.arr.SetSolo(t.ID, !t.Strip.Solo); err == nil {
				a.applyArr(next)
				return
			}
		}

		if st.busCycle.Clicked(gtx) {
			ids := m.busIDList(a)
			if len(ids) > 0 {
				if next, err := a.arr.RouteTo(t.ID, nextBus(ids, t.Strip.Bus)); err == nil {
					a.applyArr(next)
					return
				}
			}
		}
	}
}

// layoutStrip renders one vertical channel strip.
func (m *mixerPanel) layoutStrip(
	gtx layout.Context, a *App,
	chains fxchain.Chains,
	id string, gain, pan float64,
	muted, soloed bool, bus string,
	st *stripState,
) layout.Dimensions {
	const stripW = 72

	gtx.Constraints.Min.X = gtx.Dp(unit.Dp(stripW))
	gtx.Constraints.Max.X = gtx.Dp(unit.Dp(stripW))

	macro := op.Record(gtx.Ops)
	dims := layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
		// bus-colour accent bar — Jordan's rule: a track WEARS its bus colour.
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			w := gtx.Constraints.Max.X
			h := gtx.Dp(unit.Dp(4))
			fillRRect(gtx.Ops, image.Rect(0, 0, w, h), 2, busColor(bus))
			return layout.Dimensions{Size: image.Pt(w, h)}
		}),
		// track name label — coloured by its bus (vocals=green, guitars=red, …)
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(3)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				lbl := material.Caption(a.th, truncateID(id, 8))
				lbl.Color = busColor(bus)
				return lbl.Layout(gtx)
			})
		}),
		// vertical fader
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return m.layoutVerticalFader(gtx, a, st, gain)
		}),
		// gain readout
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(2)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				lbl := material.Caption(a.th, gainLabel(gain))
				lbl.Color = colNeonGreen
				return lbl.Layout(gtx)
			})
		}),
		// pan slider
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: unit.Dp(4), Right: unit.Dp(4), Bottom: unit.Dp(2)}.Layout(gtx,
				func(gtx layout.Context) layout.Dimensions {
					sl := material.Slider(a.th, &st.pan)
					sl.Color = colElecBlue
					return sl.Layout(gtx)
				})
		}),
		// pan readout
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(2)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				lbl := material.Caption(a.th, panLabel(pan))
				lbl.Color = colElecBlue
				return lbl.Layout(gtx)
			})
		}),
		// mute + solo row
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return m.layoutToggleBtn(gtx, a.th, &st.mute, "M", muted, colCrimson)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(2)}.Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return m.layoutToggleBtn(gtx, a.th, &st.solo, "S", soloed, colYellow)
				}),
			)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(3)}.Layout),
		// bus cycle button
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return m.layoutBusCycle(gtx, a.th, &st.busCycle, bus)
		}),
		// per-bus FX chain: small plugin-name chips (read-only view).
		// TODO: add plugins via fxchain.Add when an insert UI is available.
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return m.layoutFXChips(gtx, a.th, chains.Get(bus))
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
	)
	call := macro.Stop()

	// strip background + right separator
	bg := image.Rectangle{Max: image.Pt(dims.Size.X, dims.Size.Y)}
	fillRect(gtx.Ops, bg, colLaneA)
	sep := image.Rectangle{
		Min: image.Pt(dims.Size.X-1, 0),
		Max: image.Pt(dims.Size.X, dims.Size.Y),
	}
	fillRect(gtx.Ops, sep, colGridLine)
	call.Add(gtx.Ops)
	return dims
}

// layoutVerticalFader draws a custom vertical fader visual while relying on an
// invisible horizontal slider widget underneath for pointer-event capture.
func (m *mixerPanel) layoutVerticalFader(gtx layout.Context, a *App, st *stripState, gain float64) layout.Dimensions {
	const faderH = 100

	fH := gtx.Dp(unit.Dp(faderH))
	fW := gtx.Dp(unit.Dp(52))
	gtx.Constraints = layout.Exact(image.Pt(fW, fH))

	// groove
	trackW := 4
	cx := fW / 2
	pad6 := gtx.Dp(unit.Dp(6))
	trackR := image.Rectangle{
		Min: image.Pt(cx-trackW/2, pad6),
		Max: image.Pt(cx+trackW/2, fH-pad6),
	}
	fillRRect(gtx.Ops, trackR, 2, colGridLine)

	// unity-gain mark (50% fader travel)
	usableH := fH - 2*pad6 - gtx.Dp(unit.Dp(14))
	unityY := fH - pad6 - gtx.Dp(unit.Dp(14)) - usableH/2
	unityR := image.Rectangle{
		Min: image.Pt(cx-8, unityY),
		Max: image.Pt(cx+8, unityY+1),
	}
	fillRect(gtx.Ops, unityR, colTextDim)

	// thumb — inverted: top = loud, bottom = quiet
	thumbFrac := gain / 2.0
	if thumbFrac < 0 {
		thumbFrac = 0
	}
	if thumbFrac > 1 {
		thumbFrac = 1
	}
	thumbY := pad6 + int((1.0-thumbFrac)*float64(usableH))
	thumbH := gtx.Dp(unit.Dp(14))
	thumbR := image.Rectangle{
		Min: image.Pt(cx-10, thumbY),
		Max: image.Pt(cx+10, thumbY+thumbH),
	}
	thumbCol := colNeonGreen
	if gain > 1.0+1e-4 {
		thumbCol = colYellow // hot gain visual cue
	}
	fillRRect(gtx.Ops, thumbR, 3, thumbCol)
	strokeRect(gtx.Ops, thumbR, colText)

	// invisible slider captures pointer events
	m.paintInvisibleSlider(gtx, a, &st.fader, fW, fH)

	return layout.Dimensions{Size: image.Pt(fW, fH)}
}

// paintInvisibleSlider lays a fully-transparent slider widget over the fader area so
// the Gio event system picks up drags without any additional drawing.
func (m *mixerPanel) paintInvisibleSlider(gtx layout.Context, a *App, f *widget.Float, w, h int) {
	macro := op.Record(gtx.Ops)
	slGtx := gtx
	slGtx.Constraints = layout.Exact(image.Pt(w, h))
	sl := material.Slider(a.th, f)
	sl.Color = color.NRGBA{A: 0} // transparent
	sl.Layout(slGtx)             //nolint:errcheck
	call := macro.Stop()
	call.Add(gtx.Ops)
}

// layoutToggleBtn renders a small square toggle button (M = mute, S = solo).
func (m *mixerPanel) layoutToggleBtn(
	gtx layout.Context, th *material.Theme,
	btn *widget.Clickable,
	label string, active bool, activeCol color.NRGBA,
) layout.Dimensions {
	return layout.UniformInset(unit.Dp(2)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		sz := gtx.Dp(unit.Dp(22))
		gtx.Constraints = layout.Exact(image.Pt(sz, sz))

		macro := op.Record(gtx.Ops)
		dims := btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Dimensions{Size: image.Pt(sz, sz)}
		})
		call := macro.Stop()

		bgCol := colHeaderBg
		textCol := colTextDim
		if active {
			bgCol = activeCol
			textCol = colWindowBg
		}
		r := image.Rectangle{Max: image.Pt(sz, sz)}
		fillRRect(gtx.Ops, r, 3, bgCol)
		strokeRect(gtx.Ops, r, colGridLine)
		call.Add(gtx.Ops)

		layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			lbl := material.Caption(th, label)
			lbl.Color = textCol
			return lbl.Layout(gtx)
		})
		return dims
	})
}

// layoutBusCycle renders a small bus-routing button that cycles through bus IDs on click.
func (m *mixerPanel) layoutBusCycle(
	gtx layout.Context, th *material.Theme,
	btn *widget.Clickable,
	currentBus string,
) layout.Dimensions {
	return layout.Inset{Left: unit.Dp(4), Right: unit.Dp(4)}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			h := gtx.Dp(unit.Dp(16))
			gtx.Constraints.Min.Y = h
			gtx.Constraints.Max.Y = h

			macro := op.Record(gtx.Ops)
			dims := btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, h)}
			})
			call := macro.Stop()

			r := image.Rectangle{Max: dims.Size}
			fillRRect(gtx.Ops, r, 2, colHeaderBg)
			strokeRect(gtx.Ops, r, colDeepPurple)
			call.Add(gtx.Ops)

			layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				lbl := material.Caption(th, shortBusName(currentBus))
				lbl.Color = colDeepPurple
				return lbl.Layout(gtx)
			})
			return dims
		})
}

// layoutFXChips renders the ordered plugin names from ch as small read-only caption
// chips under the bus-cycle button. If the chain is empty, a quiet "no chain" hint
// is shown in colTextDim. Uses the same colDeepPurple text as layoutBusCycle.
func (m *mixerPanel) layoutFXChips(gtx layout.Context, th *material.Theme, ch fxchain.Chain) layout.Dimensions {
	return layout.Inset{Left: unit.Dp(4), Right: unit.Dp(4), Top: unit.Dp(2)}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			if len(ch.Plugins) == 0 {
				lbl := material.Caption(th, "no chain")
				lbl.Color = colTextDim
				return lbl.Layout(gtx)
			}
			items := make([]layout.FlexChild, 0, len(ch.Plugins))
			for _, p := range ch.Plugins {
				name := p.Name
				items = append(items, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					lbl := material.Caption(th, truncateID(name, 7))
					lbl.Color = colDeepPurple
					return lbl.Layout(gtx)
				}))
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, items...)
		})
}

// layoutRoutingSummary draws a compact bus → out routing summary at the panel bottom.
func (m *mixerPanel) layoutRoutingSummary(gtx layout.Context, a *App) layout.Dimensions {
	if a.arr == nil || len(a.arr.Buses) == 0 {
		return layout.Dimensions{}
	}
	return layout.UniformInset(unit.Dp(6)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		lines := make([]string, 0, len(a.arr.Buses))
		for _, b := range a.arr.Buses {
			line := fmt.Sprintf("%s → %s", shortBusName(b.ID), shortBusName(b.Out))
			if len(b.Sidechain) > 0 {
				sc := make([]string, len(b.Sidechain))
				for i, s := range b.Sidechain {
					sc[i] = shortBusName(s)
				}
				line += fmt.Sprintf(" (SC: %s)", strings.Join(sc, ", "))
			}
			lines = append(lines, line)
		}
		return layoutStringLines(gtx, a.th, lines, colTextDim)
	})
}

// ---------- helpers ------------------------------------------------------------------

// stripFor returns the stripState for id, creating it with sentinel values on first use.
func (m *mixerPanel) stripFor(id string) *stripState {
	if st, ok := m.strips[id]; ok {
		return st
	}
	st := &stripState{lastGain: -999, lastPan: -999}
	m.strips[id] = st
	return st
}

// busIDList returns the bus IDs from the current arrangement in declaration order.
func (m *mixerPanel) busIDList(a *App) []string {
	if a.arr == nil {
		return nil
	}
	ids := make([]string, len(a.arr.Buses))
	for i, b := range a.arr.Buses {
		ids[i] = b.ID
	}
	return ids
}

// nextBus returns the bus after current in ids, wrapping. Falls back to ids[0].
func nextBus(ids []string, current string) string {
	if len(ids) == 0 {
		return ""
	}
	for i, id := range ids {
		if id == current {
			return ids[(i+1)%len(ids)]
		}
	}
	return ids[0]
}

// mixerHeader draws the "MIXER" section label and the one-shot "Route" button.
// The Route button runs autoroute.Apply(a.arr, autoroute.Load()) via handleStripEvents.
func (m *mixerPanel) mixerHeader(gtx layout.Context, a *App) layout.Dimensions {
	return layout.Inset{Left: unit.Dp(8), Top: unit.Dp(4), Bottom: unit.Dp(2)}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					lbl := material.Caption(a.th, "MIXER")
					lbl.Color = colNeonGreen
					return lbl.Layout(gtx)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return m.layoutBusCycle(gtx, a.th, &m.route, "Route")
				}),
			)
		})
}

// layoutStringLines renders lines as a vertical Flex of Caption labels.
func layoutStringLines(gtx layout.Context, th *material.Theme, lines []string, col color.NRGBA) layout.Dimensions {
	widgets := make([]layout.FlexChild, len(lines))
	for i, line := range lines {
		l := line
		widgets[i] = layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			lbl := material.Caption(th, l)
			lbl.Color = col
			return lbl.Layout(gtx)
		})
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, widgets...)
}

// gainLabel formats gain as a short dB string ("+0.0" / "-3.5" / "-∞").
func gainLabel(gain float64) string {
	if gain <= 0 {
		return "-inf"
	}
	db := 20.0 * math.Log10(gain)
	if db >= 0 {
		return fmt.Sprintf("+%.1f", db)
	}
	return fmt.Sprintf("%.1f", db)
}

// panLabel formats pan as "L50" / "R50" / "C".
func panLabel(pan float64) string {
	switch {
	case pan < -0.01:
		return fmt.Sprintf("L%.0f", -pan*100)
	case pan > 0.01:
		return fmt.Sprintf("R%.0f", pan*100)
	default:
		return "C"
	}
}

// shortBusName trims the common "bus." prefix for compact display.
func shortBusName(id string) string {
	id = strings.TrimPrefix(id, "bus.")
	if id == "" {
		return "-"
	}
	if len(id) > 7 {
		return id[:7]
	}
	return id
}

// truncateID returns at most n runes of id for label display.
func truncateID(id string, n int) string {
	r := []rune(id)
	if len(r) <= n {
		return id
	}
	return string(r[:n])
}

// abs64 returns |x|.
func abs64(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// Sentinel to keep the clip + paint imports used (the compiler would otherwise
// complain since fillRect/fillRRect are defined in gui_waveform.go in the same package).
var (
	_ = clip.Rect{}
	_ = paint.PaintOp{}
)
