package main

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile is a test helper that creates a file with content, failing the test on
// error.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestTagsMarkPerson(t *testing.T) {
	cases := []struct {
		name string
		tags []string
		want bool
	}{
		{"explicit person", []string{"entity", "person", "defendant"}, true},
		{"entity+primary (videos wiki)", []string{"entity", "primary", "male"}, true},
		{"evidence file", []string{"evidence", "youtube", "threat"}, false},
		{"entity only", []string{"entity"}, false},
		{"empty", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := tagsMarkPerson(c.tags); got != c.want {
				t.Errorf("tagsMarkPerson(%v) = %v, want %v", c.tags, got, c.want)
			}
		})
	}
}

func TestParseTagsAndAliases(t *testing.T) {
	front := `created: 2026-04-16
tags: [entity, person, defendant]
aliases: ["John Black", "melancholy"]
source: "x"`
	tags := parseTags(front)
	if len(tags) != 3 || tags[0] != "entity" || tags[2] != "defendant" {
		t.Errorf("parseTags = %v", tags)
	}
	al := parseAliasField(front)
	if len(al) != 2 || al[0] != "John Black" {
		t.Errorf("parseAliasField = %v", al)
	}
}

func TestParseAliasTable(t *testing.T) {
	body := `# John

## Known Aliases

| Alias | Context |
|---|---|
| John Black | copyright strike |
| melancholy | youtube handle |

## Next Section
`
	al := parseAliasTable(body)
	if len(al) != 2 || al[0] != "John Black" || al[1] != "melancholy" {
		t.Fatalf("parseAliasTable = %v", al)
	}
}

func TestFirstH1IgnoresFrontmatter(t *testing.T) {
	text := "---\ntags: [person]\n# not a title\n---\n\n# Real Title\n\nbody"
	if got := firstH1(text); got != "Real Title" {
		t.Errorf("firstH1 = %q, want %q", got, "Real Title")
	}
}

func TestSpeakerFromHint(t *testing.T) {
	p := Person{Name: "John Clancy", Aliases: []string{"John Black"}}
	cases := []struct {
		hint string
		want string
	}{
		{"SPEAKER_01 = John Clancy", "SPEAKER_01"},
		{"John Clancy is SPEAKER_00", "SPEAKER_00"},
		{"SPEAKER_02 = Shelby", ""},  // names a different person
		{"the defendant speaks", ""}, // no speaker id
		{"", ""},                     // empty
	}
	for _, c := range cases {
		if got := speakerFromHint(c.hint, p); got != c.want {
			t.Errorf("speakerFromHint(%q) = %q, want %q", c.hint, got, c.want)
		}
	}
}

func TestTagNamesPerson(t *testing.T) {
	p := &Person{Name: "John Anthony Clancy", Slug: "john-anthony-clancy"}
	cases := []struct {
		tag  string
		want bool
	}{
		{"john-clancy", true},         // subset of slug tokens
		{"john-anthony-clancy", true}, // exact
		{"hair-jordan", false},        // different person
		{"evidence", false},           // generic single word
		{"youtube", false},            // generic
	}
	for _, c := range cases {
		if got := tagNamesPerson(c.tag, p); got != c.want {
			t.Errorf("tagNamesPerson(%q) = %v, want %v", c.tag, got, c.want)
		}
	}
}

func TestCenteredPerson(t *testing.T) {
	persons := map[string]*Person{
		"john-anthony-clancy": {Name: "John Anthony Clancy", Slug: "john-anthony-clancy"},
		"shelby-clancy":       {Name: "Shelby Clancy", Slug: "shelby-clancy"},
	}
	johnHeavy := "John Anthony Clancy did X. John Anthony Clancy said Y. John Anthony Clancy."
	if got := centeredPerson(johnHeavy, persons); got != "john-anthony-clancy" {
		t.Errorf("centeredPerson(johnHeavy) = %q", got)
	}
	balanced := "John Anthony Clancy and Shelby Clancy. John Anthony Clancy, Shelby Clancy. John Anthony Clancy Shelby Clancy."
	if got := centeredPerson(balanced, persons); got != "" {
		t.Errorf("centeredPerson(balanced) = %q, want empty (not clearly centered)", got)
	}
	none := "this file mentions nobody specific just once."
	if got := centeredPerson(none, persons); got != "" {
		t.Errorf("centeredPerson(none) = %q, want empty", got)
	}
}

