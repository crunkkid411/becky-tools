package footage

import (
	"path/filepath"
	"testing"
)

// TestIndex_BeckyJSONTranscript locks the fix for "becky-transcribe wrote a .json
// but the panel still says not transcribed": a <stem>.json (or <stem>.transcript.json)
// beside a video flips HasTranscript, while a video whose only .json is one of
// becky's own data sidecars (.beckymeta.json/.reel.json/.questions.json) stays NOT
// transcribed — a data sidecar must never masquerade as a transcript.
func TestIndex_BeckyJSONTranscript(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "clip.mp4"), "video-bytes")
	writeFile(t, filepath.Join(root, "clip.json"), `{"segments":[{"start":0,"end":2,"text":"hello world"}]}`)

	writeFile(t, filepath.Join(root, "story.mp4"), "video-bytes")
	writeFile(t, filepath.Join(root, "story.transcript.json"), `{"segments":[{"start":0,"end":1,"text":"once upon a time"}]}`)

	writeFile(t, filepath.Join(root, "other.mp4"), "video-bytes")
	writeFile(t, filepath.Join(root, "other.mp4.beckymeta.json"), `{"date":"2026-01-01"}`)

	idx, err := Index(root)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	by := map[string]Video{}
	for _, v := range idx.Videos {
		by[v.Name] = v
	}

	clip, ok := by["clip.mp4"]
	if !ok || !clip.HasTranscript || filepath.Base(clip.TranscriptPath) != "clip.json" {
		t.Fatalf("clip.mp4 should pair clip.json; got has=%v path=%q", clip.HasTranscript, clip.TranscriptPath)
	}
	story, ok := by["story.mp4"]
	if !ok || !story.HasTranscript || filepath.Base(story.TranscriptPath) != "story.transcript.json" {
		t.Fatalf("story.mp4 should pair story.transcript.json; got has=%v path=%q", story.HasTranscript, story.TranscriptPath)
	}
	other, ok := by["other.mp4"]
	if !ok {
		t.Fatal("other.mp4 missing from index")
	}
	if other.HasTranscript {
		t.Fatalf("other.mp4 must NOT be transcribed by a .beckymeta.json; got path=%q", other.TranscriptPath)
	}
}
