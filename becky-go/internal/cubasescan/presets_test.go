package cubasescan

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// makeVSTPreset builds a minimal valid .vstpreset embedding a class id and a Comp
// state chunk, the way Cubase would save one.
func makeVSTPreset(classID string, comp []byte) []byte {
	hdr := make([]byte, 48)
	copy(hdr[0:4], "VST3")
	binary.LittleEndian.PutUint32(hdr[4:8], 1)
	copy(hdr[8:40], classID) // padded with zeros to 32
	// data chunks start right after the 48-byte header.
	compOff := uint64(48)
	listOff := compOff + uint64(len(comp))
	binary.LittleEndian.PutUint64(hdr[40:48], listOff)

	var b bytes.Buffer
	b.Write(hdr)
	b.Write(comp)
	// the List
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

func TestParseVSTPreset_extractsSettings(t *testing.T) {
	// Pretend this is "my 1176 settings": a class id + a state chunk.
	classID := "ABCD1234ABCD1234ABCD1234ABCD1234"
	settings := []byte("ratio=4 attack=fast 1176-blue-stripe-state-blob")
	pre := makeVSTPreset(classID, settings)

	got, ok := ParseVSTPreset(pre)
	if !ok {
		t.Fatal("should parse a valid .vstpreset")
	}
	if got.ClassID != classID {
		t.Errorf("classID = %q, want %q", got.ClassID, classID)
	}
	if got.CompBytes != len(settings) {
		t.Errorf("CompBytes = %d, want %d", got.CompBytes, len(settings))
	}
	if !bytes.Equal(got.Chunk(), settings) {
		t.Errorf("the extracted state chunk does not match the saved settings:\n got %q\nwant %q", got.Chunk(), settings)
	}
}

func TestParseVSTPreset_rejectsNonPreset(t *testing.T) {
	if _, ok := ParseVSTPreset([]byte("not a preset")); ok {
		t.Error("non-VST3 data should not parse")
	}
	if _, ok := ParseVSTPreset(nil); ok {
		t.Error("nil should not parse")
	}
}

func TestParseVSTPreset_truncatedSafe(t *testing.T) {
	pre := makeVSTPreset("X", []byte("data"))
	// chop it mid-chunk — must not panic, must degrade.
	got, ok := ParseVSTPreset(pre[:30])
	if !ok {
		// a header-truncated file may be rejected; that's fine as long as no panic.
		return
	}
	_ = got
}
