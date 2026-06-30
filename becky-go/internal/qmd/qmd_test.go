package qmd

import (
	"path/filepath"
	"testing"
)

func hasKV(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}

// TestParseJSON: tolerate leading progress text + trailing bytes around the array.
func TestParseJSON(t *testing.T) {
	raw := "Expanding query... (5s)\n" +
		`[{"docid":"#a","score":0.7,"file":"qmd://transcripts/x.md","title":"ring","snippet":"hello"}]` +
		"\ndone\n"
	hits, err := ParseJSON([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(hits) != 1 || hits[0].Title != "ring" || hits[0].Score != 0.7 {
		t.Fatalf("bad parse: %+v", hits)
	}
	if _, err := ParseJSON([]byte("no json here")); err == nil {
		t.Error("non-JSON output should error so the caller can fall back")
	}
}

// TestSourceName: frontmatter source wins; else title + ".srt".
func TestSourceName(t *testing.T) {
	fm := Hit{Title: "ring", Snippet: "@@ -1,4 @@\nsource: \"real_name.srt\"\nvideo_id: \"\""}
	if got := SourceName(fm); got != "real_name.srt" {
		t.Fatalf("frontmatter should win, got %q", got)
	}
	noFm := Hit{Title: "18_2026-05-19-penguin_parakeet_transcription", Snippet: "**[00:01:02]** words"}
	if got := SourceName(noFm); got != "18_2026-05-19-penguin_parakeet_transcription.srt" {
		t.Fatalf("title fallback wrong, got %q", got)
	}
	if got := SourceName(Hit{}); got != "" {
		t.Fatalf("empty hit should yield empty source, got %q", got)
	}
}

// TestFirstTimecode: parse **[H:MM:SS]** to seconds; -1 when absent.
func TestFirstTimecode(t *testing.T) {
	if got := FirstTimecode("**[01:02:03]** hi"); got != 3723 {
		t.Fatalf("01:02:03 -> %v want 3723", got)
	}
	if got := FirstTimecode("blah [0:00:07] blah"); got != 7 {
		t.Fatalf("0:00:07 -> %v want 7", got)
	}
	if got := FirstTimecode("no timecode"); got != -1 {
		t.Fatalf("absent -> %v want -1", got)
	}
}

// TestCleanSnippet: drop diff header + frontmatter + markers, keep readable text.
func TestCleanSnippet(t *testing.T) {
	s := "@@ -1,4 @@ (0 before, 17 after)\n---\nsource: \"x.srt\"\ndate: \"2026-05-19\"\n**[00:00:00]** do I run out of time. My name is Penguin."
	if got := CleanSnippet(s); got != "do I run out of time. My name is Penguin." {
		t.Fatalf("clean snippet = %q", got)
	}
}

// TestEnvForcesVulkanAndPins: missing qmd env vars are filled (Vulkan + index pins).
func TestEnvForcesVulkanAndPins(t *testing.T) {
	t.Setenv("USERPROFILE", `C:\Users\test`)
	for _, k := range []string{"QMD_LLAMA_GPU", "XDG_CACHE_HOME", "QMD_CONFIG_DIR", "HOME"} {
		t.Setenv(k, "")
	}
	env := Env()
	if !hasKV(env, "QMD_LLAMA_GPU=vulkan") {
		t.Error("Env must force QMD_LLAMA_GPU=vulkan when unset")
	}
	if !hasKV(env, "HOME=C:\\Users\\test") {
		t.Error("Env must set HOME from USERPROFILE")
	}
	if want := "XDG_CACHE_HOME=" + filepath.Join(`C:\Users\test`, ".cache"); !hasKV(env, want) {
		t.Errorf("Env must pin %q", want)
	}
	if want := "QMD_CONFIG_DIR=" + filepath.Join(`C:\Users\test`, ".config", "qmd"); !hasKV(env, want) {
		t.Errorf("Env must pin %q", want)
	}
}

// TestEnvRespectsExplicit: an explicit env value is kept, not overridden.
func TestEnvRespectsExplicit(t *testing.T) {
	t.Setenv("QMD_LLAMA_GPU", "cuda")
	env := Env()
	if hasKV(env, "QMD_LLAMA_GPU=vulkan") {
		t.Error("Env must respect an explicit QMD_LLAMA_GPU value")
	}
	if !hasKV(env, "QMD_LLAMA_GPU=cuda") {
		t.Error("explicit QMD_LLAMA_GPU=cuda should remain")
	}
}
