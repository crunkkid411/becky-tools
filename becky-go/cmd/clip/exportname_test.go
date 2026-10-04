package main

import (
	"testing"

	"becky-go/internal/edl"
)

// TestExportBaseNamePrefixAndBracketedID: the typed name replaces "clips", and the
// source's YouTube id stays verbatim in brackets (case kept) instead of being slugged.
func TestExportBaseNamePrefixAndBracketedID(t *testing.T) {
	one := []edl.Clip{{Source: `E:\vids\2024-08-30_We_Tried_It_[_WpAGJs_ZG8].mp4`}}
	cases := []struct {
		clips  []edl.Clip
		prefix string
		want   string
	}{
		{one, "", "clips_2024-08-30-we-tried-it_[_WpAGJs_ZG8]"},
		{one, "fastfood", "fastfood_2024-08-30-we-tried-it_[_WpAGJs_ZG8]"},
		{one, `bad:na?me `, "badname_2024-08-30-we-tried-it_[_WpAGJs_ZG8]"},
		{[]edl.Clip{{Source: `E:\vids\plain name.mp4`}}, "x", "x_plain-name"},
		{[]edl.Clip{{Source: `E:\a.mp4`}, {Source: `E:\b.mp4`}}, "", "clips_compilation"},
		{[]edl.Clip{{Source: `E:\a.mp4`}, {Source: `E:\b.mp4`}}, "My Reel", "My Reel_compilation"},
	}
	for _, c := range cases {
		if got := exportBaseName(c.clips, c.prefix); got != c.want {
			t.Errorf("exportBaseName(%q) = %q, want %q", c.prefix, got, c.want)
		}
	}
}
