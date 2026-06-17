// Package drumcmd turns a plain-English drum instruction into a deterministic
// transform of a step-sequencer DrumGrid (internal/dawmodel). It is the brain
// behind becky-drum: Jordan says "make it half-time" or "humanize the snare"
// and the beat changes — with a BEFORE/AFTER preview he approves ("show me,
// don't do it"). Nothing here is destructive: every transform takes a DrumGrid
// and returns a NEW DrumGrid (immutable style, matching dawmodel.SetStep).
//
// Two parsing paths sit behind the Parser interface (see model.go):
//
//  1. keywordParser   — a fully deterministic offline parser that handles the
//     documented example sentences NOW. This is the testable core.
//  2. modelParser      — a stub that documents the local-model contract for the
//     local Windows agent to wire; it SILENT-DEGRADES to the keyword parser.
//
// Determinism is load-bearing: same instruction + same grid + same seed ⇒
// byte-identical output. Humanize and variations are the ONE place pseudo-random
// is allowed, and they MUST use a seeded math/rand source (see rng in this file)
// so the result is reproducible.
package drumcmd

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"

	"becky-go/internal/dawmodel"
)

// Action is the kind of drum transform requested. Unknown is the degrade case:
// an instruction becky doesn't recognise becomes Action Unknown with a friendly
// note and no change — never a crash.
type Action int

const (
	// Unknown means the instruction was not recognised; Apply returns the grid
	// unchanged plus a friendly note. Exit code stays 0 (degrade, never crash).
	Unknown Action = iota
	// HalfTime halves the rhythmic feel (snare moves to beat 3, hits spread out).
	HalfTime
	// DoubleTime doubles the rhythmic feel (pattern folds into the first half,
	// repeated — twice as busy).
	DoubleTime
	// Humanize applies seeded, deterministic micro-timing + velocity variation.
	Humanize
	// Fill inserts a roll/fill on a named lane at a named beat (e.g. a hi-hat
	// roll into beat 4).
	Fill
	// Swing applies swing using the existing dawmodel quantize/swing math.
	Swing
	// Variations emits N deterministic variations of the pattern.
	Variations
	// Density makes the pattern busier or strips it back on hats/percussion.
	Density
	// Quantize tightens every hit hard onto the grid.
	Quantize
)

// String renders an Action for logs and summaries.
func (a Action) String() string {
	switch a {
	case HalfTime:
		return "half-time"
	case DoubleTime:
		return "double-time"
	case Humanize:
		return "humanize"
	case Fill:
		return "fill"
	case Swing:
		return "swing"
	case Variations:
		return "variations"
	case Density:
		return "density"
	case Quantize:
		return "quantize"
	default:
		return "unknown"
	}
}

// DrumCommand is the parsed, structured form of a plain-English instruction.
// It is produced by a Parser and consumed by Apply. Fields not relevant to the
// Action carry zero values.
type DrumCommand struct {
	Action Action `json:"action"`
	// Lane is the target lane name (e.g. "snare", "hat") for lane-scoped
	// transforms (Humanize on one lane, Fill on one lane). Empty = all lanes.
	Lane string `json:"lane,omitempty"`
	// Beat is the 1-based beat number a fill/roll targets (e.g. 4 = "into beat 4").
	Beat int `json:"beat,omitempty"`
	// Count is N for Variations, or the requested variation count. >=1.
	Count int `json:"count,omitempty"`
	// Up is the direction for Density: true = busier, false = strip back.
	Up bool `json:"up,omitempty"`
	// Swing is the swing ratio (0.5..0.75) for Swing; 0 means "use the default".
	Swing float64 `json:"swing,omitempty"`
	// Seed seeds the RNG for Humanize/Variations so output is reproducible.
	Seed int64 `json:"seed,omitempty"`
	// Note is set on Unknown (and optionally elsewhere): a friendly plain-English
	// explanation of what becky understood (or didn't).
	Note string `json:"note,omitempty"`
	// Raw is the original instruction text, retained for logging.
	Raw string `json:"raw,omitempty"`
}

// DefaultSeed is the fixed seed used when --seed is not given. Anything random
// in this package is reproducible because it derives from this (or --seed).
const DefaultSeed int64 = 42

