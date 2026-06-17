package drumcmd

// parse.go is the deterministic keyword parser — the testable offline core that
// turns the documented example sentences into a DrumCommand with NO model and NO
// network. It is the fallback the model path degrades to (see model.go).
//
// Handled sentences (each is "click for 10 minutes" in a normal drum machine):
//
//	"make it half-time"                         → HalfTime
//	"double-time it" / "double time"            → DoubleTime
//	"humanize the snare" / "humanize the drums" → Humanize (lane scoped)
//	"add a hi-hat roll into beat 4"             → Fill (lane=hat, beat=4)
//	"add a fill"                                → Fill (default lane/beat)
//	"swing it" / "more swing"                   → Swing
//	"give me 3 variations"                      → Variations (count=3)
//	"make it busier" / "strip it back"          → Density (up/down)
//	"tighten it to the grid"                    → Quantize
//
// Anything else → Action Unknown with a friendly note (degrade, never crash).

import (
	"regexp"
	"strconv"
	"strings"
)

// laneVocab maps spoken lane words to canonical lane names used by the grid.
// Order doesn't matter; longest/most-specific match wins via explicit checks.
var laneVocab = map[string]string{
	"hi-hat": "hat", "hihat": "hat", "hi hat": "hat", "hat": "hat", "hats": "hat",
	"open hat": "ohat", "open-hat": "ohat", "ohat": "ohat",
	"snare": "snare", "clap": "clap", "kick": "kick", "kicks": "kick",
	"rim": "rim", "ride": "ride", "crash": "crash", "tom": "tom", "toms": "tom",
}

var (
	numWords = map[string]int{
		"one": 1, "two": 2, "three": 3, "four": 4, "five": 5,
		"six": 6, "seven": 7, "eight": 8, "a": 1, "an": 1,
	}
	beatRe  = regexp.MustCompile(`\bbeat\s+(\d+)\b`)
	countRe = regexp.MustCompile(`\b(\d+)\b`)
)

// ParseKeyword is the deterministic parser. It lower-cases and trims the
// instruction, then matches the documented intents in priority order. seed is
// stamped onto the command so Humanize/Variations are reproducible.
func ParseKeyword(instruction string, seed int64) DrumCommand {
	raw := instruction
	s := strings.ToLower(strings.TrimSpace(instruction))
	if seed <= 0 {
		seed = DefaultSeed
	}
	cmd := DrumCommand{Seed: seed, Raw: raw}

	switch {
	case s == "":
		cmd.Action = Unknown
		cmd.Note = "Tell me what to do with the beat — e.g. 'make it half-time' or 'humanize the snare'."
		return cmd

	// Half-time / double-time. Check double first ("double time", "double-time").
	case containsAny(s, "double-time", "double time", "doubletime", "double it", "twice as fast"):
		cmd.Action = DoubleTime
		return cmd
	case containsAny(s, "half-time", "half time", "halftime", "halve", "half speed", "half as fast"):
		cmd.Action = HalfTime
		return cmd

	// Humanize.
	case containsAny(s, "humanize", "humanise", "human feel", "loosen", "less robotic", "more human"):
		cmd.Action = Humanize
		cmd.Lane = laneFrom(s) // "" when "the drums"/all
		return cmd

	// Fill / roll. Capture optional lane + beat.
	case containsAny(s, "fill", "roll", "drum fill", "build"):
		cmd.Action = Fill
		cmd.Lane = laneFrom(s)
		cmd.Beat = beatFrom(s)
		return cmd

	// Swing.
	case containsAny(s, "swing", "shuffle", "groove", "swung"):
		cmd.Action = Swing
		cmd.Swing = swingFrom(s)
		return cmd

	// Variations.
	case containsAny(s, "variation", "variations", "variant", "variants", "alternatives", "options", "ideas", "versions"):
		cmd.Action = Variations
		cmd.Count = countFrom(s)
		return cmd

	// Density up.
	case containsAny(s, "busier", "more busy", "more hats", "more going on", "fuller", "denser", "more dense", "fill it out"):
		cmd.Action = Density
		cmd.Up = true
		return cmd

	// Density down.
	case containsAny(s, "strip it back", "strip back", "stripped back", "simpler", "sparser", "less busy", "thin it", "thin out", "minimal", "fewer hats", "take stuff out", "less going on"):
		cmd.Action = Density
		cmd.Up = false
		return cmd

	// Quantize.
	case containsAny(s, "tighten", "quantize", "quantise", "to the grid", "on the grid", "snap", "lock to grid", "tight"):
		cmd.Action = Quantize
		return cmd

	default:
		cmd.Action = Unknown
		cmd.Note = "I didn't recognise \"" + strings.TrimSpace(raw) + "\". Try 'make it half-time', 'humanize the snare', 'swing it', 'add a fill on beat 4', 'give me 3 variations', 'make it busier', or 'tighten it to the grid'."
		return cmd
	}
}

// containsAny reports whether s contains any of the substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// laneFrom extracts a canonical lane name from the instruction, or "" when the
// instruction targets everything ("the drums", "the beat", "all", or no lane).
func laneFrom(s string) string {
	// Explicit "all"/"drums"/"beat" → all lanes (empty scope).
	if containsAny(s, "the drums", "all drums", "everything", "the beat", "the whole", "all of it") {
		return ""
	}
	// Longest keys first so "open hat" beats "hat" and "hi-hat" beats "hat".
	for _, k := range []string{"open-hat", "open hat", "hi-hat", "hi hat", "hihat", "snare", "clap", "kicks", "kick", "ride", "crash", "toms", "tom", "rim", "ohat", "hats", "hat"} {
		if strings.Contains(s, k) {
			return laneVocab[k]
		}
	}
	return ""
}

// beatFrom extracts a 1-based beat number ("into beat 4" → 4); 0 when absent.
func beatFrom(s string) int {
	if m := beatRe.FindStringSubmatch(s); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			return n
		}
	}
	return 0
}

// countFrom extracts a variation count: a digit ("3 variations") or a number
// word ("three variations"). Defaults to 3 when none is given.
func countFrom(s string) int {
	if m := countRe.FindStringSubmatch(s); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
			return n
		}
	}
	for w, n := range numWords {
		if regexp.MustCompile(`\b` + w + `\b`).MatchString(s) {
			if n > 1 || strings.Contains(s, w+" variation") {
				return n
			}
		}
	}
	return 3
}

// swingFrom returns a swing ratio. "more swing"/"heavy"/"hard" lean higher; a
// bare "swing it" uses the musical default (0 → Apply picks 0.58). A percentage
// ("swing 66%") maps linearly into 0.5..0.75.
func swingFrom(s string) float64 {
	if m := regexp.MustCompile(`(\d+)\s*%`).FindStringSubmatch(s); m != nil {
		if pct, err := strconv.Atoi(m[1]); err == nil {
			r := 0.5 + float64(pct)/100*0.25
			if r > 0.75 {
				r = 0.75
			}
			if r < 0.5 {
				r = 0.5
			}
			return r
		}
	}
	if containsAny(s, "more swing", "heavy swing", "hard swing", "lots of swing", "extra swing") {
		return 0.66
	}
	return 0 // Apply substitutes the default 0.58
}
