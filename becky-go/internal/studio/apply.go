package studio

// apply.go turns an Intent into a NEW, immutably-patched music.Project plus a
// plain-English summary. Nothing is mutated in place: the input Project is
// deep-copied, the edit is applied to the copy, and the copy is returned (the
// mixplan/dawmodel "layer, don't mutate" discipline). Output is deterministic:
// routing edges are sorted before return so the same Intent always serialises
// identically.

import (
	"sort"
	"strings"

	"becky-go/internal/music"
)

// standardChainTypes is the deterministic floor FX chain inserted by
// ActionInsertChain when becky doesn't have a richer per-bus template. It mirrors
// the gate->eq->comp->saturation ordering that mixplan treats as load-bearing.
// (becky-mix owns the rich, per-role chains; becky-wire inserts a sane, ordered
// floor onto the project graph so the bus is "set up" with one instruction.)
var standardChainTypes = []string{"gate", "eq", "compressor", "saturation"}

// Apply returns a NEW Project with the Intent applied, plus a one-line summary of
// what changed. The input proj is never modified. For ActionUnknown it returns an
// unchanged copy and the friendly note (degrade-never-crash).
func Apply(proj music.Project, in Intent) (music.Project, string) {
	out := cloneProject(proj)

	switch in.Action {
	case ActionSidechain:
		return applySidechain(out, in)
	case ActionRoute:
		return applyRoute(out, in)
	case ActionInsertChain:
		return applyInsertChain(out, in)
	case ActionSetVST:
		return applySetVST(out, in)
	case ActionSetGain:
		return applySetGain(out, in)
	default:
		note := in.Note
		if note == "" {
			note = "nothing to apply (instruction not understood)"
		}
		return out, note
	}
}

// applySidechain adds a {from:detector, to:<bus>.sidechainComp, kind:"sidechain"}
// control edge. Idempotent: re-applying the same duck does not duplicate the edge.
func applySidechain(out music.Project, in Intent) (music.Project, string) {
	to := in.Target + ".sidechainComp"
	note := in.Note
	if in.Band == "low" {
		note += " (low band)"
	}
	edge := music.ProjEdge{From: in.Source, To: to, Kind: "sidechain", Note: note}
	out.Routing = addEdge(out.Routing, edge)
	out.Routing = sortEdges(out.Routing)

	band := ""
	if in.Band != "" {
		band = " (" + in.Band + " band)"
	}
	summary := "Sidechained the " + in.TargetWord + " to the " + in.SourceWord + band +
		" — " + in.Source + " -> " + to
	return out, summary
}

// applyRoute adds an audio routing edge track -> bus and updates the track's Out.
func applyRoute(out music.Project, in Intent) (music.Project, string) {
	edge := music.ProjEdge{From: in.Source, To: in.Target, Kind: "audio", Note: in.Note}
	out.Routing = addEdge(out.Routing, edge)
	out.Routing = sortEdges(out.Routing)

	// Keep the track's declared Out in sync so other tools see the new home.
	for i := range out.Tracks {
		if out.Tracks[i].Node == in.Source {
			out.Tracks[i].Out = in.Target
		}
	}
	summary := "Routed the " + in.SourceWord + " to the " + in.TargetWord + " bus" +
		" — " + in.Source + " -> " + in.Target
	return out, summary
}

// applyInsertChain inserts the standard FX chain onto the target bus, creating
// the bus if the project doesn't declare it yet. Existing chain is preserved if
// it already contains these nodes (idempotent).
func applyInsertChain(out music.Project, in Intent) (music.Project, string) {
	idx := busIndex(out, in.Target)
	if idx < 0 {
		out.Buses = append(out.Buses, music.ProjBus{ID: in.Target, Out: "bus.master"})
		idx = len(out.Buses) - 1
		out.Buses = sortBuses(out.Buses)
		idx = busIndex(out, in.Target)
	}
	busShort := shortBusName(in.Target)
	for _, typ := range standardChainTypes {
		id := busShort + "." + typ
		if !hasFX(out.Buses[idx].FX, id) {
			out.Buses[idx].FX = append(out.Buses[idx].FX, music.ProjFX{Type: typ, ID: id})
		}
	}
	summary := "Set up the " + in.TargetWord + " bus with the standard chain (" +
		strings.Join(standardChainTypes, " -> ") + ")"
	return out, summary
}

