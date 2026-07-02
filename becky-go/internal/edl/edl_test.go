package edl

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeFile(path, content string) error { return os.WriteFile(path, []byte(content), 0o644) }
func readFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}

func TestSecondsToTimecode(t *testing.T) {
	tests := []struct {
		name string
		sec  float64
		fps  float64
		want string
	}{
		{"zero", 0, 30, "00:00:00:00"},
		{"ten seconds 30fps", 10.0, 30, "00:00:10:00"},
		{"frame 315 = 10s 15f at 30fps", 10.5, 30, "00:00:10:15"},
		{"one frame at 30fps", 1.0 / 30.0, 30, "00:00:00:01"},
		{"one hour", 3600, 30, "01:00:00:00"},
		{"mixed h/m/s/f", 3661.5, 30, "01:01:01:15"},
		{"25fps last frame of second", 24.0 / 25.0, 25, "00:00:00:24"},
		{"25fps rolls to next second", 1.0, 25, "00:00:01:00"},
		{"ntsc 29.97 labels at 30", 1.0, 29.97, "00:00:01:00"},
		{"negative clamps to zero", -5, 30, "00:00:00:00"},
		{"zero fps falls back to default", 1.0, 0, "00:00:01:00"},
		{"frame field wraps not 30", 1.0 + 29.0/30.0, 30, "00:00:01:29"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SecondsToTimecode(tc.sec, tc.fps)
			if got != tc.want {
				t.Fatalf("SecondsToTimecode(%v, %v) = %q, want %q", tc.sec, tc.fps, got, tc.want)
			}
		})
	}
}

func TestSecondsToTimecodeDeterministic(t *testing.T) {
	// Same input must always produce the same output (becky invariant).
	for i := 0; i < 100; i++ {
		if SecondsToTimecode(12.345, 30) != "00:00:12:10" {
			t.Fatalf("non-deterministic timecode at iteration %d", i)
		}
	}
}

func TestClipDur(t *testing.T) {
	tests := []struct {
		name string
		clip Clip
		want float64
	}{
		{"normal", Clip{In: 10, Out: 12}, 2},
		{"zero length", Clip{In: 5, Out: 5}, 0},
		{"negative clamps to zero", Clip{In: 12, Out: 10}, 0},
		{"fractional", Clip{In: 1.5, Out: 4.25}, 2.75},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.clip.Dur(); got != tc.want {
				t.Fatalf("Dur() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestReelDuration(t *testing.T) {
	r := Reel{Clips: []Clip{
		{In: 10, Out: 12}, // 2s
		{In: 3, Out: 5},   // 2s
		{In: 0, Out: 1.5}, // 1.5s
		{In: 5, Out: 5},   // 0s
	}}
	if got := r.Duration(); got != 5.5 {
		t.Fatalf("Duration() = %v, want 5.5", got)
	}
	if (Reel{}).Duration() != 0 {
		t.Fatalf("empty reel Duration() should be 0")
	}
}

func TestClipFPS(t *testing.T) {
	tests := []struct {
		name     string
		clip     Clip
		fallback float64
		want     float64
	}{
		{"meta wins", Clip{Meta: ClipMeta{SourceFPS: 24}}, 30, 24},
		{"fallback when no meta", Clip{}, 25, 25},
		{"default when nothing", Clip{}, 0, DefaultFPS},
		{"zero meta uses fallback", Clip{Meta: ClipMeta{SourceFPS: 0}}, 60, 60},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.clip.FPS(tc.fallback); got != tc.want {
				t.Fatalf("FPS(%v) = %v, want %v", tc.fallback, got, tc.want)
			}
		})
	}
}

func sampleReel() Reel {
	return Reel{
		Version: "1",
		Name:    "penguin-bounty",
		Clips: []Clip{
			{
				ID: "c1", Source: `X:\case\interview1.mp4`,
				In: 10.0, Out: 12.0, Label: "I'll pay $500 for the cat",
				Meta: ClipMeta{Date: "2026-06-18", Person: "J.DOE", Location: "KITCHEN", SourceFPS: 30},
			},
			{
				ID: "c2", Source: `X:\case\interview2.mp4`,
				In: 3.0, Out: 5.0, Label: "bring me Penguin and there's a reward",
				Meta: ClipMeta{Date: "2026-06-19", SourceFPS: 25},
			},
			{
				// Same source as c1 -> must reuse the same reel alias in the EDL.
				ID: "c3", Source: `X:\case\interview1.mp4`,
				In: 30.0, Out: 31.0, Label: "",
				Meta: ClipMeta{SourceFPS: 30},
			},
		},
		Overlay: Overlay{Enabled: true, ShowFilename: true, ShowTimecode: true, Position: "bottom"},
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reel.json")

	orig := sampleReel()
	if err := Save(path, orig); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(orig, got) {
		t.Fatalf("round-trip mismatch:\n orig=%+v\n got =%+v", orig, got)
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("Load of missing file should error")
	}
}

func TestLoadBadJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := writeFile(path, "{not json"); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load of malformed JSON should error")
	}
}

func TestLoadDefaultsVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.json")
	if err := writeFile(path, `{"name":"x","clips":[]}`); err != nil {
		t.Fatal(err)
	}
	r, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if r.Version != "1" {
		t.Fatalf("Version not defaulted: %q", r.Version)
	}
}

func TestSavePrettyJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.json")
	if err := Save(path, sampleReel()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := readFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(data, "\n  \"version\": \"1\"") {
		t.Fatalf("expected 2-space indented JSON, got:\n%s", data)
	}
	if !strings.HasSuffix(data, "\n") {
		t.Fatal("expected trailing newline")
	}
}

func TestWriteEDL(t *testing.T) {
	var sb strings.Builder
	if err := WriteEDL(&sb, sampleReel()); err != nil {
		t.Fatalf("WriteEDL: %v", err)
	}
	out := sb.String()

	mustContain := []string{
		"TITLE: penguin-bounty",
		"FCM: NON-DROP FRAME",
		// The channel field must carry audio, not bare "V" (video-only) — a Vegas
		// Pro EDL import with "V" produced no audio track (Jordan's real bug).
		"AA/V  C",
		// Event 1: src 10.0->12.0 @30fps = 00:00:10:00 -> 00:00:12:00; record 0->2s.
		"00:00:10:00 00:00:12:00 00:00:00:00 00:00:02:00",
		// Event 2: src 3.0->5.0 @25fps; record contiguous from 2s -> 4s @30 (record fps = first clip 30).
		"00:00:03:00 00:00:05:00 00:00:02:00 00:00:04:00",
		// FROM CLIP NAME comments carry the real basenames.
		"* FROM CLIP NAME: interview1.mp4",
		"* FROM CLIP NAME: interview2.mp4",
		"* COMMENT: I'll pay $500 for the cat",
	}
	for _, want := range mustContain {
		if !strings.Contains(out, want) {
			t.Fatalf("EDL missing %q in:\n%s", want, out)
		}
	}

	// Event numbering is sequential and zero-padded.
	for _, want := range []string{"001  ", "002  ", "003  "} {
		if !strings.Contains(out, want) {
			t.Fatalf("EDL missing event line %q in:\n%s", want, out)
		}
	}

	// The same source (c1 and c3 are interview1.mp4) must reuse one reel alias.
	if c := strings.Count(out, "BL000001"); c < 2 {
		t.Fatalf("expected reel BL000001 reused for the repeated source, count=%d:\n%s", c, out)
	}
	// Two distinct sources -> exactly two reel aliases.
	if strings.Contains(out, "BL000003") {
		t.Fatalf("a third reel alias was emitted for only two sources:\n%s", out)
	}
}

func TestWriteEDLEmptyName(t *testing.T) {
	var sb strings.Builder
	if err := WriteEDL(&sb, Reel{Clips: []Clip{{ID: "c1", Source: "a.mp4", In: 0, Out: 1}}}); err != nil {
		t.Fatalf("WriteEDL: %v", err)
	}
	if !strings.Contains(sb.String(), "TITLE: BECKY-REEL") {
		t.Fatalf("empty name should default TITLE, got:\n%s", sb.String())
	}
}

func TestWriteEDLEmptyReel(t *testing.T) {
	var sb strings.Builder
	if err := WriteEDL(&sb, Reel{Name: "empty"}); err != nil {
		t.Fatalf("WriteEDL: %v", err)
	}
	out := sb.String()
	if !strings.Contains(out, "TITLE: empty") || !strings.Contains(out, "FCM: NON-DROP FRAME") {
		t.Fatalf("empty reel should still emit header:\n%s", out)
	}
	if strings.Contains(out, "001  ") {
		t.Fatalf("empty reel should emit no events:\n%s", out)
	}
}

