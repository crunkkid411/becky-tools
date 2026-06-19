package kitimport

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"becky-go/internal/pathx"
	"becky-go/internal/sampler"
)

// DecentSampler .dspreset structure (the subset we model). The format nests
// <DecentSampler><groups><group><sample .../></group></groups>. Sample mapping
// attributes follow the DecentSampler developer guide:
//
//	path            -> sample file (relative to the .dspreset dir)
//	rootNote        -> pitch keycenter
//	loNote/hiNote   -> key range
//	loVel/hiVel     -> velocity layer bounds
//	start/end       -> sample frame offsets
//	tuning          -> fine tune (semitones in DecentSampler; we store cents)
//	loopStart/loopEnd + loopEnabled -> loop region
//	seqMode/seqPosition/seqLength    -> round robin
type dsPreset struct {
	XMLName xml.Name  `xml:"DecentSampler"`
	Groups  []dsGroup `xml:"groups>group"`
	// Some presets omit the <groups> wrapper and put <group> at the root.
	RootGroups []dsGroup `xml:"group"`
}

type dsGroup struct {
	Tuning  string     `xml:"tuning,attr"`
	SeqMode string     `xml:"seqMode,attr"`
	Samples []dsSample `xml:"sample"`
}

type dsSample struct {
	Path        string `xml:"path,attr"`
	RootNote    string `xml:"rootNote,attr"`
	LoNote      string `xml:"loNote,attr"`
	HiNote      string `xml:"hiNote,attr"`
	LoVel       string `xml:"loVel,attr"`
	HiVel       string `xml:"hiVel,attr"`
	Start       string `xml:"start,attr"`
	End         string `xml:"end,attr"`
	Tuning      string `xml:"tuning,attr"`
	Pan         string `xml:"pan,attr"`
	Volume      string `xml:"volume,attr"`
	LoopStart   string `xml:"loopStart,attr"`
	LoopEnd     string `xml:"loopEnd,attr"`
	LoopEnabled string `xml:"loopEnabled,attr"`
	SeqMode     string `xml:"seqMode,attr"`
	SeqPosition string `xml:"seqPosition,attr"`
	SeqLength   string `xml:"seqLength,attr"`
}

// ParseDecentSamplerResult mirrors ParseSFZResult.
type ParseDecentSamplerResult struct {
	Sounds []sampler.Sound
	Notes  []string
}

