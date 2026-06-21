package beatgen

import "becky-go/internal/dawmodel"

// convert.go bridges beatgen's rich Pattern to the canvas spine's
// dawmodel.DrumGrid (a thinner steps x lanes/velocity grid). The richer per-step
// properties beatgen carries (pitch/pan/probability/ratchet/locks, polymeter
// length, direction) have no home in DrumGrid, so ToDrumGrid is LOSSY for those —
// it flattens each lane to its On/Vel rows and resolves polymeter against the
// global step count so the grid stays rectangular. FromDrumGrid lifts a grid back
// into a Pattern with default rich properties. Together they let beatgen edits
// flow into and out of the existing canvas drum panel without modifying dawmodel.

// roleForNote maps a GM percussion note number to a beatgen role string, mirroring
// the dawmodel lane naming. Anything unknown becomes "perc".
func roleForNote(note int) string {
	switch note {
	case 35, 36:
		return "kick"
	case 38, 40:
		return "snare"
	case 37:
		return "rim"
	case 39:
		return "clap"
	case 42, 44:
		return "hat"
	case 46:
		return "ohat"
	case 49:
		return "crash"
	case 51:
		return "ride"
	case 41, 43, 45, 47, 48, 50:
		return "tom"
	default:
		return "perc"
	}
}

// noteForRole maps a beatgen role back to a representative GM percussion note (the
// inverse of roleForNote, picking the canonical note). Unknown roles default to a
// mid percussion note.
func noteForRole(role string) int {
	switch role {
	case "kick":
		return 36
	case "snare":
		return 38
	case "rim":
		return 37
	case "clap":
		return 39
	case "hat", "hihat":
		return 42
	case "ohat":
		return 46
	case "crash":
		return 49
	case "ride":
		return 51
	case "tom":
		return 45
	default:
		return 39
	}
}

// FromDrumGrid lifts a dawmodel.DrumGrid into a beatgen Pattern. Each grid lane
// becomes a beatgen lane (Role from the GM note, Forward direction, full length),
// each ON cell a Step with the grid's velocity and default rich properties. The
// global step count is the grid's total cells (Steps*Bars). A nil grid yields an
// empty pattern (degrade-never-crash).
func FromDrumGrid(g *dawmodel.DrumGrid) *Pattern {
	if g == nil {
		return &Pattern{}
	}
	cells := len(laneCellSpan(g))
	p := &Pattern{Steps: cells}
	for _, gl := range g.Lanes {
		ln := Lane{
			Name:      gl.Name,
			Role:      roleForNote(gl.Note),
			Direction: Forward,
			Steps:     make([]Step, len(gl.On)),
		}
		for i := range gl.On {
			if gl.On[i] {
				v := DefaultVelocity
				if i < len(gl.Vel) && gl.Vel[i] > 0 {
					v = clampVelocity(gl.Vel[i])
				}
				ln.Steps[i] = Step{
					On:          true,
					Velocity:    v,
					Probability: MaxProbability,
					Ratchet:     MinRatchet,
				}
			}
		}
		p.Lanes = append(p.Lanes, ln)
	}
	return p
}

// laneCellSpan returns a slice whose length is the grid's per-lane cell count
// (max On length across lanes, falling back to Steps*Bars). It exists so an
// inconsistent grid still produces a coherent global length.
func laneCellSpan(g *dawmodel.DrumGrid) []bool {
	n := g.Steps * g.Bars
	for _, gl := range g.Lanes {
		if len(gl.On) > n {
			n = len(gl.On)
		}
	}
	if n < 0 {
		n = 0
	}
	return make([]bool, n)
}

// ToDrumGrid flattens a beatgen Pattern into a dawmodel.DrumGrid over the global
// step count. Polymeter and direction are RESOLVED: for each global step the
// lane's StepAt is queried (using the pattern Seed), so a short/long or reversed
// lane is rendered as it would actually play across the grid. Probability and
// ratchet are NOT expanded here (the grid has no representation for them) — a step
// is ON in the grid iff its resolved Step is On; its grid velocity is the step
// velocity. The grid is sized to one global cell per step with sensible defaults
// (16 steps/bar, GM channel 9). A nil pattern yields an empty 1-bar grid.
func ToDrumGrid(p *Pattern) *dawmodel.DrumGrid {
	g := &dawmodel.DrumGrid{
		Steps:     dawmodel.DefaultSteps,
		Bars:      1,
		StepTicks: 0,
		Channel:   9,
	}
	if p == nil || p.Steps <= 0 {
		return g
	}
	cells := p.Steps
	g.Bars = (cells + dawmodel.DefaultSteps - 1) / dawmodel.DefaultSteps
	if g.Bars < 1 {
		g.Bars = 1
	}
	total := g.Steps * g.Bars
	for _, ln := range p.Lanes {
		gl := dawmodel.Lane{
			Name: ln.Name,
			Note: noteForRole(ln.Role),
			On:   make([]bool, total),
			Vel:  make([]int, total),
		}
		for gs := 0; gs < cells && gs < total; gs++ {
			st, ok := ln.StepAt(gs, p.Seed)
			if !ok || !st.On {
				continue
			}
			gl.On[gs] = true
			v := st.Velocity
			if v <= 0 {
				v = DefaultVelocity
			}
			gl.Vel[gs] = clampVelocity(v)
		}
		g.Lanes = append(g.Lanes, gl)
	}
	return g
}
