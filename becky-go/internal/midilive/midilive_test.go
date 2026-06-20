package midilive

import (
	"testing"
)

// ---------------------------------------------------------------------------
// PackShortMsg / NoteOn / NoteOff — pure-Go, OS-independent, testable everywhere.
// These are the bytes that go on the wire, so they get bit-exact assertions.
// ---------------------------------------------------------------------------

func TestPackShortMsg(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                 string
		status, data1, data2 byte
		want                 uint32
	}{
		// little-endian DWORD: status | data1<<8 | data2<<16
		{"noteOn C4 vel100", 0x90, 60, 100, 0x90 | 60<<8 | 100<<16},
		{"noteOff C4", 0x80, 60, 0, 0x80 | 60<<8},
		{"all zero", 0, 0, 0, 0},
		{"max bytes", 0xFF, 0x7F, 0x7F, 0xFF | 0x7F<<8 | 0x7F<<16},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := PackShortMsg(tt.status, tt.data1, tt.data2); got != tt.want {
				t.Fatalf("PackShortMsg(%#x,%d,%d) = %#x, want %#x", tt.status, tt.data1, tt.data2, got, tt.want)
			}
		})
	}
}

func TestNoteOnOffMsg(t *testing.T) {
	t.Parallel()
	// kick on channel 9, vel 100
	on := NoteOnMsg(DrumChannel, NoteKick, 100)
	if want := uint32(0x99 | 36<<8 | 100<<16); on != want {
		t.Fatalf("NoteOnMsg = %#x, want %#x", on, want)
	}
	off := NoteOffMsg(DrumChannel, NoteKick)
	if want := uint32(0x89 | 36<<8); off != want {
		t.Fatalf("NoteOffMsg = %#x, want %#x", off, want)
	}
	// status nibble for note-off must be < note-on so the schedule sort works.
	if on&0xF0 <= off&0xF0 {
		t.Fatalf("expected note-on status nibble (%#x) > note-off (%#x)", on&0xF0, off&0xF0)
	}
}

func TestChannelMasking(t *testing.T) {
	t.Parallel()
	// channel beyond 15 must wrap into the low nibble, never corrupt the status.
	got := NoteOnMsg(0x1F, 60, 100) // 0x1F & 0x0F == 0x0F
	if status := byte(got & 0xFF); status != 0x9F {
		t.Fatalf("channel mask: status = %#x, want 0x9F", status)
	}
}

// ---------------------------------------------------------------------------
// BuildDrumPattern — the deterministic, inspectable schedule.
// ---------------------------------------------------------------------------

func TestBuildDrumPatternDefaults(t *testing.T) {
	t.Parallel()
	sched := BuildDrumPattern(DrumPatternOptions{})
	if len(sched) == 0 {
		t.Fatal("expected a non-empty default pattern")
	}
	// One bar at 120 BPM: 2 kicks + 2 snares + 8 hats = 12 hits => 24 messages
	// (each hit is an on + an off). Hats land on even steps and two of them
	// coincide with kick/snare steps, but they are SEPARATE notes (different
	// keys), so all 12 hits are distinct.
	if want := 24; len(sched) != want {
		t.Fatalf("default pattern has %d messages, want %d", len(sched), want)
	}

	// Sorted ascending by offset.
	for i := 1; i < len(sched); i++ {
		if sched[i].OffsetMs < sched[i-1].OffsetMs {
			t.Fatalf("schedule not sorted at %d: %d < %d", i, sched[i].OffsetMs, sched[i-1].OffsetMs)
		}
	}

	// First message is at offset 0 and is a note-ON (kick or hat) — offs sort
	// before ons only at the SAME tick, and nothing is off at t=0.
	if sched[0].OffsetMs != 0 {
		t.Fatalf("first message offset = %d, want 0", sched[0].OffsetMs)
	}
	if status := byte(sched[0].Msg & 0xF0); status != statusNoteOn {
		t.Fatalf("first message status = %#x, want note-on %#x", status, statusNoteOn)
	}
}

func TestBuildDrumPatternDeterministic(t *testing.T) {
	t.Parallel()
	opts := DrumPatternOptions{BPM: 140, Bars: 2, Velocity: 110, GateMs: 50}
	a := BuildDrumPattern(opts)
	b := BuildDrumPattern(opts)
	if len(a) != len(b) {
		t.Fatalf("non-deterministic length: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("non-deterministic at %d: %+v vs %+v", i, a[i], b[i])
		}
	}
}