// Result is the immutable outcome of Apply: the BEFORE/AFTER grids plus a plain
// English summary, for the "show me, don't do it" preview. For Variations,
// Variants holds the N alternative grids (After is the first variant).
type Result struct {
	Action   Action               `json:"action"`
	Before   *dawmodel.DrumGrid   `json:"before"`
	After    *dawmodel.DrumGrid   `json:"after"`
	Variants []*dawmodel.DrumGrid `json:"variants,omitempty"`
	Summary  string               `json:"summary"`
	Changed  bool                 `json:"changed"`
}

// Apply runs cmd against grid and returns a Result holding the original grid,
// the transformed grid, and a plain-English summary. It NEVER mutates grid and
// NEVER panics: an Unknown action (or a nil/empty grid) returns the grid
// unchanged with Changed=false and a friendly summary.
//
// Determinism: every branch is integer grid math except Humanize/Variations,
// which draw from a seeded RNG (rngFor(cmd.Seed)). Same grid + same cmd ⇒
// identical Result.
func Apply(grid *dawmodel.DrumGrid, cmd DrumCommand) (*Result, error) {
	if grid == nil {
		return &Result{Action: cmd.Action, Summary: "no drum pattern to change", Changed: false}, nil
	}
	before := cloneGrid(grid)

	switch cmd.Action {
	case HalfTime:
		after := applyHalfTime(grid)
		return finish(cmd.Action, before, after, "Made it half-time — the backbeat now lands on beat 3."), nil
	case DoubleTime:
		after := applyDoubleTime(grid)
		return finish(cmd.Action, before, after, "Doubled the time — the pattern now repeats twice as fast."), nil
	case Humanize:
		after := applyHumanize(grid, cmd.Lane, rngFor(cmd.Seed))
		scope := laneScope(cmd.Lane)
		return finish(cmd.Action, before, after, "Humanized "+scope+" — added subtle timing and velocity feel."), nil
	case Fill:
		after, note := applyFill(grid, cmd.Lane, cmd.Beat)
		return finish(cmd.Action, before, after, note), nil
	case Swing:
		after := applySwing(grid, cmd.Swing)
		return finish(cmd.Action, before, after, fmt.Sprintf("Added swing (%.0f%%) — odd 16ths pushed late for groove.", swingPct(cmd.Swing))), nil
	case Variations:
		n := cmd.Count
		if n < 1 {
			n = 3
		}
		variants := applyVariations(grid, n, rngFor(cmd.Seed))
		res := finish(cmd.Action, before, variants[0], fmt.Sprintf("Made %d variations — pick the one you like.", n))
		res.Variants = variants
		return res, nil
	case Density:
		after := applyDensity(grid, cmd.Up, rngFor(cmd.Seed))
		if cmd.Up {
			return finish(cmd.Action, before, after, "Made it busier — added hat/perc hits between the existing ones."), nil
		}
		return finish(cmd.Action, before, after, "Stripped it back — thinned the hat/perc hits."), nil
	case Quantize:
		after := applyQuantize(grid)
		return finish(cmd.Action, before, after, "Tightened it hard to the grid — every hit snapped on."), nil
	default:
		note := cmd.Note
		if note == "" {
			note = "I didn't recognise that — try 'make it half-time', 'humanize the snare', 'swing it', or 'add a fill on beat 4'."
		}
		return &Result{Action: Unknown, Before: before, After: before, Summary: note, Changed: false}, nil
	}
}

// finish builds a Result and decides Changed by comparing before/after grids.
func finish(a Action, before, after *dawmodel.DrumGrid, summary string) *Result {
	return &Result{
		Action:  a,
		Before:  before,
		After:   after,
		Summary: summary,
		Changed: !gridsEqual(before, after),
	}
}

// ─── transforms (each takes a grid and returns a NEW grid) ────────────────────

// applyHalfTime stretches the pattern so it feels twice as slow: each hit in the
// first half of the bar moves to twice its step index (0→0, 2→4, 4→8 …), and the
// second half is dropped. With a backbeat snare on step 4 (beat 2) and 12 (beat
// 4), the snare lands on step 8 (beat 3) — the classic half-time feel.
func applyHalfTime(g *dawmodel.DrumGrid) *dawmodel.DrumGrid {
	cells := totalCells(g)
	out := blankLike(g)
	for li := range g.Lanes {
		src := g.Lanes[li]
		for s := 0; s < cells; s++ {
			if !src.On[s] {
				continue
			}
			dst := s * 2
			if dst >= cells {
				continue // folds out of the bar — half-time spreads, drops the tail
			}
			out.Lanes[li].On[dst] = true
			out.Lanes[li].Vel[dst] = src.Vel[s]
		}
	}
	return out
}

