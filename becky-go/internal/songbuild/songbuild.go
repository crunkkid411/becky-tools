// Package songbuild is the shared, deterministic "phrase → arrangement" core of
// becky's music pipe. Both becky-song (CLI → WAV/JSON/MID) and becky-canvas (type a
// phrase in the window) call Build, so the generation logic lives in ONE place and the
// canvas and the CLI always agree. No render, no model — just beatgen drums + the
// arrange layering, resolved from an intent.Spec.
package songbuild

import (
	"becky-go/internal/arrange"
	"becky-go/internal/beatgen"
	"becky-go/internal/dawmodel"
	"becky-go/internal/intent"
	"becky-go/internal/music"
)

var standardKit = []string{"kick", "snare", "clap", "hat", "ohat", "rim", "tom", "ride"}

// genreBPM is the default-tempo table; an explicit Spec.BPM overrides it.
var genreBPM = map[string]int{
	"house": 124, "techno": 130, "trap": 140, "dnb": 174, "hiphop": 90,
	"lofi": 80, "breakbeat": 136, "emo": 160, "pop-punk": 170, "metalcore": 180,
	"crunkcore": 145, "straight": 120,
}

// DefaultBPM returns the genre's default tempo (128 for unknown).
func DefaultBPM(genre string) int {
	if b, ok := genreBPM[genre]; ok {
		return b
	}
	return 128
}

// Build turns a parsed intent into a full arrangement: a genre drum beat tiled to the
// requested bars, then (unless drums-only) bass → chords → melody jammed in on top,
// each layer fitting the stems before it. Deterministic; defaults fill any unset
// field. Never returns nil without an error.
func Build(spec intent.Spec) (*dawmodel.Arrangement, error) {
	genre := spec.Genre
	if genre == "" {
		genre = "straight"
	}
	bars := spec.Bars
	if bars <= 0 {
		bars = 4
	}
	seed := spec.Seed
	if seed == 0 {
		seed = 1
	}
	bpm := spec.BPM
	if bpm <= 0 {
		bpm = DefaultBPM(genre)
	}
	root := spec.Root
	if root == "" {
		root = "A"
	}
	scale := spec.Scale
	if scale == "" {
		scale = "minor"
	}

	lanes := make([]beatgen.Lane, 0, len(standardKit))
	for _, role := range standardKit {
		lanes = append(lanes, beatgen.Lane{Name: role, Role: role})
	}
	pat := beatgen.NewPattern(16*bars, lanes...).GenerateGenre(genre, seed)
	grid := beatgen.ToDrumGrid(pat)
	if grid.StepTicks <= 0 {
		grid.StepTicks = music.StepTicks
	}
	arr := dawmodel.New()
	arr.BPM = bpm
	arr.Root, arr.Scale = root, scale
	arr = arr.AddTrack("drums", dawmodel.KindMIDI)
	arr.Tracks[0].Clips = append(arr.Tracks[0].Clips, dawmodel.Clip{Name: "beat", Channel: 9, Program: -1})
	out, err := arr.ApplyDrumGrid("drums", "beat", grid)
	if err != nil {
		return nil, err
	}
	arr = out

	if !spec.DrumsOnly {
		for {
			next, layer, jerr := arrange.Jam(arr, arrange.Options{Genre: genre, Seed: seed})
			if jerr != nil || layer == "" {
				break
			}
			arr = next
		}
	}
	return arr, nil
}

// BuildPhrase is the convenience front door: parse plain English, then Build.
func BuildPhrase(phrase string) (*dawmodel.Arrangement, intent.Spec, error) {
	spec := intent.Parse(phrase)
	arr, err := Build(spec)
	return arr, spec, err
}
