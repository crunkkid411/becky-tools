package pyhelpers

import (
	"os"
	"strings"
	"testing"
)

// The embed must carry the real script, not an empty placeholder: a zero-byte
// embed still compiles and only fails at runtime, in production.
func TestAudioSignalsEmbedded(t *testing.T) {
	if len(AudioSignals) < 5000 {
		t.Fatalf("audio_signals.py embed is %d bytes, expected the real script", len(AudioSignals))
	}
	for _, want := range []string{"def yin(", "--selftest", `"ok": False`} {
		if !strings.Contains(string(AudioSignals), want) {
			t.Errorf("embedded script is missing %q", want)
		}
	}
	p, err := Materialize("audio_signals.py", AudioSignals)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(b) != len(AudioSignals) {
		t.Fatalf("materialized %d bytes, embed has %d", len(b), len(AudioSignals))
	}
}