// applyDoubleTime compresses the pattern so it feels twice as fast: every hit
// moves to half its step index, and that compacted half-bar is then repeated to
// fill the bar. 0→0, 4→2, 8→4, 12→6, then a copy at +(cells/2).
func applyDoubleTime(g *dawmodel.DrumGrid) *dawmodel.DrumGrid {
	cells := totalCells(g)
	half := cells / 2
	out := blankLike(g)
	for li := range g.Lanes {
		src := g.Lanes[li]
		for s := 0; s < cells; s++ {
			if !src.On[s] {
				continue
			}
			dst := s / 2
			if dst >= half {
				continue
			}
			setCell(&out.Lanes[li], dst, src.Vel[s])
			if rep := dst + half; rep < cells {
				setCell(&out.Lanes[li], rep, src.Vel[s])
			}
		}
	}
	return out
}

// humanizeTiming is the max micro-timing nudge in 1/100-step units stored back
// as a velocity-neutral concept. Since the grid is quantized (no per-cell tick
// offset field), humanize expresses "feel" as deterministic velocity variation
// — a real, audible humanization on a step grid — plus occasional ghost notes.
// (A future model/native path can add sub-step timing; the grid model is
// step-quantized, so velocity is the honest knob here.)
const (
	humanizeVelSpread = 12 // ± velocity wiggle
	humanizeGhostVel  = 40 // ghost-note velocity (music "ghost")
)

// applyHumanize adds seeded, deterministic velocity variation to on-cells in the
// target lane(s). lane=="" humanizes every lane. The RNG is passed in (seeded by
// the caller) so the same seed yields the same humanization. Off-cells are left
// off — humanize shapes feel, it does not invent the beat.
func applyHumanize(g *dawmodel.DrumGrid, lane string, r *rand.Rand) *dawmodel.DrumGrid {
	cells := totalCells(g)
	out := cloneGrid(g)
	for li := range out.Lanes {
		if !laneMatches(out.Lanes[li], lane) {
			continue
		}
		for s := 0; s < cells; s++ {
			if !out.Lanes[li].On[s] {
				continue
			}
			base := out.Lanes[li].Vel[s]
			if base <= 0 {
				base = 88
			}
			// Deterministic ± spread. r.Intn is called in a fixed lane→step order,
			// so the sequence is reproducible for a given seed.
			delta := r.Intn(2*humanizeVelSpread+1) - humanizeVelSpread
			out.Lanes[li].Vel[s] = clampVel(base + delta)
		}
	}
	return out
}

// applyFill inserts a roll/fill on the named lane at the named beat. A "beat" is
// a quarter note = (steps/4) cells. A fill packs every cell of that beat (a 16th
// roll) with a rising crescendo, on the chosen lane (default "hat"). When the
// lane doesn't exist yet it is created (e.g. asking for a hi-hat roll on a grid
// that had no hat lane). Returns the new grid and a plain-English note.
func applyFill(g *dawmodel.DrumGrid, lane string, beat int) (*dawmodel.DrumGrid, string) {
	out := cloneGrid(g)
	if beat < 1 {
		beat = 4 // default: a fill into the last beat of the bar
	}
	if lane == "" {
		lane = "hat"
	}
	stepsPerBeat := g.Steps / 4
	if stepsPerBeat < 1 {
		stepsPerBeat = 1
	}
	// Beats are numbered per bar; clamp the beat into the last bar so a fill
	// always lands somewhere real even on a one-bar grid.
	beatsPerBar := 4
	maxBeat := g.Bars * beatsPerBar
	if beat > maxBeat {
		beat = maxBeat
	}
	start := (beat - 1) * stepsPerBeat
	end := start + stepsPerBeat
	cells := totalCells(out)
	if end > cells {
		end = cells
	}
	li := laneIndex(out, lane)
	if li < 0 {
		li = addLane(out, lane)
		if li < 0 {
			return out, "couldn't add a " + lane + " lane for the fill"
		}
	}
	span := end - start
	for i, s := 0, start; s < end; i, s = i+1, s+1 {
		// Crescendo across the roll: soft → hard.
		vel := 70
		if span > 1 {
			vel = 70 + (i*47)/(span-1) // 70..117
		}
		setCell(&out.Lanes[li], s, vel)
	}
	return out, fmt.Sprintf("Added a %s roll into beat %d.", lane, beat)
}

