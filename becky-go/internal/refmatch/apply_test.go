package refmatch

import (
	"strings"
	"testing"

	"becky-go/internal/music"
)

// planWithMoves builds a MatchPlan by comparing a dark stem (mine) to a bright,
// louder reference, so the plan reliably carries EQ moves AND a gain move.
func planWithMoves(t *testing.T) MatchPlan {
	t.Helper()
	ref := profileFromSamples(t, mix(tone(4000, 0.7, 1.0), tone(8000, 0.5, 1.0)), Options{})
	mine := profileFromSamples(t, mix(tone(90, 0.2, 1.0), tone(110, 0.15, 1.0)), Options{})
	plan := Match(ref, mine)
	if len(plan.EQMoves) == 0 {
		t.Fatalf("test setup: expected EQ moves in the plan")
	}
	return plan
}

func baseProject() music.Project {
	return music.Project{
		SchemaVersion: 1,
		Tool:          "test",
		Buses: []music.ProjBus{
			{ID: "bus.drums", Out: "bus.master"},
			{ID: "bus.master", Out: "out.main"},
		},
	}
}

func findBus(p music.Project, id string) (music.ProjBus, bool) {
	for _, b := range p.Buses {
		if b.ID == id {
			return b, true
		}
	}
	return music.ProjBus{}, false
}

func findFX(b music.ProjBus, id string) (music.ProjFX, bool) {
	for _, fx := range b.FX {
		if fx.ID == id {
			return fx, true
		}
	}
	return music.ProjFX{}, false
}

// --- apply inserts the right EQ + gain nodes ---

func TestApplyInsertsEQAndGainNodes(t *testing.T) {
	plan := planWithMoves(t)
	res := ApplyPlan(baseProject(), "bus.drums", plan)
	if res.NoMoves {
		t.Fatalf("plan had moves; should not report NoMoves")
	}

	bus, ok := findBus(res.Project, "bus.drums")
	if !ok {
		t.Fatalf("drums bus missing from result")
	}
	eq, ok := findFX(bus, "drums.ref.eq")
	if !ok {
		t.Fatalf("expected an EQ node drums.ref.eq; got fx %+v", bus.FX)
	}
	if !strings.HasPrefix(eq.Type, "eq:ref:") {
		t.Errorf("EQ node Type should be the eq:ref: encoding, got %q", eq.Type)
	}
	// The encoded EQ string must contain one term per plan move ("@" separator).
	if got := strings.Count(eq.Type, "@"); got != len(plan.EQMoves) {
		t.Errorf("encoded EQ has %d terms, want %d (%q)", got, len(plan.EQMoves), eq.Type)
	}

	if plan.GainText != "" {
		g, ok := findFX(bus, "drums.ref.gain")
		if !ok {
			t.Fatalf("plan has a gain move; expected drums.ref.gain node")
		}
		if !strings.HasPrefix(g.Type, "gain:ref:") || !strings.HasSuffix(g.Type, "dB") {
			t.Errorf("gain node Type malformed: %q", g.Type)
		}
	}
}

// --- apply is immutable: the input project is never modified ---

func TestApplyImmutable(t *testing.T) {
	plan := planWithMoves(t)
	in := baseProject()
	beforeBus, _ := findBus(in, "bus.drums")
	beforeFXCount := len(beforeBus.FX)

	_ = ApplyPlan(in, "bus.drums", plan)

	afterBus, _ := findBus(in, "bus.drums")
	if len(afterBus.FX) != beforeFXCount {
		t.Errorf("input project mutated: drums bus FX count %d -> %d", beforeFXCount, len(afterBus.FX))
	}
}

// --- apply is idempotent: re-applying the same plan does not duplicate nodes ---

func TestApplyIdempotent(t *testing.T) {
	plan := planWithMoves(t)
	once := ApplyPlan(baseProject(), "bus.drums", plan)
	twice := ApplyPlan(once.Project, "bus.drums", plan)

	b1, _ := findBus(once.Project, "bus.drums")
	b2, _ := findBus(twice.Project, "bus.drums")
	if len(b1.FX) != len(b2.FX) {
		t.Errorf("re-applying duplicated nodes: %d -> %d FX", len(b1.FX), len(b2.FX))
	}
	// And the encoded node values must be byte-identical (replace, not append).
	for _, fx := range b1.FX {
		o, ok := findFX(b2, fx.ID)
		if !ok || o.Type != fx.Type {
			t.Errorf("node %s changed on re-apply: %q -> %q (ok=%v)", fx.ID, fx.Type, o.Type, ok)
		}
	}
}

// --- apply creates a missing bus, routed to master ---

func TestApplyCreatesMissingBus(t *testing.T) {
	plan := planWithMoves(t)
	res := ApplyPlan(baseProject(), "bus.newcomer", plan)
	bus, ok := findBus(res.Project, "bus.newcomer")
	if !ok {
		t.Fatalf("apply should have created bus.newcomer")
	}
	if bus.Out != "bus.master" {
		t.Errorf("created bus should route to bus.master, got %q", bus.Out)
	}
}

// --- close-enough plan applies nothing ---

func TestApplyNoMoves(t *testing.T) {
	// Identical profiles -> "close enough" -> no moves.
	p := profileFromSamples(t, mix(tone(440, 0.4, 1.0)), Options{})
	plan := Match(p, p)
	if plan.MoveCount != 0 {
		t.Fatalf("test setup: identical stems should give 0 moves, got %d", plan.MoveCount)
	}
	in := baseProject()
	res := ApplyPlan(in, "bus.drums", plan)
	if !res.NoMoves {
		t.Errorf("a close-enough plan should report NoMoves")
	}
	bus, _ := findBus(res.Project, "bus.drums")
	if _, ok := findFX(bus, "drums.ref.eq"); ok {
		t.Errorf("no-move plan should not write an EQ node")
	}
	if _, ok := findFX(bus, "drums.ref.gain"); ok {
		t.Errorf("no-move plan should not write a gain node")
	}
}

// --- dry-run produces a summary identical to what apply would do, with no project ---

func TestDryRunSummaryMatchesApply(t *testing.T) {
	plan := planWithMoves(t)
	got := DryRunSummary("bus.drums", plan)
	applied := ApplyPlan(baseProject(), "bus.drums", plan)
	if got != applied.Summary {
		t.Errorf("dry-run summary %q != apply summary %q", got, applied.Summary)
	}
	if !strings.Contains(got, "drums") {
		t.Errorf("summary should name the bus: %q", got)
	}
}

// --- EQ encoding round-trips the move count and is deterministic ---

func TestEncodeEQTypeDeterministic(t *testing.T) {
	moves := []EQMove{
		{Band: "low", CenterHz: 84.9, DeltaDB: -1.5},
		{Band: "presence", CenterHz: 3873.0, DeltaDB: 2.5},
	}
	a := encodeEQType(moves)
	b := encodeEQType(moves)
	if a != b {
		t.Errorf("encodeEQType not deterministic: %q vs %q", a, b)
	}
	if a != "eq:ref:-1.5@85,+2.5@3873" {
		t.Errorf("unexpected encoding: %q", a)
	}
}
