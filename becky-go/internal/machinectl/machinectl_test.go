package machinectl

import (
	"bytes"
	"strings"
	"testing"

	"becky-go/internal/drumcmd"
	"becky-go/internal/drummachine"
)

// newTestMachine returns a fresh machine with a recognisable backbeat so beat
// transforms have something to chew on (kick on 0/8, snare on 4/12, hats on evens).
func newTestMachine(t *testing.T) *drummachine.Machine {
	t.Helper()
	m := drummachine.NewMachine()
	var err error
	// Start pads below unity so "louder" has headroom (unity is the max).
	for i := 0; i < drummachine.PadCount; i++ {
		m, err = m.SetPadLevel(i, 0.6)
		if err != nil {
			t.Fatalf("seed level: %v", err)
		}
	}
	for _, s := range []int{0, 8} {
		m, err = m.SetStep(0, 0, s, true, 110) // kick
		if err != nil {
			t.Fatalf("seed kick: %v", err)
		}
	}
	for _, s := range []int{4, 12} {
		m, err = m.SetStep(0, 1, s, true, 100) // snare
		if err != nil {
			t.Fatalf("seed snare: %v", err)
		}
	}
	for _, s := range []int{0, 2, 4, 6, 8, 10, 12, 14} {
		m, err = m.SetStep(0, 2, s, true, 80) // closed hat
		if err != nil {
			t.Fatalf("seed hat: %v", err)
		}
	}
	return m
}

