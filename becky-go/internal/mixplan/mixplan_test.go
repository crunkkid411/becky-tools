package mixplan

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"becky-go/internal/music"
)

// metalcoreProject mirrors a becky-compose project.json for a full band: a drum
// kit, an isolated low-end (bass -> bus.808), rhythm + lead guitar, and a breakdown
// signal carried on a routing note. It is the canonical fixture for these tests.
func metalcoreProject() music.Project {
	return music.Project{
		SchemaVersion: 1,
		Tool:          "becky-compose",
		Genre:         "metalcore",
		Tempo:         145,
		Tracks: []music.ProjTrack{
			{ID: "drums", Kind: "percussion", Out: "bus.drums"},
			{ID: "bass", Kind: "instrument", Out: "bus.808"},
			{ID: "chords", Kind: "instrument", Out: "bus.music"},
			{ID: "melody", Kind: "instrument", Out: "bus.music"},
			{ID: "lead", Kind: "instrument", Out: "bus.music"},
		},
		Buses: []music.ProjBus{
			{ID: "bus.808", Out: "bus.master"},
			{ID: "bus.drums", Out: "bus.master"},
			{ID: "bus.music", Out: "bus.master"},
			{ID: "bus.master", Out: "out.main"},
		},
		Routing: []music.ProjEdge{
			{From: "src.drums.kick", To: "comp.music.sidechain", Kind: "sidechain", Note: "duck the music bus off the kick on the breakdown"},
		},
	}
}

func buildMetalcore(t *testing.T) *MixPlan {
	t.Helper()
	raw, err := json.Marshal(metalcoreProject())
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return Build(Load(raw, "out/project.json"), Options{Profile: ProfileJST})
}

// --- routing / bus derivation ------------------------------------------------

func TestDeriveBusRoles_fullBand(t *testing.T) {
	roles := deriveBusRoles(metalcoreProject())
	cases := []struct {
		bus  string
		want string
	}{
		{BusMaster, roleMaster},
		{BusDrums, roleDrums},
		{Bus808, roleBass},
		{BusGtrRhythm, roleGuitar}, // chords -> rhythm guitar
		{BusGtrLead, roleGuitar},   // lead -> lead guitar
		{BusSynth, roleSynth},      // melody -> synth
		{busKick, roleDrums},       // kit-piece insert bus appears with a kit
		{busSnare, roleDrums},
	}
	for _, c := range cases {
		if got := roles[c.bus]; got != c.want {
			t.Errorf("role[%s] = %q, want %q", c.bus, got, c.want)
		}
	}
}

func TestBuild_busPlansHaveChainsAndOut(t *testing.T) {
	plan := buildMetalcore(t)
	for _, b := range plan.Buses {
		if len(b.FX) == 0 {
			t.Errorf("bus %s has an empty FX chain", b.Bus)
		}
		if b.Out == "" {
			t.Errorf("bus %s has no output routing", b.Bus)
		}
	}
	// Buses must be sorted (determinism + readability).
	for i := 1; i < len(plan.Buses); i++ {
		if plan.Buses[i-1].Bus > plan.Buses[i].Bus {
			t.Errorf("buses not sorted: %s before %s", plan.Buses[i-1].Bus, plan.Buses[i].Bus)
		}
	}
}

func TestOutFor_masterToProjectOutput(t *testing.T) {
	p := metalcoreProject()
	if got := outFor(BusMaster, p); got != "out.main" {
		t.Errorf("master out = %q, want out.main", got)
	}
	if got := outFor(BusGtrRhythm, p); got != BusMaster {
		t.Errorf("derived bus out = %q, want %s", got, BusMaster)
	}
}

// --- FX chain ordering -------------------------------------------------------

func TestChainOrdering_isSemanticNotAlphabetical(t *testing.T) {
	// Rhythm guitar: HPF/eq must come BEFORE the gate, the band-split before the
	// low sidechain comp — i.e. the authored signal-flow order, never sorted.
	fx := rhythmGuitarChain()
	order := typeSeq(fx)
	want := []string{"eq", "gate", "bandsplit", "compressor", "compressor", "saturation"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("rhythm chain order = %v, want %v", order, want)
	}
}

