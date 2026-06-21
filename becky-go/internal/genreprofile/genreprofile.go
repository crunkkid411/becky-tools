// Package genreprofile is the deterministic back half of becky's genre-research
// pipeline (STANDARDS-MUSIC-RESEARCH.md): it turns the DISTILLED "5 elements" of a
// genre into a valid, embeddable music.Profile, and it generates the research query
// templates that drive the front half.
//
//	becky-research "{genre}"  →  distill to Elements (the model/online 5%)  →  ProfileFromElements  →  profiles/<id>.json
//
// Only the distillation step needs a model/network; everything here is pure,
// deterministic Go, so the genre KNOWLEDGE — once researched — becomes permanent data
// the arranger consumes, never re-derived in chat.
package genreprofile

import (
	"fmt"
	"strings"

	"becky-go/internal/music"
)

// Queries returns the research search-query templates for a genre, verbatim from the
// standard. Run these (aimed at Hooktheory/Chordify/Ultimate Guitar) to research a
// genre's 5 elements. Deterministic.
func Queries(genre string) []string {
	g := strings.TrimSpace(genre)
	return []string{
		fmt.Sprintf("%q chord progressions analysis", g),
		fmt.Sprintf("%q song structure common patterns", g),
		fmt.Sprintf("%q rhythm patterns drum programming", g),
		fmt.Sprintf("%q bass line techniques", g),
		fmt.Sprintf("%q typical BPM tempo", g),
		fmt.Sprintf("%q scales modes used", g),
	}
}

// ReferenceQueries returns the higher-value query templates for a NAMED reference
// (a song/artist Jordan calls out) — "named references are gold".
func ReferenceQueries(songOrArtist string) []string {
	s := strings.TrimSpace(songOrArtist)
	return []string{
		fmt.Sprintf("%q chord progression key BPM", s),
		fmt.Sprintf("%q music analysis breakdown", s),
	}
}

// Elements is the distilled research output — the genre's "5 elements" in a shape a
// model fills from research and ProfileFromElements turns into a profile.
type Elements struct {
	ID          string   `json:"id"`
	DisplayName string   `json:"displayName"`
	Sources     []string `json:"sources"`
	// 1. Key/Scale
	Root         string   `json:"root"`
	Scales       []string `json:"scales"`
	DefaultScale string   `json:"defaultScale"`
	// 2. Chord language — progressions in Roman numerals
	Progressions []music.Progression `json:"progressions"`
	// 3. Rhythmic feel
	TempoMin     int     `json:"tempoMin"`
	TempoMax     int     `json:"tempoMax"`
	TempoDefault int     `json:"tempoDefault"`
	Swing        float64 `json:"swing"`
	TimingJitter int     `json:"timingJitter"`
	VelHumanize  int     `json:"velHumanize"`
	// 4. Texture — which instrument roles the genre uses
	Texture []string `json:"texture"`
	// 5. Form — section lengths + energy (optional; a default is generated if empty)
	Form []music.Section `json:"form"`
	// Provenance (optional)
	Artist string `json:"artist,omitempty"`
	Album  string `json:"album,omitempty"`
	Era    string `json:"era,omitempty"`
}

// BlankElements is a scaffold for a genre — a model/human fills it from research.
func BlankElements(id string) Elements {
	id = strings.ToLower(strings.TrimSpace(id))
	return Elements{
		ID:           id,
		DisplayName:  titleize(id),
		Root:         "A",
		Scales:       []string{"minor"},
		DefaultScale: "minor",
		Progressions: []music.Progression{{Name: "main", Weight: 3, Roman: []string{"i", "bVII", "bVI", "V"}}},
		TempoMin:     120, TempoMax: 140, TempoDefault: 128,
		Swing: 0.5, TimingJitter: 4, VelHumanize: 8,
		Texture: []string{"drums", "bass", "chords", "melody"},
	}
}

