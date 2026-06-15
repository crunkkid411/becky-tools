// Package music is becky's deterministic, theory-driven composition engine: given
// a genre profile + key + BPM + integer seed it emits multi-track MIDI (drums,
// bass, chords, melody, lead, counter-melody, sfx) as Standard MIDI Files the
// producer then tweaks. Same inputs => byte-identical output (becky's
// offline+deterministic invariant applied to music). This file is the SMF writer:
// a small, dependency-free Standard MIDI File (type 1) encoder.
package music

import (
	"bytes"
	"encoding/binary"
	"sort"
)

// Event is one MIDI/meta message at an absolute tick. Raw holds the status byte
// and payload (no delta time — that's computed at encode time from Tick).
type Event struct {
	Tick int    // absolute ticks from track start
	ord  int    // stable tie-breaker so same-tick order is deterministic
	Raw  []byte // status + data bytes (e.g. 0x90 key vel), or FF .. for meta
}

// Track is an ordered set of events on one MIDI track.
type Track struct {
	events []Event
	n      int
}

func (t *Track) add(tick int, raw []byte) {
	t.events = append(t.events, Event{Tick: tick, ord: t.n, Raw: raw})
	t.n++
}

// NoteOn / NoteOff on channel ch (0-15), key 0-127, velocity 0-127.
func (t *Track) NoteOn(tick, ch, key, vel int) {
	t.add(tick, []byte{byte(0x90 | ch&0x0F), byte(key & 0x7F), byte(vel & 0x7F)})
}
func (t *Track) NoteOff(tick, ch, key int) {
	t.add(tick, []byte{byte(0x80 | ch&0x0F), byte(key & 0x7F), 0})
}

// Note adds a note-on at start and note-off at start+dur (one call, two events).
func (t *Track) Note(start, dur, ch, key, vel int) {
	t.NoteOn(start, ch, key, vel)
	t.NoteOff(start+dur, ch, key)
}

// Program sets the instrument (GM program 0-127) on a channel.
func (t *Track) Program(tick, ch, prog int) {
	t.add(tick, []byte{byte(0xC0 | ch&0x0F), byte(prog & 0x7F)})
}

// Tempo writes a set-tempo meta from BPM (FF 51 03 microseconds-per-quarter).
func (t *Track) Tempo(tick, bpm int) {
	if bpm <= 0 {
		bpm = 120
	}
	upq := 60000000 / bpm
	t.add(tick, []byte{0xFF, 0x51, 0x03, byte(upq >> 16), byte(upq >> 8), byte(upq)})
}

// TimeSig writes a time-signature meta (FF 58 04 nn dd cc bb). den must be a
// power of two (2,4,8,16); it is stored as its log2.
func (t *Track) TimeSig(tick, num, den int) {
	dd := 0
	for d := den; d > 1; d >>= 1 {
		dd++
	}
	t.add(tick, []byte{0xFF, 0x58, 0x04, byte(num), byte(dd), 24, 8})
}

// Name writes a track-name meta (FF 03 len text).
func (t *Track) Name(tick int, name string) {
	raw := append([]byte{0xFF, 0x03}, vlq(len(name))...)
	raw = append(raw, []byte(name)...)
	t.add(tick, raw)
}

// File is a complete multi-track SMF. TPQ is ticks per quarter note.
type File struct {
	TPQ    int
	Tracks []*Track
}

// NewFile returns a File with the given ticks-per-quarter (480 is a good default).
func NewFile(tpq int) *File {
	if tpq <= 0 {
		tpq = 480
	}
	return &File{TPQ: tpq}
}

// AddTrack appends a fresh track and returns it.
func (f *File) AddTrack() *Track {
	t := &Track{}
	f.Tracks = append(f.Tracks, t)
	return t
}

// Bytes encodes the whole file to SMF type-1 bytes. Deterministic: events are
// sorted by (tick, insertion order), so identical inputs produce identical bytes.
func (f *File) Bytes() []byte {
	var out bytes.Buffer
	out.WriteString("MThd")
	writeU32(&out, 6)
	writeU16(&out, 1) // format 1 (multi-track)
	writeU16(&out, uint16(len(f.Tracks)))
	writeU16(&out, uint16(f.TPQ))
	for _, tr := range f.Tracks {
		out.Write(encodeTrack(tr))
	}
	return out.Bytes()
}

func encodeTrack(tr *Track) []byte {
	evs := make([]Event, len(tr.events))
	copy(evs, tr.events)
	sort.SliceStable(evs, func(i, j int) bool {
		if evs[i].Tick != evs[j].Tick {
			return evs[i].Tick < evs[j].Tick
		}
		return evs[i].ord < evs[j].ord
	})
	var body bytes.Buffer
	prev := 0
	for _, e := range evs {
		body.Write(vlq(e.Tick - prev))
		body.Write(e.Raw)
		prev = e.Tick
	}
	body.Write(vlq(0))
	body.Write([]byte{0xFF, 0x2F, 0x00}) // end of track

	var out bytes.Buffer
	out.WriteString("MTrk")
	writeU32(&out, uint32(body.Len()))
	out.Write(body.Bytes())
	return out.Bytes()
}

// vlq encodes a non-negative int as a MIDI variable-length quantity.
func vlq(n int) []byte {
	if n < 0 {
		n = 0
	}
	buf := []byte{byte(n & 0x7F)}
	n >>= 7
	for n > 0 {
		buf = append([]byte{byte(n&0x7F | 0x80)}, buf...)
		n >>= 7
	}
	return buf
}

func writeU16(b *bytes.Buffer, v uint16) {
	var tmp [2]byte
	binary.BigEndian.PutUint16(tmp[:], v)
	b.Write(tmp[:])
}

func writeU32(b *bytes.Buffer, v uint32) {
	var tmp [4]byte
	binary.BigEndian.PutUint32(tmp[:], v)
	b.Write(tmp[:])
}
