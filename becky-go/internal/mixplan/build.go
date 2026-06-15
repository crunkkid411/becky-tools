package mixplan

// build.go is the deterministic project.json -> mix.json layerer: it loads a
// becky-compose project (degrade-safe), derives the logical mix buses + roles,
// assembles each bus's ordered FX chain, emits the JST breakdown sidechain edges
// when (and only when) two corroborating signals agree, and attaches the per-bus
// VST preference slots. No network, no mutation of the source project.

import (
	"encoding/json"
	"fmt"

	"becky-go/internal/music"
	"becky-go/internal/pathx"
)

// Logical bus roles (used to pick chains, JST equivalents, and VST defaults).
const (
	roleDrums  = "drums"
	roleBass   = "bass"
	roleGuitar = "guitar"
	roleVox    = "vox"
	roleSynth  = "synth"
	roleMaster = "master"
	roleAux    = "aux"
)

// ProfileJST is the default mix-profile id.
const ProfileJST = "jst"

// odinII is the user's first registered guitar/lead VST preference (§6).
const odinII = "The Odin II"

// Source is a loaded project.json plus the raw bytes (for content-addressing).
type Source struct {
	Project music.Project
	Raw     []byte
	Path    string
	Err     error // non-nil when the project could not be fully parsed
}

// Load reads a project.json off disk into a Source. A read/parse failure does
// NOT abort: the bytes (if any) are kept and Err is set so Build can still emit
// a partial plan with a plain note (degrade-never-crash).
func Load(raw []byte, path string) Source {
	s := Source{Raw: raw, Path: path}
	if len(raw) == 0 {
		s.Err = fmt.Errorf("empty project.json")
		return s
	}
	if err := json.Unmarshal(raw, &s.Project); err != nil {
		s.Err = fmt.Errorf("parse project.json: %w", err)
	}
	return s
}

// Options tune the layering. Breakdown forces the JST breakdown routine on even
// when the project carries no explicit breakdown marker (the producer knows).
type Options struct {
	Profile   string          // mix profile id (default "jst")
	Breakdown bool            // force the breakdown sidechain routine on
	Prefs     []VSTPreference // user VST/preset overrides (highest precedence)
}

// Build layers a deterministic mix plan over a loaded project. It never panics:
// a garbled source yields a minimal-but-valid plan plus a note explaining what
// degraded. Two corroborating signals — a breakdown is present AND an isolated
// low-end bus exists — are required before the aggressive breakdown edges emit.
func Build(s Source, opts Options) *MixPlan {
	profile := opts.Profile
	if profile == "" {
		profile = ProfileJST
	}
	m := &MixPlan{
		SchemaVersion: SchemaVersion,
		Tool:          "becky-mix",
		Profile:       profile,
		Deterministic: true,
		AppliesTo:     pathx.Base(s.Path),
		AppliesToHash: hashBytes(s.Raw),
	}
	if s.Err != nil {
		m.Notes = append(m.Notes, "project.json degraded: "+s.Err.Error()+" — emitting a partial plan from what parsed")
	}

	roles := deriveBusRoles(s.Project)
	for bus, role := range roles {
		m.Buses = append(m.Buses, BusPlan{
			Bus: bus, Role: role, Out: outFor(bus, s.Project),
			FX: chainForRole(role, bus), JSTEquiv: jstEquivFor(role),
		})
	}
	hasLowEnd := roles[Bus808] != "" || roles[BusBass] != ""
	breakdown := opts.Breakdown || projectHasBreakdown(s.Project)
	if breakdown && hasLowEnd {
		m.BreakdownDetected = true
		m.BreakdownRouting = breakdownEdges(roles)
	} else if breakdown && !hasLowEnd {
		m.Notes = append(m.Notes, "breakdown signalled but no isolated low-end bus (808/bass) — emitting no breakdown edges (anti-hedge)")
	}

	m.VSTMap = resolveVSTMap(roles, opts.Prefs)
	sortPlan(m)
	return m
}

// outFor returns the downstream bus for a derived bus: the project's own routing
// when it knows, else master. The master bus routes to the project's output.
func outFor(bus string, p music.Project) string {
	for _, b := range p.Buses {
		if b.ID == bus && b.Out != "" {
			return b.Out
		}
	}
	if bus == BusMaster {
		return "out.main"
	}
	return BusMaster
}
