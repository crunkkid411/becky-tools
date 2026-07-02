package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"becky-go/internal/footage"
)

func TestParseTime(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"00:00:11", 11, true},
		{"0:01:02", 62, true},
		{"01:02", 62, true},
		{"00:01:05,500", 65.5, true},
		{"90", 90, true},
		{"12.5", 12.5, true},
		{"", 0, false},
		{"nope", 0, false},
	}
	for _, c := range cases {
		got, ok := parseTime(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("parseTime(%q) = %v,%v; want %v,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

// writeFixtureFolder creates a temp case folder with one video + its .srt and
// returns the indexed folder.
func writeFixtureFolder(t *testing.T) footage.FolderIndex {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kitchen.mp4"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	srt := "1\r\n00:00:10,000 --> 00:00:13,000\r\nthe cat was returned\r\n\r\n" +
		"2\r\n00:01:00,000 --> 00:01:05,000\r\nnothing happened\r\n"
	if err := os.WriteFile(filepath.Join(dir, "kitchen.srt"), []byte(srt), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, err := footage.Index(dir)
	if err != nil {
		t.Fatal(err)
	}
	return idx
}

func TestBuildReelSnapsToCue(t *testing.T) {
	idx := writeFixtureFolder(t)
	hits := []hit{
		{SRT: "kitchen.srt", T: "00:00:11"},            // snaps to cue 1
		{SRT: "kitchen.srt", T: "0:01:02", Q: "quote"}, // snaps to cue 2, explicit label
		{SRT: "missing.srt", T: "00:00:05"},            // no video -> warning, skipped
	}
	reel, warnings, _ := buildReel(idx, hits, "case", 0.5, 4.0)

	if len(reel.Clips) != 2 {
		t.Fatalf("clips = %d, want 2", len(reel.Clips))
	}
	c0 := reel.Clips[0]
	if c0.In != 9.5 || c0.Out != 13.5 {
		t.Errorf("clip0 window = %v/%v, want 9.5/13.5", c0.In, c0.Out)
	}
	if c0.Label != "the cat was returned" {
		t.Errorf("clip0 label = %q, want cue text", c0.Label)
	}
	if c0.Source != idx.Videos[0].Path {
		t.Errorf("clip0 source = %q, want %q", c0.Source, idx.Videos[0].Path)
	}
	if reel.Clips[1].Label != "quote" {
		t.Errorf("clip1 label = %q, want override", reel.Clips[1].Label)
	}
	if len(warnings) != 1 {
		t.Errorf("warnings = %d, want 1 (the orphan)", len(warnings))
	}
	if !reel.Overlay.Enabled {
		t.Error("forensic overlay should be enabled on a hits reel")
	}
}

// TestEDLSidecar covers Jordan's ask: Open Forensic Hits.bat's output should also
// be directly importable into Vegas Pro. edlPathFor names the sibling beside the
// reel; writeEDLSidecar must produce a real CMX3600 EDL carrying audio (not the
// video-only "V" that dropped Vegas's audio track — see internal/edl/cmx3600.go).
func TestEDLSidecar(t *testing.T) {
	if got, want := edlPathFor(`C:\case\becky-hits.reel.json`), `C:\case\becky-hits.reel.edl`; got != want {
		t.Fatalf("edlPathFor = %q, want %q", got, want)
	}

	idx := writeFixtureFolder(t)
	hits := []hit{{SRT: "kitchen.srt", T: "00:00:11"}}
	reel, _, _ := buildReel(idx, hits, "case", 0.5, 4.0)

	edlPath := filepath.Join(t.TempDir(), "hits.edl")
	if err := writeEDLSidecar(edlPath, reel); err != nil {
		t.Fatalf("writeEDLSidecar: %v", err)
	}
	data, err := os.ReadFile(edlPath)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	if !strings.Contains(out, "AA/V") {
		t.Fatalf("EDL sidecar has no audio channel designator:\n%s", out)
	}
	if !strings.Contains(out, "TITLE: case") {
		t.Fatalf("EDL sidecar missing the reel title:\n%s", out)
	}
}

func TestBuildReelExplicitWindowAndFallback(t *testing.T) {
	idx := writeFixtureFolder(t)
	hits := []hit{
		{SRT: "kitchen.srt", In: "0:30", Out: "0:36"}, // explicit window, no cue snap
		{SRT: "kitchen.srt", T: "0:00:40"},            // between cues -> fixed fallback window
	}
	reel, _, _ := buildReel(idx, hits, "case", 0.5, 4.0)
	if len(reel.Clips) != 2 {
		t.Fatalf("clips = %d, want 2", len(reel.Clips))
	}
	if reel.Clips[0].In != 30 || reel.Clips[0].Out != 36 {
		t.Errorf("explicit window = %v/%v, want 30/36", reel.Clips[0].In, reel.Clips[0].Out)
	}
	if reel.Clips[1].In != 36 || reel.Clips[1].Out != 44 {
		t.Errorf("fallback window = %v/%v, want 36/44", reel.Clips[1].In, reel.Clips[1].Out)
	}
}
