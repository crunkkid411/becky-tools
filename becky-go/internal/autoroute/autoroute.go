// Package autoroute is becky's DETERMINISTIC routing engine — the "little AI dude"
// minus the AI. Jordan's routing is rule-based, not magic: "if it's labelled kick it
// goes to the drum bus; a Serum synth goes to the synth bus UNLESS it's labelled bass,
// then it goes to the bass bus." That is a first-match-wins rule table, so it lives in
// code (a model is optional gravy for fuzzy labels later, never required).
//
// The whole point: WRITING stays lightweight (no buses, no plugins, no lag); when
// Jordan is happy, Apply routes every track to the right bus + sets his sidechains in
// one shot — recreating the deterministic part of his heavy template instantly, with
// zero clicks. The ruleset is user-editable JSON (~/.becky/routing.json) so his rules
// are encoded once and never change.
package autoroute

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"becky-go/internal/dawmodel"
)

// Rule routes a track to Bus when its (lowercased) label contains any Match token and
// none of the Except tokens. Rules are evaluated in order; the FIRST match wins — so
// put overrides first (the "bass" rule before the "synth" rule).
type Rule struct {
	Match  []string `json:"match"`
	Except []string `json:"except,omitempty"`
	Bus    string   `json:"bus"`
}

// BusDef declares a bus in the tree: its id and where it routes (another bus / master).
type BusDef struct {
	ID  string `json:"id"`
	Out string `json:"out"`
}

// SidechainRule ducks Bus off Source (a track label or a bus id).
type SidechainRule struct {
	Bus    string `json:"bus"`
	Source string `json:"source"`
}

// Ruleset is the whole deterministic routing config.
type Ruleset struct {
	Buses      []BusDef        `json:"buses"`
	Rules      []Rule          `json:"rules"`
	Default    string          `json:"default"`
	Sidechains []SidechainRule `json:"sidechains"`
}

// DefaultRuleset is Jordan's dummy-proof default: label → bus, BASS checked before
// SYNTH so "bass synth" lands on BASS, kick ducks the bass. Edit ~/.becky/routing.json
// to change it once; it then applies everywhere.
func DefaultRuleset() Ruleset {
	const master = "bus.master"
	return Ruleset{
		Buses: []BusDef{
			{"DRUMS", master}, {"BASS", master}, {"GUITARS", master},
			{"SYNTH", master}, {"VOCALS", master}, {"FX", master}, {"MUSIC", master},
		},
		// Order matters (first match wins). The specific nouns (guitar/vox) come
		// BEFORE SYNTH so "lead guitar"→GUITARS and "lead vox"→VOCALS, while a bare
		// "lead" or "serum lead" still falls through to SYNTH. BASS comes before SYNTH
		// so "serum bass"→BASS. DRUMS is first; FX last before the default.
		Rules: []Rule{
			{Match: []string{"kick", "snare", "hat", "tom", "clap", "ride", "crash", "perc", "rim", "drum", "808 kit", "cymbal", "shaker", "cowbell"}, Bus: "DRUMS"},
			{Match: []string{"bass", "sub", "808", "reese"}, Bus: "BASS"}, // before synth
			{Match: []string{"guitar", "gtr", "riff", "djent", "power chord"}, Bus: "GUITARS"},
			{Match: []string{"vox", "vocal", "sing", "adlib", "ad-lib", "harmony", "choir", "scream", "rap"}, Bus: "VOCALS"},
			{Match: []string{"serum", "synth", "saw", "lead", "pad", "arp", "pluck", "chord", "melody", "keys", "piano", "organ", "bell"}, Bus: "SYNTH"},
			{Match: []string{"fx", "riser", "impact", "sweep", "noise", "sfx", "downlifter", "uplifter", "whoosh"}, Bus: "FX"},
		},
		Default:    "MUSIC",
		Sidechains: []SidechainRule{{Bus: "BASS", Source: "kick"}},
	}
}

// BusFor returns the destination bus for a track label — the core deterministic
// decision. First matching rule (Match hit, no Except hit) wins; else Default.
func (rs Ruleset) BusFor(label string) string {
	l := strings.ToLower(strings.TrimSpace(label))
	for _, r := range rs.Rules {
		if containsAny(l, r.Except) {
			continue
		}
		if containsAny(l, r.Match) {
			return r.Bus
		}
	}
	return rs.Default
}

// Assignment records where one track was routed and why (for a dummy-proof report).
type Assignment struct {
	Track string `json:"track"`
	Bus   string `json:"bus"`
}

// Apply routes every track to its rule-determined bus, ensures the bus tree exists,
// and wires the sidechains — all via the immutable dawmodel verbs. It is the one-shot
// "make my routing happen" that replaces re-routing 16+ channels by hand. Returns the
// new arrangement and the per-track assignments. Never mutates the input.
func Apply(arr *dawmodel.Arrangement, rs Ruleset) (*dawmodel.Arrangement, []Assignment) {
	if arr == nil {
		return arr, nil
	}
	out := arr
	// Ensure the bus tree (so a routed skeleton is a lightweight default starting point).
	have := map[string]bool{}
	for _, b := range out.Buses {
		have[b.ID] = true
	}
	for _, bd := range rs.Buses {
		if !have[bd.ID] {
			out.Buses = append(out.Buses, dawmodel.Bus{ID: bd.ID, Out: bd.Out})
			have[bd.ID] = true
		}
	}
	// Route each track.
	var assigns []Assignment
	for _, t := range out.Tracks {
		bus := rs.BusFor(t.ID)
		if next, err := out.RouteTo(t.ID, bus); err == nil {
			out = next
		}
		assigns = append(assigns, Assignment{Track: t.ID, Bus: bus})
	}
	// Sidechains: resolve the source label to a track id or a bus id that exists.
	for _, sc := range rs.Sidechains {
		src := resolveSource(out, sc.Source)
		if src == "" {
			continue
		}
		if next, err := out.AddSidechain(sc.Bus, src); err == nil {
			out = next
		}
	}
	return out, assigns
}

// resolveSource turns a sidechain source label into a real node id: a track whose
// label contains it, else a bus with that id, else "".
func resolveSource(arr *dawmodel.Arrangement, source string) string {
	s := strings.ToLower(strings.TrimSpace(source))
	for _, t := range arr.Tracks {
		if strings.Contains(strings.ToLower(t.ID), s) {
			return t.ID
		}
	}
	for _, b := range arr.Buses {
		if strings.EqualFold(b.ID, source) {
			return b.ID
		}
	}
	return ""
}

func containsAny(s string, toks []string) bool {
	for _, t := range toks {
		if t != "" && strings.Contains(s, t) {
			return true
		}
	}
	return false
}

// ---- persistence: the ruleset is the user's, edited once ----

// Path is ~/.becky/routing.json (override with BECKY_ROUTING).
func Path() string {
	if p := strings.TrimSpace(os.Getenv("BECKY_ROUTING")); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "routing.json"
	}
	return filepath.Join(home, ".becky", "routing.json")
}

// Load reads the user's ruleset, falling back to DefaultRuleset when absent/invalid.
func Load() Ruleset {
	data, err := os.ReadFile(Path())
	if err != nil {
		return DefaultRuleset()
	}
	var rs Ruleset
	if err := json.Unmarshal(data, &rs); err != nil || len(rs.Rules) == 0 {
		return DefaultRuleset()
	}
	return rs
}

// Save writes a ruleset (so a user can `init` the default then tweak it).
func Save(rs Ruleset) error {
	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(rs, "", "  ")
	return os.WriteFile(p, data, 0o644)
}
