package studio

import (
	"encoding/json"
	"reflect"
	"testing"

	"becky-go/internal/music"
)

// sampleProject mirrors a typical becky-compose project: drums/bass/lead/vox/
// synth tracks and the canonical buses, so noun resolution has real ids to bind.
func sampleProject() music.Project {
	return music.Project{
		SchemaVersion: 1,
		Tool:          "becky-compose",
		Tracks: []music.ProjTrack{
			{ID: "drums", Node: "src.drums", Out: "bus.drums", Kind: "percussion"},
			{ID: "bass", Node: "src.bass", Out: "bus.808", Kind: "instrument"},
			{ID: "lead", Node: "src.lead", Out: "bus.music", Kind: "instrument"},
			{ID: "vox", Node: "src.vox", Out: "bus.music", Kind: "instrument"},
			{ID: "synth", Node: "src.synth", Out: "bus.music", Kind: "instrument"},
			{ID: "chords", Node: "src.chords", Out: "bus.music", Kind: "instrument"},
		},
		Buses: []music.ProjBus{
			{ID: "bus.808", Out: "bus.master"},
			{ID: "bus.drums", Out: "bus.master"},
			{ID: "bus.music", Out: "bus.master"},
			{ID: "bus.master", Out: "out.main"},
		},
	}
}

// ─── parser: each example sentence -> correct Intent ───────────────────────────

func TestDeterministicParser_Examples(t *testing.T) {
	proj := sampleProject()
	p := DeterministicParser{}

	cases := []struct {
		name    string
		instr   string
		action  Action
		source  string
		target  string
		band    string
		vst     string
		gainDB  float64
		hasGain bool
	}{
		{
			name:   "sidechain bass to kick",
			instr:  "sidechain the bass to the kick",
			action: ActionSidechain, source: "src.drums.kick", target: "bus.808", band: "low",
		},
		{
			name:   "sidechain 808 to kick",
			instr:  "sidechain the 808 to the kick",
			action: ActionSidechain, source: "src.drums.kick", target: "bus.808", band: "low",
		},
		{
			name:   "duck synths under vocal",
			instr:  "duck the synths under the vocal",
			action: ActionSidechain, source: "src.vox", target: "bus.synth", band: "",
		},
		{
			name:   "route lead guitar to guitar bus",
			instr:  "route the lead guitar to the guitar bus",
			action: ActionRoute, source: "src.lead", target: "bus.gtrLead",
		},
		{
			name:   "set up drum bus",
			instr:  "set up the drum bus",
			action: ActionInsertChain, target: "bus.drums",
		},
		{
			name:   "my usual chain on the drum bus",
			instr:  "put my usual chain on the drum bus",
			action: ActionInsertChain, target: "bus.drums",
		},
		{
			name:   "use Odin II on the lead",
			instr:  "use Odin II on the lead",
			action: ActionSetVST, target: "bus.gtrLead", vst: "The Odin II",
		},
		{
			name:   "gain stage the kick to -7",
			instr:  "gain stage the kick to -7",
			action: ActionSetGain, target: "src.drums", gainDB: -7, hasGain: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in, err := p.Parse(tc.instr, proj)
			if err != nil {
				t.Fatalf("Parse returned error: %v", err)
			}
			if in.Action != tc.action {
				t.Errorf("action = %q, want %q (note: %s)", in.Action, tc.action, in.Note)
			}
			if tc.source != "" && in.Source != tc.source {
				t.Errorf("source = %q, want %q", in.Source, tc.source)
			}
			if in.Target != tc.target {
				t.Errorf("target = %q, want %q", in.Target, tc.target)
			}
			if in.Band != tc.band {
				t.Errorf("band = %q, want %q", in.Band, tc.band)
			}
			if tc.vst != "" && in.VST != tc.vst {
				t.Errorf("vst = %q, want %q", in.VST, tc.vst)
			}
			if in.HasGain != tc.hasGain {
				t.Errorf("hasGain = %v, want %v", in.HasGain, tc.hasGain)
			}
			if tc.hasGain && in.GainDB != tc.gainDB {
				t.Errorf("gainDb = %v, want %v", in.GainDB, tc.gainDB)
			}
		})
	}
}

