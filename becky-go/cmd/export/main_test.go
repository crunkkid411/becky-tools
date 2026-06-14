package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultOutput(t *testing.T) {
	tests := []struct {
		name   string
		source string
		ext    string
		want   string
	}{
		{
			// defaultOutput preserves the Windows separator style deterministically
			// on any host, so this asserts the literal '\' result (not a host-joined
			// path) and stays green on both Windows and Linux/CI.
			name:   "mp4 from windows source",
			source: `X:\AI-2\becky-tools\test.mp4`,
			ext:    ".mp4",
			want:   `X:\AI-2\becky-tools\test_edited.mp4`,
		},
		{
			name:   "kdenlive ext",
			source: `X:\AI-2\becky-tools\clip.mov`,
			ext:    ".kdenlive",
			want:   `X:\AI-2\becky-tools\clip_edited.kdenlive`,
		},
		{
			// A posix source keeps posix separators.
			name:   "posix source",
			source: "/home/user/clip.mov",
			ext:    ".mp4",
			want:   "/home/user/clip_edited.mp4",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := defaultOutput(tc.source, tc.ext)
			if got != tc.want {
				t.Errorf("defaultOutput(%q,%q) = %q, want %q", tc.source, tc.ext, got, tc.want)
			}
		})
	}
}

func TestEscapeSubsPath(t *testing.T) {
	// The colon after the drive letter must be escaped and slashes normalized,
	// or ffmpeg's subtitles filter graph fails to parse the path.
	got := escapeSubsPath(`X:\AI-2\sub.srt`)
	if !strings.Contains(got, `X\:/AI-2/sub.srt`) {
		t.Errorf("escapeSubsPath did not escape drive colon / slashes: %q", got)
	}
	if !strings.HasPrefix(got, "'") || !strings.HasSuffix(got, "'") {
		t.Errorf("escapeSubsPath should single-quote the path: %q", got)
	}
}

func TestLoadTimeline(t *testing.T) {
	dir := t.TempDir()

	good := filepath.Join(dir, "good.json")
	writeFile(t, good, `{"version":"1","source":"X:\\v.mp4","chunks":[[0,9,99999],[9,300,1]]}`)
	tl, err := loadTimeline(good)
	if err != nil {
		t.Fatalf("loadTimeline(good) error: %v", err)
	}
	if tl.Source != `X:\v.mp4` || len(tl.Chunks) != 2 {
		t.Errorf("parsed timeline wrong: source=%q chunks=%d", tl.Source, len(tl.Chunks))
	}

	cases := map[string]string{
		"garbage":      "not json {{{",
		"no source":    `{"version":"1","chunks":[[0,9,1]]}`,
		"empty chunks": `{"version":"1","source":"X:\\v.mp4","chunks":[]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(dir, strings.ReplaceAll(name, " ", "_")+".json")
			writeFile(t, p, body)
			if _, err := loadTimeline(p); err == nil {
				t.Errorf("loadTimeline(%s) = nil error, want error", name)
			}
		})
	}

	if _, err := loadTimeline(filepath.Join(dir, "missing.json")); err == nil {
		t.Error("loadTimeline(missing) = nil error, want error")
	}
}

func TestValidateXML(t *testing.T) {
	dir := t.TempDir()

	ok := filepath.Join(dir, "ok.xml")
	writeFile(t, ok, `<mlt><producer id="p"/></mlt>`)
	if valid, note := validateXML(ok); !valid {
		t.Errorf("validateXML(ok) = false (%s), want true", note)
	}

	bad := filepath.Join(dir, "bad.xml")
	writeFile(t, bad, `<mlt><producer`)
	if valid, _ := validateXML(bad); valid {
		t.Error("validateXML(bad) = true, want false")
	}
}

func TestRound3(t *testing.T) {
	if got := round3(23.79133); got != 23.791 {
		t.Errorf("round3 = %v, want 23.791", got)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
