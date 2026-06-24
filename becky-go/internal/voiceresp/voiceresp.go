// Package voiceresp is becky-voice's response map: the AUTO-GENERATED, fill-in-the-blank
// set of spoken lines for every tool × every outcome (ok / partial / error), plus the
// "fix it" verb per tool. This directly answers Jordan's "I have no idea how to make a
// trace dataset / what responses we need" — the catalog defines the outcomes, the
// generator pre-fills sensible defaults in whoretana's voice, and Jordan just REPLACES
// strings (same experience as speak_error in his Python fork). The chooser is pure Go
// and seeded/round-robin — choosing a line calls NO model (SPEC-BECKY-VOICE.md §5.6,
// HANDOFF-BECKY-VOICE.md Step 0.3).
package voiceresp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"becky-go/internal/catalog"
)

// Outcome is one of the three results a tool run can have.
type Outcome string

const (
	OutcomeOK      Outcome = "ok"
	OutcomePartial Outcome = "partial"
	OutcomeError   Outcome = "error"
)

// Outcomes is the canonical ordered set of outcomes every tool entry must cover.
var Outcomes = []Outcome{OutcomeOK, OutcomePartial, OutcomeError}

// Entry is the response set for ONE tool: a list of spoken lines per outcome (Jordan
// edits these strings) and the verb that "fix it" maps to for this tool.
type Entry struct {
	OK      []string `json:"ok"`
	Partial []string `json:"partial"`
	Error   []string `json:"error"`
	FixVerb string   `json:"fix_verb"`
}

// lines returns the slice for an outcome.
func (e Entry) lines(o Outcome) []string {
	switch o {
	case OutcomeOK:
		return e.OK
	case OutcomePartial:
		return e.Partial
	case OutcomeError:
		return e.Error
	}
	return nil
}

// Map is the whole response map: tool verb -> Entry. It is the serialized responses.json.
type Map map[string]Entry

// defaultFixVerb is the repair tool a bare "fix it" deploys when a tool has no more
// specific repair verb — becky's "propose a new tool / coding agent" path.
const defaultFixVerb = "becky-new-tool"

// Generate walks internal/catalog and emits a pre-populated, editable response map: for
// every tool, a default line per outcome in whoretana's voice. Jordan edits by replacing
// the strings; the file is usable on day one.
func Generate() Map {
	m := Map{}
	for _, c := range catalog.All() {
		short := strings.TrimSuffix(strings.TrimSpace(c.Summary), ".")
		if short == "" {
			short = c.Verb
		}
		m[c.Verb] = Entry{
			OK: []string{
				fmt.Sprintf("Done — %s.", lower1(short)),
				fmt.Sprintf("Got it. %s, finished.", cap1(short)),
			},
			Partial: []string{
				fmt.Sprintf("Mostly there — %s, but some of it came back thin. Want me to dig deeper?", lower1(short)),
				fmt.Sprintf("Partial result on %s. I kept what's solid and flagged the rest.", c.Verb),
			},
			Error: []string{
				fmt.Sprintf("Ah shit, %s broke. %s.", c.Verb, cap1(short)),
				fmt.Sprintf("That one fell over — %s didn't finish. Say \"fix it\" and I'll get on it.", c.Verb),
			},
			FixVerb: defaultFixVerb,
		}
	}
	return m
}

// MarshalJSON-style helper: WriteJSON returns the pretty, stable (sorted-key) JSON for
// the map so the generated responses.json is human-diffable and order-stable.
func (m Map) JSON() ([]byte, error) {
	// json.Marshal already sorts map string keys, so this is deterministic.
	return json.MarshalIndent(m, "", "  ")
}

// Load parses a responses.json into a Map.
func Load(b []byte) (Map, error) {
	var m Map
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parse responses.json: %w", err)
	}
	return m, nil
}

// Chooser is a deterministic, seeded round-robin selector over a Map. The same chooser
// returns each variant of a (tool, outcome) line before repeating any — no model, no
// global randomness, reproducible.
type Chooser struct {
	m   Map
	idx map[string]int // "tool|outcome" -> next index
}

// NewChooser builds a chooser over a response map.
func NewChooser(m Map) *Chooser {
	return &Chooser{m: m, idx: map[string]int{}}
}

// Choose returns the next spoken line for a tool+outcome, advancing round-robin. It
// returns ok=false only when the tool/outcome has no lines at all.
func (c *Chooser) Choose(tool string, o Outcome) (string, bool) {
	e, ok := c.m[tool]
	if !ok {
		return "", false
	}
	lines := e.lines(o)
	if len(lines) == 0 {
		return "", false
	}
	key := tool + "|" + string(o)
	i := c.idx[key] % len(lines)
	c.idx[key] = (c.idx[key] + 1) % len(lines)
	return lines[i], true
}

// FixVerb resolves the repair verb for a tool: "fix it" on that tool deploys this verb.
// An unknown tool falls back to the default repair verb.
func (m Map) FixVerb(tool string) string {
	if e, ok := m[tool]; ok && strings.TrimSpace(e.FixVerb) != "" {
		return e.FixVerb
	}
	return defaultFixVerb
}

// Tools returns the response map's tool verbs, sorted, for stable iteration/tests.
func (m Map) Tools() []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// --- small string helpers (deterministic, no model) ---

func lower1(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	// Only lower the first rune if it isn't an acronym-y all-caps token like "OCR".
	if len(r) >= 2 && r[1] >= 'A' && r[1] <= 'Z' {
		return s
	}
	r[0] = lowerRune(r[0])
	return string(r)
}

func cap1(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = upperRune(r[0])
	return string(r)
}

func lowerRune(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + ('a' - 'A')
	}
	return r
}

func upperRune(r rune) rune {
	if r >= 'a' && r <= 'z' {
		return r - ('a' - 'A')
	}
	return r
}