// ─── noun / verb resolution edge cases ─────────────────────────────────────────

func TestNounResolution_LongestMatchWins(t *testing.T) {
	proj := sampleProject()
	// "lead guitar" must beat a bare "guitar".
	w, id, ok := resolveBus("the lead guitar", proj)
	if !ok || id != "bus.gtrLead" {
		t.Fatalf("lead guitar -> (%q,%q,%v), want bus.gtrLead", w, id, ok)
	}
	// "rhythm guitar" -> rhythm bus.
	_, id2, _ := resolveBus("the rhythm guitar", proj)
	if id2 != "bus.gtrRhythm" {
		t.Errorf("rhythm guitar -> %q, want bus.gtrRhythm", id2)
	}
}

func TestContainsWord_Boundaries(t *testing.T) {
	if containsWord("bassoon section", "bass") {
		t.Error("'bass' should not match inside 'bassoon'")
	}
	if !containsWord("the bass guitar", "bass") {
		t.Error("'bass' should match as a whole word")
	}
}

func TestDetectorResolution(t *testing.T) {
	proj := sampleProject()
	w, id, ok := resolveDetector("the kick", proj)
	if !ok || id != "src.drums.kick" {
		t.Fatalf("kick detector -> (%q,%q,%v), want src.drums.kick", w, id, ok)
	}
	// Non-drum trigger uses its source node.
	_, vid, vok := resolveDetector("the vocal", proj)
	if !vok || vid != "src.vox" {
		t.Errorf("vocal detector -> (%q,%v), want src.vox", vid, vok)
	}
}

// ─── Apply produces the right edge/chain/vst/gain ──────────────────────────────

func TestApply_Sidechain(t *testing.T) {
	proj := sampleProject()
	in := Intent{Action: ActionSidechain, Source: "src.drums.kick", Target: "bus.808",
		SourceWord: "kick", TargetWord: "bass", Band: "low", Note: "duck bass under the kick"}
	out, summary := Apply(proj, in)

	want := music.ProjEdge{From: "src.drums.kick", To: "bus.808.sidechainComp", Kind: "sidechain"}
	if !hasEdge(out.Routing, want) {
		t.Fatalf("expected sidechain edge %+v in %+v", want, out.Routing)
	}
	if summary == "" {
		t.Error("expected a non-empty summary")
	}
}

func TestApply_Route(t *testing.T) {
	proj := sampleProject()
	in := Intent{Action: ActionRoute, Source: "src.lead", Target: "bus.gtrLead",
		SourceWord: "lead", TargetWord: "guitar"}
	out, _ := Apply(proj, in)

	want := music.ProjEdge{From: "src.lead", To: "bus.gtrLead", Kind: "audio"}
	if !hasEdge(out.Routing, want) {
		t.Fatalf("expected audio edge %+v", want)
	}
	// Track Out should be updated.
	for _, tr := range out.Tracks {
		if tr.Node == "src.lead" && tr.Out != "bus.gtrLead" {
			t.Errorf("lead track Out = %q, want bus.gtrLead", tr.Out)
		}
	}
}

func TestApply_InsertChain(t *testing.T) {
	proj := sampleProject()
	in := Intent{Action: ActionInsertChain, Target: "bus.drums", TargetWord: "drums"}
	out, _ := Apply(proj, in)

	idx := busIndex(out, "bus.drums")
	if idx < 0 {
		t.Fatal("bus.drums missing after insert")
	}
	for _, typ := range standardChainTypes {
		if !hasFX(out.Buses[idx].FX, "drums."+typ) {
			t.Errorf("expected fx node drums.%s on the drum bus", typ)
		}
	}
}

