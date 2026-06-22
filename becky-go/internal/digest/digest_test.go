package digest

import (
	"strings"
	"testing"
	"time"

	"becky-go/internal/report"
)

// fixedClock returns a deterministic clock for byte-stable output.
func fixedClock() func() time.Time {
	return func() time.Time { return time.Date(2026, 6, 22, 14, 3, 11, 0, time.UTC) }
}

// richReport is a hand-authored report.Report with one DOCUMENTED person
// (voice+face), one CANDIDATE single-signal unknown, one DOCUMENTED event, and
// one sub-second motion review item.
func richReport() report.Report {
	return report.Report{
		Source:   "reddit-livestream-2025-08-14",
		Duration: 862,
		Entities: []report.Entity{
			{
				Name: "John Clancy", Type: "voice+face", Confidence: 0.88,
				CorroboratedBy: []string{"voice", "face"}, CorroboratedCount: 2,
				Concluded: true, Tag: "DOCUMENTED",
			},
			{
				Name: "Mark", Type: "voice", Confidence: 0.71,
				Concluded: false, Tag: "CANDIDATE",
			},
		},
		Conclusions: []report.Finding{
			{What: "John taps her hip", When: "0:13", WhenSec: 13, Confidence: 0.9, Sources: []string{"events"}, Tag: "DOCUMENTED"},
		},
		ReviewItems: []report.Finding{
			{What: "sub-second motion burst at 588.0s - missed by 1-fps sampling (score 0.82)", When: "9:48", WhenSec: 588, Confidence: 0.82, Sources: []string{"motion"}, Tag: "CANDIDATE"},
		},
	}
}

func trustedCapture() CaptureMeta {
	return CaptureMeta{
		CaptureTimeLocal:  "2025-08-14T19:32:07-05:00",
		UTCOffset:         "-05:00",
		CaptureTimeSource: "quicktime",
		DeviceName:        "Samsung Galaxy S25 Ultra",
		GPS:               &MetaGPS{Latitude: 41.8781, Longitude: -87.6298},
	}
}

func TestMarkdown_DocumentedPersonNamedPlainly(t *testing.T) {
	d := Build([]ClipInput{{
		Stem: "reddit-livestream-2025-08-14", Input: "/cases/reddit/reddit-livestream-2025-08-14.mp4",
		Status: "ok", SidecarDir: "/cases/reddit/pipeline-out/reddit-livestream-2025-08-14",
		Report: richReport(), Capture: trustedCapture(), HasReport: true,
	}}, CorpusInfo{Folder: "/cases/reddit", KB: "kb-final"}, fixedClock())
	md := Markdown(d)

	wantContains := []string{
		"John Clancy - DOCUMENTED.",
		"[source: quicktime - trusted]",
		"2025-08-14T19:32:07-05:00",
		"Mark - CANDIDATE, single signal, not concluded.",
		"GPS 41.878100, -87.629800",
		"Device: Samsung Galaxy S25 Ultra",
		"0:13 - John taps her hip. [DOCUMENTED, events]",
	}
	for _, w := range wantContains {
		if !strings.Contains(md, w) {
			t.Errorf("DIGEST.md missing %q\n--- got ---\n%s", w, md)
		}
	}
	// Candidate must NOT appear in the concluded corpus people line.
	if strings.Contains(md, "People concluded across the corpus: John Clancy, Mark") {
		t.Errorf("candidate Mark leaked into concluded people line:\n%s", md)
	}
}

func TestMarkdown_UnknownsListedAndReviewMarked(t *testing.T) {
	d := Build([]ClipInput{{
		Stem: "c", Report: richReport(), Capture: trustedCapture(), HasReport: true, Status: "ok",
	}}, CorpusInfo{}, fixedClock())
	md := Markdown(d)

	if !strings.Contains(md, "Unknowns / needs a human:") {
		t.Fatalf("no Unknowns section:\n%s", md)
	}
	// The sub-second motion review item must appear with REVIEW.
	if !strings.Contains(md, "REVIEW") {
		t.Errorf("review item not marked REVIEW:\n%s", md)
	}
	if !strings.Contains(md, "sub-second motion burst") {
		t.Errorf("sub-second burst not surfaced as an unknown:\n%s", md)
	}
}

