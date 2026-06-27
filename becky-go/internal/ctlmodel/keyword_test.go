package ctlmodel

import (
	"math"
	"testing"

	"becky-go/internal/ctledit"
	"becky-go/internal/dawmodel"
)

// testArr mirrors a small becky-compose session: bass / lead / drums tracks with
// mixer strips + buses, so keyword edits resolve against real track IDs.
func testArr() *dawmodel.Arrangement {
	return &dawmodel.Arrangement{
		BPM: 140, PPQ: 480, Num: 4, Den: 4, Genre: "crunkcore", Root: "F", Scale: "minor",
		Tracks: []dawmodel.Track{
			{ID: "bass", Kind: dawmodel.KindMIDI, Strip: dawmodel.Strip{Gain: 1, Pan: 0, Bus: "bus.808"},
				Clips: []dawmodel.Clip{{Name: "bass", Notes: []dawmodel.Note{{ID: 1, Start: 0, Dur: 240, Pitch: 36, Vel: 100}}}}},
			{ID: "lead", Kind: dawmodel.KindMIDI, Strip: dawmodel.Strip{Gain: 0.8, Pan: 0, Bus: "bus.music"},
				Clips: []dawmodel.Clip{{Name: "lead", Notes: []dawmodel.Note{{ID: 2, Start: 0, Dur: 240, Pitch: 60, Vel: 90}}}}},
			{ID: "drums", Kind: dawmodel.KindMIDI, Strip: dawmodel.Strip{Gain: 1, Pan: 0, Bus: "bus.drums"},
				Clips: []dawmodel.Clip{{Name: "drums", Channel: 9, Program: -1, Notes: []dawmodel.Note{{ID: 3, Start: 0, Dur: 120, Pitch: 36, Vel: 110}}}}},
		},
		Buses:  []dawmodel.Bus{{ID: "bus.808", Out: "master"}, {ID: "bus.music", Out: "master"}, {ID: "bus.drums", Out: "master"}},
		NextID: 3,
	}
}

