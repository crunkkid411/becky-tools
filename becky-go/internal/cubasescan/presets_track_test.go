package cubasescan

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// makeEmbeddedPreset builds one VST3 preset region (the same wrapper a standalone
// .vstpreset uses, since a host persists plugin state that way). Its List trailer offset is
// relative to the start of THIS region, which is how ParseTrackPreset reads each embedded
// region. An optional name prefix is prepended OUTSIDE the region so we can simulate Cubase
// storing a readable module name near the state.
func makeEmbeddedPreset(classID string, comp []byte) []byte {
	hdr := make([]byte, 48)
	copy(hdr[0:4], "VST3")
	binary.LittleEndian.PutUint32(hdr[4:8], 1)
	copy(hdr[8:40], classID) // padded with zeros to 32
	compOff := uint64(48)
	listOff := compOff + uint64(len(comp))
	binary.LittleEndian.PutUint64(hdr[40:48], listOff)

	var b bytes.Buffer
	b.Write(hdr)
	b.Write(comp)
	b.WriteString("List")
	cnt := make([]byte, 4)
	binary.LittleEndian.PutUint32(cnt, 1)
	b.Write(cnt)
	entry := make([]byte, 20)
	copy(entry[0:4], "Comp")
	binary.LittleEndian.PutUint64(entry[4:12], compOff)
	binary.LittleEndian.PutUint64(entry[12:20], uint64(len(comp)))
	b.Write(entry)
	return b.Bytes()
}

// makeTrackPreset glues several embedded presets into one container, each preceded by some
// Cubase-style framing bytes carrying the module's readable name (so name-proximity pairing
// has something to find).
func makeTrackPreset(t *testing.T, names, classIDs []string, comps [][]byte) []byte {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("TKPRESET-HEADER-JUNK\x00\x00") // outer container framing
	for i := range classIDs {
		// readable module name near the state (UTF-16LE, as Cubase often stores it)
		for j := 0; j < len(names[i]); j++ {
			b.WriteByte(names[i][j])
			b.WriteByte(0x00)
		}
		b.WriteString("\x00\x00") // a separator
		b.Write(makeEmbeddedPreset(classIDs[i], comps[i]))
	}
	return b.Bytes()
}

func TestParseTrackPreset_threePluginStrip(t *testing.T) {
	names := []string{"FabFilter Pro-Q 3", "Pro-C 2", "Ozone Maximizer"}
	classIDs := []string{
		"AAAA1111AAAA1111AAAA1111AAAA1111",
		"BBBB2222BBBB2222BBBB2222BBBB2222",
		"CCCC3333CCCC3333CCCC3333CCCC3333",
	}
	comps := [][]byte{
		[]byte("eq-bands=8 hipass=80"),
		[]byte("ratio=2 attack=med threshold=-18"),
		[]byte("ceiling=-0.3 character=2"),
	}
	data := makeTrackPreset(t, names, classIDs, comps)

	cs, ok := ParseTrackPreset(data)
	if !ok {
		t.Fatal("a 3-plugin strip should parse")
	}
	if len(cs.Plugins) != 3 {
		t.Fatalf("Plugins = %d, want 3", len(cs.Plugins))
	}
	// chain order + class ids + chunk sizes must match what we embedded.
	for i, p := range cs.Plugins {
		if p.ClassID != classIDs[i] {
			t.Errorf("plugin[%d].ClassID = %q, want %q", i, p.ClassID, classIDs[i])
		}
		if p.CompBytes != len(comps[i]) {
			t.Errorf("plugin[%d].CompBytes = %d, want %d", i, p.CompBytes, len(comps[i]))
		}
		if !bytes.Equal(p.Chunk, comps[i]) {
			t.Errorf("plugin[%d] chunk = %q, want %q", i, p.Chunk, comps[i])
		}
	}
	// offsets must be strictly increasing (chain/file order preserved).
	for i := 1; i < len(cs.Plugins); i++ {
		if cs.Plugins[i].Offset <= cs.Plugins[i-1].Offset {
			t.Errorf("offset not increasing: [%d]=%d <= [%d]=%d",
				i, cs.Plugins[i].Offset, i-1, cs.Plugins[i-1].Offset)
		}
	}
}

