package captions

// Tests run fully offline: the yt-dlp fetch seam (FetchAutoSubs) is faked so the
// whole decision flow — id extraction, coverage parsing, edit detection, fetch
// path, degrade paths — is exercised without network or ffprobe. Duration-
// dependent math is verified directly on decide(); the fetch/coverage/id logic
// (the parts unique to this package) is covered end-to-end through Analyze with a
// faked FetchAutoSubs. The fake .mp4 files are unprobeable, so Analyze's duration
// is 0 and the "duration unknown" branch is what those end-to-end cases assert.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile is a tiny helper to drop a file with content.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// srt builds a minimal valid SRT whose single cue ends at endSec seconds.
func srt(endSec int) string {
	return "1\n00:00:00,000 --> " + tc(endSec) + "\nhello world\n"
}

// tc formats whole seconds as an SRT timecode HH:MM:SS,000.
func tc(sec int) string {
	h := sec / 3600
	m := (sec % 3600) / 60
	s := sec % 60
	return pad2(h) + ":" + pad2(m) + ":" + pad2(s) + ",000"
}

func pad2(n int) string {
	if n < 10 {
		return "0" + itoa(n)
	}
	return itoa(n)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// restoreFetch swaps in a fake FetchAutoSubs and restores it after the test.
func restoreFetch(t *testing.T, fake func(id, outPath string) (string, error)) {
	t.Helper()
	prev := FetchAutoSubs
	FetchAutoSubs = fake
	t.Cleanup(func() { FetchAutoSubs = prev })
}

// stemPath returns the video path with ".mp4" replaced by suffix.
func stemPath(video, suffix string) string {
	return video[:len(video)-len(".mp4")] + suffix
}

func TestExtractID(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"standard yt-dlp name", "2026-06-16_TakingBack2007_became_Scene_[46T0KmQA7Eg].mp4", "46T0KmQA7Eg"},
		{"id with dash and underscore", "clip_[T0r3hrJPW-g].mkv", "T0r3hrJPW-g"},
		{"no bracket token", "02-03-2026_live_720p.mp4", ""},
		{"bare 11-char run not in brackets", "abcdefghijk_video.mp4", ""},
		{"first token wins", "[AAAAAAAAAAA]_[BBBBBBBBBBB].mp4", "AAAAAAAAAAA"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := extractID(c.in); got != c.want {
				t.Fatalf("extractID(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestOfficialSRTPath(t *testing.T) {
	got := OfficialSRTPath(filepath.Join("E:", "footage", "vid_[ID0000000A].mp4"))
	want := filepath.Join("E:", "footage", "vid_[ID0000000A].en.srt")
	if got != want {
		t.Fatalf("OfficialSRTPath = %q, want %q", got, want)
	}
}

func TestDecide_CompleteVsEdited(t *testing.T) {
	// Coverage >= 90% of duration → use_official, not edited.
	d := decide(Decision{VideoDuration: 1000, OfficialCoverage: 950}, "local")
	if d.Action != ActionUseOfficial {
		t.Fatalf("complete: action = %q, want use_official", d.Action)
	}
	if d.Edited {
		t.Fatalf("complete: edited should be false")
	}
	if d.CoverageRatio < 0.94 || d.CoverageRatio > 0.96 {
		t.Fatalf("complete: coverage_ratio = %v, want ~0.95", d.CoverageRatio)
	}

	// Coverage well under 90% (2h srt for a 3h video) → local_needed, edited.
	d = decide(Decision{VideoDuration: 10800, OfficialCoverage: 7200}, "local")
	if d.Action != ActionLocalNeeded {
		t.Fatalf("edited: action = %q, want local_needed", d.Action)
	}
	if !d.Edited {
		t.Fatalf("edited: edited should be true")
	}
	if d.CoverageRatio < 0.66 || d.CoverageRatio > 0.67 {
		t.Fatalf("edited: coverage_ratio = %v, want ~0.667", d.CoverageRatio)
	}
}

func TestDecide_BoundaryAtCoverageOK(t *testing.T) {
	// Exactly at the floor counts as complete (>=).
	d := decide(Decision{VideoDuration: 1000, OfficialCoverage: 900}, "local")
	if d.Action != ActionUseOfficial || d.Edited {
		t.Fatalf("at-floor: got action=%q edited=%v, want use_official/false", d.Action, d.Edited)
	}
	// Just under the floor is edited.
	d = decide(Decision{VideoDuration: 1000, OfficialCoverage: 899}, "local")
	if d.Action != ActionLocalNeeded || !d.Edited {
		t.Fatalf("under-floor: got action=%q edited=%v, want local_needed/true", d.Action, d.Edited)
	}
}

func TestDecide_NoCoverage(t *testing.T) {
	d := decide(Decision{VideoDuration: 1000, OfficialCoverage: 0}, "fetched")
	if d.Action != ActionLocalNeeded {
		t.Fatalf("no-coverage: action = %q, want local_needed", d.Action)
	}
	if d.Edited {
		t.Fatalf("no-coverage: edited should be false (no cues != edited)")
	}
}

func TestDecide_DurationUnknown(t *testing.T) {
	// Real cues but no probe-able duration → accept official, ratio 0, not edited.
	d := decide(Decision{VideoDuration: 0, OfficialCoverage: 1234}, "local")
	if d.Action != ActionUseOfficial {
		t.Fatalf("dur-unknown: action = %q, want use_official", d.Action)
	}
	if d.Edited {
		t.Fatalf("dur-unknown: edited should be false")
	}
	if !strings.Contains(d.Reason, "duration unavailable") {
		t.Fatalf("dur-unknown: reason should explain missing duration, got %q", d.Reason)
	}
}

func TestAnalyze_LocalOfficialPresent(t *testing.T) {
	// An official .en.srt is present whose coverage we can read; duration is
	// unprobeable here, so this exercises the local-official + duration-unknown
	// path and confirms we DON'T fetch.
	dir := t.TempDir()
	video := filepath.Join(dir, "vid_[ABCDEFGHIJK].mp4")
	writeFile(t, video, "not a real video")
	writeFile(t, stemPath(video, ".en.srt"), srt(120))

	restoreFetch(t, func(id, outPath string) (string, error) {
		t.Fatalf("must NOT fetch when a local official srt exists")
		return "", nil
	})

	d, err := Analyze(video, Options{})
	if err != nil {
		t.Fatalf("Analyze err: %v", err)
	}
	if d.ID != "ABCDEFGHIJK" {
		t.Fatalf("id = %q, want ABCDEFGHIJK", d.ID)
	}
	if d.Fetched {
		t.Fatalf("should not have fetched")
	}
	if d.OfficialCoverage != 120 {
		t.Fatalf("coverage = %v, want 120", d.OfficialCoverage)
	}
	if d.Action != ActionUseOfficial {
		t.Fatalf("action = %q, want use_official", d.Action)
	}
	if filepath.Base(d.OfficialSRT) != "vid_[ABCDEFGHIJK].en.srt" {
		t.Fatalf("official_srt = %q", d.OfficialSRT)
	}
}

func TestAnalyze_PrefersENOverBare(t *testing.T) {
	// Both <stem>.en.srt and <stem>.srt present → the .en.srt wins.
	dir := t.TempDir()
	video := filepath.Join(dir, "vid_[ABCDEFGHIJK].mp4")
	writeFile(t, video, "x")
	writeFile(t, stemPath(video, ".srt"), srt(60))
	writeFile(t, stemPath(video, ".en.srt"), srt(120))
	d, err := Analyze(video, Options{Offline: true})
	if err != nil {
		t.Fatalf("Analyze err: %v", err)
	}
	if filepath.Base(d.OfficialSRT) != "vid_[ABCDEFGHIJK].en.srt" {
		t.Fatalf("should prefer .en.srt, got %q", d.OfficialSRT)
	}
}

func TestAnalyze_IgnoresLocalFile(t *testing.T) {
	// A becky-made _LOCAL.srt must NOT count as an official transcript: with no
	// official srt and --offline, the decision is local_needed even though a
	// _LOCAL file sits right there.
	dir := t.TempDir()
	video := filepath.Join(dir, "vid_[ABCDEFGHIJK].mp4")
	writeFile(t, video, "not a real video")
	writeFile(t, stemPath(video, "_LOCAL.srt"), srt(120))

	d, err := Analyze(video, Options{Offline: true})
	if err != nil {
		t.Fatalf("Analyze err: %v", err)
	}
	if d.OfficialSRT != "" {
		t.Fatalf("a _LOCAL srt must not be treated as official; got %q", d.OfficialSRT)
	}
	if d.Action != ActionLocalNeeded {
		t.Fatalf("action = %q, want local_needed", d.Action)
	}
}

func TestAnalyze_OfflineSkipsFetch(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "vid_[ABCDEFGHIJK].mp4")
	writeFile(t, video, "x")
	restoreFetch(t, func(id, outPath string) (string, error) {
		t.Fatalf("--offline must not fetch")
		return "", nil
	})
	d, err := Analyze(video, Options{Offline: true})
	if err != nil {
		t.Fatalf("Analyze err: %v", err)
	}
	if d.Action != ActionLocalNeeded || d.Fetched {
		t.Fatalf("offline: got action=%q fetched=%v, want local_needed/false", d.Action, d.Fetched)
	}
}

func TestAnalyze_NoIDNoFetch(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "02-03-2026_live_720p.mp4") // no [id] token
	writeFile(t, video, "x")
	restoreFetch(t, func(id, outPath string) (string, error) {
		t.Fatalf("no id ⇒ must not fetch")
		return "", nil
	})
	d, err := Analyze(video, Options{})
	if err != nil {
		t.Fatalf("Analyze err: %v", err)
	}
	if d.ID != "" {
		t.Fatalf("id should be empty, got %q", d.ID)
	}
	if d.Action != ActionLocalNeeded {
		t.Fatalf("action = %q, want local_needed", d.Action)
	}
}

func TestAnalyze_FetchSuccess(t *testing.T) {
	// No local official srt + id present + online: the fake fetch writes the srt
	// to the guaranteed path, and Analyze parses its coverage.
	dir := t.TempDir()
	video := filepath.Join(dir, "vid_[ABCDEFGHIJK].mp4")
	writeFile(t, video, "x")
	wantPath := OfficialSRTPath(video)

	restoreFetch(t, func(id, outPath string) (string, error) {
		if id != "ABCDEFGHIJK" {
			t.Fatalf("fetch id = %q, want ABCDEFGHIJK", id)
		}
		if outPath != wantPath {
			t.Fatalf("fetch outPath = %q, want %q", outPath, wantPath)
		}
		writeFile(t, outPath, srt(300))
		return outPath, nil
	})

	d, err := Analyze(video, Options{})
	if err != nil {
		t.Fatalf("Analyze err: %v", err)
	}
	if !d.Fetched {
		t.Fatalf("fetched should be true")
	}
	if d.OfficialCoverage != 300 {
		t.Fatalf("coverage = %v, want 300", d.OfficialCoverage)
	}
	if d.OfficialSRT != wantPath {
		t.Fatalf("official_srt = %q, want %q", d.OfficialSRT, wantPath)
	}
	if d.Action != ActionUseOfficial {
		t.Fatalf("action = %q, want use_official", d.Action)
	}
}

func TestAnalyze_FetchUnavailable(t *testing.T) {
	// yt-dlp finds no captions (private/removed/none): a clean local_needed.
	dir := t.TempDir()
	video := filepath.Join(dir, "vid_[ABCDEFGHIJK].mp4")
	writeFile(t, video, "x")
	restoreFetch(t, func(id, outPath string) (string, error) {
		return "", errFake("no English subtitles available")
	})
	d, err := Analyze(video, Options{})
	if err != nil {
		t.Fatalf("Analyze should not error on an unavailable video: %v", err)
	}
	if d.Action != ActionLocalNeeded {
		t.Fatalf("action = %q, want local_needed", d.Action)
	}
	if d.Fetched {
		t.Fatalf("fetched should be false")
	}
	if !strings.Contains(d.Reason, "no official captions available online") {
		t.Fatalf("reason = %q", d.Reason)
	}
}

func TestAnalyze_FetchClaimsButNoFile(t *testing.T) {
	// The seam returns success but the file isn't actually there → local_needed.
	dir := t.TempDir()
	video := filepath.Join(dir, "vid_[ABCDEFGHIJK].mp4")
	writeFile(t, video, "x")
	restoreFetch(t, func(id, outPath string) (string, error) {
		return outPath, nil // claims success but writes nothing
	})
	d, err := Analyze(video, Options{})
	if err != nil {
		t.Fatalf("Analyze err: %v", err)
	}
	if d.Action != ActionLocalNeeded || d.Fetched {
		t.Fatalf("got action=%q fetched=%v, want local_needed/false", d.Action, d.Fetched)
	}
}

func TestAnalyze_EmptyPath(t *testing.T) {
	d, err := Analyze("   ", Options{})
	if err == nil {
		t.Fatalf("expected an error for an empty path")
	}
	if d.Action != ActionLocalNeeded {
		t.Fatalf("even on error, action should be local_needed, got %q", d.Action)
	}
}

func TestAnalyze_Deterministic(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "vid_[ABCDEFGHIJK].mp4")
	writeFile(t, video, "x")
	writeFile(t, stemPath(video, ".en.srt"), srt(60))
	d1, _ := Analyze(video, Options{Offline: true})
	d2, _ := Analyze(video, Options{Offline: true})
	if d1 != d2 {
		t.Fatalf("non-deterministic: %+v != %+v", d1, d2)
	}
}

// errFake is a tiny error type so tests don't import errors/fmt just for one.
type errFake string

func (e errFake) Error() string { return string(e) }
