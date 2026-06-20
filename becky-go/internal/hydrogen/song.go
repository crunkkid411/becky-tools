package hydrogen

import (
	"encoding/xml"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Timing constants. Hydrogen's internal resolution is 48 ticks per quarter note.
// A `<pattern>` declares <size> in ticks and <denominator>; a 4/4 one-bar pattern is
// 192 ticks with denominator 4. Notes carry <position> in ticks.
const (
	// TicksPerQuarter is Hydrogen's tick resolution per quarter note (H2_TICKS_PER_QUARTER).
	TicksPerQuarter = 48
	// TicksPerBar is one 4/4 bar (4 * TicksPerQuarter).
	TicksPerBar = TicksPerQuarter * 4
	// TicksPerStep16 is the tick spacing of a 16th-note step in a one-bar 16-step grid.
	TicksPerStep16 = TicksPerBar / 16 // 12
	// StepsPerBar is the step count of the standard one-bar grid.
	StepsPerBar = 16
)

// FormatVersion is the Hydrogen song/format version becky writes. Pinned >= 1.2.4 per
// the becky contract; 1.2.4 is the format that the 1.2.6 runtime reads and writes.
const FormatVersion = "1.2.4"

// DefaultKey is the per-note key Hydrogen writes for a plain drum hit.
const DefaultKey = "C0"

// ---------------------------------------------------------------------------
// XML model — element names/order mirror what Hydrogen 1.2.x writes.
// ---------------------------------------------------------------------------

// songXML is the root <song> document.
type songXML struct {
	XMLName               xml.Name        `xml:"song"`
	Version               string          `xml:"version"`
	BPM                   float64         `xml:"bpm"`
	Volume                float64         `xml:"volume"`
	MetronomeVolume       float64         `xml:"metronomeVolume"`
	Name                  string          `xml:"name"`
	Author                string          `xml:"author"`
	Notes                 string          `xml:"notes"`
	License               string          `xml:"license"`
	LoopEnabled           bool            `xml:"loopEnabled"`
	PatternModeMode       bool            `xml:"patternModeMode"`
	PlaybackTrackFilename string          `xml:"playbackTrackFilename"`
	PlaybackTrackEnabled  bool            `xml:"playbackTrackEnabled"`
	PlaybackTrackVolume   float64         `xml:"playbackTrackVolume"`
	ActionMode            int             `xml:"action_mode"`
	Mode                  string          `xml:"mode"`
	PanLawType            string          `xml:"pan_law_type"`
	PanLawKNorm           float64         `xml:"pan_law_k_norm"`
	HumanizeTime          float64         `xml:"humanize_time"`
	HumanizeVelocity      float64         `xml:"humanize_velocity"`
	SwingFactor           float64         `xml:"swing_factor"`
	ComponentList         componentList   `xml:"componentList"`
	InstrumentList        instrumentList  `xml:"instrumentList"`
	PatternList           patternListXML  `xml:"patternList"`
	PatternSequence       patternSequence `xml:"patternSequence"`
	Ladspa                ladspa          `xml:"ladspa"`
}

type componentList struct {
	Components []drumkitComponent `xml:"drumkitComponent"`
}

type drumkitComponent struct {
	ID     int     `xml:"id"`
	Name   string  `xml:"name"`
	Volume float64 `xml:"volume"`
}

type instrumentList struct {
	Instruments []songInstrument `xml:"instrument"`
}

// songInstrument is an <instrument> as it appears inside a <song> (it carries a
// <drumkit> reference and the full ADSR/pan/gain block plus its sample layers).
type songInstrument struct {
	ID                  int                 `xml:"id"`
	Name                string              `xml:"name"`
	Drumkit             string              `xml:"drumkit"`
	DrumkitLookup       int                 `xml:"drumkitLookup"`
	Volume              float64             `xml:"volume"`
	IsMuted             bool                `xml:"isMuted"`
	IsSoloed            bool                `xml:"isSoloed"`
	Pan                 float64             `xml:"pan"`
	Gain                float64             `xml:"gain"`
	ApplyVelocity       bool                `xml:"applyVelocity"`
	FilterActive        bool                `xml:"filterActive"`
	FilterCutoff        float64             `xml:"filterCutoff"`
	FilterResonance     float64             `xml:"filterResonance"`
	FX1Level            float64             `xml:"FX1Level"`
	FX2Level            float64             `xml:"FX2Level"`
	FX3Level            float64             `xml:"FX3Level"`
	FX4Level            float64             `xml:"FX4Level"`
	Attack              float64             `xml:"Attack"`
	Decay               float64             `xml:"Decay"`
	Sustain             float64             `xml:"Sustain"`
	Release             float64             `xml:"Release"`
	PitchOffset         float64             `xml:"pitchOffset"`
	RandomPitchFactor   float64             `xml:"randomPitchFactor"`
	MuteGroup           int                 `xml:"muteGroup"`
	IsStopNote          bool                `xml:"isStopNote"`
	SampleSelectionAlgo string              `xml:"sampleSelectionAlgo"`
	MidiOutChannel      int                 `xml:"midiOutChannel"`
	MidiOutNote         int                 `xml:"midiOutNote"`
	IsHihat             int                 `xml:"isHihat"`
	LowerCC             int                 `xml:"lower_cc"`
	HigherCC            int                 `xml:"higher_cc"`
	Component           instrumentComponent `xml:"instrumentComponent"`
}

type instrumentComponent struct {
	ComponentID int     `xml:"component_id"`
	Gain        float64 `xml:"gain"`
	Layers      []layer `xml:"layer"`
}

// layer is one sample inside an instrument component. The song form carries the
// extended playback fields (smode/startframe/...); the drumkit form (drumkit.go)
// uses the minimal form. We include the extended fields so a written song matches
// what Hydrogen saves.
type layer struct {
	Filename      string  `xml:"filename"`
	IsModified    bool    `xml:"ismodified"`
	SMode         string  `xml:"smode"`
	StartFrame    int64   `xml:"startframe"`
	LoopFrame     int64   `xml:"loopframe"`
	Loops         int     `xml:"loops"`
	EndFrame      int64   `xml:"endframe"`
	UseRubber     int     `xml:"userubber"`
	RubberDivider float64 `xml:"rubberdivider"`
	RubberCSet    int     `xml:"rubberCsettings"`
	RubberPitch   float64 `xml:"rubberPitch"`
	Min           float64 `xml:"min"`
	Max           float64 `xml:"max"`
	Gain          float64 `xml:"gain"`
	Pitch         float64 `xml:"pitch"`
}

type patternListXML struct {
	Patterns []patternXML `xml:"pattern"`
}

type patternXML struct {
	Name        string   `xml:"name"`
	Category    string   `xml:"category"`
	Size        int      `xml:"size"`
	Denominator int      `xml:"denominator"`
	Info        string   `xml:"info"`
	NoteList    noteList `xml:"noteList"`
}

type noteList struct {
	Notes []noteXML `xml:"note"`
}

type noteXML struct {
	Position    int     `xml:"position"`
	LeadLag     float64 `xml:"leadlag"`
	Velocity    float64 `xml:"velocity"`
	Pan         float64 `xml:"pan"`
	Pitch       float64 `xml:"pitch"`
	Probability float64 `xml:"probability"`
	Key         string  `xml:"key"`
	Length      int     `xml:"length"`
	Instrument  int     `xml:"instrument"`
	NoteOff     bool    `xml:"note_off"`
}

type patternSequence struct {
	Groups []seqGroup `xml:"group"`
}

type seqGroup struct {
	PatternIDs []string `xml:"patternID"`
}

type ladspa struct {
	FX []ladspaFX `xml:"fx"`
}

type ladspaFX struct {
	Name     string  `xml:"name"`
	Filename string  `xml:"filename"`
	Enabled  bool    `xml:"enabled"`
	Volume   float64 `xml:"volume"`
}

// ---------------------------------------------------------------------------
// Public, caller-facing model (the friendly API)
// ---------------------------------------------------------------------------

// Note is one drum hit inside a pattern. Position is in Hydrogen ticks (use
// TicksForStep for a step-grid position); Velocity is 0..1.
type Note struct {
	Position   int     // ticks from the start of the pattern
	Instrument int     // instrument id within the kit
	Velocity   float64 // 0..1
	Length     int     // -1 = play to end (the drum default)
}

// Pattern is a named bar (or bars) of notes. Size is the pattern length in ticks
// (default TicksPerBar). Notes need not be sorted; the writer sorts deterministically.
type Pattern struct {
	Name  string
	Size  int // ticks; 0 -> TicksPerBar
	Notes []Note
}

// Song is the friendly description of a Hydrogen song to write.
type Song struct {
	Name     string
	Author   string
	Notes    string
	BPM      float64
	Kit      Kit       // the instrument list this song plays
	Patterns []Pattern // the patterns
	// Sequence is the arrangement: each entry is a pattern name played for one
	// "group" slot. If empty, every pattern is played once in order.
	Sequence []string
	// LoopEnabled controls whether Hydrogen loops the sequence on playback.
	LoopEnabled bool
}

// ---------------------------------------------------------------------------
// Step-grid helpers
// ---------------------------------------------------------------------------

// TicksForStep maps a 0-based 16th-note step in a one-bar grid to its tick position.
func TicksForStep(step int) int { return step * TicksPerStep16 }

// StepPattern builds a Pattern from a per-instrument step map: hits[instrumentID] is a
// slice of 0-based step indices (0..15) that instrument hits on. velocity is applied to
// every hit (clamped 0..1; a non-positive value falls back to 0.8, Hydrogen's default).
// The result is deterministic (instruments ascending, then steps ascending).
func StepPattern(name string, hits map[int][]int, velocity float64) Pattern {
	if velocity <= 0 {
		velocity = 0.8
	}
	if velocity > 1 {
		velocity = 1
	}
	insts := make([]int, 0, len(hits))
	for id := range hits {
		insts = append(insts, id)
	}
	sort.Ints(insts)

	var notes []Note
	for _, id := range insts {
		steps := append([]int(nil), hits[id]...)
		sort.Ints(steps)
		for _, st := range steps {
			if st < 0 || st >= StepsPerBar {
				continue // ignore out-of-grid steps rather than crash
			}
			notes = append(notes, Note{
				Position:   TicksForStep(st),
				Instrument: id,
				Velocity:   velocity,
				Length:     -1,
			})
		}
	}
	return Pattern{Name: name, Size: TicksPerBar, Notes: notes}
}

// ---------------------------------------------------------------------------
// Marshalling
// ---------------------------------------------------------------------------

// MarshalSong renders the Song to Hydrogen-compatible .h2song XML bytes. It is
// deterministic: the same Song yields byte-identical output. Out-of-range values are
// clamped (degrade-never-crash). The kit's instruments define the instrument list.
func MarshalSong(s Song) ([]byte, error) {
	bpm := s.BPM
	if bpm <= 0 {
		bpm = 120
	}

	doc := songXML{
		Version:               FormatVersion,
		BPM:                   bpm,
		Volume:                0.8,
		MetronomeVolume:       0.5,
		Name:                  defaultStr(s.Name, "becky-groove"),
		Author:                defaultStr(s.Author, "becky"),
		Notes:                 s.Notes,
		License:               "Unknown license",
		LoopEnabled:           s.LoopEnabled,
		PatternModeMode:       false, // song mode
		PlaybackTrackFilename: "",
		PlaybackTrackEnabled:  false,
		PlaybackTrackVolume:   0,
		ActionMode:            0,
		Mode:                  "song",
		PanLawType:            "RATIO_STRAIGHT_POLYGONAL",
		PanLawKNorm:           1.33333,
		HumanizeTime:          0,
		HumanizeVelocity:      0,
		SwingFactor:           0,
		ComponentList: componentList{
			Components: []drumkitComponent{{ID: 0, Name: "Main", Volume: 1}},
		},
		InstrumentList:  buildSongInstruments(s.Kit),
		PatternList:     buildPatternList(s.Patterns),
		PatternSequence: buildSequence(s.Patterns, s.Sequence),
		Ladspa:          defaultLadspa(),
	}

	var b strings.Builder
	b.WriteString(xml.Header) // <?xml version="1.0" encoding="UTF-8"?>\n
	enc := xml.NewEncoder(&b)
	enc.Indent("", " ")
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("hydrogen: marshal song: %w", err)
	}
	b.WriteString("\n")
	return []byte(b.String()), nil
}