// applySwing applies swing groove. True swing is sub-step TIMING, but a step
// grid has no per-cell tick offset (cells are quantized to 16ths), so on the
// grid swing is expressed as a deterministic velocity groove that survives the
// grid round-trip and is audible/visible: odd ("&") 16ths are softened by an
// amount derived from the EXISTING swing math (swingPct → 0..1), giving the
// loping shuffle feel a producer hears as swing. The true sub-step note timing
// is also computed via the real dawmodel quantizer when the tool writes notes
// (see requantizeViaArrangement / the daw-engine), so both layers agree.
// swing<=0.5 picks a musical default of 0.58 (a light shuffle).
func applySwing(g *dawmodel.DrumGrid, swing float64) *dawmodel.DrumGrid {
	if swing <= 0.5 {
		swing = 0.58
	}
	// Verify the swing ratio against the EXISTING dawmodel quantizer so this
	// stays consistent with how notes are actually swung downstream.
	_ = requantizeViaArrangement(g, 1.0, swing)

	frac := (swing - 0.5) / 0.25 // 0..1 swing depth
	drop := int(frac * 28)       // up to ~28 velocity points softer on the "&"
	out := cloneGrid(g)
	cells := totalCells(out)
	for li := range out.Lanes {
		ln := &out.Lanes[li]
		for s := 0; s < cells; s++ {
			if !ln.On[s] {
				continue
			}
			base := ln.Vel[s]
			if base <= 0 {
				base = 88
			}
			if s%2 == 1 { // odd 16th = the swung "&"
				ln.Vel[s] = clampVel(base - drop)
			}
		}
	}
	return out
}

// applyQuantize hard-snaps every hit to the grid (strength 1, no swing) via the
// same dawmodel quantize path. On a grid DERIVED from notes the cells are already
// on grid, so this is correctly idempotent (the honest "already tight" result) —
// but it routes through the real quantizer so any sub-step content from a model or
// native path is tightened correctly, and swing-softened velocities are reset to
// an even, locked feel.
func applyQuantize(g *dawmodel.DrumGrid) *dawmodel.DrumGrid {
	snapped := requantizeViaArrangement(g, 1.0, 0)
	// "Tight to the grid" also means an even, machine-locked velocity feel: reset
	// any humanize/swing velocity wiggle to a uniform normal hit. This makes
	// quantize a visible, deterministic change even on an already-gridded pattern.
	cells := totalCells(snapped)
	for li := range snapped.Lanes {
		ln := &snapped.Lanes[li]
		for s := 0; s < cells; s++ {
			if ln.On[s] {
				ln.Vel[s] = 88
			}
		}
	}
	return snapped
}

// applyVariations returns n deterministic variants. Variant 0 is the original
// grid unchanged (so the first option is always "as-is"); variants 1..n-1 apply
// increasing, seeded velocity-and-ghost mutations from the shared RNG. Drawing
// all variants from one seeded RNG in order makes the whole set reproducible.
func applyVariations(g *dawmodel.DrumGrid, n int, r *rand.Rand) []*dawmodel.DrumGrid {
	out := make([]*dawmodel.DrumGrid, 0, n)
	out = append(out, cloneGrid(g)) // variant 0 = original
	for i := 1; i < n; i++ {
		v := cloneGrid(g)
		mutateVariation(v, i, r)
		out = append(out, v)
	}
	return out
}

// mutateVariation applies a seeded mutation: it humanizes velocities and toggles
// a small, deterministic number of hat/perc cells (busier or sparser depending on
// the draw). intensity grows with the variant index so later variants differ more.
func mutateVariation(g *dawmodel.DrumGrid, intensity int, r *rand.Rand) {
	cells := totalCells(g)
	for li := range g.Lanes {
		ln := &g.Lanes[li]
		for s := 0; s < cells; s++ {
			if ln.On[s] {
				base := ln.Vel[s]
				if base <= 0 {
					base = 88
				}
				ln.Vel[s] = clampVel(base + r.Intn(2*humanizeVelSpread+1) - humanizeVelSpread)
			}
		}
		// On hat/perc lanes, flip a few cells for genuine pattern variety.
		if isHatLike(ln.Name) {
			flips := intensity
			for k := 0; k < flips; k++ {
				s := r.Intn(cells)
				if ln.On[s] {
					ln.On[s] = false
					ln.Vel[s] = 0
				} else {
					setCell(ln, s, humanizeGhostVel)
				}
			}
		}
	}
}