func TestMarkdown_UntrustedMtimeEmitsUntrustedWord(t *testing.T) {
	d := Build([]ClipInput{{
		Stem: "backyard-2025-08-29", Status: "ok", HasReport: true,
		Report: report.Report{Source: "backyard-2025-08-29"},
		Capture: CaptureMeta{
			CaptureTimeLocal:  "2025-08-29T00:00:00Z",
			CaptureTimeSource: sourceMTime,
		},
	}}, CorpusInfo{}, fixedClock())
	md := Markdown(d)

	if !strings.Contains(md, "UNTRUSTED") {
		t.Errorf("mtime-only clip did not emit the literal UNTRUSTED word:\n%s", md)
	}
	// And it must be listed under unverified_dates in the corpus.
	found := false
	for _, s := range d.Corpus.UnverifiedDates {
		if s == "backyard-2025-08-29" {
			found = true
		}
	}
	if !found {
		t.Errorf("mtime-only clip not in unverified_dates: %v", d.Corpus.UnverifiedDates)
	}
}

func TestMarkdown_NoTablesNoEmojiNoBoxDrawing(t *testing.T) {
	d := Build([]ClipInput{{
		Stem: "c", Report: richReport(), Capture: trustedCapture(), HasReport: true, Status: "ok",
	}}, CorpusInfo{Folder: "/cases/reddit"}, fixedClock())
	md := Markdown(d)

	// No GitHub-style table pipe rows.
	for _, line := range strings.Split(md, "\n") {
		if strings.Count(line, "|") >= 2 {
			t.Errorf("table-pipe row found (violates ACCESSIBILITY.md): %q", line)
		}
	}
	// No box-drawing characters.
	for _, r := range []string{"┌", "┐", "└", "┘", "─", "│", "├", "┤", "┬", "┴", "═", "╔"} {
		if strings.Contains(md, r) {
			t.Errorf("box-drawing char %q found (violates ACCESSIBILITY.md)", r)
		}
	}
	// No emoji-as-meaning (the ones internal/report/markdown.go uses).
	for _, e := range []string{"✅", "⚫", "❌", "⚠", "🟢", "🔴"} {
		if strings.Contains(md, e) {
			t.Errorf("emoji %q found (violates ACCESSIBILITY.md)", e)
		}
	}
}

func TestMarkdown_EmptyUnknownsSaysNoneFlagged(t *testing.T) {
	rep := report.Report{
		Source:   "clean",
		Duration: 30,
		Entities: []report.Entity{{Name: "Jane", Concluded: true, Tag: "DOCUMENTED", CorroboratedBy: []string{"voice", "face"}}},
		Conclusions: []report.Finding{
			{What: "she waves", When: "0:05", WhenSec: 5, Tag: "DOCUMENTED", Sources: []string{"events"}},
		},
		// No ReviewItems.
	}
	d := Build([]ClipInput{{
		Stem: "clean", Report: rep, Capture: trustedCapture(), HasReport: true, Status: "ok",
	}}, CorpusInfo{}, fixedClock())
	md := Markdown(d)

	// The per-clip Unknowns block must read "none flagged", never be omitted.
	idx := strings.Index(md, "Unknowns / needs a human:")
	if idx < 0 {
		t.Fatalf("Unknowns block omitted:\n%s", md)
	}
	after := md[idx:]
	if !strings.Contains(after[:120], "none flagged") {
		t.Errorf("empty Unknowns did not say 'none flagged':\n%s", after[:120])
	}
}

func TestBuild_Deterministic(t *testing.T) {
	clips := []ClipInput{{
		Stem: "c", Input: "/x/c.mp4", Status: "ok", SidecarDir: "/x/out/c",
		Report: richReport(), Capture: trustedCapture(), HasReport: true,
	}}
	info := CorpusInfo{Folder: "/x", KB: "kb"}
	a := Markdown(Build(clips, info, fixedClock()))
	b := Markdown(Build(clips, info, fixedClock()))
	if a != b {
		t.Errorf("Markdown not deterministic over identical input")
	}
}

