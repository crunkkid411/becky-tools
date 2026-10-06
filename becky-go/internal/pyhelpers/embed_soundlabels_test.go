package pyhelpers

import (
	"strings"
	"testing"
)

// The breath check depends on these exact pieces: the class groups, the
// offline refusal and the one-JSON-line contract.
func TestSoundLabelsEmbedded(t *testing.T) {
	s := string(SoundLabels)
	if len(s) < 3000 {
		t.Fatalf("sound_labels.py embed is %d bytes, expected the real script", len(s))
	}
	for _, want := range []string{`"Breathing", "Pant"`, `"Male speech, man speaking"`, `"Background noise", "Mechanisms"`,
		"BEATs_strong_1.pt", "is missing", `"other_label"`, `"voice"`, "redirect_stdout"} {
		if !strings.Contains(s, want) {
			t.Errorf("embedded script is missing %q", want)
		}
	}
}