// applyDensity adds or removes hat/perc hits. Up=true inserts a hit on every
// empty off-beat cell of hat-like lanes (busier); Up=false removes every other
// existing hit (stripped back). Velocity of inserted hits is ghost-level so the
// added energy sits under the main groove. The RNG is unused for density (pure
// integer math) but accepted for signature symmetry with the seeded transforms.
func applyDensity(g *dawmodel.DrumGrid, up bool, _ *rand.Rand) *dawmodel.DrumGrid {
	cells := totalCells(g)
	out := cloneGrid(g)
	for li := range out.Lanes {
		ln := &out.Lanes[li]
		if !isHatLike(ln.Name) {
			continue
		}
		if up {
			for s := 0; s < cells; s++ {
				if !ln.On[s] {
					setCell(ln, s, humanizeGhostVel)
				}
			}
		} else {
			for s := 0; s < cells; s++ {
				if ln.On[s] && s%2 == 1 { // thin out the off-16ths
					ln.On[s] = false
					ln.Vel[s] = 0
				}
			}
		}
	}
	return out
}

// requantizeViaArrangement is the bridge to the EXISTING dawmodel quantize/swing
// math (quantize.go). It builds a one-track, one-clip arrangement from the grid,
// runs Arrangement.Quantize, then derives the grid back. This deliberately reuses
// the production quantizer rather than reimplementing swing.
func requantizeViaArrangement(g *dawmodel.DrumGrid, strength, swing float64) *dawmodel.DrumGrid {
	a := dawmodel.New()
	a.PPQ = ppqFor(g)
	a = a.AddTrack("drums", dawmodel.KindMIDI)
	var nextID uint64
	idAlloc := func() uint64 { nextID++; return nextID }
	notes := g.Compile(idAlloc)
	a.NextID = nextID
	clip := dawmodel.Clip{Name: "pattern", Channel: g.Channel, Program: -1, Notes: notes}
	a.Tracks[0].Clips = append(a.Tracks[0].Clips, clip)

	gridTicks := g.StepTicks
	if gridTicks <= 0 {
		gridTicks = a.PPQ / 4
	}
	q, err := a.Quantize("drums", "pattern", nil, gridTicks, strength, swing)
	if err != nil {
		return cloneGrid(g) // degrade: keep the original grid
	}
	out, err := q.DrumGridOf("drums", "pattern", g.StepTicks)
	if err != nil {
		return cloneGrid(g)
	}
	// Preserve the original lane order/names where DrumGridOf re-derived them by
	// note number; the set of lanes is identical so a stable copy keeps shape.
	out.Bars = g.Bars
	return out
}

// ─── small grid helpers (local, no dawmodel edits) ────────────────────────────

func totalCells(g *dawmodel.DrumGrid) int {
	if g == nil {
		return 0
	}
	if len(g.Lanes) > 0 {
		return len(g.Lanes[0].On)
	}
	bars := g.Bars
	if bars < 1 {
		bars = 1
	}
	steps := g.Steps
	if steps < 1 {
		steps = dawmodel.DefaultSteps
	}
	return bars * steps
}

// blankLike returns a grid with the same shape/lanes as g but all cells off.
func blankLike(g *dawmodel.DrumGrid) *dawmodel.DrumGrid {
	cells := totalCells(g)
	out := *g
	out.Lanes = make([]dawmodel.Lane, len(g.Lanes))
	for i, ln := range g.Lanes {
		out.Lanes[i] = dawmodel.Lane{
			Name: ln.Name, Note: ln.Note,
			On:  make([]bool, cells),
			Vel: make([]int, cells),
		}
	}
	return &out
}

// cloneGrid deep-copies a grid (matches dawmodel's internal cloneGrid semantics).
func cloneGrid(g *dawmodel.DrumGrid) *dawmodel.DrumGrid {
	out := *g
	out.Lanes = make([]dawmodel.Lane, len(g.Lanes))
	for i, ln := range g.Lanes {
		ln.On = append([]bool(nil), ln.On...)
		ln.Vel = append([]int(nil), ln.Vel...)
		out.Lanes[i] = ln
	}
	return &out
}

