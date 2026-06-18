package main

import (
	"flag"
	"strings"
	"testing"

	"becky-go/internal/edl"
)

func TestPositional_BeforeAndAfterFlags(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		want string
	}{
		{"positional first", []string{"reel.json", "--output", "out.mp4"}, "reel.json"},
		{"positional after flags", []string{"--output", "out.mp4", "reel.json"}, "reel.json"},
		{"flag with value then positional", []string{"--codec", "libx264", "r.json"}, "r.json"},
		{"only positional", []string{"r.json"}, "r.json"},
		{"no positional", []string{"--verbose"}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("t", flag.ContinueOnError)
			fs.String("output", "", "")
			fs.String("codec", "", "")
			fs.Bool("verbose", false, "")
			got := positional(fs, tc.argv)
			if got != tc.want {
				t.Fatalf("positional(%v) = %q, want %q", tc.argv, got, tc.want)
			}
		})
	}
}

func TestPositional_ParsesFlagsAfterPositional(t *testing.T) {
	fs := flag.NewFlagSet("render", flag.ContinueOnError)
	out := fs.String("output", "", "")
	codec := fs.String("codec", "", "")
	_ = positional(fs, []string{"reel.json", "--output", "x.mp4", "--codec", "libx264"})
	if *out != "x.mp4" {
		t.Fatalf("--output not parsed: %q", *out)
	}
	if *codec != "libx264" {
		t.Fatalf("--codec not parsed: %q", *codec)
	}
}

func TestSiblingOutput(t *testing.T) {
	r := edl.Reel{Name: "Penguin Bounty"}
	got := siblingOutput("/cases/case42/reel.json", r, ".edl")
	if !strings.HasSuffix(got, "penguin-bounty.edl") {
		t.Fatalf("siblingOutput = %q, want suffix penguin-bounty.edl", got)
	}
	// Falls back to the reel-json stem when the reel has no name.
	got2 := siblingOutput("/cases/myreel.json", edl.Reel{}, ".srt")
	if !strings.HasSuffix(got2, "myreel.srt") {
		t.Fatalf("siblingOutput (no name) = %q, want suffix myreel.srt", got2)
	}
}

func TestFrameOutput(t *testing.T) {
	got := frameOutput(`X:\case\interview.mp4`, 14.5)
	if !strings.Contains(got, "interview_14.500s.png") {
		t.Fatalf("frameOutput = %q, want interview_14.500s.png", got)
	}
}

func TestSlug(t *testing.T) {
	if slug("Case #1: Threats") != "case-1-threats" {
		t.Fatalf("slug wrong: %q", slug("Case #1: Threats"))
	}
	if slug("") != "becky" {
		t.Fatalf("empty slug should be becky, got %q", slug(""))
	}
}
