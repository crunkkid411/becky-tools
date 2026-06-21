// Package fxchain is becky's deterministic DATA MODEL + storage for the per-bus /
// per-track VST FX CHAINS — the ordered list of plugins (with their saved state)
// that load onto a bus when Jordan "finalizes" a song. The ROUTING already exists
// (internal/autoroute: label→bus); this is the FX layer that sits on top of it.
//
// IMPORTANT — the defaults are NOT deterministic. Jordan picks his chains by
// hand/conversation; becky does NOT auto-populate "most-used = default" (that would
// presume his taste). This package only stores, lists, and applies a chain AS DATA.
// DefaultChains() therefore returns the standard bus ids EMPTY and ready to fill —
// taste stays his. The plugin state itself (the PresetRef file) is loaded by the C++
// VST3 host / REAPER later; here we just record which plugin + which preset, in order.
//
// The persistence mirrors internal/autoroute exactly: one user-owned JSON at
// ~/.becky/fxchains.json (override BECKY_FXCHAINS), edited once.
package fxchain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Plugin is one insert in a chain: a VST plugin and (optionally) a saved-state preset.
// ClassID is the VST3 class/component id used by the host to load the exact plugin;
// PresetRef is a path to a .vstpreset / state file (the state is loaded by the C++
// host later — this package never reads it). Bypass keeps the slot but mutes its FX.
type Plugin struct {
	Name      string `json:"name"`
	ClassID   string `json:"class_id,omitempty"`
	PresetRef string `json:"preset_ref,omitempty"`
	Bypass    bool   `json:"bypass,omitempty"`
}

// Chain is the ordered insert chain on one bus (or track). Order is signal order:
// Plugins[0] processes first. Bus is the destination id (e.g. "DRUMS"), matching the
// bus ids autoroute uses.
type Chain struct {
	Bus     string   `json:"bus"`
	Plugins []Plugin `json:"plugins"`
}

// Chains is the whole FX config: one chain per bus, keyed by bus id.
type Chains struct {
	ByBus map[string]Chain `json:"by_bus"`
}

// StandardBuses are the bus ids becky routes to (kept in sync with
// autoroute.DefaultRuleset's buses). DefaultChains seeds these EMPTY so they're
// present and ready for Jordan to fill — no presumptuous plugins.
var StandardBuses = []string{"DRUMS", "BASS", "GUITARS", "SYNTH", "VOCALS", "FX", "MUSIC"}

// DefaultChains returns the standard bus ids with EMPTY chains — ready to fill, never
// pre-populated. This is intentional: the defaults are Jordan's, not becky's.
func DefaultChains() Chains {
	byBus := make(map[string]Chain, len(StandardBuses))
	for _, b := range StandardBuses {
		byBus[b] = Chain{Bus: b, Plugins: []Plugin{}}
	}
	return Chains{ByBus: byBus}
}

// Buses returns the bus ids that have a chain entry, sorted for deterministic output.
func (c Chains) Buses() []string {
	ids := make([]string, 0, len(c.ByBus))
	for id := range c.ByBus {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Get returns the chain for a bus (an empty, non-nil chain if the bus has none).
func (c Chains) Get(bus string) Chain {
	if ch, ok := c.ByBus[bus]; ok {
		return ch
	}
	return Chain{Bus: bus, Plugins: []Plugin{}}
}

// Add returns a new Chains with one plugin appended to the bus's chain (in order).
// Never mutates the receiver; the bus is created if it didn't exist.
func (c Chains) Add(bus string, p Plugin) Chains {
	out := c.clone()
	ch := out.Get(bus)
	plugins := append([]Plugin{}, ch.Plugins...)
	plugins = append(plugins, p)
	out.ByBus[bus] = Chain{Bus: bus, Plugins: plugins}
	return out
}

// SetChain returns a new Chains with the bus's whole chain replaced (order preserved).
// Never mutates the receiver.
func (c Chains) SetChain(bus string, plugins []Plugin) Chains {
	out := c.clone()
	cp := append([]Plugin{}, plugins...)
	out.ByBus[bus] = Chain{Bus: bus, Plugins: cp}
	return out
}

// clone deep-copies so the immutable-ish helpers never alias the caller's maps/slices.
func (c Chains) clone() Chains {
	byBus := make(map[string]Chain, len(c.ByBus))
	for id, ch := range c.ByBus {
		byBus[id] = Chain{Bus: ch.Bus, Plugins: append([]Plugin{}, ch.Plugins...)}
	}
	return Chains{ByBus: byBus}
}

// ---- persistence: the FX config is the user's, edited once ----

// Path is ~/.becky/fxchains.json (override with BECKY_FXCHAINS).
func Path() string {
	if p := strings.TrimSpace(os.Getenv("BECKY_FXCHAINS")); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "fxchains.json"
	}
	return filepath.Join(home, ".becky", "fxchains.json")
}

// Load reads the user's FX config, falling back to DefaultChains when absent/invalid.
func Load() Chains {
	data, err := os.ReadFile(Path())
	if err != nil {
		return DefaultChains()
	}
	var c Chains
	if err := json.Unmarshal(data, &c); err != nil || c.ByBus == nil {
		return DefaultChains()
	}
	return c
}

// Save writes the FX config (so a user can `init` the empty default then fill it).
func Save(c Chains) error {
	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(p, data, 0o644)
}
