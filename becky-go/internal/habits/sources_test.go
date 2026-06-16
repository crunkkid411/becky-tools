package habits

import (
	"os"
	"path/filepath"
	"testing"
)

// writeJSONL is a test helper that writes content to path, creating parent dirs.
func writeJSONL(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestLoadCorrectionLog_clean parses a well-formed JSONL file with two records.
func TestLoadCorrectionLog_clean(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "daw.jsonl")
	writeJSONL(t, p,
		`{"tool":"daw","scope":"kick","field":"gain_db","auto":"-3","fixed":"-7","ts":"2026-06-15T14:00:00Z"}`+"\n"+
			`{"tool":"daw","scope":"snare","field":"velocity","auto":"100","fixed":"118"}`+"\n",
	)

	recs, err := LoadCorrectionLog(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2", len(recs))
	}
	r0 := recs[0]
	if r0.Scope != "kick" || r0.Field != "gain_db" || r0.Auto != "-3" || r0.Fixed != "-7" {
		t.Errorf("record 0 wrong: %+v", r0)
	}
	if r0.Context["tool"] != "daw" {
		t.Errorf("record 0 Context[tool]=%q want daw", r0.Context["tool"])
	}
	r1 := recs[1]
	if r1.Scope != "snare" || r1.Fixed != "118" {
		t.Errorf("record 1 wrong: %+v", r1)
	}
}

// TestLoadCorrectionLog_blankAndMalformed verifies that blank lines are silently
// skipped and malformed JSON lines are skipped (degrade, not fatal), while valid
// lines before/after them are still returned.
func TestLoadCorrectionLog_blankAndMalformed(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "hum.jsonl")
	writeJSONL(t, p,
		"\n"+ // leading blank
			`{"tool":"hum","scope":"note","field":"pitch","auto":"C4","fixed":"D4"}`+"\n"+
			"\n"+ // mid blank
			`{not valid json}`+"\n"+ // malformed — skip
			`{"tool":"hum","scope":"note","field":"velocity","auto":"80","fixed":"100"}`+"\n"+
			"\n", // trailing blank
	)

	recs, err := LoadCorrectionLog(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2 (malformed line must be skipped)", len(recs))
	}
	if recs[0].Field != "pitch" || recs[1].Field != "velocity" {
		t.Errorf("wrong records: %+v", recs)
	}
}

// TestLoadCorrectionLog_missingFile returns a wrapped error, not a panic.
func TestLoadCorrectionLog_missingFile(t *testing.T) {
	_, err := LoadCorrectionLog(filepath.Join(t.TempDir(), "no-such.jsonl"))
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

// TestLoadCorrectionLogs_multiFileDeterminism verifies that two JSONL files in
// a directory are loaded in lexicographic order and concatenated correctly.
// Calling twice must produce identical results (determinism invariant).
func TestLoadCorrectionLogs_multiFileDeterminism(t *testing.T) {
	dir := t.TempDir()
	// Deliberately write in reverse lexicographic order to confirm sorting.
	writeJSONL(t, filepath.Join(dir, "vox.jsonl"),
		`{"tool":"vox","scope":"lead","field":"pitch","auto":"C4","fixed":"D4"}`+"\n",
	)
	writeJSONL(t, filepath.Join(dir, "daw.jsonl"),
		`{"tool":"daw","scope":"kick","field":"gain_db","auto":"-3","fixed":"-7"}`+"\n",
	)

	recs1, err := LoadCorrectionLogs(dir)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	recs2, err := LoadCorrectionLogs(dir)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}

	if len(recs1) != 2 {
		t.Fatalf("got %d records, want 2", len(recs1))
	}
	// daw.jsonl sorts before vox.jsonl — kick must come first.
	if recs1[0].Scope != "kick" {
		t.Errorf("record[0].Scope=%q want kick (daw.jsonl sorts before vox.jsonl)", recs1[0].Scope)
	}
	if recs1[1].Scope != "lead" {
		t.Errorf("record[1].Scope=%q want lead", recs1[1].Scope)
	}
	// Determinism: both loads must return the same sequence. CorrectionRecord
	// contains a map so we compare the scalar fields (scope/field/auto/fixed are
	// sufficient to assert order stability; Context provenance is a bonus).
	for i := range recs1 {
		a, b := recs1[i], recs2[i]
		if a.Scope != b.Scope || a.Field != b.Field || a.Auto != b.Auto || a.Fixed != b.Fixed {
			t.Errorf("load 1 and load 2 differ at index %d: %+v vs %+v", i, a, b)
		}
	}
}

