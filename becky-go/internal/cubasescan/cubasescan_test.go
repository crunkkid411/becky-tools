package cubasescan

import (
	"strings"
	"testing"
)

// fakeCPR builds a synthetic binary that embeds plugin names the way a .cpr does:
// some as ASCII, some as UTF-16LE, surrounded by binary noise.
func fakeCPR() []byte {
	var b []byte
	b = append(b, 0x00, 0x01, 0xff, 0xfe) // noise
	b = append(b, []byte("FabFilter Pro-Q 3")...)
	b = append(b, 0x00, 0x00)
	b = append(b, []byte("Superior Drummer 3")...)
	b = append(b, 0x07, 0x00)
	// a UTF-16LE plugin name
	for _, r := range "Serum 2" {
		b = append(b, byte(r), 0x00)
	}
	b = append(b, 0xff)
	// a track/bus label (not a plugin)
	b = append(b, []byte("DRUMS BUS")...)
	b = append(b, 0x00)
	// noise that shouldn't match
	b = append(b, []byte("xq")...) // too short
	return b
}

func TestAllStrings_asciiAndUtf16(t *testing.T) {
	all := AllStrings(fakeCPR(), 4)
	joined := strings.Join(all, "|")
	for _, want := range []string{"FabFilter Pro-Q 3", "Superior Drummer 3", "Serum 2", "DRUMS BUS"} {
		if !strings.Contains(joined, want) {
			t.Errorf("AllStrings missed %q (got %v)", want, all)
		}
	}
	// "xq" is below minLen and must be excluded.
	if strings.Contains(joined, "|xq") || joined == "xq" {
		t.Error("short noise string should be excluded")
	}
}

func TestFindPlugins(t *testing.T) {
	got := FindPlugins(AllStrings(fakeCPR(), 4))
	joined := strings.Join(got, "|")
	for _, want := range []string{"FabFilter Pro-Q 3", "Superior Drummer 3", "Serum 2"} {
		if !strings.Contains(joined, want) {
			t.Errorf("FindPlugins missed %q (got %v)", want, got)
		}
	}
	// A bus label is NOT a plugin.
	if strings.Contains(joined, "DRUMS BUS") {
		t.Error("a bus label should not be reported as a plugin")
	}
}

func TestScan_report(t *testing.T) {
	r := Scan("template.cpr", fakeCPR())
	if r.Path != "template.cpr" {
		t.Errorf("path = %q", r.Path)
	}
	if len(r.Plugins) < 3 {
		t.Errorf("expected >=3 plugins, got %v", r.Plugins)
	}
	if r.Strings == 0 {
		t.Error("string count should be > 0")
	}
}

func TestScan_emptyAndJunkSafe(t *testing.T) {
	if r := Scan("x", nil); len(r.Plugins) != 0 {
		t.Error("nil data should yield no plugins, not panic")
	}
	if r := Scan("x", []byte{0x00, 0x01, 0x02, 0x03}); len(r.Plugins) != 0 {
		t.Error("pure binary noise should yield no plugins")
	}
}
