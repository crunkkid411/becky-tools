package ctlmodel

// keyword.go — the deterministic, offline NL→BeckyEditBatch parser. It handles the
// common, UNAMBIGUOUS studio phrasings that need no model, and is grounded in the
// live arrangement so every edit it emits is one ctledit.Apply will accept:
//
//	transport : "set tempo to 140", "tempo 92", "140 bpm"
//	mute      : "mute the bass", "unmute the drums"
//	solo      : "solo the lead", "unsolo the bass"
//	pan       : "pan the lead left|right|center", "pan the bass hard left"
//	gain      : "make the bass louder|quieter", "set the lead gain to 0.8"
//	transpose : "transpose the lead up an octave", "transpose down 3 semitones"
//
// Richer phrasings (note-level edits, drum fills, sidechain) are intentionally left to
// the GBNF-constrained model path — the keyword parser never guesses. When it cannot
// turn the instruction into any edit it returns a batch with no edits and a Summary
// that lists examples, so the caller can show the user how to phrase it.

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"becky-go/internal/ctledit"
	"becky-go/internal/dawmodel"
)

// relGainStep is the multiplier for one "louder"/"quieter" step (≈ +2 dB, 10^(2/20)).
const relGainStep = 1.2589

var intRe = regexp.MustCompile(`-?\d+(\.\d+)?`)

// ParseKeyword turns instruction into a BeckyEditBatch using only deterministic rules.
// It never panics; an unrecognized instruction yields an empty-edits batch with a
// helpful Summary.
func ParseKeyword(instruction string, arr *dawmodel.Arrangement) ctledit.BeckyEditBatch {
	s := strings.ToLower(strings.TrimSpace(instruction))
	if s == "" {
		return noEdits(`say what to change, e.g. "set tempo to 140" or "mute the bass"`)
	}

	// Order matters: check the most specific intents first.
	switch {
	case isTempo(s):
		return parseTempo(s)
	case strings.Contains(s, "transpose") || isMelodicMove(s):
		return parseTranspose(s, arr)
	case strings.Contains(s, "pan"):
		return parsePan(s, arr)
	case strings.Contains(s, "mute"):
		return parseMute(s, arr)
	case strings.Contains(s, "solo"):
		return parseSolo(s, arr)
	case isGain(s):
		return parseGain(s, arr)
	}
	return noEdits(`couldn't read "` + strings.TrimSpace(instruction) +
		`" — try "set tempo to 140", "mute the bass", "pan the lead left", "make the bass louder", or "transpose up an octave"`)
}

// ─── transport ─────────────────────────────────────────────────────────────────

func isTempo(s string) bool {
	return strings.Contains(s, "tempo") || strings.Contains(s, "bpm")
}

func parseTempo(s string) ctledit.BeckyEditBatch {
	bpm, ok := firstInt(s)
	if !ok || bpm <= 0 || bpm > 999 {
		return noEdits(`give a tempo between 1 and 999, e.g. "set tempo to 140"`)
	}
	return batch(fmt.Sprintf("set tempo to %d BPM", bpm),
		ctledit.BeckyEdit{Op: ctledit.OpSetTempo, BPM: bpm})
}

// ─── transpose ───────────────────────────────────────────────────────────────

func isMelodicMove(s string) bool {
	moved := strings.Contains(s, "move") || strings.Contains(s, "shift") || strings.Contains(s, "pitch")
	unit := strings.Contains(s, "octave") || strings.Contains(s, "semitone") || strings.Contains(s, "step")
	return moved && unit
}

func parseTranspose(s string, arr *dawmodel.Arrangement) ctledit.BeckyEditBatch {
	semis, ok := transposeAmount(s)
	if !ok {
		return noEdits(`say how far, e.g. "transpose up an octave" or "transpose down 3 semitones"`)
	}
	track := findTrackID(s, arr)
	if track == "" {
		track = firstMIDITrackID(arr)
	}
	if track == "" {
		return noEdits("no MIDI track to transpose — load a session first")
	}
	dir := "up"
	if semis < 0 {
		dir = "down"
	}
	return batch(fmt.Sprintf("transpose %s %s %d semitone(s)", track, dir, abs(semis)),
		ctledit.BeckyEdit{Op: ctledit.OpTranspose, Track: track, Semitones: semis})
}

