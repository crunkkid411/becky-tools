package datetri

import "testing"

// Real basenames from Jordan's evidence folder. Before MM-DD-YYYY support, the
// five US-ordered ones parsed to nothing and fell back to the file's COPY date,
// which put January footage above May footage in the "newest" list.
func TestParseFilenameDateOnRealEvidenceNames(t *testing.T) {
	cases := []struct{ name, want string }{
		{"18_2026-05-19-penguin.mp4", "2026-05-19"},
		{"ScreenRecording_04-25-2026 23-12-01_1.MP4", "2026-04-25"},
		{"15_01-14-2026.mp4", "2026-01-14"},
		{"23_05-18-2026-clips.mp4", "2026-05-18"},
		{"2026-02-26_TakingBack2007 Studio Serotonin 0%_[pNMS91b6Zqo].mp4", "2026-02-26"},
		{"screen-20260220-001650-1771564474160.mp4", "2026-02-20"},
		{"screen-20260117-213747.mp4", "2026-01-17"},
		{"VID_20260102_200149.mp4", "2026-01-02"},
		{"ScreenRecording_01-09-2026 18-08-32_1.mp4", "2026-01-09"},
		{"ScreenRecording_01-02-2026-15-45-55_1.mp4", "2026-01-02"},
		{"2026-01-12 18-43-01.mp4", "2026-01-12"},
	}
	for _, c := range cases {
		got, ok := ParseFilenameDate(c.name)
		if !ok {
			t.Errorf("%s: no date parsed, want %s", c.name, c.want)
			continue
		}
		if g := got.Time.Format("2006-01-02"); g != c.want {
			t.Errorf("%s: got %s, want %s", c.name, g, c.want)
		}
	}
}

// Files with no date token must stay unparsed so the caller can fall back.
func TestParseFilenameDateRejectsNonDates(t *testing.T) {
	for _, n := range []string{
		"last-ndc.ts",
		"TakingBack2007-ft-Green-Sparkle-Dragon-[zimJlAOYja8].mp4",
	} {
		if d, ok := ParseFilenameDate(n); ok {
			t.Errorf("%s: parsed %s, want no date", n, d.Time.Format("2006-01-02"))
		}
	}
}

// A first field over 12 cannot be a month, so DD-MM-YYYY is read correctly.
func TestParseUSHandlesDayFirstWhenUnambiguous(t *testing.T) {
	got, ok := ParseFilenameDate("clip_25-04-2026.mp4")
	if !ok || got.Time.Format("2006-01-02") != "2026-04-25" {
		t.Errorf("25-04-2026 = %v ok=%v, want 2026-04-25", got.Time, ok)
	}
}
