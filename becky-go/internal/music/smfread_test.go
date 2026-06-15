package music

import (
	"bytes"
	"errors"
	"testing"
)

// TestDecodeVLQ_vectors checks the reader's VLQ decoder against the canonical SMF
// spec vectors (the same set the writer's TestVLQ encodes), proving decode is the
// exact inverse of the writer's vlq().
func TestDecodeVLQ_vectors(t *testing.T) {
	cases := []struct {
		bytes []byte
		want  int
		n     int
	}{
		{[]byte{0x00}, 0x00, 1},
		{[]byte{0x40}, 0x40, 1},
		{[]byte{0x7F}, 0x7F, 1},
		{[]byte{0x81, 0x00}, 0x80, 2},
		{[]byte{0xC0, 0x00}, 0x2000, 2},
		{[]byte{0xFF, 0x7F}, 0x3FFF, 2},
		{[]byte{0x81, 0x80, 0x00}, 0x4000, 3},
		{[]byte{0xC0, 0x80, 0x00}, 0x100000, 3},
		{[]byte{0xFF, 0xFF, 0xFF, 0x7F}, 0x0FFFFFFF, 4},
	}
	for _, c := range cases {
		got, n, ok := decodeVLQ(c.bytes)
		if !ok {
			t.Errorf("decodeVLQ(% X) not ok", c.bytes)
			continue
		}
		if got != c.want || n != c.n {
			t.Errorf("decodeVLQ(% X) = (%#x,%d), want (%#x,%d)", c.bytes, got, n, c.want, c.n)
		}
	}
}

// TestDecodeVLQ_roundTrip feeds the writer's vlq() output back through decodeVLQ.
func TestDecodeVLQ_roundTrip(t *testing.T) {
	for _, n := range []int{0, 1, 127, 128, 8192, 16383, 16384, 0x0FFFFFFF} {
		enc := vlq(n)
		got, used, ok := decodeVLQ(enc)
		if !ok || got != n || used != len(enc) {
			t.Errorf("round-trip %d: vlq=% X decode=(%d,%d,%v)", n, enc, got, used, ok)
		}
	}
}

// TestDecodeVLQ_truncated: a continuation byte with no terminator must not panic.
func TestDecodeVLQ_truncated(t *testing.T) {
	if _, _, ok := decodeVLQ([]byte{0x81}); ok {
		t.Error("decodeVLQ should fail on a dangling continuation byte")
	}
	if _, _, ok := decodeVLQ(nil); ok {
		t.Error("decodeVLQ should fail on empty input")
	}
}

// TestParseSMF_roundTripWriter builds a small song with the EXISTING writer API,
// serializes it, parses it back, and asserts division/tempo/timesig/notes match.
func TestParseSMF_roundTripWriter(t *testing.T) {
	f := NewFile(480)
	tr := f.AddTrack()
	tr.Tempo(0, 140)
	tr.TimeSig(0, 7, 8)
	tr.Program(0, 0, 30)
	tr.Note(0, 240, 0, 60, 100)   // C4
	tr.Note(240, 240, 0, 64, 90)  // E4
	tr.Note(480, 480, 0, 67, 110) // G4
	data := f.Bytes()

	song, err := ParseSMF(data)
	if err != nil {
		t.Fatalf("ParseSMF: %v", err)
	}
	if song.Format != 1 {
		t.Errorf("Format = %d, want 1", song.Format)
	}
	if song.Division != 480 {
		t.Errorf("Division = %d, want 480", song.Division)
	}
	if len(song.Tracks) != 1 {
		t.Fatalf("got %d tracks, want 1", len(song.Tracks))
	}

	var tempo, timesig, prog, noteOns, noteOffs, eot int
	for _, e := range song.Tracks[0].Events {
		switch e.Kind {
		case KindTempo:
			tempo++
			if e.BPM != 140 {
				t.Errorf("tempo BPM = %d, want 140", e.BPM)
			}
		case KindTimeSig:
			timesig++
			if e.Numerator != 7 || e.Denominator != 8 {
				t.Errorf("timesig = %d/%d, want 7/8", e.Numerator, e.Denominator)
			}
		case KindProgramChange:
			prog++
			if e.Key != 30 {
				t.Errorf("program = %d, want 30", e.Key)
			}
		case KindNoteOn:
			noteOns++
		case KindNoteOff:
			noteOffs++
		case KindEndOfTrack:
			eot++
		}
	}
	if tempo != 1 || timesig != 1 || prog != 1 {
		t.Errorf("meta counts: tempo=%d timesig=%d prog=%d, want 1/1/1", tempo, timesig, prog)
	}
	if noteOns != 3 || noteOffs != 3 {
		t.Errorf("notes: %d on / %d off, want 3/3", noteOns, noteOffs)
	}
	if eot != 1 {
		t.Errorf("end-of-track count = %d, want 1", eot)
	}

	// Verify first note's identity and absolute timing survived the round-trip.
	first := firstNoteOn(song.Tracks[0].Events)
	if first == nil || first.Key != 60 || first.Velocity != 100 || first.Tick != 0 {
		t.Errorf("first note-on = %+v, want key 60 vel 100 tick 0", first)
	}
}