// snapshot returns the deterministic bytes of a machine for immutability checks.
func snapshot(t *testing.T, m *drummachine.Machine) []byte {
	t.Helper()
	b, err := m.MarshalBytes()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// ─── Parser: phrase → correct Intent ──────────────────────────────────────────

func TestParseIntents(t *testing.T) {
	m := newTestMachine(t)
	p := DeterministicParser{}

	tests := []struct {
		name        string
		instruction string
		wantAction  Action
		check       func(t *testing.T, in Intent)
	}{
		{"empty", "", Unknown, nil},
		{"gibberish", "asdf qwerty zxcv", Unknown, nil},

		// Transport
		{"play", "play", Transport, func(t *testing.T, in Intent) {
			if in.Transport != TransportPlay {
				t.Errorf("transport = %q, want play", in.Transport)
			}
		}},
		{"stop", "stop it", Transport, func(t *testing.T, in Intent) {
			if in.Transport != TransportStop {
				t.Errorf("transport = %q, want stop", in.Transport)
			}
		}},

		// Tempo
		{"tempo", "set the tempo to 140", SetTempo, func(t *testing.T, in Intent) {
			if in.Value != 140 {
				t.Errorf("tempo value = %g, want 140", in.Value)
			}
		}},
		{"bpm", "make it 92 bpm", SetTempo, func(t *testing.T, in Intent) {
			if in.Value != 92 {
				t.Errorf("tempo value = %g, want 92", in.Value)
			}
		}},

		// Kit
		{"load kit", "load my 808 kit", LoadKit, func(t *testing.T, in Intent) {
			if in.KitName != "808" {
				t.Errorf("kit name = %q, want 808", in.KitName)
			}
		}},
		{"use kit", "use the trap kit", LoadKit, func(t *testing.T, in Intent) {
			if in.KitName != "trap" {
				t.Errorf("kit name = %q, want trap", in.KitName)
			}
		}},

		// Pad sample
		{"put sample", "put a clap on pad 5", SetPadSample, func(t *testing.T, in Intent) {
			if in.Pad != 4 {
				t.Errorf("pad = %d, want 4 (pad 5)", in.Pad)
			}
			if in.SamplePath != "clap" {
				t.Errorf("sample = %q, want clap", in.SamplePath)
			}
		}},
		{"swap named", "swap the snare", SetPadSample, func(t *testing.T, in Intent) {
			if in.Pad != 1 {
				t.Errorf("pad = %d, want 1 (snare)", in.Pad)
			}
		}},

		// Level
		{"louder", "make the kick louder", SetPadLevel, func(t *testing.T, in Intent) {
			if in.Pad != 0 {
				t.Errorf("pad = %d, want 0 (kick)", in.Pad)
			}
			if in.Value <= m.Kit.Pads[0].Level {
				t.Errorf("level %g should be louder than %g", in.Value, m.Kit.Pads[0].Level)
			}
		}},
		{"quieter", "turn the snare down", SetPadLevel, func(t *testing.T, in Intent) {
			if in.Pad != 1 {
				t.Errorf("pad = %d, want 1 (snare)", in.Pad)
			}
		}},

		// Pan
		{"pan left", "pan the hats left", SetPadPan, func(t *testing.T, in Intent) {
			if in.Pad != 2 {
				t.Errorf("pad = %d, want 2 (hat)", in.Pad)
			}
			if in.Value >= 0 {
				t.Errorf("pan = %g, want negative (left)", in.Value)
			}
		}},

		// Pitch
		{"pitch down", "pitch the kick down", SetPadPitch, func(t *testing.T, in Intent) {
			if in.Pad != 0 {
				t.Errorf("pad = %d, want 0 (kick)", in.Pad)
			}
			if in.Value >= 0 {
				t.Errorf("pitch = %g, want negative (down)", in.Value)
			}
		}},

		// Decay
		{"shorten", "shorten the snare", SetPadDecay, func(t *testing.T, in Intent) {
			if in.Pad != 1 {
				t.Errorf("pad = %d, want 1 (snare)", in.Pad)
			}
			if in.Value <= 0 || in.Value >= 1 {
				t.Errorf("decay = %g, want a short value", in.Value)
			}
		}},

		// Choke
		{"choke", "choke the hats together", SetChoke, func(t *testing.T, in Intent) {
			if in.Pad != 2 {
				t.Errorf("pad = %d, want 2 (hat)", in.Pad)
			}
			if in.Group != 1 {
				t.Errorf("group = %d, want 1", in.Group)
			}
		}},

		// Mute / solo
		{"mute", "mute the clap", MutePad, func(t *testing.T, in Intent) {
			if in.Pad != 4 || !in.On {
				t.Errorf("mute pad = %d on = %v, want 4/true", in.Pad, in.On)
			}
		}},
		{"solo", "solo the kick", SoloPad, func(t *testing.T, in Intent) {
			if in.Pad != 0 || !in.On {
				t.Errorf("solo pad = %d on = %v, want 0/true", in.Pad, in.On)
			}
		}},
		{"unmute", "unmute the clap", MutePad, func(t *testing.T, in Intent) {
			if in.Pad != 4 || in.On {
				t.Errorf("unmute pad = %d on = %v, want 4/false", in.Pad, in.On)
			}
		}},

		// Swing
		{"more swing", "more swing", SetSwing, func(t *testing.T, in Intent) {
			if in.Value <= 0.5 {
				t.Errorf("swing = %g, want > 0.5", in.Value)
			}
		}},
		{"swing pct", "swing it 66%", SetSwing, func(t *testing.T, in Intent) {
			if in.Value < 0.6 || in.Value > 0.7 {
				t.Errorf("swing = %g, want ~0.66", in.Value)
			}
		}},

		// Structure
		{"new pattern", "new pattern", NewPattern, nil},
		{"duplicate", "duplicate this pattern", DuplicatePattern, nil},
		{"add scene", "add a scene", AddScene, nil},

		// Genre starters
		{"trap", "make a trap beat", GenreStarter, func(t *testing.T, in Intent) {
			if in.Genre != "trap" {
				t.Errorf("genre = %q, want trap", in.Genre)
			}
		}},
		{"four on the floor", "give me a four on the floor beat", GenreStarter, func(t *testing.T, in Intent) {
			if in.Genre != "four-on-the-floor" {
				t.Errorf("genre = %q, want four-on-the-floor", in.Genre)
			}
		}},
		{"boom bap", "make a boom bap groove", GenreStarter, func(t *testing.T, in Intent) {
			if in.Genre != "boom-bap" {
				t.Errorf("genre = %q, want boom-bap", in.Genre)
			}
		}},

		// Beat transforms (delegated to drumcmd)
		{"half-time", "make it half-time", Beat, func(t *testing.T, in Intent) {
			if in.Drum.Action != drumcmd.HalfTime {
				t.Errorf("drum action = %v, want half-time", in.Drum.Action)
			}
		}},
		{"humanize snare", "humanize the snare", Beat, func(t *testing.T, in Intent) {
			if in.Drum.Action != drumcmd.Humanize {
				t.Errorf("drum action = %v, want humanize", in.Drum.Action)
			}
			if in.Drum.Lane != "snare" {
				t.Errorf("drum lane = %q, want snare", in.Drum.Lane)
			}
		}},
		{"add fill", "add a fill", Beat, func(t *testing.T, in Intent) {
			if in.Drum.Action != drumcmd.Fill {
				t.Errorf("drum action = %v, want fill", in.Drum.Action)
			}
		}},
		{"variation", "give me a variation", Beat, func(t *testing.T, in Intent) {
			if in.Drum.Action != drumcmd.Variations {
				t.Errorf("drum action = %v, want variations", in.Drum.Action)
			}
		}},
		{"busier", "make it busier", Beat, func(t *testing.T, in Intent) {
			if in.Drum.Action != drumcmd.Density || !in.Drum.Up {
				t.Errorf("drum action = %v up = %v, want density/up", in.Drum.Action, in.Drum.Up)
			}
		}},
		{"tighten", "tighten it to the grid", Beat, func(t *testing.T, in Intent) {
			if in.Drum.Action != drumcmd.Quantize {
				t.Errorf("drum action = %v, want quantize", in.Drum.Action)
			}
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in, err := p.Parse(tc.instruction, m)
			if err != nil {
				t.Fatalf("Parse error: %v", err)
			}
			if in.Action != tc.wantAction {
				t.Fatalf("action = %v, want %v (note: %q)", in.Action, tc.wantAction, in.Note)
			}
			if tc.check != nil {
				tc.check(t, in)
			}
		})
	}
}

// ─── Apply: correct Machine change + immutability ─────────────────────────────

func TestApplyEdits(t *testing.T) {
	tests := []struct {
		name        string
		instruction string
		verify      func(t *testing.T, before, after *drummachine.Machine)
	}{
		{"tempo", "set the tempo to 140", func(t *testing.T, before, after *drummachine.Machine) {
			if after.Tempo != 140 {
				t.Errorf("tempo = %g, want 140", after.Tempo)
			}
		}},
		{"louder kick", "make the kick way louder", func(t *testing.T, before, after *drummachine.Machine) {
			if after.Kit.Pads[0].Level <= before.Kit.Pads[0].Level {
				t.Errorf("kick level not increased: %g -> %g", before.Kit.Pads[0].Level, after.Kit.Pads[0].Level)
			}
		}},
		{"pan hats left", "pan the hats left", func(t *testing.T, before, after *drummachine.Machine) {
			if after.Kit.Pads[2].Pan >= 0 {
				t.Errorf("hat pan = %g, want negative", after.Kit.Pads[2].Pan)
			}
		}},
		{"pitch kick down", "pitch the kick down", func(t *testing.T, before, after *drummachine.Machine) {
			if after.Kit.Pads[0].PitchSemitones >= 0 {
				t.Errorf("kick pitch = %g, want negative", after.Kit.Pads[0].PitchSemitones)
			}
		}},
		{"shorten snare", "shorten the snare", func(t *testing.T, before, after *drummachine.Machine) {
			if after.Kit.Pads[1].Decay <= 0 {
				t.Errorf("snare decay = %g, want > 0", after.Kit.Pads[1].Decay)
			}
		}},
		{"mute clap", "mute the clap", func(t *testing.T, before, after *drummachine.Machine) {
			if !after.Kit.Pads[4].Mute {
				t.Error("clap not muted")
			}
		}},
		{"solo kick", "solo the kick", func(t *testing.T, before, after *drummachine.Machine) {
			if !after.Kit.Pads[0].Solo {
				t.Error("kick not soloed")
			}
		}},
		{"choke hats", "choke the hats together", func(t *testing.T, before, after *drummachine.Machine) {
			if after.Kit.Pads[2].ChokeGroup != 1 {
				t.Errorf("hat choke group = %d, want 1", after.Kit.Pads[2].ChokeGroup)
			}
		}},
		{"sample", "put a clap on pad 5", func(t *testing.T, before, after *drummachine.Machine) {
			if after.Kit.Pads[4].SamplePath != "clap" {
				t.Errorf("pad 5 sample = %q, want clap", after.Kit.Pads[4].SamplePath)
			}
		}},
		{"swing", "swing it 66%", func(t *testing.T, before, after *drummachine.Machine) {
			if after.Bank.Patterns[0].Swing <= 0.5 {
				t.Errorf("swing = %g, want > 0.5", after.Bank.Patterns[0].Swing)
			}
		}},
		{"new pattern", "new pattern", func(t *testing.T, before, after *drummachine.Machine) {
			if after.PatternCount() != before.PatternCount()+1 {
				t.Errorf("pattern count = %d, want %d", after.PatternCount(), before.PatternCount()+1)
			}
		}},
		{"duplicate", "duplicate this pattern", func(t *testing.T, before, after *drummachine.Machine) {
			if after.PatternCount() != before.PatternCount()+1 {
				t.Errorf("pattern count = %d, want %d", after.PatternCount(), before.PatternCount()+1)
			}
		}},
		{"scene", "add a scene", func(t *testing.T, before, after *drummachine.Machine) {
			if after.SceneCount() != before.SceneCount()+1 {
				t.Errorf("scene count = %d, want %d", after.SceneCount(), before.SceneCount()+1)
			}
		}},
		{"kit name", "load my 808 kit", func(t *testing.T, before, after *drummachine.Machine) {
			if !strings.Contains(strings.ToLower(after.Kit.Name), "808") {
				t.Errorf("kit name = %q, want it to mention 808", after.Kit.Name)
			}
		}},
	}

	p := DeterministicParser{}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := newTestMachine(t)
			beforeBytes := snapshot(t, before)

			in, _ := p.Parse(tc.instruction, before)
			after, summary, err := Apply(before, in)
			if err != nil {
				t.Fatalf("Apply error: %v", err)
			}
			if after == nil {
				t.Fatal("Apply returned nil machine")
			}
			if summary == "" {
				t.Error("expected a non-empty summary")
			}
			tc.verify(t, before, after)

			// Immutability: the input machine is byte-identical to its snapshot.
			if got := snapshot(t, before); !bytes.Equal(got, beforeBytes) {
				t.Error("Apply mutated the input machine")
			}
		})
	}
}

