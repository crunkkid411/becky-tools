package main

import "testing"

func TestParseArgs(t *testing.T) {
	cases := []struct {
		name        string
		argv        []string
		wantVideo   string
		wantOffline bool
		wantVerbose bool
		wantErr     bool
	}{
		{"video only", []string{"clip.mp4"}, "clip.mp4", false, false, false},
		{"video then json", []string{"clip.mp4", "--json"}, "clip.mp4", false, false, false},
		{"flags before path", []string{"--offline", "clip.mp4"}, "clip.mp4", true, false, false},
		{"offline after path", []string{"clip.mp4", "--offline"}, "clip.mp4", true, false, false},
		{"verbose", []string{"-v", "clip.mp4"}, "clip.mp4", false, true, false},
		{"path with spaces token", []string{"E:/a b/clip_[ID000000001].mp4"}, "E:/a b/clip_[ID000000001].mp4", false, false, false},
		{"no path", []string{"--offline"}, "", false, false, true},
		{"unknown flag", []string{"clip.mp4", "--wat"}, "", false, false, true},
		{"two paths", []string{"a.mp4", "b.mp4"}, "", false, false, true},
		{"help", []string{"-h"}, "", false, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			video, opt, verbose, err := parseArgs(c.argv)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error for %v", c.argv)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if video != c.wantVideo {
				t.Fatalf("video = %q, want %q", video, c.wantVideo)
			}
			if opt.Offline != c.wantOffline {
				t.Fatalf("offline = %v, want %v", opt.Offline, c.wantOffline)
			}
			if verbose != c.wantVerbose {
				t.Fatalf("verbose = %v, want %v", verbose, c.wantVerbose)
			}
		})
	}
}
