package mediainfo

import (
	"math"
	"strings"
	"testing"
)

func TestParseVolumeDetect_RealAudio(t *testing.T) {
	stderr := `[Parsed_volumedetect_0 @ 0x1] n_samples: 1193984
[Parsed_volumedetect_0 @ 0x1] mean_volume: -21.3 dB
[Parsed_volumedetect_0 @ 0x1] max_volume: -2.4 dB`
	v, ok := parseVolumeDetect(stderr)
	if !ok {
		t.Fatal("expected ok for real audio output")
	}
	if v.MeanDB != -21.3 || v.MaxDB != -2.4 {
		t.Fatalf("mean/max = %v/%v, want -21.3/-2.4", v.MeanDB, v.MaxDB)
	}
	if !v.Audible {
		t.Fatal("-21.3 dB mean should be audible")
	}
	if !strings.Contains(v.Describe(), "audible") {
		t.Fatalf("Describe = %q", v.Describe())
	}
}

func TestParseVolumeDetect_Silence(t *testing.T) {
	for _, mean := range []string{"-91.0", "-inf"} {
		stderr := "mean_volume: " + mean + " dB\nmax_volume: " + mean + " dB"
		v, ok := parseVolumeDetect(stderr)
		if !ok {
			t.Fatalf("expected ok for mean=%s", mean)
		}
		if v.Audible {
			t.Fatalf("mean %s dB must be flagged SILENT, got audible", mean)
		}
		if !strings.Contains(v.Describe(), "SILENT") {
			t.Fatalf("Describe for %s = %q, want SILENT", mean, v.Describe())
		}
	}
}

func TestParseVolumeDetect_NoMatch(t *testing.T) {
	if _, ok := parseVolumeDetect("ffmpeg: some unrelated error\n"); ok {
		t.Fatal("expected ok=false when no volumedetect line is present")
	}
}

func TestParseDB_Inf(t *testing.T) {
	if !math.IsInf(parseDB("-inf"), -1) {
		t.Fatal("'-inf' should parse to -Inf")
	}
	if parseDB("-12.5") != -12.5 {
		t.Fatal("'-12.5' should parse to -12.5")
	}
}
