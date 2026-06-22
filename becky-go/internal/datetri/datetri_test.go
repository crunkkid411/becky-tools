package datetri

import (
	"encoding/json"
	"testing"
	"time"
)

// day builds a precise local-time signal for a given date+clock.
func sig(source string, trust Trust, y, mo, d, hh, mm, ss int, raw string) Signal {
	return Signal{
		Source:      source,
		Trust:       trust,
		Time:        time.Date(y, time.Month(mo), d, hh, mm, ss, 0, time.Local),
		Raw:         raw,
		TimePrecise: !(hh == 0 && mm == 0 && ss == 0),
	}
}

func TestTwoSignalAgreement_Documented(t *testing.T) {
	signals := []Signal{
		sig(SourceQuickTime, TrustStrong, 2025, 7, 4, 18, 14, 31, "2025-07-04T18:14:31-05:00"),
		sig(SourceFilename, TrustMedium, 2025, 7, 4, 18, 14, 31, "20250704_181431"),
	}
	v := Triangulate(signals, 1)
	if v.VerdictDate != "2025-07-04" {
		t.Fatalf("verdict_date = %q, want 2025-07-04", v.VerdictDate)
	}
	if v.Status != StatusDocumented {
		t.Fatalf("status = %q, want DOCUMENTED", v.Status)
	}
	if v.SingleSignal {
		t.Fatalf("single_signal = true, want false (two signals)")
	}
	if v.Confidence < 0.9 {
		t.Fatalf("confidence = %v, want >= 0.9", v.Confidence)
	}
	if len(v.Conflicts) != 0 {
		t.Fatalf("conflicts = %v, want none", v.Conflicts)
	}
}

func TestLoneStrongTag_DocumentedSingleSignal(t *testing.T) {
	signals := []Signal{
		sig(SourceEXIF, TrustStrong, 2024, 3, 1, 9, 0, 0, "2024-03-01T09:00:00Z"),
	}
	v := Triangulate(signals, 1)
	if v.Status != StatusDocumented {
		t.Fatalf("status = %q, want DOCUMENTED", v.Status)
	}
	if !v.SingleSignal {
		t.Fatalf("single_signal = false, want true (lone strong tag)")
	}
	if v.VerdictDate != "2024-03-01" {
		t.Fatalf("verdict_date = %q, want 2024-03-01", v.VerdictDate)
	}
}

func TestWeakOnly_Candidate(t *testing.T) {
	signals := []Signal{
		sig(SourceFilename, TrustMedium, 2025, 7, 4, 0, 0, 0, "20250704"),
	}
	v := Triangulate(signals, 1)
	if v.Status != StatusCandidate {
		t.Fatalf("status = %q, want CANDIDATE", v.Status)
	}
	if v.VerdictDate != "2025-07-04" {
		t.Fatalf("verdict_date = %q, want 2025-07-04", v.VerdictDate)
	}
}

func TestMTimeOnly_Unknown(t *testing.T) {
	signals := []Signal{
		sig(SourceMTime, TrustWeak, 2026, 2, 2, 11, 0, 0, "2026-02-02T11:00:00Z"),
	}
	v := Triangulate(signals, 1)
	if v.Status != StatusUnknown {
		t.Fatalf("status = %q, want UNKNOWN", v.Status)
	}
	if v.VerdictDate != "" {
		t.Fatalf("verdict_date = %q, want empty", v.VerdictDate)
	}
	// mtime must still be listed in signals so the reviewer sees it.
	if len(v.Signals) != 1 || v.Signals[0].Source != SourceMTime {
		t.Fatalf("signals = %v, want the mtime signal listed", v.Signals)
	}
	// And a remedy note must be present.
	if !containsSubstr(v.Notes, "becky-ocr") {
		t.Fatalf("notes = %v, want a remedy mentioning becky-ocr", v.Notes)
	}
}

func TestConflict_FfprobeVsOCR(t *testing.T) {
	signals := []Signal{
		sig(SourceFFprobe, TrustStrong, 2024, 3, 1, 0, 0, 0, "2024-03-01T00:00:00Z"),
		{Source: SourceOCR, Trust: TrustStrong, Time: time.Date(2025, 7, 4, 0, 0, 0, 0, time.Local), Raw: "2025-07-04", OCRConfidence: 0.93},
	}
	v := Triangulate(signals, 1)
	if v.Status != StatusConflict {
		t.Fatalf("status = %q, want CONFLICT", v.Status)
	}
	if len(v.Conflicts) == 0 {
		t.Fatalf("conflicts empty, want one naming both sources")
	}
	c := v.Conflicts[0]
	// Both dates must be named.
	if !((c.ADate == "2024-03-01" && c.BDate == "2025-07-04") || (c.ADate == "2025-07-04" && c.BDate == "2024-03-01")) {
		t.Fatalf("conflict dates = %s vs %s, want 2024-03-01 and 2025-07-04", c.ADate, c.BDate)
	}
	// Verdict goes to the higher-trust cluster. Both are strong here; on a tie the
	// earlier day wins deterministically -> ffprobe 2024-03-01.
	if v.VerdictDate != "2024-03-01" {
		t.Fatalf("verdict_date = %q, want 2024-03-01 (tie -> earlier day)", v.VerdictDate)
	}
	// Basis must mention both dates.
	if !substr(v.Basis, "2024-03-01") || !substr(v.Basis, "2025-07-04") {
		t.Fatalf("basis = %q, want both dates named", v.Basis)
	}
}

