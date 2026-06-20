package hydrogen

import (
	"testing"

	"becky-go/internal/samplelib"
)

// fakeIndex builds an in-memory samplelib.Index with the given samples (no disk walk).
func fakeIndex(samples ...samplelib.Sample) *samplelib.Index {
	idx := &samplelib.Index{Root: "fake", RoleCounts: map[string]int{}}
	idx.Samples = append(idx.Samples, samples...)
	for _, s := range idx.Samples {
		idx.RoleCounts[s.Role]++
	}
	return idx
}

func TestKitFromLibrary_PicksByRole(t *testing.T) {
	idx := fakeIndex(
		samplelib.Sample{Path: "X:/lib/kick_01.wav", Name: "kick_01.wav", Role: samplelib.RoleKick, Kind: samplelib.KindOneShot},
		samplelib.Sample{Path: "X:/lib/snare_clap.wav", Name: "snare_clap.wav", Role: samplelib.RoleSnare, Kind: samplelib.KindOneShot},
		samplelib.Sample{Path: "X:/lib/hatC.wav", Name: "hatC.wav", Role: samplelib.RoleHat, Kind: samplelib.KindOneShot},
	)
	kit, missing := KitFromLibrary("Beat", idx, DefaultBeatVoices)
	if len(missing) != 0 {
		t.Fatalf("missing = %v, want none", missing)
	}
	if len(kit.Instruments) != 3 {
		t.Fatalf("instruments = %d, want 3", len(kit.Instruments))
	}
	if kit.Instruments[0].Name != "Kick" || kit.Instruments[0].MidiNote != MIDIKick {
		t.Errorf("inst0 = %+v", kit.Instruments[0])
	}
	if kit.Instruments[0].Layers[0].Filename != "X:/lib/kick_01.wav" {
		t.Errorf("kick sample = %q", kit.Instruments[0].Layers[0].Filename)
	}
	// IDs are 0,1,2 in voice order.
	for i, inst := range kit.Instruments {
		if inst.ID != i {
			t.Errorf("inst %d has ID %d", i, inst.ID)
		}
	}
}

func TestKitFromLibrary_DeterministicSelection(t *testing.T) {
	// Two kicks; the lexicographically-first path must be chosen, regardless of insert order.
	idx := fakeIndex(
		samplelib.Sample{Path: "X:/lib/z_kick.wav", Name: "z_kick.wav", Role: samplelib.RoleKick, Kind: samplelib.KindOneShot},
		samplelib.Sample{Path: "X:/lib/a_kick.wav", Name: "a_kick.wav", Role: samplelib.RoleKick, Kind: samplelib.KindOneShot},
	)
	kit, _ := KitFromLibrary("Beat", idx, []BeatVoice{{Name: "Kick", Role: samplelib.RoleKick, MidiNote: MIDIKick}})
	if got := kit.Instruments[0].Layers[0].Filename; got != "X:/lib/a_kick.wav" {
		t.Errorf("chose %q, want lexicographically-first X:/lib/a_kick.wav", got)
	}
}

func TestKitFromLibrary_NameHintPreferred(t *testing.T) {
	idx := fakeIndex(
		samplelib.Sample{Path: "X:/lib/a_kick.wav", Name: "a_kick.wav", Role: samplelib.RoleKick, Kind: samplelib.KindOneShot},
		samplelib.Sample{Path: "X:/lib/z_808_kick.wav", Name: "z_808_kick.wav", Role: samplelib.RoleKick, Kind: samplelib.KindOneShot},
	)
	kit, _ := KitFromLibrary("Beat", idx, []BeatVoice{
		{Name: "Kick", Role: samplelib.RoleKick, MidiNote: MIDIKick, NameHint: "808"},
	})
	if got := kit.Instruments[0].Layers[0].Filename; got != "X:/lib/z_808_kick.wav" {
		t.Errorf("with hint=808 chose %q, want the 808 kick even though it sorts later", got)
	}
}

func TestKitFromLibrary_MissingReported(t *testing.T) {
	// Only a kick; snare and hat are missing -> reported, not fatal.
	idx := fakeIndex(
		samplelib.Sample{Path: "X:/lib/kick.wav", Name: "kick.wav", Role: samplelib.RoleKick, Kind: samplelib.KindOneShot},
	)
	kit, missing := KitFromLibrary("Beat", idx, DefaultBeatVoices)
	if len(kit.Instruments) != 1 {
		t.Errorf("instruments = %d, want 1 (just kick)", len(kit.Instruments))
	}
	if len(missing) != 2 {
		t.Errorf("missing = %v, want [Snare Hat]", missing)
	}
}

func TestKitFromLibrary_NameFallbackSkipsLoops(t *testing.T) {
	// No role match, but a file literally named "kick" exists. A loop should be skipped;
	// the one-shot named match should win.
	idx := fakeIndex(
		samplelib.Sample{Path: "X:/lib/kick_loop.wav", Name: "kick_loop.wav", Role: samplelib.RoleUnknown, Kind: samplelib.KindLoop},
		samplelib.Sample{Path: "X:/lib/kick_hit.wav", Name: "kick_hit.wav", Role: samplelib.RoleUnknown, Kind: samplelib.KindOneShot},
	)
	kit, missing := KitFromLibrary("Beat", idx, []BeatVoice{{Name: "Kick", Role: samplelib.RoleKick, MidiNote: MIDIKick}})
	if len(missing) != 0 {
		t.Fatalf("missing = %v, want none (name fallback should find kick_hit)", missing)
	}
	if got := kit.Instruments[0].Layers[0].Filename; got != "X:/lib/kick_hit.wav" {
		t.Errorf("chose %q, want the one-shot kick_hit (loop must be skipped)", got)
	}
}

func TestKitFromLibrary_NilIndex(t *testing.T) {
	kit, missing := KitFromLibrary("Beat", nil, DefaultBeatVoices)
	if len(kit.Instruments) != 0 {
		t.Errorf("nil index produced %d instruments, want 0", len(kit.Instruments))
	}
	if len(missing) != 3 {
		t.Errorf("nil index missing = %v, want all 3", missing)
	}
}

func TestFindHydrogenCLI_EnvOverride(t *testing.T) {
	// Point the override at a real file (this test binary) and confirm it's returned.
	exe, err := osExecutable()
	if err != nil {
		t.Skip("cannot resolve test executable")
	}
	t.Setenv("BECKY_HYDROGEN_CLI", exe)
	got, ok := FindHydrogenCLI()
	if !ok || got != exe {
		t.Errorf("FindHydrogenCLI with override = (%q,%v), want (%q,true)", got, ok, exe)
	}
}

func TestExportSong_MissingCLI(t *testing.T) {
	// An explicit bad CLI path -> start error, not a panic, and no output file.
	err := ExportSong("song.h2song", "out.wav", ExportOptions{CLIPath: "X:/nope/does-not-exist.exe", Timeout: 0})
	if err == nil {
		t.Fatal("ExportSong with bogus CLI should error")
	}
}
