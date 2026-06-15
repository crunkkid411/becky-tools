package canvas

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"becky-go/internal/music"
)

// sampleProjectJSON is a synthetic becky-compose project.json (the shape compose
// emits): tempo/ppq + a few MIDI track lanes routed to buses, plus two sidechain
// edges — deliberately given OUT of sorted order so the determinism/sort tests bite.
const sampleProjectJSON = `{
  "schemaVersion": 1,
  "tool": "becky-compose",
  "tempo": 140,
  "ppq": 480,
  "key": {"root": "F#", "scale": "minor"},
  "tracks": [
    {"id": "melody", "midi": "melody.mid", "channel": 2, "kind": "instrument", "node": "src.melody", "out": "bus.music"},
    {"id": "bass",   "midi": "bass.mid",   "channel": 1, "kind": "instrument", "node": "src.bass",   "out": "bus.808"},
    {"id": "drums",  "midi": "drums.mid",  "channel": 9, "kind": "percussion", "node": "src.drums",  "out": "bus.drums"}
  ],
  "routing": [
    {"from": "src.drums.kick", "to": "bus.808.compressor.sidechain", "kind": "sidechain", "note": "808 ducks under the kick"},
    {"from": "src.drums.kick", "to": "comp.music.sidechain", "kind": "sidechain", "note": "duck the music bus"}
  ]
}`

func parseSample(t *testing.T) music.Project {
	t.Helper()
	var p music.Project
	if err := json.Unmarshal([]byte(sampleProjectJSON), &p); err != nil {
		t.Fatalf("sample project does not parse: %v", err)
	}
	return p
}

func TestSceneFromProject_mapping(t *testing.T) {
	p := parseSample(t)
	s := SceneFromProject(p)

	if s.ActiveMode != ModeDAW {
		t.Errorf("a loaded project should open in DAW mode, got %q", s.ActiveMode)
	}
	if s.Transport.BPM != 140 || s.Transport.PPQ != 480 {
		t.Errorf("transport=%+v, want BPM140/PPQ480", s.Transport)
	}
	if len(s.Tracks) != 3 {
		t.Fatalf("got %d track lanes, want 3", len(s.Tracks))
	}
	// Tracks must be sorted by ID (bass, drums, melody) regardless of input order.
	wantOrder := []string{"bass", "drums", "melody"}
	for i, w := range wantOrder {
		if s.Tracks[i].ID != w {
			t.Errorf("track[%d].ID=%q, want %q (must be sorted)", i, s.Tracks[i].ID, w)
		}
	}
	// Every lane is a MIDI lane with a pitch lane placeholder + exactly one clip.
	for _, tr := range s.Tracks {
		if tr.Kind != LaneMIDI {
			t.Errorf("track %q kind=%q, want midi", tr.ID, tr.Kind)
		}
		if tr.Lane.Pitch == nil {
			t.Errorf("track %q missing pitch-lane placeholder", tr.ID)
		}
		if len(tr.Clips) != 1 {
			t.Errorf("track %q has %d clips, want 1 placeholder", tr.ID, len(tr.Clips))
		}
		if tr.Bus == "" || tr.Source == "" {
			t.Errorf("track %q lost routing/source: %+v", tr.ID, tr)
		}
	}
	// Routing edges carry across and are sorted (From, then To).
	if len(s.Routing) != 2 {
		t.Fatalf("got %d routing edges, want 2", len(s.Routing))
	}
	if s.Routing[0].To > s.Routing[1].To {
		t.Errorf("routing edges not sorted by To: %+v", s.Routing)
	}
	// The corrections-log hook is present and empty (never nil Entries).
	if s.Corrections.Entries == nil {
		t.Error("corrections log Entries must be non-nil so JSON emits []")
	}
}