// WriteSong marshals and writes the Song to path (0644). The caller chooses the path;
// for Hydrogen to find the kit, write the song into the kit's directory OR reference an
// installed kit by name (see Kit.Name).
func WriteSong(path string, s Song) error {
	b, err := MarshalSong(s)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("hydrogen: write song %q: %w", path, err)
	}
	return nil
}

// buildSongInstruments turns the Kit into the song's <instrumentList>.
func buildSongInstruments(k Kit) instrumentList {
	out := instrumentList{}
	for _, inst := range k.Instruments {
		si := songInstrument{
			ID:                  inst.ID,
			Name:                inst.Name,
			Drumkit:             k.Name,
			DrumkitLookup:       2, // "user" lookup; harmless for a self-contained kit dir
			Volume:              clampPos(inst.Volume, 1),
			IsMuted:             false,
			IsSoloed:            false,
			Pan:                 clampPan(inst.Pan),
			Gain:                clampPos(inst.Gain, 1),
			ApplyVelocity:       true,
			FilterActive:        false,
			FilterCutoff:        1,
			FilterResonance:     0,
			Attack:              maxF(inst.Attack, 0),
			Decay:               maxF(inst.Decay, 0),
			Sustain:             clamp01OrDefault(inst.Sustain, 1),
			Release:             releaseOrDefault(inst.Release),
			PitchOffset:         0,
			RandomPitchFactor:   0,
			MuteGroup:           inst.MuteGroup,
			IsStopNote:          false,
			SampleSelectionAlgo: "VELOCITY",
			MidiOutChannel:      -1,
			MidiOutNote:         inst.MidiNote,
			IsHihat:             -1,
			LowerCC:             0,
			HigherCC:            127,
			Component: instrumentComponent{
				ComponentID: 0,
				Gain:        1,
				Layers:      songLayers(inst),
			},
		}
		out.Instruments = append(out.Instruments, si)
	}
	return out
}