func TestKickChain_startsGatedThenTriggered(t *testing.T) {
	fx := chainForRole(roleDrums, busKick)
	if len(fx) < 2 || fx[0].Type != "gate" || fx[1].Type != "trigger" {
		t.Errorf("kick chain should start gate -> trigger, got %v", typeSeq(fx))
	}
}

func TestLowEndChain_hasSidechainCompFedByKick(t *testing.T) {
	fx := chainForRole(roleBass, Bus808)
	var sc *FXNode
	for i := range fx {
		if fx[i].ID == Bus808+".sidechainComp" {
			sc = &fx[i]
		}
	}
	if sc == nil {
		t.Fatal("808 chain missing its sidechainComp node")
	}
	if sc.Params["sidechain"] != true || sc.Params["source"] != srcKick {
		t.Errorf("808 sidechainComp not fed by the kick: %+v", sc.Params)
	}
}

// --- sidechain-edge generation ----------------------------------------------

func TestBreakdownEdges_emittedWhenBreakdownAndLowEnd(t *testing.T) {
	plan := buildMetalcore(t)
	if !plan.BreakdownDetected {
		t.Fatal("breakdown should be detected (note says 'breakdown' + bus.808 exists)")
	}
	wantTo := map[string]bool{
		Bus808 + ".sidechainComp": false,
		BusGtrRhythm + ".scLow":   false,
	}
	for _, e := range plan.BreakdownRouting {
		if e.Kind != "sidechain" {
			t.Errorf("edge %s->%s kind = %q, want sidechain", e.From, e.To, e.Kind)
		}
		if _, ok := wantTo[e.To]; ok {
			wantTo[e.To] = true
		}
	}
	for to, seen := range wantTo {
		if !seen {
			t.Errorf("expected a breakdown edge ducking %s", to)
		}
	}
	// The rhythm-guitar duck must be band-split (lows only).
	for _, e := range plan.BreakdownRouting {
		if e.To == BusGtrRhythm+".scLow" && e.Band != "low" {
			t.Errorf("rhythm-guitar duck must be band-split low, got band=%q", e.Band)
		}
	}
}

func TestBreakdownEdges_skippedWithoutLowEnd(t *testing.T) {
	p := metalcoreProject()
	// Drop the low-end source entirely.
	p.Tracks = []music.ProjTrack{{ID: "drums", Kind: "percussion", Out: "bus.drums"}}
	p.Buses = []music.ProjBus{{ID: "bus.drums", Out: "bus.master"}, {ID: "bus.master", Out: "out.main"}}
	raw, _ := json.Marshal(p)
	plan := Build(Load(raw, "p/project.json"), Options{Breakdown: true}) // forced ON
	if plan.BreakdownDetected {
		t.Error("no isolated low-end bus -> breakdown routine must NOT fire (anti-hedge)")
	}
	if len(plan.BreakdownRouting) != 0 {
		t.Errorf("expected zero breakdown edges, got %d", len(plan.BreakdownRouting))
	}
	if !containsNote(plan.Notes, "no isolated low-end") {
		t.Errorf("expected a plain note explaining the skip, got %v", plan.Notes)
	}
}

func TestBreakdownEdges_sortedDeterministically(t *testing.T) {
	plan := buildMetalcore(t)
	for i := 1; i < len(plan.BreakdownRouting); i++ {
		a, b := plan.BreakdownRouting[i-1], plan.BreakdownRouting[i]
		if a.To > b.To || (a.To == b.To && a.From > b.From) {
			t.Errorf("breakdown edges not sorted: (%s->%s) before (%s->%s)", a.From, a.To, b.From, b.To)
		}
	}
}

func TestProjectHasBreakdown_detectsNoteAndOff(t *testing.T) {
	if projectHasBreakdown(music.Project{Routing: []music.ProjEdge{{Note: "duck on the BREAKDOWN"}}}) != true {
		t.Error("should detect 'breakdown' in a routing note (case-insensitive)")
	}
	if projectHasBreakdown(music.Project{Routing: []music.ProjEdge{{Note: "verse pump"}}}) != false {
		t.Error("should not detect a breakdown when none is signalled")
	}
}

// --- VST preferences ---------------------------------------------------------

