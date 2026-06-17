package studio

// parse.go is the DETERMINISTIC keyword/grammar parser — the offline, testable
// core. It recognises the verbs (sidechain/duck, route/send, insert-chain /
// "set up" / "my usual", use <vst>, gain stage) and the nouns (kick, snare,
// bass, 808, lead, rhythm guitar, vox/vocal, synth, drum bus, master), resolving
// each noun to a real node/bus id in the loaded Project.

import (
	"regexp"
	"strconv"
	"strings"

	"becky-go/internal/music"
)

// DeterministicParser is the always-available offline parser. It holds no state,
// so the zero value is ready to use.
type DeterministicParser struct{}

// Parse implements Parser. It never returns a non-nil error (the deterministic
// path cannot "fail to run"); an unrecognised instruction yields
// Intent{Action: Unknown} with a friendly Note.
func (DeterministicParser) Parse(instruction string, proj music.Project) (Intent, error) {
	norm := normalize(instruction)
	if norm == "" {
		return unknown(instruction, "empty instruction"), nil
	}

	switch {
	case isSidechain(norm):
		return parseSidechain(instruction, norm, proj), nil
	case isSetVST(norm):
		return parseSetVST(instruction, norm, proj), nil
	case isGainStage(norm):
		return parseGainStage(instruction, norm, proj), nil
	case isRoute(norm):
		return parseRoute(instruction, norm, proj), nil
	case isInsertChain(norm):
		return parseInsertChain(instruction, norm, proj), nil
	}
	return unknown(instruction, "no studio verb recognised"), nil
}

// ─── verb detection ───────────────────────────────────────────────────────────

func isSidechain(s string) bool {
	return containsWord(s, "sidechain") || containsWord(s, "side-chain") ||
		containsWord(s, "duck") || containsWord(s, "ducks") || containsWord(s, "ducking")
}

func isRoute(s string) bool {
	return containsWord(s, "route") || containsWord(s, "send") || containsWord(s, "bus") &&
		(containsWord(s, "to") || containsWord(s, "into"))
}

func isSetVST(s string) bool {
	return containsWord(s, "use") || containsWord(s, "load") || containsWord(s, "put") &&
		!isInsertChainPhrase(s)
}

func isGainStage(s string) bool {
	return strings.Contains(s, "gain stage") || strings.Contains(s, "gain-stage") ||
		(containsWord(s, "gain") && containsWord(s, "stage")) ||
		(containsWord(s, "trim") && hasDBValue(s)) ||
		(containsWord(s, "gain") && hasDBValue(s))
}

// isInsertChainPhrase matches the "standard chain on a bus" intent: "my usual",
// "set up", "standard chain", "the chain on".
func isInsertChainPhrase(s string) bool {
	return strings.Contains(s, "my usual") || strings.Contains(s, "usual chain") ||
		strings.Contains(s, "set up") || strings.Contains(s, "setup") ||
		strings.Contains(s, "standard chain") || strings.Contains(s, "the chain") ||
		strings.Contains(s, "fx chain") || strings.Contains(s, "my chain")
}

func isInsertChain(s string) bool { return isInsertChainPhrase(s) }

// ─── per-verb parsers ─────────────────────────────────────────────────────────

// parseSidechain handles "sidechain the bass to the kick" and "duck the synths
// under the vocal". The DETECTOR is whatever the target ducks UNDER/TO; the bus
// being ducked is the first noun. Convention: "sidechain X to Y" => Y ducks X
// (Y is the detector/kick), matching becky-compose's kick->bass duck edges.
func parseSidechain(raw, norm string, proj music.Project) Intent {
	in := Intent{Action: ActionSidechain}

	// Split on the linking preposition: "to"/"under"/"off"/"from" separates the
	// thing-being-ducked (left) from the detector/trigger (right).
	left, right, ok := splitOnPrep(norm, []string{" under ", " to ", " off the ", " off ", " from "})
	if !ok {
		return unknown(raw, "sidechain needs two parts, e.g. 'sidechain the bass to the kick'")
	}

	duckedWord, duckedID, dOK := resolveBus(left, proj)
	detWord, detID, detOK := resolveDetector(right, proj)
	if !dOK || !detOK {
		return unknown(raw, "couldn't resolve both the source and the trigger for the sidechain")
	}

	in.Source = detID // the detector tap (kick) is the edge source
	in.Target = duckedID
	in.SourceWord = detWord
	in.TargetWord = duckedWord
	if isLowBand(duckedWord, duckedID) {
		in.Band = "low"
	}
	in.Note = "duck " + duckedWord + " under the " + detWord
	return in
}