// TestParseSMF_runningStatus hand-builds a track that uses running status (two
// note-ons sharing one 0x90 status byte) and asserts both are decoded.
func TestParseSMF_runningStatus(t *testing.T) {
	// Body: dt=0, 90 3C 64 (note on C4 v100); dt=0, 3E 64 (running: note on D4 v100).
	body := []byte{
		0x00, 0x90, 0x3C, 0x64,
		0x00, 0x3E, 0x64,
		0x00, 0xFF, 0x2F, 0x00, // end of track
	}
	data := buildSMF(1, 1, 96, [][]byte{body})

	song, err := ParseSMF(data)
	if err != nil {
		t.Fatalf("ParseSMF: %v", err)
	}
	evs := song.Tracks[0].Events
	var ons []ParsedEvent
	for _, e := range evs {
		if e.Kind == KindNoteOn {
			ons = append(ons, e)
		}
	}
	if len(ons) != 2 {
		t.Fatalf("running status: got %d note-ons, want 2", len(ons))
	}
	if ons[0].Key != 0x3C || ons[1].Key != 0x3E {
		t.Errorf("running status keys = %d,%d, want 60,62", ons[0].Key, ons[1].Key)
	}
	if ons[1].Status != 0x90 || ons[1].Channel != 0 {
		t.Errorf("running status not carried: status=%#x ch=%d", ons[1].Status, ons[1].Channel)
	}
}

// TestParseSMF_noteOnZeroVelocity: a note-on with velocity 0 must classify as off.
func TestParseSMF_noteOnZeroVelocity(t *testing.T) {
	body := []byte{
		0x00, 0x90, 0x3C, 0x00, // note-on vel 0 == note-off
		0x00, 0xFF, 0x2F, 0x00,
	}
	data := buildSMF(0, 1, 480, [][]byte{body})
	song, err := ParseSMF(data)
	if err != nil {
		t.Fatalf("ParseSMF: %v", err)
	}
	got := song.Tracks[0].Events[0]
	if got.Kind != KindNoteOff {
		t.Errorf("vel-0 note-on Kind = %v, want KindNoteOff", got.Kind)
	}
}

// TestParseSMF_tempoAndTimeSig parses hand-built tempo + time-signature metas.
func TestParseSMF_tempoAndTimeSig(t *testing.T) {
	// 120 BPM = 500000 us/qn = 0x07A120; time sig 6/8 -> nn=6, dd=3 (2^3=8).
	body := []byte{
		0x00, 0xFF, 0x51, 0x03, 0x07, 0xA1, 0x20,
		0x00, 0xFF, 0x58, 0x04, 0x06, 0x03, 0x18, 0x08,
		0x00, 0xFF, 0x2F, 0x00,
	}
	data := buildSMF(0, 1, 480, [][]byte{body})
	song, err := ParseSMF(data)
	if err != nil {
		t.Fatalf("ParseSMF: %v", err)
	}
	evs := song.Tracks[0].Events
	if evs[0].Kind != KindTempo || evs[0].MicrosPerQ != 500000 || evs[0].BPM != 120 {
		t.Errorf("tempo = %+v, want 500000us/120bpm", evs[0])
	}
	if evs[1].Kind != KindTimeSig || evs[1].Numerator != 6 || evs[1].Denominator != 8 {
		t.Errorf("timesig = %d/%d, want 6/8", evs[1].Numerator, evs[1].Denominator)
	}
}

