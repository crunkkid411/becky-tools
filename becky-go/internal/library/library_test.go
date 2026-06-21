package library

import (
	"testing"
	"time"

	"becky-go/internal/dawmodel"
)

func testLib(t *testing.T) *Library {
	t.Helper()
	l := OpenDir(t.TempDir())
	n := time.Unix(1700000000, 0)
	l.now = func() time.Time { n = n.Add(time.Second); return n } // deterministic, increasing
	return l
}

func sampleArr() *dawmodel.Arrangement {
	a := dawmodel.New()
	a.Root, a.Scale, a.BPM = "F", "minor", 140
	a = a.AddTrack("drums", dawmodel.KindMIDI)
	a.Tracks[0].Clips = append(a.Tracks[0].Clips, dawmodel.Clip{Name: "beat", Channel: 9, Program: -1})
	a, _, _ = a.AddNote("drums", "beat", dawmodel.Note{Start: 0, Dur: 120, Pitch: 36, Vel: 110, Ch: 9})
	return a
}

func TestStarAndList(t *testing.T) {
	l := testLib(t)
	if err := l.Star(CatKit, "X:/kits/808", "My 808"); err != nil {
		t.Fatal(err)
	}
	if err := l.Star(CatGenre, "crunkcore", ""); err != nil {
		t.Fatal(err)
	}
	all, _ := l.Favorites("")
	if len(all) != 2 {
		t.Fatalf("want 2 favorites, got %d", len(all))
	}
	kits, _ := l.Favorites(CatKit)
	if len(kits) != 1 || kits[0].Label != "My 808" {
		t.Errorf("kit favorite wrong: %+v", kits)
	}
}

func TestStarIdempotentAndRelabel(t *testing.T) {
	l := testLib(t)
	_ = l.Star(CatKit, "X:/kits/808", "808")
	_ = l.Star(CatKit, "X:/kits/808", "My Favorite 808") // same value → update label, no dup
	kits, _ := l.Favorites(CatKit)
	if len(kits) != 1 {
		t.Fatalf("starring the same value twice should not duplicate: %d", len(kits))
	}
	if kits[0].Label != "My Favorite 808" {
		t.Errorf("label not updated: %q", kits[0].Label)
	}
}

func TestStarValidation(t *testing.T) {
	l := testLib(t)
	if err := l.Star("bogus", "x", ""); err == nil {
		t.Error("unknown category should error")
	}
	if err := l.Star(CatKit, "", ""); err == nil {
		t.Error("empty value should error")
	}
}

func TestUnstar(t *testing.T) {
	l := testLib(t)
	_ = l.Star(CatSound, "kick.wav", "")
	_ = l.Unstar(CatSound, "kick.wav")
	s, _ := l.Favorites(CatSound)
	if len(s) != 0 {
		t.Errorf("unstar failed: %+v", s)
	}
	if err := l.Unstar(CatSound, "missing.wav"); err != nil {
		t.Errorf("unstar of missing should be a no-op, got %v", err)
	}
}

func TestTemplateRoundTrip(t *testing.T) {
	l := testLib(t)
	meta, err := l.SaveTemplate("My Crunkcore Starter", "crunkcore", sampleArr())
	if err != nil {
		t.Fatal(err)
	}
	if meta.Slug != "my-crunkcore-starter" {
		t.Errorf("slug = %q", meta.Slug)
	}
	if meta.Tracks != 1 || meta.Notes != 1 || meta.BPM != 140 {
		t.Errorf("meta wrong: %+v", meta)
	}
	arr, m2, err := l.LoadTemplate("my crunkcore starter") // by name, case-insensitive
	if err != nil {
		t.Fatal(err)
	}
	if arr.BPM != 140 || arr.Root != "F" || len(arr.Tracks) != 1 {
		t.Errorf("loaded arrangement wrong: bpm=%d root=%s tracks=%d", arr.BPM, arr.Root, len(arr.Tracks))
	}
	if m2.Genre != "crunkcore" {
		t.Errorf("genre tag lost: %q", m2.Genre)
	}
}

func TestTemplateOverwriteAndList(t *testing.T) {
	l := testLib(t)
	_, _ = l.SaveTemplate("House Starter", "house", sampleArr())
	_, _ = l.SaveTemplate("House Starter", "house", sampleArr()) // same slug → overwrite, not dup
	_, _ = l.SaveTemplate("Trap Starter", "trap", sampleArr())
	list, _ := l.ListTemplates()
	if len(list) != 2 {
		t.Fatalf("want 2 templates, got %d", len(list))
	}
	// newest first
	if list[0].Name != "Trap Starter" {
		t.Errorf("expected newest (Trap) first, got %q", list[0].Name)
	}
}

func TestTemplateRemoveAndMissing(t *testing.T) {
	l := testLib(t)
	_, _ = l.SaveTemplate("Gone", "", sampleArr())
	if err := l.RemoveTemplate("Gone"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := l.LoadTemplate("Gone"); err == nil {
		t.Error("loading a removed template should error")
	}
}

func TestSaveTemplateValidation(t *testing.T) {
	l := testLib(t)
	if _, err := l.SaveTemplate("", "", sampleArr()); err == nil {
		t.Error("empty name should error")
	}
	if _, err := l.SaveTemplate("ok", "", nil); err == nil {
		t.Error("nil arrangement should error")
	}
	if _, err := l.SaveTemplate("!!!", "", sampleArr()); err == nil {
		t.Error("name with no usable chars should error")
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"My Crunkcore Starter": "my-crunkcore-starter",
		"  Four/On The Floor ": "four-on-the-floor",
		"808!!!Heavy":          "808-heavy",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}
