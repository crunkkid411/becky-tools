package sampler

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPickLayer(t *testing.T) {
	s := Sound{
		Name: "snare",
		Layers: []Layer{
			{VelLo: 1, VelHi: 63, RoundRobin: []Variant{{SamplePath: "soft.wav"}}},
			{VelLo: 64, VelHi: 127, RoundRobin: []Variant{{SamplePath: "hard.wav"}}},
		},
	}
	cases := []struct {
		name string
		vel  int
		want string
	}{
		{"soft hit", 30, "soft.wav"},
		{"low boundary", 1, "soft.wav"},
		{"upper of soft", 63, "soft.wav"},
		{"lower of hard", 64, "hard.wav"},
		{"hard hit", 120, "hard.wav"},
		{"clamp high", 200, "hard.wav"},
		{"clamp low", 0, "soft.wav"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l, ok := PickLayer(s, c.vel)
			if !ok {
				t.Fatalf("expected a layer for vel %d", c.vel)
			}
			if got := l.RoundRobin[0].SamplePath; got != c.want {
				t.Fatalf("vel %d: got %q want %q", c.vel, got, c.want)
			}
		})
	}
}

func TestPickLayerNoLayers(t *testing.T) {
	if _, ok := PickLayer(Sound{Name: "empty"}, 64); ok {
		t.Fatal("expected ok=false for a Sound with no layers")
	}
}

func TestPickLayerGapFallsBackToNearest(t *testing.T) {
	// Ranges leave a gap 50..59; a hit there must still produce a layer.
	s := Sound{Layers: []Layer{
		{VelLo: 1, VelHi: 49, RoundRobin: []Variant{{SamplePath: "lo.wav"}}},
		{VelLo: 60, VelHi: 127, RoundRobin: []Variant{{SamplePath: "hi.wav"}}},
	}}
	l, ok := PickLayer(s, 52) // closer to lo (49) than hi (60)
	if !ok {
		t.Fatal("expected a fallback layer")
	}
	if l.RoundRobin[0].SamplePath != "lo.wav" {
		t.Fatalf("expected nearest layer lo.wav, got %q", l.RoundRobin[0].SamplePath)
	}
}

func TestSelectVariantSequentialCycling(t *testing.T) {
	layer := Layer{
		RRMode: Sequential,
		RoundRobin: []Variant{
			{SamplePath: "a.wav"},
			{SamplePath: "b.wav"},
			{SamplePath: "c.wav"},
		},
	}
	want := []string{"a.wav", "b.wav", "c.wav", "a.wav", "b.wav", "c.wav", "a.wav"}
	counter := 0
	for i, w := range want {
		v, next := SelectVariant(layer, counter)
		if v.SamplePath != w {
			t.Fatalf("step %d: got %q want %q", i, v.SamplePath, w)
		}
		counter = next
	}
	if counter != 1 {
		t.Fatalf("after 7 steps over 3 variants counter should be 1, got %d", counter)
	}
}

func TestSelectVariantWraparoundAndNegative(t *testing.T) {
	layer := Layer{RoundRobin: []Variant{{SamplePath: "a"}, {SamplePath: "b"}}}
	// A large counter must wrap deterministically.
	v, next := SelectVariant(layer, 5) // 5 % 2 = 1 -> "b"
	if v.SamplePath != "b" || next != 0 {
		t.Fatalf("large counter: got %q,%d want b,0", v.SamplePath, next)
	}
	// A negative counter must normalize, not panic or index OOB.
	v, next = SelectVariant(layer, -1) // -1 -> 1 -> "b"
	if v.SamplePath != "b" || next != 0 {
		t.Fatalf("negative counter: got %q,%d want b,0", v.SamplePath, next)
	}
}

func TestSelectVariantEmpty(t *testing.T) {
	v, next := SelectVariant(Layer{}, 7)
	if v.SamplePath != "" || next != 7 {
		t.Fatalf("empty round-robin should return zero variant and unchanged counter, got %q,%d", v.SamplePath, next)
	}
}

func TestChokeFields(t *testing.T) {
	closed := Sound{Name: "chh", ChokeGroup: 1, ChokeMode: Fast}
	open := Sound{Name: "ohh", ChokeGroup: 1, OffBy: []int{1}, ChokeMode: Fast}
	if closed.ChokeGroup != open.ChokeGroup {
		t.Fatal("hat pair must share a choke group")
	}
	if len(open.OffBy) != 1 || open.OffBy[0] != 1 {
		t.Fatalf("open hat should be off_by group 1, got %v", open.OffBy)
	}
}

func TestLoopModeRoundTrip(t *testing.T) {
	for _, m := range []LoopMode{NoLoop, OneShot, LoopContinuous, LoopSustain} {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		// Must marshal as a readable string, not an int.
		if !strings.HasPrefix(string(b), `"`) {
			t.Fatalf("LoopMode %v marshalled as %s, expected a quoted string", m, b)
		}
		var got LoopMode
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatal(err)
		}
		if got != m {
			t.Fatalf("round trip: got %v want %v (%s)", got, m, b)
		}
	}
}

func TestEnumStringsAreReadable(t *testing.T) {
	checks := map[string]string{
		string(must(json.Marshal(OneShot))):        `"one_shot"`,
		string(must(json.Marshal(LoopContinuous))): `"loop_continuous"`,
		string(must(json.Marshal(Sequential))):     `"sequential"`,
		string(must(json.Marshal(Random))):         `"random"`,
		string(must(json.Marshal(Fast))):           `"fast"`,
		string(must(json.Marshal(Normal))):         `"normal"`,
	}
	for got, want := range checks {
		if got != want {
			t.Fatalf("got %s want %s", got, want)
		}
	}
}

