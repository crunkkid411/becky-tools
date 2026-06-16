package winctx

import (
	"errors"
	"testing"
)

// ---------------------------------------------------------------------------
// parseExplorerOutput — pure-Go, OS-independent, testable everywhere.
// ---------------------------------------------------------------------------

func TestParseExplorerOutput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
		want []ExplorerWindow
	}{
		{
			name: "empty string yields empty slice",
			raw:  "",
			want: nil,
		},
		{
			name: "single path LF",
			raw:  `C:\Users\Jordan\Desktop` + "\n",
			want: []ExplorerWindow{
				{Path: `C:\Users\Jordan\Desktop`, Title: "Desktop"},
			},
		},
		{
			name: "single path no trailing newline",
			raw:  `C:\Users\Jordan\Desktop`,
			want: []ExplorerWindow{
				{Path: `C:\Users\Jordan\Desktop`, Title: "Desktop"},
			},
		},
		{
			name: "CRLF line endings (Windows PowerShell output)",
			raw:  "C:\\Users\\Jordan\\Desktop\r\nC:\\Users\\Jordan\\Downloads\r\n",
			want: []ExplorerWindow{
				{Path: `C:\Users\Jordan\Desktop`, Title: "Desktop"},
				{Path: `C:\Users\Jordan\Downloads`, Title: "Downloads"},
			},
		},
		{
			name: "blank lines are skipped",
			raw:  "\nC:\\Footage\n\nC:\\Music\n\n",
			want: []ExplorerWindow{
				{Path: `C:\Footage`, Title: "Footage"},
				{Path: `C:\Music`, Title: "Music"},
			},
		},
		{
			name: "whitespace-only lines are skipped",
			raw:  "   \r\nC:\\Footage\r\n   \r\n",
			want: []ExplorerWindow{
				{Path: `C:\Footage`, Title: "Footage"},
			},
		},
		{
			name: "duplicate paths deduplicated first occurrence kept",
			raw:  "C:\\Footage\nC:\\Music\nC:\\Footage\n",
			want: []ExplorerWindow{
				{Path: `C:\Footage`, Title: "Footage"},
				{Path: `C:\Music`, Title: "Music"},
			},
		},
		{
			name: "drive root path",
			raw:  "C:\\\n",
			want: []ExplorerWindow{
				{Path: `C:\`, Title: `C:\`},
			},
		},
		{
			name: "multiple paths preserve order",
			raw:  "C:\\A\nC:\\B\nC:\\C\n",
			want: []ExplorerWindow{
				{Path: `C:\A`, Title: "A"},
				{Path: `C:\B`, Title: "B"},
				{Path: `C:\C`, Title: "C"},
			},
		},
		{
			name: "forward-slash path",
			raw:  "/home/jordan/music\n",
			want: []ExplorerWindow{
				{Path: "/home/jordan/music", Title: "music"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := parseExplorerOutput(tc.raw)
			if len(got) != len(tc.want) {
				t.Fatalf("len=%d want %d\ngot:  %v\nwant: %v", len(got), len(tc.want), got, tc.want)
			}
			for i, w := range tc.want {
				if got[i].Path != w.Path {
					t.Errorf("[%d] Path: got %q, want %q", i, got[i].Path, w.Path)
				}
				if got[i].Title != w.Title {
					t.Errorf("[%d] Title: got %q, want %q", i, got[i].Title, w.Title)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// explorerTitle helper — pure Go.
// ---------------------------------------------------------------------------

func TestExplorerTitle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path string
		want string
	}{
		{`C:\Users\Jordan\Desktop`, "Desktop"},
		{`C:\Users\Jordan\`, "Jordan"},
		{`C:\`, `C:\`},
		{`C:\Footage`, "Footage"},
		{"/home/jordan/music", "music"},
		{"/home/jordan/", "jordan"},
		{"Desktop", "Desktop"},
		{"", ""},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			got := explorerTitle(tc.path)
			if got != tc.want {
				t.Errorf("explorerTitle(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// parseForeground — mirrors parseForegroundOutput logic in pure Go so tests
// compile and run on every OS (the real function is behind //go:build windows).
// ---------------------------------------------------------------------------

// parseForeground is a test-local re-implementation of parseForegroundOutput so
// we can table-test the foreground-fallback logic without OS constraints. Any
// change to the real function should be mirrored here.
func parseForeground(raw string) string {
	const prefix = "FOREGROUND:"
	first, rest, _ := cutString(raw, "\n")
	first = trimTrailingCR(first)
	if len(first) >= len(prefix) && first[:len(prefix)] == prefix {
		if fg := trimAllSpace(first[len(prefix):]); fg != "" {
			return fg
		}
	}
	if windows := parseExplorerOutput(rest); len(windows) > 0 {
		return windows[0].Path
	}
	return ""
}

// cutString splits s at the first occurrence of sep, returning before, after,
// and whether sep was found. Mirrors strings.Cut without the import to keep
// the test helpers self-contained.
func cutString(s, sep string) (before, after string, found bool) {
	if i := findStr(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):], true
	}
	return s, "", false
}

func findStr(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func trimTrailingCR(s string) string {
	for len(s) > 0 && s[len(s)-1] == '\r' {
		s = s[:len(s)-1]
	}
	return s
}

func trimAllSpace(s string) string {
	start := 0
	for start < len(s) && isSpace(s[start]) {
		start++
	}
	end := len(s)
	for end > start && isSpace(s[end-1]) {
		end--
	}
	return s[start:end]
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n'
}

func TestParseForegroundOutput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "foreground window is Explorer",
			raw:  "FOREGROUND:C:\\Footage\r\nC:\\Music\r\n",
			want: `C:\Footage`,
		},
		{
			name: "foreground not Explorer falls back to first open folder",
			raw:  "FOREGROUND:\r\nC:\\Music\r\nC:\\Desktop\r\n",
			want: `C:\Music`,
		},
		{
			name: "no Explorer windows open at all",
			raw:  "FOREGROUND:\r\n",
			want: "",
		},
		{
			name: "empty output",
			raw:  "",
			want: "",
		},
		{
			name: "foreground only no other windows",
			raw:  "FOREGROUND:C:\\Downloads\r\n",
			want: `C:\Downloads`,
		},
		{
			name: "foreground with trailing whitespace",
			raw:  "FOREGROUND:C:\\Footage  \r\nC:\\Music\r\n",
			want: `C:\Footage`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := parseForeground(tc.raw)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ErrUnsupportedOS sentinel type check.
// ---------------------------------------------------------------------------

func TestErrUnsupportedOSType(t *testing.T) {
	if !errors.Is(ErrUnsupportedOS, ErrUnsupportedOS) {
		t.Error("ErrUnsupportedOS must satisfy errors.Is against itself")
	}
	if ErrUnsupportedOS.Error() == "" {
		t.Error("ErrUnsupportedOS.Error() must not be empty")
	}
}
