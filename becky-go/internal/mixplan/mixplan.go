// Package mixplan turns a becky-compose project.json into a DETERMINISTIC mix
// plan (mix.json): per-bus FX chains, the Joey Sturgis breakdown kick->low-end
// sidechain expressed as declared {from,to,kind:"sidechain"} edges, and per-bus
// VST preference slots (default "The Odin II" on guitar/lead). It is the
// knowledge layer over the routing DAG — a mix as a DECLARED graph, not a diary
// of manual moves (SPEC-BECKY-MIX-JST.md).
//
// Pure-Go, offline, deterministic: the SAME project.json + profile + prefs yield
// a BYTE-IDENTICAL mix.json (buses/edges/chains are sorted; map order is never
// serialized). JST plugins appear only as optional VST equivalents — data only,
// no audio is processed here (degrade-never-crash: a missing VST falls back to
// the built-in floor; a garbled project.json yields a partial plan + a plain
// note, never a panic).
package mixplan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// SchemaVersion is the mix.json schema version emitted by this package.
const SchemaVersion = 1

// MixPlan is the mix.json artifact: it LAYERS onto a project.json (referenced by
// content hash, never mutated) and declares, per bus, the ordered FX chain plus
// the breakdown sidechain edges. Deterministic: every slice is sorted before
// emission so the same inputs always serialize identically.
type MixPlan struct {
	SchemaVersion     int             `json:"schemaVersion"`
	Tool              string          `json:"tool"`
	Profile           string          `json:"profile"`
	Deterministic     bool            `json:"deterministic"`
	AppliesTo         string          `json:"appliesTo"`         // source project.json basename
	AppliesToHash     string          `json:"appliesToHash"`     // sha256 of the source project bytes
	Buses             []BusPlan       `json:"buses"`             // sorted by Bus id
	BreakdownRouting  []SidechainEdge `json:"breakdownRouting"`  // sorted; empty unless a breakdown is signalled
	BreakdownDetected bool            `json:"breakdownDetected"` // true when the JST breakdown routine fired
	VSTMap            []VSTPreference `json:"vstMap"`            // sorted by Bus id; default Odin II on guitar/lead
	Notes             []string        `json:"notes"`             // plain-language degrade/info notes
}

// BusPlan is one mix bus: its logical role, the ordered FX chain (the
// deterministic built-in floor), and the JST plugin named as the OPTIONAL VST3/
// CLAP equivalent for the bus as a whole.
type BusPlan struct {
	Bus      string   `json:"bus"`                // canonical bus id, e.g. "bus.gtrRhythm"
	Role     string   `json:"role"`               // drums | bass | guitar | vox | master | aux
	Out      string   `json:"out"`                // downstream bus id
	FX       []FXNode `json:"fx"`                 // ordered chain (gate->eq->comp->sat order is data)
	JSTEquiv []string `json:"jstEquiv,omitempty"` // optional VST equivalents (documentation only)
}

// SidechainEdge is one declared {from,to,kind:"sidechain"} control edge: a
// detector tap (from) ducks a compressor's sidechain input (to). Amount/feel
// live in the FX node the edge targets; the edge carries the high-level intent.
type SidechainEdge struct {
	From   string  `json:"from"`             // detector source, e.g. "kick"
	To     string  `json:"to"`               // sidechain input, e.g. "bus.808.sidechainComp"
	Kind   string  `json:"kind"`             // always "sidechain"
	Amount float64 `json:"amount,omitempty"` // optional 0..1 blend (omit = full)
	Band   string  `json:"band,omitempty"`   // "low" for frequency-selective ducks
	Note   string  `json:"note"`
}

// VSTPreference is the replaceable per-bus plugin slot. The user registers his
// own trusted VST/preset here; absent or untrusted -> fall back to the built-in
// floor (degrade-never-crash). Resolution order: user prefs -> profile -> floor.
type VSTPreference struct {
	Bus               string `json:"bus"`
	VST               string `json:"vst"`
	Preset            string `json:"preset,omitempty"`
	FallbackToBuiltin bool   `json:"fallbackToBuiltin"`
}

// hashBytes content-addresses the source project so the mix stays reproducible
// (a stem stays a stem; the mix references it, never edits it).
func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// sortPlan sorts every slice in the plan so emission is byte-stable regardless
// of the order things were assembled or any map iteration upstream.
func sortPlan(m *MixPlan) {
	sort.Slice(m.Buses, func(i, j int) bool { return m.Buses[i].Bus < m.Buses[j].Bus })
	sort.SliceStable(m.BreakdownRouting, func(i, j int) bool {
		a, b := m.BreakdownRouting[i], m.BreakdownRouting[j]
		if a.To != b.To {
			return a.To < b.To
		}
		return a.From < b.From
	})
	sort.Slice(m.VSTMap, func(i, j int) bool { return m.VSTMap[i].Bus < m.VSTMap[j].Bus })
	// FX chains keep their authored (semantic) order — gate->eq->comp->sat is
	// load-bearing, NOT alphabetical — so they are intentionally NOT re-sorted.
}

// Marshal renders the plan to deterministic, newline-terminated JSON.
func (m *MixPlan) Marshal() ([]byte, error) {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal mix plan: %w", err)
	}
	return append(b, '\n'), nil
}
