package main

import (
	"os"
	"testing"
)

// TestMain points LOCALAPPDATA at a throwaway dir for the WHOLE package run:
// many app tests load reels/folders, which now persist clip colours - without
// this every `go test` littered the developer's real %LOCALAPPDATA%\becky\colors
// with files keyed to temp paths that can never be read again.
//
// It also stubs the qmd seams (warmQmd, runQmdUpdate) to no-ops for the whole
// binary. Dozens of tests call OpenFolder (which backgrounds warmQmd) and
// several exercise a real transcribe/forensic success path (which triggers
// runQmdUpdate) — without this default every `go test` run would shell the
// REAL qmd binary that many times, the exact "no test shells real X" rule
// this package otherwise holds for becky-judge/becky-hits/becky-transcribe.
// A test that specifically wants to assert on qmd-update behavior overrides
// it locally (fakeQmdUpdate in forensic_test.go) and restores it via
// t.Cleanup, which lands back on this no-op, not the real qmd.Update.
func TestMain(m *testing.M) {
	d, err := os.MkdirTemp("", "becky-colors-test")
	if err == nil {
		os.Setenv("LOCALAPPDATA", d)
		defer os.RemoveAll(d)
	}
	warmQmd = func() {}
	runQmdUpdate = func() error { return nil }
	os.Exit(func() int { defer os.RemoveAll(d); return m.Run() }())
}

// Jordan's ordering, quoted from his feedback: "#14FF39 (video 1), #00AEEF
// (video 2), #DC143C (video 3)". Colours are assigned in order of first
// appearance, not by any property of the path.
func TestClipColorAssignsPaletteInOrder(t *testing.T) {
	ResetClipColors()
	want := []string{"#14FF39", "#00AEEF", "#DC143C"}
	got := []string{
		clipColor(`X:\v\video1.mp4`),
		clipColor(`X:\v\video2.mp4`),
		clipColor(`X:\v\video3.mp4`),
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("video %d = %s, want %s", i+1, got[i], want[i])
		}
	}
}

// THE LOAD-BEARING ONE, in his words: "if user deletes all clips from video 2,
// then clips from video 3 change color to #00AEEF. This should not happen."
// Deleting a source must never release its colour.
func TestDeletingAVideoDoesNotRecolourTheOthers(t *testing.T) {
	ResetClipColors()
	clipColor(`X:\v\video1.mp4`)
	two := clipColor(`X:\v\video2.mp4`)
	three := clipColor(`X:\v\video3.mp4`)

	// Every clip of video 2 is deleted; the app keeps asking about 1 and 3.
	for i := 0; i < 50; i++ {
		if c := clipColor(`X:\v\video3.mp4`); c != three {
			t.Fatalf("video 3 became %s, want %s — deleting video 2 must not recolour it", c, three)
		}
	}
	// And if video 2 comes back it is still its own original colour.
	if c := clipColor(`X:\v\video2.mp4`); c != two {
		t.Errorf("video 2 returned as %s, want its original %s", c, two)
	}
}

func TestClipColorIgnoresCaseAndSlashStyle(t *testing.T) {
	ResetClipColors()
	want := clipColor(`X:\Videos\clip.mp4`)
	for _, v := range []string{`x:\videos\clip.mp4`, `X:/Videos/clip.mp4`, `  X:\Videos\clip.mp4  `} {
		if got := clipColor(v); got != want {
			t.Errorf("clipColor(%q) = %q, want %q — one file must not get two colours", v, got, want)
		}
	}
}

func TestSeedClipColorsFixesTheOrderAtLoad(t *testing.T) {
	ResetClipColors()
	SeedClipColors([]string{`X:\v\a.mp4`, `X:\v\b.mp4`})
	// Asking about b first afterwards must NOT make it video 1.
	if got := clipColor(`X:\v\b.mp4`); got != "#00AEEF" {
		t.Errorf("b = %s, want #00AEEF — load order decides, not query order", got)
	}
	if got := clipColor(`X:\v\a.mp4`); got != "#14FF39" {
		t.Errorf("a = %s, want #14FF39", got)
	}
}

func TestClipColorEmptySourceGivesNoColor(t *testing.T) {
	ResetClipColors()
	if got := clipColor("   "); got != "" {
		t.Errorf("clipColor(blank) = %q, want empty so the app keeps its own default", got)
	}
}