// parseRoute handles "route the lead guitar to the guitar bus".
func parseRoute(raw, norm string, proj music.Project) Intent {
	in := Intent{Action: ActionRoute}
	left, right, ok := splitOnPrep(norm, []string{" to the ", " to ", " into the ", " into "})
	if !ok {
		return unknown(raw, "route needs a source and a destination bus, e.g. 'route the lead guitar to the guitar bus'")
	}
	trackWord, trackID, tOK := resolveTrack(left, proj)
	busWord, busID, bOK := resolveBus(right, proj)
	if !tOK || !bOK {
		return unknown(raw, "couldn't resolve the track or the destination bus")
	}
	// Disambiguate a generic "guitar bus": route a SPECIFIC guitar (lead/rhythm)
	// to ITS OWN guitar bus rather than the default rhythm bus. "route the lead
	// guitar to the guitar bus" => bus.gtrLead, not bus.gtrRhythm.
	if isGenericGuitarBus(right) {
		if srcBus, ok := guitarBusForTrack(left); ok {
			busID = srcBus
		}
	}
	in.Source = trackID
	in.Target = busID
	in.SourceWord = trackWord
	in.TargetWord = busWord
	in.Note = "route " + trackWord + " to the " + busWord + " bus"
	return in
}

// parseInsertChain handles "put my usual chain on the drum bus" / "set up the drum bus".
func parseInsertChain(raw, norm string, proj music.Project) Intent {
	in := Intent{Action: ActionInsertChain}
	busWord, busID, ok := resolveBus(norm, proj)
	if !ok {
		return unknown(raw, "couldn't tell which bus to set up")
	}
	in.Target = busID
	in.TargetWord = busWord
	in.Note = "insert the standard FX chain on the " + busWord + " bus"
	return in
}

// parseSetVST handles "use Odin II on the lead".
func parseSetVST(raw, norm string, proj music.Project) Intent {
	in := Intent{Action: ActionSetVST}
	// The plugin name sits between the verb and the "on <bus>" clause.
	vst := extractVSTName(raw)
	busWord, busID, ok := resolveBus(afterOn(norm), proj)
	if vst == "" || !ok {
		return unknown(raw, "couldn't tell which plugin or which bus, e.g. 'use Odin II on the lead'")
	}
	in.VST = vst
	in.Target = busID
	in.TargetWord = busWord
	in.Note = "use " + vst + " on the " + busWord + " bus"
	return in
}

// parseGainStage handles "gain stage the kick to -7".
func parseGainStage(raw, norm string, proj music.Project) Intent {
	in := Intent{Action: ActionSetGain}
	db, hasDB := extractDB(norm)
	if !hasDB {
		return unknown(raw, "gain staging needs a target level, e.g. 'gain stage the kick to -7'")
	}
	// The node is whatever noun appears before the " to <db>" clause.
	target := norm
	if i := strings.LastIndex(norm, " to "); i >= 0 {
		target = norm[:i]
	}
	word, id, ok := resolveNode(target, proj)
	if !ok {
		return unknown(raw, "couldn't tell what to gain-stage")
	}
	in.Target = id
	in.TargetWord = word
	in.GainDB = db
	in.HasGain = true
	in.Note = "gain stage " + word + " to " + formatDB(db) + " dB"
	return in
}

// ─── noun resolution ──────────────────────────────────────────────────────────

// nounAlias maps a spoken noun to a canonical node/bus. Order matters: longer,
// more specific phrases are matched first (see resolveAlias).
type nounAlias struct {
	phrases  []string // spoken forms (lowercase), longest-first within the table
	bus      string   // canonical bus id (mixplan / compose ids)
	track    string   // track id as it appears in Project.Tracks (lowercase)
	detector string   // detector node id for sidechain (kick/snare via src.drums.*)
	display  string   // human label for summaries
	lowBand  bool     // true => band-split low duck when this is the ducked source
}

