package genreprofile

import (
	"encoding/json"
	"strings"
	"testing"

	"becky-go/internal/music"
)

func TestQueries(t *testing.T) {
	qs := Queries("crunkcore")
	if len(qs) != 6 {
		t.Fatalf("want 6 query templates, got %d", len(qs))
	}
	joined := strings.Join(qs, "\n")
	for _, must := range []string{"chord progressions", "BPM tempo", "drum programming", "bass line", "scales modes"} {
		if !strings.Contains(joined, must) {
			t.Errorf("query templates missing %q", must)
		}
	}
	if !strings.Contains(qs[0], "crunkcore") {
		t.Errorf("genre not interpolated: %q", qs[0])
	}
}

func TestReferenceQueries(t *testing.T) {
	qs := ReferenceQueries("Underoath Reinventing")
	if len(qs) != 2 || !strings.Contains(qs[0], "chord progression key BPM") {
		t.Errorf("reference queries wrong: %v", qs)
	}
}

func TestProfileFromElements_valid(t *testing.T) {
	e := BlankElements("nightcore")
	e.Root = "F"
	e.Scales = []string{"minor", "phrygian"}
	e.DefaultScale = "minor"
	e.TempoMin, e.TempoMax, e.TempoDefault = 160, 180, 174
	e.Progressions = []music.Progression{{Name: "drive", Weight: 3, Roman: []string{"i", "bVI", "bVII", "i"}}}
	e.Texture = []string{"drums", "bass", "chords", "melody", "lead"}

	p, err := ProfileFromElements(e)
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "nightcore" || p.Tempo.Default != 174 || p.Key.DefaultRoot != "F" {
		t.Errorf("profile basics wrong: %+v", p.Tempo)
	}
	if len(p.Progressions) != 1 {
		t.Errorf("progressions lost")
	}
	// Track specs filled for every texture role, drums on channel 9.
	for _, role := range e.Texture {
		if _, ok := p.Tracks[role]; !ok {
			t.Errorf("missing track spec for role %q", role)
		}
	}
	if p.Tracks["drums"].Channel != 9 {
		t.Errorf("drums should be channel 9, got %d", p.Tracks["drums"].Channel)
	}
	if len(p.Arrangement) == 0 {
		t.Error("a default form should be generated when Form is empty")
	}
	// All form section bars must be <= 8 (the chunk rule), except handled sections.
	for _, s := range p.Arrangement {
		if s.Bars > 8 {
			t.Errorf("section %q has %d bars (>8 chunk rule)", s.Name, s.Bars)
		}
	}

	// It must round-trip as a real music.Profile (the embed format).
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var back music.Profile
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("generated profile is not valid music.Profile JSON: %v", err)
	}
	if back.ID != "nightcore" {
		t.Errorf("round-trip lost id: %q", back.ID)
	}
}

func TestProfileFromElements_validation(t *testing.T) {
	if _, err := ProfileFromElements(Elements{}); err == nil {
		t.Error("empty elements should error (no id)")
	}
	if _, err := ProfileFromElements(Elements{ID: "x"}); err == nil {
		t.Error("missing root should error")
	}
	if _, err := ProfileFromElements(Elements{ID: "x", Root: "A"}); err == nil {
		t.Error("missing progression should error")
	}
}

func TestBlankElements(t *testing.T) {
	e := BlankElements("Pop-Punk")
	if e.ID != "pop-punk" {
		t.Errorf("id should be lowercased: %q", e.ID)
	}
	if e.DisplayName != "Pop Punk" {
		t.Errorf("displayName = %q, want \"Pop Punk\"", e.DisplayName)
	}
	if _, err := ProfileFromElements(e); err != nil {
		t.Errorf("a blank scaffold should still produce a valid profile: %v", err)
	}
}