// Item 26 (2026-07-24, Jordan: "currently it seems to just do random"). The per-folder
// colour cache accumulated FOREIGN sources (external drops, dev/test churn), which consumed
// palette slots and scrambled the case videos' order. ReseedClipColorsInOrder must re-derive
// the palette STRICTLY from the current reel's first-appearance order, overriding that
// polluted history so video 1 = #14FF39, video 2 = #00AEEF, video 3 = #DC143C.
func TestReseedClipColorsInOrderOverridesPollution(t *testing.T) {
	ResetClipColors()
	// A polluted history: two foreign sources assigned first would push video1 to palette[2].
	clipColor(`X:\foreign\a.mp4`)
	clipColor(`X:\foreign\b.mp4`)
	clipColor(`X:\case\video1.mp4`)
	// Reseed from the reel's own order (video1 appears twice; distinct order is 1,2,3).
	ReseedClipColorsInOrder([]string{`X:\case\video1.mp4`, `X:\case\video2.mp4`, `X:\case\video3.mp4`, `X:\case\video1.mp4`})
	for i, src := range []string{`X:\case\video1.mp4`, `X:\case\video2.mp4`, `X:\case\video3.mp4`} {
		if got := clipColor(src); got != clipPalette[i] {
			t.Errorf("source %d = %s, want %s (reel-order palette, not polluted history)", i+1, got, clipPalette[i])
		}
	}
	// The foreign sources were cleared by the reseed; asking about one now assigns the NEXT
	// palette slot (4th), never stealing a case video's colour.
	if got := clipColor(`X:\foreign\a.mp4`); got != clipPalette[3] {
		t.Errorf("re-added foreign source = %s, want %s (next slot)", got, clipPalette[3])
	}
}

// 2026-07-22, Jordan: "the colors are going wild... that color does not change
// for the rest of the project." The in-memory map died with every engine
// process, so a restart re-assigned colours in that session's first-appearance
// order. This asserts the on-disk assignment survives a restart even when the
// appearance ORDER changes (his real case: clips deleted/reordered mid-session,
// then the app relaunched).
func TestClipColorsSurviveEngineRestart(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	ResetClipColors()
	LoadClipColors(`E:\case`)
	one := clipColor(`E:\case\video1.mp4`)
	two := clipColor(`E:\case\video2.mp4`)
	three := clipColor(`E:\case\video3.mp4`)

	// The engine restarts: memory is gone, the project file remains.
	ResetClipColors()
	LoadClipColors(`E:\case`)

	// Video 3 appears FIRST this session (1 and 2 were cut from the reel).
	// It must still wear ITS colour, not inherit video 1's green.
	if got := clipColor(`E:\case\video3.mp4`); got != three {
		t.Errorf("after restart video 3 = %s, want its frozen %s", got, three)
	}
	if got := clipColor(`E:\case\video1.mp4`); got != one {
		t.Errorf("after restart video 1 = %s, want its frozen %s", got, one)
	}
	if got := clipColor(`E:\case\video2.mp4`); got != two {
		t.Errorf("after restart video 2 = %s, want its frozen %s", got, two)
	}
	// A brand-new source continues the sequence instead of reusing a slot.
	if got := clipColor(`E:\case\video4.mp4`); got != "#8A2BE2" {
		t.Errorf("new video 4 after restart = %s, want #8A2BE2 (slot 4)", got)
	}
}

// Colours assigned BEFORE the project file loads (the forensic launcher loads
// the reel before open_folder) merge into it; disk wins on conflict.
func TestLoadClipColorsDiskWinsOverPreloadAssignments(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	ResetClipColors()
	LoadClipColors(`E:\case2`)
	green := clipColor(`E:\case2\a.mp4`) // #14FF39 on disk now
	ResetClipColors()

	// Fresh process: reel preload asks about b then a BEFORE the folder opens.
	clipColor(`E:\case2\b.mp4`) // gets green in memory, wrongly
	LoadClipColors(`E:\case2`)  // disk arrives
	if got := clipColor(`E:\case2\a.mp4`); got != green {
		t.Errorf("a = %s, want disk's frozen %s", got, green)
	}
}