func TestLongestCleanSpan(t *testing.T) {
	segs := []diarSegment{
		{Start: 0, End: 5},   // too short
		{Start: 10, End: 50}, // 40s -> clamps to 30
	}
	span, ok := longestCleanSpan(segs)
	if !ok {
		t.Fatal("expected a span")
	}
	if span.Start != 10 || span.End != 40 {
		t.Errorf("span = %+v, want {10,40}", span)
	}
	tooShort := []diarSegment{{Start: 0, End: 5}, {Start: 6, End: 9}}
	if _, ok := longestCleanSpan(tooShort); ok {
		t.Error("expected no span for all-short segments")
	}
}

func TestChooseSpeakerSingle(t *testing.T) {
	diar := diarOutput{Speakers: []diarSpeaker{{ID: "SPEAKER_00", Segments: []diarSegment{{Start: 0, End: 20}}}}}
	sp := chooseSpeaker(diar, Person{Name: "X"})
	if sp == nil || sp.ID != "SPEAKER_00" {
		t.Errorf("chooseSpeaker single = %+v", sp)
	}
}

func TestChooseSpeakerByHint(t *testing.T) {
	diar := diarOutput{Speakers: []diarSpeaker{
		{ID: "SPEAKER_00", Segments: []diarSegment{{Start: 0, End: 5}}},
		{ID: "SPEAKER_01", Segments: []diarSegment{{Start: 6, End: 10}}},
	}}
	p := Person{Name: "John", SpeakerHint: "SPEAKER_01 = John"}
	sp := chooseSpeaker(diar, p)
	if sp == nil || sp.ID != "SPEAKER_01" {
		t.Errorf("chooseSpeaker by hint = %+v, want SPEAKER_01", sp)
	}
}

func TestChooseSpeakerDominant(t *testing.T) {
	diar := diarOutput{Speakers: []diarSpeaker{
		{ID: "SPEAKER_00", Segments: []diarSegment{{Start: 0, End: 5}}},  // 5s
		{ID: "SPEAKER_01", Segments: []diarSegment{{Start: 6, End: 40}}}, // 34s dominant
	}}
	sp := chooseSpeaker(diar, Person{Name: "Unknown"})
	if sp == nil || sp.ID != "SPEAKER_01" {
		t.Errorf("chooseSpeaker dominant = %+v, want SPEAKER_01", sp)
	}
}

func TestCrawlWikiDetectsPersonAndEvidence(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "wiki")
	rawDir := filepath.Join(dir, "raw")

	// A real (existing) video in raw/ so resolution succeeds.
	videoPath := filepath.Join(rawDir, "clip.mp4")
	writeFile(t, videoPath, "fake video bytes")

	// Person file.
	writeFile(t, filepath.Join(root, "jane-doe.md"), `---
tags: [entity, person, witness]
aliases: ["JD"]
---

# Jane Doe

## Known Aliases

| Alias | Context |
|---|---|
| Janie | nickname |
`)
	// Evidence file naming the person in tags + referencing the video.
	writeFile(t, filepath.Join(root, "jane-statement.md"), `---
tags: [evidence, jane-doe, statement]
---

# Jane Statement

Video: raw/clip.mp4
`)

	people, _, err := crawlWiki([]string{root}, false)
	if err != nil {
		t.Fatalf("crawlWiki: %v", err)
	}
	if len(people) != 1 {
		t.Fatalf("expected 1 person, got %d", len(people))
	}
	p := people[0]
	if p.Name != "Jane Doe" {
		t.Errorf("name = %q", p.Name)
	}
	if !contains(p.Aliases, "JD") || !contains(p.Aliases, "Janie") {
		t.Errorf("aliases = %v", p.Aliases)
	}
	if len(p.VideoRefs) != 1 || filepath.Base(p.VideoRefs[0]) != "clip.mp4" {
		t.Errorf("video refs = %v (expected the evidence-linked clip.mp4)", p.VideoRefs)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
