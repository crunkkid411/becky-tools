//go:build gui

// gui_overlay.go — the GLOBAL "show me, don't do it" preview overlay.
//
// When Jordan types an instruction in the agent box (with something selected on
// the canvas), becky produces a Proposal and shows it as a neon preview OVER
// the current canvas surface.  Nothing changes until he explicitly approves.
//
// UX north star (CLAUDE.md §6 / CANVAS-INSPIRATION.md):
//   - Colours and shapes > text. The overlay is a coloured strip ABOVE the
//     agent box, NOT a modal dialog or wall of text.
//   - A left-side coloured bar (change-kind colour) + "before → after" pair.
//   - ONE short sentence of plain text (Proposal.Summary).
//   - Two large, obvious affordances: ✓ (Approve, neon-green) and ✗ (Reject,
//     crimson). Enter = approve, Esc = reject (keyboard-first).
//   - When no proposal is pending, the overlay takes ZERO screen space.
//
// Reusability: overlayWidget is a self-contained struct. Any canvas mode sets
// a.overlay.show(proposal) and the main layoutWorkColumn renders it
// automatically via a.layoutOverlay. Piano-roll, drum, video — same path.
//
// Habits: on Approve, habits.AppendCorrectionLog is called (best-effort).
//
// degrade-never-crash (CLAUDE.md §2): every error path emits a neon line,
// never panics. The overlay is dismissed on any Apply failure.
package main

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"becky-go/internal/canvas"
	"becky-go/internal/habits"
)

// ─── overlayWidget ────────────────────────────────────────────────────────────

// overlayWidget holds the full Gio widget state for the preview overlay.
// Embed it in App; the zero value is "no pending proposal".
type overlayWidget struct {
	// pending is the proposal currently shown. nil = overlay hidden.
	pending *canvas.Proposal

	// approveBtn / rejectBtn are the ✓ / ✗ clickable areas.
	approveBtn widget.Clickable
	rejectBtn  widget.Clickable
}

// show loads p into the overlay. A nil p silently clears it (same as dismiss).
func (o *overlayWidget) show(p *canvas.Proposal) { o.pending = p }

// dismiss clears the overlay without applying.
func (o *overlayWidget) dismiss() { o.pending = nil }

// hasPending reports whether a proposal is waiting.
func (o *overlayWidget) hasPending() bool { return o.pending != nil }

// ─── Input handling ───────────────────────────────────────────────────────────

// handleOverlayInput processes approve/reject button clicks every frame.
// Call it from App.handleInput whenever the overlay might be visible.
func (a *App) handleOverlayInput(gtx layout.Context) {
	if !a.overlay.hasPending() {
		return
	}
	if a.overlay.approveBtn.Clicked(gtx) {
		a.applyProposal()
	}
	if a.overlay.rejectBtn.Clicked(gtx) {
		a.rejectProposal()
	}
}

// applyProposal applies the pending proposal: updates the scene, appends a
// habits correction log entry (best-effort), then dismisses the overlay.
func (a *App) applyProposal() {
	p := a.overlay.pending
	if p == nil {
		return
	}
	next, err := canvas.Apply(a.scene, p)
	if err != nil {
		a.appendLine("couldn't apply: " + err.Error())
		a.overlay.dismiss()
		return
	}
	a.scene = next
	a.appendHabitEntry(p)
	a.overlay.dismiss()
	a.appendLine("✓ " + p.Summary)
	a.window.Invalidate()
}

// rejectProposal discards the pending proposal without changing the scene.
func (a *App) rejectProposal() {
	p := a.overlay.pending
	a.scene = canvas.RejectScene(a.scene, p)
	a.overlay.dismiss()
	a.appendLine("✗ rejected.")
	a.window.Invalidate()
}

// appendHabitEntry appends one JSONL correction line for the learning loop.
// Best-effort: any failure is silently discarded — degrade, never crash.
func (a *App) appendHabitEntry(p *canvas.Proposal) {
	if p == nil {
		return
	}
	logPath := overlayHabitsLogPath()
	if logPath == "" {
		return
	}
	scope := string(p.Sel.Kind)
	if p.Sel.TrackID != "" {
		scope = p.Sel.TrackID
	}
	// Failure is non-fatal (degrade, never crash).
	_ = habits.AppendCorrectionLog(logPath, "canvas", scope, string(p.Kind), p.Before, p.After)
}

// overlayHabitsLogPath returns the path for canvas.corrections.jsonl, placed
// next to becky-canvas.exe (or CWD as fallback). Returns "" on failure.
func overlayHabitsLogPath() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), "canvas.corrections.jsonl")
	}
	if wd, err := os.Getwd(); err == nil {
		return filepath.Join(wd, "canvas.corrections.jsonl")
	}
	return ""
}