// transposeAmount reads a signed semitone count: "octave" = 12, otherwise the first
// integer in the string. Direction comes from up/down (default up).
func transposeAmount(s string) (int, bool) {
	mag := 0
	switch {
	case strings.Contains(s, "octave"):
		mag = 12
		if n, ok := firstInt(s); ok && n > 0 {
			mag = 12 * n // "down two octaves"
		}
	default:
		n, ok := firstInt(s)
		if !ok || n == 0 {
			return 0, false
		}
		mag = abs(n)
	}
	if strings.Contains(s, "down") || strings.Contains(s, "lower") {
		return -mag, true
	}
	return mag, true
}

// ─── pan ───────────────────────────────────────────────────────────────────────

func parsePan(s string, arr *dawmodel.Arrangement) ctledit.BeckyEditBatch {
	track := findTrackID(s, arr)
	if track == "" {
		return noEdits(`name a track to pan, e.g. "pan the lead left"`)
	}
	var pan float64
	var label string
	switch {
	case strings.Contains(s, "center") || strings.Contains(s, "centre") || strings.Contains(s, "middle"):
		pan, label = 0, "center"
	case strings.Contains(s, "left"):
		pan, label = -1, "left"
		if !strings.Contains(s, "hard") {
			pan, label = -0.5, "left"
		}
	case strings.Contains(s, "right"):
		pan, label = 1, "right"
		if !strings.Contains(s, "hard") {
			pan, label = 0.5, "right"
		}
	default:
		return noEdits(`say which way, e.g. "pan the lead left", "pan the bass hard right", or "pan the keys center"`)
	}
	return batch(fmt.Sprintf("pan %s %s", track, label),
		ctledit.BeckyEdit{Op: ctledit.OpSetPan, Target: track, Pan: pan})
}

// ─── mute / solo ─────────────────────────────────────────────────────────────

func parseMute(s string, arr *dawmodel.Arrangement) ctledit.BeckyEditBatch {
	track := findTrackID(s, arr)
	if track == "" {
		return noEdits(`name a track to mute, e.g. "mute the bass"`)
	}
	muted := !strings.Contains(s, "unmute") && !strings.Contains(s, "un-mute")
	verb := "mute"
	if !muted {
		verb = "unmute"
	}
	return batch(fmt.Sprintf("%s %s", verb, track),
		ctledit.BeckyEdit{Op: ctledit.OpMute, Target: track, Muted: muted})
}

func parseSolo(s string, arr *dawmodel.Arrangement) ctledit.BeckyEditBatch {
	track := findTrackID(s, arr)
	if track == "" {
		return noEdits(`name a track to solo, e.g. "solo the drums"`)
	}
	soloed := !strings.Contains(s, "unsolo") && !strings.Contains(s, "un-solo")
	verb := "solo"
	if !soloed {
		verb = "unsolo"
	}
	return batch(fmt.Sprintf("%s %s", verb, track),
		ctledit.BeckyEdit{Op: ctledit.OpSolo, Target: track, Soloed: soloed})
}

// ─── gain ──────────────────────────────────────────────────────────────────────

func isGain(s string) bool {
	return strings.Contains(s, "gain") || strings.Contains(s, "louder") ||
		strings.Contains(s, "quieter") || strings.Contains(s, "softer") ||
		strings.Contains(s, "turn up") || strings.Contains(s, "turn down") ||
		strings.Contains(s, "boost") || strings.Contains(s, "volume")
}