func TestParseTrackPreset_pairsNamesByProximity(t *testing.T) {
	names := []string{"FabFilter Pro-Q 3", "Ozone Maximizer"}
	classIDs := []string{
		"AAAA1111AAAA1111AAAA1111AAAA1111",
		"CCCC3333CCCC3333CCCC3333CCCC3333",
	}
	comps := [][]byte{[]byte("eq"), []byte("ceiling")}
	data := makeTrackPreset(t, names, classIDs, comps)

	cs, ok := ParseTrackPreset(data)
	if !ok {
		t.Fatal("should parse")
	}
	if cs.Plugins[0].Name != "FabFilter Pro-Q 3" {
		t.Errorf("plugin[0].Name = %q, want %q", cs.Plugins[0].Name, "FabFilter Pro-Q 3")
	}
	if cs.Plugins[1].Name != "Ozone Maximizer" {
		t.Errorf("plugin[1].Name = %q, want %q", cs.Plugins[1].Name, "Ozone Maximizer")
	}
	// PluginNames cross-check should contain both recognized names.
	if len(cs.PluginNames) != 2 {
		t.Errorf("PluginNames = %v, want 2 entries", cs.PluginNames)
	}
}

func TestParseTrackPreset_singlePlugin(t *testing.T) {
	data := makeTrackPreset(t,
		[]string{"Serum 2"},
		[]string{"DDDD4444DDDD4444DDDD4444DDDD4444"},
		[][]byte{[]byte("wavetable-state-blob")},
	)
	cs, ok := ParseTrackPreset(data)
	if !ok {
		t.Fatal("single-plugin strip should parse")
	}
	if len(cs.Plugins) != 1 {
		t.Fatalf("Plugins = %d, want 1", len(cs.Plugins))
	}
	if cs.Plugins[0].ClassID != "DDDD4444DDDD4444DDDD4444DDDD4444" {
		t.Errorf("ClassID = %q", cs.Plugins[0].ClassID)
	}
	if string(cs.Plugins[0].Chunk) != "wavetable-state-blob" {
		t.Errorf("Chunk = %q", cs.Plugins[0].Chunk)
	}
}

func TestParseTrackPreset_rejectsJunk(t *testing.T) {
	if _, ok := ParseTrackPreset([]byte("no markers here, just text and stuff padding padding")); ok {
		t.Error("a buffer with no VST3 markers should not parse")
	}
	if _, ok := ParseTrackPreset(nil); ok {
		t.Error("nil should not parse")
	}
	if _, ok := ParseTrackPreset([]byte("VST3")); ok {
		t.Error("too-short buffer should not parse")
	}
}

func TestParseTrackPreset_truncatedRegionSafe(t *testing.T) {
	data := makeTrackPreset(t,
		[]string{"Pro-Q 3", "Pro-C 2"},
		[]string{"AAAA1111AAAA1111AAAA1111AAAA1111", "BBBB2222BBBB2222BBBB2222BBBB2222"},
		[][]byte{[]byte("first-plugin-state"), []byte("second-plugin-state")},
	)
	// chop mid-second-region: the first plugin must still come through, no panic.
	chopped := data[:len(data)-15]
	cs, ok := ParseTrackPreset(chopped)
	if !ok {
		t.Fatal("the intact first plugin should still parse")
	}
	if len(cs.Plugins) < 1 {
		t.Fatal("expected at least the first plugin")
	}
	if cs.Plugins[0].ClassID != "AAAA1111AAAA1111AAAA1111AAAA1111" {
		t.Errorf("first ClassID = %q", cs.Plugins[0].ClassID)
	}
	if string(cs.Plugins[0].Chunk) != "first-plugin-state" {
		t.Errorf("first chunk = %q", cs.Plugins[0].Chunk)
	}
}

func TestParseTrackPreset_headerOnlyRegionDegrades(t *testing.T) {
	// a VST3 region with a zero list offset (no chunk) must yield a PluginState with the
	// class id but an empty chunk, not a crash.
	hdr := make([]byte, 48)
	copy(hdr[0:4], "VST3")
	copy(hdr[8:40], "EEEE5555EEEE5555EEEE5555EEEE5555")
	binary.LittleEndian.PutUint64(hdr[40:48], 0) // no list
	var b bytes.Buffer
	b.WriteString("frame")
	b.Write(hdr)
	cs, ok := ParseTrackPreset(b.Bytes())
	if !ok {
		t.Fatal("a valid header with no chunk should still register the plugin")
	}
	if len(cs.Plugins) != 1 {
		t.Fatalf("Plugins = %d, want 1", len(cs.Plugins))
	}
	if cs.Plugins[0].ClassID != "EEEE5555EEEE5555EEEE5555EEEE5555" {
		t.Errorf("ClassID = %q", cs.Plugins[0].ClassID)
	}
	if cs.Plugins[0].CompBytes != 0 {
		t.Errorf("CompBytes = %d, want 0", cs.Plugins[0].CompBytes)
	}
}