func TestEnumUnknownDegrades(t *testing.T) {
	var lm LoopMode = OneShot
	if err := json.Unmarshal([]byte(`"nonsense"`), &lm); err != nil {
		t.Fatal(err)
	}
	if lm != NoLoop {
		t.Fatalf("unknown loop mode should degrade to NoLoop, got %v", lm)
	}
	var rr RRMode = Random
	_ = json.Unmarshal([]byte(`"???"`), &rr)
	if rr != Sequential {
		t.Fatalf("unknown rr mode should degrade to Sequential, got %v", rr)
	}
}

func TestVariantDefaults(t *testing.T) {
	v := Variant{}.Normalize()
	if v.PitchKeycenter != DefaultKeycenter {
		t.Fatalf("default keycenter should be %d, got %d", DefaultKeycenter, v.PitchKeycenter)
	}
}

func TestNormalizeClamps(t *testing.T) {
	v := Variant{
		PitchKeycenter: 999,
		Transpose:      500,
		Tune:           -9999,
		Pan:            5,
		StartFrame:     -10,
		LoopEnd:        -3,
	}.Normalize()
	if v.PitchKeycenter != 127 {
		t.Fatalf("keycenter clamp: %d", v.PitchKeycenter)
	}
	if v.Transpose != 127 {
		t.Fatalf("transpose clamp: %d", v.Transpose)
	}
	if v.Tune != -100 {
		t.Fatalf("tune clamp: %d", v.Tune)
	}
	if v.Pan != 1 {
		t.Fatalf("pan clamp: %v", v.Pan)
	}
	if v.StartFrame != 0 || v.LoopEnd != 0 {
		t.Fatalf("negative frames should clamp to 0: %d %d", v.StartFrame, v.LoopEnd)
	}
}

func TestLayerNormalizeVelRange(t *testing.T) {
	l := Layer{VelLo: 100, VelHi: 20}.Normalize() // reversed
	if l.VelLo != 20 || l.VelHi != 100 {
		t.Fatalf("expected ordered range 20..100, got %d..%d", l.VelLo, l.VelHi)
	}
	l2 := Layer{VelLo: 0, VelHi: 0}.Normalize() // zeros -> full range
	if l2.VelLo != 1 || l2.VelHi != 127 {
		t.Fatalf("zero range should become 1..127, got %d..%d", l2.VelLo, l2.VelHi)
	}
}

func TestKit16JSONStability(t *testing.T) {
	dir := t.TempDir()
	var k Kit16
	k.Name = "test kit"
	k.Pads[0] = Sound{
		Name:      "kick",
		ChokeMode: Fast,
		OneShot:   true,
		Layers: []Layer{{
			VelLo: 1, VelHi: 127, RRMode: Sequential,
			RoundRobin: []Variant{
				{SamplePath: "kick_01.wav", PitchKeycenter: 60, LoopMode: OneShot},
				{SamplePath: "kick_02.wav", PitchKeycenter: 60, LoopMode: OneShot},
			},
		}},
	}
	k.Pads[2] = Sound{Name: "chh", ChokeGroup: 1, ChokeMode: Fast}
	k.Pads[3] = Sound{Name: "ohh", ChokeGroup: 1, OffBy: []int{1}, ChokeMode: Fast}

	path := filepath.Join(dir, "kit.json")
	if err := k.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != k.Name {
		t.Fatalf("name mismatch: %q", got.Name)
	}
	if got.Pads[0].Name != "kick" || len(got.Pads[0].Layers[0].RoundRobin) != 2 {
		t.Fatalf("kick pad did not round trip: %+v", got.Pads[0])
	}
	if got.Pads[3].OffBy[0] != 1 {
		t.Fatalf("off_by did not round trip: %+v", got.Pads[3])
	}

	// Re-save the loaded kit; bytes must be byte-identical (deterministic output).
	path2 := filepath.Join(dir, "kit2.json")
	if err := got.Save(path2); err != nil {
		t.Fatal(err)
	}
	b1 := must(json.MarshalIndent(k, "", "  "))
	b2 := must(json.MarshalIndent(got, "", "  "))
	if !reflect.DeepEqual(b1, b2) {
		t.Fatalf("kit JSON not stable across save/load:\n%s\n---\n%s", b1, b2)
	}
}

func TestSoundRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := Sound{
		Name:      "snare",
		ChokeMode: Normal,
		Layers: []Layer{
			{VelLo: 1, VelHi: 63, RRMode: Random, RoundRobin: []Variant{{SamplePath: "s_soft.wav", PitchKeycenter: 60}}},
			{VelLo: 64, VelHi: 127, RRMode: Sequential, RoundRobin: []Variant{
				{SamplePath: "s_hard_1.wav", PitchKeycenter: 60, Tune: 5, Pan: -0.3},
				{SamplePath: "s_hard_2.wav", PitchKeycenter: 60},
			}},
		},
	}
	path := filepath.Join(dir, "snare.json")
	if err := SaveSound(path, s); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSound(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.ChokeMode != Normal {
		t.Fatalf("choke mode lost: %v", got.ChokeMode)
	}
	if got.Layers[1].RoundRobin[0].Tune != 5 || got.Layers[1].RoundRobin[0].Pan != -0.3 {
		t.Fatalf("tune/pan lost: %+v", got.Layers[1].RoundRobin[0])
	}
	if got.Layers[0].RRMode != Random {
		t.Fatalf("rr mode lost: %v", got.Layers[0].RRMode)
	}
}

func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}