// ProfileFromElements builds a valid, embeddable music.Profile from distilled
// Elements. It fills sensible default track specs per texture role and a default
// 8-bar-chunked arrangement when Form is empty. Returns an error if the Elements are
// missing the irreducible facts (id, a key, at least one progression).
func ProfileFromElements(e Elements) (music.Profile, error) {
	id := strings.ToLower(strings.TrimSpace(e.ID))
	if id == "" {
		return music.Profile{}, fmt.Errorf("genreprofile: id is required")
	}
	if strings.TrimSpace(e.Root) == "" {
		return music.Profile{}, fmt.Errorf("genreprofile: a key root is required (e.g. \"A\")")
	}
	if len(e.Progressions) == 0 {
		return music.Profile{}, fmt.Errorf("genreprofile: at least one progression is required")
	}
	var p music.Profile
	p.SchemaVersion = 1
	p.ID = id
	p.DisplayName = orStr(e.DisplayName, id)
	p.Sources = e.Sources
	p.Artist, p.Album, p.Era = e.Artist, e.Album, e.Era

	p.Tempo.Min = orInt(e.TempoMin, 120)
	p.Tempo.Max = orInt(e.TempoMax, 140)
	p.Tempo.Default = orInt(e.TempoDefault, clampTempo((p.Tempo.Min+p.Tempo.Max)/2))

	p.Key.DefaultRoot = strings.TrimSpace(e.Root)
	p.Key.Scales = e.Scales
	if len(p.Key.Scales) == 0 {
		p.Key.Scales = []string{"minor"}
	}
	p.Key.DefaultScale = orStr(e.DefaultScale, p.Key.Scales[0])

	p.Swing = e.Swing
	if p.Swing == 0 {
		p.Swing = 0.5
	}
	p.Humanize.TimingJitter = orInt(e.TimingJitter, 4)
	p.Humanize.VelHumanize = orInt(e.VelHumanize, 8)

	p.Progressions = e.Progressions

	texture := e.Texture
	if len(texture) == 0 {
		texture = []string{"drums", "bass", "chords", "melody"}
	}
	p.Tracks = map[string]music.TrackSpec{}
	for _, role := range texture {
		p.Tracks[role] = defaultTrackSpec(role)
	}

	p.Arrangement = e.Form
	if len(p.Arrangement) == 0 {
		p.Arrangement = defaultForm(texture)
	}
	return p, nil
}

// defaultForm is a standard song shape in 8-bar-max chunks (Jordan's rule) using the
// genre's texture.
func defaultForm(texture []string) []music.Section {
	full := texture
	core := intersect(texture, []string{"drums", "bass", "chords", "melody"})
	if len(core) == 0 {
		core = texture
	}
	return []music.Section{
		{Name: "intro", Bars: 4, Energy: 0.2, Tracks: intersect(texture, []string{"chords"})},
		{Name: "verse", Bars: 8, Energy: 0.5, Tracks: core},
		{Name: "build", Bars: 8, Energy: 0.8, Tracks: core},
		{Name: "drop", Bars: 8, Energy: 1.0, Tracks: full},
		{Name: "verse2", Bars: 8, Energy: 0.5, Tracks: core},
		{Name: "outro", Bars: 4, Energy: 0.3, Tracks: intersect(texture, []string{"chords"})},
	}
}

// defaultTrackSpec returns a sensible spec for a role so the profile is immediately
// usable by becky-compose. Conservative GM defaults; research can refine the JSON.
func defaultTrackSpec(role string) music.TrackSpec {
	prog := func(n int) *int { return &n }
	switch strings.ToLower(role) {
	case "drums":
		return music.TrackSpec{
			Channel: 9, DensityByEnergy: true,
			Patterns: map[string]music.DrumVoice{
				"kick": {Note: 36, Grid: []int{1, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0}, Vel: "accent"},
				"clap": {Note: 39, Grid: []int{0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0}, Vel: "accent"},
				"hat":  {Note: 42, Grid: []int{0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 1, 0}, Vel: "soft"},
			},
		}
	case "bass":
		return music.TrackSpec{Program: prog(38), Channel: 1, Register: []int{28, 48}, Octave: -2, Rhythm: "rootFollowsKick"}
	case "chords":
		return music.TrackSpec{Program: prog(81), Channel: 2, Register: []int{48, 72}, Voicing: "triad"}
	case "melody", "lead":
		return music.TrackSpec{Program: prog(80), Channel: 3, Register: []int{60, 84}, ScaleSource: "key", Contour: "arch", MotifBars: 2}
	default:
		return music.TrackSpec{Program: prog(81), Channel: 4, Register: []int{48, 84}, Role: role}
	}
}

// titleize upper-cases the first letter of each dash/space-separated word (ASCII).
func titleize(id string) string {
	words := strings.FieldsFunc(id, func(r rune) bool { return r == '-' || r == ' ' })
	for i, w := range words {
		if w == "" {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

func orStr(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}
func orInt(n, def int) int {
	if n == 0 {
		return def
	}
	return n
}
func clampTempo(n int) int {
	if n < 40 {
		return 40
	}
	if n > 250 {
		return 250
	}
	return n
}
func intersect(have, want []string) []string {
	set := map[string]bool{}
	for _, h := range have {
		set[strings.ToLower(h)] = true
	}
	var out []string
	for _, w := range want {
		if set[strings.ToLower(w)] {
			out = append(out, w)
		}
	}
	return out
}