func TestApply_InsertChain_CreatesMissingBus(t *testing.T) {
	proj := sampleProject()
	in := Intent{Action: ActionInsertChain, Target: "bus.gtrRhythm", TargetWord: "guitar"}
	out, _ := Apply(proj, in)
	if busIndex(out, "bus.gtrRhythm") < 0 {
		t.Fatal("expected the missing bus to be created")
	}
}

func TestApply_SetVST(t *testing.T) {
	proj := sampleProject()
	in := Intent{Action: ActionSetVST, Target: "bus.gtrLead", TargetWord: "lead", VST: "The Odin II"}
	out, _ := Apply(proj, in)

	idx := busIndex(out, "bus.gtrLead")
	if idx < 0 {
		t.Fatal("bus.gtrLead missing")
	}
	found := false
	for _, fx := range out.Buses[idx].FX {
		if fx.Type == "vst:The Odin II" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a vst:The Odin II fx node, got %+v", out.Buses[idx].FX)
	}
}

func TestApply_SetGain(t *testing.T) {
	proj := sampleProject()
	// Target is a track node; it should land on the track's out bus (bus.drums).
	in := Intent{Action: ActionSetGain, Target: "src.drums", TargetWord: "kick", GainDB: -7, HasGain: true}
	out, summary := Apply(proj, in)

	idx := busIndex(out, "bus.drums")
	if idx < 0 {
		t.Fatal("bus.drums missing")
	}
	found := false
	for _, fx := range out.Buses[idx].FX {
		if fx.Type == "gain:-7dB" && fx.ID == "drums.gain" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected gain:-7dB node drums.gain, got %+v", out.Buses[idx].FX)
	}
	if summary == "" {
		t.Error("expected a summary")
	}
}

// ─── immutability ──────────────────────────────────────────────────────────────

func TestApply_Immutable(t *testing.T) {
	proj := sampleProject()
	origEdges := len(proj.Routing)
	origDrumsFX := 0
	if i := busIndex(proj, "bus.drums"); i >= 0 {
		origDrumsFX = len(proj.Buses[i].FX)
	}

	in := Intent{Action: ActionSidechain, Source: "src.drums.kick", Target: "bus.808",
		SourceWord: "kick", TargetWord: "bass"}
	_, _ = Apply(proj, in)

	if len(proj.Routing) != origEdges {
		t.Errorf("input Routing mutated: %d edges now, was %d", len(proj.Routing), origEdges)
	}

	in2 := Intent{Action: ActionInsertChain, Target: "bus.drums", TargetWord: "drums"}
	_, _ = Apply(proj, in2)
	if i := busIndex(proj, "bus.drums"); i >= 0 && len(proj.Buses[i].FX) != origDrumsFX {
		t.Errorf("input bus FX mutated: %d now, was %d", len(proj.Buses[i].FX), origDrumsFX)
	}
}

// ─── determinism: same input -> byte-identical output ──────────────────────────

func TestApply_Deterministic(t *testing.T) {
	proj := sampleProject()
	in := Intent{Action: ActionSidechain, Source: "src.drums.kick", Target: "bus.808",
		SourceWord: "kick", TargetWord: "bass", Band: "low", Note: "duck"}

	a, _ := Apply(proj, in)
	b, _ := Apply(proj, in)
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	if string(ab) != string(bb) {
		t.Error("Apply is not deterministic across runs")
	}
}

func TestApply_Idempotent(t *testing.T) {
	proj := sampleProject()
	in := Intent{Action: ActionSidechain, Source: "src.drums.kick", Target: "bus.808",
		SourceWord: "kick", TargetWord: "bass"}
	once, _ := Apply(proj, in)
	twice, _ := Apply(once, in)
	if len(once.Routing) != len(twice.Routing) {
		t.Errorf("re-applying duplicated edges: %d -> %d", len(once.Routing), len(twice.Routing))
	}
}