func TestMTimeDisagreement_IsNotConflict(t *testing.T) {
	signals := []Signal{
		sig(SourceQuickTime, TrustStrong, 2025, 7, 4, 18, 14, 31, "2025-07-04T18:14:31-05:00"),
		sig(SourceFilename, TrustMedium, 2025, 7, 4, 18, 14, 31, "20250704_181431"),
		sig(SourceMTime, TrustWeak, 2026, 1, 12, 9, 1, 0, "2026-01-12T09:01:00Z"),
	}
	v := Triangulate(signals, 1)
	if v.Status != StatusDocumented {
		t.Fatalf("status = %q, want DOCUMENTED (mtime disagreement is not a conflict)", v.Status)
	}
	if len(v.Conflicts) != 0 {
		t.Fatalf("conflicts = %v, want none", v.Conflicts)
	}
	if !containsSubstr(v.Notes, "UNTRUSTED") {
		t.Fatalf("notes = %v, want an mtime-untrusted explanation", v.Notes)
	}
	if v.VerdictDate != "2025-07-04" {
		t.Fatalf("verdict_date = %q, want 2025-07-04", v.VerdictDate)
	}
}

func TestToleranceWindow(t *testing.T) {
	signals := []Signal{
		sig(SourceQuickTime, TrustStrong, 2025, 7, 4, 12, 0, 0, "2025-07-04T12:00:00Z"),
		{Source: SourceOCR, Trust: TrustStrong, Time: time.Date(2025, 7, 5, 0, 0, 0, 0, time.Local), Raw: "2025-07-05", OCRConfidence: 0.97},
	}
	// tolerance 1 -> they agree -> DOCUMENTED.
	if v := Triangulate(signals, 1); v.Status != StatusDocumented {
		t.Fatalf("tolerance 1: status = %q, want DOCUMENTED", v.Status)
	}
	// tolerance 0 -> distinct days -> CONFLICT.
	if v := Triangulate(signals, 0); v.Status != StatusConflict {
		t.Fatalf("tolerance 0: status = %q, want CONFLICT", v.Status)
	}
}

func TestTwoWeakAgree_Candidate(t *testing.T) {
	// filename + mtime on the same day, no capture tag -> corroborated but no
	// strong tag -> CANDIDATE (a strong one, not UNKNOWN, not DOCUMENTED).
	signals := []Signal{
		sig(SourceFilename, TrustMedium, 2025, 7, 4, 0, 0, 0, "20250704"),
		sig(SourceMTime, TrustWeak, 2025, 7, 4, 9, 0, 0, "2025-07-04T09:00:00Z"),
	}
	v := Triangulate(signals, 1)
	if v.Status != StatusCandidate {
		t.Fatalf("status = %q, want CANDIDATE", v.Status)
	}
	if v.VerdictDate != "2025-07-04" {
		t.Fatalf("verdict_date = %q, want 2025-07-04", v.VerdictDate)
	}
}

func TestDeterminism_ByteIdenticalJSON(t *testing.T) {
	signals := []Signal{
		sig(SourceQuickTime, TrustStrong, 2025, 7, 4, 18, 14, 31, "2025-07-04T18:14:31-05:00"),
		sig(SourceFilename, TrustMedium, 2025, 7, 4, 18, 14, 31, "20250704_181431"),
		sig(SourceMTime, TrustWeak, 2026, 1, 12, 9, 1, 0, "2026-01-12T09:01:00Z"),
	}
	a, _ := json.Marshal(Triangulate(signals, 1))
	b, _ := json.Marshal(Triangulate(signals, 1))
	if string(a) != string(b) {
		t.Fatalf("non-deterministic JSON:\n%s\n%s", a, b)
	}
}

func TestNoSignals_Unknown(t *testing.T) {
	v := Triangulate(nil, 1)
	if v.Status != StatusUnknown {
		t.Fatalf("status = %q, want UNKNOWN", v.Status)
	}
	if v.VerdictDate != "" {
		t.Fatalf("verdict_date = %q, want empty", v.VerdictDate)
	}
}

func TestBestWallClock_PrefersStrongPrecise(t *testing.T) {
	signals := []Signal{
		sig(SourceFilename, TrustMedium, 2025, 7, 4, 0, 0, 0, "20250704"),
		sig(SourceQuickTime, TrustStrong, 2025, 7, 4, 18, 14, 31, "2025-07-04T18:14:31-05:00"),
	}
	v := Triangulate(signals, 1)
	if v.VerdictTimeLoc == "" {
		t.Fatalf("verdict_time_local empty, want the quicktime wall-clock")
	}
}

// --- helpers ---

func substr(s, sub string) bool { return len(s) >= len(sub) && indexOf(s, sub) >= 0 }
func containsSubstr(list []string, sub string) bool {
	for _, s := range list {
		if substr(s, sub) {
			return true
		}
	}
	return false
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