func TestWriteEDLWindowsPathBasename(t *testing.T) {
	// pathx.Base must extract the basename from a Windows path even on Linux/CI.
	var sb strings.Builder
	r := Reel{Name: "w", Clips: []Clip{{ID: "c1", Source: `C:\Users\only1\evidence\clip A.mp4`, In: 0, Out: 1, Meta: ClipMeta{SourceFPS: 30}}}}
	if err := WriteEDL(&sb, r); err != nil {
		t.Fatalf("WriteEDL: %v", err)
	}
	if !strings.Contains(sb.String(), "* FROM CLIP NAME: clip A.mp4") {
		t.Fatalf("Windows basename not extracted:\n%s", sb.String())
	}
}

func TestWriteSRT(t *testing.T) {
	var sb strings.Builder
	if err := WriteSRT(&sb, sampleReel()); err != nil {
		t.Fatalf("WriteSRT: %v", err)
	}
	out := sb.String()

	// c1: 0 -> 2s on the compilation timeline.
	if !strings.Contains(out, "00:00:00,000 --> 00:00:02,000") {
		t.Fatalf("SRT missing c1 re-based cue:\n%s", out)
	}
	// c2: 2 -> 4s (re-based, NOT its 3-5s source position).
	if !strings.Contains(out, "00:00:02,000 --> 00:00:04,000") {
		t.Fatalf("SRT missing c2 re-based cue:\n%s", out)
	}
	if !strings.Contains(out, "I'll pay $500 for the cat") ||
		!strings.Contains(out, "bring me Penguin and there's a reward") {
		t.Fatalf("SRT missing labels:\n%s", out)
	}
	// c3 has an empty label -> no cue; only two cues total (indices 1 and 2).
	if strings.Contains(out, "3\r\n") {
		t.Fatalf("SRT should have only 2 cues (c3 has no label):\n%s", out)
	}
	if !strings.HasPrefix(out, "1\r\n") {
		t.Fatalf("SRT should start with cue index 1:\n%s", out)
	}
}

func TestWriteSRTSkippedClipStillAdvancesTimeline(t *testing.T) {
	// A labelless clip in the MIDDLE must still shift later cues' timing.
	r := Reel{Clips: []Clip{
		{ID: "c1", In: 0, Out: 2, Label: "first"},  // 0-2
		{ID: "c2", In: 0, Out: 3, Label: ""},       // 2-5 (no cue)
		{ID: "c3", In: 0, Out: 1, Label: "second"}, // 5-6
	}}
	var sb strings.Builder
	if err := WriteSRT(&sb, r); err != nil {
		t.Fatalf("WriteSRT: %v", err)
	}
	out := sb.String()
	if !strings.Contains(out, "00:00:00,000 --> 00:00:02,000") {
		t.Fatalf("first cue wrong:\n%s", out)
	}
	// second cue must start at 5s, proving the labelless clip's 3s counted.
	if !strings.Contains(out, "00:00:05,000 --> 00:00:06,000") {
		t.Fatalf("second cue should start at 5s (labelless clip advanced timeline):\n%s", out)
	}
}

func TestWriteSRTZeroDurationClip(t *testing.T) {
	r := Reel{Clips: []Clip{{ID: "c1", In: 5, Out: 5, Label: "instant"}}}
	var sb strings.Builder
	if err := WriteSRT(&sb, r); err != nil {
		t.Fatalf("WriteSRT: %v", err)
	}
	if !strings.Contains(sb.String(), "00:00:00,000 --> 00:00:00,500") {
		t.Fatalf("zero-duration clip should get a minimum cue window:\n%s", sb.String())
	}
}

func TestWriteSRTLabelNewlinesFlattened(t *testing.T) {
	r := Reel{Clips: []Clip{{ID: "c1", In: 0, Out: 1, Label: "line one\nline two"}}}
	var sb strings.Builder
	if err := WriteSRT(&sb, r); err != nil {
		t.Fatalf("WriteSRT: %v", err)
	}
	if !strings.Contains(sb.String(), "line one line two") {
		t.Fatalf("label newlines should be flattened to a single line:\n%s", sb.String())
	}
}