// TestParseSMF_sysExAndUnknownMeta: SysEx and an unknown meta are stored, not lost.
func TestParseSMF_sysExAndUnknownMeta(t *testing.T) {
	body := []byte{
		0x00, 0xF0, 0x03, 0x41, 0x42, 0xF7, // sysex body len 3
		0x00, 0xFF, 0x7F, 0x02, 0xDE, 0xAD, // sequencer-specific meta (unknown to us)
		0x00, 0xFF, 0x2F, 0x00,
	}
	data := buildSMF(0, 1, 480, [][]byte{body})
	song, err := ParseSMF(data)
	if err != nil {
		t.Fatalf("ParseSMF: %v", err)
	}
	evs := song.Tracks[0].Events
	if evs[0].Kind != KindSysEx || !bytes.Equal(evs[0].Data, []byte{0x41, 0x42, 0xF7}) {
		t.Errorf("sysex = %+v, want body 41 42 F7", evs[0])
	}
	if evs[1].Kind != KindMeta || evs[1].MetaType != 0x7F || !bytes.Equal(evs[1].Data, []byte{0xDE, 0xAD}) {
		t.Errorf("unknown meta = %+v, want type 7F body DE AD", evs[1])
	}
}

// TestParseSMF_malformed verifies every malformed input degrades to an error
// (no panic). Table-driven, mirroring freshness_test style.
func TestParseSMF_malformed(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"short header", []byte("MThd")},
		{"bad magic", append([]byte("XXXX"), make([]byte, 10)...)},
		{"truncated track header", buildSMFTruncatedTrackHeader()},
		{"bad chunk length overruns", buildSMFBadTrackLength()},
		{"dangling vlq delta", buildSMFDanglingDelta()},
		{"truncated channel data", buildSMFTruncatedChannel()},
		{"truncated meta payload", buildSMFTruncatedMeta()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("ParseSMF panicked on %q: %v", c.name, r)
				}
			}()
			_, err := ParseSMF(c.data)
			if err == nil {
				t.Errorf("ParseSMF(%q) = nil error, want an error", c.name)
			}
		})
	}
}

// TestParseSMF_truncatedIsErrTruncated checks the typed sentinel is wrapped.
func TestParseSMF_truncatedIsErrTruncated(t *testing.T) {
	_, err := ParseSMF([]byte("MThd\x00\x00\x00\x06"))
	if !errors.Is(err, ErrTruncated) {
		t.Errorf("want errors.Is(err, ErrTruncated); got %v", err)
	}
}

// ---- test helpers ----

func firstNoteOn(evs []ParsedEvent) *ParsedEvent {
	for i := range evs {
		if evs[i].Kind == KindNoteOn {
			return &evs[i]
		}
	}
	return nil
}

// buildSMF assembles a minimal SMF from a format, declared track count, division,
// and raw MTrk bodies (each body must already contain its own end-of-track).
func buildSMF(format, ntracks, division int, bodies [][]byte) []byte {
	var out bytes.Buffer
	out.WriteString("MThd")
	out.Write(u32rd(6))
	out.Write(u16rd(format))
	out.Write(u16rd(ntracks))
	out.Write(u16rd(division))
	for _, b := range bodies {
		out.WriteString("MTrk")
		out.Write(u32rd(len(b)))
		out.Write(b)
	}
	return out.Bytes()
}

func u16rd(v int) []byte { return []byte{byte(v >> 8), byte(v)} }
func u32rd(v int) []byte {
	return []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
}

func buildSMFTruncatedTrackHeader() []byte {
	// Valid header declaring 1 track, but only 3 bytes where MTrk header begins.
	var out bytes.Buffer
	out.WriteString("MThd")
	out.Write(u32rd(6))
	out.Write(u16rd(0))
	out.Write(u16rd(1))
	out.Write(u16rd(480))
	out.Write([]byte{'M', 'T', 'r'}) // truncated chunk magic/header
	return out.Bytes()
}

func buildSMFBadTrackLength() []byte {
	// Chunk length claims 999 bytes but only a few follow.
	var out bytes.Buffer
	out.WriteString("MThd")
	out.Write(u32rd(6))
	out.Write(u16rd(0))
	out.Write(u16rd(1))
	out.Write(u16rd(480))
	out.WriteString("MTrk")
	out.Write(u32rd(999))
	out.Write([]byte{0x00, 0x90, 0x3C}) // truncated event, far short of 999
	return out.Bytes()
}

func buildSMFDanglingDelta() []byte {
	// Track body whose final delta is an unterminated VLQ.
	body := []byte{0x81} // continuation bit set, no terminator
	return buildSMF(0, 1, 480, [][]byte{body})
}

func buildSMFTruncatedChannel() []byte {
	body := []byte{0x00, 0x90, 0x3C} // note-on missing its velocity byte
	return buildSMF(0, 1, 480, [][]byte{body})
}

func buildSMFTruncatedMeta() []byte {
	body := []byte{0x00, 0xFF, 0x51, 0x03, 0x07, 0xA1} // tempo claims 3 bytes, only 2
	return buildSMF(0, 1, 480, [][]byte{body})
}