func TestSceneFromProject_clipLengthFollowsPPQ(t *testing.T) {
	p := parseSample(t) // ppq 480 -> bar 1920t -> 8-bar clip = 15360t
	s := SceneFromProject(p)
	wantLen := int64(480) * 4 * defaultClipBars
	for _, tr := range s.Tracks {
		if tr.Clips[0].Len != wantLen {
			t.Errorf("track %q clip len=%d, want %d", tr.ID, tr.Clips[0].Len, wantLen)
		}
		if got := tr.Clips[0].End(); got != wantLen {
			t.Errorf("track %q clip End()=%d, want %d", tr.ID, got, wantLen)
		}
	}
}

func TestSceneJSON_deterministic(t *testing.T) {
	p := parseSample(t)
	a, err := SceneFromProject(p).JSON()
	if err != nil {
		t.Fatalf("encode A: %v", err)
	}
	// Re-parse + re-map + re-encode: must be byte-identical (no map-order leak).
	b, err := SceneFromProject(parseSample(t)).JSON()
	if err != nil {
		t.Fatalf("encode B: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("scene.json not deterministic:\nA=%s\nB=%s", a, b)
	}
}

func TestNewScene_emptyIsUsable(t *testing.T) {
	s := NewScene(ModeAsk)
	if s.ActiveMode != ModeAsk {
		t.Errorf("active mode=%q want ask", s.ActiveMode)
	}
	if len(s.Modes) != len(Modes()) {
		t.Errorf("empty scene must still carry all mode tabs")
	}
	if s.Tracks == nil || s.Routing == nil || s.Corrections.Entries == nil {
		t.Error("empty scene slices must be non-nil so JSON emits []")
	}
	if _, err := s.JSON(); err != nil {
		t.Errorf("empty scene must still encode: %v", err)
	}
}

func TestNewScene_unknownModeFallsBackToAsk(t *testing.T) {
	if s := NewScene(Mode("bogus")); s.ActiveMode != ModeAsk {
		t.Errorf("unknown mode should fall back to ask, got %q", s.ActiveMode)
	}
}

func TestLoad_roundTripsFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "project.json")
	if err := os.WriteFile(path, []byte(sampleProjectJSON), 0o644); err != nil {
		t.Fatalf("write sample: %v", err)
	}
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error on a good file: %v", err)
	}
	if s.Title != "project.json" {
		t.Errorf("title=%q, want project.json", s.Title)
	}
	if len(s.Tracks) != 3 {
		t.Errorf("loaded %d tracks, want 3", len(s.Tracks))
	}
}

func TestLoad_missingFileDegrades(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err == nil {
		t.Fatal("expected an error for a missing file")
	}
	// Degrade, never crash: a usable empty DAW scene still comes back.
	if s.ActiveMode != ModeDAW {
		t.Errorf("degraded scene mode=%q, want daw", s.ActiveMode)
	}
	if _, jerr := s.JSON(); jerr != nil {
		t.Errorf("degraded scene must still encode: %v", jerr)
	}
}

func TestSceneFromProjectJSON_malformedDegrades(t *testing.T) {
	s, err := SceneFromProjectJSON([]byte(`{ this is not json `))
	if err == nil {
		t.Fatal("expected a parse error for malformed JSON")
	}
	if s.ActiveMode != ModeDAW {
		t.Errorf("degraded scene mode=%q, want daw", s.ActiveMode)
	}
}

func TestSceneFromProject_zeroPPQFallsBack(t *testing.T) {
	// A project missing ppq must still get a usable timeline scale (compose's 480).
	p := music.Project{Tempo: 120, PPQ: 0, Tracks: []music.ProjTrack{{ID: "x", Out: "bus.music"}}}
	s := SceneFromProject(p)
	if s.Transport.PPQ != music.PPQ {
		t.Errorf("zero-ppq transport PPQ=%d, want fallback %d", s.Transport.PPQ, music.PPQ)
	}
	if s.Viewport.PxPerTick <= 0 {
		t.Errorf("zero-ppq viewport zoom must stay positive, got %v", s.Viewport.PxPerTick)
	}
}
