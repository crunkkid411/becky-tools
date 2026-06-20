// Package midilive sends LIVE MIDI messages to a Windows MIDI output port so an
// already-open hardware/software instrument — Maschine 2 standalone with its pads
// MIDI-mapped, a loopMIDI virtual port, or the built-in GS Wavetable synth — fires
// in real time. It is the live-output sibling of internal/music, which writes
// Standard MIDI Files to disk; this package instead streams short messages NOW.
//
// # Why no cgo
//
// The usual Go MIDI route (gitlab.com/gomidi/midi v2 + the rtmidi or portmidi
// driver) needs cgo and a C MIDI library on PATH. becky deliberately avoids that
// rabbit hole: on Windows the multimedia API winmm.dll already exposes
// midiOutGetNumDevs / midiOutGetDevCaps / midiOutOpen / midiOutShortMsg /
// midiOutClose, and those are reachable from pure Go via
// golang.org/x/sys/windows (a LazyDLL syscall, already an indirect dependency).
// No cgo, no extra C library — see midilive_windows.go.
//
// # becky invariants
//
//   - Offline + deterministic OUTPUT: the bytes we emit for a given pattern are
//     fixed (BuildDrumPattern is pure and unit-tested). The act of sending is a
//     local OS facility, never a network call.
//   - Degrade, never crash: on non-Windows platforms (and when no port is open)
//     the exported calls return ErrUnsupportedOS / a typed error, not a panic.
//     Callers should check the error and fall back (e.g. write an SMF to disk).
//   - Propose-then-apply friendly: BuildDrumPattern produces a fully-described,
//     inspectable []ScheduledMessage that a UI/agent can preview BEFORE playback
//     actually streams it to hardware.
package midilive

import (
	"errors"
	"sort"
)

// ErrUnsupportedOS is returned by ListPorts/Open on platforms without the
// Windows winmm MIDI API (Linux/macOS, where becky's CI runs). Callers should
// treat it as "live MIDI isn't available here" and degrade — e.g. fall back to
// writing a Standard MIDI File via internal/music.
var ErrUnsupportedOS = errors.New("midilive: live MIDI output is only supported on Windows (winmm)")

// ErrNoSuchPort is returned by OpenNamed when no output port matches the request.
var ErrNoSuchPort = errors.New("midilive: no MIDI output port matches the requested name")

// ErrPortClosed is returned by Send when called on a closed/zero port handle.
var ErrPortClosed = errors.New("midilive: MIDI output port is not open")

// Port describes one MIDI OUTPUT device as reported by the OS. Index is the
// winmm device id passed to midiOutOpen; Name is the user-visible port name
// (e.g. "loopMIDI Port", "Microsoft GS Wavetable Synth").
type Port struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
}

// ---------------------------------------------------------------------------
// Message packing — pure Go, OS-independent, the deterministic core.
// ---------------------------------------------------------------------------

// MIDI status nibbles and the General-MIDI percussion channel.
const (
	statusNoteOff = 0x80
	statusNoteOn  = 0x90

	// DrumChannel is GM channel 10 (zero-based 9) — the percussion channel.
	// Maschine and most drum instruments listen here for note-triggered pads.
	DrumChannel = 9
)

// General-MIDI percussion key numbers for the three core drum voices. These are
// the standard GM map values, so they trigger the right sound on any GM device
// and are the conventional defaults a producer maps Maschine pads to.
const (
	NoteKick  = 36 // C1  — Bass Drum 1 / Acoustic Bass Drum
	NoteSnare = 38 // D1  — Acoustic Snare
	NoteHat   = 42 // F#1 — Closed Hi-Hat
)

// PackShortMsg encodes a MIDI short message into the little-endian DWORD layout
// midiOutShortMsg expects: byte0 = status, byte1 = data1, byte2 = data2.
// Exported so it is directly unit-testable without any MIDI device.
func PackShortMsg(status, data1, data2 byte) uint32 {
	return uint32(status) | uint32(data1)<<8 | uint32(data2)<<16
}

// NoteOnMsg builds a packed note-on message for one note. ch is 0-15.
func NoteOnMsg(ch, key, vel byte) uint32 {
	return PackShortMsg(statusNoteOn|(ch&0x0F), key&0x7F, vel&0x7F)
}

// NoteOffMsg builds a packed note-off message for one note.
func NoteOffMsg(ch, key byte) uint32 {
	return PackShortMsg(statusNoteOff|(ch&0x0F), key&0x7F, 0)
}

// ---------------------------------------------------------------------------
// Drum pattern — a deterministic, inspectable schedule of timed messages.
// ---------------------------------------------------------------------------

// ScheduledMessage is one packed MIDI message to send at OffsetMs from the start
// of playback. The schedule is fully materialised before any byte is sent, so a
// caller can preview it (propose-then-apply) and so playback stays a dumb streamer.
type ScheduledMessage struct {
	OffsetMs int    `json:"offset_ms"` // when to send, ms from playback start
	Msg      uint32 `json:"msg"`       // packed midiOutShortMsg DWORD
	Label    string `json:"label"`     // human tag, e.g. "kick on", "hat off"
}

