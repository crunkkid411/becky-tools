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
