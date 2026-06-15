package habits

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// seedStore builds a small store with one learned and one candidate habit.
func seedStore() *Store {
	s := NewStore()
	s.Observe(rec("kick", "gain_db", "-7"))
	s.Observe(rec("kick", "gain_db", "-7"))  // learned
	s.Observe(rec("snare", "gain_db", "-3")) // candidate
	return s
}

// TestSaveLoad_roundTrip saves to a temp dir then loads back and asserts the
// store is identical (same habits, defaults, evidence, learned flags).
func TestSaveLoad_roundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "habits.json")

	orig := seedStore()
	if err := orig.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded.Habits) != len(orig.Habits) {
		t.Fatalf("habit count %d != %d", len(loaded.Habits), len(orig.Habits))
	}
	for _, k := range orig.Keys() {
		a, b := orig.Habits[k], loaded.Habits[k]
		if a.Default != b.Default || a.Evidence != b.Evidence || a.Learned != b.Learned {
			t.Errorf("habit %q differs: %+v vs %+v", k, a, b)
		}
	}
	// re-marshalling the loaded store must equal the original bytes (full fidelity)
	ob, _ := orig.Marshal()
	lb, _ := loaded.Marshal()
	if !bytes.Equal(ob, lb) {
		t.Errorf("round-trip not byte-identical:\n--- saved ---\n%s\n--- reloaded ---\n%s", ob, lb)
	}
}

// TestMarshal_deterministic asserts that two stores built from the SAME records in
// DIFFERENT insertion orders serialise to byte-identical JSON (the determinism
// invariant — never map iteration order in output).
func TestMarshal_deterministic(t *testing.T) {
	recordsA := []CorrectionRecord{
		rec("snare", "gain_db", "-3"),
		rec("kick", "gain_db", "-7"),
		rec("kick", "gain_db", "-7"),
		rec("kick", "pan", "0.1"),
	}
	recordsB := []CorrectionRecord{
		rec("kick", "pan", "0.1"),
		rec("kick", "gain_db", "-7"),
		rec("snare", "gain_db", "-3"),
		rec("kick", "gain_db", "-7"),
	}
	a, b := NewStore(), NewStore()
	a.ObserveAll(recordsA)
	b.ObserveAll(recordsB)

	ab, err := a.Marshal()
	if err != nil {
		t.Fatalf("marshal a: %v", err)
	}
	bb, err := b.Marshal()
	if err != nil {
		t.Fatalf("marshal b: %v", err)
	}
	if !bytes.Equal(ab, bb) {
		t.Errorf("not deterministic across insertion order:\n--- a ---\n%s\n--- b ---\n%s", ab, bb)
	}
	// re-marshalling the same store twice must also be identical
	ab2, _ := a.Marshal()
	if !bytes.Equal(ab, ab2) {
		t.Error("re-marshalling the same store is not stable")
	}
}

// TestLoad_missingFileDegrades confirms a missing store is a fresh empty store,
// not an error (first run learns from zero).
func TestLoad_missingFileDegrades(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	s, err := Load(path)
	if err != nil {
		t.Fatalf("missing file should not error, got %v", err)
	}
	if s == nil || len(s.Habits) != 0 {
		t.Errorf("expected fresh empty store, got %+v", s)
	}
}

// TestLoad_corruptFileDegrades confirms a malformed habits.json yields a typed
// (wrapped) error and never panics.
func TestLoad_corruptFileDegrades(t *testing.T) {
	path := filepath.Join(t.TempDir(), "habits.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Error("expected a parse error for corrupt JSON")
	}
}

// TestSave_emptyPath is rejected with an error, not a panic.
func TestSave_emptyPath(t *testing.T) {
	if err := NewStore().Save(""); err == nil {
		t.Error("empty path should be an error")
	}
}

// TestUnmarshal_recomputesFromDisk confirms a hand-edited habits.json has its
// default/evidence/learned re-derived from counts on load (disk edits stay honest).
func TestUnmarshal_recomputesFromDisk(t *testing.T) {
	// counts say -7 won 3x but the (stale) default/learned fields disagree.
	body := []byte(`{"version":1,"habits":[
		{"scope":"kick","field":"gain_db","counts":{"-7":3,"-6":1},
		 "default":"-6","evidence":1,"learned":false}]}`)
	s, err := Unmarshal(body)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	h, _ := s.Lookup("kick", "gain_db")
	if h.Default != "-7" || h.Evidence != 3 || !h.Learned {
		t.Errorf("recompute failed: %+v want default=-7 evidence=3 learned=true", h)
	}
}

// TestDefaultStorePath always returns a usable, habits.json-suffixed path.
func TestDefaultStorePath(t *testing.T) {
	p := DefaultStorePath()
	if p == "" {
		t.Fatal("empty default path")
	}
	if filepath.Base(p) != "habits.json" {
		t.Errorf("default path %q should end in habits.json", p)
	}
}
