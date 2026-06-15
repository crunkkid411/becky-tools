package music

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

//go:embed profiles/*.json
var profilesFS embed.FS

// Profile is a genre encoded as a bundle of deterministic numeric constraints
// (tempo window, scales, Roman-numeral progressions, per-track 16-step grids,
// registers, swing, humanization). Researched once, frozen as JSON, generated
// from forever. Schema mirrors SPEC-BECKY-COMPOSE.md §4.
type Profile struct {
	SchemaVersion int      `json:"schemaVersion"`
	ID            string   `json:"id"`
	DisplayName   string   `json:"displayName"`
	Sources       []string `json:"sources"`
	Tempo         struct {
		Min     int `json:"min"`
		Max     int `json:"max"`
		Default int `json:"default"`
	} `json:"tempo"`
	Key struct {
		DefaultRoot  string   `json:"defaultRoot"`
		Scales       []string `json:"scales"`
		DefaultScale string   `json:"defaultScale"`
	} `json:"key"`
	Swing    float64 `json:"swing"`
	Humanize struct {
		TimingJitter int `json:"timingJitter"`
		VelHumanize  int `json:"velHumanize"`
	} `json:"humanize"`
	Progressions []Progression        `json:"progressions"`
	Arrangement  []Section            `json:"arrangement"`
	Tracks       map[string]TrackSpec `json:"tracks"`
	// Album/era granularity (all optional): a profile can be a broad genre OR a
	// specific band/album sound. Aliases let a producer ask loosely ("underoath
	// safety", "tocs", "define the great line"). Artist/Album/Era are provenance.
	Aliases []string `json:"aliases"`
	Artist  string   `json:"artist"`
	Album   string   `json:"album"`
	Era     string   `json:"era"`
}

// Progression is a weighted Roman-numeral chord loop.
type Progression struct {
	Name   string   `json:"name"`
	Weight int      `json:"weight"`
	Roman  []string `json:"roman"`
}

// Section is one arrangement block (bars + energy + which tracks play).
type Section struct {
	Name    string   `json:"name"`
	Bars    int      `json:"bars"`
	Energy  float64  `json:"energy"`
	Tracks  []string `json:"tracks"`
	Chaotic bool     `json:"chaotic"`
}

// TrackSpec describes how one track is generated. Drum tracks use Patterns; pitched
// tracks use the style/voicing/contour/rhythm fields.
type TrackSpec struct {
	Program          *int                 `json:"program"`
	Channel          int                  `json:"channel"`
	Register         []int                `json:"register"`
	Patterns         map[string]DrumVoice `json:"patterns"`
	DensityByEnergy  bool                 `json:"densityByEnergy"`
	Style            string               `json:"style"`
	Octave           int                  `json:"octave"`
	Glide            bool                 `json:"glide"`
	Voicing          string               `json:"voicing"`
	Extensions       []string             `json:"extensions"`
	Rhythm           string               `json:"rhythm"`
	ScaleSource      string               `json:"scaleSource"`
	Contour          string               `json:"contour"`
	MotifBars        int                  `json:"motifBars"`
	Density          float64              `json:"density"`
	Role             string               `json:"role"`
	Events           []string             `json:"events"`
	ActiveFromEnergy float64              `json:"activeFromEnergy"`
	Vel              string               `json:"vel"`
}

// DrumVoice is one percussion voice: a 16-step grid of hits + accents + rolls.
type DrumVoice struct {
	Note  int    `json:"note"`
	Grid  []int  `json:"grid"`
	Vel   string `json:"vel"`
	Rolls []Roll `json:"rolls"`
}

// Roll subdivides a grid cell into N evenly-spaced hits with a velocity ramp.
type Roll struct {
	Cell int    `json:"cell"`
	N    int    `json:"n"`
	Ramp string `json:"ramp"`
}

// velLadder is the named velocity ladder (SPEC §2.9) — fixed accents read musically.
var velLadder = map[string]int{
	"ghost": 40, "soft": 64, "normal": 88, "accent": 104, "hard": 118,
}

// Vel resolves a velocity name to a number (normal if unknown/empty).
func Vel(name string) int {
	if v, ok := velLadder[name]; ok {
		return v
	}
	return 88
}

// scaleAlias maps mode names used in profiles to this engine's scale table keys.
func scaleAlias(name string) string {
	switch strings.ToLower(name) {
	case "aeolian":
		return "minor"
	case "ionian":
		return "major"
	}
	return strings.ToLower(name)
}

// LoadProfile returns the embedded genre profile for id (case-insensitive). The
// genre DB is shipped in the binary, so generation is offline + deterministic.
func LoadProfile(id string) (Profile, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	data, err := profilesFS.ReadFile("profiles/" + id + ".json")
	if err != nil {
		return Profile{}, fmt.Errorf("genre %q not in the becky-compose DB (%w). Known: %s",
			id, err, strings.Join(KnownGenres(), ", "))
	}
	var p Profile
	if err := json.Unmarshal(data, &p); err != nil {
		return Profile{}, fmt.Errorf("parse profile %q: %w", id, err)
	}
	p.Key.DefaultScale = scaleAlias(p.Key.DefaultScale)
	return p, nil
}

// KnownGenres lists the profile ids shipped in the embedded DB (sorted).
func KnownGenres() []string {
	ents, _ := profilesFS.ReadDir("profiles")
	out := make([]string, 0, len(ents))
	for _, e := range ents {
		out = append(out, strings.TrimSuffix(e.Name(), ".json"))
	}
	sort.Strings(out)
	return out
}

// ResolveProfile finds the best profile for a free-text query: an exact id wins,
// otherwise it scores every profile by how many query tokens appear in its id,
// display name, artist, album, era, or aliases. This is how "underoath safety",
// "tocs", or "define the great line" resolve to the right album-specific profile.
func ResolveProfile(query string) (Profile, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	if p, err := LoadProfile(q); err == nil {
		return p, nil
	}
	qTokens := strings.Fields(strings.ReplaceAll(q, "-", " "))
	var best Profile
	bestScore := 0
	for _, id := range KnownGenres() {
		p, err := LoadProfile(id)
		if err != nil {
			continue
		}
		fields := append([]string{p.ID, p.DisplayName, p.Artist, p.Album, p.Era}, p.Aliases...)
		hay := strings.ReplaceAll(strings.ToLower(strings.Join(fields, " ")), "-", " ")
		score := 0
		for _, t := range qTokens {
			if t != "" && strings.Contains(hay, t) {
				score++
			}
		}
		if score > bestScore {
			best, bestScore = p, score
		}
	}
	if bestScore > 0 {
		return best, nil
	}
	return Profile{}, fmt.Errorf("no profile matches %q. Known: %s", query, strings.Join(KnownGenres(), ", "))
}
