package refmatch

// apply.go closes the loop: it takes a deterministic MatchPlan (the moves that make
// YOUR stem sound like a reference that already sounds right) and writes those moves
// onto a music.Project routing graph as FX nodes on a named bus. Nothing here is
// audio DSP — it is the same "layer, don't mutate" graph edit becky-wire performs
// (see internal/studio/apply.go), so a printed plan becomes an APPLIED plan.
//
// IMMUTABILITY: ApplyPlan deep-copies the bus it touches; the input Project is never
// modified. IDEMPOTENCE: re-applying the same plan to the same bus replaces the two
// nodes it owns (by stable id) instead of duplicating them — apply twice, get the
// same graph.
//
// ENCODING (no struct changes): music.ProjFX has exactly two string fields, Type and
// ID. We do NOT extend it. The EQ moves are packed into the Type field as a compact,
// deterministic, self-describing string:
//
//	Type: "eq:ref:+2.5@250,+0.0@850,-1.5@3000"   ID: "<bus>.ref.eq"
//	Type: "gain:ref:+2.6dB"                       ID: "<bus>.ref.gain"
//
// i.e. each EQ term is "<signed dB>@<centerHz>" (Hz as an integer, dB to one
// decimal), comma-joined in the plan's (fixed, center-frequency-sorted) order. A
// downstream tool — or a human reading the JSON — can recover every move from the
// string alone. The "ref:" tag distinguishes a reference-match node from becky-wire's
// own "eq"/"gain:" nodes so the two tools never collide on a bus.

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"becky-go/internal/music"
)

// Node-id suffixes ApplyPlan owns on a bus. Stable so re-applying replaces, never
// duplicates. They are namespaced with "ref." so they never clash with becky-wire's
// nodes (which use plain "eq"/"gain"/"vst.*" ids).
const (
	refEQSuffix   = ".ref.eq"
	refGainSuffix = ".ref.gain"
)

// ApplyResult is the outcome of applying a plan to a project: the new (immutably
// patched) project, a plain-English summary of exactly what was inserted/changed, and
// the structured nodes that were written (so a --dry-run can describe them without
// mutating anything, and a caller can log them).
type ApplyResult struct {
	Project  music.Project // NEW project; the input is untouched
	Summary  string        // plain-English, non-dev readable
	BusID    string        // the bus the nodes landed on
	EQNode   *music.ProjFX // the EQ node written (nil if the plan had no EQ moves)
	GainNode *music.ProjFX // the gain node written (nil if the plan had no gain move)
	NoMoves  bool          // true when the plan was "close enough" — nothing to apply
	Note     string        // honest caveats carried from the plan (RMS not LUFS, etc.)
}

// ApplyPlan inserts the plan's EQ moves and overall gain move onto busID in proj,
// returning a NEW project (the input is never mutated). It is deterministic and
// idempotent: the two nodes it owns are keyed by a stable id, so applying the same
// plan twice yields the same graph. The bus is created (routed to bus.master) if the
// project does not declare it yet — degrade-never-crash, never an error here.
//
// A "close enough" plan (no EQ moves and no gain move) writes nothing and reports
// NoMoves; the returned project is an untouched deep-copy.
func ApplyPlan(proj music.Project, busID string, plan MatchPlan) ApplyResult {
	out := cloneProjectForApply(proj)
	res := ApplyResult{Project: out, BusID: busID, Note: plan.Note}

	hasEQ := len(plan.EQMoves) > 0
	hasGain := plan.GainText != "" && math.Abs(plan.GainDB) > 0
	if !hasEQ && !hasGain {
		res.NoMoves = true
		res.Summary = "nothing to apply — the plan says your stem already matches the reference"
		return res
	}

	idx := ensureBus(&out, busID)
	short := shortBus(busID)

	if hasEQ {
		node := music.ProjFX{Type: encodeEQType(plan.EQMoves), ID: short + refEQSuffix}
		out.Buses[idx].FX = setOrAddFX(out.Buses[idx].FX, node)
		res.EQNode = &node
	}
	if hasGain {
		node := music.ProjFX{Type: encodeGainType(plan.GainDB), ID: short + refGainSuffix}
		out.Buses[idx].FX = setOrAddFX(out.Buses[idx].FX, node)
		res.GainNode = &node
	}

	res.Project = out
	res.Summary = summarize(busID, plan, hasEQ, hasGain)
	return res
}

// DryRunSummary returns the same plain-English summary ApplyPlan would produce, WITHOUT
// touching the project — for `becky-ref apply --dry-run` ("show me, don't do it").
func DryRunSummary(busID string, plan MatchPlan) string {
	hasEQ := len(plan.EQMoves) > 0
	hasGain := plan.GainText != "" && math.Abs(plan.GainDB) > 0
	if !hasEQ && !hasGain {
		return "nothing to apply — the plan says your stem already matches the reference"
	}
	return summarize(busID, plan, hasEQ, hasGain)
}

