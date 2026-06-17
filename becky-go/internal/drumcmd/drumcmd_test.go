package drumcmd

import (
	"reflect"
	"testing"

	"becky-go/internal/dawmodel"
)

// ─── test fixtures ────────────────────────────────────────────────────────────

// fixtureGrid builds a canonical 1-bar 16-step beat: kick on 0/8, backbeat snare
// on 4/12, straight 8th hats. The classic pattern every transform is tested on.
func fixtureGrid() *dawmodel.DrumGrid {
	cells := 16
	mk := func(name string, note int, steps ...int) dawmodel.Lane {
		ln := dawmodel.Lane{Name: name, Note: note, On: make([]bool, cells), Vel: make([]int, cells)}
		for _, s := range steps {
			ln.On[s] = true
			ln.Vel[s] = 88
		}
		return ln
	}
	return &dawmodel.DrumGrid{
		Steps: 16, Bars: 1, StepTicks: 120, Channel: 9,
		Lanes: []dawmodel.Lane{
			mk("kick", 36, 0, 8),
			mk("snare", 38, 4, 12),
			mk("hat", 42, 0, 2, 4, 6, 8, 10, 12, 14),
		},
	}
}

func onSteps(ln dawmodel.Lane) []int {
	var out []int
	for s, on := range ln.On {
		if on {
			out = append(out, s)
		}
	}
	return out
}

func laneByName(g *dawmodel.DrumGrid, name string) (dawmodel.Lane, bool) {
	for _, ln := range g.Lanes {
		if ln.Name == name {
			return ln, true
		}
	}
	return dawmodel.Lane{}, false
}

// ─── parser: each example sentence → correct DrumCommand ──────────────────────

func TestParseKeyword_examples(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    Action
		lane    string
		beat    int
		count   int
		up      bool
		upCheck bool // when true, assert Up == up
	}{
		{name: "half-time", in: "make it half-time", want: HalfTime},
		{name: "half time spaced", in: "can you make it half time please", want: HalfTime},
		{name: "double-time", in: "double-time it", want: DoubleTime},
		{name: "double time spaced", in: "double time the whole thing", want: DoubleTime},
		{name: "humanize snare", in: "humanize the snare", want: Humanize, lane: "snare"},
		{name: "humanise spelling", in: "humanise the hats", want: Humanize, lane: "hat"},
		{name: "humanize drums = all", in: "humanize the drums", want: Humanize, lane: ""},
		{name: "hat roll into beat 4", in: "add a hi-hat roll into beat 4", want: Fill, lane: "hat", beat: 4},
		{name: "plain fill", in: "add a fill", want: Fill, lane: "", beat: 0},
		{name: "snare fill beat 2", in: "put a snare fill on beat 2", want: Fill, lane: "snare", beat: 2},
		{name: "swing it", in: "swing it", want: Swing},
		{name: "more swing", in: "more swing", want: Swing},
		{name: "3 variations digit", in: "give me 3 variations", want: Variations, count: 3},
		{name: "five variations word", in: "give me five variations", want: Variations, count: 5},
		{name: "bare variations", in: "show me some variations", want: Variations, count: 3},
		{name: "busier", in: "make it busier", want: Density, up: true, upCheck: true},
		{name: "strip back", in: "strip it back", want: Density, up: false, upCheck: true},
		{name: "tighten grid", in: "tighten it to the grid", want: Quantize},
		{name: "quantize word", in: "quantize the drums", want: Quantize},
		{name: "unknown", in: "make it sound purple", want: Unknown},
		{name: "empty", in: "   ", want: Unknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := ParseKeyword(tt.in, DefaultSeed)
			if cmd.Action != tt.want {
				t.Fatalf("ParseKeyword(%q) action = %v, want %v", tt.in, cmd.Action, tt.want)
			}
			if tt.want == Humanize || tt.want == Fill {
				if cmd.Lane != tt.lane {
					t.Errorf("lane = %q, want %q", cmd.Lane, tt.lane)
				}
			}
			if tt.want == Fill && tt.beat != cmd.Beat {
				t.Errorf("beat = %d, want %d", cmd.Beat, tt.beat)
			}
			if tt.want == Variations && cmd.Count != tt.count {
				t.Errorf("count = %d, want %d", cmd.Count, tt.count)
			}
			if tt.upCheck && cmd.Up != tt.up {
				t.Errorf("up = %v, want %v", cmd.Up, tt.up)
			}
			if cmd.Seed != DefaultSeed {
				t.Errorf("seed not stamped: got %d", cmd.Seed)
			}
		})
	}
}

