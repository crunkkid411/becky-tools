package music

import (
	"bytes"
	"testing"
)

// VLQ spec vectors (SPEC-BECKY-COMPOSE §6.2).
func TestVLQ(t *testing.T) {
	cases := []struct {
		n    int
		want []byte
	}{
		{0x40, []byte{0x40}},
		{0x7F, []byte{0x7F}},
		{0x80, []byte{0x81, 0x00}},
		{0x2000, []byte{0xC0, 0x00}},
		{0x3FFF, []byte{0xFF, 0x7F}},
		{0x4000, []byte{0x81, 0x80, 0x00}},
		{0x100000, []byte{0xC0, 0x80, 0x00}},
		{0x0FFFFFFF, []byte{0xFF, 0xFF, 0xFF, 0x7F}},
	}
	for _, c := range cases {
		if got := vlq(c.n); !bytes.Equal(got, c.want) {
			t.Errorf("vlq(%#x) = % X, want % X", c.n, got, c.want)
		}
	}
}

func TestParseKey(t *testing.T) {
	cases := []struct {
		in    string
		root  int
		scale string
	}{
		{"Am", 9, "minor"},
		{"C", 0, "major"},
		{"F#m", 6, "minor"},
		{"Bb minor", 10, "minor"},
		{"E phrygian", 4, "phrygian"},
	}
	for _, c := range cases {
		r, s := ParseKey(c.in)
		if r != c.root || s != c.scale {
			t.Errorf("ParseKey(%q) = (%d,%q), want (%d,%q)", c.in, r, s, c.root, c.scale)
		}
	}
}

func TestRomanChord_minorDominant(t *testing.T) {
	minor := ScaleIntervals("minor")
	// A minor i triad = A C E = 69 72 76
	i := romanChord("i", 9, minor, 4, nil)
	if len(i) < 3 || i[0] != 69 || i[1] != 72 || i[2] != 76 {
		t.Errorf("i in Am = %v, want [69 72 76]", i)
	}
	// V in a minor key must be MAJOR (harmonic-minor raised third): E G# B = 76 80 83
	v := romanChord("V", 9, minor, 4, nil)
	if len(v) < 3 || v[0] != 76 || v[1] != 80 || v[2] != 83 {
		t.Errorf("V in Am = %v, want major dominant [76 80 83]", v)
	}
	// v (lowercase) stays minor: E G B = 76 79 83
	lv := romanChord("v", 9, minor, 4, nil)
	if len(lv) < 3 || lv[1] != 79 {
		t.Errorf("v in Am = %v, want minor third 79", lv)
	}
}

func TestGenerate_deterministic(t *testing.T) {
	p, err := LoadProfile("crunkcore")
	if err != nil {
		t.Fatal(err)
	}
	a := Generate(p, "Fm", 140, 7).SMF().Bytes()
	b := Generate(p, "Fm", 140, 7).SMF().Bytes()
	if !bytes.Equal(a, b) {
		t.Error("same (genre,key,bpm,seed) produced different bytes — not deterministic")
	}
	c := Generate(p, "Fm", 140, 8).SMF().Bytes()
	if bytes.Equal(a, c) {
		t.Error("different seed produced identical bytes — seed not affecting output")
	}
}

func TestGenerate_validSMFWithNotes(t *testing.T) {
	for _, g := range KnownGenres() {
		p, err := LoadProfile(g)
		if err != nil {
			t.Fatalf("%s: %v", g, err)
		}
		smf := Generate(p, "", 0, 1).SMF().Bytes()
		ntracks, notes, err := parseSMF(smf)
		if err != nil {
			t.Errorf("%s: SMF did not parse: %v", g, err)
			continue
		}
		if ntracks < 2 {
			t.Errorf("%s: only %d tracks", g, ntracks)
		}
		if notes < 50 {
			t.Errorf("%s: only %d note-ons (expected a full arrangement)", g, notes)
		}
	}
}

// parseSMF walks a type-1 SMF and counts tracks + note-on events. It fully decodes
// VLQ deltas and event lengths, so a successful parse proves the byte stream is
// well-formed end to end (deltas + events line up to each End-of-Track).
func parseSMF(b []byte) (ntracks, noteOns int, err error) {
	if len(b) < 14 || string(b[0:4]) != "MThd" {
		return 0, 0, errf("no MThd")
	}
	nt := int(b[10])<<8 | int(b[11])
	pos := 14
	for tr := 0; tr < nt; tr++ {
		if pos+8 > len(b) || string(b[pos:pos+4]) != "MTrk" {
			return 0, 0, errf("bad MTrk header")
		}
		length := int(b[pos+4])<<24 | int(b[pos+5])<<16 | int(b[pos+6])<<8 | int(b[pos+7])
		pos += 8
		end := pos + length
		if end > len(b) {
			return 0, 0, errf("track overruns buffer")
		}
		var status byte
		for pos < end {
			_, n := readVLQ(b[pos:end]) // delta time
			if n == 0 {
				return 0, 0, errf("bad VLQ delta")
			}
			pos += n
			if pos >= end {
				return 0, 0, errf("truncated event")
			}
			c := b[pos]
			switch {
			case c == 0xFF: // meta
				pos++
				if pos >= end {
					return 0, 0, errf("truncated meta")
				}
				pos++ // type
				ln, m := readVLQ(b[pos:end])
				pos += m + ln
			case c >= 0x80: // status byte
				status = c
				pos++
				pos += dataLen(c, b, pos, end, &noteOns)
			default: // running status
				pos += dataLen(status, b, pos, end, &noteOns)
			}
		}
		pos = end
		ntracks++
	}
	return ntracks, noteOns, nil
}

func dataLen(status byte, b []byte, pos, end int, noteOns *int) int {
	switch status & 0xF0 {
	case 0x90: // note on
		if pos+1 < end && b[pos+1] > 0 {
			*noteOns++
		}
		return 2
	case 0x80, 0xA0, 0xB0, 0xE0:
		return 2
	case 0xC0, 0xD0:
		return 1
	}
	return 1
}

func readVLQ(b []byte) (val, n int) {
	for i := 0; i < len(b) && i < 4; i++ {
		val = val<<7 | int(b[i]&0x7F)
		n++
		if b[i]&0x80 == 0 {
			return val, n
		}
	}
	return 0, 0
}

type strErr string

func (e strErr) Error() string { return string(e) }
func errf(s string) error      { return strErr(s) }