// DrumPatternOptions configures BuildDrumPattern. Zero values give a sensible
// one-bar 4/4 backbeat at 120 BPM with eighth-note hats.
type DrumPatternOptions struct {
	BPM      int  // beats per minute (default 120)
	Bars     int  // number of 4/4 bars to generate (default 1)
	Velocity byte // note-on velocity 1-127 (default 100)
	GateMs   int  // how long each hit is held before its note-off (default 60)
	Channel  byte // MIDI channel 0-15 (default DrumChannel = 9)
	KickKey  byte // override kick key (default NoteKick)
	SnareKey byte // override snare key (default NoteSnare)
	HatKey   byte // override hat key (default NoteHat)
}

// stepsPerBar is the 16th-note step grid resolution for a 4/4 bar.
const stepsPerBar = 16

func (o DrumPatternOptions) withDefaults() DrumPatternOptions {
	if o.BPM <= 0 {
		o.BPM = 120
	}
	if o.Bars <= 0 {
		o.Bars = 1
	}
	if o.Velocity == 0 {
		o.Velocity = 100
	}
	if o.GateMs <= 0 {
		o.GateMs = 60
	}
	// A drum pattern almost always lives on channel 9; the zero value maps there
	// for convenience. A caller wanting a different channel sets it explicitly.
	if o.Channel == 0 {
		o.Channel = DrumChannel
	}
	if o.KickKey == 0 {
		o.KickKey = NoteKick
	}
	if o.SnareKey == 0 {
		o.SnareKey = NoteSnare
	}
	if o.HatKey == 0 {
		o.HatKey = NoteHat
	}
	return o
}

// BuildDrumPattern returns the deterministic schedule for a classic backbeat:
//   - kick on steps 0 and 8 (beats 1 and 3),
//   - snare on steps 4 and 12 (beats 2 and 4),
//   - closed hi-hat on every even step (the 8 eighth-note positions).
//
// Same options in => identical schedule out. The result is sorted by OffsetMs
// (note-offs before note-ons at the same instant, so a re-hit re-articulates).
func BuildDrumPattern(opts DrumPatternOptions) []ScheduledMessage {
	o := opts.withDefaults()
	msPerStep := (60000 / o.BPM) / 4 // a 16th note = quarter/4
	var out []ScheduledMessage

	hit := func(step int, key byte, label string) {
		on := step * msPerStep
		out = append(out,
			ScheduledMessage{OffsetMs: on, Msg: NoteOnMsg(o.Channel, key, o.Velocity), Label: label + " on"},
			ScheduledMessage{OffsetMs: on + o.GateMs, Msg: NoteOffMsg(o.Channel, key), Label: label + " off"},
		)
	}

	for bar := 0; bar < o.Bars; bar++ {
		base := bar * stepsPerBar
		hit(base+0, o.KickKey, "kick")
		hit(base+8, o.KickKey, "kick")
		hit(base+4, o.SnareKey, "snare")
		hit(base+12, o.SnareKey, "snare")
		for step := 0; step < stepsPerBar; step += 2 { // eighth-note hats
			hit(base+step, o.HatKey, "hat")
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].OffsetMs != out[j].OffsetMs {
			return out[i].OffsetMs < out[j].OffsetMs
		}
		// note-off (status 0x8n) sorts before note-on (0x9n) at the same tick.
		return out[i].Msg&0xF0 < out[j].Msg&0xF0
	})
	return out
}

// TotalDurationMs returns the time of the last scheduled message (so a caller
// knows how long to keep the port open). Returns 0 for an empty schedule.
func TotalDurationMs(sched []ScheduledMessage) int {
	max := 0
	for _, m := range sched {
		if m.OffsetMs > max {
			max = m.OffsetMs
		}
	}
	return max
}

// ---------------------------------------------------------------------------
// Public OS-delegating API. The real work lives in the build-tagged files.
// ---------------------------------------------------------------------------

// ListPorts returns the MIDI output ports the OS reports. On non-Windows it
// returns (nil, ErrUnsupportedOS).
func ListPorts() ([]Port, error) { return listPorts() }

// OutPort is an open MIDI output handle. Send streams one packed message; Close
// releases the OS handle. Construct it with Open or OpenNamed.
type OutPort struct {
	port   Port
	handle uintptr // OS handle (HMIDIOUT on Windows); 0 means closed
	open   bool
}

// Port returns the descriptor of the opened device.
func (p *OutPort) Port() Port { return p.port }

// Open opens the output port with the given device index.
func Open(index int) (*OutPort, error) { return openIndex(index) }

// OpenNamed opens the first output port whose Name contains substr
// (case-insensitive). Useful so a user can say "loopMIDI" or "Maschine" without
// knowing the numeric index. Returns ErrNoSuchPort if none match.
func OpenNamed(substr string) (*OutPort, error) { return openNamed(substr) }

// Send streams one packed short message to the port immediately.
func (p *OutPort) Send(msg uint32) error { return p.send(msg) }

// Close releases the OS port handle. Safe to call more than once.
func (p *OutPort) Close() error { return p.close() }
