package reaper

import (
	"strings"
	"testing"

	"becky-go/internal/dawmodel"
)

func TestWriteRPP_Deterministic(t *testing.T) {
	p := DemoProject("X:\\out.wav")
	a := WriteRPP(p)
	b := WriteRPP(p)
	if a != b {
		t.Fatal("WriteRPP not deterministic: two calls differ")
	}
}

func TestWriteRPP_StructureAndHeader(t *testing.T) {
	p := DemoProject("X:\\AI-2\\out.wav")
	out := WriteRPP(p)
	for _, want := range []string{
		"<REAPER_PROJECT 0.1",
		"TEMPO 132 4 4 0",
		"SAMPLERATE 48000 0 0",
		"RENDER_FILE X:\\AI-2\\out.wav",
		"<RENDER_CFG\n    ZXZhdxgBAA==\n  >",
		"NAME \"Synth Bass\"",
		"VSTi: ReaSynth (Cockos)", // built-in instrument embedded -> audible MIDI
		"<SOURCE MIDI",
		"HASDATA 1 960 QN",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rpp output missing %q", want)
		}
	}
	if !strings.HasSuffix(out, ">\n") {
		t.Error("rpp must end with a closing >")
	}
}

func TestFolderISBUS(t *testing.T) {
	parent := Track{Name: "BASS_bus", FolderStart: true}
	if got := isbus(parent); got != "1 1" {
		t.Errorf("folder parent ISBUS = %q, want \"1 1\"", got)
	}
	last := Track{Name: "Sub", FolderCloses: 1}
	if got := isbus(last); got != "2 -1" {
		t.Errorf("folder-closing ISBUS = %q, want \"2 -1\"", got)
	}
	mid := Track{Name: "Synth Bass"}
	if got := isbus(mid); got != "0 0" {
		t.Errorf("normal ISBUS = %q, want \"0 0\"", got)
	}
}

func TestMIDIEvents_DeltaAndHex(t *testing.T) {
	// A single 1-beat note at 132 BPM, 480 PPQ. tick 0..480 (arr) -> 0..960 (960 PPQ).
	p := Project{BPM: 132, Num: 4, Den: 4, SampleRate: 48000}
	secPerTick := 60.0 / (132.0 * 480.0)
	it := Item{Length: 480 * secPerTick, Notes: []MIDINote{{Start: 0, Dur: 480, Pitch: 57, Vel: 100, Ch: 0}}}
	evs := midiEvents(it, p)
	if len(evs) < 3 {
		t.Fatalf("want >=3 events (on/off/all-notes-off), got %d", len(evs))
	}
	on := evs[0]
	if on.status != 0x90 || on.d1 != 57 || on.d2 != 100 || on.delta != 0 {
		t.Errorf("note-on = %+v, want delta0 status0x90 pitch57 vel100", on)
	}
	off := evs[1]
	if off.status != 0x80 || off.d1 != 57 || off.delta != 960 {
		t.Errorf("note-off = %+v, want delta960 status0x80 pitch57", off)
	}
	last := evs[len(evs)-1]
	if last.status != 0xb0 || last.d1 != 0x7b {
		t.Errorf("final event = %+v, want all-notes-off CC 0x7b", last)
	}
	// Hex formatting check in the rendered text.
	out := WriteRPP(Project{BPM: 132, Num: 4, Den: 4, SampleRate: 48000,
		Tracks: []Track{{Name: "t", ReaSynth: true, Items: []Item{it}}}})
	if !strings.Contains(out, "E 0 90 39 64") { // 0x39=57, 0x64=100
		t.Errorf("expected MIDI E line 'E 0 90 39 64' in output")
	}
}

func TestScaleTo960(t *testing.T) {
	cases := map[int]int{0: 0, 480: 960, 240: 480, 120: 240}
	for in, want := range cases {
		if got := scaleTo960(in); got != want {
			t.Errorf("scaleTo960(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestFromArrangement_BusFolders(t *testing.T) {
	a := dawmodel.New()
	a.BPM = 132
	a = a.AddTrack("Kick", dawmodel.KindAudio)
	a = a.AddTrack("Snare", dawmodel.KindAudio)
	// Route both into a drums bus.
	a, _ = a.RouteTo("Kick", "bus.drums")
	a, _ = a.RouteTo("Snare", "bus.drums")

	p := FromArrangement(a, "")
	// Expect a DRUMS_bus folder parent followed by Kick, Snare (Snare closes).
	if len(p.Tracks) != 3 {
		t.Fatalf("want 3 tracks (bus + 2 members), got %d", len(p.Tracks))
	}
	if !p.Tracks[0].FolderStart || p.Tracks[0].Name != "DRUMS_bus" {
		t.Errorf("track0 = %+v, want DRUMS_bus folder parent", p.Tracks[0])
	}
	if p.Tracks[2].FolderCloses != 1 {
		t.Errorf("last member should close the folder, got FolderCloses=%d", p.Tracks[2].FolderCloses)
	}
	if p.BPM != 132 {
		t.Errorf("BPM = %v, want 132", p.BPM)
	}
}

func TestDemoProject_HasAudibleNotes(t *testing.T) {
	p := DemoProject("")
	var notes int
	var hasSynth bool
	for _, tr := range p.Tracks {
		if tr.ReaSynth {
			hasSynth = true
		}
		for _, it := range tr.Items {
			notes += len(it.Notes)
		}
	}
	if !hasSynth {
		t.Error("demo must host ReaSynth so the render is audible")
	}
	if notes < 4 {
		t.Errorf("demo should have a 4-note riff, got %d notes", notes)
	}
}

func TestQuoteIfNeeded(t *testing.T) {
	cases := map[string]string{
		"":           "\"\"",
		"Kick":       "Kick",
		"Synth Bass": "\"Synth Bass\"",
	}
	for in, want := range cases {
		if got := quoteIfNeeded(in); got != want {
			t.Errorf("quoteIfNeeded(%q) = %q, want %q", in, got, want)
		}
	}
}
