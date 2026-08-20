package pathx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBase(t *testing.T) {
	cases := []struct{ in, want string }{
		{`C:\tmp\frame_0007.jpg`, "frame_0007.jpg"}, // windows path on any host
		{"/usr/bin/ffmpeg", "ffmpeg"},               // posix path
		{`C:\ProgramData\bin\ffmpeg.exe`, "ffmpeg.exe"},
		{"ffmpeg", "ffmpeg"}, // bare name, no separator
		{"a/b\\c", "c"},      // mixed separators, last wins
		{"", ""},             // empty stays empty (not ".")
		{`dir\`, ""},         // trailing separator -> empty tail
	}
	for _, c := range cases {
		if got := Base(c.in); got != c.want {
			t.Errorf("Base(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIsAbs(t *testing.T) {
	abs := []string{`X:\Videos\raw\a.mp4`, `C:/tmp/a.jpg`, "/usr/bin/ffmpeg", `\\server\share\a.mp4`}
	for _, p := range abs {
		if !IsAbs(p) {
			t.Errorf("IsAbs(%q) = false, want true", p)
		}
	}
	rel := []string{"a.mp4", `raw\a.mp4`, "raw/a.mp4", "X:", "X:a.mp4", ""}
	for _, p := range rel {
		if IsAbs(p) {
			t.Errorf("IsAbs(%q) = true, want false", p)
		}
	}
}

func TestDir(t *testing.T) {
	cases := []struct{ in, want string }{
		{`C:\tmp\frame_0007.jpg`, `C:\tmp`},
		{"/usr/bin/ffmpeg", "/usr/bin"},
		{"ffmpeg", ""}, // no separator -> empty
		{"a/b\\c", "a/b"},
	}
	for _, c := range cases {
		if got := Dir(c.in); got != c.want {
			t.Errorf("Dir(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The bug this exists to prevent, reproduced end to end: a directory named
// after a yt-dlp download, so it carries the video id in brackets. filepath.Glob
// reads `[unswA5Jv7fI]` as a character class and looks somewhere else entirely.
func TestFilesInReadsDirectoriesWithGlobMetacharactersInTheName(t *testing.T) {
	root := t.TempDir()
	// The real shape: "<clip stem>" as the frame-cache directory name.
	bracketed := filepath.Join(root, "Prank Clips_Sony AVC-MVC_BEST 30 FPS 1080[4]")
	if err := os.MkdirAll(bracketed, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"frame_0001.jpg", "frame_0002.jpg", "frame_0003.jpg", "audio.wav", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(bracketed, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// REGRESSION GUARD: prove filepath.Glob really does fail here, so the rest of
	// this test is checking a real bug and not a strawman.
	globbed, _ := filepath.Glob(filepath.Join(bracketed, "frame_*.jpg"))
	if len(globbed) != 0 {
		t.Fatalf("filepath.Glob unexpectedly found %d file(s) in a bracketed dir; "+
			"if this now works, FilesIn may no longer be needed", len(globbed))
	}

	got, err := FilesIn(bracketed, "frame_", ".jpg")
	if err != nil {
		t.Fatalf("FilesIn: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("FilesIn found %d frame(s), want 3: %v", len(got), got)
	}
	if filepath.Base(got[0]) != "frame_0001.jpg" || filepath.Base(got[2]) != "frame_0003.jpg" {
		t.Errorf("FilesIn is not sorted by name: %v", got)
	}
}

// The other half of the same bug: a SIBLING directory whose name is what the
// character class actually matches. Glob does not just miss - it silently reads
// the wrong clip's frames and the model gets asked about the wrong video.
func TestFilesInDoesNotReachIntoTheSiblingAGlobWouldMatch(t *testing.T) {
	root := t.TempDir()
	wanted := filepath.Join(root, "clip[4]")
	decoy := filepath.Join(root, "clip4")
	for _, d := range []string{wanted, decoy} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(decoy, "frame_0001.jpg"), []byte("WRONG CLIP"), 0o644); err != nil {
		t.Fatal(err)
	}

	if globbed, _ := filepath.Glob(filepath.Join(wanted, "frame_*.jpg")); len(globbed) != 1 ||
		!strings.Contains(globbed[0], "clip4") {
		t.Fatalf("regression guard: expected Glob to wander into the sibling, got %v", globbed)
	}

	got, err := FilesIn(wanted, "frame_", ".jpg")
	if err != nil {
		t.Fatalf("FilesIn: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("FilesIn returned %v; the requested directory is empty", got)
	}
}

func TestFilesInAffixesAreOptional(t *testing.T) {
	d := t.TempDir()
	for _, n := range []string{"a.json", "b.json", "c.txt"} {
		if err := os.WriteFile(filepath.Join(d, n), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := FilesIn(d, "", ".json"); len(got) != 2 {
		t.Errorf("suffix-only filter got %d, want 2: %v", len(got), got)
	}
	if got, _ := FilesIn(d, "", ""); len(got) != 3 {
		t.Errorf("no filter got %d, want 3: %v", len(got), got)
	}
	if _, err := FilesIn(filepath.Join(d, "nope"), "", ""); err == nil {
		t.Error("FilesIn on a missing directory should report the error, not return empty")
	}
}
