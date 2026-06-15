package habits

import (
	"strconv"
	"testing"
)

// rec is a tiny constructor for a canonical correction record.
func rec(scope, field, fixed string) CorrectionRecord {
	return CorrectionRecord{Scope: scope, Field: field, Auto: "auto", Fixed: fixed}
}

// TestObserve_thresholdFlip is the load-bearing test: a fixed value becomes the
// learned default EXACTLY at MinEvidence — not before (corroborate, then
// conclude). It asserts Apply too, since that is the consumer-visible behaviour.
func TestObserve_thresholdFlip(t *testing.T) {
	for n := 0; n <= MinEvidence+1; n++ {
		s := NewStore()
		for i := 0; i < n; i++ {
			s.Observe(rec("kick", "gain_db", "-7"))
		}
		h, ok := s.Lookup("kick", "gain_db")
		wantLearned := n >= MinEvidence

		if n == 0 {
			if ok {
				t.Fatalf("n=0: expected no habit, got %+v", h)
			}
			if got := s.Apply("kick", "gain_db", "0"); got != "0" {
				t.Errorf("n=0: Apply should pass through fresh, got %q", got)
			}
			continue
		}
		if !ok {
			t.Fatalf("n=%d: expected a habit to exist", n)
		}
		if h.Learned != wantLearned {
			t.Errorf("n=%d: Learned=%v want %v (evidence=%d, threshold=%d)",
				n, h.Learned, wantLearned, h.Evidence, MinEvidence)
		}
		got := s.Apply("kick", "gain_db", "0")
		if wantLearned && got != "-7" {
			t.Errorf("n=%d: Apply should return learned -7, got %q", n, got)
		}
		if !wantLearned && got != "0" {
			t.Errorf("n=%d: below threshold Apply must pass through fresh, got %q", n, got)
		}
	}
}

// TestObserve_winnerIsMostCorroborated checks that with competing fixed values the
// MOST-corroborated one wins and ties break deterministically by string order.
func TestObserve_winnerIsMostCorroborated(t *testing.T) {
	tests := []struct {
		name        string
		fixes       []string
		wantDefault string
		wantLearned bool
	}{
		{"clear winner", []string{"-7", "-7", "-6"}, "-7", true},
		{"tie breaks by string order", []string{"-7", "-6"}, "-6", false},
		{"tie at threshold", []string{"-7", "-7", "-6", "-6"}, "-6", true},
		{"single fix is candidate", []string{"-7"}, "-7", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStore()
			for _, f := range tc.fixes {
				s.Observe(rec("kick", "gain_db", f))
			}
			h, _ := s.Lookup("kick", "gain_db")
			if h.Default != tc.wantDefault {
				t.Errorf("default=%q want %q", h.Default, tc.wantDefault)
			}
			if h.Learned != tc.wantLearned {
				t.Errorf("learned=%v want %v", h.Learned, tc.wantLearned)
			}
		})
	}
}

// TestObserve_skipsUnlearnable confirms blank scope/field/fixed records are
// ignored (reported via the bool) so a flood of empty rows can't create habits.
func TestObserve_skipsUnlearnable(t *testing.T) {
	s := NewStore()
	bad := []CorrectionRecord{
		{Scope: "", Field: "gain_db", Fixed: "-7"},
		{Scope: "kick", Field: "", Fixed: "-7"},
		{Scope: "kick", Field: "gain_db", Fixed: ""},
	}
	for _, r := range bad {
		if s.Observe(r) {
			t.Errorf("expected %+v to be skipped", r)
		}
	}
	if len(s.Habits) != 0 {
		t.Errorf("no habits should have been created, got %d", len(s.Habits))
	}
}

// TestKeysAndScopeIsolation confirms different {scope, field} pairs never collide
// and Keys() is sorted by scope then field.
func TestKeysAndScopeIsolation(t *testing.T) {
	s := NewStore()
	s.Observe(rec("snare", "gain_db", "-3"))
	s.Observe(rec("kick", "pan", "0.1"))
	s.Observe(rec("kick", "gain_db", "-7"))

	if len(s.Habits) != 3 {
		t.Fatalf("expected 3 distinct habits, got %d", len(s.Habits))
	}
	ks := s.Keys()
	wantOrder := []string{"kick\x1fgain_db", "kick\x1fpan", "snare\x1fgain_db"}
	for i, w := range wantOrder {
		if ks[i] != w {
			t.Errorf("Keys()[%d]=%q want %q", i, ks[i], w)
		}
	}
}

// TestObserveAll_countsLearnable verifies the batch counter equals the number of
// learnable rows (used by the CLI to report skipped rows).
func TestObserveAll_countsLearnable(t *testing.T) {
	s := NewStore()
	records := []CorrectionRecord{
		rec("kick", "gain_db", "-7"),
		rec("kick", "gain_db", "-7"),
		{Scope: "kick", Field: "gain_db", Fixed: ""}, // unlearnable
	}
	if got := s.ObserveAll(records); got != 2 {
		t.Errorf("learnable=%d want 2", got)
	}
}

// TestSourcesProvenance confirms the tool that fed a habit is recorded, deduped,
// and sorted (deterministic provenance).
func TestSourcesProvenance(t *testing.T) {
	s := NewStore()
	feed := func(tool string) {
		s.Observe(CorrectionRecord{Scope: "kick", Field: "gain_db", Fixed: "-7",
			Context: map[string]string{"tool": tool}})
	}
	feed("becky-daw")
	feed("becky-canvas")
	feed("becky-daw") // dup
	h, _ := s.Lookup("kick", "gain_db")
	if len(h.Sources) != 2 || h.Sources[0] != "becky-canvas" || h.Sources[1] != "becky-daw" {
		t.Errorf("sources=%v want [becky-canvas becky-daw]", h.Sources)
	}
}

// TestLearnedAndCandidates partitions habits by whether they crossed the
// threshold, exercising the report inputs.
func TestLearnedAndCandidates(t *testing.T) {
	s := NewStore()
	s.Observe(rec("kick", "gain_db", "-7"))
	s.Observe(rec("kick", "gain_db", "-7"))  // learned
	s.Observe(rec("snare", "gain_db", "-3")) // candidate

	learned, cands := s.Learned(), s.Candidates()
	if len(learned) != 1 || learned[0].Scope != "kick" {
		t.Errorf("learned=%+v want one kick habit", learned)
	}
	if len(cands) != 1 || cands[0].Scope != "snare" {
		t.Errorf("candidates=%+v want one snare habit", cands)
	}
}

// TestObserve_manyValuesDeterministicWinner is a stress check that winner
// selection does not depend on insertion order (determinism over map reduction).
func TestObserve_manyValuesDeterministicWinner(t *testing.T) {
	build := func(order []int) *Store {
		s := NewStore()
		for _, v := range order {
			s.Observe(rec("kick", "gain_db", strconv.Itoa(v)))
		}
		return s
	}
	a := build([]int{1, 2, 2, 3, 3, 3})
	b := build([]int{3, 1, 3, 2, 3, 2})
	ha, _ := a.Lookup("kick", "gain_db")
	hb, _ := b.Lookup("kick", "gain_db")
	if ha.Default != "3" || hb.Default != "3" {
		t.Errorf("winner not stable across order: a=%q b=%q want 3", ha.Default, hb.Default)
	}
}
