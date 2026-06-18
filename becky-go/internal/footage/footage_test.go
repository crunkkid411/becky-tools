package footage

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile is a tiny fixture helper.
func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// synthFolder builds a tiny case folder: two videos in the root + one in a
// subdir, two with SRT sidecars, one with a beckymeta sidecar, plus a stray
// non-video file. Returns the root.
func synthFolder(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	// Video 1: has transcript + beckymeta.
	writeFile(t, filepath.Join(root, "2026-06-14_ring.mp4"), "not real video bytes")
	writeFile(t, filepath.Join(root, "2026-06-14_ring.srt"),
		"1\n00:00:10,000 --> 00:00:13,000\nI will pay you for the cat\n\n"+
			"2\n00:00:20,000 --> 00:00:22,000\nbring me the cat Penguin\n")
	writeFile(t, filepath.Join(root, "2026-06-14_ring.mp4.beckymeta.json"),
		`{"date":"2026-06-14","source_fps":30,"person":"defendant"}`)

	// Video 2: has an .en.srt transcript, no meta.
	writeFile(t, filepath.Join(root, "doorbell.mov"), "x")
	writeFile(t, filepath.Join(root, "doorbell.en.srt"),
		"1\n00:01:00,000 --> 00:01:04,000\nthe dog is barking\n")

	// Video 3 in a subdir: no transcript.
	writeFile(t, filepath.Join(root, "sub", "clip3.mkv"), "y")

	// Stray non-video file (must be ignored).
	writeFile(t, filepath.Join(root, "notes.txt"), "ignore me")

	return root
}

func TestIndex(t *testing.T) {
	root := synthFolder(t)
	idx, err := Index(root)
	if err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	if len(idx.Videos) != 3 {
		t.Fatalf("Index() found %d videos, want 3: %+v", len(idx.Videos), idx.Videos)
	}

	by := map[string]Video{}
	for _, v := range idx.Videos {
		by[v.Name] = v
	}

	ring, ok := by["2026-06-14_ring.mp4"]
	if !ok {
		t.Fatal("ring video missing from index")
	}
	if !ring.HasTranscript || filepath.Base(ring.TranscriptPath) != "2026-06-14_ring.srt" {
		t.Fatalf("ring transcript = %q (has=%v), want 2026-06-14_ring.srt", ring.TranscriptPath, ring.HasTranscript)
	}
	if ring.Meta.Date != "2026-06-14" || ring.Meta.SourceFPS != 30 || ring.Meta.Person != "defendant" {
		t.Fatalf("ring meta = %+v, want date/fps/person filled", ring.Meta)
	}

	door, ok := by["doorbell.mov"]
	if !ok || !door.HasTranscript {
		t.Fatalf("doorbell should be indexed with its .en.srt; got %+v", door)
	}
	if door.Meta.Date != "" {
		t.Fatalf("doorbell has no meta sidecar; want zero Meta, got %+v", door.Meta)
	}

	clip3, ok := by["clip3.mkv"]
	if !ok || clip3.HasTranscript {
		t.Fatalf("clip3 should be indexed with NO transcript; got %+v", clip3)
	}

	if got := len(idx.WithTranscripts()); got != 2 {
		t.Fatalf("WithTranscripts() = %d, want 2", got)
	}
	if _, ok := idx.VideoByName("doorbell.mov"); !ok {
		t.Fatal("VideoByName(doorbell.mov) should resolve")
	}
	if _, ok := idx.VideoByName("nope.mp4"); ok {
		t.Fatal("VideoByName(nope.mp4) should not resolve")
	}
}

func TestIndexMissingFolderDegrades(t *testing.T) {
	idx, err := Index(filepath.Join(t.TempDir(), "does-not-exist"))
	// A non-nil error here is acceptable (root truly absent); the contract is
	// "never panic, never invent videos".
	if len(idx.Videos) != 0 {
		t.Fatalf("missing folder yielded %d videos, want 0 (err=%v)", len(idx.Videos), err)
	}
}

