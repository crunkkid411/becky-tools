package otio

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"becky-go/internal/edl"
)

// twoClipReel: clip A at 30fps (65.0->73.5s), clip B at 25fps (120->128s).
func twoClipReel() edl.Reel {
	return edl.Reel{
		Version: "1",
		Name:    "cat-tooth",
		Clips: []edl.Clip{
			{ID: "c1", Source: `C:\Videos\cam1.mp4`, In: 65.0, Out: 73.5, Label: "cat closeup",
				Meta: edl.ClipMeta{SourceFPS: 30}},
			{ID: "c2", Source: `C:\Videos\cam2.mp4`, In: 120, Out: 128, Label: "",
				Meta: edl.ClipMeta{SourceFPS: 25}},
		},
	}
}

func TestWriteOTIO_FrameMathAndStructure(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteOTIO(&buf, twoClipReel(), Options{}); err != nil {
		t.Fatalf("WriteOTIO: %v", err)
	}

	var tl map[string]any
	if err := json.Unmarshal(buf.Bytes(), &tl); err != nil {
		t.Fatalf("emitted OTIO is not valid JSON: %v", err)
	}
	if got := tl["OTIO_SCHEMA"]; got != "Timeline.1" {
		t.Errorf("timeline schema = %v, want Timeline.1", got)
	}
	if got := tl["name"]; got != "cat-tooth" {
		t.Errorf("timeline name = %v, want cat-tooth", got)
	}

	tracks := tl["tracks"].(map[string]any)["children"].([]any)
	if len(tracks) != 1 {
		t.Fatalf("track count = %d, want 1 (video-only default)", len(tracks))
	}
	v := tracks[0].(map[string]any)
	if v["kind"] != "Video" {
		t.Errorf("track kind = %v, want Video", v["kind"])
	}
	clips := v["children"].([]any)
	if len(clips) != 2 {
		t.Fatalf("clip count = %d, want 2", len(clips))
	}

	// Clip A: 30fps, in 65.0s -> 1950 frames, dur 8.5s -> 255 frames.
	a := clips[0].(map[string]any)
	if a["name"] != "cat closeup" {
		t.Errorf("clip A name = %v, want 'cat closeup'", a["name"])
	}
	aSrc := a["source_range"].(map[string]any)
	aStart := aSrc["start_time"].(map[string]any)
	aDur := aSrc["duration"].(map[string]any)
	if aStart["value"].(float64) != 1950 {
		t.Errorf("clip A start frames = %v, want 1950", aStart["value"])
	}
	if aStart["rate"].(float64) != 30 {
		t.Errorf("clip A rate = %v, want 30", aStart["rate"])
	}
	if aDur["value"].(float64) != 255 {
		t.Errorf("clip A duration frames = %v, want 255", aDur["value"])
	}
	if url := a["media_reference"].(map[string]any)["target_url"]; url != "file:///C:/Videos/cam1.mp4" {
		t.Errorf("clip A target_url = %v, want file:///C:/Videos/cam1.mp4", url)
	}

	// Clip B: 25fps, in 120s -> 3000 frames, dur 8s -> 200 frames; blank label -> basename.
	b := clips[1].(map[string]any)
	if b["name"] != "cam2.mp4" {
		t.Errorf("clip B name = %v, want basename cam2.mp4", b["name"])
	}
	bSrc := b["source_range"].(map[string]any)
	if bSrc["start_time"].(map[string]any)["value"].(float64) != 3000 {
		t.Errorf("clip B start frames = %v, want 3000", bSrc["start_time"].(map[string]any)["value"])
	}
	if bSrc["duration"].(map[string]any)["value"].(float64) != 200 {
		t.Errorf("clip B duration frames = %v, want 200", bSrc["duration"].(map[string]any)["value"])
	}
}

