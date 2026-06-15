package dawmodel

import "fmt"

// quantize.go is the one Quantize implementation both the piano roll and the drum
// grid call (SPEC §2.4). It is fully DETERMINISTIC: integer grid math, no RNG, so
// the same (grid, strength, swing, notes) always yields the same result.
//
//   - grid     : the quantize lattice in ticks (e.g. music.StepTicks = a 1/16).
//   - strength : 0..1; the fraction a note moves toward its grid line. strength=1
//     is a hard snap (Cubase "Use Quantize"); 0<strength<1 is iterative quantize.
//   - swing    : 0.5..0.75; delays odd grid cells by (swing-0.5)*2*grid, matching
//     the generator's swing so an edited beat keeps the genre feel.

// Quantize moves the given notes' starts toward the grid (optionally swung). When
// ids is empty, every note in the clip is quantized. Returns the new arrangement.
// Any start that actually changed is logged as a Correction so becky learns how
// hard Jordan likes the grid pulled.
func (a *Arrangement) Quantize(trackID, clipName string, ids []uint64, grid int, strength, swing float64) (*Arrangement, error) {
	out := a.clone()
	_, c := out.findClip(trackID, clipName)
	if c == nil {
		return a, fmt.Errorf("quantize: clip %q/%q not found", trackID, clipName)
	}
	if grid <= 0 {
		return a, fmt.Errorf("quantize: grid must be > 0, got %d", grid)
	}
	strength = clampUnit(strength)
	swingTicks := swingOffset(grid, swing)
	all := len(ids) == 0
	sel := idSet(ids)
	for i := range c.Notes {
		if !all && !sel[c.Notes[i].ID] {
			continue
		}
		old := c.Notes[i].Start
		c.Notes[i].Start = quantizeTick(old, grid, strength, swingTicks)
		if c.Notes[i].Start != old {
			out.logCorrection("quantize", clipName, old,
				fmt.Sprintf("%d", old), fmt.Sprintf("%d", c.Notes[i].Start))
		}
	}
	sortNotes(c.Notes)
	return out, nil
}

// quantizeTick is the pure per-note quantizer. It snaps tick toward the nearest
// grid line by strength, then adds swing on odd cells. Deterministic integer math
// (round-half-up via +grid/2) so results never drift.
func quantizeTick(tick, grid int, strength float64, swingTicks int) int {
	cell := divRound(tick, grid)
	target := cell*grid + cellSwing(cell, swingTicks)
	moved := float64(tick) + strength*float64(target-tick)
	out := int(moved + 0.5)
	if out < 0 {
		out = 0
	}
	return out
}

// swingOffset converts a swing ratio (0.5..0.75) into a tick delay for odd cells,
// mirroring the generator's (swing-0.5)*2*StepTicks math.
func swingOffset(grid int, swing float64) int {
	if swing <= 0.5 {
		return 0
	}
	if swing > 0.75 {
		swing = 0.75
	}
	return int((swing-0.5)*2*float64(grid) + 0.5)
}

// cellSwing returns the swing delay applied to a grid cell (odd cells only).
func cellSwing(cell, swingTicks int) int {
	if swingTicks != 0 && cell%2 == 1 {
		return swingTicks
	}
	return 0
}

// divRound divides n by d rounding to the nearest integer (d>0).
func divRound(n, d int) int {
	if d <= 0 {
		return n
	}
	if n >= 0 {
		return (n + d/2) / d
	}
	return -((-n + d/2) / d)
}

func clampUnit(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}