// canvasExeDir returns the directory containing becky-canvas.exe, or "".
// Exported for gui_overlay helpers; used only in this file.
func canvasExeDir() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Dir(exe)
	}
	return ""
}

// ─── Propose path ─────────────────────────────────────────────────────────────

// proposeForInstruction is the new agent-box submit path. When a selection is
// active, it calls the transformer and shows the overlay. When nothing is
// selected it falls through to the existing keyword tool-routing path
// (runCommand) — no existing behaviour is broken.
func (a *App) proposeForInstruction(instruction string) {
	instruction = strings.TrimSpace(instruction)
	if instruction == "" {
		return
	}
	if a.selection.Empty() {
		// No selection → standard tool routing (existing behaviour preserved).
		a.runCommand(instruction)
		return
	}
	// The real local model loads on first use (~10-30s) — run Propose OFF the UI
	// thread so the window never freezes. Snapshot the inputs; the result is posted
	// back via a.incomingProposal and drained into the overlay on the next frame.
	a.command.SetText("")
	a.outExpanded = true
	a.appendLine("becky is thinking…")
	scene, sel, tr := a.scene, a.selection, a.transformer
	go func() {
		p, err := tr.Propose(scene, sel, instruction)
		if err != nil {
			a.appendLine("becky: " + err.Error())
			a.window.Invalidate()
			return
		}
		a.mu.Lock()
		a.incomingProposal = p
		a.mu.Unlock()
		a.window.Invalidate()
	}()
}

// ─── Rendering ────────────────────────────────────────────────────────────────

// overlayHeight is the fixed pixel height of the preview strip.
const overlayHeight = 68

// layoutOverlay draws the preview overlay above the agent box. It returns zero
// dimensions when no proposal is pending (takes no space in the layout).
func (a *App) layoutOverlay(gtx layout.Context) layout.Dimensions {
	if !a.overlay.hasPending() {
		return layout.Dimensions{}
	}
	p := a.overlay.pending
	accent := overlayAccent(p.Kind)

	h := gtx.Dp(unit.Dp(overlayHeight))
	gtx.Constraints.Min.Y = h
	gtx.Constraints.Max.Y = h

	return widgetBg(gtx, colCanvasBg, func(gtx layout.Context) layout.Dimensions {
		return borderBox(gtx, accent, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(10)).Layout(gtx,
				func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{
						Axis:      layout.Horizontal,
						Alignment: layout.Middle,
					}.Layout(gtx,
						// Coloured kind-indicator bar (shapes > text).
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return drawKindBar(gtx, accent)
						}),
						layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
						// Before → After + summary.
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return a.layoutOverlayContent(gtx, p, accent)
						}),
						layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
						// ✓ and ✗ buttons.
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return a.layoutOverlayButtons(gtx)
						}),
					)
				})
		})
	})
}

// layoutOverlayContent draws the before → after pair and the summary text.
func (a *App) layoutOverlayContent(gtx layout.Context, p *canvas.Proposal, accent color.NRGBA) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical, Alignment: layout.Start}.Layout(gtx,
		// Row 1: Before → After (colours and shapes).
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if p.Before == "" && p.After == "" {
				return layout.Dimensions{}
			}
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return overlayText(gtx, a.th, p.Before, colTextDim, unit.Sp(13))
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return drawOverlayArrow(gtx, accent)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return overlayText(gtx, a.th, p.After, accent, unit.Sp(13))
				}),
			)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
		// Row 2: One-sentence summary (the only text).
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			s := p.Summary
			if len(s) > 80 {
				s = s[:77] + "…"
			}
			return overlayText(gtx, a.th, s, colText, unit.Sp(12))
		}),
	)
}

// layoutOverlayButtons draws the ✓ / ✗ affordances using vector icons so no
// font glyph is needed (the theme font lacks ✓/✗ codepoints → tofu boxes).
func (a *App) layoutOverlayButtons(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.overlayBtn(gtx, &a.overlay.approveBtn, a.icons.apply, colNeonGreen)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.overlayBtn(gtx, &a.overlay.rejectBtn, a.icons.reject, colCrimson)
		}),
	)
}

// overlayBtn draws one 44×44 approve/reject button: a neon-edged square with
// a vector icon. Hover brightens the border. ic may be nil (degrade to blank).
func (a *App) overlayBtn(gtx layout.Context, btn *widget.Clickable, ic *widget.Icon, accent color.NRGBA) layout.Dimensions {
	side := gtx.Dp(unit.Dp(44))
	gtx.Constraints = layout.Exact(image.Pt(side, side))
	return btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		bg, border := colCanvasBg, accent
		if btn.Hovered() {
			bg = colHeaderBg
			border = colNeonGreen
		}
		fillRRect(gtx.Ops, image.Rect(0, 0, side, side), 8, bg)
		strokeRect(gtx.Ops, image.Rect(0, 0, side, side), border)
		if ic == nil {
			return layout.Dimensions{Size: image.Pt(side, side)}
		}
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			iconColor := accent
			if btn.Hovered() {
				iconColor = colNeonGreen
			}
			iconPx := gtx.Dp(unit.Dp(22))
			gtx.Constraints = layout.Exact(image.Pt(iconPx, iconPx))
			return ic.Layout(gtx, iconColor)
		})
	})
}

