package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"becky-go/internal/dawmodel"
	"becky-go/internal/music"
)

// writeFixtureProject builds a minimal project.json with one percussion clip and
// writes it to dir, returning its path.
func writeFixtureProject(t *testing.T, dir string) string {
	t.Helper()
	arr := dawmodel.New()
	arr.BPM = 140
	arr = arr.AddTrack("drums", dawmodel.KindMIDI)
	clip := dawmodel.Clip{
		Name: "beat", Channel: 9, Program: -1,
		Notes: []dawmodel.Note{
			{ID: 1, Start: 0, Dur: 60, Pitch: 36, Vel: 100, Ch: 9},    // kick beat 1
			{ID: 2, Start: 480, Dur: 60, Pitch: 38, Vel: 100, Ch: 9},  // snare beat 2
			{ID: 3, Start: 960, Dur: 60, Pitch: 36, Vel: 100, Ch: 9},  // kick beat 3
			{ID: 4, Start: 1440, Dur: 60, Pitch: 38, Vel: 100, Ch: 9}, // snare beat 4
		},
	}
	arr.Tracks[0].Clips = append(arr.Tracks[0].Clips, clip)
	arr.NextID = 4
	data, err := json.MarshalIndent(arr, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "song.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRun_requiresFlags(t *testing.T) {
	if code := run([]string{}); code != exitUsage {
		t.Errorf("no flags should be usage error, got %d", code)
	}
	if code := run([]string{"--project", "x.json"}); code != exitUsage {
		t.Errorf("missing instruction should be usage error, got %d", code)
	}
}

func TestRun_badProject(t *testing.T) {
	if code := run([]string{"--project", "/no/such/file.json", "--instruction", "swing it"}); code != exitErr {
		t.Errorf("missing project file should be runtime error, got %d", code)
	}
}

func TestRun_halfTimeWritesPatchedProject(t *testing.T) {
	dir := t.TempDir()
	proj := writeFixtureProject(t, dir)
	out := filepath.Join(dir, "out.json")
	code := run([]string{"--project", proj, "--instruction", "make it half-time", "--output", out})
	if code != exitOK {
		t.Fatalf("half-time exit = %d, want 0", code)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("output project not written: %v", err)
	}
	// The patched project must still parse as an arrangement.
	data, _ := os.ReadFile(out)
	var arr dawmodel.Arrangement
	if err := json.Unmarshal(data, &arr); err != nil {
		t.Fatalf("patched project is not valid JSON arrangement: %v", err)
	}
	if arr.NoteCount() == 0 {
		t.Error("patched project lost all notes")
	}
}

func TestRun_defaultOutputNextToSource(t *testing.T) {
	dir := t.TempDir()
	proj := writeFixtureProject(t, dir)
	code := run([]string{"--project", proj, "--instruction", "double-time it"})
	if code != exitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	want := filepath.Join(dir, "song.drum.json")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("expected default output %s, got error %v", want, err)
	}
}

func TestRun_dryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	proj := writeFixtureProject(t, dir)
	code := run([]string{"--project", proj, "--instruction", "humanize the drums", "--dry-run"})
	if code != exitOK {
		t.Fatalf("dry-run exit = %d, want 0", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "song.drum.json")); !os.IsNotExist(err) {
		t.Error("dry-run must not write an output file")
	}
}

func TestRun_unknownInstructionDegradesExitZero(t *testing.T) {
	dir := t.TempDir()
	proj := writeFixtureProject(t, dir)
	code := run([]string{"--project", proj, "--instruction", "make it taste like blue"})
	if code != exitOK {
		t.Errorf("unknown instruction should degrade to exit 0, got %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "song.drum.json")); !os.IsNotExist(err) {
		t.Error("unknown instruction must not write an output file")
	}
}

func TestRun_logsCorrection(t *testing.T) {
	dir := t.TempDir()
	proj := writeFixtureProject(t, dir)
	out := filepath.Join(dir, "out.json")
	if code := run([]string{"--project", proj, "--instruction", "make it half-time", "--output", out}); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "drum.corrections.jsonl")); err != nil {
		t.Errorf("expected drum.corrections.jsonl to be written: %v", err)
	}
}

func TestRun_determinismByteIdentical(t *testing.T) {
	dir := t.TempDir()
	proj := writeFixtureProject(t, dir)
	out1 := filepath.Join(dir, "a.json")
	out2 := filepath.Join(dir, "b.json")
	run([]string{"--project", proj, "--instruction", "give me 3 variations", "--seed", "5", "--output", out1})
	run([]string{"--project", proj, "--instruction", "give me 3 variations", "--seed", "5", "--output", out2})
	a, _ := os.ReadFile(out1)
	b, _ := os.ReadFile(out2)
	if string(a) != string(b) {
		t.Error("same project + instruction + seed must yield byte-identical output")
	}
}

