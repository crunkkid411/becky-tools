//go:build !gui

package main

import (
	"os"
	"strings"
	"testing"

	"becky-go/internal/videopreview"
)

// --- timecode --------------------------------------------------------------------

func TestFormatTC(t *testing.T) {
	cases := []struct {
		sec  float64
		want string
	}{
		{0, "00:00.000"},
		{1.5, "00:01.500"},
		{61.25, "01:01.250"},
		{3661.5, "1:01:01.500"}, // hours-aware
		{7322.0, "2:02:02.000"}, // multi-hour
		{-3, "00:00.000"},       // negative clamps
		{0.9999, "00:01.000"},   // ms rounding spill carries to seconds
	}
	for _, tc := range cases {
		if got := formatTC(tc.sec); got != tc.want {
			t.Errorf("formatTC(%g) = %q, want %q", tc.sec, got, tc.want)
		}
	}
}

func TestFormatTCShort(t *testing.T) {
	cases := []struct {
		sec  float64
		want string
	}{
		{0, "0:00"},
		{83, "1:23"},
		{3661, "1:01:01"},
	}
	for _, tc := range cases {
		if got := formatTCShort(tc.sec); got != tc.want {
			t.Errorf("formatTCShort(%g) = %q, want %q", tc.sec, got, tc.want)
		}
	}
}

func TestParseTimecode(t *testing.T) {
	cases := []struct {
		in    string
		want  float64
		valid bool
	}{
		{"90", 90, true},
		{"1:30", 90, true},
		{"1:01:01", 3661, true},
		{"2.5", 2.5, true},
		{"", 0, false},
		{"abc", 0, false},
	}
	for _, tc := range cases {
		got, ok := parseTimecode(tc.in)
		if ok != tc.valid {
			t.Errorf("parseTimecode(%q) ok=%v, want %v", tc.in, ok, tc.valid)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("parseTimecode(%q) = %g, want %g", tc.in, got, tc.want)
		}
	}
}

// --- Project marks ---------------------------------------------------------------

func TestProject_MarksClampToDuration(t *testing.T) {
	p := NewProject()
	p.LoadInfo(`C:\v.mp4`, videopreview.Info{DurationSec: 10, FPS: 30})

	if !p.IsOpen() {
		t.Fatal("IsOpen() false after LoadInfo")
	}
	// Marks span the whole clip on load.
	if p.In != 0 || p.Out != 10 {
		t.Errorf("after load In/Out = %g/%g, want 0/10", p.In, p.Out)
	}

	// Playhead clamps to [0,dur].
	if got := p.SetPlayhead(12); got != 10 {
		t.Errorf("SetPlayhead(12) = %g, want 10 (clamped)", got)
	}
	if got := p.SetPlayhead(-1); got != 0 {
		t.Errorf("SetPlayhead(-1) = %g, want 0 (clamped)", got)
	}

	// In can't exceed Out; Out can't fall below In.
	p.SetOut(5)
	p.SetIn(8) // requested past Out(5) -> clamped to 5
	if p.In != 5 {
		t.Errorf("SetIn past Out: In = %g, want 5", p.In)
	}
	p.In = 3
	p.SetOut(1) // below In(3) -> clamped to 3
	if p.Out != 3 {
		t.Errorf("SetOut below In: Out = %g, want 3", p.Out)
	}
}

func TestProject_MarkDur(t *testing.T) {
	p := NewProject()
	p.LoadInfo(`C:\v.mp4`, videopreview.Info{DurationSec: 20})
	p.In, p.Out = 5, 12
	if got := p.MarkDur(); got != 7 {
		t.Errorf("MarkDur = %g, want 7", got)
	}
	p.In, p.Out = 12, 5 // inverted -> 0, never negative
	if got := p.MarkDur(); got != 0 {
		t.Errorf("inverted MarkDur = %g, want 0", got)
	}
}

// --- exportRange guards (no ffmpeg run) ------------------------------------------

func TestExportRange_NothingOpen(t *testing.T) {
	p := NewProject()
	if _, err := exportRange(p, ""); err == nil {
		t.Fatal("exportRange with nothing open should error")
	}
}

func TestExportRange_EmptyRange(t *testing.T) {
	p := NewProject()
	p.LoadInfo(`C:\v.mp4`, videopreview.Info{DurationSec: 10})
	p.In, p.Out = 4, 4 // empty
	_, err := exportRange(p, "")
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("exportRange empty range err = %v, want an 'empty' error", err)
	}
}

