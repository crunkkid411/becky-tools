package sampler

import (
	"encoding/json"
	"testing"
)

// TestVelGain_LouderWhenHarder is the regression test for the P0 bug the original
// 31 tests missed: velocity must affect loudness. With tracking on, a hard hit is
// louder than a ghost note; with tracking off, they are equal.
func TestVelGain_LouderWhenHarder(t *testing.T) {
	s := NewDrumSound("kick") // AmpVelTrack defaults to 1
	soft := s.VelGain(1)
	hard := s.VelGain(127)
	if !(hard > soft) {
		t.Fatalf("velocity must scale loudness: VelGain(1)=%v should be < VelGain(127)=%v", soft, hard)
	}
	if hard <= 0.99 {
		t.Errorf("full velocity should be ~unity, got %v", hard)
	}
	// Monotonic non-decreasing across the range.
	prev := -1.0
	for v := 1; v <= 127; v++ {
		g := s.VelGain(v)
		if g < prev {
			t.Fatalf("VelGain not monotonic at vel %d: %v < %v", v, g, prev)
		}
		prev = g
	}
	// Tracking off => velocity-independent.
	flat := Sound{AmpVelTrack: 0}
	if flat.VelGain(1) != flat.VelGain(127) {
		t.Errorf("AmpVelTrack=0 must be velocity-independent")
	}
}

// TestSelectVariantRandom_Honest verifies that random round-robin actually maps a
// random value onto a variant (even split when no bands), and honors explicit
// lorand/hirand bands when set. SelectVariant (sequential) is unchanged.
func TestSelectVariantRandom_Honest(t *testing.T) {
	layer := Layer{RoundRobin: []Variant{
		{SamplePath: "a.wav"}, {SamplePath: "b.wav"}, {SamplePath: "c.wav"}, {SamplePath: "d.wav"},
	}}
	// Even split: r in each quarter selects the matching variant.
	cases := map[float64]string{0.0: "a.wav", 0.3: "b.wav", 0.6: "c.wav", 0.95: "d.wav"}
	for r, want := range cases {
		if got := SelectVariantRandom(layer, r).SamplePath; got != want {
			t.Errorf("SelectVariantRandom(r=%v) = %q, want %q", r, got, want)
		}
	}
	// Explicit bands win.
	banded := Layer{RoundRobin: []Variant{
		{SamplePath: "lo.wav", RandLo: 0, RandHi: 0.2},
		{SamplePath: "hi.wav", RandLo: 0.2, RandHi: 1},
	}}
	if got := SelectVariantRandom(banded, 0.1).SamplePath; got != "lo.wav" {
		t.Errorf("banded r=0.1 = %q, want lo.wav", got)
	}
	if got := SelectVariantRandom(banded, 0.5).SamplePath; got != "hi.wav" {
		t.Errorf("banded r=0.5 = %q, want hi.wav", got)
	}
	// Empty layer degrades, never panics.
	if SelectVariantRandom(Layer{}, 0.5).SamplePath != "" {
		t.Errorf("empty layer should yield zero Variant")
	}
}

// TestAmpEnv_NormalizeAndJSON checks the envelope clamps and round-trips with a
// readable type token.
func TestAmpEnv_NormalizeAndJSON(t *testing.T) {
	e := AmpEnv{Type: EnvADSR, A: -1, H: -2, D: 0.1, S: 5, R: -3}.Normalize()
	if e.A != 0 || e.H != 0 || e.R != 0 {
		t.Errorf("negative env times must clamp to 0: %+v", e)
	}
	if e.S != 1 {
		t.Errorf("sustain must clamp to 0..1: got %v", e.S)
	}
	b, err := json.Marshal(AmpEnv{Type: EnvAHD, D: 0.2})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) == "" || !containsToken(string(b), `"type":"ahd"`) {
		t.Errorf("AmpEnv JSON should carry readable type token, got %s", b)
	}
}

// TestSound_NewDrumDefaultsResponsive ensures the constructor yields a Sound that
// actually responds to velocity and declicks — the opposite of the zero-value trap.
func TestSound_NewDrumDefaultsResponsive(t *testing.T) {
	s := NewDrumSound("snare").Normalize()
	if s.AmpVelTrack <= 0 {
		t.Errorf("NewDrumSound must default to responsive velocity, got track=%v", s.AmpVelTrack)
	}
	if s.DeclickMs <= 0 {
		t.Errorf("NewDrumSound must set a declick floor, got %v", s.DeclickMs)
	}
	if s.AmpEnv.Type != EnvOneshot {
		t.Errorf("drum default env should be one-shot")
	}
}

// TestVariant_ReverseAndBandsRoundTrip ensures the new fields persist.
func TestVariant_ReverseAndBandsRoundTrip(t *testing.T) {
	v := Variant{SamplePath: "x.wav", Reverse: true, RandLo: 0.25, RandHi: 0.75}
	b, _ := json.Marshal(v)
	var got Variant
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Reverse || got.RandLo != 0.25 || got.RandHi != 0.75 {
		t.Errorf("reverse/rand bands did not round-trip: %+v", got)
	}
	n := Variant{RandLo: -1, RandHi: 9}.Normalize()
	if n.RandLo != 0 || n.RandHi != 1 {
		t.Errorf("rand bands must clamp to 0..1: %+v", n)
	}
}

func containsToken(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
