package fxchain

import (
	"path/filepath"
	"testing"
)

func TestDefaultChainsEmptyButPresent(t *testing.T) {
	c := DefaultChains()
	if len(c.ByBus) != len(StandardBuses) {
		t.Fatalf("want %d buses, got %d", len(StandardBuses), len(c.ByBus))
	}
	for _, b := range StandardBuses {
		ch, ok := c.ByBus[b]
		if !ok {
			t.Fatalf("standard bus %q missing", b)
		}
		if len(ch.Plugins) != 0 {
			t.Errorf("bus %q must default to EMPTY (taste is Jordan's), got %d plugins", b, len(ch.Plugins))
		}
		if ch.Bus != b {
			t.Errorf("bus %q chain has wrong Bus field %q", b, ch.Bus)
		}
	}
}

func TestAddAppendsInOrder(t *testing.T) {
	c := DefaultChains()
	c = c.Add("DRUMS", Plugin{Name: "Pro-C 2"})
	c = c.Add("DRUMS", Plugin{Name: "Pro-Q 3"})
	c = c.Add("DRUMS", Plugin{Name: "Saturn 2"})

	got := c.Get("DRUMS").Plugins
	want := []string{"Pro-C 2", "Pro-Q 3", "Saturn 2"}
	if len(got) != len(want) {
		t.Fatalf("want %d plugins, got %d", len(want), len(got))
	}
	for i := range want {
		if got[i].Name != want[i] {
			t.Errorf("plugin %d: want %q, got %q", i, want[i], got[i].Name)
		}
	}
}

func TestAddIsImmutable(t *testing.T) {
	base := DefaultChains()
	added := base.Add("BASS", Plugin{Name: "Saturator"})
	if len(base.Get("BASS").Plugins) != 0 {
		t.Errorf("Add mutated the receiver: BASS should still be empty, got %d", len(base.Get("BASS").Plugins))
	}
	if len(added.Get("BASS").Plugins) != 1 {
		t.Errorf("Add result missing the plugin, got %d", len(added.Get("BASS").Plugins))
	}
}

func TestAddCreatesUnknownBus(t *testing.T) {
	c := DefaultChains().Add("CUSTOMBUS", Plugin{Name: "Limiter"})
	got := c.Get("CUSTOMBUS")
	if len(got.Plugins) != 1 || got.Plugins[0].Name != "Limiter" {
		t.Fatalf("Add to unknown bus failed: %+v", got)
	}
	if got.Bus != "CUSTOMBUS" {
		t.Errorf("new bus has wrong Bus field %q", got.Bus)
	}
}

func TestSetChainReplaces(t *testing.T) {
	c := DefaultChains().Add("VOCALS", Plugin{Name: "Old"})
	c = c.SetChain("VOCALS", []Plugin{{Name: "DeEsser"}, {Name: "Comp"}})
	got := c.Get("VOCALS").Plugins
	if len(got) != 2 || got[0].Name != "DeEsser" || got[1].Name != "Comp" {
		t.Fatalf("SetChain did not replace correctly: %+v", got)
	}
}

func TestSetChainIsImmutable(t *testing.T) {
	src := []Plugin{{Name: "A"}, {Name: "B"}}
	c := DefaultChains().SetChain("FX", src)
	src[0].Name = "MUTATED" // mutate the caller's slice
	if c.Get("FX").Plugins[0].Name != "A" {
		t.Error("SetChain aliased the caller's slice")
	}
}

func TestGetUnknownBusReturnsEmpty(t *testing.T) {
	c := Chains{ByBus: map[string]Chain{}}
	got := c.Get("NOPE")
	if got.Bus != "NOPE" || got.Plugins == nil || len(got.Plugins) != 0 {
		t.Fatalf("Get on unknown bus should return empty non-nil chain, got %+v", got)
	}
}

func TestBusIsolation(t *testing.T) {
	c := DefaultChains()
	c = c.Add("DRUMS", Plugin{Name: "DrumComp"})
	c = c.Add("BASS", Plugin{Name: "BassSat"})
	if len(c.Get("DRUMS").Plugins) != 1 || c.Get("DRUMS").Plugins[0].Name != "DrumComp" {
		t.Error("DRUMS chain wrong after BASS add")
	}
	if len(c.Get("BASS").Plugins) != 1 || c.Get("BASS").Plugins[0].Name != "BassSat" {
		t.Error("BASS chain wrong")
	}
	if len(c.Get("SYNTH").Plugins) != 0 {
		t.Error("SYNTH should be untouched")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BECKY_FXCHAINS", filepath.Join(dir, "fxchains.json"))

	want := DefaultChains().
		Add("DRUMS", Plugin{Name: "Pro-C 2", ClassID: "DRMCMP01", PresetRef: "glue.vstpreset"}).
		Add("DRUMS", Plugin{Name: "Pro-Q 3", Bypass: true}).
		Add("VOCALS", Plugin{Name: "Soothe2", PresetRef: "vox.vstpreset"})

	if err := Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got := Load()

	d := got.Get("DRUMS").Plugins
	if len(d) != 2 {
		t.Fatalf("DRUMS want 2 plugins after round-trip, got %d", len(d))
	}
	if d[0].Name != "Pro-C 2" || d[0].ClassID != "DRMCMP01" || d[0].PresetRef != "glue.vstpreset" {
		t.Errorf("DRUMS[0] round-trip mismatch: %+v", d[0])
	}
	if !d[1].Bypass {
		t.Error("DRUMS[1] Bypass not preserved")
	}
	v := got.Get("VOCALS").Plugins
	if len(v) != 1 || v[0].Name != "Soothe2" || v[0].PresetRef != "vox.vstpreset" {
		t.Errorf("VOCALS round-trip mismatch: %+v", v)
	}
}

func TestLoadFallsBackToDefaultWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BECKY_FXCHAINS", filepath.Join(dir, "does-not-exist.json"))
	got := Load()
	if len(got.ByBus) != len(StandardBuses) {
		t.Fatalf("absent config should fall back to DefaultChains, got %d buses", len(got.ByBus))
	}
	for _, b := range StandardBuses {
		if len(got.Get(b).Plugins) != 0 {
			t.Errorf("fallback bus %q should be empty", b)
		}
	}
}

func TestPathHonorsEnvOverride(t *testing.T) {
	t.Setenv("BECKY_FXCHAINS", "/tmp/custom/fx.json")
	if Path() != "/tmp/custom/fx.json" {
		t.Fatalf("Path should honor BECKY_FXCHAINS, got %q", Path())
	}
}

func TestBusesSorted(t *testing.T) {
	c := Chains{ByBus: map[string]Chain{
		"ZED":   {Bus: "ZED"},
		"ALPHA": {Bus: "ALPHA"},
		"MID":   {Bus: "MID"},
	}}
	got := c.Buses()
	want := []string{"ALPHA", "MID", "ZED"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Buses not sorted: want %v, got %v", want, got)
		}
	}
}
