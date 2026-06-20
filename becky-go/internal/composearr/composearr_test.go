package composearr

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"becky-go/internal/dawmodel"
	"becky-go/internal/music"
	"becky-go/internal/reaper"
)

// writeStem writes a minimal valid .mid stem (one track, a couple of notes) at
// 480 PPQ so FromSMF/reaper agree on resolution.
func writeStem(t *testing.T, dir, name string, ch int, prog int, notes [][3]int) {
	t.Helper()
	f := music.NewFile(480)
	tr := f.AddTrack()
	if prog >= 0 {
		tr.Program(0, ch, prog)
	}
	for _, n := range notes { // n = {start, dur, key}
		tr.Note(n[0], n[1], ch, n[2], 100)
	}
	if err := os.WriteFile(filepath.Join(dir, name), f.Bytes(), 0o644); err != nil {
		t.Fatalf("write stem %s: %v", name, err)
	}
}

// sampleProject writes a 2-stem crunkcore-shaped manifest (bass -> bus.808,
// drums -> bus.drums) with a compressor on bus.music and two sidechain edges, plus
// the referenced stems, into a fresh temp dir. Returns the project.json path.
func sampleProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeStem(t, dir, "bass.mid", 0, 38, [][3]int{{0, 480, 33}, {480, 480, 36}})
	writeStem(t, dir, "drums.mid", 9, -1, [][3]int{{0, 120, 36}, {240, 120, 38}})

	proj := music.Project{
		SchemaVersion: 1,
		Tool:          "becky-compose",
		Genre:         "crunkcore",
		Tempo:         150,
		TimeSignature: []int{4, 4},
		Key:           music.ProjKey{Root: "F", Scale: "minor"},
		PPQ:           480,
		Tracks: []music.ProjTrack{
			{ID: "bass", Midi: "bass.mid", Channel: 0, Kind: "instrument", Program: 38, Node: "src.bass", Out: "bus.808"},
			{ID: "drums", Midi: "drums.mid", Channel: 9, Kind: "percussion", Node: "src.drums", Out: "bus.drums"},
		},
		Buses: []music.ProjBus{
			{ID: "bus.808", Out: "bus.master"},
			{ID: "bus.drums", Out: "bus.master"},
			{ID: "bus.music", Out: "bus.master", FX: []music.ProjFX{{Type: "compressor", ID: "comp.music"}}},
			{ID: "bus.master", Out: "out.main"},
		},
		Routing: []music.ProjEdge{
			{From: "src.drums.kick", To: "bus.808.compressor.sidechain", Kind: "sidechain"},
			{From: "src.drums.kick", To: "comp.music.sidechain", Kind: "sidechain"},
		},
	}
	raw, err := json.MarshalIndent(proj, "", "  ")
	if err != nil {
		t.Fatalf("marshal project: %v", err)
	}
	path := filepath.Join(dir, "project.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write project.json: %v", err)
	}
	return path
}

func TestLoadProject(t *testing.T) {
	path := sampleProject(t)
	proj, base, err := LoadProject(path)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	if base != filepath.Dir(path) {
		t.Errorf("baseDir = %q, want %q", base, filepath.Dir(path))
	}
	if proj.Tempo != 150 || proj.Genre != "crunkcore" || len(proj.Tracks) != 2 {
		t.Errorf("unexpected project: tempo=%d genre=%q tracks=%d", proj.Tempo, proj.Genre, len(proj.Tracks))
	}
}