func TestParseKeyword_Recognized(t *testing.T) {
	cases := []struct {
		name   string
		instr  string
		wantOp string
		check  func(t *testing.T, ed ctledit.BeckyEdit)
	}{
		{"tempo to", "set tempo to 128", ctledit.OpSetTempo, func(t *testing.T, ed ctledit.BeckyEdit) {
			if ed.BPM != 128 {
				t.Errorf("bpm = %d, want 128", ed.BPM)
			}
		}},
		{"tempo bare", "tempo 92", ctledit.OpSetTempo, func(t *testing.T, ed ctledit.BeckyEdit) {
			if ed.BPM != 92 {
				t.Errorf("bpm = %d, want 92", ed.BPM)
			}
		}},
		{"bpm suffix", "140 bpm", ctledit.OpSetTempo, func(t *testing.T, ed ctledit.BeckyEdit) {
			if ed.BPM != 140 {
				t.Errorf("bpm = %d, want 140", ed.BPM)
			}
		}},
		{"mute", "mute the bass", ctledit.OpMute, func(t *testing.T, ed ctledit.BeckyEdit) {
			if ed.Target != "bass" || !ed.Muted {
				t.Errorf("got target=%q muted=%v, want bass/true", ed.Target, ed.Muted)
			}
		}},
		{"unmute", "unmute the bass", ctledit.OpMute, func(t *testing.T, ed ctledit.BeckyEdit) {
			if ed.Target != "bass" || ed.Muted {
				t.Errorf("got target=%q muted=%v, want bass/false", ed.Target, ed.Muted)
			}
		}},
		{"solo", "solo the drums", ctledit.OpSolo, func(t *testing.T, ed ctledit.BeckyEdit) {
			if ed.Target != "drums" || !ed.Soloed {
				t.Errorf("got target=%q soloed=%v, want drums/true", ed.Target, ed.Soloed)
			}
		}},
		{"unsolo", "unsolo the drums", ctledit.OpSolo, func(t *testing.T, ed ctledit.BeckyEdit) {
			if ed.Soloed {
				t.Errorf("soloed=%v, want false", ed.Soloed)
			}
		}},
		{"pan left", "pan the lead left", ctledit.OpSetPan, func(t *testing.T, ed ctledit.BeckyEdit) {
			if ed.Target != "lead" || ed.Pan != -0.5 {
				t.Errorf("got target=%q pan=%v, want lead/-0.5", ed.Target, ed.Pan)
			}
		}},
		{"pan hard right", "pan the bass hard right", ctledit.OpSetPan, func(t *testing.T, ed ctledit.BeckyEdit) {
			if ed.Pan != 1 {
				t.Errorf("pan=%v, want 1", ed.Pan)
			}
		}},
		{"pan center", "pan the lead center", ctledit.OpSetPan, func(t *testing.T, ed ctledit.BeckyEdit) {
			if ed.Pan != 0 {
				t.Errorf("pan=%v, want 0", ed.Pan)
			}
		}},
		{"gain louder", "make the bass louder", ctledit.OpSetGain, func(t *testing.T, ed ctledit.BeckyEdit) {
			if ed.Gain == nil || math.Abs(*ed.Gain-relGainStep) > 1e-6 {
				t.Errorf("gain=%v, want ~%v", ed.Gain, relGainStep)
			}
		}},
		{"gain quieter", "make the lead quieter", ctledit.OpSetGain, func(t *testing.T, ed ctledit.BeckyEdit) {
			want := 0.8 / relGainStep
			if ed.Gain == nil || math.Abs(*ed.Gain-want) > 1e-6 {
				t.Errorf("gain=%v, want ~%v", ed.Gain, want)
			}
		}},
		{"gain explicit", "set the lead gain to 0.5", ctledit.OpSetGain, func(t *testing.T, ed ctledit.BeckyEdit) {
			if ed.Gain == nil || *ed.Gain != 0.5 {
				t.Errorf("gain=%v, want 0.5", ed.Gain)
			}
		}},
		{"transpose octave", "transpose the lead up an octave", ctledit.OpTranspose, func(t *testing.T, ed ctledit.BeckyEdit) {
			if ed.Track != "lead" || ed.Semitones != 12 {
				t.Errorf("got track=%q semis=%d, want lead/12", ed.Track, ed.Semitones)
			}
		}},
		{"transpose down default track", "transpose down 3 semitones", ctledit.OpTranspose, func(t *testing.T, ed ctledit.BeckyEdit) {
			if ed.Track != "bass" || ed.Semitones != -3 {
				t.Errorf("got track=%q semis=%d, want bass/-3", ed.Track, ed.Semitones)
			}
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			arr := testArr()
			b := ParseKeyword(c.instr, arr)
			if len(b.Edits) != 1 {
				t.Fatalf("%q: got %d edits, want 1 (summary: %s)", c.instr, len(b.Edits), b.Summary)
			}
			if b.Edits[0].Op != c.wantOp {
				t.Fatalf("%q: op = %q, want %q", c.instr, b.Edits[0].Op, c.wantOp)
			}
			c.check(t, b.Edits[0])

			// Grounding: every recognized batch must apply cleanly to the same session.
			_, res, err := ctledit.Apply(arr, b, nil)
			if err != nil {
				t.Fatalf("%q: Apply error: %v", c.instr, err)
			}
			if res.Applied != 1 || res.Skipped != 0 {
				t.Fatalf("%q: applied=%d skipped=%d, want 1/0 (%+v)", c.instr, res.Applied, res.Skipped, res.Outcomes)
			}
		})
	}
}

