package mixplan

// derive.go reads a becky-compose project and decides — deterministically —
// which logical mix buses exist, which JST FX chain each gets, whether the
// breakdown sidechain routine should fire, and which VST preference sits on each
// bus. All derivation is from explicit project fields; nothing is guessed.

import (
	"strings"

	"becky-go/internal/music"
)

// kit-piece insert buses (so kick/snare get their own §4.1/§4.2 chains).
const (
	busKick  = "bus.kick"
	busSnare = "bus.snare"
)

// deriveBusRoles maps every mix bus the plan will describe to its logical role.
// It always includes the master bus, derives drums/low-end/guitar/vox/synth from
// the project's tracks, and adds kick/snare insert buses when a drum kit exists.
func deriveBusRoles(p music.Project) map[string]string {
	roles := map[string]string{BusMaster: roleMaster}
	for _, t := range p.Tracks {
		assignTrackBus(roles, t)
	}
	// Honour any explicit isolated low-end bus from compose even if no track
	// mapped to it (e.g. a hand-edited project): bus.808 is the canonical sub bus.
	for _, b := range p.Buses {
		if b.ID == Bus808 {
			roles[Bus808] = roleBass
		}
	}
	if _, hasDrums := roles[BusDrums]; hasDrums {
		roles[busKick] = roleDrums
		roles[busSnare] = roleDrums
	}
	return roles
}

// assignTrackBus routes one track to its logical mix bus + role. Pitched tracks
// that becky-compose lumps onto bus.music are split out here (guitar/lead/vox/
// synth) so each gets a purpose-built JST chain.
func assignTrackBus(roles map[string]string, t music.ProjTrack) {
	id := strings.ToLower(t.ID)
	switch {
	case t.Kind == "percussion" || id == "drums":
		roles[BusDrums] = roleDrums
	case id == "bass":
		roles[Bus808] = roleBass
	case id == "lead":
		roles[BusGtrLead] = roleGuitar
	case id == "chords" || strings.Contains(id, "gtr") || strings.Contains(id, "guitar") || id == "rhythm":
		roles[BusGtrRhythm] = roleGuitar
	case id == "vox" || id == "vocal" || strings.Contains(id, "vocal"):
		roles[BusVox] = roleVox
	case id == "melody" || id == "counter" || id == "synth" || id == "pad" || id == "sfx":
		roles[BusSynth] = roleSynth
	default:
		roles[BusSynth] = roleSynth
	}
}

// projectHasBreakdown looks for a breakdown signal the source project carries:
// any routing note/edge mentioning "breakdown". (The arrangement sections live in
// the compose profile, not project.json, so this is the available in-band signal;
// the --breakdown flag is the explicit override.)
func projectHasBreakdown(p music.Project) bool {
	for _, e := range p.Routing {
		if strings.Contains(strings.ToLower(e.Note), "breakdown") ||
			strings.Contains(strings.ToLower(e.From), "breakdown") ||
			strings.Contains(strings.ToLower(e.To), "breakdown") {
			return true
		}
	}
	return false
}

// breakdownEdges emits the §3.1 JST breakdown sidechain edges, skipping any whose
// destination bus does not exist in this project (anti-hedge: never duck a bus
// that isn't there). Edges are returned in a fixed authored order; Build sorts.
func breakdownEdges(roles map[string]string) []SidechainEdge {
	var edges []SidechainEdge
	add := func(have bool, e SidechainEdge) {
		if have {
			edges = append(edges, e)
		}
	}
	_, hasBass := roles[BusBass]
	add(roles[Bus808] != "", SidechainEdge{
		From: srcKick, To: Bus808 + ".sidechainComp", Kind: "sidechain",
		Note: "808/sub ducks hard to the kick so only one source owns the sub",
	})
	add(hasBass, SidechainEdge{
		From: srcKick, To: BusBass + ".sidechainComp", Kind: "sidechain",
		Note: "DI bass ducks to the kick; keeps the kick transient clean",
	})
	add(roles[BusGtrRhythm] != "", SidechainEdge{
		From: srcKick, To: BusGtrRhythm + ".scLow", Kind: "sidechain", Band: "low",
		Note: "down-tuned rhythm chug ducks LOWS only to the kick (band-split)",
	})
	add(roles[Bus808] != "", SidechainEdge{
		From: srcSnare, To: Bus808 + ".sidechainComp", Kind: "sidechain", Amount: 0.5,
		Note: "optional: 808 also nods to the snare on breakdown backbeats",
	})
	return edges
}

// resolveVSTMap builds the per-bus VST preference slots. Defaults: "The Odin II"
// on guitar buses (§6). User Prefs override defaults by bus id. Resolution order:
// user prefs -> profile defaults -> built-in floor (fallbackToBuiltin always on).
func resolveVSTMap(roles map[string]string, prefs []VSTPreference) []VSTPreference {
	chosen := map[string]VSTPreference{}
	for bus, role := range roles {
		if role == roleGuitar {
			chosen[bus] = VSTPreference{Bus: bus, VST: odinII, FallbackToBuiltin: true}
		}
	}
	for _, p := range prefs {
		if p.Bus == "" {
			continue
		}
		p.FallbackToBuiltin = true // a user pref still degrades to the floor if the VST is absent
		chosen[p.Bus] = p
	}
	out := make([]VSTPreference, 0, len(chosen))
	for _, v := range chosen {
		out = append(out, v)
	}
	return out
}