func TestBuild_CorpusRollupUnionAndCaptureRange(t *testing.T) {
	clip1 := ClipInput{
		Stem: "a", Status: "ok", HasReport: true,
		Report: report.Report{Entities: []report.Entity{
			{Name: "John Clancy", Concluded: true, Tag: "DOCUMENTED", CorroboratedBy: []string{"voice", "face"}},
			{Name: "Shelby Reed", Concluded: true, Tag: "DOCUMENTED", CorroboratedBy: []string{"voice", "face"}},
		}},
		Capture: CaptureMeta{CaptureTimeLocal: "2025-08-14T19:32:07-05:00", CaptureTimeSource: "quicktime"},
	}
	clip2 := ClipInput{
		Stem: "b", Status: "ok", HasReport: true,
		Report: report.Report{Entities: []report.Entity{
			{Name: "Shelby Reed", Concluded: true, Tag: "DOCUMENTED", CorroboratedBy: []string{"voice", "face"}},
		}},
		Capture: CaptureMeta{CaptureTimeLocal: "2025-09-02T11:07:44-05:00", CaptureTimeSource: "quicktime"},
	}
	clip3 := ClipInput{
		Stem: "c", Status: "ok", HasReport: true,
		Report:  report.Report{},
		Capture: CaptureMeta{CaptureTimeLocal: "2025-08-29T00:00:00Z", CaptureTimeSource: sourceMTime},
	}
	d := Build([]ClipInput{clip1, clip2, clip3}, CorpusInfo{}, fixedClock())

	want := []string{"John Clancy", "Shelby Reed"}
	if len(d.Corpus.PeopleConcluded) != 2 || d.Corpus.PeopleConcluded[0] != want[0] || d.Corpus.PeopleConcluded[1] != want[1] {
		t.Errorf("people_concluded = %v, want de-duplicated union %v", d.Corpus.PeopleConcluded, want)
	}
	if d.Corpus.EarliestCapture != "2025-08-14T19:32:07-05:00" {
		t.Errorf("earliest = %q, want trusted earliest", d.Corpus.EarliestCapture)
	}
	if d.Corpus.LatestCapture != "2025-09-02T11:07:44-05:00" {
		t.Errorf("latest = %q, want trusted latest (untrusted mtime excluded)", d.Corpus.LatestCapture)
	}
	if len(d.Corpus.UnverifiedDates) != 1 || d.Corpus.UnverifiedDates[0] != "c" {
		t.Errorf("unverified_dates = %v, want [c]", d.Corpus.UnverifiedDates)
	}
}

func TestBuild_DegradedClipRendersStubNoPanic(t *testing.T) {
	d := Build([]ClipInput{{
		Stem: "broken", Status: "partial", HasReport: false,
		Report: report.Report{Source: "broken", Degraded: true},
		Notes:  []string{"step diarize failed"},
	}}, CorpusInfo{Folder: "/x"}, fixedClock())
	md := Markdown(d)

	if md == "" {
		t.Fatal("Markdown empty for degraded clip")
	}
	if !strings.Contains(md, "## 1. broken") {
		t.Errorf("degraded clip section missing:\n%s", md)
	}
	if !strings.Contains(md, "nobody identified") {
		t.Errorf("degraded clip should say nobody identified:\n%s", md)
	}
	if !strings.Contains(md, "broken is PARTIAL") {
		t.Errorf("partial status not noted:\n%s", md)
	}
}

func TestBuild_EmptyCorpusDegradedExitZero(t *testing.T) {
	d := Build(nil, CorpusInfo{Folder: "/empty"}, fixedClock())
	if !d.Degraded {
		t.Error("empty corpus should be degraded")
	}
	jb, err := JSON(d)
	if err != nil {
		t.Fatalf("JSON encode: %v", err)
	}
	s := string(jb)
	// Slices must be [] not null (chain-friendly).
	for _, key := range []string{`"clips": []`, `"people_concluded": []`, `"unverified_dates": []`, `"steps": []`} {
		if !strings.Contains(s, key) {
			t.Errorf("digest.json missing %q (slice should be [] not null):\n%s", key, s)
		}
	}
	if !strings.Contains(s, `"degraded": true`) {
		t.Errorf("digest.json should mark degraded true:\n%s", s)
	}
	md := Markdown(d)
	if !strings.Contains(md, "DEGRADED") {
		t.Errorf("empty corpus DIGEST.md should state DEGRADED:\n%s", md)
	}
}

func TestBuild_MoreMomentsCap(t *testing.T) {
	concl := make([]report.Finding, 8)
	for i := range concl {
		concl[i] = report.Finding{What: "moment", When: "0:01", WhenSec: 1, Tag: "DOCUMENTED", Sources: []string{"events"}}
	}
	d := Build([]ClipInput{{
		Stem: "c", Status: "ok", HasReport: true,
		Report: report.Report{Conclusions: concl},
	}}, CorpusInfo{}, fixedClock())
	if d.Clips[0].MoreMoments != 3 {
		t.Errorf("MoreMoments = %d, want 3 (8 - cap 5)", d.Clips[0].MoreMoments)
	}
	if len(d.Clips[0].KeyMoments) != 5 {
		t.Errorf("KeyMoments len = %d, want 5", len(d.Clips[0].KeyMoments))
	}
	if !strings.Contains(Markdown(d), "(+3 more in report.json)") {
		t.Errorf("more-moments line missing")
	}
}
