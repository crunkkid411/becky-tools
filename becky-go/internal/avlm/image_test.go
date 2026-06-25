package avlm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAnalyzeImageDegradesWhenModelMissing: with nothing configured, AnalyzeImage
// must degrade (never panic) and name the missing model — and it must NOT touch
// the network (no server URL, no binary).
func TestAnalyzeImageDegradesWhenModelMissing(t *testing.T) {
	r := New("", "", "", "", "", "", nil)
	_, err := r.AnalyzeImage(context.Background(), "whatever.jpg", ImageOptions{})
	if err == nil || !IsDegrade(err) {
		t.Fatalf("expected a *DegradeError, got %v", err)
	}
	if !strings.Contains(err.Error(), "gemma model GGUF") {
		t.Errorf("expected model-missing reason, got %q", err.Error())
	}
}

// TestAnalyzeImageDegradesWhenImageMissing: with model/mmproj/ffmpeg present and a
// ServerURL set (so Ready passes without a binary), a missing image must degrade
// on "image not found" BEFORE any server contact.
func TestAnalyzeImageDegradesWhenImageMissing(t *testing.T) {
	dir := t.TempDir()
	model := filepath.Join(dir, "gemma.gguf")
	mmproj := filepath.Join(dir, "mmproj-BF16.gguf")
	ffmpeg := filepath.Join(dir, "ffmpeg.exe")
	for _, p := range []string{model, mmproj, ffmpeg} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	// ServerURL points nowhere real, but we never reach it: the image stat fails
	// first. A bogus URL proves AnalyzeImage short-circuits before any HTTP call.
	r := New(model, mmproj, "", "http://127.0.0.1:9", ffmpeg, "", nil)

	_, err := r.AnalyzeImage(context.Background(), filepath.Join(dir, "does-not-exist.jpg"), ImageOptions{})
	if err == nil || !IsDegrade(err) {
		t.Fatalf("expected a *DegradeError, got %v", err)
	}
	if !strings.Contains(err.Error(), "image not found") {
		t.Errorf("expected image-not-found reason, got %q", err.Error())
	}
}

// TestImageMIME maps extensions to data-URL MIME types, defaulting to JPEG.
func TestImageMIME(t *testing.T) {
	cases := map[string]string{
		"frame.jpg":  "image/jpeg",
		"frame.JPEG": "image/jpeg",
		"shot.png":   "image/png",
		"shot.PNG":   "image/png",
		"pic.webp":   "image/webp",
		"noext":      "image/jpeg",
		"weird.tiff": "image/jpeg",
		"a/b/c.Png":  "image/png",
	}
	for in, want := range cases {
		if got := imageMIME(in); got != want {
			t.Errorf("imageMIME(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestImageDefaults fills unset fields and supplies a neutral describe prompt,
// but never overrides a caller-provided prompt.
func TestImageDefaults(t *testing.T) {
	o := ImageOptions{}
	imageDefaults(&o)
	if o.MaxTokens != 512 || o.Temperature != 0.2 || o.Seed != 42 {
		t.Errorf("defaults not applied: %+v", o)
	}
	if !strings.Contains(o.Prompt, "Describe this image") {
		t.Errorf("expected a neutral default prompt, got %q", o.Prompt)
	}

	custom := ImageOptions{Prompt: "Describe this cat's mouth and teeth.", MaxTokens: 100}
	imageDefaults(&custom)
	if custom.Prompt != "Describe this cat's mouth and teeth." {
		t.Errorf("caller prompt must be preserved, got %q", custom.Prompt)
	}
	if custom.MaxTokens != 100 {
		t.Errorf("caller MaxTokens must be preserved, got %d", custom.MaxTokens)
	}
}