func TestLoadProject_BadFile(t *testing.T) {
	if _, _, err := LoadProject(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("expected error for missing project.json")
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(bad, []byte("{not json"), 0o644)
	if _, _, err := LoadProject(bad); err == nil {
		t.Fatal("expected parse error for malformed json")
	}
}

func TestFromProject_TransportAndTracks(t *testing.T) {
	path := sampleProject(t)
	proj, base, _ := LoadProject(path)
	a, err := FromProject(proj, base)
	if err != nil {
		t.Fatalf("FromProject: %v", err)
	}

	// Transport lifted from the manifest.
	if a.BPM != 150 || a.PPQ != 480 || a.Num != 4 || a.Den != 4 {
		t.Errorf("transport = bpm:%d ppq:%d %d/%d, want 150/480 4/4", a.BPM, a.PPQ, a.Num, a.Den)
	}
	if a.Root != "F" || a.Scale != "minor" || a.Genre != "crunkcore" {
		t.Errorf("key/genre = %s %s %s, want F minor crunkcore", a.Root, a.Scale, a.Genre)
	}

	// One track per stem, routed to its declared bus, with notes loaded.
	if len(a.Tracks) != 2 {
		t.Fatalf("tracks = %d, want 2", len(a.Tracks))
	}
	wantBus := map[string]string{"bass": "bus.808", "drums": "bus.drums"}
	totalNotes := 0
	for _, tr := range a.Tracks {
		if tr.Strip.Bus != wantBus[tr.ID] {
			t.Errorf("track %s bus = %q, want %q", tr.ID, tr.Strip.Bus, wantBus[tr.ID])
		}
		if tr.Kind != dawmodel.KindMIDI {
			t.Errorf("track %s kind = %q, want midi", tr.ID, tr.Kind)
		}
		for _, c := range tr.Clips {
			totalNotes += len(c.Notes)
		}
	}
	if totalNotes != 4 { // 2 bass + 2 drum notes
		t.Errorf("total notes = %d, want 4", totalNotes)
	}
}

func TestFromProject_NoteIDsUniqueAndMonotonic(t *testing.T) {
	proj, base, _ := LoadProject(sampleProject(t))
	a, _ := FromProject(proj, base)
	seen := map[uint64]bool{}
	for _, tr := range a.Tracks {
		for _, c := range tr.Clips {
			for _, n := range c.Notes {
				if n.ID == 0 {
					t.Error("note id 0 (sentinel reused)")
				}
				if seen[n.ID] {
					t.Errorf("duplicate note id %d across merged stems", n.ID)
				}
				seen[n.ID] = true
			}
		}
	}
	if uint64(len(seen)) >= a.NextID {
		t.Errorf("NextID %d must exceed allocated ids (%d)", a.NextID, len(seen))
	}
}

func TestFromProject_BusesAndSidechain(t *testing.T) {
	proj, base, _ := LoadProject(sampleProject(t))
	a, _ := FromProject(proj, base)

	if len(a.Buses) != 4 {
		t.Fatalf("buses = %d, want 4", len(a.Buses))
	}
	byID := map[string]dawmodel.Bus{}
	for _, b := range a.Buses {
		byID[b.ID] = b
	}
	// "bus.808.compressor.sidechain" -> direct bus-id prefix match.
	if got := byID["bus.808"].Sidechain; len(got) != 1 || got[0] != "src.drums.kick" {
		t.Errorf("bus.808 sidechain = %v, want [src.drums.kick]", got)
	}
	// "comp.music.sidechain" -> resolves via the FX-on-bus map to bus.music.
	if got := byID["bus.music"].Sidechain; len(got) != 1 || got[0] != "src.drums.kick" {
		t.Errorf("bus.music sidechain = %v, want [src.drums.kick]", got)
	}
}

func TestSidechainTargetBus(t *testing.T) {
	buses := []music.ProjBus{
		{ID: "bus.808", Out: "bus.master"},
		{ID: "bus.music", Out: "bus.master", FX: []music.ProjFX{{ID: "comp.music"}}},
	}
	fxBus := map[string]string{"comp.music": "bus.music"}
	cases := []struct{ to, want string }{
		{"bus.808.compressor.sidechain", "bus.808"},
		{"comp.music.sidechain", "bus.music"},
		{"bus.808", "bus.808"},
		{"unknown.node.sidechain", ""},
	}
	for _, c := range cases {
		if got := sidechainTargetBus(c.to, buses, fxBus); got != c.want {
			t.Errorf("sidechainTargetBus(%q) = %q, want %q", c.to, got, c.want)
		}
	}
}

func TestFromProject_DegradeOnMissingStem(t *testing.T) {
	proj, base, _ := LoadProject(sampleProject(t))
	proj.Tracks = append(proj.Tracks, music.ProjTrack{
		ID: "ghost", Midi: "does-not-exist.mid", Out: "bus.music",
	})
	a, err := FromProject(proj, base)
	if err == nil {
		t.Fatal("expected error naming the missing stem")
	}
	// Partial result kept: the two good stems still converted.
	if len(a.Tracks) != 2 {
		t.Errorf("tracks = %d, want 2 (ghost skipped)", len(a.Tracks))
	}
}

func TestFromProject_Deterministic(t *testing.T) {
	proj, base, _ := LoadProject(sampleProject(t))
	a1, _ := FromProject(proj, base)
	a2, _ := FromProject(proj, base)
	j1, _ := json.Marshal(a1)
	j2, _ := json.Marshal(a2)
	if string(j1) != string(j2) {
		t.Error("FromProject not deterministic for identical input")
	}
}

// The whole point: a compose project becomes a REAPER session with one bus FOLDER
// per shared bus (the Cubase-style tree), via reaper.FromArrangement.
func TestFromProject_ReaperFolderTree(t *testing.T) {
	proj, base, _ := LoadProject(sampleProject(t))
	a, _ := FromProject(proj, base)
	p := reaper.FromArrangement(a, "")

	folders := 0
	for _, tr := range p.Tracks {
		if tr.FolderStart {
			folders++
		}
	}
	if folders != 2 { // bus.808 + bus.drums each get a folder parent
		t.Errorf("reaper folder parents = %d, want 2 (BASS_bus + DRUMS_bus)", folders)
	}
	if len(p.Tracks) != 4 { // 2 folder parents + 2 member tracks
		t.Errorf("reaper tracks = %d, want 4", len(p.Tracks))
	}
}
