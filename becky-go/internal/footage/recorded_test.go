package footage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The exact failure from Jordan's evidence folder: every file on a copied
// evidence drive carries a late-May mtime (the COPY date), so ordering by mtime
// put January footage above May footage in the "newest" list. The date burned
// into the filename survives the copy and must win.
func TestResolveRecordedPrefersFilenameOverCopyDate(t *testing.T) {
	dir := t.TempDir()
	copyDate := time.Date(2026, 5, 29, 12, 0, 0, 0, time.Local)

	// name -> the capture date its filename claims
	want := map[string]string{
		"15_01-14-2026.mp4":                         "2026-01-14",
		"23_05-18-2026-clips.mp4":                   "2026-05-18",
		"18_2026-05-19-penguin.mp4":                 "2026-05-19",
		"ScreenRecording_04-25-2026 23-12-01_1.MP4": "2026-04-25",
		"VID_20260102_200149.mp4":                   "2026-01-02",
	}
	for name, w := range want {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, copyDate, copyDate); err != nil {
			t.Fatal(err)
		}
		v := Video{Path: p, Name: name, Mtime: copyDate.Unix()}
		resolveRecorded(&v)
		if got := time.Unix(v.Recorded, 0).Format("2006-01-02"); got != w {
			t.Errorf("%s: recorded %s, want %s (the filename date, not the copy date)", name, got, w)
		}
		if v.RecordedFrom != "filename" {
			t.Errorf("%s: RecordedFrom = %q, want \"filename\"", name, v.RecordedFrom)
		}
		if v.RecordedText == "" {
			t.Errorf("%s: RecordedText is empty — the card would show no date and the sort would be unverifiable", name)
		}
	}

	// And the ordering those dates produce must be right, which is the thing the
	// user actually sees: January footage must NOT outrank May footage.
	jan := Video{Path: filepath.Join(dir, "15_01-14-2026.mp4"), Mtime: copyDate.Unix()}
	may := Video{Path: filepath.Join(dir, "23_05-18-2026-clips.mp4"), Mtime: copyDate.Unix() - 86400}
	resolveRecorded(&jan)
	resolveRecorded(&may)
	if !(may.Recorded > jan.Recorded) {
		t.Error("May footage must sort newer than January footage even when its file was copied first")
	}
}

// No date token in the name: fall back to the file date, and SAY it is the file
// date so the card never presents a copy date as if it were a recording date.
func TestResolveRecordedFallsBackHonestly(t *testing.T) {
	v := Video{Path: `E:\case\last-ndc.ts`, Name: "last-ndc.ts", Mtime: 1780502340}
	resolveRecorded(&v)
	if v.Recorded != 1780502340 || v.RecordedFrom != "file" {
		t.Errorf("fallback = %d from %q, want the mtime from \"file\"", v.Recorded, v.RecordedFrom)
	}
	if want := " (file date)"; len(v.RecordedText) < len(want) ||
		v.RecordedText[len(v.RecordedText)-len(want):] != want {
		t.Errorf("RecordedText = %q, want it marked %q so a guess never reads as a fact", v.RecordedText, want)
	}
}

// An explicit human-entered sidecar date outranks both.
func TestResolveRecordedPrefersSidecarMeta(t *testing.T) {
	v := Video{Path: `E:\case\18_2026-05-19-penguin.mp4`, Mtime: 1780502340}
	v.Meta.Date = "2026-03-01"
	resolveRecorded(&v)
	if got := time.Unix(v.Recorded, 0).Format("2006-01-02"); got != "2026-03-01" || v.RecordedFrom != "meta" {
		t.Errorf("recorded = %s from %q, want 2026-03-01 from \"meta\"", got, v.RecordedFrom)
	}
}