// songLayers builds the extended <layer> list for a song instrument.
func songLayers(inst Instrument) []layer {
	if len(inst.Layers) == 0 {
		return nil
	}
	out := make([]layer, 0, len(inst.Layers))
	for _, l := range inst.Layers {
		out = append(out, layer{
			Filename:      l.Filename,
			IsModified:    false,
			SMode:         "forward",
			StartFrame:    0,
			LoopFrame:     0,
			Loops:         0,
			EndFrame:      0,
			UseRubber:     0,
			RubberDivider: 1,
			RubberCSet:    4,
			RubberPitch:   1,
			Min:           clamp01(l.Min),
			Max:           clamp01OrDefault(l.Max, 1),
			Gain:          clampPos(l.Gain, 1),
			Pitch:         l.Pitch,
		})
	}
	return out
}

func buildPatternList(ps []Pattern) patternListXML {
	out := patternListXML{}
	for _, p := range ps {
		size := p.Size
		if size <= 0 {
			size = TicksPerBar
		}
		px := patternXML{
			Name:        p.Name,
			Category:    "",
			Size:        size,
			Denominator: 4,
			Info:        "",
		}
		// Sort notes deterministically: position, then instrument.
		notes := append([]Note(nil), p.Notes...)
		sort.SliceStable(notes, func(i, j int) bool {
			if notes[i].Position != notes[j].Position {
				return notes[i].Position < notes[j].Position
			}
			return notes[i].Instrument < notes[j].Instrument
		})
		for _, n := range notes {
			length := n.Length
			if length == 0 {
				length = -1
			}
			px.NoteList.Notes = append(px.NoteList.Notes, noteXML{
				Position:    maxI(n.Position, 0),
				LeadLag:     0,
				Velocity:    clamp01OrDefault(n.Velocity, 0.8),
				Pan:         0,
				Pitch:       0,
				Probability: 1,
				Key:         DefaultKey,
				Length:      length,
				Instrument:  n.Instrument,
				NoteOff:     false,
			})
		}
		out.Patterns = append(out.Patterns, px)
	}
	return out
}