// aliasTable is the deterministic noun dictionary. The ids line up with
// becky-compose's project (src.drums.kick, bus.808) and mixplan's bus constants.
var aliasTable = []nounAlias{
	{phrases: []string{"rhythm guitar", "rhythm gtr", "rhythm"}, bus: "bus.gtrRhythm", track: "chords", display: "rhythm guitar"},
	{phrases: []string{"lead guitar", "lead gtr", "lead"}, bus: "bus.gtrLead", track: "lead", display: "lead"},
	{phrases: []string{"drum bus", "drums", "drum"}, bus: "bus.drums", track: "drums", display: "drums"},
	{phrases: []string{"the kick", "kick drum", "kick"}, bus: "bus.kick", track: "drums", detector: "src.drums.kick", display: "kick"},
	{phrases: []string{"snare drum", "snare"}, bus: "bus.snare", track: "drums", detector: "src.drums.snare", display: "snare"},
	{phrases: []string{"808s", "808", "sub"}, bus: "bus.808", track: "bass", display: "808", lowBand: true},
	{phrases: []string{"bass guitar", "bass"}, bus: "bus.808", track: "bass", display: "bass", lowBand: true},
	{phrases: []string{"vocals", "vocal", "vox"}, bus: "bus.vox", track: "vox", detector: "src.vox", display: "vocal"},
	{phrases: []string{"synths", "synth", "pads", "pad"}, bus: "bus.synth", track: "synth", display: "synths"},
	{phrases: []string{"guitar bus", "guitar"}, bus: "bus.gtrRhythm", track: "chords", display: "guitar"},
	{phrases: []string{"master bus", "master"}, bus: "bus.master", display: "master"},
}

// matchAlias finds the alias whose longest phrase appears in s. Phrases across the
// whole table are scored by length so "lead guitar" beats a bare "guitar".
func matchAlias(s string) (nounAlias, bool) {
	var best nounAlias
	bestLen := 0
	found := false
	for _, a := range aliasTable {
		for _, p := range a.phrases {
			if containsWord(s, p) && len(p) > bestLen {
				best, bestLen, found = a, len(p), true
			}
		}
	}
	return best, found
}

// resolveBus resolves a phrase to a bus id (+ display + canonical word).
func resolveBus(s string, proj music.Project) (word, id string, ok bool) {
	a, found := matchAlias(s)
	if !found {
		return "", "", false
	}
	bus := a.bus
	// Honour a bus that already exists in the project verbatim if present.
	if hasBus(proj, bus) {
		return a.display, bus, true
	}
	return a.display, bus, true
}

// resolveTrack resolves a phrase to a track node id (src.<track>).
func resolveTrack(s string, proj music.Project) (word, id string, ok bool) {
	a, found := matchAlias(s)
	if !found {
		return "", "", false
	}
	node := "src." + a.track
	// Prefer the project's declared node for that track if it exists.
	for _, t := range proj.Tracks {
		if strings.EqualFold(t.ID, a.track) && t.Node != "" {
			node = t.Node
			break
		}
	}
	return a.display, node, true
}

// resolveDetector resolves a phrase to a sidechain detector node (kick/snare tap,
// or a track src for non-drum triggers like the vocal).
func resolveDetector(s string, proj music.Project) (word, id string, ok bool) {
	a, found := matchAlias(s)
	if !found {
		return "", "", false
	}
	if a.detector != "" {
		return a.display, a.detector, true
	}
	// Non-drum trigger (e.g. vocal): use its source node.
	return resolveTrack(s, proj)
}

// resolveNode resolves a phrase to either a track node or a bus (gain staging can
// target either). Tracks win — gain staging is usually per-source.
func resolveNode(s string, proj music.Project) (word, id string, ok bool) {
	if w, id, ok := resolveTrack(s, proj); ok {
		return w, id, ok
	}
	return resolveBus(s, proj)
}

// isGenericGuitarBus reports whether the destination phrase is a bare "guitar"/
// "guitar bus" with no lead/rhythm qualifier — the ambiguous case to disambiguate.
func isGenericGuitarBus(s string) bool {
	if containsWord(s, "lead") || containsWord(s, "rhythm") {
		return false
	}
	return containsWord(s, "guitar")
}

