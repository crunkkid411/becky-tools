package ctledit

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"becky-go/internal/beatgen"
	"becky-go/internal/dawmodel"
)

// phrase.go is the deterministic natural-language fallback for the canvas AI box.
// The full NL→batch step is a local model (the GPU boundary); this keyword parser
// makes the most common generative requests work WITHOUT a model — same as the
// keyword fallbacks in becky-drum/becky-wire. ParsePhrase turns a plain-English
// instruction into a BeckyEditBatch by inspecting the arrangement (so it can name
// the real drum track/lane). It returns ok=false when nothing matches, so the
// caller can fall through to model proposal or tool routing.
//
// Deterministic: the same phrase + arrangement yields the same batch (fixed seed).
func ParsePhrase(text string, a *dawmodel.Arrangement) (BeckyEditBatch, bool) {
	if a == nil {
		return BeckyEditBatch{}, false
	}
	t := strings.ToLower(strings.TrimSpace(text))
	if t == "" {
		return BeckyEditBatch{}, false
	}

	// Stem-aware layering: "add bass" / "add some chords" / "add a melody" / "lay
	// down a bassline". Works whenever an arrangement is loaded (the layer reads
	// whatever stems exist), so it's checked before the drum-clip requirement.
	if layer := addLayerWord(t); layer != "" {
		return single("add "+layer, BeckyEdit{
			Op: OpAddLayer, Layer: layer, Genre: firstGenreWord(t), Seed: seedFromText(t),
		}), true
	}

	// Transport + mixer phrases (don't need a drum clip): "set bpm to 128",
	// "mute the bass", "unmute the lead", "solo the drums".
	if bpm, ok := parseBPMPhrase(t); ok {
		return single(fmt.Sprintf("set tempo to %d", bpm), BeckyEdit{Op: OpSetTempo, BPM: bpm}), true
	}
	if ed, sum, ok := parseMuteSoloPhrase(t, a); ok {
		return single(sum, ed), true
	}

	trackID, _ := findDrumClipRef(a)
	if trackID == "" {
		return BeckyEditBatch{}, false // no drum clip to act on
	}

	// "four on the floor" (and variants) → euclidean kick, 4 pulses across 16.
	if containsAny(t, "four on the floor", "4 on the floor", "four-on-the-floor", "4 on floor") {
		return single("kick: four on the floor", BeckyEdit{
			Op: OpEuclidLane, Track: trackID, Lane: "kick", Pulses: 4,
		}), true
	}

	// "euclid kick 5" / "euclidean snare 3" → euclid_lane with parsed lane + pulses.
	if containsAny(t, "euclid", "euclidean") {
		lane := firstLaneWord(t)
		if lane == "" {
			lane = "kick"
		}
		pulses := firstInt(t)
		if pulses <= 0 {
			pulses = 4
		}
		return single("euclidean "+lane, BeckyEdit{
			Op: OpEuclidLane, Track: trackID, Lane: lane, Pulses: pulses,
		}), true
	}

	// A genre word, or a generate/randomize verb → regenerate the beat.
	genre := firstGenreWord(t)
	if genre != "" || containsAny(t, "randomi", "regenerate", "generate", "new beat", "make a beat", "fresh beat", "shuffle the beat") {
		seed := seedFromText(t)
		summary := "randomized the beat"
		if genre != "" {
			summary = "generated a " + genre + " beat"
		}
		return single(summary, BeckyEdit{
			Op: OpGenerateBeat, Track: trackID, Genre: genre, Seed: seed,
		}), true
	}

	return BeckyEditBatch{}, false
}

// addLayerWord detects an "add a <layer>" request and returns the canonical layer
// role (bass/chords/melody), or "". It only fires when the instruction is about
// ADDING (so "make the bass louder" doesn't match). "bassline" → bass, "guitar"/
// "lead" → melody (the melodic top line).
func addLayerWord(t string) string {
	if !containsAny(t, "add", "lay down", "put in", "give me a", "give it a", "write a", "write some") {
		return ""
	}
	switch {
	case containsAny(t, "bassline", "bass line", "bass"):
		return "bass"
	case containsAny(t, "chord", "harmony", "pad"):
		return "chords"
	case containsAny(t, "melody", "lead", "guitar", "riff", "topline", "top line"):
		return "melody"
	}
	return ""
}

var bpmRe = regexp.MustCompile(`(\d{2,3})\s*bpm|(?:bpm|tempo)\s*(?:to|=|:|of)?\s*(\d{2,3})`)

// parseBPMPhrase detects "set bpm to 128" / "tempo 140" / "128 bpm" → a tempo in
// the sane 40..250 range.
func parseBPMPhrase(t string) (int, bool) {
	if !strings.Contains(t, "bpm") && !strings.Contains(t, "tempo") {
		return 0, false
	}
	m := bpmRe.FindStringSubmatch(t)
	if m == nil {
		return 0, false
	}
	num := m[1]
	if num == "" {
		num = m[2]
	}
	n, err := strconv.Atoi(num)
	if err != nil || n < 40 || n > 250 {
		return 0, false
	}
	return n, true
}