func TestVSTMap_defaultsOdinIIOnGuitarBuses(t *testing.T) {
	plan := buildMetalcore(t)
	got := map[string]string{}
	for _, v := range plan.VSTMap {
		got[v.Bus] = v.VST
	}
	for _, bus := range []string{BusGtrRhythm, BusGtrLead} {
		if got[bus] != odinII {
			t.Errorf("vst[%s] = %q, want %q", bus, got[bus], odinII)
		}
	}
}

func TestVSTMap_userPrefOverridesDefault(t *testing.T) {
	roles := map[string]string{BusGtrRhythm: roleGuitar}
	out := resolveVSTMap(roles, []VSTPreference{{Bus: BusGtrRhythm, VST: "Toneforge Menace"}})
	if len(out) != 1 || out[0].VST != "Toneforge Menace" {
		t.Errorf("user pref should override the Odin II default, got %+v", out)
	}
	if !out[0].FallbackToBuiltin {
		t.Error("a user pref must still fall back to the built-in floor")
	}
}

// --- determinism -------------------------------------------------------------

func TestBuild_byteIdentical(t *testing.T) {
	raw, _ := json.Marshal(metalcoreProject())
	a := mustMarshal(t, Build(Load(raw, "x/project.json"), Options{Profile: ProfileJST}))
	b := mustMarshal(t, Build(Load(raw, "x/project.json"), Options{Profile: ProfileJST}))
	if !bytes.Equal(a, b) {
		t.Error("same project + profile must yield byte-identical mix.json")
	}
}

func TestBuild_hashChangesWithProject(t *testing.T) {
	p1, _ := json.Marshal(metalcoreProject())
	pp := metalcoreProject()
	pp.Tempo = 200
	p2, _ := json.Marshal(pp)
	h1 := Build(Load(p1, "a/project.json"), Options{}).AppliesToHash
	h2 := Build(Load(p2, "a/project.json"), Options{}).AppliesToHash
	if h1 == h2 {
		t.Error("a different project must content-address to a different hash")
	}
	if !strings.HasPrefix(h1, "sha256:") {
		t.Errorf("hash should be sha256-prefixed, got %q", h1)
	}
}

// --- degrade-never-crash -----------------------------------------------------

func TestBuild_garbledProjectDegrades(t *testing.T) {
	plan := Build(Load([]byte("{ this is not json"), "bad/project.json"), Options{})
	if plan == nil {
		t.Fatal("must return a (partial) plan, never nil")
	}
	if len(plan.Notes) == 0 || !containsNote(plan.Notes, "degraded") {
		t.Errorf("garbled project should yield a plain degrade note, got %v", plan.Notes)
	}
	// Even degraded, the master bus + a valid hash must be present.
	if plan.AppliesToHash == "" {
		t.Error("degraded plan still needs a content hash")
	}
	if !hasBus(plan, BusMaster) {
		t.Error("degraded plan must still describe the master bus")
	}
}

func TestBuild_emptyProjectDegrades(t *testing.T) {
	plan := Build(Load(nil, "missing/project.json"), Options{})
	if plan == nil || !containsNote(plan.Notes, "degraded") {
		t.Errorf("empty/missing project should degrade with a note, got %+v", plan)
	}
	if _, err := plan.Marshal(); err != nil {
		t.Errorf("a degraded plan must still marshal: %v", err)
	}
}

// --- marshal -----------------------------------------------------------------

func TestMarshal_isValidNewlineTerminatedJSON(t *testing.T) {
	data := mustMarshal(t, buildMetalcore(t))
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Error("marshal output must be newline-terminated")
	}
	var round MixPlan
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if round.SchemaVersion != SchemaVersion || round.Tool != "becky-mix" {
		t.Errorf("round-trip lost header fields: %+v", round)
	}
}

// --- helpers -----------------------------------------------------------------

func typeSeq(fx []FXNode) []string {
	out := make([]string, len(fx))
	for i, n := range fx {
		out[i] = n.Type
	}
	return out
}

func containsNote(notes []string, sub string) bool {
	for _, n := range notes {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}

func hasBus(m *MixPlan, bus string) bool {
	for _, b := range m.Buses {
		if b.Bus == bus {
			return true
		}
	}
	return false
}

func mustMarshal(t *testing.T, m *MixPlan) []byte {
	t.Helper()
	b, err := m.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