func TestParseKeyword_unknownHasFriendlyNote(t *testing.T) {
	cmd := ParseKeyword("turn it into a banana", DefaultSeed)
	if cmd.Action != Unknown {
		t.Fatalf("want Unknown, got %v", cmd.Action)
	}
	if cmd.Note == "" {
		t.Error("Unknown command should carry a friendly note")
	}
}

func TestParseKeyword_seedDefaulted(t *testing.T) {
	cmd := ParseKeyword("humanize the drums", 0)
	if cmd.Seed != DefaultSeed {
		t.Errorf("seed 0 should default to %d, got %d", DefaultSeed, cmd.Seed)
	}
}

// ─── Apply: each transform produces the expected grid change ──────────────────

func TestApply_halfTime(t *testing.T) {
	g := fixtureGrid()
	res, err := Apply(g, DrumCommand{Action: HalfTime})
	if err != nil {
		t.Fatal(err)
	}
	snare, _ := laneByName(res.After, "snare")
	// Backbeat snare on 4,12 spreads to 8 (12*2=24 falls out of the bar, dropped).
	if got := onSteps(snare); !reflect.DeepEqual(got, []int{8}) {
		t.Errorf("half-time snare = %v, want [8] (lands on beat 3)", got)
	}
	if !res.Changed {
		t.Error("half-time should change the grid")
	}
}

func TestApply_doubleTime(t *testing.T) {
	g := fixtureGrid()
	res, _ := Apply(g, DrumCommand{Action: DoubleTime})
	snare, _ := laneByName(res.After, "snare")
	// 4→2, 12→6, then repeated at +8: 10,14.
	want := []int{2, 6, 10, 14}
	if got := onSteps(snare); !reflect.DeepEqual(got, want) {
		t.Errorf("double-time snare = %v, want %v", got, want)
	}
}

func TestApply_humanizeChangesVelocityOnlyOnTargetLane(t *testing.T) {
	g := fixtureGrid()
	res, _ := Apply(g, DrumCommand{Action: Humanize, Lane: "snare", Seed: DefaultSeed})
	// snare velocities should vary; on-cells unchanged in POSITION.
	snareBefore, _ := laneByName(g, "snare")
	snareAfter, _ := laneByName(res.After, "snare")
	if !reflect.DeepEqual(onSteps(snareBefore), onSteps(snareAfter)) {
		t.Error("humanize must not move hits, only shape velocity")
	}
	// hat lane (not targeted) must be byte-identical.
	hatBefore, _ := laneByName(g, "hat")
	hatAfter, _ := laneByName(res.After, "hat")
	if !reflect.DeepEqual(hatBefore.Vel, hatAfter.Vel) {
		t.Error("humanize on snare must not touch the hat lane")
	}
	// at least one snare velocity should differ from the flat 88.
	changed := false
	for _, s := range onSteps(snareAfter) {
		if snareAfter.Vel[s] != 88 {
			changed = true
		}
	}
	if !changed {
		t.Error("humanize should vary snare velocities away from a flat 88")
	}
}

func TestApply_humanizeAllLanes(t *testing.T) {
	g := fixtureGrid()
	res, _ := Apply(g, DrumCommand{Action: Humanize, Lane: "", Seed: DefaultSeed})
	if !res.Changed {
		t.Error("humanize all should change velocities")
	}
}

func TestApply_fillRollIntoBeat4(t *testing.T) {
	g := fixtureGrid()
	res, _ := Apply(g, DrumCommand{Action: Fill, Lane: "hat", Beat: 4})
	hat, ok := laneByName(res.After, "hat")
	if !ok {
		t.Fatal("hat lane missing after fill")
	}
	// Beat 4 = steps 12..15 must all be on (a 16th roll).
	for s := 12; s <= 15; s++ {
		if !hat.On[s] {
			t.Errorf("fill: step %d should be on", s)
		}
	}
	// Crescendo: last cell louder than the first cell of the roll.
	if hat.Vel[15] <= hat.Vel[12] {
		t.Errorf("fill should crescendo: vel[15]=%d should exceed vel[12]=%d", hat.Vel[15], hat.Vel[12])
	}
}

