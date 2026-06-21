// Package intent is becky's DETERMINISTIC plain-English parser: it turns a phrase
// like "dark trap at 140, 8 bars, just drums" into a structured Spec the music pipe
// can run — with NO model, NO tokens. Humans are deterministic; so is this. It reads
// genre (+ mood/synonyms), tempo, bar count, key, and flags out of free text, and
// derives a stable seed from the words so the "same vibe" always yields the same song.
//
// This is the front of the pipe: phrase → Spec → beatgen/arrange/render.
package intent

import (
	"hash/fnv"
	"regexp"
	"strconv"
	"strings"
)

// Spec is the parsed musical intent. Zero values mean "use the pipe default".
type Spec struct {
	Genre      string   // canonical genre id ("" = none recognized → caller's default)
	BPM        int      // 0 = use the genre default
	Bars       int      // 0 = use the default
	Root       string   // "" = default key root
	Scale      string   // "" = default scale
	DrumsOnly  bool     // build only the drum layer
	Seed       int64    // stable: derived from the text when not given as "seed N"
	Understood []string // human-readable list of what was parsed (for feedback)
}

// genreSynonyms maps spoken vocabulary to a canonical genre. Multi-word keys are
// checked before single words so "drum and bass" wins over a stray "bass".
var genreSynonyms = []struct {
	phrase string
	genre  string
}{
	{"drum and bass", "dnb"}, {"drum n bass", "dnb"}, {"d&b", "dnb"}, {"jungle", "dnb"}, {"dnb", "dnb"},
	{"boom bap", "hiphop"}, {"hip hop", "hiphop"}, {"hip-hop", "hiphop"}, {"hiphop", "hiphop"}, {"rap", "hiphop"},
	{"lo-fi", "lofi"}, {"lo fi", "lofi"}, {"lofi", "lofi"}, {"chillhop", "lofi"}, {"chill", "lofi"},
	{"deep house", "house"}, {"tech house", "house"}, {"house", "house"},
	{"pop punk", "pop-punk"}, {"pop-punk", "pop-punk"}, {"poppunk", "pop-punk"},
	{"metalcore", "metalcore"}, {"metal", "metalcore"},
	{"crunkcore", "crunkcore"}, {"crunk", "crunkcore"},
	{"breakbeat", "breakbeat"}, {"breaks", "breakbeat"},
	{"techno", "techno"}, {"trap", "trap"}, {"emo", "emo"}, {"digicore", "digicore"}, {"hyperpop", "hyperpop"},
}

// genreMatchers is genreSynonyms compiled to word-bounded regexes, so "rap" does NOT
// match inside "trap". Built once at init.
var genreMatchers []struct {
	re    *regexp.Regexp
	genre string
}

func init() {
	for _, g := range genreSynonyms {
		re := regexp.MustCompile(`(?:^|[^a-z0-9])` + regexp.QuoteMeta(g.phrase) + `(?:[^a-z0-9]|$)`)
		genreMatchers = append(genreMatchers, struct {
			re    *regexp.Regexp
			genre string
		}{re, g.genre})
	}
}

// moodMinor / moodMajor adjust the scale when no explicit key is given.
var moodMinor = []string{"dark", "sad", "moody", "aggressive", "evil", "melancholy", "melancholic", "gloomy", "heavy", "menacing"}
var moodMajor = []string{"happy", "bright", "uplifting", "sunny", "joyful", "feel good", "feel-good", "major key", "cheerful"}

var (
	reBPM  = regexp.MustCompile(`(\d{2,3})\s*bpm|(?:at|tempo)\s+(\d{2,3})`)
	reBars = regexp.MustCompile(`(\d{1,3})[\s-]*bars?`)
	reKey  = regexp.MustCompile(`\b(?:in|key of)\s+([a-g])(#|b|♯|♭)?\s*(major|minor|maj|min|dorian|phrygian|lydian|mixolydian|aeolian|harmonic minor)?\b`)
	reSeed = regexp.MustCompile(`seed\s+(\d+)`)
)

// Parse turns free text into a Spec. It never errors — unrecognized text just leaves
// fields at their defaults.
func Parse(text string) Spec {
	t := strings.ToLower(strings.TrimSpace(text))
	var s Spec

	// Genre (first matching synonym, multi-word first, word-bounded so "rap" ≠ "trap").
	for _, g := range genreMatchers {
		if g.re.MatchString(t) {
			s.Genre = g.genre
			s.Understood = append(s.Understood, "genre: "+g.genre)
			break
		}
	}

	// BPM.
	if m := reBPM.FindStringSubmatch(t); m != nil {
		num := m[1]
		if num == "" {
			num = m[2]
		}
		if n, err := strconv.Atoi(num); err == nil && n >= 40 && n <= 250 {
			s.BPM = n
			s.Understood = append(s.Understood, "bpm: "+num)
		}
	}

	// Bars.
	if m := reBars.FindStringSubmatch(t); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n >= 1 && n <= 256 {
			s.Bars = n
			s.Understood = append(s.Understood, m[1]+" bars")
		}
	}

	// Explicit key ("in F minor", "key of A").
	if m := reKey.FindStringSubmatch(t); m != nil {
		root := strings.ToUpper(m[1])
		switch m[2] {
		case "#", "♯":
			root += "#"
		case "b", "♭":
			root += "b"
		}
		s.Root = root
		s.Scale = normalizeScale(m[3])
		s.Understood = append(s.Understood, "key: "+root+" "+s.Scale)
	} else {
		// Mood → scale (only when no explicit key).
		if containsAny(t, moodMajor) {
			s.Scale = "major"
			s.Understood = append(s.Understood, "feel: major")
		} else if containsAny(t, moodMinor) {
			s.Scale = "minor"
			s.Understood = append(s.Understood, "feel: minor (dark)")
		}
	}

	// Drums-only.
	if containsAny(t, []string{"drums only", "just drums", "only drums", "beat only", "just a beat", "drum loop", "no melody"}) {
		s.DrumsOnly = true
		s.Understood = append(s.Understood, "drums only")
	}

	// Seed: explicit "seed N", else a stable hash of the words (same vibe → same song).
	if m := reSeed.FindStringSubmatch(t); m != nil {
		if n, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			s.Seed = n
		}
	} else {
		s.Seed = stableSeed(t)
	}
	return s
}

func normalizeScale(s string) string {
	switch strings.TrimSpace(s) {
	case "", "min", "minor", "aeolian":
		return "minor"
	case "maj", "major":
		return "major"
	default:
		return s
	}
}

func containsAny(t string, words []string) bool {
	for _, w := range words {
		if strings.Contains(t, w) {
			return true
		}
	}
	return false
}

// stableSeed hashes the text to a small positive seed so the same phrase always
// produces the same song (deterministic "read my mind").
func stableSeed(t string) int64 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(t))
	return int64(h.Sum32()%100000) + 1
}
