package audioengine

import "testing"

// These guard the "no sound" fix: the auto-selector must prefer real hardware (the
// UR12) over a VIRTUAL device (Voicemod / VB-Cable / Voicemeeter), even when the
// virtual one is the OS default and flagged as an interface.

func TestPickPreferred_DeprioritizesVirtual(t *testing.T) {
	devs := []Device{
		{ID: "a-voicemod", Name: "Line (Voicemod Virtual Audio Device (WDM))", Kind: KindOutput, IsInterface: true, IsDefault: true},
		{ID: "z-ur12", Name: "Line (Steinberg UR12)", Kind: KindOutput, IsInterface: true},
		{ID: "m-builtin", Name: "Speakers (Realtek)", Kind: KindOutput, IsInterface: false},
	}
	got := pickPreferred(devs)
	if got == nil || got.ID != "z-ur12" {
		t.Fatalf("picked %+v, want the real UR12 over the Voicemod virtual device", got)
	}
}

func TestPickPreferred_VirtualOnlyFallsBack(t *testing.T) {
	devs := []Device{{ID: "vm", Name: "Voicemod Virtual Audio Device", Kind: KindOutput, IsInterface: true}}
	got := pickPreferred(devs)
	if got == nil || got.ID != "vm" {
		t.Fatalf("a virtual device must still be chosen when it's the only one, got %+v", got)
	}
}

func TestPickPreferred_EnvOverride(t *testing.T) {
	t.Setenv("BECKY_AUDIO_DEVICE", "ur12")
	devs := []Device{
		{ID: "a-voicemod", Name: "Voicemod Virtual Audio Device", Kind: KindOutput, IsInterface: true, IsDefault: true},
		{ID: "z-ur12", Name: "Line (Steinberg UR12)", Kind: KindOutput, IsInterface: true},
	}
	got := pickPreferred(devs)
	if got == nil || got.ID != "z-ur12" {
		t.Fatalf("BECKY_AUDIO_DEVICE=ur12 should force the UR12, got %+v", got)
	}
}

func TestLooksVirtual(t *testing.T) {
	cases := map[string]bool{
		"Line (Voicemod Virtual Audio Device (WDM))": true,
		"CABLE Output (VB-Audio Virtual Cable)":      true,
		"Voicemeeter Out B1":                         true,
		"Line (Steinberg UR12)":                      false,
		"Speakers (Realtek High Definition Audio)":   false,
	}
	for name, want := range cases {
		if got := looksVirtual(name); got != want {
			t.Errorf("looksVirtual(%q)=%v want %v", name, got, want)
		}
	}
}
