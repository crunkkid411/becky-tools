package hydrogen

import (
	"fmt"

	"github.com/hypebeast/go-osc/osc"
)

// OSC live control of a running Hydrogen.
//
// Hydrogen exposes an OSC server when launched with `-O <port>` (or `--osc-port`),
// e.g. `hydrogen -O 9000`. It listens on UDP and responds to a documented set of
// /Hydrogen/* messages (https://hydrogen-music.org/documentation/manual/manualen.html
// — "OSC interface"). This client speaks that protocol.
//
// Argument types follow Hydrogen's expectations: PLAY/STOP take a float fire-value
// (what the GUI sends); BPM takes a float; LOAD_DRUMKIT takes a string (kit name or
// absolute path); pattern select takes an int; NOTE_ON takes the instrument's MIDI
// note as an int. We send the forms the Hydrogen GUI itself emits.

// DefaultOSCPort is Hydrogen's conventional OSC port (matches `hydrogen -O 9000`).
const DefaultOSCPort = 9000

// OSC address constants for the verbs becky drives.
const (
	addrPlay                 = "/Hydrogen/PLAY"
	addrStop                 = "/Hydrogen/STOP"
	addrPlayStopToggle       = "/Hydrogen/PLAY_STOP_TOGGLE"
	addrBPM                  = "/Hydrogen/BPM"
	addrLoadDrumkit          = "/Hydrogen/LOAD_DRUMKIT"
	addrSelectAndPlayPattern = "/Hydrogen/SELECT_AND_PLAY_PATTERN"
	addrNoteOn               = "/Hydrogen/NOTE_ON"
	addrMasterVolume         = "/Hydrogen/MASTER_VOLUME_ABSOLUTE"
	addrOpenSong             = "/Hydrogen/OPEN_SONG"
)

// sender is the minimal interface this client needs from an OSC transport. The real
// *osc.Client satisfies it; tests inject a recorder to assert address+args without a
// network or a running Hydrogen (which CI does not have).
type sender interface {
	Send(packet osc.Packet) error
}

// OSCClient drives a running Hydrogen over OSC.
type OSCClient struct {
	host string
	port int
	tx   sender
}

// NewOSCClient connects (lazily — UDP has no handshake) to a Hydrogen OSC server at
// host:port. Pass DefaultOSCPort to match `hydrogen -O 9000`. An empty host defaults to
// 127.0.0.1.
func NewOSCClient(host string, port int) *OSCClient {
	if host == "" {
		host = "127.0.0.1"
	}
	if port <= 0 {
		port = DefaultOSCPort
	}
	return &OSCClient{host: host, port: port, tx: osc.NewClient(host, port)}
}

// newOSCClientWith builds a client over an injected sender (used by tests).
func newOSCClientWith(tx sender) *OSCClient {
	return &OSCClient{host: "test", port: 0, tx: tx}
}

// Addr returns the target the client sends to (for logging).
func (c *OSCClient) Addr() string { return fmt.Sprintf("%s:%d", c.host, c.port) }

// send marshals and transmits one message; errors are wrapped with the address.
func (c *OSCClient) send(msg *osc.Message) error {
	if err := c.tx.Send(msg); err != nil {
		return fmt.Errorf("hydrogen osc: send %s: %w", msg.Address, err)
	}
	return nil
}

// Play starts transport (sends /Hydrogen/PLAY with the GUI's 1.0 fire value).
func (c *OSCClient) Play() error { return c.send(osc.NewMessage(addrPlay, float32(1))) }

// Stop stops transport (/Hydrogen/STOP).
func (c *OSCClient) Stop() error { return c.send(osc.NewMessage(addrStop, float32(1))) }

// PlayStopToggle toggles transport (/Hydrogen/PLAY_STOP_TOGGLE).
func (c *OSCClient) PlayStopToggle() error {
	return c.send(osc.NewMessage(addrPlayStopToggle, float32(1)))
}

// SetBPM sets the tempo (/Hydrogen/BPM, float). Values are clamped to Hydrogen's
// accepted 30..500 range rather than rejected.
func (c *OSCClient) SetBPM(bpm float64) error {
	if bpm < 30 {
		bpm = 30
	}
	if bpm > 500 {
		bpm = 500
	}
	return c.send(osc.NewMessage(addrBPM, float32(bpm)))
}

// LoadDrumkit loads a kit by name (an installed kit) or absolute path
// (/Hydrogen/LOAD_DRUMKIT, string).
func (c *OSCClient) LoadDrumkit(nameOrPath string) error {
	return c.send(osc.NewMessage(addrLoadDrumkit, nameOrPath))
}

// SelectAndPlayPattern selects pattern at the 0-based index and plays it
// (/Hydrogen/SELECT_AND_PLAY_PATTERN, int).
func (c *OSCClient) SelectAndPlayPattern(index int) error {
	if index < 0 {
		index = 0
	}
	return c.send(osc.NewMessage(addrSelectAndPlayPattern, int32(index)))
}

// NoteOn triggers a one-shot note (/Hydrogen/NOTE_ON, int = MIDI note). Hydrogen maps
// the MIDI note to the instrument whose midiOutNote matches. Velocity is sent as a
// second int (0..127) when > 0; Hydrogen accepts the single-arg form too.
func (c *OSCClient) NoteOn(midiNote, velocity int) error {
	midiNote = clampMIDI(midiNote)
	if velocity <= 0 {
		return c.send(osc.NewMessage(addrNoteOn, int32(midiNote)))
	}
	if velocity > 127 {
		velocity = 127
	}
	return c.send(osc.NewMessage(addrNoteOn, int32(midiNote), int32(velocity)))
}

// MasterVolume sets master volume 0..1 (/Hydrogen/MASTER_VOLUME_ABSOLUTE, float).
func (c *OSCClient) MasterVolume(v float64) error {
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	return c.send(osc.NewMessage(addrMasterVolume, float32(v)))
}

// OpenSong tells Hydrogen to open a song file (/Hydrogen/OPEN_SONG, string=path).
func (c *OSCClient) OpenSong(path string) error {
	return c.send(osc.NewMessage(addrOpenSong, path))
}

func clampMIDI(n int) int {
	if n < 0 {
		return 0
	}
	if n > 127 {
		return 127
	}
	return n
}