func TestWriteOTIO_IncludeAudioAddsParallelTrack(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteOTIO(&buf, twoClipReel(), Options{IncludeAudio: true}); err != nil {
		t.Fatalf("WriteOTIO: %v", err)
	}
	var tl map[string]any
	_ = json.Unmarshal(buf.Bytes(), &tl)
	tracks := tl["tracks"].(map[string]any)["children"].([]any)
	if len(tracks) != 2 {
		t.Fatalf("track count = %d, want 2 (video+audio)", len(tracks))
	}
	if tracks[1].(map[string]any)["kind"] != "Audio" {
		t.Errorf("second track kind = %v, want Audio", tracks[1].(map[string]any)["kind"])
	}
}

func TestWriteOTIO_Deterministic(t *testing.T) {
	var a, b bytes.Buffer
	_ = WriteOTIO(&a, twoClipReel(), Options{})
	_ = WriteOTIO(&b, twoClipReel(), Options{})
	if a.String() != b.String() {
		t.Error("WriteOTIO is not deterministic: two runs differ")
	}
}

func TestWriteOTIO_SkipsDegenerateClip(t *testing.T) {
	r := twoClipReel()
	r.Clips = append(r.Clips, edl.Clip{ID: "bad", Source: `C:\x.mp4`, In: 10, Out: 10}) // out==in
	var buf bytes.Buffer
	_ = WriteOTIO(&buf, r, Options{})
	var tl map[string]any
	_ = json.Unmarshal(buf.Bytes(), &tl)
	clips := tl["tracks"].(map[string]any)["children"].([]any)[0].(map[string]any)["children"].([]any)
	if len(clips) != 2 {
		t.Errorf("clip count = %d, want 2 (degenerate clip skipped)", len(clips))
	}
}

func TestWriteVegasList_ExactLines(t *testing.T) {
	var buf bytes.Buffer
	n, err := WriteVegasList(&buf, twoClipReel())
	if err != nil {
		t.Fatalf("WriteVegasList: %v", err)
	}
	if n != 2 {
		t.Fatalf("wrote %d clips, want 2", n)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 { // 1 comment header + 2 clips
		t.Fatalf("line count = %d, want 3 (header + 2 clips)\n%s", len(lines), buf.String())
	}
	if !strings.HasPrefix(lines[0], "#") {
		t.Errorf("line 0 = %q, want a # comment header", lines[0])
	}
	wantA := `C:\Videos\cam1.mp4 | 65 | 73.5 | cat closeup`
	if lines[1] != wantA {
		t.Errorf("clip A line:\n got %q\nwant %q", lines[1], wantA)
	}
	wantB := `C:\Videos\cam2.mp4 | 120 | 128 | cam2.mp4` // blank label -> basename
	if lines[2] != wantB {
		t.Errorf("clip B line:\n got %q\nwant %q", lines[2], wantB)
	}
}

func TestWriteVegasList_StripsPipeFromLabel(t *testing.T) {
	r := edl.Reel{Clips: []edl.Clip{
		{Source: "/v/a.mp4", In: 1, Out: 2, Label: "a | b | c"},
	}}
	var buf bytes.Buffer
	_, _ = WriteVegasList(&buf, r)
	// Inspect the clip line only (the comment header legitimately contains pipes).
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	clipLine := lines[len(lines)-1]
	if strings.Count(clipLine, "|") != 3 { // exactly the 3 field delimiters, none from the label
		t.Errorf("clip-line pipe count = %d, want 3 (label pipes stripped)\n%s", strings.Count(clipLine, "|"), clipLine)
	}
	if !strings.Contains(clipLine, "a / b / c") {
		t.Errorf("label pipes not converted to '/': %q", clipLine)
	}
}

func TestFileURL(t *testing.T) {
	cases := map[string]string{
		`C:\Videos\cam1.mp4`: "file:///C:/Videos/cam1.mp4",
		`/home/u/v.mp4`:      "file:///home/u/v.mp4",
		`D:/already/fwd.mp4`: "file:///D:/already/fwd.mp4",
		`file:///x.mp4`:      "file:///x.mp4", // already a URL
	}
	for in, want := range cases {
		if got := FileURL(in); got != want {
			t.Errorf("FileURL(%q) = %q, want %q", in, got, want)
		}
	}
}
