package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestWriteSidecarTranscript proves a subtitle sidecar is parsed into the
// becky-transcribe transcript shape with transcript_source="youtube-srt".
func TestWriteSidecarTranscript(t *testing.T) {
	dir := t.TempDir()
	srt := filepath.Join(dir, "clip.en.srt")
	mustWriteFile(t, srt, "1\n00:00:00,000 --> 00:00:02,000\nhello world\n\n2\n00:00:02,000 --> 00:00:04,000\nsecond line\n")
	video := filepath.Join(dir, "clip.mp4")
	out := filepath.Join(dir, "transcript.json")

	sub, err := writeSidecarTranscript(srt, video, out, "en", "")
	if err != nil {
		t.Fatalf("writeSidecarTranscript: %v", err)
	}
	if len(sub.Segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(sub.Segments))
	}
	var doc transcriptDoc
	mustReadJSON(t, out, &doc)
	if doc.TranscriptSource != "youtube-srt" {
		t.Errorf("transcript_source = %q, want youtube-srt", doc.TranscriptSource)
	}
	if doc.File != video {
		t.Errorf("file = %q, want %q", doc.File, video)
	}
	if len(doc.Segments) != 2 || doc.Segments[0].Text != "hello world" {
		t.Errorf("segments = %+v", doc.Segments)
	}
	// duration falls back to the last segment end when ffprobe is unavailable.
	if doc.Duration != 4 {
		t.Errorf("duration = %v, want 4 (last segment end)", doc.Duration)
	}
}

// TestStampTranscriptSource proves a becky-transcribe transcript gets stamped
// transcript_source="parakeet" without disturbing its other fields.
func TestStampTranscriptSource(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "transcript.json")
	mustWriteFile(t, p, `{"file":"x.mp4","model":"parakeet","segments":[{"start":0,"end":1,"text":"hi"}]}`)

	stampTranscriptSource(p, "parakeet")

	var m map[string]any
	mustReadJSON(t, p, &m)
	if m["transcript_source"] != "parakeet" {
		t.Errorf("transcript_source = %v, want parakeet", m["transcript_source"])
	}
	if m["model"] != "parakeet" {
		t.Errorf("model field clobbered: %v", m["model"])
	}
}

// TestIngestMetadata proves info.json + live_chat.json are parsed and written to
// metadata.json, and that a no-sidecar video is a graceful no-op.
func TestIngestMetadata(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "vid.mp4")
	mustWriteFile(t, video, "x")
	mustWriteFile(t, filepath.Join(dir, "vid.info.json"),
		`{"id":"vid1","title":"T","uploader":"U","upload_date":"20260101","timestamp":1767225600,"webpage_url":"http://x"}`)
	mustWriteFile(t, filepath.Join(dir, "vid.live_chat.json"),
		`{"replayChatItemAction":{"actions":[{"addChatItemAction":{"item":{"liveChatTextMessageRenderer":{"message":{"runs":[{"text":"hi"}]},"authorName":{"simpleText":"@a"}}}}}],"videoOffsetTimeMsec":"5000"}}`+"\n")

	out := filepath.Join(dir, "metadata.json")
	doc, found, err := ingestMetadata(video, out)
	if err != nil {
		t.Fatalf("ingestMetadata: %v", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}
	if doc.Info == nil || doc.Info.VideoID != "vid1" {
		t.Errorf("info = %+v", doc.Info)
	}
	if doc.Info.UploadISO == "" {
		t.Error("upload_iso should be set")
	}
	if doc.ChatCount != 1 || doc.Chat[0].Author != "@a" {
		t.Errorf("chat = %+v", doc.Chat)
	}
	if _, statErr := os.Stat(out); statErr != nil {
		t.Errorf("metadata.json not written: %v", statErr)
	}
}

func TestIngestMetadataNoSidecar(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "plain.mp4")
	mustWriteFile(t, video, "x")
	out := filepath.Join(dir, "metadata.json")

	_, found, err := ingestMetadata(video, out)
	if err != nil {
		t.Fatalf("ingestMetadata: %v", err)
	}
	if found {
		t.Error("expected found=false for a video with no metadata sidecars")
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Error("metadata.json should NOT be written when nothing was found")
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustReadJSON(t *testing.T, path string, dest any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, dest); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
}