func parseGain(s string, arr *dawmodel.Arrangement) ctledit.BeckyEditBatch {
	track := findTrackID(s, arr)
	if track == "" {
		return noEdits(`name a track, e.g. "make the bass louder" or "set the lead gain to 0.8"`)
	}
	cur := currentGain(arr, track)

	// Explicit target: "set the X gain to 0.8" / "gain to 1.2" / "volume 0.5".
	if (strings.Contains(s, "gain") || strings.Contains(s, "volume")) &&
		(strings.Contains(s, " to ") || strings.Contains(s, "gain ") || strings.Contains(s, "volume ")) {
		if g, ok := firstFloat(s); ok {
			g = clamp(g, 0, 2)
			return batch(fmt.Sprintf("set %s gain to %.2f", track, g),
				ctledit.BeckyEdit{Op: ctledit.OpSetGain, Target: track, Gain: f64(g)})
		}
	}

	base := cur
	if base <= 0 {
		base = 1 // a silenced track still gets a sensible relative move
	}
	up := strings.Contains(s, "louder") || strings.Contains(s, "turn up") ||
		strings.Contains(s, "boost") || strings.Contains(s, "raise")
	down := strings.Contains(s, "quieter") || strings.Contains(s, "softer") ||
		strings.Contains(s, "turn down") || strings.Contains(s, "lower") ||
		strings.Contains(s, "reduce")
	switch {
	case up && !down:
		g := clamp(base*relGainStep, 0, 2)
		return batch(fmt.Sprintf("turn %s up (%.2f → %.2f)", track, cur, g),
			ctledit.BeckyEdit{Op: ctledit.OpSetGain, Target: track, Gain: f64(g)})
	case down && !up:
		g := clamp(base/relGainStep, 0, 2)
		return batch(fmt.Sprintf("turn %s down (%.2f → %.2f)", track, cur, g),
			ctledit.BeckyEdit{Op: ctledit.OpSetGain, Target: track, Gain: f64(g)})
	}
	return noEdits(`say louder or quieter (or "set ` + track + ` gain to 0.8")`)
}

// ─── arrangement grounding ─────────────────────────────────────────────────────

// findTrackID returns the track whose ID is mentioned in s (longest match wins, so
// "lead bass" prefers the longer ID). Empty when none is named.
func findTrackID(s string, arr *dawmodel.Arrangement) string {
	if arr == nil {
		return ""
	}
	best := ""
	for _, t := range arr.Tracks {
		id := strings.ToLower(t.ID)
		if id == "" {
			continue
		}
		if containsWord(s, id) && len(id) > len(best) {
			best = t.ID
		}
	}
	return best
}

func firstMIDITrackID(arr *dawmodel.Arrangement) string {
	if arr == nil {
		return ""
	}
	for _, t := range arr.Tracks {
		if t.Kind != dawmodel.KindAudio {
			return t.ID
		}
	}
	return ""
}

// currentGain returns the track's strip gain, or 1 when the track/strip is unknown.
func currentGain(arr *dawmodel.Arrangement, trackID string) float64 {
	if arr == nil {
		return 1
	}
	for _, t := range arr.Tracks {
		if t.ID == trackID {
			return t.Strip.Gain
		}
	}
	return 1
}

// ─── parsing + math helpers ────────────────────────────────────────────────────

func firstInt(s string) (int, bool) {
	m := intRe.FindString(s)
	if m == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(m, 64)
	if err != nil {
		return 0, false
	}
	return int(math.Round(f)), true
}

func firstFloat(s string) (float64, bool) {
	m := intRe.FindString(s)
	if m == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(m, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// containsWord reports whether sub appears in s on word boundaries (so "bass" does
// not match inside "bassoon").
func containsWord(s, sub string) bool {
	idx := 0
	for {
		i := strings.Index(s[idx:], sub)
		if i < 0 {
			return false
		}
		start := idx + i
		end := start + len(sub)
		leftOK := start == 0 || !isWordByte(s[start-1])
		rightOK := end == len(s) || !isWordByte(s[end])
		if leftOK && rightOK {
			return true
		}
		idx = start + 1
		if idx >= len(s) {
			return false
		}
	}
}

func isWordByte(b byte) bool {
	return b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func f64(v float64) *float64 { return &v }

// ─── batch constructors ────────────────────────────────────────────────────────

func batch(summary string, edits ...ctledit.BeckyEdit) ctledit.BeckyEditBatch {
	return ctledit.BeckyEditBatch{Summary: summary, Edits: edits}
}

func noEdits(summary string) ctledit.BeckyEditBatch {
	return ctledit.BeckyEditBatch{Summary: summary}
}
