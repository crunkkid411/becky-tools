package pianoroll

// quantize.go is the piano-roll quantizer. It is fully DETERMINISTIC — integer
// grid math, no RNG — so the same (grid, strength, swing, notes) always yields the
// same result. The math mirrors internal/dawmodel.quantize exactly (the one
// quantizer the drum grid already uses) so an edited line keeps the same feel the
// generator produced; it is reproduced here (not imported) because dawmodel's
// quantizer is bound to dawmodel.Note/Arrangement, whereas this operates on the
// standalone clip model. Cross-checked against dawmodel in quantize_test.go.
//
//   - grid     : the quantize lattice in ticks (e.g. PPQ/4 = a 1/16).
//   - strength : 0..1; the fraction a note moves toward its grid line. strength=1
//     is a hard snap (Cubase "Use Quantize"); 0<strength<1 is iterative quantize.
//   - swing    : 0.5..0.75; delays odd grid cells by (swing-0.5)*2*grid, matching
//     the generator's swing so the groove survives editing.

// Quantize moves the selected notes' starts toward the grid (optionally swung).
// When indices is empty, every note is quantized. Returns the new clip. A grid<=0
// is a no-op (returns a clone) — degrade, never crash.
func (c *Clip) Quantize(indices []int, grid int, strength, swing float64) *Clip {
	if grid <= 0 {
		return c.clone()
	}
	strength = clampUnit(strength)
	swingTicks := swingOffset(grid, swing)
	all := len(indices) == 0
	sel := c.selected(indices)
	notes := append([]Note(nil), c.Notes...)
	for i := range notes {
		if !all && !sel[i] {
			continue
		}
		notes[i].Start = quantizeTick(notes[i].Start, grid, strength, swingTicks)
	}
	return c.withNotes(notes)
}

// QuantizeEnds snaps the END of each selected note (its Start+Length) to the grid
// with the given strength, adjusting Length so the start is unchanged. Useful for
// tightening note lengths to the grid ("snap releases to 1/16"). Length stays >=1.
func (c *Clip) QuantizeEnds(indices []int, grid int, strength float64) *Clip {
	if grid <= 0 {
		return c.clone()
	}
	strength = clampUnit(strength)
	all := len(indices) == 0
	sel := c.selected(indices)
	notes := append([]Note(nil), c.Notes...)
	for i := range notes {
		if !all && !sel[i] {
			continue
		}
		end := quantizeTick(notes[i].End(), grid, strength, 0)
		notes[i].Length = maxInt(end-notes[i].Start, 1)
	}
	return c.withNotes(notes)
}

// quantizeTick is the pure per-note quantizer (identical to dawmodel's): snap tick
// toward the nearest grid line by strength, then add swing on odd cells. Integer
// round-half-up so results never drift.
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
// mirroring the generator's (swing-0.5)*2*grid math.
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
