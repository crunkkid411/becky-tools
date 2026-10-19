package pyhelpers

import (
	"strings"
	"testing"
)

// becky-livestream's picture check depends on these exact pieces: the mouth
// measure it was calibrated with, the pose model, insightface kept off stdout,
// and the one-JSON-line contract.
func TestPictureSignalsEmbedded(t *testing.T) {
	s := string(PictureSignals)
	if len(s) < 4000 {
		t.Fatalf("picture_signals.py embed is %d bytes, expected the real script", len(s))
	}
	for _, want := range []string{`"landmark_3d_68"`, "lm[62, :2] - lm[66, :2]", "lm[36, :2] - lm[45, :2]",
		"PoseLandmarker", `"image2pipe"`, "redirect_stdout(sys.stderr)", `{"ok": True, "seconds"`, `{"ok": False, "reason": reason}`} {
		if !strings.Contains(s, want) {
			t.Errorf("embedded script is missing %q", want)
		}
	}
}

// The graphics-card path (2026-10-19): insightface on CUDA, and a network the
// card cannot run whole goes to the processor - split, the pose detector gave
// wrong scores and no body was ever found.
func TestMPGPUEmbedded(t *testing.T) {
	s, p := string(MPGPU), string(PictureSignals)
	for _, want := range []string{"if gpu and not self.m.is_fully_accelerated():", "ssd_anchors(224, [8, 16, 32, 32, 32], 5)",
		"OneEuro(0.05, 80.0, 1.0)", "0.1 * body[:, 3] + 0.9 * self.vis", "def face_matrix("} {
		if !strings.Contains(s, want) {
			t.Errorf("mp_gpu.py is missing %q", want)
		}
	}
	for _, want := range []string{"import mp_gpu", `"CUDAExecutionProvider"`, "onnxruntime.preload_dlls()", `"--gpu"`} {
		if !strings.Contains(p, want) {
			t.Errorf("picture_signals.py is missing %q", want)
		}
	}
}
