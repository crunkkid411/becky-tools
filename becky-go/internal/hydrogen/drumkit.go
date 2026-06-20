package hydrogen

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"becky-go/internal/pathx"
	"becky-go/internal/sampledecode"
)

// ---------------------------------------------------------------------------
// Public Kit model
// ---------------------------------------------------------------------------

// SampleLayer is one velocity-mapped sample inside an instrument. Filename is the
// sample file Hydrogen loads (a bare name when the sample lives in the kit dir, or an
// absolute path otherwise — Hydrogen accepts both). Min/Max are the 0..1 velocity band.
type SampleLayer struct {
	Filename string
	Min      float64
	Max      float64
	Gain     float64
	Pitch    float64
}

// Instrument is one drum voice (a row in the kit). ID is its index; MidiNote is the
// note that triggers it over OSC/MIDI (e.g. 36 kick, 38 snare, 42 closed hat). Layers
// hold its sample(s); a single-sample instrument has one layer spanning 0..1.
type Instrument struct {
	ID        int
	Name      string
	MidiNote  int
	Volume    float64
	Gain      float64
	Pan       float64 // -1..1
	Attack    float64
	Decay     float64
	Sustain   float64 // 0..1
	Release   float64
	MuteGroup int // -1 = none
	Layers    []SampleLayer
}

// Kit is a Hydrogen drumkit: a name plus an ordered instrument list.
type Kit struct {
	Name        string
	Author      string
	Info        string
	License     string
	Instruments []Instrument
}

// NewInstrument builds an Instrument with responsive defaults and a single full-range
// sample layer pointing at filename. Use SampleInfo + an absolute path for samples that
// are not copied into the kit directory.
func NewInstrument(id int, name string, midiNote int, filename string) Instrument {
	return Instrument{
		ID:        id,
		Name:      name,
		MidiNote:  midiNote,
		Volume:    1,
		Gain:      1,
		Pan:       0,
		Attack:    0,
		Decay:     0,
		Sustain:   1,
		Release:   1000,
		MuteGroup: -1,
		Layers: []SampleLayer{{
			Filename: filename,
			Min:      0,
			Max:      1,
			Gain:     1,
			Pitch:    0,
		}},
	}
}

// ---------------------------------------------------------------------------
// drumkit.xml model — mirrors what Hydrogen writes for data/drumkits/*/drumkit.xml.
// ---------------------------------------------------------------------------

const drumkitNamespace = "http://www.hydrogen-music.org/drumkit"

type drumkitInfoXML struct {
	XMLName        xml.Name        `xml:"drumkit_info"`
	XSI            string          `xml:"xmlns:xsi,attr"`
	Xmlns          string          `xml:"xmlns,attr"`
	FormatVersion  string          `xml:"formatVersion,omitempty"`
	Name           string          `xml:"name"`
	Author         string          `xml:"author"`
	Info           string          `xml:"info"`
	License        string          `xml:"license"`
	Image          string          `xml:"image"`
	ImageLicense   string          `xml:"imageLicense"`
	ComponentList  componentList   `xml:"componentList"`
	InstrumentList kitInstrumentLs `xml:"instrumentList"`
}

type kitInstrumentLs struct {
	Instruments []kitInstrument `xml:"instrument"`
}

// kitInstrument is an <instrument> as it appears in a drumkit.xml (no <drumkit> ref;
// the layer form is the minimal one without smode/startframe).
type kitInstrument struct {
	ID                  int                    `xml:"id"`
	Name                string                 `xml:"name"`
	Volume              float64                `xml:"volume"`
	IsMuted             bool                   `xml:"isMuted"`
	IsSoloed            bool                   `xml:"isSoloed"`
	Pan                 float64                `xml:"pan"`
	PitchOffset         float64                `xml:"pitchOffset"`
	RandomPitchFactor   float64                `xml:"randomPitchFactor"`
	Gain                float64                `xml:"gain"`
	ApplyVelocity       bool                   `xml:"applyVelocity"`
	FilterActive        bool                   `xml:"filterActive"`
	FilterCutoff        float64                `xml:"filterCutoff"`
	FilterResonance     float64                `xml:"filterResonance"`
	Attack              float64                `xml:"Attack"`
	Decay               float64                `xml:"Decay"`
	Sustain             float64                `xml:"Sustain"`
	Release             float64                `xml:"Release"`
	MuteGroup           int                    `xml:"muteGroup"`
	MidiOutChannel      int                    `xml:"midiOutChannel"`
	MidiOutNote         int                    `xml:"midiOutNote"`
	IsStopNote          bool                   `xml:"isStopNote"`
	SampleSelectionAlgo string                 `xml:"sampleSelectionAlgo"`
	IsHihat             int                    `xml:"isHihat"`
	LowerCC             int                    `xml:"lower_cc"`
	HigherCC            int                    `xml:"higher_cc"`
	FX1Level            float64                `xml:"FX1Level"`
	FX2Level            float64                `xml:"FX2Level"`
	FX3Level            float64                `xml:"FX3Level"`
	FX4Level            float64                `xml:"FX4Level"`
	Component           kitInstrumentComponent `xml:"instrumentComponent"`
}

type kitInstrumentComponent struct {
	ComponentID int        `xml:"component_id"`
	Gain        float64    `xml:"gain"`
	Layers      []kitLayer `xml:"layer"`
}