func TestDefaultExportPath(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{`E:\TakingBack2007\clip.mp4`, `E:\TakingBack2007\clip_range.mp4`},
		{`/home/x/clip.mkv`, `/home/x/clip_range.mp4`},
		{`bare.mov`, `bare_range.mp4`},
	}
	for _, tc := range cases {
		got := defaultExportPath(tc.src)
		// Normalize the separator the helper used (filepath.Separator differs by OS),
		// so the test passes on both Windows and Linux CI.
		gotN := strings.ReplaceAll(got, `\`, "/")
		wantN := strings.ReplaceAll(tc.want, `\`, "/")
		if gotN != wantN {
			t.Errorf("defaultExportPath(%q) = %q, want %q", tc.src, got, tc.want)
		}
	}
}

func TestStem(t *testing.T) {
	cases := map[string]string{
		`C:\a\b\clip.mp4`:  "clip",
		`/x/y/clip.tar.gz`: "clip.tar",
		`noext`:            "noext",
		`.hidden`:          ".hidden", // leading dot is not an extension separator
	}
	for in, want := range cases {
		if got := stem(in); got != want {
			t.Errorf("stem(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- routeCommand (the one AI box) -----------------------------------------------

func TestRouteCommand(t *testing.T) {
	open := NewProject()
	open.LoadInfo(`C:\v.mp4`, videopreview.Info{DurationSec: 100})
	open.SetPlayhead(20)

	closed := NewProject()

	cases := []struct {
		name  string
		text  string
		proj  *Project
		check func(commandResult) bool
	}{
		{"mark in (open)", "mark in", open, func(r commandResult) bool { return r.MarkIn }},
		{"mark out (open)", "set out point", open, func(r commandResult) bool { return r.MarkOut }},
		{"export (open)", "export the clip", open, func(r commandResult) bool { return r.Export }},
		{"open", "open a file", closed, func(r commandResult) bool { return r.OpenPicker }},
		{"window", "pop out the big preview", open, func(r commandResult) bool { return r.WantWindow }},
		{"seek phrase", "go to 1:30", open, func(r commandResult) bool { return r.Seek && r.SeekTo == 90 }},
		{"bare time seek", "0:50", open, func(r commandResult) bool { return r.Seek && r.SeekTo == 50 }},
		{"mark in (closed)", "mark in", closed, func(r commandResult) bool { return !r.MarkIn && r.Reply != "" }},
		{"gibberish", "asdf qwer", open, func(r commandResult) bool { return r.Unrecognized }},
		{"seek clamps", "go to 999", open, func(r commandResult) bool { return r.Seek && r.SeekTo == 100 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := routeCommand(tc.text, tc.proj)
			if !tc.check(got) {
				t.Errorf("routeCommand(%q) = %+v — failed check", tc.text, got)
			}
			// routeCommand must never mutate the project (show-me discipline).
			if tc.proj == open && (open.In != 0 || open.Out != 100 || open.Play != 20) {
				t.Errorf("routeCommand mutated the project: %+v", open)
			}
		})
	}
}

// --- headless CLI surface --------------------------------------------------------

func TestRun_NoArgs_BadInvocation(t *testing.T) {
	if code := run(nil, os.Stdout, os.Stderr); code != exitBadArgs {
		t.Errorf("run(nil) = %d, want %d", code, exitBadArgs)
	}
}

func TestRun_UnknownFlag_BadInvocation(t *testing.T) {
	if code := run([]string{"--nope"}, os.Stdout, os.Stderr); code != exitBadArgs {
		t.Errorf("run(--nope) = %d, want %d", code, exitBadArgs)
	}
}

func TestRun_ExportRange_EmptyMarks_BadArgs(t *testing.T) {
	// in==out==0 is an empty range; caught before any ffmpeg attempt.
	code := run([]string{"--export-range", `C:\v.mp4`, "--in", "5", "--out", "5"}, os.Stdout, os.Stderr)
	if code != exitBadArgs {
		t.Errorf("run(empty range) = %d, want %d (bad args)", code, exitBadArgs)
	}
}

func TestRun_Probe_MissingSidecar_Degrades(t *testing.T) {
	// No sidecar binary present in CI -> probe degrades (exit 1), never panics.
	t.Setenv("BECKY_VIDEO_PREVIEW", "") // ensure no host override leaks in
	code := run([]string{"--probe", `C:\nope.mp4`}, os.Stdout, os.Stderr)
	if code != exitDegraded {
		t.Errorf("run(--probe) with no sidecar = %d, want %d (degraded)", code, exitDegraded)
	}
}
