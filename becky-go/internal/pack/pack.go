// Package pack loads becky-voice tool packs — saved allowlists of tools + optional
// tier overrides for an active context (e.g. "default" for forensic triage, "reaper"
// for DAW work). A pack file lives at packs/<name>.json and is searched next to the
// running binary and in the working directory (HANDOFF-BECKY-VOICE.md Phase 2 Step 2.1).
// Compiled-in defaults are the fallback so the binary works with no packs/ dir.
package pack

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"becky-go/internal/catalog"
)

// Pack is a named allowlist of tool verbs + optional per-tool tier overrides.
type Pack struct {
	Name          string                  `json:"name"`
	Tools         []string                `json:"tools"`
	TierOverrides map[string]catalog.Tier `json:"tier_overrides,omitempty"`
}

// Offers reports whether the tool verb is in this pack's allowed list.
func (p Pack) Offers(tool string) bool {
	for _, t := range p.Tools {
		if t == tool {
			return true
		}
	}
	return false
}

// TierFor returns the effective action tier for a tool: a pack-level override wins,
// else the catalog tier, else TierRed for an unknown tool. This mirrors the semantics
// of voicerules.Rules.TierFor — packs can only tighten, not loosen, a catalog tier.
func (p Pack) TierFor(tool string) catalog.Tier {
	if p.TierOverrides != nil {
		if t, ok := p.TierOverrides[tool]; ok {
			switch t {
			case catalog.TierGreen, catalog.TierYellow, catalog.TierRed:
				return t
			}
		}
	}
	return catalog.TierOf(tool)
}

// Load finds and parses packs/<name>.json. Search order:
//  1. <exe_dir>/packs/<name>.json
//  2. <exe_dir>/../packs/<name>.json  (covers bin/ → repo root)
//  3. <cwd>/packs/<name>.json
//  4. Compiled-in defaults ("default", "reaper")
//
// Unknown names with no on-disk file return an error.
func Load(name string) (Pack, error) {
	fname := strings.ToLower(strings.TrimSpace(name)) + ".json"
	for _, dir := range searchDirs() {
		cand := filepath.Join(dir, "packs", fname)
		b, err := os.ReadFile(cand)
		if err == nil {
			return parse(b)
		}
	}
	// Compiled-in fallback — always works, no filesystem required.
	if src, ok := builtinPacks[strings.ToLower(strings.TrimSpace(name))]; ok {
		return parse([]byte(src))
	}
	return Pack{}, fmt.Errorf("pack %q: packs/%s not found", name, fname)
}

// DefaultPack returns the compiled-in default pack without touching the filesystem.
func DefaultPack() Pack {
	p, _ := parse([]byte(defaultPackJSON))
	return p
}

// ReaperPack returns the compiled-in reaper pack without touching the filesystem.
func ReaperPack() Pack {
	p, _ := parse([]byte(reaperPackJSON))
	return p
}

func parse(b []byte) (Pack, error) {
	var p Pack
	if err := json.Unmarshal(b, &p); err != nil {
		return Pack{}, fmt.Errorf("parse pack: %w", err)
	}
	if strings.TrimSpace(p.Name) == "" {
		return Pack{}, fmt.Errorf("pack has no name field")
	}
	return p, nil
}

// searchDirs returns candidate base directories for packs/, ordered by preference.
func searchDirs() []string {
	var dirs []string
	if exe, err := os.Executable(); err == nil {
		d := filepath.Dir(exe)
		dirs = append(dirs, d, filepath.Dir(d)) // exe dir + parent (handles bin/ layout)
	}
	if wd, err := os.Getwd(); err == nil {
		dirs = append(dirs, wd)
	}
	return dirs
}

// builtinPacks are the compiled-in defaults — identical to packs/*.json on disk.
var builtinPacks = map[string]string{
	"default": defaultPackJSON,
	"reaper":  reaperPackJSON,
}

const defaultPackJSON = `{
  "name": "default",
  "tools": [
    "becky-transcribe",
    "becky-diarize",
    "becky-identify",
    "becky-pipeline",
    "becky-ocr",
    "becky-search",
    "becky-research",
    "becky-radar",
    "becky-scout",
    "find",
    "becky-events",
    "becky-review",
    "becky-web2md",
    "becky-clipcheck",
    "becky-regrab",
    "becky-export"
  ],
  "tier_overrides": {}
}`

const reaperPackJSON = `{
  "name": "reaper",
  "tools": [
    "reaper-bridge"
  ],
  "tier_overrides": {}
}`
