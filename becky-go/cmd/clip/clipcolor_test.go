package main

import "testing"

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
