package main

import (
	"math"
	"path/filepath"
	"testing"

	"becky-go/internal/beckydb"
	"becky-go/internal/config"
)

// nilDB is used for propagation tests that run in dry-run mode (no DB writes), so
// SetIdentificationVerified is never called and a nil *beckydb.DB is safe.
var nilDB *beckydb.DB

// configForTest loads config, skipping the test when the sqlite3 CLI / vec0
// extension this layer needs are not present on the machine.
func configForTest(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Load()
	if cfg.Sqlite3 == "" || cfg.SqliteVecExt == "" {
		t.Skip("sqlite3 CLI or vec0 extension not configured")
	}
	return cfg
}

// freshDB opens a temp DB and ensures the schema, skipping gracefully if the
// extension can't load on this machine.
func freshDB(t *testing.T, cfg config.Config) *beckydb.DB {
	t.Helper()
	db, err := beckydb.Open(cfg, filepath.Join(t.TempDir(), "cons.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.EnsureSchema(); err != nil {
		t.Skipf("schema/vec0 unavailable: %v", err)
	}
	return db
}

// TestPropagateThresholdGate proves the core rule: a confirmed entity's name
// propagates onto its UNCONFIRMED rows at/above the threshold and is skipped
// below it. Dry-run so no DB write is attempted.
func TestPropagateThresholdGate(t *testing.T) {
	ids := []beckydb.Identification{
		{ID: "v2:voice:S0", SourceFile: "b.mp4", EntityName: "Defendant", Modality: "voice", Confidence: 0.91, SpeakerID: "S0", VerifiedBy: "analyst"}, // confirmed seed
		{ID: "v1:voice:S0", SourceFile: "a.mp4", EntityName: "Defendant", Modality: "voice", Confidence: 0.85, SpeakerID: "S0"},                        // above -> propagate
		{ID: "v1:voice:S1", SourceFile: "a.mp4", EntityName: "Defendant", Modality: "voice", Confidence: 0.40, SpeakerID: "S1"},                        // below -> skip
	}
	prop, out := propagate(nilDB, ids, 0.8, true, false)
	if prop.Propagated != 1 || prop.Skipped != 1 {
		t.Fatalf("propagated=%d skipped=%d, want 1/1", prop.Propagated, prop.Skipped)
	}
	// dry-run: the in-memory slice must NOT be mutated (no write happened), so
	// coverage never falsely reflects un-persisted changes.
	if out[1].Confirmed() {
		t.Errorf("dry-run must not mutate the row: %+v", out[1])
	}
	// The decision is recorded in the propagation detail instead.
	var propagated, skipped *PropDetail
	for i := range prop.Details {
		switch prop.Details[i].Action {
		case "propagated":
			propagated = &prop.Details[i]
		case "skipped":
			skipped = &prop.Details[i]
		}
	}
	if propagated == nil || propagated.SpeakerID != "S0" || propagated.VerifiedBy != propagationVerifier {
		t.Errorf("propagated detail = %+v, want S0 verified by %q", propagated, propagationVerifier)
	}
	if skipped == nil || skipped.SpeakerID != "S1" {
		t.Errorf("skipped detail = %+v, want S1 (below threshold)", skipped)
	}
}

// TestPropagateAppliesWrite proves the non-dry-run path persists the name via the
// DB and reflects it in the returned slice. Uses the real CLI; skips gracefully.
func TestPropagateAppliesWrite(t *testing.T) {
	cfg := configForTest(t)
	db := freshDB(t, cfg)
	ids := []beckydb.Identification{
		{ID: "v2:voice:S0", SourceFile: "b.mp4", EntityName: "Defendant", Modality: "voice", Confidence: 0.91, SpeakerID: "S0", VerifiedBy: "analyst"},
		{ID: "v1:voice:S0", SourceFile: "a.mp4", EntityName: "Defendant", Modality: "voice", Confidence: 0.85, SpeakerID: "S0"},
	}
	for _, r := range ids {
		if err := db.UpsertIdentification(r); err != nil {
			t.Fatalf("seed UpsertIdentification: %v", err)
		}
	}
	prop, out := propagate(db, ids, 0.8, false, false)
	if prop.Propagated != 1 {
		t.Fatalf("propagated=%d, want 1", prop.Propagated)
	}
	if !out[1].Confirmed() || out[1].VerifiedBy != propagationVerifier {
		t.Errorf("returned row not reflected as written: %+v", out[1])
	}
	// And the write must be persisted.
	persisted, err := db.ListIdentifications()
	if err != nil {
		t.Fatalf("ListIdentifications: %v", err)
	}
	for _, r := range persisted {
		if r.ID == "v1:voice:S0" && r.VerifiedBy != propagationVerifier {
			t.Errorf("DB row not persisted as propagated: %+v", r)
		}
	}
}

// TestPropagateNeverInvents proves that an entity with NO confirmed row is never
// propagated, regardless of how high its confidence is.
func TestPropagateNeverInvents(t *testing.T) {
	ids := []beckydb.Identification{
		{ID: "v1:voice:S0", SourceFile: "a.mp4", EntityName: "The Wife", Modality: "voice", Confidence: 0.99}, // unconfirmed only
	}
	prop, out := propagate(nilDB, ids, 0.5, true, false)
	if prop.Propagated != 0 || prop.Skipped != 0 {
		t.Fatalf("no confirmation should mean no propagation; got propagated=%d skipped=%d",
			prop.Propagated, prop.Skipped)
	}
	if out[0].Confirmed() {
		t.Errorf("uninvited name must not be confirmed: %+v", out[0])
	}
}

// TestPropagateLeavesConfirmedAlone confirms an already-confirmed row is not
// touched (no double-write, no spurious detail).
func TestPropagateLeavesConfirmedAlone(t *testing.T) {
	ids := []beckydb.Identification{
		{ID: "v1:voice:S0", SourceFile: "a.mp4", EntityName: "Defendant", Modality: "voice", Confidence: 0.9, VerifiedBy: "analyst"},
	}
	prop, _ := propagate(nilDB, ids, 0.8, true, false)
	if prop.Propagated != 0 || prop.Skipped != 0 || len(prop.Details) != 0 {
		t.Fatalf("confirmed-only corpus needs no propagation; got %+v", prop)
	}
}

// TestCoverage checks per-entity overall + per-modality distinct-video counts.
func TestCoverage(t *testing.T) {
	ids := []beckydb.Identification{
		{SourceFile: "a.mp4", EntityName: "Defendant", Modality: "voice", VerifiedBy: "analyst"},
		{SourceFile: "a.mp4", EntityName: "Defendant", Modality: "location"}, // same video, different modality
		{SourceFile: "b.mp4", EntityName: "Defendant", Modality: "voice"},
		{SourceFile: "a.mp4", EntityName: "The Wife", Modality: "voice"},
	}
	cov := coverage(ids, 4) // corpus of 4 videos
	if len(cov) != 2 {
		t.Fatalf("got %d entities, want 2", len(cov))
	}
	// Sorted alphabetically: Defendant first.
	def := cov[0]
	if def.Name != "Defendant" || def.Recognized != 2 {
		t.Fatalf("Defendant recognized = %d (name %q), want 2", def.Recognized, def.Name)
	}
	if def.Modalities["voice"].Videos != 2 {
		t.Errorf("Defendant voice videos = %d, want 2", def.Modalities["voice"].Videos)
	}
	if def.Modalities["location"].Videos != 1 {
		t.Errorf("Defendant location videos = %d, want 1", def.Modalities["location"].Videos)
	}
	if def.Modalities["face"].Videos != 0 {
		t.Errorf("Defendant face videos = %d, want 0", def.Modalities["face"].Videos)
	}
	if def.Confirmed != 1 || def.Unconfirmed != 2 {
		t.Errorf("Defendant confirmed/unconfirmed = %d/%d, want 1/2", def.Confirmed, def.Unconfirmed)
	}
	if math.Abs(def.Percent-50.0) > 1e-9 {
		t.Errorf("Defendant percent = %v, want 50.0 (2/4)", def.Percent)
	}
}

// TestGaps flags only entities below half coverage and emits suggestions.
func TestGaps(t *testing.T) {
	entities := []EntityCov{
		{Name: "Defendant", Recognized: 3, TotalVideos: 4, Modalities: map[string]ModeCov{
			"voice": {Videos: 3, TotalVideos: 4}, "face": {}, "location": {}}},
		{Name: "The Wife", Recognized: 1, TotalVideos: 4, Modalities: map[string]ModeCov{
			"voice": {Videos: 1, TotalVideos: 4}, "face": {}, "location": {}}},
	}
	g := gaps(entities, 4)
	// Defendant at 75% is above the 50% gap line; only The Wife (25%) is flagged.
	if len(g) != 1 || g[0].Entity != "The Wife" {
		t.Fatalf("gaps = %+v, want only The Wife", g)
	}
	if g[0].NotRecognized != 3 {
		t.Errorf("The Wife not_recognized = %d, want 3", g[0].NotRecognized)
	}
	if len(g[0].Suggestions) == 0 {
		t.Error("expected templated suggestions for the gap")
	}
}

// TestGapsEmptyCorpus must not panic and yields no gaps when total is 0.
func TestGapsEmptyCorpus(t *testing.T) {
	if g := gaps(nil, 0); len(g) != 0 {
		t.Errorf("gaps(empty) = %+v, want none", g)
	}
}

// TestPct and rounding behavior for the report percentages.
func TestPct(t *testing.T) {
	cases := []struct {
		n, total int
		want     float64
	}{
		{0, 0, 0},
		{1, 0, 0},
		{1, 1, 100},
		{45, 92, 48.9},
		{47, 92, 51.1},
		{1, 3, 33.3},
	}
	for _, c := range cases {
		if got := pct(c.n, c.total); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("pct(%d,%d) = %v, want %v", c.n, c.total, got, c.want)
		}
	}
}