func TestGrepTranscripts(t *testing.T) {
	root := synthFolder(t)
	idx, err := Index(root)
	if err != nil {
		t.Fatal(err)
	}

	cands := GrepTranscripts(idx, []string{"cat", "Penguin"})
	if len(cands) != 2 {
		t.Fatalf("grep cat/Penguin = %d candidates, want 2: %+v", len(cands), cands)
	}
	// The cue that hits BOTH terms ("bring me the cat Penguin") must rank first.
	if cands[0].Text != "bring me the cat Penguin" {
		t.Fatalf("top candidate = %q, want the two-term hit first", cands[0].Text)
	}
	if cands[0].Timestamp != 20.0 || cands[0].Source == "" {
		t.Fatalf("candidate timestamps/source must be verbatim from the cue; got %+v", cands[0])
	}
	if len(cands[0].Terms) != 2 {
		t.Fatalf("top candidate should record 2 matched terms, got %v", cands[0].Terms)
	}

	// Case-insensitive and OR semantics.
	if got := GrepTranscripts(idx, []string{"DOG"}); len(got) != 1 || got[0].Name != "doorbell.mov" {
		t.Fatalf("grep DOG = %+v, want 1 hit in doorbell.mov", got)
	}

	// Blank-only term list → no candidates (not the whole transcript).
	if got := GrepTranscripts(idx, []string{"   ", ""}); len(got) != 0 {
		t.Fatalf("blank terms should yield 0 candidates, got %d", len(got))
	}
	// A miss yields an empty, non-nil slice.
	if got := GrepTranscripts(idx, []string{"zzzz"}); got == nil || len(got) != 0 {
		t.Fatalf("a miss should be empty non-nil, got %v", got)
	}
}

func TestMetaRoundTrip(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "clip.mp4")
	writeFile(t, video, "video bytes that must NOT be touched")
	before, _ := os.ReadFile(video)

	// Missing sidecar → zero Meta, nil error.
	m, err := LoadMeta(video)
	if err != nil {
		t.Fatalf("LoadMeta(missing) error = %v, want nil", err)
	}
	if (m != Meta{}) {
		t.Fatalf("LoadMeta(missing) = %+v, want zero Meta", m)
	}

	// Save then load round-trips exactly, and writes only the sidecar.
	want := Meta{Date: "2026-06-14", Link: "https://example/evidence", Person: "defendant", Location: "porch", SourceFPS: 29.97}
	if err := SaveMeta(video, want); err != nil {
		t.Fatalf("SaveMeta error = %v", err)
	}
	if _, err := os.Stat(MetaPath(video)); err != nil {
		t.Fatalf("sidecar %s should exist after SaveMeta: %v", MetaPath(video), err)
	}
	got, err := LoadMeta(video)
	if err != nil {
		t.Fatalf("LoadMeta after save error = %v", err)
	}
	if got != want {
		t.Fatalf("round-trip Meta = %+v, want %+v", got, want)
	}

	// The video bytes must be byte-identical (originals never modified).
	after, _ := os.ReadFile(video)
	if string(before) != string(after) {
		t.Fatal("SaveMeta modified the video file — originals must never be touched")
	}
}

func TestLoadMetaCorruptSidecar(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "clip.mp4")
	writeFile(t, video, "x")
	writeFile(t, MetaPath(video), "{ this is not valid json")

	// LoadMeta surfaces the parse error...
	if _, err := LoadMeta(video); err == nil {
		t.Fatal("LoadMeta(corrupt) should return an error")
	}
	// ...but indexing degrades (a corrupt sidecar must not abort the walk).
	idx, err := Index(dir)
	if err != nil {
		t.Fatalf("Index() with a corrupt sidecar should not fail: %v", err)
	}
	if len(idx.Videos) != 1 || (idx.Videos[0].Meta != Meta{}) {
		t.Fatalf("corrupt sidecar should index the video with zero Meta; got %+v", idx.Videos)
	}
}
