package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// writeMotionJSON writes a synthetic becky-motion JSON to a temp file and returns
// the path. Caller is responsible for cleanup.
func writeMotionJSON(t *testing.T, bursts []motionBurst) string {
	t.Helper()
	doc := motionDoc{MotionBursts: bursts}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal motion doc: %v", err)
	}
	f, err := os.CreateTemp(t.TempDir(), "motion-*.json")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	if _, err := f.Write(b); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	f.Close()
	return f.Name()
}

// TestMotionWindowEmpty returns zeros when no path is given.
func TestMotionWindowEmpty(t *testing.T) {
	start, dur, fps, note := motionWindow("")
	if start != 0 || dur != 0 || fps != 0 || note != "" {
		t.Errorf("empty path: got (%v,%v,%v,%q), want (0,0,0,\"\")", start, dur, fps, note)
	}
}

// TestMotionWindowNoBursts returns zeros + a diagnostic note when motion_bursts=[].
func TestMotionWindowNoBursts(t *testing.T) {
	path := writeMotionJSON(t, []motionBurst{})
	start, dur, fps, note := motionWindow(path)
	if start != 0 || dur != 0 || fps != 0 {
		t.Errorf("no bursts: expected zeros, got (%v,%v,%v)", start, dur, fps)
	}
	if note == "" {
		t.Error("no bursts: want a non-empty note explaining the fallback")
	}
}

// TestMotionWindowSingleBurst targets the correct window with padding.
func TestMotionWindowSingleBurst(t *testing.T) {
	burst := motionBurst{WindowStart: 5.0, WindowEnd: 5.7, MotionScore: 0.9}
	path := writeMotionJSON(t, []motionBurst{burst})
	start, dur, fps, note := motionWindow(path)

	wantStart := 5.0 - burstPad // 4.0 (pad applied; >= 0)
	wantEnd := 5.7 + burstPad   // 6.7
	wantDur := wantEnd - wantStart

	if math.Abs(start-wantStart) > 1e-6 {
		t.Errorf("start = %v, want %v", start, wantStart)
	}
	if math.Abs(dur-wantDur) > 1e-6 {
		t.Errorf("dur = %v, want %v", dur, wantDur)
	}
	if fps != burstFPS {
		t.Errorf("fps = %v, want %v", fps, burstFPS)
	}
	if note == "" {
		t.Error("expect a non-empty note describing the targeting")
	}
}

// TestMotionWindowPadClampAtZero ensures start is never negative when the burst
// is near the beginning of the clip.
func TestMotionWindowPadClampAtZero(t *testing.T) {
	burst := motionBurst{WindowStart: 0.3, WindowEnd: 0.8, MotionScore: 0.85}
	path := writeMotionJSON(t, []motionBurst{burst})
	start, dur, fps, note := motionWindow(path)

	if start < 0 {
		t.Errorf("start = %v; must be >= 0", start)
	}
	if start != 0 {
		t.Errorf("start = %v; expected 0 (clamped from %v)", start, 0.3-burstPad)
	}
	if dur <= 0 {
		t.Errorf("dur = %v; must be positive", dur)
	}
	if fps != burstFPS {
		t.Errorf("fps = %v, want %v", fps, burstFPS)
	}
	_ = note
}

// TestMotionWindowHighestScoreSelected picks the burst with the highest score,
// not the first burst.
func TestMotionWindowHighestScoreSelected(t *testing.T) {
	bursts := []motionBurst{
		{WindowStart: 2.0, WindowEnd: 2.5, MotionScore: 0.4},
		{WindowStart: 8.0, WindowEnd: 8.6, MotionScore: 0.92}, // highest score
		{WindowStart: 14.0, WindowEnd: 14.3, MotionScore: 0.3},
	}
	path := writeMotionJSON(t, bursts)
	start, _, _, _ := motionWindow(path)

	expectedStart := 8.0 - burstPad // 7.0
	if math.Abs(start-expectedStart) > 1e-6 {
		t.Errorf("start = %v, want %v (should target highest-score burst at 8.0s)", start, expectedStart)
	}
}

// TestMotionWindowMissingFile degrades gracefully with a note when the path is wrong.
func TestMotionWindowMissingFile(t *testing.T) {
	start, dur, fps, note := motionWindow(filepath.Join(t.TempDir(), "nonexistent.json"))
	if start != 0 || dur != 0 || fps != 0 {
		t.Errorf("missing file: expected zeros, got (%v,%v,%v)", start, dur, fps)
	}
	if note == "" {
		t.Error("missing file: want a non-empty error note")
	}
}

// TestMotionWindowBadJSON degrades gracefully when the JSON is malformed.
func TestMotionWindowBadJSON(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "bad-*.json")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("not valid json")
	f.Close()

	start, dur, fps, note := motionWindow(f.Name())
	if start != 0 || dur != 0 || fps != 0 {
		t.Errorf("bad JSON: expected zeros, got (%v,%v,%v)", start, dur, fps)
	}
	if note == "" {
		t.Error("bad JSON: want a non-empty error note")
	}
}

// TestMotionWindowConstants verifies the architecture constants are sane.
func TestMotionWindowConstants(t *testing.T) {
	if burstPad <= 0 {
		t.Errorf("burstPad = %v; must be positive", burstPad)
	}
	if burstFPS < 2.0 || burstFPS > 30.0 {
		t.Errorf("burstFPS = %v; expected in [2, 30]", burstFPS)
	}
}
