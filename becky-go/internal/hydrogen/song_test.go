package hydrogen

import (
	"encoding/xml"
	"strings"
	"testing"
)

// reparseSong is the minimal structure used to validate emitted .h2song XML by reading
// it back — it asserts the elements a real Hydrogen would read are present and correct.
type reparseSong struct {
	XMLName     xml.Name `xml:"song"`
	Version     string   `xml:"version"`
	BPM         float64  `xml:"bpm"`
	Name        string   `xml:"name"`
	Mode        string   `xml:"mode"`
	LoopEnabled bool     `xml:"loopEnabled"`
	Instruments []struct {
		ID          int     `xml:"id"`
		Name        string  `xml:"name"`
		MidiOutNote int     `xml:"midiOutNote"`
		Pan         float64 `xml:"pan"`
		Layers      []struct {
			Filename string  `xml:"filename"`
			Min      float64 `xml:"min"`
			Max      float64 `xml:"max"`
		} `xml:"instrumentComponent>layer"`
	} `xml:"instrumentList>instrument"`
	Patterns []struct {
		Name        string `xml:"name"`
		Size        int    `xml:"size"`
		Denominator int    `xml:"denominator"`
		Notes       []struct {
			Position   int     `xml:"position"`
			Velocity   float64 `xml:"velocity"`
			Instrument int     `xml:"instrument"`
			Length     int     `xml:"length"`
			Key        string  `xml:"key"`
		} `xml:"noteList>note"`
	} `xml:"patternList>pattern"`
	Groups []struct {
		PatternID string `xml:"patternID"`
	} `xml:"patternSequence>group"`
}

func sampleSong() Song {
	kit := Kit{
		Name: "TestKit",
		Instruments: []Instrument{
			NewInstrument(0, "Kick", MIDIKick, "C:\\samples\\kick.wav"),
			NewInstrument(1, "Snare", MIDISnare, "C:\\samples\\snare.wav"),
		},
	}
	// Four-on-the-floor kick (steps 0,4,8,12) + backbeat snare (4,12).
	p := StepPattern("Pattern 1", map[int][]int{
		0: {0, 4, 8, 12},
		1: {4, 12},
	}, 0.8)
	return Song{
		Name:        "Test Beat",
		Author:      "becky",
		BPM:         128,
		Kit:         kit,
		Patterns:    []Pattern{p},
		LoopEnabled: true,
	}
}

func TestMarshalSong_Valid(t *testing.T) {
	b, err := MarshalSong(sampleSong())
	if err != nil {
		t.Fatalf("MarshalSong: %v", err)
	}
	out := string(b)

	if !strings.HasPrefix(out, "<?xml") {
		t.Errorf("missing XML declaration; got prefix %q", out[:minInt(20, len(out))])
	}

	var got reparseSong
	if err := xml.Unmarshal(b, &got); err != nil {
		t.Fatalf("emitted XML did not re-parse: %v\n%s", err, out)
	}

	if got.Version != FormatVersion {
		t.Errorf("version = %q, want %q", got.Version, FormatVersion)
	}
	if got.BPM != 128 {
		t.Errorf("bpm = %v, want 128", got.BPM)
	}
	if got.Name != "Test Beat" {
		t.Errorf("name = %q", got.Name)
	}
	if got.Mode != "song" {
		t.Errorf("mode = %q, want song", got.Mode)
	}
	if !got.LoopEnabled {
		t.Error("loopEnabled = false, want true")
	}
}

func TestMarshalSong_PinnedFormatVersion(t *testing.T) {
	// Contract: format version must be >= 1.2.4.
	if FormatVersion != "1.2.4" {
		t.Errorf("FormatVersion = %q; the becky contract pins >= 1.2.4", FormatVersion)
	}
	b, _ := MarshalSong(sampleSong())
	if !strings.Contains(string(b), "<version>1.2.4</version>") {
		t.Error("emitted song does not declare <version>1.2.4</version>")
	}
}

func TestMarshalSong_Instruments(t *testing.T) {
	b, _ := MarshalSong(sampleSong())
	var got reparseSong
	if err := xml.Unmarshal(b, &got); err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if len(got.Instruments) != 2 {
		t.Fatalf("instruments = %d, want 2", len(got.Instruments))
	}
	if got.Instruments[0].Name != "Kick" || got.Instruments[0].MidiOutNote != MIDIKick {
		t.Errorf("inst0 = %+v", got.Instruments[0])
	}
	if got.Instruments[1].MidiOutNote != MIDISnare {
		t.Errorf("inst1 midiOutNote = %d, want %d", got.Instruments[1].MidiOutNote, MIDISnare)
	}
	if len(got.Instruments[0].Layers) != 1 {
		t.Fatalf("inst0 layers = %d, want 1", len(got.Instruments[0].Layers))
	}
	if !strings.HasSuffix(got.Instruments[0].Layers[0].Filename, "kick.wav") {
		t.Errorf("inst0 layer filename = %q", got.Instruments[0].Layers[0].Filename)
	}
	if got.Instruments[0].Layers[0].Max != 1 {
		t.Errorf("inst0 layer max = %v, want 1", got.Instruments[0].Layers[0].Max)
	}
}

