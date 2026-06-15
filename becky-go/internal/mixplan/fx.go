package mixplan

// fx.go holds the deterministic FX-chain templates per bus (SPEC-BECKY-MIX-JST
// §4) and the breakdown sidechain defaults (§3.2), expressed as named-constant
// data — no magic numbers buried in control flow. Order within each chain is
// load-bearing (gate -> eq -> comp -> saturation) so the plan is topo-sortable
// and reproducible.

// FXNode is one ordered effect in a bus chain: a built-in floor type with its
// default params. The matching JST plugin is named per-bus on BusPlan.JSTEquiv.
type FXNode struct {
	Type   string         `json:"type"`             // gain|eq|gate|compressor|saturation|delay|reverb|limiter|trigger|bandsplit
	ID     string         `json:"id"`               // stable node id, e.g. "gtrRhythm.gate"
	Params map[string]any `json:"params,omitempty"` // default parameters (named constants)
}

// Canonical mix-bus ids. becky-compose emits bus.808/bus.drums/bus.music/bus.fx/
// bus.master; the JST mix adds logical guitar/vox/lead buses where those tracks
// exist so each gets its own deterministic chain.
const (
	BusMaster    = "bus.master"
	BusDrums     = "bus.drums"
	Bus808       = "bus.808" // the isolated low-end bus from compose (the "808/sub")
	BusBass      = "bus.bass"
	BusGtrRhythm = "bus.gtrRhythm"
	BusGtrLead   = "bus.gtrLead"
	BusVox       = "bus.vox"
	BusSynth     = "bus.synth"
)

// Detector sources for sidechain edges (control taps, not audio).
const (
	srcKick  = "kick"
	srcSnare = "snare"
)

// fxNode is a tiny constructor that keeps the chain templates readable.
func fxNode(typ, id string, params map[string]any) FXNode {
	return FXNode{Type: typ, ID: id, Params: params}
}

// drumsChain: eq -> compressor(glue) -> compressor(parallel smash) -> saturation
// -> limiter. Glue comp lets the transient through (20ms attack), a parallel
// smashed copy adds thickness, gentle bus saturation + a transparent ceiling.
func drumsChain() []FXNode {
	return []FXNode{
		fxNode("eq", "drums.eq", map[string]any{"hpfHz": 30, "presenceHz": 4000, "presenceDb": 2}),
		fxNode("compressor", "drums.glue", map[string]any{"ratio": 4, "attackMs": 20, "releaseMs": 10}),
		fxNode("compressor", "drums.parallel", map[string]any{"ratio": 8, "attackMs": 1, "releaseMs": 50, "blend": 0.4}),
		fxNode("saturation", "drums.sat", map[string]any{"driveDb": 2}),
		fxNode("limiter", "drums.limit", map[string]any{"ceilingDb": -0.3}),
	}
}

// kickChain / snareChain: gate -> trigger/sample(blend) -> eq -> compressor ->
// saturation. Tight, consistent one-shots blended under the kit (the Sturgis
// sample-replacement workflow), modelled as a trigger node with a blend amount.
func kickChain() []FXNode {
	return []FXNode{
		fxNode("gate", "kick.gate", map[string]any{"attackMs": 0.1, "releaseMs": 60}),
		fxNode("trigger", "kick.trigger", map[string]any{"blend": 0.6}),
		fxNode("eq", "kick.eq", map[string]any{"hpfHz": 40, "mudHz": 300, "mudDb": -3, "clickHz": 3000, "clickDb": 3, "subHz": 70, "subDb": 2}),
		fxNode("compressor", "kick.comp", map[string]any{"ratio": 4, "attackMs": 10, "releaseMs": 40}),
		fxNode("saturation", "kick.sat", map[string]any{"driveDb": 2}),
	}
}