func TestParseKeyword_RouteAndSidechain(t *testing.T) {
	t.Run("route to music bus", func(t *testing.T) {
		arr := testArr()
		b := ParseKeyword("route the lead to the music bus", arr)
		if len(b.Edits) != 1 || b.Edits[0].Op != ctledit.OpRouteTo {
			t.Fatalf("got %+v, want one route_to", b)
		}
		if b.Edits[0].Target != "lead" || b.Edits[0].BusID != "bus.music" {
			t.Errorf("got target=%q bus=%q, want lead/bus.music", b.Edits[0].Target, b.Edits[0].BusID)
		}
		next, res, _ := ctledit.Apply(arr, b, nil)
		if res.Applied != 1 || res.Skipped != 0 {
			t.Fatalf("apply route: %d/%d", res.Applied, res.Skipped)
		}
		if tr, _ := next.TrackByID("lead"); tr.Strip.Bus != "bus.music" {
			t.Errorf("lead bus = %q, want bus.music", tr.Strip.Bus)
		}
	})

	t.Run("send to drums bus disambiguates track vs bus", func(t *testing.T) {
		// "lead" is the track, "drums" names the destination bus — not the drums track.
		b := ParseKeyword("send the lead to the drums bus", testArr())
		if len(b.Edits) != 1 || b.Edits[0].Target != "lead" || b.Edits[0].BusID != "bus.drums" {
			t.Fatalf("got %+v, want route lead -> bus.drums", b)
		}
	})

	t.Run("sidechain bass to kick", func(t *testing.T) {
		arr := testArr()
		b := ParseKeyword("sidechain the bass to the kick", arr)
		if len(b.Edits) != 1 || b.Edits[0].Op != ctledit.OpAddSidechain {
			t.Fatalf("got %+v, want one add_sidechain", b)
		}
		// bass's bus (bus.808) ducked by the drums track (kick maps to drums).
		if b.Edits[0].BusID != "bus.808" || b.Edits[0].SidechainSource != "drums" {
			t.Errorf("got bus=%q source=%q, want bus.808/drums", b.Edits[0].BusID, b.Edits[0].SidechainSource)
		}
		next, res, _ := ctledit.Apply(arr, b, nil)
		if res.Applied != 1 || res.Skipped != 0 {
			t.Fatalf("apply sidechain: %d/%d", res.Applied, res.Skipped)
		}
		bus := busByID(next, "bus.808")
		if bus == nil || len(bus.Sidechain) != 1 || bus.Sidechain[0] != "drums" {
			t.Errorf("bus.808 sidechain = %+v, want [drums]", bus)
		}
	})

	t.Run("duck music under the kick", func(t *testing.T) {
		b := ParseKeyword("duck the music under the kick", testArr())
		// "music" isn't a track id here, so this should decline cleanly (no wrong edit).
		// chords/counter/lead route to bus.music but there's no track literally named music.
		if len(b.Edits) != 0 {
			t.Fatalf("expected no edit for unresolved victim, got %+v", b)
		}
	})
}

func busByID(a *dawmodel.Arrangement, id string) *dawmodel.Bus {
	for i := range a.Buses {
		if a.Buses[i].ID == id {
			return &a.Buses[i]
		}
	}
	return nil
}

func TestParseKeyword_AppliesChangeTheState(t *testing.T) {
	arr := testArr()

	next, _, _ := ctledit.Apply(arr, ParseKeyword("set tempo to 128", arr), nil)
	if next.BPM != 128 {
		t.Errorf("tempo: bpm = %d, want 128", next.BPM)
	}

	next, _, _ = ctledit.Apply(arr, ParseKeyword("mute the bass", arr), nil)
	if got, _ := next.TrackByID("bass"); !got.Strip.Mute {
		t.Errorf("mute: bass strip mute = false, want true")
	}

	next, _, _ = ctledit.Apply(arr, ParseKeyword("pan the lead hard left", arr), nil)
	if got, _ := next.TrackByID("lead"); got.Strip.Pan != -1 {
		t.Errorf("pan: lead pan = %v, want -1", got.Strip.Pan)
	}
}

func TestParseKeyword_Unrecognized(t *testing.T) {
	for _, instr := range []string{"", "   ", "make me a sandwich", "do the thing"} {
		b := ParseKeyword(instr, testArr())
		if len(b.Edits) != 0 {
			t.Errorf("%q: got %d edits, want 0", instr, len(b.Edits))
		}
		if b.Summary == "" {
			t.Errorf("%q: empty summary; want a helpful hint", instr)
		}
	}
}

func TestParseKeyword_UnknownTrackDegrades(t *testing.T) {
	// A named-but-absent track yields no edit (not a wrong-track edit).
	b := ParseKeyword("mute the trombone", testArr())
	if len(b.Edits) != 0 {
		t.Errorf("got %d edits for unknown track, want 0", len(b.Edits))
	}
}

func TestParseKeyword_NilArrangement(t *testing.T) {
	// Transport edits need no arrangement; track edits degrade to no-edits.
	if b := ParseKeyword("set tempo to 120", nil); len(b.Edits) != 1 {
		t.Errorf("tempo with nil arr: got %d edits, want 1", len(b.Edits))
	}
	if b := ParseKeyword("mute the bass", nil); len(b.Edits) != 0 {
		t.Errorf("mute with nil arr: got %d edits, want 0", len(b.Edits))
	}
}

func TestContainsWord(t *testing.T) {
	cases := []struct {
		s, sub string
		want   bool
	}{
		{"mute the bass", "bass", true},
		{"the bassoon plays", "bass", false},
		{"pan lead left", "lead", true},
		{"misleading text", "lead", false},
		{"drums", "drums", true},
	}
	for _, c := range cases {
		if got := containsWord(c.s, c.sub); got != c.want {
			t.Errorf("containsWord(%q,%q) = %v, want %v", c.s, c.sub, got, c.want)
		}
	}
}
