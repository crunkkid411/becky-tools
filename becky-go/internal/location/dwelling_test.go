package location

import (
	"strings"
	"testing"
)

// Two clear rooms sharing a decor-palette signal + close capture time → SAME_DWELLING.
func TestVerdict_SameDwelling(t *testing.T) {
	// Distinct decor hashes (different rooms) but a SHARED color palette, and
	// capture times 5 minutes apart (within the 30-min window) → 2 dwelling signals.
	shared := distinctHist(0)
	clips := []Clip{
		{Index: 0, Print: Fingerprint{DecorHash: 0x0, ColorHist: shared}, CaptureTime: "2025-11-03T14:00:00Z"},
		{Index: 1, Print: Fingerprint{DecorHash: 0x3, ColorHist: shared}, CaptureTime: "2025-11-03T14:00:30Z"},
		{Index: 2, Print: Fingerprint{DecorHash: 0xFFFF0000, ColorHist: shared}, CaptureTime: "2025-11-03T14:05:00Z"},
		{Index: 3, Print: Fingerprint{DecorHash: 0xFFFF0003, ColorHist: shared}, CaptureTime: "2025-11-03T14:05:30Z"},
	}
	cr := Cluster(clips, DefaultThresholds())
	if len(cr.Rooms) != 2 {
		t.Fatalf("expected 2 distinct rooms, got %d", len(cr.Rooms))
	}
	dw, v := GroupDwellings(clips, cr, DefaultThresholds(), DefaultDwellingParams())
	if v.Level != SameDwelling {
		t.Fatalf("level = %s, want SAME_DWELLING (basis: %v)", v.Level, v.Basis)
	}
	if len(dw) != 1 {
		t.Fatalf("expected 1 dwelling, got %d", len(dw))
	}
	if v.Confidence <= 0.5 {
		t.Fatalf("SAME_DWELLING confidence = %v, want > 0.5", v.Confidence)
	}
	// basis must name the corroborating signals.
	joined := strings.Join(v.Basis, " ")
	if !strings.Contains(joined, "palette") && !strings.Contains(joined, "capture") {
		t.Fatalf("basis should name the corroborating signals, got %v", v.Basis)
	}
}

// Two rooms with NO shared signal + large decor distance → DIFFERENT_DWELLING.
func TestVerdict_DifferentDwelling(t *testing.T) {
	clips := []Clip{
		{Index: 0, Print: Fingerprint{DecorHash: 0x0, ColorHist: distinctHist(0)}, CaptureTime: "2025-01-01T10:00:00Z"},
		{Index: 1, Print: Fingerprint{DecorHash: 0x3, ColorHist: distinctHist(0)}, CaptureTime: "2025-01-01T10:00:30Z"},
		// A totally different room: far decor hash, different palette, captured months later.
		{Index: 2, Print: Fingerprint{DecorHash: 0xFFFFFFFFFFFFFFFF, ColorHist: distinctHist(30)}, CaptureTime: "2025-06-01T10:00:00Z"},
		{Index: 3, Print: Fingerprint{DecorHash: 0xFFFFFFFFFFFFFFFC, ColorHist: distinctHist(30)}, CaptureTime: "2025-06-01T10:00:30Z"},
	}
	cr := Cluster(clips, DefaultThresholds())
	if len(cr.Rooms) != 2 {
		t.Fatalf("expected 2 distinct rooms, got %d", len(cr.Rooms))
	}
	_, v := GroupDwellings(clips, cr, DefaultThresholds(), DefaultDwellingParams())
	if v.Level != DifferentDwell {
		t.Fatalf("level = %s, want DIFFERENT_DWELLING (basis: %v)", v.Level, v.Basis)
	}
}

// Two rooms sharing only ONE dwelling signal → UNDETERMINED (one weak signal).
func TestVerdict_OneSignalUndetermined(t *testing.T) {
	shared := distinctHist(0)
	clips := []Clip{
		{Index: 0, Print: Fingerprint{DecorHash: 0x0, ColorHist: shared}, CaptureTime: "2025-01-01T10:00:00Z"},
		{Index: 1, Print: Fingerprint{DecorHash: 0x3, ColorHist: shared}, CaptureTime: "2025-01-01T10:00:30Z"},
		// Room 2: SHARES the color palette (1 signal) but captured a year apart and
		// no GPS → exactly one dwelling signal.
		{Index: 2, Print: Fingerprint{DecorHash: 0xFFFF0000, ColorHist: shared}, CaptureTime: "2026-01-01T10:00:00Z"},
		{Index: 3, Print: Fingerprint{DecorHash: 0xFFFF0003, ColorHist: shared}, CaptureTime: "2026-01-01T10:00:30Z"},
	}
	cr := Cluster(clips, DefaultThresholds())
	if len(cr.Rooms) != 2 {
		t.Fatalf("expected 2 distinct rooms, got %d", len(cr.Rooms))
	}
	_, v := GroupDwellings(clips, cr, DefaultThresholds(), DefaultDwellingParams())
	if v.Level != Undetermined {
		t.Fatalf("level = %s, want UNDETERMINED (one weak signal)", v.Level)
	}
}

func TestVerdict_SingleClipUndetermined(t *testing.T) {
	clips := []Clip{{Index: 0, Print: Fingerprint{DecorHash: 0x0, ColorHist: uniformHist()}}}
	cr := Cluster(clips, DefaultThresholds())
	_, v := GroupDwellings(clips, cr, DefaultThresholds(), DefaultDwellingParams())
	if v.Level != Undetermined {
		t.Fatalf("single clip → level = %s, want UNDETERMINED", v.Level)
	}
	if !strings.Contains(strings.Join(v.Basis, " "), "one clip") {
		t.Fatalf("basis should say only one clip, got %v", v.Basis)
	}
}

func TestVerdict_AllSameRoom(t *testing.T) {
	clips := []Clip{
		{Index: 0, Print: Fingerprint{DecorHash: 0x0, ColorHist: distinctHist(0)}},
		{Index: 1, Print: Fingerprint{DecorHash: 0x1, ColorHist: distinctHist(0)}},
		{Index: 2, Print: Fingerprint{DecorHash: 0x3, ColorHist: distinctHist(0)}},
	}
	cr := Cluster(clips, DefaultThresholds())
	if len(cr.Rooms) != 1 {
		t.Fatalf("expected 1 room, got %d", len(cr.Rooms))
	}
	_, v := GroupDwellings(clips, cr, DefaultThresholds(), DefaultDwellingParams())
	if v.Level != SameRoom {
		t.Fatalf("level = %s, want SAME_ROOM", v.Level)
	}
	if v.Confidence < 0.8 {
		t.Fatalf("clean same-room confidence = %v, want >= 0.8", v.Confidence)
	}
}
