package main

import (
	"os"
	"testing"

	"becky-go/internal/config"
)

// testAsset is the real assault clip. The integration test is skipped if it (or
// ffmpeg) is absent, so the unit suite still runs anywhere.
const testAsset = `E:\TakingBack2007\test\test.mp4`

// TestMotionSignalRealClip exercises the full ffmpeg decode + dense frame-difference
// path on real footage and asserts the localizer finds the ~0:13 quick movement that
// 1-fps sampling misses, plus contact-window bursts — and that it does not flood.
func TestMotionSignalRealClip(t *testing.T) {
	if _, err := os.Stat(testAsset); err != nil {
		t.Skipf("real test asset not present: %v", err)
	}
	cfg := config.Load()
	if _, err := os.Stat(cfg.FFmpeg); err != nil {
		t.Skipf("ffmpeg not present at %s: %v", cfg.FFmpeg, err)
	}

	sig, err := motionSignal(cfg.FFmpeg, testAsset, 0, 0, 30, false, false)
	if err != nil {
		t.Fatalf("motionSignal: %v", err)
	}
	if len(sig.Norm) < 1000 {
		t.Fatalf("expected ~1035 per-frame values at 30fps, got %d", len(sig.Norm))
	}
	if sig.RawPeak < absMotionFloor {
		t.Fatalf("assault clip raw peak %.3f should exceed floor %.1f", sig.RawPeak, absMotionFloor)
	}

	p := defaultBurstParams()
	raw, _ := detectBursts(sig.Norm, p)
	bursts := buildBursts(raw, sig.Norm, p, 30, 30, 0)
	if len(bursts) == 0 {
		t.Fatal("expected motion bursts on the assault clip, got none")
	}
	if len(bursts) > 30 {
		t.Errorf("over-firing: %d bursts on a 34s clip is too many", len(bursts))
	}

	// The forensic property: a burst overlapping the ~0:13 quick movement (12.5-14s).
	found13 := false
	for _, b := range bursts {
		if b.WindowStart <= 14 && b.WindowEnd >= 12.5 {
			found13 = true
		}
	}
	if !found13 {
		t.Error("FAILED to localize the ~0:13 quick movement that 1-fps sampling misses")
	}
}

// TestCalmClipNoOverFire confirms the near-static / panning calm clip does not flood
// with bursts under default settings.
func TestCalmClipNoOverFire(t *testing.T) {
	const calmAsset = `E:\TakingBack2007\July 2025\20250704_181431.mp4`
	if _, err := os.Stat(calmAsset); err != nil {
		t.Skipf("calm test asset not present: %v", err)
	}
	cfg := config.Load()
	if _, err := os.Stat(cfg.FFmpeg); err != nil {
		t.Skipf("ffmpeg not present: %v", err)
	}

	sig, err := motionSignal(cfg.FFmpeg, calmAsset, 0, 0, 30, false, false)
	if err != nil {
		t.Fatalf("motionSignal: %v", err)
	}
	p := defaultBurstParams()
	raw, _ := detectBursts(sig.Norm, p)
	bursts := buildBursts(raw, sig.Norm, p, 30, 30, 0)
	// An 8s uniformly-busy clip should yield at most a couple genuine peaks, not a flood.
	if len(bursts) > 3 {
		t.Errorf("calm clip over-fired: %d bursts (want <= 3)", len(bursts))
	}
}
