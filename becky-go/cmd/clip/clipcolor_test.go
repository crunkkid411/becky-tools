package main

import "testing"

// The load-bearing property: a source's colour never changes. Jordan's rule is
// that deleting every clip from video 2 must not recolour video 3, so the
// colour cannot depend on what else is on the timeline.
func TestClipColorIsPermanentPerSource(t *testing.T) {
	const a = `X:\Videos\2025\11_November\Rendered\FLYV9992_convertedsnow2.mp4`
	first := clipColor(a)
	if first == "" {
		t.Fatal("clipColor returned empty for a real path")
	}
	for i := 0; i < 100; i++ {
		if got := clipColor(a); got != first {
			t.Fatalf("call %d returned %q, want %q — a source's colour must never change", i, got, first)
		}
	}
}

func TestClipColorIgnoresCaseAndSlashStyle(t *testing.T) {
	want := clipColor(`X:\Videos\clip.mp4`)
	for _, variant := range []string{
		`x:\videos\clip.mp4`,
		`X:/Videos/clip.mp4`,
		`  X:\Videos\clip.mp4  `,
	} {
		if got := clipColor(variant); got != want {
			t.Errorf("clipColor(%q) = %q, want %q — the same file must not get two colours", variant, got, want)
		}
	}
}

func TestClipColorIsAlwaysFromThePalette(t *testing.T) {
	inPalette := map[string]bool{}
	for _, c := range clipPalette {
		inPalette[c] = true
	}
	for _, src := range []string{`a.mp4`, `b.mp4`, `X:\x\y\z.mov`, `E:\TakingBack2007\v.mkv`} {
		if got := clipColor(src); !inPalette[got] {
			t.Errorf("clipColor(%q) = %q, which is not one of Jordan's eight colours", src, got)
		}
	}
}

func TestClipColorEmptySourceGivesNoColor(t *testing.T) {
	if got := clipColor("   "); got != "" {
		t.Errorf("clipColor(blank) = %q, want empty so the app keeps its own default", got)
	}
}

// Different sources should mostly get different colours - the point is telling
// them apart. With 8 colours some collision is unavoidable, but a handful of
// real filenames must not all land on one.
func TestClipColorSpreadsRealFilenames(t *testing.T) {
	seen := map[string]int{}
	for _, s := range []string{
		`X:\v\FLYV9992_convertedsnow2.mp4`, `X:\v\grammy.mp4`, `X:\v\evil_billionaire2.mp4`,
		`X:\v\hot_girl_toddler.mp4`, `X:\v\i_dont_need_a_man.mp4`, `X:\v\forgot_name_3_times.mp4`,
	} {
		seen[clipColor(s)]++
	}
	if len(seen) < 3 {
		t.Errorf("six different videos produced only %d distinct colours: %v", len(seen), seen)
	}
}