func TestApply_fillCreatesMissingLane(t *testing.T) {
	g := fixtureGrid()
	// Remove the hat lane, then ask for a hat roll → lane should be created.
	g.Lanes = g.Lanes[:2] // kick, snare only
	res, _ := Apply(g, DrumCommand{Action: Fill, Lane: "hat", Beat: 4})
	if _, ok := laneByName(res.After, "hat"); !ok {
		t.Error("fill on a missing lane should create that lane")
	}
}

func TestApply_swingSoftensOddSixteenths(t *testing.T) {
	g := fixtureGrid()
	res, _ := Apply(g, DrumCommand{Action: Swing, Swing: 0.66})
	hat, _ := laneByName(res.After, "hat")
	hatBefore, _ := laneByName(g, "hat")
	// On-cell positions unchanged; an odd on-cell (step 6? hats are on evens here)
	// Hats sit on even steps, so swing softening of ODD cells won't touch them.
	// Use a denser check: every odd ON cell should be softer than before.
	for s := range hat.On {
		if hat.On[s] && s%2 == 1 {
			if hat.Vel[s] >= hatBefore.Vel[s] {
				t.Errorf("swing: odd cell %d should be softer", s)
			}
		}
	}
	if !res.Changed {
		// fixture hats are on even cells; add an odd hit to ensure swing bites.
		g2 := fixtureGrid()
		g2.Lanes[2].On[3] = true
		g2.Lanes[2].Vel[3] = 88
		r2, _ := Apply(g2, DrumCommand{Action: Swing, Swing: 0.66})
		if !r2.Changed {
			t.Error("swing should change a grid with odd-step hits")
		}
	}
}

func TestApply_densityBusier(t *testing.T) {
	g := fixtureGrid()
	res, _ := Apply(g, DrumCommand{Action: Density, Up: true})
	hat, _ := laneByName(res.After, "hat")
	// Busier fills every empty hat cell → all 16 on.
	if got := len(onSteps(hat)); got != 16 {
		t.Errorf("busier hats = %d cells, want 16", got)
	}
	// kick/snare (not hat-like) untouched.
	kickBefore, _ := laneByName(g, "kick")
	kickAfter, _ := laneByName(res.After, "kick")
	if !reflect.DeepEqual(onSteps(kickBefore), onSteps(kickAfter)) {
		t.Error("density should only touch hat/perc lanes, not the kick")
	}
}

func TestApply_densityStripBack(t *testing.T) {
	g := fixtureGrid()
	res, _ := Apply(g, DrumCommand{Action: Density, Up: false})
	hat, _ := laneByName(res.After, "hat")
	// Strip back removes odd-step hits; fixture hats on evens → unchanged here.
	for _, s := range onSteps(hat) {
		if s%2 == 1 {
			t.Errorf("strip-back should remove odd-step hat at %d", s)
		}
	}
}

func TestApply_quantizeResetsHumanizedVelocities(t *testing.T) {
	g := fixtureGrid()
	hum, _ := Apply(g, DrumCommand{Action: Humanize, Lane: "", Seed: DefaultSeed})
	q, _ := Apply(hum.After, DrumCommand{Action: Quantize})
	for _, ln := range q.After.Lanes {
		for _, s := range onSteps(ln) {
			if ln.Vel[s] != 88 {
				t.Errorf("quantize should reset velocities to 88, got %d on lane %s step %d", ln.Vel[s], ln.Name, s)
			}
		}
	}
	if !q.Changed {
		t.Error("quantize after humanize should be a change (velocities reset)")
	}
}

func TestApply_variationsCount(t *testing.T) {
	g := fixtureGrid()
	res, _ := Apply(g, DrumCommand{Action: Variations, Count: 4, Seed: DefaultSeed})
	if len(res.Variants) != 4 {
		t.Fatalf("want 4 variants, got %d", len(res.Variants))
	}
	// Variant 0 is the original (as-is).
	if !gridsEqual(res.Variants[0], g) {
		t.Error("variant 0 should equal the original grid")
	}
	// At least one later variant should differ.
	differs := false
	for i := 1; i < len(res.Variants); i++ {
		if !gridsEqual(res.Variants[i], g) {
			differs = true
		}
	}
	if !differs {
		t.Error("variations should produce at least one different grid")
	}
}