// ─── Beat edits round-trip through drumcmd ────────────────────────────────────

func TestBeatHalfTimeRoundTrip(t *testing.T) {
	m := newTestMachine(t)
	beforeBytes := snapshot(t, m)
	p := DeterministicParser{}

	in, _ := p.Parse("make it half-time", m)
	if in.Action != Beat {
		t.Fatalf("action = %v, want Beat", in.Action)
	}
	after, summary, err := Apply(m, in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !strings.Contains(strings.ToLower(summary), "half") {
		t.Errorf("summary = %q, want it to mention half-time", summary)
	}

	// The snare (pad 1) was on steps 4 and 12; half-time moves a hit from step 4
	// to step 8 (beat 3) — the classic half-time backbeat.
	if !after.Bank.Patterns[0].Lanes[1][8].On {
		t.Error("expected the snare to land on step 8 after half-time")
	}
	// Input unchanged.
	if got := snapshot(t, m); !bytes.Equal(got, beforeBytes) {
		t.Error("beat transform mutated the input machine")
	}
}

func TestBeatBusierAddsHats(t *testing.T) {
	m := newTestMachine(t)
	p := DeterministicParser{}
	in, _ := p.Parse("make it busier", m)
	after, _, err := Apply(m, in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	before, after2 := countHits(m, 2), countHits(after, 2)
	if after2 <= before {
		t.Errorf("hat hits = %d, want more than %d (busier should add hats)", after2, before)
	}
}

func countHits(m *drummachine.Machine, pad int) int {
	n := 0
	for _, c := range m.Bank.Patterns[0].Lanes[pad] {
		if c.On {
			n++
		}
	}
	return n
}

// ─── Genre starters deterministic ─────────────────────────────────────────────

func TestGenreStarterDeterministic(t *testing.T) {
	for _, genre := range []string{"trap", "boom-bap", "house", "four-on-the-floor"} {
		m := newTestMachine(t)
		a, _, err := applyGenreStarter(m, genre)
		if err != nil {
			t.Fatalf("%s: %v", genre, err)
		}
		b, _, err := applyGenreStarter(m, genre)
		if err != nil {
			t.Fatalf("%s (2nd): %v", genre, err)
		}
		if !bytes.Equal(snapshot(t, a), snapshot(t, b)) {
			t.Errorf("%s starter not deterministic", genre)
		}
		// A starter must put a kick down (pad 0 has at least one hit).
		if countHits(a, 0) == 0 {
			t.Errorf("%s starter has no kick", genre)
		}
	}
}

func TestTrapStarterShape(t *testing.T) {
	m := newTestMachine(t)
	a, summary, err := applyGenreStarter(m, "trap")
	if err != nil {
		t.Fatalf("trap: %v", err)
	}
	if !strings.Contains(strings.ToLower(summary), "trap") {
		t.Errorf("summary = %q, want it to mention trap", summary)
	}
	// Trap kick on steps 0, 6, 10.
	for _, s := range []int{0, 6, 10} {
		if !a.Bank.Patterns[0].Lanes[0][s].On {
			t.Errorf("trap kick missing on step %d", s)
		}
	}
	// Snare backbeat on 4 and 12.
	if !a.Bank.Patterns[0].Lanes[1][4].On || !a.Bank.Patterns[0].Lanes[1][12].On {
		t.Error("trap snare backbeat missing")
	}
}

func TestGenreStarterUnknownDegrades(t *testing.T) {
	m := newTestMachine(t)
	out, summary, err := applyGenreStarter(m, "polka")
	if err == nil {
		t.Error("expected an error for an unknown genre")
	}
	if out == nil || summary == "" {
		t.Error("expected a safe machine + friendly summary on unknown genre")
	}
}

// ─── Determinism of the full path ─────────────────────────────────────────────

func TestParseApplyDeterministic(t *testing.T) {
	phrases := []string{
		"make it half-time", "humanize the snare", "make a trap beat",
		"set the tempo to 128", "pan the hats left", "mute the clap",
		"swing it 60%", "make it busier",
	}
	p := DeterministicParser{}
	for _, ph := range phrases {
		m := newTestMachine(t)
		in1, _ := p.Parse(ph, m)
		in2, _ := p.Parse(ph, m)
		if in1.Action != in2.Action || in1.Pad != in2.Pad || in1.Value != in2.Value {
			t.Errorf("%q: parse not deterministic", ph)
		}
		a1, _, _ := Apply(m, in1)
		a2, _, _ := Apply(m, in2)
		if !bytes.Equal(snapshot(t, a1), snapshot(t, a2)) {
			t.Errorf("%q: apply not deterministic", ph)
		}
	}
}

// ─── Unknown / degrade ────────────────────────────────────────────────────────

func TestUnknownDegrades(t *testing.T) {
	m := newTestMachine(t)
	beforeBytes := snapshot(t, m)
	p := DeterministicParser{}

	in, err := p.Parse("flibbertigibbet the wormhole", m)
	if err != nil {
		t.Fatalf("Parse should not error on gibberish: %v", err)
	}
	if in.Action != Unknown {
		t.Fatalf("action = %v, want Unknown", in.Action)
	}
	if in.Note == "" {
		t.Error("expected a friendly note on Unknown")
	}

	after, summary, err := Apply(m, in)
	if err != nil {
		t.Fatalf("Apply should not error on Unknown: %v", err)
	}
	if summary == "" {
		t.Error("expected a friendly summary")
	}
	// Machine unchanged.
	if got := snapshot(t, after); !bytes.Equal(got, beforeBytes) {
		t.Error("Unknown should not change the machine")
	}
}

func TestApplyNilMachineSafe(t *testing.T) {
	p := DeterministicParser{}
	in, _ := p.Parse("set the tempo to 100", nil)
	out, _, err := Apply(nil, in)
	if err != nil {
		t.Fatalf("Apply(nil) error: %v", err)
	}
	if out == nil {
		t.Fatal("Apply(nil) returned nil")
	}
	if out.Tempo != 100 {
		t.Errorf("tempo = %g, want 100", out.Tempo)
	}
}

func TestTransportNoEdit(t *testing.T) {
	m := newTestMachine(t)
	beforeBytes := snapshot(t, m)
	p := DeterministicParser{}
	in, _ := p.Parse("play", m)
	after, summary, err := Apply(m, in)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if summary == "" {
		t.Error("expected a transport summary")
	}
	if got := snapshot(t, after); !bytes.Equal(got, beforeBytes) {
		t.Error("transport should not change the machine")
	}
	if in.Transport != TransportPlay {
		t.Errorf("transport = %q, want play", in.Transport)
	}
}

// ─── Unresolvable pad degrades to Unknown ─────────────────────────────────────

func TestUnresolvablePadDegrades(t *testing.T) {
	m := newTestMachine(t)
	p := DeterministicParser{}
	in, _ := p.Parse("mute the flugelhorn", m)
	if in.Action != Unknown {
		t.Errorf("action = %v, want Unknown for an unknown pad name", in.Action)
	}
	if in.Note == "" {
		t.Error("expected a friendly note")
	}
}

// ─── ModelParser degrade-to-deterministic ─────────────────────────────────────

func TestModelParserDegradesOnError(t *testing.T) {
	m := newTestMachine(t)
	mp := ModelParser{
		bin:   "x",
		model: "y",
		run: func(bin, model, prompt string) (string, error) {
			return "", errModelStub // simulate a failed exec
		},
	}
	in, err := mp.Parse("make it half-time", m)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if in.Action != Beat || in.Drum.Action != drumcmd.HalfTime {
		t.Errorf("expected degrade to deterministic Beat/half-time, got %v", in.Action)
	}
}

func TestModelParserUsesModelJSON(t *testing.T) {
	m := newTestMachine(t)
	mp := ModelParser{
		bin:   "x",
		model: "y",
		run: func(bin, model, prompt string) (string, error) {
			// The model resolves a free-form phrase to a structured edit.
			return `chatter {"action":"set_tempo","pad":-1,"value":174,"note":"set tempo"} trailing`, nil
		},
	}
	in, err := mp.Parse("can you take it up to dnb tempo", m)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if in.Action != SetTempo || in.Value != 174 {
		t.Errorf("model parse = %v/%g, want set-tempo/174", in.Action, in.Value)
	}
}

func TestModelParserBeatReparsedByDrumcmd(t *testing.T) {
	m := newTestMachine(t)
	mp := ModelParser{
		bin:   "x",
		model: "y",
		run: func(bin, model, prompt string) (string, error) {
			return `{"action":"beat","drum":"humanize the snare","note":"loosen it up"}`, nil
		},
	}
	in, err := mp.Parse("loosen up the snare a touch", m)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if in.Action != Beat || in.Drum.Action != drumcmd.Humanize || in.Drum.Lane != "snare" {
		t.Errorf("expected re-parsed humanize/snare, got %v %v/%q", in.Action, in.Drum.Action, in.Drum.Lane)
	}
}

func TestModelParserNilRunIsDeterministic(t *testing.T) {
	m := newTestMachine(t)
	mp := ModelParser{bin: "x", model: "y", run: nil}
	in, _ := mp.Parse("make a trap beat", m)
	if in.Action != GenreStarter || in.Genre != "trap" {
		t.Errorf("nil-run ModelParser should behave deterministically, got %v/%q", in.Action, in.Genre)
	}
}

// ─── PickParser returns a usable parser ───────────────────────────────────────

func TestPickParserAlwaysUsable(t *testing.T) {
	// PickParser must always return a non-nil parser that doesn't error on a
	// valid instruction. On CI/cloud (no binary+model on disk) it returns
	// DeterministicParser; on Jordan's local machine it may return ModelParser
	// and the model decides the action — so we only require: no error, non-Unknown.
	p := PickParser()
	m := newTestMachine(t)
	in, err := p.Parse("make it half-time", m)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// DeterministicParser always yields Beat for this phrase; ModelParser may
	// yield a different valid action. Either way action must not be Unknown.
	if in.Action == Unknown {
		t.Errorf("PickParser returned Unknown for a well-documented phrase — degrade path broken; action = %v", in.Action)
	}
}

func TestDeterministicParserHalfTime(t *testing.T) {
	// Pin the deterministic parse of a documented phrase independent of PickParser
	// so the regression is always caught even when PickParser routes to the model.
	p := DeterministicParser{}
	m := newTestMachine(t)
	in, err := p.Parse("make it half-time", m)
	if err != nil {
		t.Fatalf("DeterministicParser.Parse: %v", err)
	}
	if in.Action != Beat {
		t.Errorf("action = %v, want Beat", in.Action)
	}
}

// ─── Action.String coverage ───────────────────────────────────────────────────

func TestActionString(t *testing.T) {
	cases := map[Action]string{
		Unknown:          "unknown",
		Beat:             "beat",
		LoadKit:          "load-kit",
		SetPadSample:     "set-pad-sample",
		SetPadLevel:      "set-pad-level",
		SetTempo:         "set-tempo",
		Transport:        "transport",
		GenreStarter:     "genre-starter",
		DuplicatePattern: "duplicate-pattern",
	}
	for a, want := range cases {
		if got := a.String(); got != want {
			t.Errorf("Action(%d).String() = %q, want %q", a, got, want)
		}
	}
}

// ─── logEdit is best-effort (no panic with no LogPath) ────────────────────────

func TestLogEditBestEffort(t *testing.T) {
	saved := LogPath
	defer func() { LogPath = saved }()
	LogPath = "" // logging off
	logEdit("kick", "level", "1.0", "0.8")
	// reaching here without panic is the assertion
}