func TestBuildDrumPatternTiming(t *testing.T) {
	t.Parallel()
	// At 120 BPM a 16th note is (60000/120)/4 = 125 ms.
	sched := BuildDrumPattern(DrumPatternOptions{BPM: 120, Bars: 1, GateMs: 60})
	// Snare lands on step 4 => 4*125 = 500 ms. Find a snare-on at 500.
	wantSnareOn := NoteOnMsg(DrumChannel, NoteSnare, 100)
	found := false
	for _, m := range sched {
		if m.Msg == wantSnareOn && m.OffsetMs == 500 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected a snare note-on at 500 ms (beat 2); schedule=%+v", sched)
	}
}

func TestBuildDrumPatternBarsScale(t *testing.T) {
	t.Parallel()
	one := BuildDrumPattern(DrumPatternOptions{Bars: 1})
	two := BuildDrumPattern(DrumPatternOptions{Bars: 2})
	if len(two) != 2*len(one) {
		t.Fatalf("2 bars = %d msgs, expected 2x 1 bar (%d)", len(two), len(one))
	}
}

func TestTotalDurationMs(t *testing.T) {
	t.Parallel()
	if got := TotalDurationMs(nil); got != 0 {
		t.Fatalf("empty schedule duration = %d, want 0", got)
	}
	sched := []ScheduledMessage{
		{OffsetMs: 0}, {OffsetMs: 500}, {OffsetMs: 125},
	}
	if got := TotalDurationMs(sched); got != 500 {
		t.Fatalf("duration = %d, want 500", got)
	}
}

// ---------------------------------------------------------------------------
// OS-delegating API: assert the documented contract on whatever platform the
// test runs. On a zero/closed port the behaviour is identical on all platforms.
// ---------------------------------------------------------------------------

func TestSendOnClosedPort(t *testing.T) {
	t.Parallel()
	var p OutPort // zero value: not open
	if err := p.Send(0x99); err == nil {
		t.Fatal("Send on a zero/closed port must return an error")
	}
	// Close on a zero port is a safe no-op.
	if err := p.Close(); err != nil {
		t.Fatalf("Close on a zero port should be a no-op, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Virtual port — the bytes-unpacking helper and the closed-port contract are
// pure Go and behave identically on every platform, so they're tested here. The
// actual teVirtualMIDI port creation needs the Windows driver and is exercised by
// `becky-midi create-port` live, not in CI.
// ---------------------------------------------------------------------------

func TestMsgBytes(t *testing.T) {
	t.Parallel()
	// A note-on (kick, ch9, vel100) packs as status|data1<<8|data2<<16; MsgBytes
	// must unpack it back to the 3 raw MIDI bytes teVirtualMIDI wants, in order.
	on := NoteOnMsg(DrumChannel, NoteKick, 100)
	got := MsgBytes(on)
	want := []byte{0x99, 36, 100}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("MsgBytes(noteOn) = %v, want %v", got, want)
	}
	// Note-off (kick) -> {0x89, 36, 0}.
	off := MsgBytes(NoteOffMsg(DrumChannel, NoteKick))
	if len(off) != 3 || off[0] != 0x89 || off[1] != 36 || off[2] != 0 {
		t.Fatalf("MsgBytes(noteOff) = %v, want [0x89 36 0]", off)
	}
	// Round-trip: PackShortMsg of the bytes equals the original packed message.
	if rt := PackShortMsg(got[0], got[1], got[2]); rt != on {
		t.Fatalf("round-trip mismatch: PackShortMsg(MsgBytes(x)) = %#x, want %#x", rt, on)
	}
}

func TestVirtualPortClosedContract(t *testing.T) {
	t.Parallel()
	var v VirtualPort // zero value: not open
	if v.IsOpen() {
		t.Fatal("a zero VirtualPort must not report open")
	}
	if v.Name() != "" {
		t.Fatalf("a zero VirtualPort Name() = %q, want empty", v.Name())
	}
	if err := v.SendBytes([]byte{0x99, 36, 100}); err == nil {
		t.Fatal("SendBytes on a zero/closed virtual port must return an error")
	}
	// Close on a zero port is a safe no-op on every platform.
	if err := v.Close(); err != nil {
		t.Fatalf("Close on a zero virtual port should be a no-op, got %v", err)
	}
	// A nil receiver must also be safe (the CLI may defer Close before checking err).
	var vp *VirtualPort
	if vp.IsOpen() {
		t.Fatal("nil *VirtualPort must not report open")
	}
	if vp.Name() != "" {
		t.Fatal("nil *VirtualPort Name() must be empty")
	}
	if err := vp.Close(); err != nil {
		t.Fatalf("Close on a nil *VirtualPort should be a no-op, got %v", err)
	}
}