// writeComposeManifest writes a becky-compose ROUTING MANIFEST (tracks that
// reference external .mid stems, no inline notes) plus its drum stem, into dir.
// Returns the project.json path. This is the shape becky-compose actually emits —
// distinct from the inline-note dawmodel.Arrangement writeFixtureProject builds.
func writeComposeManifest(t *testing.T, dir string) string {
	t.Helper()
	// A drum stem on channel 9 (GM percussion): kick on 1, snare on 2.
	f := music.NewFile(480)
	tr := f.AddTrack()
	tr.Note(0, 120, 9, 36, 100)   // kick beat 1
	tr.Note(480, 120, 9, 38, 100) // snare beat 2
	tr.Note(960, 120, 9, 36, 100) // kick beat 3
	if err := os.WriteFile(filepath.Join(dir, "drums.mid"), f.Bytes(), 0o644); err != nil {
		t.Fatalf("write drum stem: %v", err)
	}
	proj := music.Project{
		SchemaVersion: 1,
		Tool:          "becky-compose",
		Genre:         "crunkcore",
		Tempo:         140,
		TimeSignature: []int{4, 4},
		Key:           music.ProjKey{Root: "F", Scale: "minor"},
		PPQ:           480,
		Tracks: []music.ProjTrack{
			{ID: "drums", Midi: "drums.mid", Channel: 9, Kind: "percussion", Node: "src.drums", Out: "bus.drums"},
		},
		Buses: []music.ProjBus{
			{ID: "bus.drums", Out: "bus.master"},
			{ID: "bus.master", Out: "out.main"},
		},
	}
	raw, err := json.MarshalIndent(proj, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	path := filepath.Join(dir, "project.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write project.json: %v", err)
	}
	return path
}

// TestRun_composeManifestIsEditable proves the seam that was silently broken:
// becky-drum must accept a becky-compose routing manifest (resolving its .mid
// stems via composearr) so "compose a beat, then tweak it in plain English"
// works end to end. Before the fix, this returned "no MIDI drum clip found".
func TestRun_composeManifestIsEditable(t *testing.T) {
	dir := t.TempDir()
	proj := writeComposeManifest(t, dir)
	out := filepath.Join(dir, "edited.json")
	if code := run([]string{"--project", proj, "--instruction", "make it half-time", "--output", out}); code != exitOK {
		t.Fatalf("compose manifest should be editable, exit = %d", code)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("expected an output arrangement: %v", err)
	}
	var arr dawmodel.Arrangement
	if err := json.Unmarshal(data, &arr); err != nil {
		t.Fatalf("output is not a valid arrangement: %v", err)
	}
	// The percussion stem must have been loaded into inline notes (proof the
	// stem was actually resolved, not an empty arrangement).
	var notes int
	for _, tr := range arr.Tracks {
		for _, c := range tr.Clips {
			notes += len(c.Notes)
		}
	}
	if notes == 0 {
		t.Error("compose manifest produced an arrangement with no notes — stem was not resolved")
	}
	// The original manifest must be untouched (non-destructive).
	orig, _ := os.ReadFile(proj)
	if !json.Valid(orig) {
		t.Error("original compose manifest was corrupted")
	}
}

// TestIsComposeManifest covers the detection heuristic directly.
func TestIsComposeManifest(t *testing.T) {
	cases := []struct {
		name string
		json string
		want bool
	}{
		{"tool stamp", `{"tool":"becky-compose","tracks":[]}`, true},
		{"midi ref", `{"tracks":[{"midi":"drums.mid"}]}`, true},
		{"inline arrangement", `{"bpm":140,"tracks":[{"id":"d","clips":[{"notes":[{"pitch":36}]}]}]}`, false},
		{"empty", `{}`, false},
		{"garbage", `not json`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isComposeManifest([]byte(tc.json)); got != tc.want {
				t.Errorf("isComposeManifest(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestResolveOutput(t *testing.T) {
	if got := resolveOutput("/x/song.json", ""); got != filepath.Join("/x", "song.drum.json") {
		t.Errorf("default output = %q", got)
	}
	if got := resolveOutput("/x/song.json", "/y/explicit.json"); got != "/y/explicit.json" {
		t.Errorf("explicit output should win, got %q", got)
	}
	// Windows-style path handled via pathx.
	if got := resolveOutput(`C:\beats\song.json`, ""); got == "" {
		t.Error("windows path should resolve to a non-empty output")
	}
}

// TestFindDrumClip_prefersChannel9NonEmpty is a regression guard: a non-drum
// track whose program is -1 (instrument simply unknown) and that has no notes
// must NOT shadow the real GM-percussion clip on channel 9. This was a real bug
// — arrangements loaded from multi-file projects carry a program -1 placeholder
// track first, and the empty clip was being picked, yielding "nothing to change".
func TestFindDrumClip_prefersChannel9NonEmpty(t *testing.T) {
	arr := dawmodel.New()
	// First track: program -1, channel 0, NO notes (the decoy that won before).
	arr = arr.AddTrack("lead", dawmodel.KindMIDI)
	arr.Tracks[0].Clips = append(arr.Tracks[0].Clips, dawmodel.Clip{
		Name: "lead", Channel: 0, Program: -1,
	})
	// Second track: the real drum clip on channel 9 with notes.
	arr = arr.AddTrack("drums", dawmodel.KindMIDI)
	arr.Tracks[1].Clips = append(arr.Tracks[1].Clips, dawmodel.Clip{
		Name: "beat", Channel: 9, Program: -1,
		Notes: []dawmodel.Note{{ID: 1, Start: 0, Dur: 60, Pitch: 36, Vel: 100, Ch: 9}},
	})
	track, clip := findDrumClip(arr)
	if track != "drums" || clip != "beat" {
		t.Fatalf("findDrumClip = (%q,%q); want (drums,beat) — empty program -1 clip must not shadow channel 9", track, clip)
	}
}
