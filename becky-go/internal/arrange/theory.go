package arrange

import (
	"strings"

	"becky-go/internal/dawmodel"
	"becky-go/internal/music"
)

// theory.go holds the shared, deterministic helpers every layer uses to read the
// existing arrangement (its key, its groove) — the "aware of the stems already
// there" part — plus the per-genre chord-progression defaults.

// Options parameterises a layer add. Genre selects the progression/idiom; Seed makes
// humanization reproducible.
type Options struct {
	Genre string
	Seed  int64
}

const stepsPerBar = 16 // a 16-step bar at music.StepTicks resolution

// resolveKey returns the arrangement's (rootPC, scale intervals, scale name). It
// defaults to A minor when the key is unset — a safe, common default across Jordan's
// genres — so a drums-only loop can still be harmonised.
func resolveKey(a *dawmodel.Arrangement) (rootPC int, scale []int, scaleName string) {
	root := strings.TrimSpace(a.Root)
	sc := strings.TrimSpace(a.Scale)
	if root == "" {
		return 9, music.ScaleIntervals("minor"), "minor" // A minor
	}
	keyStr := root
	if sc != "" {
		keyStr = root + " " + sc
	}
	pc, name := music.ParseKey(keyStr)
	return pc, music.ScaleIntervals(name), name
}

// genreProgressions maps a genre to a default chord progression in Roman numerals.
// RomanToIndex ignores the accidental (the flat/major/minor quality comes from the
// scale), so in a minor key "i bVII bVI V" yields degree indices 0,6,5,4 — the
// natural-minor i–♭VII–♭VI–v root motion. Unknown genres fall back to the default.
var genreProgressions = map[string][]string{
	"":          {"i", "bVII", "bVI", "V"}, // default minor loop
	"house":     {"i", "VI", "III", "VII"}, // four-chord minor
	"techno":    {"i", "i", "bVI", "V"},    // hypnotic, kick-led
	"trap":      {"i", "i", "bVI", "bVII"}, // dark, static
	"dnb":       {"i", "bVII", "bVI", "bVII"},
	"lofi":      {"ii", "V", "i", "vi"}, // jazz-ish ii–V
	"lo-fi":     {"ii", "V", "i", "vi"},
	"emo":       {"i", "bVI", "bIII", "bVII"},
	"pop-punk":  {"I", "V", "vi", "IV"}, // major four-chord
	"metalcore": {"i", "bVI", "bVII", "v"},
	"crunkcore": {"i", "bVII", "bVI", "V"},
}

// progressionFor returns the per-bar scale-degree indices for a genre's default
// progression (deterministic; unknown genre → the default).
func progressionFor(genre string) []int {
	prog := genreProgressions[strings.ToLower(strings.TrimSpace(genre))]
	if len(prog) == 0 {
		prog = genreProgressions[""]
	}
	out := make([]int, len(prog))
	for i, r := range prog {
		out[i] = music.RomanToIndex(r)
	}
	return out
}

// drumClip finds the arrangement's drum clip (channel-9 by clip header or by note),
// returning (trackID, clipName, ok). Mirrors the detection used elsewhere so every
// part of becky resolves the same drums.
func drumClip(a *dawmodel.Arrangement) (string, string, bool) {
	for _, t := range a.Tracks {
		if t.Kind != "" && t.Kind != dawmodel.KindMIDI {
			continue
		}
		for _, c := range t.Clips {
			if len(c.Notes) == 0 {
				continue
			}
			if c.Channel == 9 {
				return t.ID, c.Name, true
			}
			for _, n := range c.Notes {
				if n.Ch == 9 {
					return t.ID, c.Name, true
				}
			}
		}
	}
	return "", "", false
}

// kickOnsets reads the actual kick rhythm from the arrangement's drums: it returns,
// per bar, the step indices (0..15) where the kick fires — the groove the bass must
// lock to — plus the number of bars (capped at MaxChunkBars). When there is no drum
// clip it returns (nil, 0): callers fall back to placing roots on strong beats.
func kickOnsets(a *dawmodel.Arrangement) (perBar [][]int, bars int) {
	tid, clip, ok := drumClip(a)
	if !ok {
		return nil, 0
	}
	g, err := a.DrumGridOf(tid, clip, 0)
	if err != nil || g == nil || len(g.Lanes) == 0 {
		return nil, 0
	}
	// The kick lane is GM note 35 or 36.
	var kick *dawmodel.Lane
	for i := range g.Lanes {
		if g.Lanes[i].Note == 35 || g.Lanes[i].Note == 36 {
			kick = &g.Lanes[i]
			break
		}
	}
	bars = g.Bars
	if bars < 1 {
		bars = 1
	}
	if bars > MaxChunkBars {
		bars = MaxChunkBars
	}
	perBar = make([][]int, bars)
	if kick == nil {
		return perBar, bars // drums exist but no identifiable kick → empty per-bar
	}
	for b := 0; b < bars; b++ {
		for s := 0; s < stepsPerBar; s++ {
			idx := b*stepsPerBar + s
			if idx < len(kick.On) && kick.On[idx] {
				perBar[b] = append(perBar[b], s)
			}
		}
	}
	return perBar, bars
}

// hasRole reports whether the arrangement already has a track whose id matches a
// layer role (so we don't add a second bass, etc.), case-insensitively.
func hasRole(a *dawmodel.Arrangement, role string) bool {
	for _, t := range a.Tracks {
		if strings.EqualFold(t.ID, role) {
			return true
		}
	}
	return false
}

// presentRoles returns the set of layer roles already in the arrangement (drums
// detected by channel-9; others by track id) for NextLayer.
func presentRoles(a *dawmodel.Arrangement) map[string]bool {
	out := map[string]bool{}
	if _, _, ok := drumClip(a); ok {
		out["drums"] = true
	}
	for _, t := range a.Tracks {
		out[strings.ToLower(t.ID)] = true
	}
	return out
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
