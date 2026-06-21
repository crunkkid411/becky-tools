package songbuild

import (
	"testing"

	"becky-go/internal/intent"
)

func TestBuildPhrase_fullSong(t *testing.T) {
	arr, spec, err := BuildPhrase("dark trap at 140, 8 bars")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Genre != "trap" || spec.BPM != 140 || spec.Bars != 8 {
		t.Errorf("intent wrong: %+v", spec)
	}
	if arr.BPM != 140 {
		t.Errorf("arrangement BPM = %d, want 140", arr.BPM)
	}
	ids := map[string]bool{}
	for _, tr := range arr.Tracks {
		ids[tr.ID] = true
	}
	for _, want := range []string{"drums", "bass", "chords", "melody"} {
		if !ids[want] {
			t.Errorf("expected a %s track in the full song, got %v", want, ids)
		}
	}
}

func TestBuild_drumsOnly(t *testing.T) {
	arr, err := Build(intent.Spec{Genre: "house", DrumsOnly: true, Bars: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(arr.Tracks) != 1 || arr.Tracks[0].ID != "drums" {
		t.Errorf("drums-only should yield just a drum track, got %d tracks", len(arr.Tracks))
	}
}

func TestBuild_keyApplied(t *testing.T) {
	arr, _ := Build(intent.Spec{Genre: "emo", Root: "F#", Scale: "minor"})
	if arr.Root != "F#" || arr.Scale != "minor" {
		t.Errorf("key not applied: %s %s", arr.Root, arr.Scale)
	}
}

func TestBuild_deterministic(t *testing.T) {
	s := intent.Spec{Genre: "trap", Seed: 7, Bars: 4}
	a1, _ := Build(s)
	a2, _ := Build(s)
	if a1.NoteCount() != a2.NoteCount() {
		t.Errorf("same spec gave different note counts: %d vs %d", a1.NoteCount(), a2.NoteCount())
	}
}

func TestDefaultBPM(t *testing.T) {
	if DefaultBPM("dnb") != 174 {
		t.Errorf("dnb default = %d, want 174", DefaultBPM("dnb"))
	}
	if DefaultBPM("nonsense") != 128 {
		t.Errorf("unknown default = %d, want 128", DefaultBPM("nonsense"))
	}
}
