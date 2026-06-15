package research

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// fixedClock returns a constant time so fetched_at is deterministic across runs.
func fixedClock() func() time.Time {
	return func() time.Time { return time.Date(2026, 6, 14, 9, 12, 0, 0, time.UTC) }
}

// fullBackends wires fake search + fetch + helper for an end-to-end pipeline run.
func fullBackends() Backends {
	pages := map[string]Capture{
		"https://e.com/a": {URL: "https://e.com/a", Title: "A", Text: "alpha content about the topic", HTTPStatus: 200},
		"https://e.com/b": {URL: "https://e.com/b", Title: "B", Text: "beta content about the topic", HTTPStatus: 200},
	}
	// Every planned query returns the same two-result list; RRF dedups to {a,b}.
	results := []SearchResult{{URL: "https://e.com/a", Rank: 1}, {URL: "https://e.com/b", Rank: 2}}
	return Backends{
		Search: &FakeSearch{Table: tableFor("the topic", results)},
		Fetch:  &FakeFetch{Pages: pages, Now: fixedClock()},
		Helper: NewFakeHelper(),
	}
}

func TestRun_topicEndToEnd(t *testing.T) {
	cfg := Config{Question: "the topic", RunDir: t.TempDir(), Now: fixedClock()}
	rep, err := Run(cfg, fullBackends())
	if err != nil {
		t.Fatalf("Run errored: %v", err)
	}
	if rep.Mode != ModeTopic {
		t.Errorf("mode=%q want topic", rep.Mode)
	}
	if len(rep.Sources) != 2 {
		t.Fatalf("expected 2 deduped sources, got %d: %+v", len(rep.Sources), rep.Sources)
	}
	// Stable one-number-per-URL citation numbering.
	if rep.Sources[0].ID != 1 || rep.Sources[1].ID != 2 {
		t.Errorf("sources should be numbered 1..n, got %+v", rep.Sources)
	}
	if len(rep.Findings) == 0 {
		t.Fatal("expected findings from the fake helper")
	}
	for _, f := range rep.Findings {
		if len(f.Cites) == 0 {
			t.Errorf("no claim may appear without a cite: %+v", f)
		}
		for _, id := range f.Cites {
			if id < 1 || id > len(rep.Sources) {
				t.Errorf("cite %d does not resolve to a source", id)
			}
		}
	}
	if rep.SnapshotSHA256 == "" {
		t.Error("a snapshot hash must always be reported")
	}
	if rep.Notes.Honesty == "" {
		t.Error("the honesty note must always be present")
	}
}

func TestRun_deterministicSameSnapshot(t *testing.T) {
	cfg1 := Config{Question: "the topic", RunDir: t.TempDir(), Now: fixedClock()}
	cfg2 := Config{Question: "the topic", RunDir: t.TempDir(), Now: fixedClock()}
	r1, _ := Run(cfg1, fullBackends())
	r2, _ := Run(cfg2, fullBackends())

	// Same captured snapshot → identical findings, sources, citation numbering,
	// and snapshot hash. (RunDir differs, so compare everything else.)
	r1.RunDir, r2.RunDir = "", ""
	if !reflect.DeepEqual(r1.Findings, r2.Findings) {
		t.Error("findings are not reproducible")
	}
	if !reflect.DeepEqual(r1.Sources, r2.Sources) {
		t.Error("sources/citation numbering are not reproducible")
	}
	if r1.SnapshotSHA256 != r2.SnapshotSHA256 {
		t.Errorf("snapshot hash not reproducible: %s vs %s", r1.SnapshotSHA256, r2.SnapshotSHA256)
	}
}