// summarize builds the plain-English line: "Would set your drum bus EQ toward the
// reference: +2.5 dB @ 3 kHz, -1.5 dB @ 250 Hz, turn up 2.6 dB". Deterministic —
// moves are listed in the plan's fixed (center-frequency-sorted) order.
func summarize(busID string, plan MatchPlan, hasEQ, hasGain bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Set the %s bus toward the reference:", busLabel(busID))
	if hasEQ {
		parts := make([]string, 0, len(plan.EQMoves))
		for _, m := range plan.EQMoves {
			parts = append(parts, fmt.Sprintf("%s @ %s", signedDB(m.DeltaDB), hzLabel(m.CenterHz)))
		}
		b.WriteString(" EQ ")
		b.WriteString(strings.Join(parts, ", "))
	}
	if hasGain {
		if hasEQ {
			b.WriteString(";")
		}
		if plan.GainDB > 0 {
			fmt.Fprintf(&b, " turn up %.1f dB", plan.GainDB)
		} else {
			fmt.Fprintf(&b, " turn down %.1f dB", math.Abs(plan.GainDB))
		}
	}
	return b.String()
}

// busLabel turns "bus.drums" into "drum" for the friendly summary ("the drum bus").
func busLabel(busID string) string {
	s := shortBus(busID)
	if s == "" {
		return busID
	}
	return s
}

// encodeEQType packs the EQ moves into the ProjFX.Type string (no struct change). The
// format is "eq:ref:<term>,<term>,..." where each term is "<signed dB>@<centerHz>".
// Center Hz is rounded to an integer; dB keeps one decimal. Moves are emitted in the
// plan's existing fixed order, so the same plan always encodes byte-identically.
func encodeEQType(moves []EQMove) string {
	terms := make([]string, 0, len(moves))
	for _, m := range moves {
		terms = append(terms, fmt.Sprintf("%s@%d", signedDB(m.DeltaDB), int(math.Round(m.CenterHz))))
	}
	return "eq:ref:" + strings.Join(terms, ",")
}

// encodeGainType packs the overall gain move: "gain:ref:+2.6dB".
func encodeGainType(gainDB float64) string {
	return "gain:ref:" + signedDB(gainDB) + "dB"
}

// signedDB formats a dB value with an explicit sign and one decimal: "+2.5", "-1.5",
// "+0.0". The explicit "+" keeps the encoded string unambiguous and parseable.
func signedDB(v float64) string {
	s := strconv.FormatFloat(math.Abs(v), 'f', 1, 64)
	if v < 0 {
		return "-" + s
	}
	return "+" + s
}

// ensureBus returns the index of busID in out.Buses, creating it (routed to
// bus.master, buses re-sorted by id for determinism) if it does not exist.
func ensureBus(out *music.Project, busID string) int {
	if i := indexBus(out.Buses, busID); i >= 0 {
		return i
	}
	out.Buses = append(out.Buses, music.ProjBus{ID: busID, Out: "bus.master"})
	sortBusesByID(out.Buses)
	return indexBus(out.Buses, busID)
}

func indexBus(buses []music.ProjBus, id string) int {
	for i, b := range buses {
		if b.ID == id {
			return i
		}
	}
	return -1
}

func sortBusesByID(buses []music.ProjBus) {
	// insertion-free stable sort via the stdlib pattern used in studio/apply.go.
	for i := 1; i < len(buses); i++ {
		for j := i; j > 0 && buses[j-1].ID > buses[j].ID; j-- {
			buses[j-1], buses[j] = buses[j], buses[j-1]
		}
	}
}

// setOrAddFX replaces an FX node with the same id (idempotent re-apply) or appends it.
// Mirrors internal/studio/apply.go's helper of the same intent.
func setOrAddFX(fx []music.ProjFX, n music.ProjFX) []music.ProjFX {
	for i := range fx {
		if fx[i].ID == n.ID {
			fx[i] = n
			return fx
		}
	}
	return append(fx, n)
}

// shortBus turns "bus.drums" into "drums" for node-id construction (mirrors
// studio.shortBusName). A bus id without the "bus." prefix is returned unchanged.
func shortBus(bus string) string {
	return strings.TrimPrefix(bus, "bus.")
}

// ShortBusID is the exported form of shortBus, for the CLI's habit-scope construction.
func ShortBusID(bus string) string { return shortBus(bus) }

// cloneProjectForApply deep-copies the slices ApplyPlan mutates (Buses + each bus's
// FX) so the input Project is never touched. Other slices are shared by value copy;
// we never mutate them here.
func cloneProjectForApply(p music.Project) music.Project {
	out := p
	out.TimeSignature = append([]int(nil), p.TimeSignature...)
	out.Progression = append([]string(nil), p.Progression...)
	out.Tracks = append([]music.ProjTrack(nil), p.Tracks...)
	out.Routing = append([]music.ProjEdge(nil), p.Routing...)
	out.Buses = make([]music.ProjBus, len(p.Buses))
	for i, b := range p.Buses {
		nb := b
		nb.FX = append([]music.ProjFX(nil), b.FX...)
		out.Buses[i] = nb
	}
	return out
}