// applySetVST records a per-bus VST preference as an FX node carrying the plugin
// name (kept on the project graph; becky-mix's VSTMap is the richer mix-side
// home, but the project edit makes the choice visible immediately).
func applySetVST(out music.Project, in Intent) (music.Project, string) {
	idx := busIndex(out, in.Target)
	if idx < 0 {
		out.Buses = append(out.Buses, music.ProjBus{ID: in.Target, Out: "bus.master"})
		out.Buses = sortBuses(out.Buses)
		idx = busIndex(out, in.Target)
	}
	id := shortBusName(in.Target) + ".vst." + slug(in.VST)
	if !hasFX(out.Buses[idx].FX, id) {
		out.Buses[idx].FX = append(out.Buses[idx].FX, music.ProjFX{Type: "vst:" + in.VST, ID: id})
	}
	summary := "Set " + in.VST + " on the " + in.TargetWord + " bus"
	return out, summary
}

// applySetGain records a gain-staging preference as a gain FX node on the target,
// encoding the target dB in the node id (deterministic, data-only).
func applySetGain(out music.Project, in Intent) (music.Project, string) {
	// Gain nodes live on a bus; if the target is a track node, stage it on its bus.
	busID := in.Target
	if !strings.HasPrefix(in.Target, "bus.") {
		busID = trackOutBus(out, in.Target)
	}
	idx := busIndex(out, busID)
	if idx < 0 {
		out.Buses = append(out.Buses, music.ProjBus{ID: busID, Out: "bus.master"})
		out.Buses = sortBuses(out.Buses)
		idx = busIndex(out, busID)
	}
	id := shortBusName(busID) + ".gain"
	out.Buses[idx].FX = setOrAddFX(out.Buses[idx].FX, music.ProjFX{Type: "gain:" + formatDB(in.GainDB) + "dB", ID: id})
	summary := "Gain-staged the " + in.TargetWord + " to " + formatDB(in.GainDB) + " dB"
	return out, summary
}

// ─── immutable-copy + graph helpers ───────────────────────────────────────────

// cloneProject deep-copies the slices we mutate so the input is never touched.
func cloneProject(p music.Project) music.Project {
	out := p // value copy: scalars + the Key/Render structs are copied by value
	out.TimeSignature = append([]int(nil), p.TimeSignature...)
	out.Progression = append([]string(nil), p.Progression...)
	out.Tracks = append([]music.ProjTrack(nil), p.Tracks...)
	out.Buses = make([]music.ProjBus, len(p.Buses))
	for i, b := range p.Buses {
		nb := b
		nb.FX = append([]music.ProjFX(nil), b.FX...)
		out.Buses[i] = nb
	}
	out.Routing = append([]music.ProjEdge(nil), p.Routing...)
	return out
}

// addEdge appends an edge unless an identical one already exists (idempotent).
func addEdge(edges []music.ProjEdge, e music.ProjEdge) []music.ProjEdge {
	for _, x := range edges {
		if x.From == e.From && x.To == e.To && x.Kind == e.Kind {
			return edges // already present — don't duplicate
		}
	}
	return append(edges, e)
}

// sortEdges orders routing deterministically: by To, then From, then Kind.
func sortEdges(edges []music.ProjEdge) []music.ProjEdge {
	sort.SliceStable(edges, func(i, j int) bool {
		a, b := edges[i], edges[j]
		if a.To != b.To {
			return a.To < b.To
		}
		if a.From != b.From {
			return a.From < b.From
		}
		return a.Kind < b.Kind
	})
	return edges
}

// sortBuses orders buses by id deterministically.
func sortBuses(buses []music.ProjBus) []music.ProjBus {
	sort.SliceStable(buses, func(i, j int) bool { return buses[i].ID < buses[j].ID })
	return buses
}

func busIndex(p music.Project, id string) int {
	for i, b := range p.Buses {
		if b.ID == id {
			return i
		}
	}
	return -1
}

func hasFX(fx []music.ProjFX, id string) bool {
	for _, n := range fx {
		if n.ID == id {
			return true
		}
	}
	return false
}

// setOrAddFX replaces an FX node with the same id, or appends it if absent.
func setOrAddFX(fx []music.ProjFX, n music.ProjFX) []music.ProjFX {
	for i := range fx {
		if fx[i].ID == n.ID {
			fx[i] = n
			return fx
		}
	}
	return append(fx, n)
}

// trackOutBus finds the bus a track node currently routes to, defaulting to the
// master bus when unknown.
func trackOutBus(p music.Project, node string) string {
	for _, t := range p.Tracks {
		if t.Node == node && t.Out != "" {
			return t.Out
		}
	}
	return "bus.master"
}

// shortBusName turns "bus.gtrLead" into "gtrLead" for node-id construction.
func shortBusName(bus string) string {
	return strings.TrimPrefix(bus, "bus.")
}

// slug lowercases and hyphenates a plugin name for a stable node id.
func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = spaceRe.ReplaceAllString(s, "-")
	return s
}