func TestRun_readingListMode(t *testing.T) {
	pages := map[string]Capture{
		"https://e.com/paper1": {URL: "https://e.com/paper1", Text: "paper one", HTTPStatus: 200},
		"https://e.com/paper2": {URL: "https://e.com/paper2", Text: "paper two", HTTPStatus: 200},
	}
	be := Backends{Fetch: &FakeFetch{Pages: pages, Now: fixedClock()}, Helper: NewFakeHelper()}
	cfg := Config{
		URLs:   []string{"https://e.com/paper1", "https://e.com/paper2", "https://e.com/paper1"}, // dup ignored
		RunDir: t.TempDir(), Now: fixedClock(),
	}
	rep, err := Run(cfg, be)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Mode != ModeReadingList {
		t.Errorf("mode=%q want reading-list", rep.Mode)
	}
	if len(rep.Plan) != 0 {
		t.Error("reading-list mode skips planning (no fan-out)")
	}
	if len(rep.Sources) != 2 {
		t.Errorf("dup URL should collapse to 2 sources, got %d", len(rep.Sources))
	}
}

func TestRun_degradeNoNetworkNothingCached(t *testing.T) {
	// Topic mode, offline, empty cache → valid empty report, exit-0 shape, note set.
	cfg := Config{Question: "x", RunDir: t.TempDir(), Offline: true, Now: fixedClock()}
	rep, err := Run(cfg, Backends{Helper: NewFakeHelper()})
	if err != nil {
		t.Fatalf("offline empty run must not error: %v", err)
	}
	if len(rep.Sources) != 0 || len(rep.Findings) != 0 {
		t.Error("offline empty run should have no sources/findings")
	}
	if !strings.Contains(rep.Notes.Degrade, "offline") {
		t.Errorf("degrade note should mention offline, got %q", rep.Notes.Degrade)
	}
}

func TestRun_degradeNoModelSourcesOnly(t *testing.T) {
	pages := map[string]Capture{
		"https://e.com/a": {URL: "https://e.com/a", Text: "alpha", HTTPStatus: 200},
	}
	be := Backends{
		Search: &FakeSearch{Table: tableFor("the topic", []SearchResult{{URL: "https://e.com/a", Rank: 1}})},
		Fetch:  &FakeFetch{Pages: pages, Now: fixedClock()},
		Helper: nil, // no model
	}
	cfg := Config{Question: "the topic", RunDir: t.TempDir(), Now: fixedClock()}
	rep, _ := Run(cfg, be)
	if len(rep.Sources) == 0 {
		t.Error("no-model run should still return the fetched sources")
	}
	if len(rep.Findings) != 0 {
		t.Error("no-model run should produce no findings (sources only)")
	}
	if !strings.Contains(rep.Notes.Degrade, "no-model") {
		t.Errorf("degrade should mention no-model, got %q", rep.Notes.Degrade)
	}
}

func TestRun_selfUpgradeFlag(t *testing.T) {
	// A captured source that mentions a becky dependency + an upgrade word should
	// surface a becky_upgrade flag (consuming the freshness manifest).
	pages := map[string]Capture{
		"https://e.com/news": {
			URL: "https://e.com/news", Title: "news",
			Text: "Parakeet just shipped a new release with a v4 model upgrade.", HTTPStatus: 200,
		},
	}
	be := Backends{
		Search: &FakeSearch{Table: tableFor("asr models", []SearchResult{{URL: "https://e.com/news", Rank: 1}})},
		Fetch:  &FakeFetch{Pages: pages, Now: fixedClock()},
		Helper: NewFakeHelper(),
	}
	cfg := Config{Question: "asr models", RunDir: t.TempDir(), SelfUpgrade: true, Now: fixedClock()}
	rep, _ := Run(cfg, be)
	if len(rep.BeckyUpgrades) == 0 {
		t.Fatal("expected a becky_upgrade flag for the Parakeet mention")
	}
	u := rep.BeckyUpgrades[0]
	if u.Status != StatusCandidate {
		t.Errorf("self-upgrade flags should be candidate until corroborated, got %q", u.Status)
	}
	if len(u.Cites) == 0 {
		t.Error("an upgrade flag must cite the captured source")
	}
}

// tableFor builds a search table mapping every planned query for question → results.
func tableFor(question string, results []SearchResult) map[string][]SearchResult {
	table := map[string][]SearchResult{}
	for _, sq := range PlanQuery(question, 5, 3) {
		for _, q := range sq.Queries {
			table[q] = results
		}
	}
	return table
}
