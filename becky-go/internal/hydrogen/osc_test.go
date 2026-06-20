package hydrogen

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/hypebeast/go-osc/osc"
)

// recordingSender captures sent OSC messages instead of putting them on a socket, so
// tests can assert addresses + argument types without a running Hydrogen or a network.
type recordingSender struct {
	msgs []*osc.Message
}

func (r *recordingSender) Send(p osc.Packet) error {
	if m, ok := p.(*osc.Message); ok {
		r.msgs = append(r.msgs, m)
	}
	return nil
}

func (r *recordingSender) last() *osc.Message {
	if len(r.msgs) == 0 {
		return nil
	}
	return r.msgs[len(r.msgs)-1]
}

func TestOSC_Addresses(t *testing.T) {
	rec := &recordingSender{}
	c := newOSCClientWith(rec)

	if err := c.Play(); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if got := rec.last().Address; got != "/Hydrogen/PLAY" {
		t.Errorf("Play address = %q", got)
	}

	if err := c.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := rec.last().Address; got != "/Hydrogen/STOP" {
		t.Errorf("Stop address = %q", got)
	}

	if err := c.LoadDrumkit("GMRockKit"); err != nil {
		t.Fatalf("LoadDrumkit: %v", err)
	}
	m := rec.last()
	if m.Address != "/Hydrogen/LOAD_DRUMKIT" {
		t.Errorf("LoadDrumkit address = %q", m.Address)
	}
	if len(m.Arguments) != 1 || m.Arguments[0] != "GMRockKit" {
		t.Errorf("LoadDrumkit args = %v, want [GMRockKit]", m.Arguments)
	}

	if err := c.SelectAndPlayPattern(2); err != nil {
		t.Fatalf("SelectAndPlayPattern: %v", err)
	}
	m = rec.last()
	if m.Address != "/Hydrogen/SELECT_AND_PLAY_PATTERN" {
		t.Errorf("SelectAndPlayPattern address = %q", m.Address)
	}
	if len(m.Arguments) != 1 {
		t.Fatalf("pattern args = %v", m.Arguments)
	}
	if v, ok := m.Arguments[0].(int32); !ok || v != 2 {
		t.Errorf("pattern arg = %v (%T), want int32(2)", m.Arguments[0], m.Arguments[0])
	}
}

func TestOSC_BPMFloatAndClamp(t *testing.T) {
	rec := &recordingSender{}
	c := newOSCClientWith(rec)

	if err := c.SetBPM(128); err != nil {
		t.Fatalf("SetBPM: %v", err)
	}
	m := rec.last()
	if m.Address != "/Hydrogen/BPM" {
		t.Errorf("BPM address = %q", m.Address)
	}
	v, ok := m.Arguments[0].(float32)
	if !ok {
		t.Fatalf("BPM arg type = %T, want float32", m.Arguments[0])
	}
	if v != 128 {
		t.Errorf("BPM arg = %v, want 128", v)
	}

	// Clamp out-of-range high.
	_ = c.SetBPM(9999)
	if v := rec.last().Arguments[0].(float32); v != 500 {
		t.Errorf("clamped BPM = %v, want 500", v)
	}
	// Clamp out-of-range low.
	_ = c.SetBPM(1)
	if v := rec.last().Arguments[0].(float32); v != 30 {
		t.Errorf("clamped BPM = %v, want 30", v)
	}
}

func TestOSC_NoteOn(t *testing.T) {
	rec := &recordingSender{}
	c := newOSCClientWith(rec)

	// No velocity -> single int arg.
	if err := c.NoteOn(MIDIKick, 0); err != nil {
		t.Fatalf("NoteOn: %v", err)
	}
	m := rec.last()
	if m.Address != "/Hydrogen/NOTE_ON" {
		t.Errorf("NoteOn address = %q", m.Address)
	}
	if len(m.Arguments) != 1 {
		t.Fatalf("NoteOn args = %v, want 1 (note only)", m.Arguments)
	}
	if v, ok := m.Arguments[0].(int32); !ok || v != MIDIKick {
		t.Errorf("NoteOn note = %v, want %d", m.Arguments[0], MIDIKick)
	}

	// With velocity -> two int args.
	_ = c.NoteOn(MIDISnare, 100)
	m = rec.last()
	if len(m.Arguments) != 2 {
		t.Fatalf("NoteOn(vel) args = %v, want 2", m.Arguments)
	}
	if v := m.Arguments[1].(int32); v != 100 {
		t.Errorf("NoteOn velocity = %v, want 100", v)
	}

	// Out-of-range note clamps to 0..127.
	_ = c.NoteOn(999, 0)
	if v := rec.last().Arguments[0].(int32); v != 127 {
		t.Errorf("clamped note = %v, want 127", v)
	}
}

func TestNewOSCClient_Defaults(t *testing.T) {
	c := NewOSCClient("", 0)
	if c.Addr() != "127.0.0.1:9000" {
		t.Errorf("default Addr = %q, want 127.0.0.1:9000", c.Addr())
	}
	if DefaultOSCPort != 9000 {
		t.Errorf("DefaultOSCPort = %d, want 9000", DefaultOSCPort)
	}
}

// TestOSC_RealUDPRoundTrip proves the client puts well-formed OSC on a real UDP socket
// (the actual *osc.Client, not the injected recorder) by standing up a go-osc server on
// loopback and confirming each verb's address arrives. This is the wire-level proof that
// the same bytes would reach a running `hydrogen -O <port>`.
func TestOSC_RealUDPRoundTrip(t *testing.T) {
	got := make(chan string, 16)
	d := osc.NewStandardDispatcher()
	for _, a := range []string{addrPlay, addrStop, addrBPM, addrLoadDrumkit, addrSelectAndPlayPattern, addrNoteOn} {
		addr := a
		if err := d.AddMsgHandler(addr, func(msg *osc.Message) { got <- msg.Address }); err != nil {
			t.Fatalf("AddMsgHandler(%s): %v", addr, err)
		}
	}

	// Bind an ephemeral UDP port the OS picks for us, then read it back so the client
	// targets the same port (avoids a hardcoded-port collision on CI).
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot bind loopback UDP (sandboxed env?): %v", err)
	}
	port := pc.LocalAddr().(*net.UDPAddr).Port
	_ = pc.Close() // close so the osc.Server can bind the same port

	server := &osc.Server{Addr: fmt.Sprintf("127.0.0.1:%d", port), Dispatcher: d}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.ListenAndServe() }()
	defer server.CloseConnection()
	time.Sleep(200 * time.Millisecond)

	c := NewOSCClient("127.0.0.1", port)
	for _, fn := range []func() error{
		c.Play, func() error { return c.SetBPM(137) },
		func() error { return c.LoadDrumkit("becky-groove-kit") },
		func() error { return c.SelectAndPlayPattern(0) },
		func() error { return c.NoteOn(MIDIKick, 110) },
		c.Stop,
	} {
		if err := fn(); err != nil {
			t.Fatalf("send: %v", err)
		}
	}

	seen := map[string]bool{}
	deadline := time.After(3 * time.Second)
	for len(seen) < 6 {
		select {
		case addr := <-got:
			seen[addr] = true
		case <-deadline:
			t.Fatalf("only received %d/6 OSC verbs over UDP: %v", len(seen), seen)
		}
	}
	for _, want := range []string{addrPlay, addrStop, addrBPM, addrLoadDrumkit, addrSelectAndPlayPattern, addrNoteOn} {
		if !seen[want] {
			t.Errorf("did not receive %s over UDP", want)
		}
	}
}
