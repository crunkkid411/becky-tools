package dawmodel

import (
	"encoding/json"
	"testing"
)

func TestFXFields_roundTripAndCloneIndependent(t *testing.T) {
	a := New()
	a = a.AddTrack("kick", KindMIDI)
	// put an FX insert + audio file + bounced flag on the track.
	a.Tracks[0].Strip.FX = []FXSlot{{Name: "FabFilter Pro-Q 3", ClassID: "ABC", PresetRef: "q.vstpreset"}}
	a.Tracks[0].Clips = append(a.Tracks[0].Clips, Clip{Name: "kick", File: "kick.bounce.wav"})
	a.Tracks[0].Bounced = true
	a.Buses = append(a.Buses, Bus{ID: "DRUMS", Out: "bus.master", FX: []FXSlot{{Name: "Glue Comp"}}})

	// JSON round-trip preserves the new fields.
	data, _ := json.Marshal(a)
	var back Arrangement
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Tracks[0].Strip.FX) != 1 || back.Tracks[0].Strip.FX[0].Name != "FabFilter Pro-Q 3" {
		t.Errorf("strip FX lost in round-trip: %+v", back.Tracks[0].Strip.FX)
	}
	if back.Tracks[0].Clips[0].File != "kick.bounce.wav" || !back.Tracks[0].Bounced {
		t.Errorf("clip.File / Bounced lost: %+v", back.Tracks[0])
	}
	if len(back.Buses[0].FX) != 1 {
		t.Errorf("bus FX lost: %+v", back.Buses[0])
	}

	// A clone's FX slices are independent (mutating the clone must not touch the original).
	c := a.clone()
	c.Tracks[0].Strip.FX[0].Bypass = true
	c.Buses[0].FX[0].Bypass = true
	if a.Tracks[0].Strip.FX[0].Bypass || a.Buses[0].FX[0].Bypass {
		t.Error("clone shares FX slice with the original — immutability broken")
	}
}
