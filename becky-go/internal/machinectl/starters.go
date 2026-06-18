package machinectl

// starters.go — deterministic, built-in genre STARTER patterns. "make a trap
// beat" / "four on the floor" / "boom bap" write a small, fixed template onto the
// active pattern. These are intentionally FEW and HARD-CODED (a tiny step table
// per genre) — no genre DB is pulled in (that lives in becky-compose). The whole
// point is a fast, predictable "give me something to start from" that the producer
// then edits by hand.
//
// Each template is expressed as the set of ON step indices per pad (default GM kit
// layout: 0=kick, 1=snare, 2=closed hat, 3=open hat, 4=clap). A starter is applied
// over a fresh 16-step pattern so it always lands cleanly regardless of prior state.

import (
	"fmt"
	"strings"

	"becky-go/internal/drummachine"
)

// starter is one genre template: a display name plus per-pad ON step indices on a
// 16-step (one bar) grid. Pads not listed stay empty.
type starter struct {
	name  string
	steps map[int][]int // pad index → ON step indices (0..15)
}

// starters is the fixed table. Keep this SMALL and obvious — it is a launch pad,
// not a composition engine.
var starters = map[string]starter{
	"trap": {
		name: "Trap",
		steps: map[int][]int{
			0: {0, 6, 10},                             // kick
			1: {4, 12},                                // snare backbeat
			2: {0, 2, 3, 4, 6, 8, 10, 11, 12, 14, 15}, // rolling closed hats (incl. triplet-ish rolls)
		},
	},
	"boom-bap": {
		name: "Boom Bap",
		steps: map[int][]int{
			0: {0, 7, 10},                  // kick (classic boom-bap swing placement)
			1: {4, 12},                     // snare backbeat
			2: {0, 2, 4, 6, 8, 10, 12, 14}, // straight 8th closed hats
		},
	},
	"four-on-the-floor": {
		name: "Four on the Floor",
		steps: map[int][]int{
			0: {0, 4, 8, 12},  // kick on every quarter
			3: {2, 6, 10, 14}, // open hat on the off-beats
			4: {4, 12},        // clap backbeat
		},
	},
	"house": {
		name: "House",
		steps: map[int][]int{
			0: {0, 4, 8, 12},  // four-on-the-floor kick
			3: {2, 6, 10, 14}, // open hat off-beats
			1: {4, 12},        // snare/clap backbeat
		},
	},
}

// genreFrom recognises a genre-starter request and returns its canonical key, or
// "" when the instruction isn't a starter. It requires a "make/give me/start a …
// beat/groove/pattern" framing OR the unambiguous idiom "four on the floor" so a
// bare genre word doesn't hijack other intents.
func genreFrom(s string) string {
	makeFrame := containsAny(s, "make", "give me", "start", "create", "lay down", "drop", "build me", "i want", "new")
	beatFrame := containsAny(s, "beat", "groove", "pattern", "rhythm", "loop")

	// "four on the floor" is an idiom — recognise it with or without the frame.
	if containsAny(s, "four on the floor", "four-on-the-floor", "4 on the floor", "four to the floor") {
		return "four-on-the-floor"
	}
	if !(makeFrame && beatFrame) {
		// Also accept "make it trap"/"make a trap beat" handled by the frame above;
		// without a make+beat frame, don't claim a starter.
		if !(makeFrame && containsAny(s, "trap", "boom bap", "boom-bap", "house")) {
			return ""
		}
	}

	switch {
	case containsAny(s, "trap"):
		return "trap"
	case containsAny(s, "boom bap", "boom-bap", "boombap"):
		return "boom-bap"
	case containsAny(s, "house"):
		return "house"
	}
	return ""
}

// applyGenreStarter writes the named genre template onto the active pattern of a
// deep copy of m, returning the new machine and a plain-English summary. An
// unknown genre degrades to (copy, friendly note, error) so Apply reports it
// without crashing.
func applyGenreStarter(m *drummachine.Machine, genre string) (*drummachine.Machine, string, error) {
	st, ok := starters[strings.ToLower(strings.TrimSpace(genre))]
	if !ok {
		return cloneMachine(m), "I don't have a starter for that genre yet (I know trap, boom bap, house, and four on the floor).", fmt.Errorf("machinectl: unknown genre %q", genre)
	}

	pat := activePattern(m)
	out := cloneMachine(m)
	if !validPattern(out, pat) {
		return out, "There's no pattern to write into.", fmt.Errorf("machinectl: no active pattern")
	}

	p := &out.Bank.Patterns[pat]
	// Reset to a clean 16-step pattern so the starter lands deterministically
	// regardless of what was there before.
	*p = drummachine.PatternFromDrumGrid(p.ToDrumGrid(out.Kit), out.Kit, p.Name) // normalize shape
	clearPattern(p)
	ensureSteps(p, drummachine.DefaultSteps)

	for pad, ons := range st.steps {
		if pad < 0 || pad >= len(p.Lanes) {
			continue
		}
		for _, step := range ons {
			if step >= 0 && step < len(p.Lanes[pad]) {
				p.Lanes[pad][step] = drummachine.Step{On: true, Vel: 100}
			}
		}
	}

	return out, "Laid down a " + st.name + " starter — edit it however you like.", nil
}

// clearPattern turns every cell of every lane off (in place on a deep copy lane).
func clearPattern(p *drummachine.Pattern) {
	for li := range p.Lanes {
		for s := range p.Lanes[li] {
			p.Lanes[li][s] = drummachine.Step{}
		}
	}
}

// ensureSteps makes the pattern exactly n steps wide per lane (n is a valid step
// count). Used so a starter always writes onto a clean one-bar grid.
func ensureSteps(p *drummachine.Pattern, n int) {
	p.Steps = n
	for li := range p.Lanes {
		if len(p.Lanes[li]) == n {
			continue
		}
		ln := make([]drummachine.Step, n)
		copy(ln, p.Lanes[li])
		p.Lanes[li] = ln
	}
	// Guarantee PadCount lanes exist.
	for len(p.Lanes) < drummachine.PadCount {
		p.Lanes = append(p.Lanes, make([]drummachine.Step, n))
	}
}