// TestLoadCorrectionLogs_missingDir degrades to empty+nil, not an error.
func TestLoadCorrectionLogs_missingDir(t *testing.T) {
	recs, err := LoadCorrectionLogs(filepath.Join(t.TempDir(), "no-such-dir"))
	if err != nil {
		t.Errorf("missing dir should return nil error, got: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("missing dir should return empty records, got %d", len(recs))
	}
}

// TestLoadCorrectionLogs_skipNonJSONL confirms that non-.jsonl files in the
// directory are ignored (e.g. a habits.json sitting in the same dir).
func TestLoadCorrectionLogs_skipNonJSONL(t *testing.T) {
	dir := t.TempDir()
	// This should be ignored.
	writeJSONL(t, filepath.Join(dir, "habits.json"),
		`{"scope":"kick","field":"gain_db","auto":"-3","fixed":"-7"}`+"\n",
	)
	// Only this should be loaded.
	writeJSONL(t, filepath.Join(dir, "daw.jsonl"),
		`{"tool":"daw","scope":"snare","field":"velocity","auto":"100","fixed":"118"}`+"\n",
	)

	recs, err := LoadCorrectionLogs(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(recs) != 1 || recs[0].Scope != "snare" {
		t.Errorf("expected only daw.jsonl to be loaded, got %+v", recs)
	}
}

// TestLoadCorrectionLogs_crossThreshold is the end-to-end integration test:
// two JSONL files each contribute one identical correction so that when both
// are loaded and fed to the Store the habit crosses the MinEvidence threshold.
func TestLoadCorrectionLogs_crossThreshold(t *testing.T) {
	dir := t.TempDir()
	line := `{"tool":"daw","scope":"kick","field":"gain_db","auto":"-3","fixed":"-7"}` + "\n"
	writeJSONL(t, filepath.Join(dir, "session1.jsonl"), line)
	writeJSONL(t, filepath.Join(dir, "session2.jsonl"), line)

	recs, err := LoadCorrectionLogs(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("want 2 records, got %d", len(recs))
	}

	s := NewStore()
	if n := s.ObserveAll(recs); n != 2 {
		t.Fatalf("ObserveAll learnable=%d want 2", n)
	}
	if got := s.Apply("kick", "gain_db", "0"); got != "-7" {
		t.Errorf("Apply=%q want -7 (threshold not crossed)", got)
	}
	h, ok := s.Lookup("kick", "gain_db")
	if !ok || !h.Learned {
		t.Errorf("habit should be Learned after 2 corroborating observations: %+v", h)
	}
	if len(h.Sources) == 0 || h.Sources[0] != "daw" {
		t.Errorf("Sources=%v want [daw]", h.Sources)
	}
}

// TestAppendCorrectionLog_roundTrip appends two lines via AppendCorrectionLog
// and reads them back with LoadCorrectionLog, verifying the round-trip.
func TestAppendCorrectionLog_roundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "canvas.corrections.jsonl")

	if err := AppendCorrectionLog(p, "canvas", "brush", "size", "10", "20"); err != nil {
		t.Fatalf("append 1: %v", err)
	}
	if err := AppendCorrectionLog(p, "canvas", "brush", "size", "10", "20"); err != nil {
		t.Fatalf("append 2: %v", err)
	}

	recs, err := LoadCorrectionLog(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("want 2 records, got %d", len(recs))
	}
	for i, r := range recs {
		if r.Scope != "brush" || r.Field != "size" || r.Fixed != "20" || r.Context["tool"] != "canvas" {
			t.Errorf("record %d wrong: %+v", i, r)
		}
	}

	// Also verify they cross the threshold through the Store.
	s := NewStore()
	s.ObserveAll(recs)
	if got := s.Apply("brush", "size", "10"); got != "20" {
		t.Errorf("Apply=%q want 20", got)
	}
}
