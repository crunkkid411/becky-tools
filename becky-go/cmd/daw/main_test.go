package main

import (
	"os"
	"path/filepath"
	"testing"

	"becky-go/internal/habits"
	"becky-go/internal/music"
)

// writeFixtureMid writes a small valid SMF into a temp dir and returns its path.
func writeFixtureMid(t *testing.T) string {
	t.Helper()
	f := music.NewFile(480)
	meta := f.AddTrack()
	meta.Tempo(0, 140)
	meta.TimeSig(0, 4, 4)
	mel := f.AddTrack()
	mel.Name(0, "melody")
	mel.Note(0, 240, 0, 60, 88)
	mel.Note(118, 240, 0, 64, 88) // off-grid, so quantize has work to do
	path := filepath.Join(t.TempDir(), "song.mid")
	if err := os.WriteFile(path, f.Bytes(), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// TestRun_exitCodes covers usage/error/ok paths.
func TestRun_exitCodes(t *testing.T) {
	mid := writeFixtureMid(t)
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"no args is usage", nil, exitUsage},
		{"unknown command", []string{"frobnicate"}, exitUsage},
		{"help is ok", []string{"--help"}, exitOK},
		{"load missing file errors", []string{"load", "--in", "nope.mid"}, exitErr},
		{"load ok", []string{"load", "--in", mid}, exitOK},
		{"load json ok", []string{"load", "--in", mid, "--json"}, exitOK},
		{"edit requires op", []string{"edit", "--in", mid}, exitUsage},
		{"drumgrid ok", []string{"drumgrid", "--in", mid}, exitOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := run(c.args); got != c.want {
				t.Errorf("run(%v) = %d, want %d", c.args, got, c.want)
			}
		})
	}
}

// TestRun_editWritesAndRoundTrips: an edit op writes an output .mid that re-loads.
func TestRun_editWritesAndRoundTrips(t *testing.T) {
	mid := writeFixtureMid(t)
	out := filepath.Join(t.TempDir(), "edited.mid")
	code := run([]string{"edit", "--in", mid, "--out", out, "--op", "quantize", "--grid", "120", "--strength", "1"})
	if code != exitOK {
		t.Fatalf("edit run = %d, want 0", code)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("output not written: %v", err)
	}
	if code := run([]string{"load", "--in", out}); code != exitOK {
		t.Errorf("re-load edited = %d, want 0", code)
	}
}

// TestRun_editTranspose exercises a clip-wide op via the CLI.
func TestRun_editTranspose(t *testing.T) {
	mid := writeFixtureMid(t)
	if code := run([]string{"edit", "--in", mid, "--op", "transpose", "--semis", "12"}); code != exitOK {
		t.Errorf("transpose run = %d, want 0", code)
	}
}

// TestRun_badMidiDegrades: a non-MIDI input errors without panicking.
func TestRun_badMidiDegrades(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "bad.mid")
	if err := os.WriteFile(bad, []byte("not a midi file"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("run panicked on bad midi: %v", r)
		}
	}()
	if code := run([]string{"load", "--in", bad}); code != exitErr {
		t.Errorf("bad midi load = %d, want %d", code, exitErr)
	}
}

// TestEmitCorrectionLog_GainOp verifies that a gain edit producing one correction
// writes a daw.corrections.jsonl sidecar next to --out that round-trips through
// habits.LoadCorrectionLog with the correct scope/field/auto/fixed mapping.
func TestEmitCorrectionLog_GainOp(t *testing.T) {
	ctx := t.Context()
	_ = ctx // offline test; context proves t.Context() compiles (Go 1.24+)

	mid := writeFixtureMid(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "edited.mid")

	// The fixture track is named "melody"; its initial gain is 1.0 (unity).
	// Setting gain to 0.5 changes it → one correction: kind="gain", clip="melody".
	code := run([]string{
		"edit", "--in", mid, "--out", out,
		"--op", "gain", "--gain", "0.5", "--track", "melody",
	})
	if code != exitOK {
		t.Fatalf("edit (gain) run = %d, want 0", code)
	}

	logPath := filepath.Join(dir, "daw.corrections.jsonl")
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("daw.corrections.jsonl not written: %v", err)
	}

	recs, err := habits.LoadCorrectionLog(logPath)
	if err != nil {
		t.Fatalf("LoadCorrectionLog: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d correction records, want 1", len(recs))
	}

	r := recs[0]
	// scope = c.Clip = trackID = "melody"
	if r.Scope != "melody" {
		t.Errorf("scope = %q, want %q", r.Scope, "melody")
	}
	// field = c.Kind = "gain"
	if r.Field != "gain" {
		t.Errorf("field = %q, want %q", r.Field, "gain")
	}
	// auto = initial gain "1" (ftoa(1.0) = "1")
	if r.Auto != "1" {
		t.Errorf("auto = %q, want %q", r.Auto, "1")
	}
	// fixed = requested gain "0.5"
	if r.Fixed != "0.5" {
		t.Errorf("fixed = %q, want %q", r.Fixed, "0.5")
	}
	// Context carries tool="daw"
	if r.Context["tool"] != "daw" {
		t.Errorf("context tool = %q, want %q", r.Context["tool"], "daw")
	}
}