// parseMuteSoloPhrase detects "mute/unmute/solo/unsolo the <track>" and resolves the
// track by id/name mentioned in the text.
func parseMuteSoloPhrase(t string, a *dawmodel.Arrangement) (BeckyEdit, string, bool) {
	var op string
	var flag bool
	switch {
	case strings.Contains(t, "unmute"):
		op, flag = OpMute, false
	case strings.Contains(t, "mute"):
		op, flag = OpMute, true
	case strings.Contains(t, "unsolo"), strings.Contains(t, "un-solo"):
		op, flag = OpSolo, false
	case strings.Contains(t, "solo"):
		op, flag = OpSolo, true
	default:
		return BeckyEdit{}, "", false
	}
	target := trackMentioned(t, a)
	if target == "" {
		return BeckyEdit{}, "", false
	}
	verb := strings.SplitN(strings.TrimSpace(t), " ", 2)[0]
	ed := BeckyEdit{Op: op, Target: target}
	if op == OpMute {
		ed.Muted = flag
	} else {
		ed.Soloed = flag
	}
	return ed, fmt.Sprintf("%s %s", verb, target), true
}

// trackMentioned returns the id of the first existing track named in the text.
func trackMentioned(t string, a *dawmodel.Arrangement) string {
	for _, tr := range a.Tracks {
		if id := strings.ToLower(tr.ID); id != "" && strings.Contains(t, id) {
			return tr.ID
		}
	}
	return ""
}

// single wraps one edit in a summarized batch.
func single(summary string, ed BeckyEdit) BeckyEditBatch {
	return BeckyEditBatch{Summary: summary, Edits: []BeckyEdit{ed}}
}

// findDrumClipRef returns the (track, clip) of the best drum candidate in a:
// a non-empty channel-9 clip, else a program -1 clip, else any non-empty MIDI
// clip. Mirrors becky-drum/becky-beat so the canvas resolves the same target.
func findDrumClipRef(a *dawmodel.Arrangement) (string, string) {
	var ch9, prog, nonEmpty [2]string
	for _, t := range a.Tracks {
		if t.Kind != "" && t.Kind != dawmodel.KindMIDI {
			continue
		}
		for _, c := range t.Clips {
			if len(c.Notes) == 0 {
				continue
			}
			if isDrumClip(c) && ch9[0] == "" {
				ch9 = [2]string{t.ID, c.Name}
			}
			if c.Program == -1 && prog[0] == "" {
				prog = [2]string{t.ID, c.Name}
			}
			if nonEmpty[0] == "" {
				nonEmpty = [2]string{t.ID, c.Name}
			}
		}
	}
	for _, cand := range [][2]string{ch9, prog, nonEmpty} {
		if cand[0] != "" {
			return cand[0], cand[1]
		}
	}
	return "", ""
}

// isDrumClip reports whether a clip is GM percussion: its own channel is 9, or
// any of its notes are on channel 9. Checking the notes too makes detection work
// for clips loaded from a bare SMF (where the channel lives on the notes, not the
// clip header) as well as becky-authored clips (where ApplyDrumGrid stamps the
// clip channel).
func isDrumClip(c dawmodel.Clip) bool {
	if c.Channel == 9 {
		return true
	}
	for _, n := range c.Notes {
		if n.Ch == 9 {
			return true
		}
	}
	return false
}

// laneWords are the drum lane names ParsePhrase recognizes in an instruction.
var laneWords = []string{"kick", "snare", "clap", "ohat", "hat", "hihat", "rim", "tom", "ride", "crash"}

// firstLaneWord returns the first lane name mentioned in t, or "".
func firstLaneWord(t string) string {
	best := ""
	bestIdx := -1
	for _, w := range laneWords {
		if i := strings.Index(t, w); i >= 0 && (bestIdx < 0 || i < bestIdx) {
			best, bestIdx = w, i
		}
	}
	if best == "hihat" {
		return "hat"
	}
	return best
}

// firstGenreWord returns the first known beatgen genre mentioned in t, or "".
func firstGenreWord(t string) string {
	best := ""
	bestIdx := -1
	for _, g := range beatgen.GenreNames() {
		if g == "straight" {
			continue // not a user-typed genre word
		}
		if i := strings.Index(t, g); i >= 0 && (bestIdx < 0 || i < bestIdx) {
			best, bestIdx = g, i
		}
	}
	return best
}

// firstInt returns the first non-negative integer embedded in t, or -1.
func firstInt(t string) int {
	cur := ""
	for _, r := range t {
		if r >= '0' && r <= '9' {
			cur += string(r)
			continue
		}
		if cur != "" {
			break
		}
	}
	if cur == "" {
		return -1
	}
	n, err := strconv.Atoi(cur)
	if err != nil {
		return -1
	}
	return n
}

// seedFromText derives a stable seed from the instruction so the same phrase
// reproduces the same beat, while "again"/"another" nudge it for variety.
func seedFromText(t string) int64 {
	var sum int64 = 42
	for _, r := range t {
		sum = sum*31 + int64(r)
	}
	if sum < 0 {
		sum = -sum
	}
	return sum
}

// containsAny reports whether t contains any of subs.
func containsAny(t string, subs ...string) bool {
	for _, s := range subs {
		if strings.Contains(t, s) {
			return true
		}
	}
	return false
}
