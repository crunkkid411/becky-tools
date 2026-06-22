package datetri

import "testing"

func TestParseFilenameDate(t *testing.T) {
	cases := []struct {
		base     string
		wantOK   bool
		wantDate string // YYYY-MM-DD
		precise  bool
	}{
		{"20250704_181431.mp4", true, "2025-07-04", true},
		{"IMG_20240301_120000.mov", true, "2024-03-01", true},
		{"VID_20240301.mp4", true, "2024-03-01", false},
		{"2025-07-04 19.14.31.mov", true, "2025-07-04", true},
		{"2025-07-04.mp4", true, "2025-07-04", false},
		{"2025.07.04.mkv", true, "2025-07-04", false},
		{"Screen Recording 2025-07-04 at 9.01.05 AM.mov", true, "2025-07-04", true},
		{"random_name.mp4", false, "", false},
		{"clip_99999999.mp4", false, "", false},                    // implausible date
		{"part_20251399.mp4", false, "", false},                    // month 13 -> rejected
		{"DSC_0001.JPG", false, "", false},                         // no date token
		{`C:\Cases\20250704_181431.mp4`, true, "2025-07-04", true}, // value already a basename-able input; ParseFilenameDate scans the string
	}
	for _, c := range cases {
		got, ok := ParseFilenameDate(c.base)
		if ok != c.wantOK {
			t.Errorf("ParseFilenameDate(%q) ok = %v, want %v", c.base, ok, c.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if d := got.Time.Format("2006-01-02"); d != c.wantDate {
			t.Errorf("ParseFilenameDate(%q) date = %q, want %q", c.base, d, c.wantDate)
		}
		if got.Precise != c.precise {
			t.Errorf("ParseFilenameDate(%q) precise = %v, want %v", c.base, got.Precise, c.precise)
		}
	}
}

func TestParseOCRDate(t *testing.T) {
	cases := []struct {
		text     string
		wantOK   bool
		wantDate string
		precise  bool
	}{
		{"07/04/2025 6:14 PM", true, "2025-07-04", true},
		{"2025-07-04", true, "2025-07-04", false},
		{"03/01/2024", true, "2024-03-01", false},
		{"12/31/24", true, "2024-12-31", false},
		{"REC 07.04.2025 18:14:31", true, "2025-07-04", true},
		{"chat message hello", false, "", false},
		{"99/99/9999", false, "", false},
	}
	for _, c := range cases {
		tm, precise, ok := ParseOCRDate(c.text)
		if ok != c.wantOK {
			t.Errorf("ParseOCRDate(%q) ok = %v, want %v", c.text, ok, c.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if d := tm.Format("2006-01-02"); d != c.wantDate {
			t.Errorf("ParseOCRDate(%q) date = %q, want %q", c.text, d, c.wantDate)
		}
		if precise != c.precise {
			t.Errorf("ParseOCRDate(%q) precise = %v, want %v", c.text, precise, c.precise)
		}
	}
}

func TestSignalFromOCR_TrustScaling(t *testing.T) {
	strong, ok := SignalFromOCR(OCRDateCandidate{Text: "2025-07-04", Confidence: 0.97}, 0.80)
	if !ok || strong.Trust != TrustStrong {
		t.Fatalf("high-conf OCR trust = %q (ok=%v), want strong", strong.Trust, ok)
	}
	weak, ok := SignalFromOCR(OCRDateCandidate{Text: "2025-07-04", Confidence: 0.40}, 0.80)
	if !ok || weak.Trust != TrustWeak {
		t.Fatalf("low-conf OCR trust = %q (ok=%v), want weak", weak.Trust, ok)
	}
	if _, ok := SignalFromOCR(OCRDateCandidate{Text: "no date here", Confidence: 0.9}, 0.8); ok {
		t.Fatalf("unparseable OCR text should yield ok=false")
	}
}