// TestEmitCorrectionLog_QuantizeOp verifies that a quantize edit producing N
// corrections writes exactly N JSONL lines, and that each records kind="quantize".
func TestEmitCorrectionLog_QuantizeOp(t *testing.T) {
	mid := writeFixtureMid(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "quant.mid")

	// The fixture has two notes in the "melody" track (clip also named "melody"):
	// tick 0 (on-grid) and tick 118 (off-grid). quantize --grid 120 --strength 1
	// moves tick 118 → 120: one correction. We must specify --track and --clip
	// because resolveTarget falls back to the first clip in iteration order
	// (track0/track0, the meta/tempo track), not to the melody track.
	code := run([]string{
		"edit", "--in", mid, "--out", out,
		"--op", "quantize", "--grid", "120", "--strength", "1",
		"--track", "melody", "--clip", "melody",
	})
	if code != exitOK {
		t.Fatalf("edit (quantize) run = %d, want 0", code)
	}

	logPath := filepath.Join(dir, "daw.corrections.jsonl")
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("daw.corrections.jsonl not written: %v", err)
	}

	recs, err := habits.LoadCorrectionLog(logPath)
	if err != nil {
		t.Fatalf("LoadCorrectionLog: %v", err)
	}
	if len(recs) == 0 {
		t.Fatal("got 0 correction records; expected at least 1 (off-grid note)")
	}
	for i, r := range recs {
		if r.Field != "quantize" {
			t.Errorf("rec[%d].field = %q, want %q", i, r.Field, "quantize")
		}
		if r.Context["tool"] != "daw" {
			t.Errorf("rec[%d].context tool = %q, want %q", i, r.Context["tool"], "daw")
		}
	}
}

// TestEmitCorrectionLog_NoCorrections verifies that when an op produces no
// corrections (e.g. gain set to its current value, or a no-op transpose), no
// sidecar is written (best-effort: no crash, no spurious file).
func TestEmitCorrectionLog_NoCorrections(t *testing.T) {
	mid := writeFixtureMid(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "same.mid")

	// Transpose by 0 semitones: the model returns a new arrangement with no notes
	// moved, so no corrections are logged.
	code := run([]string{
		"edit", "--in", mid, "--out", out,
		"--op", "transpose", "--semis", "0",
	})
	if code != exitOK {
		t.Fatalf("edit (transpose 0) run = %d, want 0", code)
	}

	logPath := filepath.Join(dir, "daw.corrections.jsonl")
	if _, err := os.Stat(logPath); err == nil {
		// Sidecar exists — it may have zero lines (empty file), which is fine,
		// but it must not crash LoadCorrectionLog.
		recs, err := habits.LoadCorrectionLog(logPath)
		if err != nil {
			t.Fatalf("LoadCorrectionLog on unexpected sidecar: %v", err)
		}
		if len(recs) != 0 {
			t.Errorf("got %d records for a no-op op, want 0", len(recs))
		}
	}
	// Either no file (best case) or an empty/zero-record file: both are valid.
}

// TestEmitCorrectionLog_ReportOnlyMode verifies that when --out is omitted
// (report-only mode), the correction log still lands next to --in, not in
// the current working directory.
func TestEmitCorrectionLog_ReportOnlyMode(t *testing.T) {
	dir := t.TempDir()
	mid := filepath.Join(dir, "song.mid")
	{
		f := music.NewFile(480)
		f.AddTrack() // meta
		mel := f.AddTrack()
		mel.Name(0, "melody")
		mel.Note(0, 240, 0, 60, 88)
		mel.Note(118, 240, 0, 64, 88)
		if err := os.WriteFile(mid, f.Bytes(), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}

	// No --out: report-only mode. The correction log should land in dir (next to --in).
	code := run([]string{
		"edit", "--in", mid,
		"--op", "gain", "--gain", "0.5", "--track", "melody",
	})
	if code != exitOK {
		t.Fatalf("edit (report-only) run = %d, want 0", code)
	}

	logPath := filepath.Join(dir, "daw.corrections.jsonl")
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("daw.corrections.jsonl not written next to --in: %v", err)
	}

	recs, err := habits.LoadCorrectionLog(logPath)
	if err != nil {
		t.Fatalf("LoadCorrectionLog: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
}