func buildSequence(ps []Pattern, seq []string) patternSequence {
	out := patternSequence{}
	names := seq
	if len(names) == 0 {
		for _, p := range ps {
			names = append(names, p.Name)
		}
	}
	for _, n := range names {
		out.Groups = append(out.Groups, seqGroup{PatternIDs: []string{n}})
	}
	return out
}

func defaultLadspa() ladspa {
	fx := ladspaFX{Name: "no plugin", Filename: "-", Enabled: false, Volume: 0}
	return ladspa{FX: []ladspaFX{fx, fx, fx, fx}}
}

// ---------------------------------------------------------------------------
// small clamps (degrade-never-crash)
// ---------------------------------------------------------------------------

func defaultStr(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// clamp01OrDefault clamps to 0..1 but treats a non-positive value as "unset" and
// returns def (used for max/velocity/sustain where 0 means "the caller didn't set it").
func clamp01OrDefault(v, def float64) float64 {
	if v <= 0 {
		return def
	}
	if v > 1 {
		return 1
	}
	return v
}

func clampPos(v, def float64) float64 {
	if v <= 0 {
		return def
	}
	return v
}

func clampPan(v float64) float64 {
	if v < -1 {
		return -1
	}
	if v > 1 {
		return 1
	}
	return v
}

func releaseOrDefault(v float64) float64 {
	if v <= 0 {
		return 1000 // Hydrogen's default release
	}
	return v
}

func maxF(v, lo float64) float64 {
	if v < lo {
		return lo
	}
	return v
}

func maxI(v, lo int) int {
	if v < lo {
		return lo
	}
	return v
}