// TestIdentIDDeterministic confirms the ingest key is stable and discriminates
// by source/modality/speaker (so re-ingest replaces, distinct rows stay distinct).
func TestIdentIDDeterministic(t *testing.T) {
	a := identID("v.mp4", "voice", "SPEAKER_00", "Defendant")
	b := identID("v.mp4", "voice", "SPEAKER_00", "Defendant")
	if a != b {
		t.Fatalf("identID not deterministic: %q vs %q", a, b)
	}
	if a == identID("v.mp4", "voice", "SPEAKER_01", "Defendant") {
		t.Error("different speaker should yield different id")
	}
	if a == identID("w.mp4", "voice", "SPEAKER_00", "Defendant") {
		t.Error("different source should yield different id")
	}
	// Location has no speaker_id; the name discriminates.
	loc := identID("v.mp4", "location", "", "Home")
	if loc == identID("v.mp4", "location", "", "Office") {
		t.Error("different location name should yield different id")
	}
}

// TestConfirmedEntitySet collects only names that have a confirmed row.
func TestConfirmedEntitySet(t *testing.T) {
	ids := []beckydb.Identification{
		{EntityName: "Defendant", VerifiedBy: "analyst"},
		{EntityName: "The Wife"}, // unconfirmed
		{EntityName: "Judge", VerifiedBy: "becky-consolidate"},
	}
	set := confirmedEntitySet(ids)
	if !set["Defendant"] || !set["Judge"] {
		t.Error("expected Defendant and Judge confirmed")
	}
	if set["The Wife"] {
		t.Error("The Wife has no confirmation; must not be in set")
	}
}