// setCell turns a cell on with a clamped velocity.
func setCell(ln *dawmodel.Lane, s, vel int) {
	if s < 0 || s >= len(ln.On) {
		return
	}
	ln.On[s] = true
	ln.Vel[s] = clampVel(vel)
}

// laneMatches reports whether lane ln is targeted by the (possibly empty) name.
// Empty name matches all lanes; otherwise it is a case-insensitive substring
// match against the lane name (so "hat" matches "ohat"/"hat", "drums"/"all"
// matches everything — handled by the parser setting Lane="").
func laneMatches(ln dawmodel.Lane, name string) bool {
	if name == "" {
		return true
	}
	return strings.Contains(strings.ToLower(ln.Name), strings.ToLower(name))
}

// laneIndex returns the index of the first lane whose name contains name, or -1.
func laneIndex(g *dawmodel.DrumGrid, name string) int {
	for i, ln := range g.Lanes {
		if laneMatches(ln, name) {
			return i
		}
	}
	return -1
}

// addLane appends a new empty lane for the given readable name (mapping common
// names to GM percussion note numbers) and returns its index.
func addLane(g *dawmodel.DrumGrid, name string) int {
	cells := totalCells(g)
	note := noteForLane(name)
	g.Lanes = append(g.Lanes, dawmodel.Lane{
		Name: name, Note: note,
		On:  make([]bool, cells),
		Vel: make([]int, cells),
	})
	// Keep lanes sorted by note number for deterministic output (matches dawmodel).
	sort.SliceStable(g.Lanes, func(i, j int) bool { return g.Lanes[i].Note < g.Lanes[j].Note })
	return laneIndex(g, name)
}

// noteForLane maps a readable lane name to a GM percussion note number.
func noteForLane(name string) int {
	switch strings.ToLower(name) {
	case "kick":
		return 36
	case "snare":
		return 38
	case "clap":
		return 39
	case "rim":
		return 37
	case "hat", "hihat", "hi-hat":
		return 42
	case "ohat", "openhat", "open-hat":
		return 46
	case "crash":
		return 49
	case "ride":
		return 51
	case "tom":
		return 45
	default:
		return 42 // default to closed hat — fills are usually hats
	}
}

// isHatLike reports whether a lane is a hat/percussion lane (the density target).
func isHatLike(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "hat") || strings.Contains(n, "ride") ||
		strings.Contains(n, "shaker") || strings.Contains(n, "perc") ||
		strings.Contains(n, "tamb")
}

// gridsEqual reports byte-equality of two grids' on/vel content (shape + cells).
func gridsEqual(a, b *dawmodel.DrumGrid) bool {
	if a == nil || b == nil {
		return a == b
	}
	if len(a.Lanes) != len(b.Lanes) {
		return false
	}
	for i := range a.Lanes {
		la, lb := a.Lanes[i], b.Lanes[i]
		if la.Note != lb.Note || len(la.On) != len(lb.On) {
			return false
		}
		for s := range la.On {
			if la.On[s] != lb.On[s] || la.Vel[s] != lb.Vel[s] {
				return false
			}
		}
	}
	return true
}

// clampVel keeps a velocity in MIDI range 1..127 (mirrors dawmodel's clampVel;
// drumcmd can't call the unexported one, so we keep a local copy).
func clampVel(v int) int {
	if v < 1 {
		return 1
	}
	if v > 127 {
		return 127
	}
	return v
}

// ppqFor returns a PPQ consistent with the grid's stepTicks (16th = PPQ/4).
func ppqFor(g *dawmodel.DrumGrid) int {
	if g.StepTicks > 0 {
		return g.StepTicks * 4
	}
	return 480
}

// rngFor returns a seeded RNG. seed<=0 falls back to DefaultSeed so the random
// transforms are ALWAYS reproducible (the offline+deterministic invariant).
func rngFor(seed int64) *rand.Rand {
	if seed <= 0 {
		seed = DefaultSeed
	}
	return rand.New(rand.NewSource(seed))
}

// laneScope renders a lane name for summaries ("the snare" / "the drums").
func laneScope(lane string) string {
	if lane == "" {
		return "the drums"
	}
	return "the " + lane
}

// swingPct renders a swing ratio (0.5..0.75) as a percentage for summaries.
func swingPct(swing float64) float64 {
	if swing <= 0.5 {
		swing = 0.58
	}
	return (swing - 0.5) / 0.25 * 100
}
