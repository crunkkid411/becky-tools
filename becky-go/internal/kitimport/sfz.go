// Package kitimport parses real, open-format kit/preset files (SFZ, DecentSampler)
// into the becky multisampling model in internal/sampler.
//
// Both parsers are line/XML-oriented, pure Go, deterministic, and degrade-never-
// crash: a missing sample file is flagged (Variant.Missing) but the region is kept;
// unknown opcodes/elements are skipped, not fatal. SFZ opcode semantics follow
// https://sfzformat.com/ (cited inline where non-obvious).
package kitimport

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"becky-go/internal/pathx"
	"becky-go/internal/sampler"
)

// region is the flattened opcode set for one <region>, after inheriting the
// enclosing <group> and <global> opcodes (region overrides group overrides
// global — the standard SFZ scoping rule, https://sfzformat.com/headers/).
type region struct {
	opcodes map[string]string
}

// ParseSFZResult holds everything parsed from one .sfz file. A single file may
// describe one drum (one key) or a whole kit (many keys); we expose both shapes.
type ParseSFZResult struct {
	Sounds []sampler.Sound // one Sound per distinct key, sorted by key
	Notes  []string        // non-fatal observations (unknown opcodes, #include, missing samples)
}

// ParseSFZ reads an .sfz file and groups its regions into Sounds — one Sound per
// distinct MIDI key, layered by velocity, with round-robin variants ordered by
// seq_position. It never returns a partial-file failure for a missing sample or
// an unknown opcode; only an unreadable file is an error.
//
// Preprocessing (P2-9):
//   - `#define $VAR value` macros are resolved in subsequent text.
//   - `#include "file.sfz"` inlines the referenced file (depth-limited to 8 levels).
//   - `<control> default_path=...` sets a prefix applied to all `sample=` values
//     in regions that follow it (per sfzformat.com/headers/control/).
func ParseSFZ(path string) (ParseSFZResult, error) {
	baseDir := filepath.Dir(path)
	var notes []string

	// Pre-process: resolve #define macros and #include directives, producing a flat
	// list of (line, fileDir) pairs for the main parser.
	lines, prepNotes, err := preprocessSFZ(path, baseDir, map[string]bool{}, 0)
	if err != nil {
		return ParseSFZResult{}, err
	}
	notes = append(notes, prepNotes...)

	// Collect #define macros from the preprocessed lines and remove them from the
	// line stream. Macros defined in included files are already substituted by
	// preprocessSFZ.
	defines := map[string]string{}
	var filteredLines []sfzLine
	for _, l := range lines {
		text := strings.TrimSpace(l.text)
		if strings.HasPrefix(text, "#define") {
			// #define $VAR value
			parts := strings.Fields(text)
			if len(parts) == 3 {
				defines[parts[1]] = parts[2]
				notes = append(notes, "resolved #define: "+parts[1]+" = "+parts[2])
			} else {
				notes = append(notes, "skipped malformed #define: "+text)
			}
			continue
		}
		filteredLines = append(filteredLines, l)
	}

	// SFZ scoping: opcodes accumulate at <global>, then <group>, then <region>;
	// the most specific scope wins at flush (region overrides group overrides
	// global — https://sfzformat.com/headers/). `scope` tracks where a bare
	// opcode=value belongs. An unmodeled header (<control>, <effect>, …) routes
	// opcodes to scopeIgnore so they never pollute a real region.
	const (
		scopeGlobal = iota
		scopeGroup
		scopeRegion
		scopeIgnore
		scopeControl
	)
	global := map[string]string{}
	group := map[string]string{}
	var current map[string]string // the active region's opcode map, nil until <region>
	scope := scopeGlobal
	var regions []region
	var defaultPath string // <control> default_path prefix

	flushRegion := func() {
		if current != nil {
			merged := mergeOpcodes(global, group, current)
			// Apply default_path to sample= if set and sample path is not absolute.
			if defaultPath != "" {
				if s, ok := merged["sample"]; ok && !isAbsolutePath(s) {
					merged["sample"] = defaultPath + s
				}
			}
			regions = append(regions, region{opcodes: merged})
			current = nil
		}
	}

	for _, l := range filteredLines {
		text := stripSFZComment(l.text)
		// Apply #define substitutions to this line.
		for k, v := range defines {
			text = strings.ReplaceAll(text, k, v)
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		tokens := tokenizeSFZ(text)
		for _, tok := range tokens {
			switch low := strings.ToLower(tok); {
			case low == "<global>":
				flushRegion()
				global = map[string]string{}
				group = map[string]string{}
				scope = scopeGlobal
			case low == "<group>":
				flushRegion()
				group = map[string]string{}
				scope = scopeGroup
			case low == "<region>":
				flushRegion()
				current = map[string]string{}
				scope = scopeRegion
			case low == "<control>":
				// <control> sets default_path and other session-level defaults.
				// Opcodes inside <control> are not region opcodes.
				flushRegion()
				scope = scopeControl
			case strings.HasPrefix(tok, "<") && strings.HasSuffix(tok, ">"):
				// <curve>, <effect>, etc. — not modeled; route to ignore.
				flushRegion()
				notes = append(notes, "skipped header "+low)
				scope = scopeIgnore
			default:
				key, val, ok := splitOpcode(tok)
				if !ok {
					continue
				}
				switch scope {
				case scopeRegion:
					current[key] = val
				case scopeGroup:
					group[key] = val
				case scopeGlobal:
					global[key] = val
				case scopeControl:
					if key == "default_path" {
						// Normalise to forward slashes; join with baseDir if relative.
						dp := strings.ReplaceAll(val, `\`, "/")
						if !filepath.IsAbs(dp) {
							dp = filepath.Join(l.dir, dp) + "/"
						}
						defaultPath = dp
					}
				default: // scopeIgnore
				}
			}
		}
	}
	flushRegion()

	sounds, missingNotes := regionsToSounds(regions, baseDir)
	notes = append(notes, missingNotes...)
	return ParseSFZResult{Sounds: sounds, Notes: notes}, nil
}

// sfzLine is one pre-processed text line with its source-file directory (needed so
// relative sample= paths resolve against the right base).
type sfzLine struct {
	text string
	dir  string // directory of the file this line came from
}

// maxIncludeDepth limits recursive #include to prevent infinite loops.
const maxIncludeDepth = 8

// preprocessSFZ reads path, resolves #include directives inline (depth-limited),
// and returns a flat list of sfzLine values. Macro definitions (#define) are left
// in the stream for the caller to handle (so macros defined in included files can
// be used after the include). Errors from an unreadable #include are noted but not
// fatal — the include is skipped.
func preprocessSFZ(path, dir string, seen map[string]bool, depth int) ([]sfzLine, []string, error) {
	if depth > maxIncludeDepth {
		return nil, []string{fmt.Sprintf("skipped #include (depth limit %d): %s", maxIncludeDepth, path)}, nil
	}
	if seen[path] {
		return nil, []string{"skipped circular #include: " + path}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	seen[path] = true
	defer func() { delete(seen, path) }()

	var lines []sfzLine
	var notes []string

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		text := strings.TrimSpace(stripSFZComment(sc.Text()))
		if text == "" {
			continue
		}
		if strings.HasPrefix(text, "#include") {
			// #include "file.sfz"  or  #include <file.sfz>
			inner := extractIncludePath(text)
			if inner == "" {
				notes = append(notes, "skipped malformed #include: "+text)
				continue
			}
			incPath := inner
			if !filepath.IsAbs(inner) {
				incPath = filepath.Join(dir, filepath.FromSlash(inner))
			}
			incLines, incNotes, incErr := preprocessSFZ(incPath, filepath.Dir(incPath), seen, depth+1)
			if incErr != nil {
				notes = append(notes, "skipped unreadable #include "+inner+": "+incErr.Error())
			} else {
				lines = append(lines, incLines...)
				notes = append(notes, incNotes...)
			}
			continue
		}
		lines = append(lines, sfzLine{text: sc.Text(), dir: dir})
	}
	if err := sc.Err(); err != nil {
		return lines, notes, err
	}
	return lines, notes, nil
}

// extractIncludePath pulls the filename out of a #include "file" or #include <file>
// directive. Returns "" on a malformed line.
func extractIncludePath(line string) string {
	line = strings.TrimPrefix(line, "#include")
	line = strings.TrimSpace(line)
	if len(line) < 2 {
		return ""
	}
	var close byte
	switch line[0] {
	case '"':
		close = '"'
	case '<':
		close = '>'
	default:
		return ""
	}
	end := strings.IndexByte(line[1:], close)
	if end < 0 {
		return ""
	}
	return line[1 : end+1]
}

// mergeOpcodes layers global <- group <- region so the most specific wins.
func mergeOpcodes(global, group, region map[string]string) map[string]string {
	out := make(map[string]string, len(global)+len(group)+len(region))
	for k, v := range global {
		out[k] = v
	}
	for k, v := range group {
		out[k] = v
	}
	for k, v := range region {
		out[k] = v
	}
	return out
}

// stripSFZComment removes a `//` line comment (SFZ uses C++-style line comments).
func stripSFZComment(s string) string {
	if i := strings.Index(s, "//"); i >= 0 {
		return s[:i]
	}
	return s
}

// tokenizeSFZ splits a line into header tags (<...>) and opcode=value tokens.
//
// SFZ's one genuine ambiguity: the `sample` opcode's value is a filename that MAY
// contain spaces, yet multiple opcodes commonly share a line
// (e.g. `sample=my kick.wav key=36`). Real SFZ parsers resolve this the same way
// (per sfzformat.com/syntax): a value runs until the start of the NEXT `opcode=`
// token. So for `sample=`, we consume up to the next ` <ident>=` boundary (or a
// header tag), not just the next space — which preserves spaces inside filenames
// while still terminating before the following opcode.
func tokenizeSFZ(line string) []string {
	var out []string
	rest := line
	for rest != "" {
		rest = strings.TrimLeft(rest, " \t")
		if rest == "" {
			break
		}
		if rest[0] == '<' {
			end := strings.IndexByte(rest, '>')
			if end < 0 {
				out = append(out, rest)
				break
			}
			out = append(out, rest[:end+1])
			rest = rest[end+1:]
			continue
		}
		// Find the opcode key up to '='.
		eq := strings.IndexByte(rest, '=')
		if eq < 0 {
			break // trailing junk; ignore
		}
		key := strings.TrimSpace(rest[:eq])
		afterEq := rest[eq+1:]
		if strings.EqualFold(key, "sample") {
			// Consume until the next opcode boundary or a header tag.
			cut := nextOpcodeBoundary(afterEq)
			out = append(out, key+"="+strings.TrimSpace(afterEq[:cut]))
			rest = afterEq[cut:]
			continue
		}
		// Normal opcode: value runs until the next whitespace.
		val := afterEq
		if sp := strings.IndexAny(val, " \t"); sp >= 0 {
			out = append(out, key+"="+val[:sp])
			rest = val[sp:]
		} else {
			out = append(out, key+"="+val)
			rest = ""
		}
	}
	return out
}

// nextOpcodeBoundary returns the index in s where the next `opcode=` token (or a
// `<` header) begins, scanning past whitespace-separated words. A word followed by
// '=' marks the next opcode; everything before it belongs to the current value.
// Returns len(s) when the whole string is one value.
func nextOpcodeBoundary(s string) int {
	i := 0
	for i < len(s) {
		// skip leading spaces of a candidate word, remembering where it started
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= len(s) {
			return len(s)
		}
		if s[i] == '<' {
			return i
		}
		wordStart := i
		for i < len(s) && s[i] != ' ' && s[i] != '\t' {
			if s[i] == '=' && isOpcodeIdent(s[wordStart:i]) {
				return wordStart
			}
			i++
		}
	}
	return len(s)
}

// isOpcodeIdent reports whether w looks like an SFZ opcode name (letters, digits,
// underscore; starts with a letter). Filenames with no '=' never reach here.
func isOpcodeIdent(w string) bool {
	if w == "" {
		return false
	}
	c := w[0]
	if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
		return false
	}
	for i := 1; i < len(w); i++ {
		c := w[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

func splitOpcode(tok string) (key, val string, ok bool) {
	eq := strings.IndexByte(tok, '=')
	if eq < 0 {
		return "", "", false
	}
	key = strings.ToLower(strings.TrimSpace(tok[:eq]))
	val = strings.TrimSpace(tok[eq+1:])
	if key == "" {
		return "", "", false
	}
	return key, val, true
}

// keyGroup collects the regions that share a MIDI key, before fusing to a Sound.
type keyGroup struct {
	key     int
	regions []region
}

// regionsToSounds groups regions by key, then by velocity layer, ordering round-
// robin variants by seq_position. Returns Sounds sorted by key for determinism.
func regionsToSounds(regions []region, baseDir string) ([]sampler.Sound, []string) {
	var notes []string
	byKey := map[int]*keyGroup{}
	var order []int
	for _, r := range regions {
		k := opcodeKey(r.opcodes)
		g, ok := byKey[k]
		if !ok {
			g = &keyGroup{key: k}
			byKey[k] = g
			order = append(order, k)
		}
		g.regions = append(g.regions, r)
	}
	sort.Ints(order)

	var sounds []sampler.Sound
	for _, k := range order {
		g := byKey[k]
		snd, n := buildSound(g, baseDir)
		notes = append(notes, n...)
		sounds = append(sounds, snd.Normalize())
	}
	return sounds, notes
}

// buildSound fuses one key's regions into a Sound: velocity layers (grouped by
// lovel/hivel) each holding seq-ordered round-robin variants.
func buildSound(g *keyGroup, baseDir string) (sampler.Sound, []string) {
	var notes []string

	type velKey struct{ lo, hi int }
	layerMap := map[velKey]*sampler.Layer{}
	var layerOrder []velKey

	snd := sampler.Sound{}
	keySet := false

	for _, r := range g.regions {
		op := r.opcodes
		lo := opInt(op, "lovel", 1)
		hi := opInt(op, "hivel", 127)
		vk := velKey{lo, hi}
		layer, ok := layerMap[vk]
		if !ok {
			layer = &sampler.Layer{VelLo: lo, VelHi: hi, RRMode: sampler.Sequential}
			if _, hasRand := op["lorand"]; hasRand {
				layer.RRMode = sampler.Random
			}
			layerMap[vk] = layer
			layerOrder = append(layerOrder, vk)
		}

		v, missing := regionToVariant(op, baseDir)
		if missing != "" {
			notes = append(notes, missing)
		}
		// Insert respecting seq_position (1-based); regions without it append.
		seqPos := opInt(op, "seq_position", 0)
		if seqPos > 0 {
			layer.RoundRobin = insertAtSeq(layer.RoundRobin, v, seqPos)
		} else {
			layer.RoundRobin = append(layer.RoundRobin, v)
		}

		// Sound-level opcodes (last region wins; they're typically on <group>).
		if grp, ok := op["group"]; ok {
			snd.ChokeGroup = atoiSafe(grp, snd.ChokeGroup)
		}
		if off, ok := op["off_by"]; ok {
			if n := atoiSafe(off, -1); n >= 0 && !containsInt(snd.OffBy, n) {
				snd.OffBy = append(snd.OffBy, n)
			}
		}
		if lm, ok := op["loop_mode"]; ok && strings.EqualFold(strings.TrimSpace(lm), "one_shot") {
			snd.OneShot = true
		}
		if !keySet {
			if _, hasKey := op["key"]; hasKey {
				snd.Root = opcodeKey(op)
				keySet = true
			}
		}
	}

	// Sort velocity layers low->high for deterministic, predictable lookup.
	sort.Slice(layerOrder, func(i, j int) bool {
		if layerOrder[i].lo != layerOrder[j].lo {
			return layerOrder[i].lo < layerOrder[j].lo
		}
		return layerOrder[i].hi < layerOrder[j].hi
	})
	for _, vk := range layerOrder {
		snd.Layers = append(snd.Layers, *layerMap[vk])
	}

	// P2-8: warn about velocity gaps (1..127 range not fully covered).
	// Convert layerOrder to the velRange slice the gap checker expects.
	var velRanges []velRange
	for _, vk := range layerOrder {
		velRanges = append(velRanges, velRange{lo: vk.lo, hi: vk.hi})
	}
	notes = append(notes, velocityGapNotes(velRanges, snd.Name)...)

	// Name the Sound from the first sample's basename (separator-agnostic).
	if len(snd.Layers) > 0 && len(snd.Layers[0].RoundRobin) > 0 {
		snd.Name = stripExt(pathx.Base(snd.Layers[0].RoundRobin[0].SamplePath))
	}
	return snd, notes
}

// velRange is a [lo, hi] velocity range for gap checking.
type velRange struct{ lo, hi int }

// velocityGapNotes checks whether the sorted velocity layers cover the full 1..127
// range without gaps or overlaps, and returns a human-readable note for each issue.
// A sound with only one layer (the common one-shot case) gets no warning.
func velocityGapNotes(layers []velRange, soundName string) []string {
	if len(layers) <= 1 {
		return nil
	}
	var ns []string
	prefix := "velocity"
	if soundName != "" {
		prefix = soundName + " velocity"
	}
	if layers[0].lo > 1 {
		ns = append(ns, fmt.Sprintf("%s gap: no layer covers 1..%d", prefix, layers[0].lo-1))
	}
	for i := 1; i < len(layers); i++ {
		prev := layers[i-1]
		cur := layers[i]
		if cur.lo > prev.hi+1 {
			ns = append(ns, fmt.Sprintf("%s gap: no layer covers %d..%d", prefix, prev.hi+1, cur.lo-1))
		} else if cur.lo <= prev.hi {
			ns = append(ns, fmt.Sprintf("%s overlap: layers %d..%d and %d..%d overlap",
				prefix, prev.lo, prev.hi, cur.lo, cur.hi))
		}
	}
	if last := layers[len(layers)-1]; last.hi < 127 {
		ns = append(ns, fmt.Sprintf("%s gap: no layer covers %d..127", prefix, last.hi+1))
	}
	return ns
}

// regionToVariant maps the SFZ opcodes of one region onto a sampler.Variant,
// resolving the sample path relative to the .sfz directory and flagging a missing
// file. The returned string is a non-fatal note when the sample is absent.
func regionToVariant(op map[string]string, baseDir string) (sampler.Variant, string) {
	v := sampler.Variant{
		PitchKeycenter: opInt(op, "pitch_keycenter", sampler.DefaultKeycenter),
		Transpose:      opInt(op, "transpose", 0),
		Tune:           opInt(op, "tune", 0), // SFZ tune is in cents
		StartFrame:     int64(opInt(op, "offset", 0)),
		EndFrame:       int64(opInt(op, "end", 0)),
		LoopStart:      int64(opInt(op, "loop_start", 0)),
		LoopEnd:        int64(opInt(op, "loop_end", 0)),
		Gain:           opFloat(op, "volume", 0), // SFZ volume is dB; 0 = unity
		Pan:            sfzPan(op),
		LoopMode:       parseLoopMode(op["loop_mode"]),
	}

	var note string
	if raw, ok := op["sample"]; ok && strings.TrimSpace(raw) != "" {
		resolved := resolveSamplePath(raw, baseDir)
		v.SamplePath = resolved
		if !fileExists(resolved) {
			v.Missing = true
			note = "missing sample: " + resolved
		}
	} else {
		v.Missing = true
		note = "region with no sample opcode"
	}
	return v, note
}

// resolveSamplePath normalizes a possibly-Windows relative path and joins it to the
// .sfz directory. We normalize '\' to '/' via pathx-aware splitting so a kit authored
// on Windows resolves on Linux/CI too (CLAUDE.md path invariant).
func resolveSamplePath(raw, baseDir string) string {
	raw = strings.TrimSpace(raw)
	// Split on either separator and rejoin with the host separator.
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == '/' || r == '\\' })
	rel := filepath.Join(parts...)
	// Absolute Windows paths (C:\...) or POSIX absolute: keep as-is, just normalized.
	if isAbsolutePath(raw) {
		return rel
	}
	return filepath.Join(baseDir, rel)
}

func isAbsolutePath(p string) bool {
	if filepath.IsAbs(p) {
		return true
	}
	// Windows drive-letter form C:\ or C:/
	if len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') {
		return true
	}
	return false
}

// sfzPan converts SFZ pan (-100..100) to the model's -1..1.
func sfzPan(op map[string]string) float64 {
	if raw, ok := op["pan"]; ok {
		f := atofSafe(raw, 0)
		return f / 100.0
	}
	return 0
}

func parseLoopMode(raw string) sampler.LoopMode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "one_shot":
		return sampler.OneShot
	case "loop_continuous":
		return sampler.LoopContinuous
	case "loop_sustain":
		return sampler.LoopSustain
	default:
		return sampler.NoLoop
	}
}

// opcodeKey returns the region's MIDI key. SFZ `key` sets lokey=hikey=pitch_keycenter;
// if only lokey is present we use it. Defaults to DefaultKeycenter.
func opcodeKey(op map[string]string) int {
	if k, ok := op["key"]; ok {
		return midiNote(k, sampler.DefaultKeycenter)
	}
	if k, ok := op["lokey"]; ok {
		return midiNote(k, sampler.DefaultKeycenter)
	}
	if k, ok := op["pitch_keycenter"]; ok {
		return midiNote(k, sampler.DefaultKeycenter)
	}
	return sampler.DefaultKeycenter
}

func insertAtSeq(rr []sampler.Variant, v sampler.Variant, pos int) []sampler.Variant {
	idx := pos - 1 // seq_position is 1-based
	for len(rr) <= idx {
		rr = append(rr, sampler.Variant{Missing: true})
	}
	rr[idx] = v
	return rr
}

// ---- small parse helpers (shared with the DecentSampler parser) ----

func opInt(op map[string]string, key string, def int) int {
	if v, ok := op[key]; ok {
		return atoiSafe(v, def)
	}
	return def
}

func opFloat(op map[string]string, key string, def float64) float64 {
	if v, ok := op[key]; ok {
		return atofSafe(v, def)
	}
	return def
}

func atoiSafe(s string, def int) int {
	s = strings.TrimSpace(s)
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	// tolerate a float written where an int is expected
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return int(f)
	}
	return def
}

func atofSafe(s string, def float64) float64 {
	if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
		return f
	}
	return def
}

// midiNote parses either a raw MIDI number (0..127) or a note name like "c4",
// "f#3", "Db2". SFZ note names use c-1..g9 with c4 = 60 (the "middle C = 60"
// convention used by sfzformat.com's default octave offset).
func midiNote(s string, def int) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	low := strings.ToLower(s)
	semis := map[byte]int{'c': 0, 'd': 2, 'e': 4, 'f': 5, 'g': 7, 'a': 9, 'b': 11}
	base, ok := semis[low[0]]
	if !ok {
		return def
	}
	i := 1
	for i < len(low) && (low[i] == '#' || low[i] == 'b') {
		if low[i] == '#' {
			base++
		} else {
			base--
		}
		i++
	}
	octStr := low[i:]
	oct, err := strconv.Atoi(octStr)
	if err != nil {
		return def
	}
	// c4 = 60  =>  note = base + (oct+1)*12
	return base + (oct+1)*12
}

func stripExt(name string) string {
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		return name[:i]
	}
	return name
}

func fileExists(p string) bool {
	if p == "" {
		return false
	}
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func containsInt(s []int, n int) bool {
	for _, x := range s {
		if x == n {
			return true
		}
	}
	return false
}
