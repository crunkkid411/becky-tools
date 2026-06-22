package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"becky-go/internal/tts"
)

func TestResolveText(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "answer.txt")
	if err := os.WriteFile(file, []byte("  from a file  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty.txt")
	os.WriteFile(empty, []byte("   \n"), 0o644)

	cases := []struct {
		name    string
		args    []string
		in      string
		want    string
		wantErr bool
	}{
		{"inline", []string{"hello", "there"}, "", "hello there", false},
		{"from file", nil, file, "from a file", false},
		{"both set", []string{"hi"}, file, "", true},
		{"neither", nil, "", "", true},
		{"empty file", nil, empty, "", true},
		{"missing file", nil, filepath.Join(dir, "nope.txt"), "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveText(c.args, c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestCheckWAVExt(t *testing.T) {
	if err := checkWAVExt("speech.wav"); err != nil {
		t.Errorf(".wav rejected: %v", err)
	}
	if err := checkWAVExt("speech.WAV"); err != nil {
		t.Errorf(".WAV (uppercase) rejected: %v", err)
	}
	if err := checkWAVExt("speech.mp3"); err == nil {
		t.Error(".mp3 should be rejected")
	}
	if err := checkWAVExt("notes.txt"); err == nil {
		t.Error(".txt should be rejected")
	}
}

func TestRefuseNonWAVOverwrite(t *testing.T) {
	dir := t.TempDir()

	// A real document with a .wav-looking name must be protected.
	doc := filepath.Join(dir, "important.wav")
	if err := os.WriteFile(doc, []byte("THIS IS A REAL DOCUMENT, NOT AUDIO"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := refuseNonWAVOverwrite(doc); err == nil {
		t.Error("expected refusal to overwrite a non-WAV file")
	}

	// An existing valid WAV may be overwritten.
	wav := filepath.Join(dir, "old.wav")
	b, _ := tts.WriteWAVPCM16([]int16{1, 2, 3, 4}, 24000)
	os.WriteFile(wav, b, 0o644)
	if err := refuseNonWAVOverwrite(wav); err != nil {
		t.Errorf("overwriting an existing WAV should be allowed: %v", err)
	}

	// A non-existent path is fine.
	if err := refuseNonWAVOverwrite(filepath.Join(dir, "new.wav")); err != nil {
		t.Errorf("non-existent path should be fine: %v", err)
	}

	// An empty placeholder is fine.
	empty := filepath.Join(dir, "empty.wav")
	os.WriteFile(empty, nil, 0o644)
	if err := refuseNonWAVOverwrite(empty); err != nil {
		t.Errorf("empty file should be overwritable: %v", err)
	}
}

func TestRun_SelfTestWritesValidWAV(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "s.wav")
	code := run([]string{"--selftest", "--out", out}, os.Stdout, os.Stderr)
	if code != 0 {
		t.Fatalf("selftest exit code = %d, want 0", code)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("selftest wrote no file: %v", err)
	}
	info, verr := tts.ValidateWAV(b)
	if verr != nil {
		t.Fatalf("selftest WAV invalid: %v", verr)
	}
	if info.DataBytes <= 0 {
		t.Fatal("selftest WAV has empty data")
	}
}

func TestRun_SelfTestNeedsOut(t *testing.T) {
	code := run([]string{"--selftest"}, os.Stdout, os.Stderr)
	if code == 0 {
		t.Fatal("--selftest with no --out/--play should fail")
	}
}

func TestRun_SynthOutRequired(t *testing.T) {
	code := run([]string{"hello"}, os.Stdout, os.Stderr)
	// No --out, no --play => usage error (2).
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (missing --out)", code)
	}
}

func TestRun_RefusesNonWAVOut(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "speech.mp3")
	code := run([]string{"hello", "--out", out}, os.Stdout, os.Stderr)
	if code != 2 {
		t.Fatalf("non-wav --out exit = %d, want 2", code)
	}
}

func TestRun_DegradesWhenNoModel(t *testing.T) {
	// Ensure no env points at a real runtime/model so synth degrades.
	t.Setenv(tts.EnvBin, "")
	t.Setenv(tts.EnvModel, "")
	dir := t.TempDir()
	out := filepath.Join(dir, "speech.wav")

	r, w, _ := os.Pipe()
	code := run([]string{"becky here, the transcript is ready", "--out", out}, w, os.Stderr)
	w.Close()
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	stdout := string(buf[:n])

	if code == 0 {
		t.Fatal("expected non-zero exit when degrading (no model)")
	}
	// The text must be printed so the human still gets the content.
	if !strings.Contains(stdout, "transcript is ready") {
		t.Errorf("degrade did not print the text; stdout = %q", stdout)
	}
	// No WAV should have been written.
	if _, err := os.Stat(out); err == nil {
		t.Error("a WAV was written despite degrade")
	}
}