func TestMarshalSong_NotesAndPositions(t *testing.T) {
	b, _ := MarshalSong(sampleSong())
	var got reparseSong
	if err := xml.Unmarshal(b, &got); err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if len(got.Patterns) != 1 {
		t.Fatalf("patterns = %d, want 1", len(got.Patterns))
	}
	p := got.Patterns[0]
	if p.Size != TicksPerBar || p.Denominator != 4 {
		t.Errorf("pattern size/denom = %d/%d, want %d/4", p.Size, p.Denominator, TicksPerBar)
	}
	// 4 kick + 2 snare = 6 notes.
	if len(p.Notes) != 6 {
		t.Fatalf("notes = %d, want 6\n%s", len(p.Notes), b)
	}
	// First note: position 0 (kick instrument 0).
	if p.Notes[0].Position != 0 || p.Notes[0].Instrument != 0 {
		t.Errorf("note0 = pos %d inst %d, want pos 0 inst 0", p.Notes[0].Position, p.Notes[0].Instrument)
	}
	// Step 4 -> tick 48. Both kick(0) and snare(1) hit there; deterministic order is
	// instrument-ascending, so the kick(0) at tick 48 precedes snare(1) at tick 48.
	var found48Kick, found48Snare bool
	for _, n := range p.Notes {
		if n.Position == 48 && n.Instrument == 0 {
			found48Kick = true
		}
		if n.Position == 48 && n.Instrument == 1 {
			found48Snare = true
		}
		if n.Length != -1 {
			t.Errorf("note length = %d, want -1 (open)", n.Length)
		}
		if n.Key != DefaultKey {
			t.Errorf("note key = %q, want %q", n.Key, DefaultKey)
		}
		if n.Velocity != 0.8 {
			t.Errorf("note velocity = %v, want 0.8", n.Velocity)
		}
	}
	if !found48Kick || !found48Snare {
		t.Errorf("expected both kick and snare at tick 48 (step 4); kick=%v snare=%v", found48Kick, found48Snare)
	}
}

func TestMarshalSong_Sequence(t *testing.T) {
	// Default sequence: one group per pattern, in order.
	b, _ := MarshalSong(sampleSong())
	var got reparseSong
	_ = xml.Unmarshal(b, &got)
	if len(got.Groups) != 1 || got.Groups[0].PatternID != "Pattern 1" {
		t.Errorf("sequence groups = %+v, want one group of Pattern 1", got.Groups)
	}

	// Explicit sequence: repeat the pattern 3x.
	s := sampleSong()
	s.Sequence = []string{"Pattern 1", "Pattern 1", "Pattern 1"}
	b2, _ := MarshalSong(s)
	var got2 reparseSong
	_ = xml.Unmarshal(b2, &got2)
	if len(got2.Groups) != 3 {
		t.Errorf("explicit sequence groups = %d, want 3", len(got2.Groups))
	}
}

func TestMarshalSong_Deterministic(t *testing.T) {
	s := sampleSong()
	a, _ := MarshalSong(s)
	b, _ := MarshalSong(s)
	if string(a) != string(b) {
		t.Error("MarshalSong is not deterministic (two calls differ)")
	}
}

func TestMarshalSong_Degrade(t *testing.T) {
	// Empty/zero song must not crash and must clamp to sane defaults.
	b, err := MarshalSong(Song{})
	if err != nil {
		t.Fatalf("empty song errored: %v", err)
	}
	var got reparseSong
	if err := xml.Unmarshal(b, &got); err != nil {
		t.Fatalf("empty song produced invalid XML: %v", err)
	}
	if got.BPM != 120 {
		t.Errorf("empty song bpm = %v, want default 120", got.BPM)
	}
	if got.Name == "" {
		t.Error("empty song name should fall back to a default")
	}
}

func TestStepPattern_OutOfRangeIgnored(t *testing.T) {
	p := StepPattern("P", map[int][]int{0: {-1, 0, 15, 16, 99}}, 0.7)
	// Only steps 0 and 15 are in-grid.
	if len(p.Notes) != 2 {
		t.Fatalf("notes = %d, want 2 (only steps 0 and 15 valid)", len(p.Notes))
	}
	if p.Notes[0].Position != 0 || p.Notes[1].Position != TicksForStep(15) {
		t.Errorf("positions = %d,%d", p.Notes[0].Position, p.Notes[1].Position)
	}
}

func TestTicksForStep(t *testing.T) {
	cases := map[int]int{0: 0, 1: 12, 4: 48, 8: 96, 15: 180}
	for step, want := range cases {
		if got := TicksForStep(step); got != want {
			t.Errorf("TicksForStep(%d) = %d, want %d", step, got, want)
		}
	}
	if TicksPerBar != 192 {
		t.Errorf("TicksPerBar = %d, want 192", TicksPerBar)
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
