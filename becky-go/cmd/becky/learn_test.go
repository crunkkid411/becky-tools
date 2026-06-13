package main

import "testing"

// parseTeachPhrase recognizes the plain-language teaching forms (no "enroll"/"learn"
// keyword) and extracts the person name; it leaves real ops / bare paths alone.
func TestParseTeachPhrase(t *testing.T) {
	cases := []struct {
		in       string
		wantName string
		wantOK   bool
	}{
		{"this is Shelby", "Shelby", true},
		{"This is Shelby Alyse Clancy", "Shelby Alyse Clancy", true},
		{"that's John", "John", true},
		{"this is a video of John Clancy", "John Clancy", true},
		{"this is a clip of Hair Jordan", "Hair Jordan", true},
		{"learn Shelby", "Shelby", true},
		{"remember Nolan Harrison", "Nolan Harrison", true},
		{`this is "Shelby".`, "Shelby", true}, // trailing punctuation/quotes trimmed
		// NOT teaching phrases — these must fall through to the normal dispatcher.
		{"find", "", false},
		{"index", "", false},
		{"appearances", "", false},
		{"test.mp4", "", false}, // a bare clip path
		{"C:/videos/x.mp4", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			name, ok := parseTeachPhrase(c.in)
			if ok != c.wantOK {
				t.Fatalf("parseTeachPhrase(%q) ok=%v, want %v (name=%q)", c.in, ok, c.wantOK, name)
			}
			if ok && name != c.wantName {
				t.Errorf("parseTeachPhrase(%q) name=%q, want %q", c.in, name, c.wantName)
			}
		})
	}
}

// splitNameAndClip separates the person name from the clip path regardless of order.
func TestSplitNameAndClip(t *testing.T) {
	cases := []struct {
		args       []string
		name, clip string
	}{
		{[]string{"Shelby", "clip.mp4"}, "Shelby", "clip.mp4"},
		{[]string{"clip.mov", "Shelby Alyse"}, "Shelby Alyse", "clip.mov"}, // order-independent
		{[]string{"John Clancy", "C:/x/y.mkv"}, "John Clancy", "C:/x/y.mkv"},
		{[]string{"OnlyName"}, "OnlyName", ""}, // missing clip
		{[]string{"only.mp4"}, "", "only.mp4"}, // missing name
	}
	for _, c := range cases {
		name, clip := splitNameAndClip(c.args)
		if name != c.name || clip != c.clip {
			t.Errorf("splitNameAndClip(%v) = (%q,%q), want (%q,%q)", c.args, name, clip, c.name, c.clip)
		}
	}
}

// looksLikePath distinguishes a media path from a person name.
func TestLooksLikePath(t *testing.T) {
	paths := []string{"a.mp4", "X:/v/b.MOV", "dir\\c.mkv", "/abs/d.webm", "e.jpg"}
	for _, p := range paths {
		if !looksLikePath(p) {
			t.Errorf("looksLikePath(%q) = false, want true", p)
		}
	}
	names := []string{"Shelby", "John Clancy", "Hair Jordan"}
	for _, n := range names {
		if looksLikePath(n) {
			t.Errorf("looksLikePath(%q) = true, want false", n)
		}
	}
}