// shapeBtn is a transport button that DRAWS its icon as a vector shape — a
// right-pointing PLAY triangle or a STOP square — instead of a font glyph. The
// theme font lacks ▶/■ (U+25B6 / U+25A0), so those rendered as empty tofu boxes
// (why "play" looked like a square). shape is "play" or "stop".
func (a *App) shapeBtn(gtx layout.Context, btn *widget.Clickable, shape string, accent color.NRGBA) layout.Dimensions {
	side := gtx.Dp(unit.Dp(44))
	gtx.Constraints = layout.Exact(image.Pt(side, side))
	return btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		bg, border, fg := colCanvasBg, accent, accent
		if btn.Hovered() {
			bg, border, fg = colHeaderBg, colNeonGreen, colNeonGreen
		}
		fillRRect(gtx.Ops, image.Rect(0, 0, side, side), 8, bg)
		strokeRect(gtx.Ops, image.Rect(0, 0, side, side), border)
		s := float32(side)
		switch shape {
		case "play": // right-pointing triangle (the universal play icon)
			m := s * 0.30
			var p clip.Path
			p.Begin(gtx.Ops)
			p.MoveTo(f32.Pt(m, m))
			p.LineTo(f32.Pt(s-m*0.8, s/2))
			p.LineTo(f32.Pt(m, s-m))
			p.Close()
			paint.FillShape(gtx.Ops, fg, clip.Outline{Path: p.End()}.Op())
		case "stop": // filled square
			m := int(s * 0.32)
			fillRRect(gtx.Ops, image.Rect(m, m, side-m, side-m), 3, fg)
		}
		return layout.Dimensions{Size: image.Pt(side, side)}
	})
}

// ─── drawing helpers ──────────────────────────────────────────────────────────

// overlayAccent maps a ChangeKind to the overlay accent colour so each change
// class has its own visual identity (shapes > text).
func overlayAccent(kind canvas.ChangeKind) color.NRGBA {
	switch kind {
	case canvas.ChangePitch:
		return colDeepPurple
	case canvas.ChangeTiming, canvas.ChangeTrim:
		return colElecBlue
	case canvas.ChangeGain:
		return colYellow
	case canvas.ChangeRoute:
		return colNeonPink
	case canvas.ChangeText:
		return colTextDim
	case canvas.ChangeStructure:
		return colNeonGreen
	default:
		return colNeonGreen
	}
}

// drawKindBar draws the solid coloured vertical bar on the left of the overlay
// (the non-textual change-type indicator).
func drawKindBar(gtx layout.Context, col color.NRGBA) layout.Dimensions {
	w := gtx.Dp(unit.Dp(4))
	h := gtx.Dp(unit.Dp(overlayHeight - 20))
	defer clip.Rect{Max: image.Pt(w, h)}.Push(gtx.Ops).Pop()
	paint.ColorOp{Color: col}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	return layout.Dimensions{Size: image.Pt(w, h)}
}

// drawOverlayArrow draws a small filled right-pointing arrow between the
// before and after labels.
func drawOverlayArrow(gtx layout.Context, col color.NRGBA) layout.Dimensions {
	sz := float32(gtx.Dp(unit.Dp(10)))
	mid := sz / 2
	third := sz / 3
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(f32.Pt(0, mid-third))
	p.LineTo(f32.Pt(sz*0.6, mid-third))
	p.LineTo(f32.Pt(sz*0.6, 0))
	p.LineTo(f32.Pt(sz, mid))
	p.LineTo(f32.Pt(sz*0.6, sz))
	p.LineTo(f32.Pt(sz*0.6, mid+third))
	p.LineTo(f32.Pt(0, mid+third))
	p.Close()
	paint.FillShape(gtx.Ops, col, clip.Outline{Path: p.End()}.Op())
	return layout.Dimensions{Size: image.Pt(int(sz), int(sz))}
}

// overlayText draws a single-line text label inside the overlay.
func overlayText(gtx layout.Context, th *material.Theme, txt string, col color.NRGBA, sp unit.Sp) layout.Dimensions {
	if txt == "" {
		return layout.Dimensions{}
	}
	lbl := material.Body2(th, txt)
	lbl.Color = col
	lbl.TextSize = sp
	return lbl.Layout(gtx)
}