func snareChain() []FXNode {
	return []FXNode{
		fxNode("gate", "snare.gate", map[string]any{"attackMs": 0.2, "releaseMs": 80}),
		fxNode("trigger", "snare.trigger", map[string]any{"blend": 0.5}),
		fxNode("eq", "snare.eq", map[string]any{"bodyHz": 200, "bodyDb": 2, "boxyHz": 500, "boxyDb": -3, "crackHz": 4500, "crackDb": 3}),
		fxNode("compressor", "snare.comp", map[string]any{"ratio": 4, "attackMs": 8, "releaseMs": 60}),
		fxNode("saturation", "snare.sat", map[string]any{"driveDb": 2}),
	}
}

// lowEndChain (bus.808 / bus.bass): eq -> bandsplit -> sidechainComp(kick) ->
// saturation -> limiter. The always-on kick->low duck is the metalcore default,
// deepened in breakdowns (§3). Sub kept mono + tight; saturate for small speakers.
func lowEndChain(bus string) []FXNode {
	return []FXNode{
		fxNode("eq", bus+".eq", map[string]any{"hpfHz": 30, "subHz": 60, "subDb": 1}),
		fxNode("bandsplit", bus+".split", map[string]any{"crossoverHz": 150}),
		fxNode("compressor", bus+".sidechainComp", lowEndSidechainParams(bus)),
		fxNode("saturation", bus+".sat", map[string]any{"driveDb": 3}),
		fxNode("limiter", bus+".limit", map[string]any{"ceilingDb": -0.3}),
	}
}

// lowEndSidechainParams picks the always-on duck feel by destination bus.
func lowEndSidechainParams(bus string) map[string]any {
	if bus == Bus808 {
		return map[string]any{"ratio": 6, "attackMs": 0.5, "releaseMs": 80, "reductionDb": 6, "sidechain": true, "source": srcKick}
	}
	return map[string]any{"ratio": 4, "attackMs": 1, "releaseMs": 60, "reductionDb": 4, "sidechain": true, "source": srcKick}
}

// rhythmGuitarChain: eq(hpf+scoop+presence) -> gate -> bandsplit ->
// sidechainComp(kick, LOWS only) -> compressor -> saturation/IR. HPF clears cab
// thump; low-mid scoop makes room for kick/snare; presence push so the riff cuts;
// the kick duck is band-split (lows only) so the riff body and pick attack ring.
func rhythmGuitarChain() []FXNode {
	return []FXNode{
		fxNode("eq", "gtrRhythm.eq", map[string]any{"hpfHz": 90, "scoopHz": 500, "scoopDb": -3, "presenceHz": 3000, "presenceDb": 3}),
		fxNode("gate", "gtrRhythm.gate", map[string]any{"attackMs": 0.5, "releaseMs": 80}),
		fxNode("bandsplit", "gtrRhythm.split", map[string]any{"crossoverHz": 120}),
		fxNode("compressor", "gtrRhythm.scLow", map[string]any{"ratio": 4, "attackMs": 1, "releaseMs": 50, "reductionDb": 3, "band": "low", "sidechain": true, "source": srcKick}),
		fxNode("compressor", "gtrRhythm.comp", map[string]any{"ratio": 3, "attackMs": 5, "releaseMs": 80}),
		fxNode("saturation", "gtrRhythm.ir", map[string]any{"driveDb": 2, "ir": "cab"}),
	}
}

// leadGuitarChain: eq -> compressor -> saturation -> delay -> reverb. Brighter,
// more present than rhythm; sits above the scoop; tasteful slap delay + plate.
func leadGuitarChain() []FXNode {
	return []FXNode{
		fxNode("eq", "gtrLead.eq", map[string]any{"hpfHz": 110, "presenceHz": 3500, "presenceDb": 3}),
		fxNode("compressor", "gtrLead.comp", map[string]any{"ratio": 3, "attackMs": 5, "releaseMs": 120}),
		fxNode("saturation", "gtrLead.ir", map[string]any{"driveDb": 2, "ir": "cab"}),
		fxNode("delay", "gtrLead.delay", map[string]any{"timeMs": 250, "feedback": 0.25, "mix": 0.15}),
		fxNode("reverb", "gtrLead.reverb", map[string]any{"type": "plate", "mix": 0.12}),
	}
}