func TestSortEdges_Order(t *testing.T) {
	edges := []music.ProjEdge{
		{From: "b", To: "z", Kind: "audio"},
		{From: "a", To: "z", Kind: "audio"},
		{From: "c", To: "a", Kind: "sidechain"},
	}
	got := sortEdges(append([]music.ProjEdge(nil), edges...))
	if got[0].To != "a" || got[1].From != "a" || got[2].From != "b" {
		t.Errorf("edges not sorted deterministically: %+v", got)
	}
}

// ─── unknown-instruction degrade ───────────────────────────────────────────────

func TestParse_Unknown(t *testing.T) {
	proj := sampleProject()
	p := DeterministicParser{}
	for _, instr := range []string{"", "make it sound cool", "xyzzy the frobnicator"} {
		in, err := p.Parse(instr, proj)
		if err != nil {
			t.Fatalf("Parse(%q) errored: %v", instr, err)
		}
		if in.Action != ActionUnknown {
			t.Errorf("Parse(%q) action = %q, want unknown", instr, in.Action)
		}
		if in.Note == "" {
			t.Errorf("Parse(%q) should carry a friendly note", instr)
		}
	}
}

func TestApply_Unknown_NoChange(t *testing.T) {
	proj := sampleProject()
	in := Intent{Action: ActionUnknown, Note: "nope"}
	out, summary := Apply(proj, in)
	if !reflect.DeepEqual(out.Routing, proj.Routing) {
		t.Error("Apply on Unknown should not change routing")
	}
	if summary == "" {
		t.Error("expected a friendly summary even for Unknown")
	}
}

// ─── model-parser stub + PickParser + FallbackParser ───────────────────────────

func TestModelParser_DegradesViaFallback(t *testing.T) {
	proj := sampleProject()
	// The model runModel is a stub that errors; FallbackParser must reach the
	// deterministic parser and still produce the right Intent.
	fp := FallbackParser{Primary: &ModelParser{}, Secondary: DeterministicParser{}}
	in, err := fp.Parse("sidechain the bass to the kick", proj)
	if err != nil {
		t.Fatalf("FallbackParser errored: %v", err)
	}
	if in.Action != ActionSidechain || in.Source != "src.drums.kick" {
		t.Errorf("fallback did not reach deterministic parser: %+v", in)
	}
}

func TestPickParser_DegradesWhenModelAbsent(t *testing.T) {
	// With no model binary/weights on the test box, PickParser must return the
	// deterministic parser (silent degrade).
	p := PickParser()
	if _, ok := p.(DeterministicParser); !ok {
		t.Skip("a model binary+weights resolved on this box; degrade path not exercised")
	}
}

func TestDecodeModelIntent_Contract(t *testing.T) {
	out := `blah blah {"action":"sidechain","source":"src.drums.kick","target":"bus.808","band":"low","vst":"","gainDb":0,"hasGain":false,"note":"duck"} trailing`
	in, err := decodeModelIntent(out)
	if err != nil {
		t.Fatalf("decode errored: %v", err)
	}
	if in.Action != ActionSidechain || in.Target != "bus.808" || in.Band != "low" {
		t.Errorf("decoded intent wrong: %+v", in)
	}
}

func TestDecodeModelIntent_BadActionDegrades(t *testing.T) {
	in, err := decodeModelIntent(`{"action":"frobnicate","note":"x"}`)
	if err != nil {
		t.Fatalf("decode errored: %v", err)
	}
	if in.Action != ActionUnknown {
		t.Errorf("bad action should degrade to unknown, got %q", in.Action)
	}
}

func TestBuildModelPrompt_IncludesIds(t *testing.T) {
	proj := sampleProject()
	prompt := buildModelPrompt("sidechain the bass to the kick", proj)
	for _, want := range []string{"bus.808", "bass", "sidechain the bass to the kick", "insertChain"} {
		if !contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

// ─── helpers ───────────────────────────────────────────────────────────────────

func hasEdge(edges []music.ProjEdge, want music.ProjEdge) bool {
	for _, e := range edges {
		if e.From == want.From && e.To == want.To && e.Kind == want.Kind {
			return true
		}
	}
	return false
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
