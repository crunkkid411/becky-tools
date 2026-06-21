package autoroute

import (
	"testing"

	"becky-go/internal/dawmodel"
)

func TestBusFor_jordansRules(t *testing.T) {
	rs := DefaultRuleset()
	cases := map[string]string{
		"kick":        "DRUMS",
		"snare":       "DRUMS",
		"808":         "BASS", // in trap, "808" is the sub-bass, not the kick
		"bass":        "BASS",
		"bass synth":  "BASS", // BASS rule wins over SYNTH (ordering)
		"reese bass":  "BASS",
		"serum lead":  "SYNTH",
		"synth":       "SYNTH",
		"pad":         "SYNTH",
		"lead guitar": "GUITARS", // "guitar" — but "lead" is also synth; guitar rule is later, so... see note
		"rhythm gtr":  "GUITARS",
		"lead vox":    "VOCALS",
		"adlib":       "VOCALS",
		"riser":       "FX",
		"weird thing": "MUSIC", // default
	}
	for label, want := range cases {
		if got := rs.BusFor(label); got != want {
			t.Errorf("BusFor(%q) = %q, want %q", label, got, want)
		}
	}
}

func TestBusFor_bassBeatsSynth(t *testing.T) {
	// The load-bearing rule from Jordan: a synth labelled bass goes to BASS, not SYNTH.
	rs := DefaultRuleset()
	if got := rs.BusFor("serum bass"); got != "BASS" {
		t.Errorf("'serum bass' should route to BASS (bass beats synth), got %q", got)
	}
}

func TestApply_routesAndBuses(t *testing.T) {
	a := dawmodel.New()
	for _, id := range []string{"kick", "bass", "serum lead", "lead vox", "drums"} {
		a = a.AddTrack(id, dawmodel.KindMIDI)
	}
	out, assigns := Apply(a, DefaultRuleset())

	// Every default bus exists.
	busIDs := map[string]bool{}
	for _, b := range out.Buses {
		busIDs[b.ID] = true
	}
	for _, want := range []string{"DRUMS", "BASS", "SYNTH", "VOCALS"} {
		if !busIDs[want] {
			t.Errorf("bus %q not created", want)
		}
	}
	// Tracks routed to the right bus.
	wantBus := map[string]string{"kick": "DRUMS", "bass": "BASS", "serum lead": "SYNTH", "lead vox": "VOCALS", "drums": "DRUMS"}
	for _, tr := range out.Tracks {
		if tr.Strip.Bus != wantBus[tr.ID] {
			t.Errorf("track %q routed to %q, want %q", tr.ID, tr.Strip.Bus, wantBus[tr.ID])
		}
	}
	if len(assigns) != len(out.Tracks) {
		t.Errorf("expected an assignment per track, got %d", len(assigns))
	}
}

func TestApply_sidechainBassOffKick(t *testing.T) {
	a := dawmodel.New()
	a = a.AddTrack("kick", dawmodel.KindMIDI)
	a = a.AddTrack("bass", dawmodel.KindMIDI)
	out, _ := Apply(a, DefaultRuleset())
	var bassBus *dawmodel.Bus
	for i := range out.Buses {
		if out.Buses[i].ID == "BASS" {
			bassBus = &out.Buses[i]
		}
	}
	if bassBus == nil {
		t.Fatal("no BASS bus")
	}
	found := false
	for _, s := range bassBus.Sidechain {
		if s == "kick" {
			found = true
		}
	}
	if !found {
		t.Errorf("BASS should be sidechained off the kick, got %v", bassBus.Sidechain)
	}
}

func TestApply_immutable(t *testing.T) {
	a := dawmodel.New()
	a = a.AddTrack("kick", dawmodel.KindMIDI)
	before := a.Tracks[0].Strip.Bus
	Apply(a, DefaultRuleset())
	if a.Tracks[0].Strip.Bus != before {
		t.Error("Apply mutated the input arrangement")
	}
}

func TestApply_nilSafe(t *testing.T) {
	out, assigns := Apply(nil, DefaultRuleset())
	if out != nil || assigns != nil {
		t.Error("Apply(nil) should be safe")
	}
}