// kitLayer is the minimal layer form drumkit.xml uses.
type kitLayer struct {
	Filename string  `xml:"filename"`
	Min      float64 `xml:"min"`
	Max      float64 `xml:"max"`
	Gain     float64 `xml:"gain"`
	Pitch    float64 `xml:"pitch"`
}

// MarshalDrumkit renders the Kit to drumkit.xml bytes (deterministic; degrade-never-crash).
func MarshalDrumkit(k Kit) ([]byte, error) {
	doc := drumkitInfoXML{
		XSI:           "http://www.w3.org/2001/XMLSchema-instance",
		Xmlns:         drumkitNamespace,
		FormatVersion: FormatVersion,
		Name:          defaultStr(k.Name, "becky-kit"),
		Author:        defaultStr(k.Author, "becky"),
		Info:          k.Info,
		License:       defaultStr(k.License, "Unknown license"),
		Image:         "",
		ImageLicense:  "undefined license",
		ComponentList: componentList{
			Components: []drumkitComponent{{ID: 0, Name: "Main", Volume: 1}},
		},
	}
	for _, inst := range k.Instruments {
		ki := kitInstrument{
			ID:                  inst.ID,
			Name:                inst.Name,
			Volume:              clampPos(inst.Volume, 1),
			IsMuted:             false,
			IsSoloed:            false,
			Pan:                 clampPan(inst.Pan),
			PitchOffset:         0,
			RandomPitchFactor:   0,
			Gain:                clampPos(inst.Gain, 1),
			ApplyVelocity:       true,
			FilterActive:        false,
			FilterCutoff:        1,
			FilterResonance:     0,
			Attack:              maxF(inst.Attack, 0),
			Decay:               maxF(inst.Decay, 0),
			Sustain:             clamp01OrDefault(inst.Sustain, 1),
			Release:             releaseOrDefault(inst.Release),
			MuteGroup:           inst.MuteGroup,
			MidiOutChannel:      -1,
			MidiOutNote:         inst.MidiNote,
			IsStopNote:          false,
			SampleSelectionAlgo: "VELOCITY",
			IsHihat:             -1,
			LowerCC:             0,
			HigherCC:            127,
			Component: kitInstrumentComponent{
				ComponentID: 0,
				Gain:        1,
				Layers:      kitLayers(inst),
			},
		}
		doc.InstrumentList.Instruments = append(doc.InstrumentList.Instruments, ki)
	}

	var b strings.Builder
	b.WriteString(xml.Header)
	enc := xml.NewEncoder(&b)
	enc.Indent("", " ")
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("hydrogen: marshal drumkit: %w", err)
	}
	b.WriteString("\n")
	return []byte(b.String()), nil
}

func kitLayers(inst Instrument) []kitLayer {
	out := make([]kitLayer, 0, len(inst.Layers))
	for _, l := range inst.Layers {
		out = append(out, kitLayer{
			Filename: l.Filename,
			Min:      clamp01(l.Min),
			Max:      clamp01OrDefault(l.Max, 1),
			Gain:     clampPos(l.Gain, 1),
			Pitch:    l.Pitch,
		})
	}
	return out
}

// WriteDrumkit writes drumkit.xml into dir (creating dir if needed). Hydrogen expects
// the file to be named exactly "drumkit.xml" inside the kit folder, alongside (or
// referencing absolute paths to) the sample files. Returns the written path.
func WriteDrumkit(dir string, k Kit) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("hydrogen: make kit dir %q: %w", dir, err)
	}
	b, err := MarshalDrumkit(k)
	if err != nil {
		return "", err
	}
	out := filepath.Join(dir, "drumkit.xml")
	if err := os.WriteFile(out, b, 0o644); err != nil {
		return "", fmt.Errorf("hydrogen: write drumkit %q: %w", out, err)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Sample info (uses internal/sampledecode)
// ---------------------------------------------------------------------------

// SampleInfo is the header-level facts about a sample, read without decoding the
// audio body (fast for library scanning). It is filled by ProbeSample for WAV files;
// for other formats only Base is set and OK is false.
type SampleInfo struct {
	Path       string
	Base       string // separator-agnostic base name (works on Windows `\` paths)
	SampleRate int
	Channels   int
	Frames     int
	Bits       int
	Format     string  // "pcm" | "float" | "" (unknown)
	Seconds    float64 // 0 when unknown
	OK         bool    // true when the header was read successfully
}

// ProbeSample reads header facts for a sample file. For WAV it uses
// sampledecode.ProbeWAV (header-only). For non-WAV (flac/aiff/...) it returns a
// SampleInfo with just Path+Base set and OK=false — Hydrogen can still load the file;
// becky just doesn't know its duration. Never panics; an unreadable WAV returns OK=false.
func ProbeSample(path string) SampleInfo {
	info := SampleInfo{Path: path, Base: pathx.Base(path)}
	if !strings.EqualFold(filepath.Ext(path), ".wav") {
		return info
	}
	sr, ch, frames, bits, format, err := sampledecode.ProbeWAV(path)
	if err != nil {
		return info // degrade: OK stays false
	}
	info.SampleRate = sr
	info.Channels = ch
	info.Frames = frames
	info.Bits = bits
	info.Format = format
	if sr > 0 {
		info.Seconds = float64(frames) / float64(sr)
	}
	info.OK = true
	return info
}
