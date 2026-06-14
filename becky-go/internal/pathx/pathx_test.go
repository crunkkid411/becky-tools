package pathx

import "testing"

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