// ParseDecentSampler reads a .dspreset file and groups its <sample> elements into
// Sounds by key, layered by velocity, with seq-ordered round-robin variants.
// Degrade-never-crash: a missing sample is flagged, not fatal; only an unreadable
// or malformed-XML file is an error.
func ParseDecentSampler(path string) (ParseDecentSamplerResult, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return ParseDecentSamplerResult{}, err
	}
	var p dsPreset
	if err := xml.Unmarshal(b, &p); err != nil {
		return ParseDecentSamplerResult{}, err
	}
	baseDir := filepath.Dir(path)

	groups := append([]dsGroup{}, p.Groups...)
	groups = append(groups, p.RootGroups...)

	var notes []string
	// key -> velKey -> *Layer, plus per-key sound metadata.
	type velKey struct{ lo, hi int }
	type keyAgg struct {
		layers     map[velKey]*sampler.Layer
		layerOrder []velKey
		root       int
		keyLo      int
		keyHi      int
	}
	byKey := map[int]*keyAgg{}
	var keyOrder []int

	for _, g := range groups {
		// Group-level seqMode applies to samples that don't set their own.
		groupRandom := strings.EqualFold(strings.TrimSpace(g.SeqMode), "random")
		for _, s := range g.Samples {
			root := midiNote(firstNonEmpty(s.RootNote, "60"), sampler.DefaultKeycenter)
			k := root
			if s.LoNote != "" {
				k = midiNote(s.LoNote, root)
			}
			agg, ok := byKey[k]
			if !ok {
				agg = &keyAgg{layers: map[velKey]*sampler.Layer{}}
				byKey[k] = agg
				keyOrder = append(keyOrder, k)
			}
			agg.root = root
			agg.keyLo = midiNote(firstNonEmpty(s.LoNote, ""), agg.keyLo)
			agg.keyHi = midiNote(firstNonEmpty(s.HiNote, ""), agg.keyHi)

			lo := atoiSafe(firstNonEmpty(s.LoVel, "1"), 1)
			hi := atoiSafe(firstNonEmpty(s.HiVel, "127"), 127)
			vk := velKey{lo, hi}
			layer, ok := agg.layers[vk]
			if !ok {
				rrMode := sampler.Sequential
				sm := firstNonEmpty(s.SeqMode, g.SeqMode)
				if strings.EqualFold(strings.TrimSpace(sm), "random") || (sm == "" && groupRandom) {
					rrMode = sampler.Random
				}
				layer = &sampler.Layer{VelLo: lo, VelHi: hi, RRMode: rrMode}
				agg.layers[vk] = layer
				agg.layerOrder = append(agg.layerOrder, vk)
			}

			v, missing := dsSampleToVariant(s, baseDir)
			if missing != "" {
				notes = append(notes, missing)
			}
			seqPos := atoiSafe(s.SeqPosition, 0)
			if seqPos > 0 {
				layer.RoundRobin = insertAtSeq(layer.RoundRobin, v, seqPos)
			} else {
				layer.RoundRobin = append(layer.RoundRobin, v)
			}
		}
	}

	sort.Ints(keyOrder)
	var sounds []sampler.Sound
	for _, k := range keyOrder {
		agg := byKey[k]
		sort.Slice(agg.layerOrder, func(i, j int) bool {
			if agg.layerOrder[i].lo != agg.layerOrder[j].lo {
				return agg.layerOrder[i].lo < agg.layerOrder[j].lo
			}
			return agg.layerOrder[i].hi < agg.layerOrder[j].hi
		})
		snd := sampler.Sound{Root: agg.root, KeyLo: agg.keyLo, KeyHi: agg.keyHi}
		for _, vk := range agg.layerOrder {
			snd.Layers = append(snd.Layers, *agg.layers[vk])
		}
		if len(snd.Layers) > 0 && len(snd.Layers[0].RoundRobin) > 0 {
			snd.Name = stripExt(pathx.Base(snd.Layers[0].RoundRobin[0].SamplePath))
		}
		sounds = append(sounds, snd.Normalize())
	}
	return ParseDecentSamplerResult{Sounds: sounds, Notes: notes}, nil
}

func dsSampleToVariant(s dsSample, baseDir string) (sampler.Variant, string) {
	v := sampler.Variant{
		PitchKeycenter: midiNote(firstNonEmpty(s.RootNote, "60"), sampler.DefaultKeycenter),
		// DecentSampler `tuning` is in semitones; we store cents in Tune.
		Tune:       int(atofSafe(s.Tuning, 0) * 100),
		StartFrame: int64(atoiSafe(s.Start, 0)),
		EndFrame:   int64(atoiSafe(s.End, 0)),
		LoopStart:  int64(atoiSafe(s.LoopStart, 0)),
		LoopEnd:    int64(atoiSafe(s.LoopEnd, 0)),
		Gain:       atofSafe(strings.TrimSuffix(s.Volume, "dB"), 0),
		Pan:        atofSafe(s.Pan, 0) / 100.0,
	}
	if strings.EqualFold(strings.TrimSpace(s.LoopEnabled), "true") || (s.LoopEnd != "" && s.LoopStart != "") {
		v.LoopMode = sampler.LoopContinuous
	}

	var note string
	if strings.TrimSpace(s.Path) != "" {
		resolved := resolveSamplePath(s.Path, baseDir)
		v.SamplePath = resolved
		if !fileExists(resolved) {
			v.Missing = true
			note = "missing sample: " + resolved
		}
	} else {
		v.Missing = true
		note = "sample element with no path attribute"
	}
	return v, note
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