// voxChain: eq -> gate -> compressor -> saturation -> delay -> reverb -> limiter.
// The signature "mix-ready" vocal: HPF, de-mud, hard comp (parallel blend),
// presence/air, tight verbed/delayed throws.
func voxChain() []FXNode {
	return []FXNode{
		fxNode("eq", "vox.eq", map[string]any{"hpfHz": 100, "mudHz": 350, "mudDb": -2, "airHz": 12000, "airDb": 2}),
		fxNode("gate", "vox.gate", map[string]any{"attackMs": 1, "releaseMs": 120}),
		fxNode("compressor", "vox.comp", map[string]any{"ratio": 4, "attackMs": 5, "releaseMs": 80, "blend": 0.6}),
		fxNode("saturation", "vox.sat", map[string]any{"driveDb": 2}),
		fxNode("delay", "vox.delay", map[string]any{"timeMs": 375, "feedback": 0.2, "mix": 0.12}),
		fxNode("reverb", "vox.reverb", map[string]any{"type": "plate", "mix": 0.15}),
		fxNode("limiter", "vox.limit", map[string]any{"ceilingDb": -0.3}),
	}
}

// synthChain: eq -> sidechainComp(kick, optional) -> reverb. Atmospheric pads can
// duck to the kick to clear the transient.
func synthChain() []FXNode {
	return []FXNode{
		fxNode("eq", "synth.eq", map[string]any{"hpfHz": 80}),
		fxNode("compressor", "synth.scKick", map[string]any{"ratio": 3, "attackMs": 1, "releaseMs": 120, "reductionDb": 3, "sidechain": true, "source": srcKick}),
		fxNode("reverb", "synth.reverb", map[string]any{"type": "hall", "mix": 0.2}),
	}
}

// masterChain: eq -> compressor(glue) -> limiter. Gentle glue, transparent ceiling.
func masterChain() []FXNode {
	return []FXNode{
		fxNode("eq", "master.eq", map[string]any{"tiltDb": 0}),
		fxNode("compressor", "master.glue", map[string]any{"ratio": 2, "attackMs": 30, "releaseMs": 100, "reductionDb": 2}),
		fxNode("limiter", "master.limit", map[string]any{"ceilingDb": -0.3}),
	}
}

// auxChain is the default chain for an unrecognized/aux bus: a single transparent
// safety limiter so the plan still describes every bus (degrade, never omit).
func auxChain(bus string) []FXNode {
	return []FXNode{
		fxNode("limiter", bus+".limit", map[string]any{"ceilingDb": -0.3}),
	}
}

// jstEquivFor names the optional JST VST3/CLAP equivalents per role (data only).
func jstEquivFor(role string) []string {
	switch role {
	case roleDrums:
		return []string{"DF-SMACK", "Drumshotz", "Gain Reduction", "Finality"}
	case roleBass:
		return []string{"Sub Destroyer", "Bassforge", "Finality"}
	case roleGuitar:
		return []string{"Toneforge", "Conquer All IRs"}
	case roleVox:
		return []string{"Gain Reduction Deluxe", "Finality"}
	case roleMaster:
		return []string{"Finality"}
	}
	return nil
}

// chainForRole returns the FX chain template for a logical bus role/id.
func chainForRole(role, bus string) []FXNode {
	switch {
	case bus == busKick:
		return kickChain()
	case bus == busSnare:
		return snareChain()
	case bus == Bus808 || bus == BusBass:
		return lowEndChain(bus)
	case bus == BusGtrRhythm:
		return rhythmGuitarChain()
	case bus == BusGtrLead:
		return leadGuitarChain()
	case bus == BusVox:
		return voxChain()
	case bus == BusSynth:
		return synthChain()
	case bus == BusDrums:
		return drumsChain()
	case bus == BusMaster:
		return masterChain()
	case role == roleDrums:
		return drumsChain()
	case role == roleMaster:
		return masterChain()
	}
	return auxChain(bus)
}
