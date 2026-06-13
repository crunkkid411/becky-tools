// output_test.go — proves the deterministic "output next to the input" rule and the
// multi-op parsing, headless (no terminal, no real tool run). This is the verifiable
// core of "video.mp4 -> video.srt, same folder, never touch the source."
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOutputPathFor_SidecarNextToInput(t *testing.T) {
	in := filepath.Join("C:", "cases", "boxing.mp4")
	cases := []struct {
		op       actionID
		wantExt  string
		wantSave bool
	}{
		{actTranscribe, ".srt", true},
		{actDiarize, ".diarize.json", true},
		{actIdentify, ".identify.json", true},
		{actDescribe, ".describe.json", true},
		{actCut, "", false}, // cut writes its own media; no sidecar here
	}
	for _, c := range cases {
		got, save := outputPathFor(in, c.op)
		if save != c.wantSave {
			t.Errorf("%s: save=%v want %v", c.op, save, c.wantSave)
			continue
		}
		if !c.wantSave {
			continue
		}
		want := filepath.Join("C:", "cases", "boxing"+c.wantExt)
		if got != want {
			t.Errorf("%s: path=%q want %q", c.op, got, want)
		}
	}
}

func TestExecArgs_TranscribeGetsSrtFormat(t *testing.T) {
	got := execArgs([]string{"becky-transcribe", "clip.mp4"})
	want := []string{"becky-transcribe", "clip.mp4", "--format", "srt"}
	if !equalArgv(got, want) {
		t.Errorf("execArgs transcribe = %v, want %v", got, want)
	}
	// A non-transcribe command is unchanged.
	id := []string{"becky-identify", "clip.mp4", "--kb", "kb-final"}
	if !equalArgv(execArgs(id), id) {
		t.Errorf("execArgs should not alter identify: %v", execArgs(id))
	}
	// Already has --format -> not doubled.
	pre := []string{"becky-transcribe", "clip.mp4", "--format", "txt"}
	if !equalArgv(execArgs(pre), pre) {
		t.Errorf("execArgs should not re-add --format: %v", execArgs(pre))
	}
}

func TestSaveOutput_RefusesMediaExtension(t *testing.T) {
	dir := t.TempDir()
	// A media extension must NEVER be written by the sidecar path (protects sources).
	if _, err := saveOutput(filepath.Join(dir, "clip.mp4"), "data"); err == nil {
		t.Fatalf("saveOutput must refuse a media extension")
	}
	if _, err := os.Stat(filepath.Join(dir, "clip.mp4")); err == nil {
		t.Fatalf("saveOutput must not create a media file")
	}
	// A real sidecar is written, and the written path is returned.
	p := filepath.Join(dir, "clip.srt")
	got, err := saveOutput(p, "1\n00:00 hi\n")
	if err != nil || got != p {
		t.Fatalf("saveOutput sidecar failed: got=%q err=%v", got, err)
	}
	// A SECOND write must NOT clobber the (possibly verified) existing file — it
	// lands as clip.becky.srt instead.
	got2, err := saveOutput(p, "different content")
	if err != nil {
		t.Fatalf("second saveOutput failed: %v", err)
	}
	if got2 == p {
		t.Fatalf("saveOutput must not overwrite an existing sidecar; got %q", got2)
	}
	if filepath.Base(got2) != "clip.becky.srt" {
		t.Errorf("expected clip.becky.srt, got %q", filepath.Base(got2))
	}
	// The original is intact.
	if b, _ := os.ReadFile(p); string(b) != "1\n00:00 hi\n" {
		t.Errorf("original sidecar was modified: %q", string(b))
	}
}

func TestParseRunSelection_NumbersAndWords(t *testing.T) {
	acts := quickActionsFor(fileTarget(t, ".mp4")) // transcribe, diarize, identify, describe, cut
	if len(acts) < 2 {
		t.Fatalf("expected several video actions, got %d", len(acts))
	}
	// "1,2" -> the first two actions (Transcribe, Diarize).
	got := parseRunSelection("1,2", acts)
	if len(got) != 2 || got[0] != acts[0].ID || got[1] != acts[1].ID {
		t.Errorf("'1,2' = %v, want first two action ids", got)
	}
	// Two op words in plain language.
	got = parseRunSelection("transcribe and diarize", acts)
	if len(got) != 2 || got[0] != actTranscribe || got[1] != actDiarize {
		t.Errorf("'transcribe and diarize' = %v, want [transcribe diarize]", got)
	}
	// A SINGLE bare op word must NOT be hijacked — the normal router handles it.
	if got := parseRunSelection("transcribe this", acts); got != nil {
		t.Errorf("single op should fall through to router, got %v", got)
	}
	// A plain question is never a run-list.
	if got := parseRunSelection("what can you do?", acts); got != nil {
		t.Errorf("a question should not parse as ops, got %v", got)
	}
}
