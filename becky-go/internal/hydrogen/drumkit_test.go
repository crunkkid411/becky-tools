package hydrogen

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// reparseKit reads back an emitted drumkit.xml to validate the elements Hydrogen reads.
type reparseKit struct {
	XMLName     xml.Name `xml:"drumkit_info"`
	Name        string   `xml:"name"`
	License     string   `xml:"license"`
	Instruments []struct {
		ID          int     `xml:"id"`
		Name        string  `xml:"name"`
		MidiOutNote int     `xml:"midiOutNote"`
		Pan         float64 `xml:"pan"`
		Volume      float64 `xml:"volume"`
		Layers      []struct {
			Filename string  `xml:"filename"`
			Min      float64 `xml:"min"`
			Max      float64 `xml:"max"`
			Gain     float64 `xml:"gain"`
		} `xml:"instrumentComponent>layer"`
	} `xml:"instrumentList>instrument"`
}

func sampleKit() Kit {
	return Kit{
		Name:   "BeckyKit",
		Author: "becky",
		Instruments: []Instrument{
			NewInstrument(0, "Kick", MIDIKick, "C:\\samples\\kick.wav"),
			func() Instrument {
				i := NewInstrument(1, "Snare", MIDISnare, "C:\\samples\\snare.wav")
				i.Pan = -0.3
				i.Volume = 0.9
				return i
			}(),
			NewInstrument(2, "Hat", MIDIHatClsd, "C:\\samples\\hat.wav"),
		},
	}
}

func TestMarshalDrumkit_Valid(t *testing.T) {
	b, err := MarshalDrumkit(sampleKit())
	if err != nil {
		t.Fatalf("MarshalDrumkit: %v", err)
	}
	out := string(b)
	if !strings.HasPrefix(out, "<?xml") {
		t.Error("missing XML declaration")
	}
	if !strings.Contains(out, "hydrogen-music.org/drumkit") {
		t.Error("missing Hydrogen drumkit namespace")
	}

	var got reparseKit
	if err := xml.Unmarshal(b, &got); err != nil {
		t.Fatalf("emitted drumkit did not re-parse: %v\n%s", err, out)
	}
	if got.Name != "BeckyKit" {
		t.Errorf("name = %q", got.Name)
	}
	if len(got.Instruments) != 3 {
		t.Fatalf("instruments = %d, want 3", len(got.Instruments))
	}
	if got.Instruments[0].MidiOutNote != MIDIKick {
		t.Errorf("kick midiOutNote = %d, want %d", got.Instruments[0].MidiOutNote, MIDIKick)
	}
	if got.Instruments[1].Pan != -0.3 {
		t.Errorf("snare pan = %v, want -0.3", got.Instruments[1].Pan)
	}
	if got.Instruments[1].Volume != 0.9 {
		t.Errorf("snare volume = %v, want 0.9", got.Instruments[1].Volume)
	}
}

func TestMarshalDrumkit_LayerReferencesSample(t *testing.T) {
	b, _ := MarshalDrumkit(sampleKit())
	var got reparseKit
	if err := xml.Unmarshal(b, &got); err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	l := got.Instruments[0].Layers
	if len(l) != 1 {
		t.Fatalf("kick layers = %d, want 1", len(l))
	}
	if !strings.HasSuffix(l[0].Filename, "kick.wav") {
		t.Errorf("kick layer filename = %q, want a kick.wav ref", l[0].Filename)
	}
	if l[0].Min != 0 || l[0].Max != 1 {
		t.Errorf("kick layer band = [%v,%v], want [0,1]", l[0].Min, l[0].Max)
	}
}

func TestMarshalDrumkit_Deterministic(t *testing.T) {
	k := sampleKit()
	a, _ := MarshalDrumkit(k)
	b, _ := MarshalDrumkit(k)
	if string(a) != string(b) {
		t.Error("MarshalDrumkit is not deterministic")
	}
}

func TestWriteDrumkit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "MyKit")
	path, err := WriteDrumkit(dir, sampleKit())
	if err != nil {
		t.Fatalf("WriteDrumkit: %v", err)
	}
	if filepath.Base(path) != "drumkit.xml" {
		t.Errorf("written file = %q, want .../drumkit.xml", path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var got reparseKit
	if err := xml.Unmarshal(b, &got); err != nil {
		t.Fatalf("written drumkit.xml invalid: %v", err)
	}
	if len(got.Instruments) != 3 {
		t.Errorf("instruments = %d, want 3", len(got.Instruments))
	}
}

func TestMarshalDrumkit_Degrade(t *testing.T) {
	// Empty kit: must not crash; should still be valid XML with defaults.
	b, err := MarshalDrumkit(Kit{})
	if err != nil {
		t.Fatalf("empty kit errored: %v", err)
	}
	var got reparseKit
	if err := xml.Unmarshal(b, &got); err != nil {
		t.Fatalf("empty kit invalid XML: %v", err)
	}
	if got.Name == "" {
		t.Error("empty kit name should fall back to a default")
	}
}

func TestProbeSample_NonWAV(t *testing.T) {
	// A non-WAV path: Base set, OK false, no crash.
	info := ProbeSample("C:\\kits\\808\\kick.flac")
	if info.OK {
		t.Error("flac ProbeSample should report OK=false (no header reader)")
	}
	if info.Base != "kick.flac" {
		t.Errorf("Base = %q, want kick.flac (separator-agnostic)", info.Base)
	}
}

func TestProbeSample_RealWAV(t *testing.T) {
	// Write a tiny valid PCM WAV and probe it.
	dir := t.TempDir()
	p := filepath.Join(dir, "tone.wav")
	writeTinyWAV(t, p)
	info := ProbeSample(p)
	if !info.OK {
		t.Fatalf("ProbeSample(real wav) OK=false")
	}
	if info.SampleRate != 44100 || info.Channels != 1 || info.Bits != 16 {
		t.Errorf("probe = %dHz %dch %dbit, want 44100/1/16", info.SampleRate, info.Channels, info.Bits)
	}
	if info.Frames != 100 {
		t.Errorf("frames = %d, want 100", info.Frames)
	}
	if info.Format != "pcm" {
		t.Errorf("format = %q, want pcm", info.Format)
	}
}

// writeTinyWAV writes a minimal 16-bit mono 44.1kHz PCM WAV with 100 frames.
func writeTinyWAV(t *testing.T, path string) {
	t.Helper()
	const (
		sr     = 44100
		ch     = 1
		bits   = 16
		frames = 100
	)
	dataBytes := frames * ch * (bits / 8)
	var buf []byte
	put := func(b ...byte) { buf = append(buf, b...) }
	putU32 := func(v uint32) { put(byte(v), byte(v>>8), byte(v>>16), byte(v>>24)) }
	putU16 := func(v uint16) { put(byte(v), byte(v>>8)) }

	put('R', 'I', 'F', 'F')
	putU32(uint32(36 + dataBytes))
	put('W', 'A', 'V', 'E')
	put('f', 'm', 't', ' ')
	putU32(16)
	putU16(1) // PCM
	putU16(ch)
	putU32(sr)
	putU32(sr * ch * (bits / 8)) // byte rate
	putU16(ch * (bits / 8))      // block align
	putU16(bits)
	put('d', 'a', 't', 'a')
	putU32(uint32(dataBytes))
	for i := 0; i < dataBytes; i++ {
		put(0)
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatalf("write tiny wav: %v", err)
	}
}
