package intent

import "testing"

func TestParse_full(t *testing.T) {
	s := Parse("dark trap at 140, 8 bars, just drums")
	if s.Genre != "trap" {
		t.Errorf("genre = %q, want trap", s.Genre)
	}
	if s.BPM != 140 {
		t.Errorf("bpm = %d, want 140", s.BPM)
	}
	if s.Bars != 8 {
		t.Errorf("bars = %d, want 8", s.Bars)
	}
	if s.Scale != "minor" {
		t.Errorf("dark → scale = %q, want minor", s.Scale)
	}
	if !s.DrumsOnly {
		t.Error("'just drums' should set DrumsOnly")
	}
}

func TestParse_genreSynonyms(t *testing.T) {
	cases := map[string]string{
		"make some drum and bass": "dnb",
		"a chill lo-fi beat":      "lofi",
		"boom bap hip hop":        "hiphop",
		"deep house groove":       "house",
		"pop punk anthem":         "pop-punk",
	}
	for phrase, want := range cases {
		if g := Parse(phrase).Genre; g != want {
			t.Errorf("Parse(%q).Genre = %q, want %q", phrase, g, want)
		}
	}
}

func TestParse_explicitKey(t *testing.T) {
	s := Parse("emo song in F# minor")
	if s.Root != "F#" || s.Scale != "minor" {
		t.Errorf("key parse: root=%q scale=%q, want F# minor", s.Root, s.Scale)
	}
	s2 := Parse("uplifting house in C major")
	if s2.Root != "C" || s2.Scale != "major" {
		t.Errorf("key parse: root=%q scale=%q, want C major", s2.Root, s2.Scale)
	}
}

func TestParse_moodScale(t *testing.T) {
	if Parse("a happy bright tune").Scale != "major" {
		t.Error("happy/bright → major")
	}
	if Parse("something dark and moody").Scale != "minor" {
		t.Error("dark/moody → minor")
	}
}

func TestParse_bpmForms(t *testing.T) {
	for _, p := range []string{"trap 140 bpm", "trap at 140"} {
		if Parse(p).BPM != 140 {
			t.Errorf("%q should parse 140 BPM", p)
		}
	}
	// out-of-range ignored
	if Parse("at 999 bpm").BPM != 0 {
		t.Error("999 is out of range and should be ignored")
	}
}

func TestParse_seedStableAndExplicit(t *testing.T) {
	if Parse("dark trap").Seed != Parse("dark trap").Seed {
		t.Error("same phrase must give the same seed (deterministic)")
	}
	if Parse("dark trap").Seed == Parse("happy house").Seed {
		t.Error("different phrases should (almost always) give different seeds")
	}
	if Parse("house seed 42").Seed != 42 {
		t.Error("explicit 'seed 42' should win")
	}
}

func TestParse_understood(t *testing.T) {
	s := Parse("trap at 128")
	if len(s.Understood) < 2 {
		t.Errorf("Understood should list parsed facts, got %v", s.Understood)
	}
}
