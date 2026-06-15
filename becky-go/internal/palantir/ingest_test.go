package palantir

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile is a small helper to drop a synthetic becky output into a temp corpus.
func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAddIdentify_namedPersonsOnly(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "clipA.identify.json", `{
	  "file":"clipA.mp4",
	  "identifications":[
	    {"type":"voice","name":"John Clancy","confidence":0.9},
	    {"type":"face","name":"","confidence":0.5}
	  ]
	}`)
	in := Ingest{}
	in.AddIdentify(p)
	if len(in.Observations) != 1 {
		t.Fatalf("want 1 named-person observation, got %d", len(in.Observations))
	}
	o := in.Observations[0]
	if o.NodeID != "person:john-clancy" || o.Kind != KindPerson {
		t.Errorf("bad node: %+v", o)
	}
	if o.SourceFile != "clipA.mp4" {
		t.Errorf("source_file should come from the JSON, got %q", o.SourceFile)
	}
}

func TestAddOSINT_emitsPlaceAndDevice(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "clipB.osint.json", `{
	  "source_file":"clipB.mp4","source_sha256":"ab12","timestamp":3.0,
	  "gps_lat":32.9,"gps_lon":-96.7,"make":"Apple","model":"iPhone 13"
	}`)
	in := Ingest{}
	in.AddOSINT(p)
	kinds := map[string]bool{}
	for _, o := range in.Observations {
		kinds[o.Kind] = true
	}
	if !kinds[KindPlace] || !kinds[KindDevice] {
		t.Errorf("expected both place and device observations, got %+v", in.Observations)
	}
}

func TestAddCluster_recurringUnknownIsCandidate(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.json", `{
	  "clusters":[{"id":"A","label":"Person A","members":[
	    {"source_file":"c1.mp4","timestamp":1.0,"confidence":0.7},
	    {"source_file":"c2.mp4","timestamp":2.0,"confidence":0.7}
	  ]}]
	}`)
	in := Ingest{}
	in.AddCluster(p)
	if len(in.Observations) != 2 {
		t.Fatalf("want 2 cluster members, got %d", len(in.Observations))
	}
	for _, o := range in.Observations {
		if o.Status != StatusCandidate {
			t.Errorf("recurring-unknown person must be a candidate, got %q", o.Status)
		}
		if o.NodeID != "person:cluster-a" {
			t.Errorf("cluster node id = %q", o.NodeID)
		}
	}
}

func TestAddIdentify_garbledFileDegradesWithNote(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "broken.identify.json", `{ this is not json `)
	in := Ingest{}
	in.AddIdentify(p)
	if len(in.Observations) != 0 {
		t.Error("garbled file should yield no observations")
	}
	if len(in.Notes) == 0 {
		t.Error("garbled file must be recorded as a degrade note, not a crash")
	}
}

func TestHarvest_discoversAndClassifies(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.identify.json", `{"file":"a.mp4","identifications":[{"type":"voice","name":"John","confidence":0.9}]}`)
	writeFile(t, dir, "a.events.json", `{"file":"a.mp4","events":[{"type":"phone","timestamp":2.0,"confidence":0.8,"description":"phone handed over"}]}`)
	writeFile(t, dir, "a.osint.json", `{"source_file":"a.mp4","make":"Apple","model":"iPhone 13"}`)
	writeFile(t, dir, "notes.txt", `ignore me`)

	in := Harvest(HarvestOptions{Root: dir})
	if in.FilesIngested != 3 {
		t.Errorf("files ingested = %d, want 3 (txt ignored)", in.FilesIngested)
	}
	kinds := map[string]int{}
	for _, o := range in.Observations {
		kinds[o.Kind]++
	}
	if kinds[KindPerson] == 0 || kinds[KindEvent] == 0 || kinds[KindDevice] == 0 {
		t.Errorf("expected person+event+device observations, got %v", kinds)
	}
}

func TestHarvest_unreadableCorpusDegrades(t *testing.T) {
	in := Harvest(HarvestOptions{Root: filepath.Join(t.TempDir(), "does-not-exist")})
	if len(in.Notes) == 0 {
		t.Error("a missing corpus must produce a plain-language note, not a crash")
	}
}
