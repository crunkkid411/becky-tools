package main

import (
	"context"
	"math"
	"testing"
)

func TestParseAudioLevels(t *testing.T) {
	// auto-editor's real shape: a leading blank line, an "@start" stream marker,
	// then one float per frame. A second "@..." marker (multi-stream) is ignored.
	raw := []byte("\n@start\n0.0\n0.5\n0.019999\n@end\n0.9\n")
	got := parseAudioLevels(raw)
	want := []float64{0.0, 0.5, 0.019999, 0.9}
	if len(got) != len(want) {
		t.Fatalf("got %d levels, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 1e-9 {
			t.Errorf("level[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestAudioLevels_ThreadsFpsAndParses(t *testing.T) {
	app, _ := openFixture(t) // ring.mp4 + kitchen.mov fixtures (see app_test.go helper)
	// Fake the auto-editor exec so no binary is needed; assert the fps was forced
	// onto it and the envelope round-trips through the verb.
	orig := runAudioLevels
	defer func() { runAudioLevels = orig }()
	var gotFps float64
	runAudioLevels = func(_ context.Context, _, _ string, fps float64) ([]byte, error) {
		gotFps = fps
		return []byte("@start\n0.01\n0.2\n0.9\n"), nil
	}
	res := app.AudioLevels("ring.mp4")
	if len(res.Levels) != 3 {
		t.Fatalf("levels = %v (note=%q)", res.Levels, res.Note)
	}
	if res.Fps <= 0 {
		t.Fatalf("fps = %v, want > 0", res.Fps)
	}
	if gotFps != res.Fps {
		t.Errorf("forced tb fps %v != returned fps %v", gotFps, res.Fps)
	}
}