// guitarBusForTrack returns the specific guitar bus implied by the source phrase
// ("lead guitar" -> bus.gtrLead, "rhythm guitar" -> bus.gtrRhythm).
func guitarBusForTrack(s string) (string, bool) {
	switch {
	case containsWord(s, "lead"):
		return "bus.gtrLead", true
	case containsWord(s, "rhythm"):
		return "bus.gtrRhythm", true
	}
	return "", false
}

// isLowBand reports whether a ducked source is low-end (808/bass), so the duck is
// band-split (lows only) — the metalcore/JST default.
func isLowBand(word, id string) bool {
	if id == "bus.808" || id == "bus.bass" {
		return true
	}
	w := strings.ToLower(word)
	return w == "808" || strings.Contains(w, "bass")
}

// ─── small helpers ────────────────────────────────────────────────────────────

var spaceRe = regexp.MustCompile(`\s+`)

// normalize lowercases, collapses whitespace, and trims punctuation noise.
func normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, ",", " ")
	s = strings.ReplaceAll(s, ".", " ")
	s = spaceRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// containsWord reports whether sub appears in s on word boundaries (so "bass"
// does not match inside "bassoon", and "to" does not match "tone").
func containsWord(s, sub string) bool {
	if !strings.Contains(s, sub) {
		return false
	}
	// Word-boundary check around each occurrence.
	idx := 0
	for {
		j := strings.Index(s[idx:], sub)
		if j < 0 {
			return false
		}
		start := idx + j
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
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// splitOnPrep splits s on the first matching preposition phrase, returning the
// left and right halves. Phrases are tried in order (longest/most-specific first).
func splitOnPrep(s string, preps []string) (left, right string, ok bool) {
	for _, p := range preps {
		if i := strings.Index(s, p); i >= 0 {
			return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+len(p):]), true
		}
	}
	return "", "", false
}

// afterOn returns the substring after " on " (the bus clause in "use X on the Y").
func afterOn(s string) string {
	if i := strings.LastIndex(s, " on "); i >= 0 {
		return strings.TrimSpace(s[i+4:])
	}
	return s
}

var dbRe = regexp.MustCompile(`-?\d+(?:\.\d+)?`)

// hasDBValue reports whether s contains a numeric (dB) value.
func hasDBValue(s string) bool { return dbRe.MatchString(s) }

// extractDB pulls the LAST numeric value from s (the target level in
// "gain stage the kick to -7").
func extractDB(s string) (float64, bool) {
	matches := dbRe.FindAllString(s, -1)
	if len(matches) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(matches[len(matches)-1], 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// formatDB renders a dB value without a trailing ".0" for whole numbers.
func formatDB(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// vstAliases maps spoken plugin names to their canonical registered name.
var vstAliases = map[string]string{
	"odin ii":  "The Odin II",
	"odin 2":   "The Odin II",
	"odin":     "The Odin II",
	"odin two": "The Odin II",
}

// extractVSTName pulls the plugin name out of a "use <name> on the <bus>" raw
// instruction. It honours the alias table, else returns the words between the
// verb and the "on" clause Title-Cased.
func extractVSTName(raw string) string {
	norm := normalize(raw)
	// Try known aliases first (longest match).
	best := ""
	for alias, canon := range vstAliases {
		if containsWord(norm, alias) && len(alias) > len(best) {
			best = canon
		}
	}
	if best != "" {
		return best
	}
	// Fall back: words after the verb, before " on ".
	verbs := []string{"use ", "load ", "put "}
	body := norm
	for _, v := range verbs {
		if i := strings.Index(norm, v); i >= 0 {
			body = norm[i+len(v):]
			break
		}
	}
	if i := strings.Index(body, " on "); i >= 0 {
		body = body[:i]
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	return titleCase(body)
}

// titleCase upper-cases the first letter of each word (deterministic, ASCII).
func titleCase(s string) string {
	parts := strings.Fields(s)
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

// hasBus reports whether the project already declares a bus with this id.
func hasBus(proj music.Project, id string) bool {
	for _, b := range proj.Buses {
		if b.ID == id {
			return true
		}
	}
	return false
}

// unknown builds the degrade Intent with a friendly note.
func unknown(raw, why string) Intent {
	return Intent{
		Action: ActionUnknown,
		Note:   "couldn't understand \"" + strings.TrimSpace(raw) + "\" (" + why + ") — try e.g. \"sidechain the bass to the kick\"",
	}
}
