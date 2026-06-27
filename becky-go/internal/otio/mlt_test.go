package otio

import (
	"bytes"
	"encoding/xml"
	"testing"
)

// mltParse is the minimal view of the emitted MLT document the tests assert on.
type mltParse struct {
	XMLName   xml.Name `xml:"mlt"`
	Producers []struct {
		ID string `xml:"id,attr"`
	} `xml:"producer"`
	Playlists []struct {
		ID      string `xml:"id,attr"`
		Entries []struct {
			Producer string `xml:"producer,attr"`
			In       int    `xml:"in,attr"`
			Out      int    `xml:"out,attr"`
		} `xml:"entry"`
	} `xml:"playlist"`
}

func TestWriteMLT_ProducersAndCutFrames(t *testing.T) {
	var buf bytes.Buffer
	n, err := WriteMLT(&buf, twoClipReel(), Options{})
	if err != nil {
		t.Fatalf("WriteMLT: %v", err)
	}
	if n != 2 {
		t.Fatalf("placed %d clips, want 2", n)
	}

	var doc mltParse
	if err := xml.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("emitted MLT is not valid XML: %v", err)
	}
	if len(doc.Producers) != 2 {
		t.Fatalf("producer count = %d, want 2 (one per distinct source)", len(doc.Producers))
	}
	if len(doc.Playlists) != 1 {
		t.Fatalf("playlist count = %d, want 1", len(doc.Playlists))
	}
	entries := doc.Playlists[0].Entries
	if len(entries) != 2 {
		t.Fatalf("entry count = %d, want 2", len(entries))
	}

	// MLT places cuts in TIMELINE-fps frames: the project fps is the first clip's
	// 30fps, and MLT conforms each source to it, so the [in,out] seconds stay
	// time-accurate regardless of a source's native rate.
	// Clip A: in 65.0s -> frame 1950; out 73.5s -> 2205, inclusive last 2204.
	if entries[0].In != 1950 || entries[0].Out != 2204 {
		t.Errorf("entry A frames = [%d,%d], want [1950,2204]", entries[0].In, entries[0].Out)
	}
	// Clip B: in 120s -> 3600; out 128s -> 3840, inclusive last 3839 (at 30fps timeline).
	if entries[1].In != 3600 || entries[1].Out != 3839 {
		t.Errorf("entry B frames = [%d,%d], want [3600,3839]", entries[1].In, entries[1].Out)
	}
}

func TestWriteMLT_ErrorsOnAllDegenerate(t *testing.T) {
	// An all-degenerate Reel produces no placeable clips -> kdenlive.Marshal
	// returns a validation error (surfaced as a warning by the CLI, not a crash).
	r := twoClipReel()
	r.Clips[0].Out = r.Clips[0].In // out == in
	r.Clips[1].Out = r.Clips[1].In
	var buf bytes.Buffer
	if _, err := WriteMLT(&buf, r, Options{}); err == nil {
		t.Error("expected an error for an all-degenerate Reel, got nil")
	}
}
