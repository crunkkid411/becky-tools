package dawmodel

import "testing"

// mixFixture builds a 2-track arrangement (a bass and a melody) with default strips.
func mixFixture() *Arrangement {
	a := New().AddTrack("bass", KindMIDI)
	a = a.AddTrack("melody", KindMIDI)
	return a
}

// TestDefaultStrip_routesByRole: bass -> bus.808, melody -> bus.music, at unity.
func TestDefaultStrip_routesByRole(t *testing.T) {
	a := mixFixture()
	bass, _ := a.TrackByID("bass")
	mel, _ := a.TrackByID("melody")
	if bass.Strip.Bus != "bus.808" {
		t.Errorf("bass bus = %q, want bus.808", bass.Strip.Bus)
	}
	if mel.Strip.Bus != "bus.music" {
		t.Errorf("melody bus = %q, want bus.music", mel.Strip.Bus)
	}
	if bass.Strip.Gain != 1 || bass.Strip.Pan != 0 {
		t.Errorf("default strip = gain %.2f pan %.2f, want 1/0", bass.Strip.Gain, bass.Strip.Pan)
	}
}

// TestMixerOps_setAndClamp: gain/pan are set and clamped; mute/solo toggle.
func TestMixerOps_setAndClamp(t *testing.T) {
	a := mixFixture()
	a, err := a.SetGain("bass", 5) // clamps to 2
	if err != nil {
		t.Fatal(err)
	}
	a, _ = a.SetPan("bass", -3) // clamps to -1
	a, _ = a.SetMute("melody", true)
	a, _ = a.SetSolo("bass", true)

	bass, _ := a.TrackByID("bass")
	mel, _ := a.TrackByID("melody")
	if bass.Strip.Gain != 2 {
		t.Errorf("gain = %.2f, want 2 (clamped)", bass.Strip.Gain)
	}
	if bass.Strip.Pan != -1 {
		t.Errorf("pan = %.2f, want -1 (clamped)", bass.Strip.Pan)
	}
	if !mel.Strip.Mute {
		t.Error("melody not muted")
	}
	if got := a.SoloedTracks(); len(got) != 1 || got[0] != "bass" {
		t.Errorf("soloed = %v, want [bass]", got)
	}
}

// TestMixer_gainOverrideLogsCorrection: changing gain off unity logs Jordan's taste.
func TestMixer_gainOverrideLogsCorrection(t *testing.T) {
	a := mixFixture()
	a.Genre = "crunkcore"
	a, err := a.SetGain("bass", 1.5)
	if err != nil {
		t.Fatal(err)
	}
	cs := a.CorrectionsByKind("gain")
	if len(cs) != 1 || cs[0].Auto != "1" || cs[0].Fixed != "1.5" {
		t.Errorf("gain correction = %+v, want 1->1.5", cs)
	}
}

// TestMixer_routeAndSidechain: routing changes the bus; sidechain is one idempotent
// declared edge (the Cubase-killer).
func TestMixer_routeAndSidechain(t *testing.T) {
	a := mixFixture()
	a, err := a.RouteTo("melody", "bus.fx")
	if err != nil {
		t.Fatal(err)
	}
	if mel, _ := a.TrackByID("melody"); mel.Strip.Bus != "bus.fx" {
		t.Errorf("route bus = %q, want bus.fx", mel.Strip.Bus)
	}
	a, _ = a.AddSidechain("bus.music", "src.drums.kick")
	a, _ = a.AddSidechain("bus.music", "src.drums.kick") // duplicate is a no-op
	b := a.busPtr("bus.music")
	if b == nil || len(b.Sidechain) != 1 || b.Sidechain[0] != "src.drums.kick" {
		t.Errorf("sidechain = %+v, want one src.drums.kick edge", b)
	}
}

// TestMixerOps_unknownTrack degrades with an error (no panic).
func TestMixerOps_unknownTrack(t *testing.T) {
	a := mixFixture()
	if _, err := a.SetGain("ghost", 1); err == nil {
		t.Error("SetGain on missing track: want error")
	}
	if _, err := a.RouteTo("ghost", "bus.x"); err == nil {
		t.Error("RouteTo on missing track: want error")
	}
}

// TestMixerOps_immutable: a mixer op returns a new arrangement; original untouched.
func TestMixerOps_immutable(t *testing.T) {
	a := mixFixture()
	if _, err := a.SetGain("bass", 2); err != nil {
		t.Fatal(err)
	}
	bass, _ := a.TrackByID("bass")
	if bass.Strip.Gain != 1 {
		t.Errorf("receiver gain mutated to %.2f, want 1", bass.Strip.Gain)
	}
}