func TestApply_variationsDefaultsToThree(t *testing.T) {
	g := fixtureGrid()
	res, _ := Apply(g, DrumCommand{Action: Variations, Count: 0, Seed: DefaultSeed})
	if len(res.Variants) != 3 {
		t.Errorf("default variation count should be 3, got %d", len(res.Variants))
	}
}

func TestApply_unknownDegradesNoChange(t *testing.T) {
	g := fixtureGrid()
	res, err := Apply(g, DrumCommand{Action: Unknown, Note: "didn't get that"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed {
		t.Error("unknown action must not change the grid")
	}
	if !gridsEqual(res.Before, res.After) {
		t.Error("unknown: before and after must be identical")
	}
	if res.Summary == "" {
		t.Error("unknown should carry a friendly summary")
	}
}

func TestApply_nilGridDegrades(t *testing.T) {
	res, err := Apply(nil, DrumCommand{Action: HalfTime})
	if err != nil {
		t.Fatalf("nil grid should degrade, not error: %v", err)
	}
	if res.Changed {
		t.Error("nil grid: nothing to change")
	}
}

// ─── immutability ─────────────────────────────────────────────────────────────

func TestApply_immutable(t *testing.T) {
	transforms := []DrumCommand{
		{Action: HalfTime},
		{Action: DoubleTime},
		{Action: Humanize, Seed: DefaultSeed},
		{Action: Fill, Lane: "hat", Beat: 4},
		{Action: Swing, Swing: 0.62},
		{Action: Density, Up: true},
		{Action: Density, Up: false},
		{Action: Quantize},
		{Action: Variations, Count: 3, Seed: DefaultSeed},
	}
	for _, cmd := range transforms {
		t.Run(cmd.Action.String(), func(t *testing.T) {
			g := fixtureGrid()
			snapshot := cloneGrid(g)
			_, _ = Apply(g, cmd)
			if !gridsEqual(g, snapshot) {
				t.Errorf("Apply(%v) mutated the input grid", cmd.Action)
			}
		})
	}
}

// ─── determinism: same seed twice == identical; humanize reproducible ─────────

func TestApply_deterministicHumanize(t *testing.T) {
	g1, g2 := fixtureGrid(), fixtureGrid()
	r1, _ := Apply(g1, DrumCommand{Action: Humanize, Lane: "", Seed: 7})
	r2, _ := Apply(g2, DrumCommand{Action: Humanize, Lane: "", Seed: 7})
	if !gridsEqual(r1.After, r2.After) {
		t.Error("same seed must produce byte-identical humanize output")
	}
}

func TestApply_humanizeDiffersBySeed(t *testing.T) {
	g1, g2 := fixtureGrid(), fixtureGrid()
	r1, _ := Apply(g1, DrumCommand{Action: Humanize, Lane: "", Seed: 1})
	r2, _ := Apply(g2, DrumCommand{Action: Humanize, Lane: "", Seed: 999})
	if gridsEqual(r1.After, r2.After) {
		t.Error("different seeds should generally produce different humanization")
	}
}

func TestApply_deterministicVariations(t *testing.T) {
	g1, g2 := fixtureGrid(), fixtureGrid()
	r1, _ := Apply(g1, DrumCommand{Action: Variations, Count: 5, Seed: 42})
	r2, _ := Apply(g2, DrumCommand{Action: Variations, Count: 5, Seed: 42})
	if len(r1.Variants) != len(r2.Variants) {
		t.Fatal("variant count mismatch")
	}
	for i := range r1.Variants {
		if !gridsEqual(r1.Variants[i], r2.Variants[i]) {
			t.Errorf("variant %d not reproducible with the same seed", i)
		}
	}
}

func TestApply_deterministicAllActions(t *testing.T) {
	cmds := []DrumCommand{
		{Action: HalfTime}, {Action: DoubleTime},
		{Action: Humanize, Seed: 3}, {Action: Fill, Lane: "hat", Beat: 4},
		{Action: Swing, Swing: 0.6}, {Action: Density, Up: true},
		{Action: Quantize},
	}
	for _, cmd := range cmds {
		a, _ := Apply(fixtureGrid(), cmd)
		b, _ := Apply(fixtureGrid(), cmd)
		if !gridsEqual(a.After, b.After) {
			t.Errorf("%v not deterministic", cmd.Action)
		}
	}
}

// ─── model parser: degrade + JSON mapping ─────────────────────────────────────

func TestModelParser_degradesToKeywordOnError(t *testing.T) {
	mp := modelParser{run: func(_, _, _ string) (string, error) {
		return "", errModelStub
	}}
	cmd := mp.Parse("make it half-time", GridSummary{}, DefaultSeed)
	if cmd.Action != HalfTime {
		t.Errorf("model error should degrade to keyword parser, got %v", cmd.Action)
	}
}

func TestModelParser_nilRunIsKeywordOnly(t *testing.T) {
	mp := modelParser{run: nil}
	cmd := mp.Parse("swing it", GridSummary{}, DefaultSeed)
	if cmd.Action != Swing {
		t.Errorf("nil run should behave as keyword parser, got %v", cmd.Action)
	}
}

func TestModelParser_usesModelJSON(t *testing.T) {
	mp := modelParser{run: func(_, _, _ string) (string, error) {
		return `chatter {"action":"fill","lane":"snare","beat":3,"count":0,"up":false,"swing":0,"note":"snare fill on 3"} trailing`, nil
	}}
	cmd := mp.Parse("do a thing on the snare around three", GridSummary{}, DefaultSeed)
	if cmd.Action != Fill || cmd.Lane != "snare" || cmd.Beat != 3 {
		t.Errorf("model JSON not mapped: %+v", cmd)
	}
}

func TestModelParser_unknownActionFallsBackToKeyword(t *testing.T) {
	// Model returns unknown, but the keyword parser CAN handle this sentence.
	mp := modelParser{run: func(_, _, _ string) (string, error) {
		return `{"action":"unknown","note":"dunno"}`, nil
	}}
	cmd := mp.Parse("double-time it", GridSummary{}, DefaultSeed)
	if cmd.Action != DoubleTime {
		t.Errorf("model Unknown should fall back to keyword parser, got %v", cmd.Action)
	}
}

func TestParseModelJSON_bad(t *testing.T) {
	_, err := parseModelJSON("no json here", DefaultSeed, "x")
	if err == nil {
		t.Error("expected error for output with no JSON object")
	}
}

func TestMapAction(t *testing.T) {
	cases := map[string]Action{
		"half_time": HalfTime, "double-time": DoubleTime, "humanize": Humanize,
		"fill": Fill, "swing": Swing, "variations": Variations, "density": Density,
		"quantize": Quantize, "nonsense": Unknown, "": Unknown,
	}
	for in, want := range cases {
		if got := mapAction(in); got != want {
			t.Errorf("mapAction(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestSummarizeGrid(t *testing.T) {
	g := fixtureGrid()
	sum := SummarizeGrid(g)
	if sum.Bars != 1 || sum.Steps != 16 || len(sum.Lanes) != 3 {
		t.Fatalf("bad summary: %+v", sum)
	}
	// kick has 2 hits in the fixture.
	for _, l := range sum.Lanes {
		if l.Name == "kick" && l.Hits != 2 {
			t.Errorf("kick hits = %d, want 2", l.Hits)
		}
	}
	if SummarizeGrid(nil).Bars != 0 {
		t.Error("nil grid should summarize to a zero value")
	}
}

func TestPickParser_defaultsToKeywordOffline(t *testing.T) {
	// On CI/cloud there is no model binary, so PickParser must return a keyword
	// parser that handles the documented sentences.
	p := PickParser()
	cmd := p.Parse("make it half-time", GridSummary{}, DefaultSeed)
	if cmd.Action != HalfTime {
		t.Errorf("offline PickParser should handle keywords, got %v", cmd.Action)
	}
}

func TestActionString(t *testing.T) {
	if HalfTime.String() != "half-time" || Unknown.String() != "unknown" {
		t.Error("Action.String mismatch")
	}
}
