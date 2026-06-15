// This file is the SMF reader: a small, dependency-free Standard MIDI File parser
// that complements the writer in smf.go. It turns SMF bytes back into an editable
// model so becky-compose's output (and any external .mid) can be parsed, edited,
// and re-emitted — the foundation for the DAW. Mirrors the writer's note/track/
// event model so reader+writer round-trip exactly.
//
// Invariants (becky house rules): deterministic (same bytes -> same structure, no
// map iteration in output), and degrade-never-crash (malformed/truncated input
// returns an error plus any partial result, never a panic — every slice index is
// guarded). Errors are wrapped with context via fmt.Errorf("...: %w", err).
package music

import (
	"errors"
	"fmt"
)

// MIDI status / meta constants used by the reader.
const (
	statusNoteOff       = 0x80
	statusNoteOn        = 0x90
	statusPolyAfter     = 0xA0
	statusControl       = 0xB0
	statusProgram       = 0xC0
	statusChanAfter     = 0xD0
	statusPitchBend     = 0xE0
	statusSysExStart    = 0xF0
	statusSysExEnd      = 0xF7
	statusMeta          = 0xFF
	metaTempo           = 0x51
	metaTimeSig         = 0x58
	metaEndOfTrack      = 0x2F
	maxVLQBytes         = 4 // an SMF VLQ is at most 4 bytes (28 significant bits)
	defaultMicrosPerQtr = 500000
)

// ErrTruncated is returned (wrapped) whenever the byte slice ends before a field
// the format requires. Callers can errors.Is against it.
var ErrTruncated = errors.New("smf: unexpected end of data")

// EventKind classifies a parsed event so editors can switch on intent rather than
// re-decoding the status byte.
type EventKind int

const (
	KindUnknown EventKind = iota
	KindNoteOn            // a true note-on (velocity > 0)
	KindNoteOff           // note-off, incl. a note-on with velocity 0
	KindProgramChange
	KindController
	KindPitchBend
	KindChannelAftertouch
	KindPolyAftertouch
	KindTempo
	KindTimeSig
	KindEndOfTrack
	KindMeta  // any other meta event (text, marker, etc.)
	KindSysEx // F0/F7 system-exclusive
)

// ParsedEvent is one decoded MIDI/meta message at an absolute tick. It keeps the
// raw status+payload (like the writer's Event) plus decoded convenience fields.
type ParsedEvent struct {
	Tick    int       // absolute ticks from track start
	Delta   int       // delta ticks from the previous event (as stored)
	Kind    EventKind // decoded classification
	Status  byte      // running-status-resolved status byte
	Channel int       // 0-15 for channel messages, -1 otherwise

	// Channel-message fields (meaning depends on Kind; -1 when not applicable).
	Key      int // note number / controller number / program number
	Velocity int // note velocity / controller value
	Bend     int // 14-bit pitch-bend value (0-16383, center 8192)

	// Meta fields.
	MetaType    byte   // meta type byte (valid when Kind is a meta kind)
	MicrosPerQ  int    // microseconds per quarter note (KindTempo)
	BPM         int    // tempo as BPM (KindTempo), derived from MicrosPerQ
	Numerator   int    // time-signature numerator (KindTimeSig)
	Denominator int    // time-signature denominator, e.g. 4 (KindTimeSig)
	Data        []byte // meta/sysex payload (text, sysex body, unknown meta)
}

// ParsedTrack is one MTrk chunk decoded into absolute-tick events, in stored order.
type ParsedTrack struct {
	Events []ParsedEvent
}

// ParsedSong is a complete SMF decoded into an editable model.
type ParsedSong struct {
	Format   int  // 0, 1 or 2
	Division int  // ticks per quarter note (PPQ) when positive
	SMPTE    bool // true when Division encodes SMPTE timing (top bit set)
	Tracks   []ParsedTrack
}

// ParseSMF parses a complete SMF byte slice into a ParsedSong. On malformed or
// truncated input it returns a wrapped error and any partial song decoded so far
// (never nil once the MThd header was read), so callers can degrade gracefully.
func ParseSMF(data []byte) (*ParsedSong, error) {
	hdr, pos, err := parseHeader(data)
	if err != nil {
		return nil, err
	}
	for i := 0; i < hdr.ntracks; i++ {
		tr, next, terr := parseTrack(data, pos)
		if tr != nil {
			hdr.out.Tracks = append(hdr.out.Tracks, *tr)
		}
		if terr != nil {
			return &hdr.out, fmt.Errorf("track %d: %w", i, terr)
		}
		pos = next
	}
	return &hdr.out, nil
}

// headerResult bundles the decoded header with the declared track count.
type headerResult struct {
	out     ParsedSong
	ntracks int
}

// parseHeader reads and validates the MThd chunk, returning the next read offset.
func parseHeader(data []byte) (*headerResult, int, error) {
	if len(data) < 14 {
		return nil, 0, fmt.Errorf("reading MThd: %w", ErrTruncated)
	}
	if string(data[0:4]) != "MThd" {
		return nil, 0, fmt.Errorf("smf: bad magic %q, want MThd", string(data[0:4]))
	}
	hlen := beU32(data[4:8])
	if hlen < 6 {
		return nil, 0, fmt.Errorf("smf: MThd length %d too small", hlen)
	}
	format := beU16(data[8:10])
	if format > 2 {
		return nil, 0, fmt.Errorf("smf: unsupported format %d", format)
	}
	res := &headerResult{ntracks: beU16(data[10:12])}
	res.out.Format = format
	division := beU16(data[12:14])
	res.out.Division = division
	res.out.SMPTE = division&0x8000 != 0
	// Header may declare a length > 6; skip any extra header bytes.
	pos := 8 + hlen
	if pos > len(data) {
		return res, len(data), fmt.Errorf("MThd declares %d bytes: %w", hlen, ErrTruncated)
	}
	return res, pos, nil
}

// parseTrack decodes one MTrk chunk starting at pos. It returns the decoded track
// (possibly partial), the offset just past the chunk, and any error.
func parseTrack(data []byte, pos int) (*ParsedTrack, int, error) {
	if pos+8 > len(data) {
		return nil, len(data), fmt.Errorf("reading MTrk header: %w", ErrTruncated)
	}
	if string(data[pos:pos+4]) != "MTrk" {
		return nil, pos, fmt.Errorf("smf: bad chunk magic %q, want MTrk", string(data[pos:pos+4]))
	}
	length := beU32(data[pos+4 : pos+8])
	bodyStart := pos + 8
	end := bodyStart + length
	if end > len(data) {
		// Bad/oversized chunk length. Decode what we can up to the buffer end.
		tr, _ := decodeEvents(data, bodyStart, len(data))
		return tr, len(data), fmt.Errorf("MTrk length %d overruns buffer: %w", length, ErrTruncated)
	}
	tr, err := decodeEvents(data, bodyStart, end)
	if err != nil {
		return tr, end, err
	}
	return tr, end, nil
}

// decodeEvents walks the event stream of one track body in [start,end), handling
// running status and VLQ deltas. Returns a partial track on error.
func decodeEvents(data []byte, start, end int) (*ParsedTrack, error) {
	tr := &ParsedTrack{}
	pos := start
	abs := 0
	var running byte
	for pos < end {
		delta, n, ok := decodeVLQ(data[pos:end])
		if !ok {
			return tr, fmt.Errorf("delta time at offset %d: %w", pos, ErrTruncated)
		}
		pos += n
		abs += delta
		ev, next, err := decodeEvent(data, pos, end, delta, abs, &running)
		if err != nil {
			return tr, err
		}
		tr.Events = append(tr.Events, ev)
		pos = next
		if ev.Kind == KindEndOfTrack {
			return tr, nil
		}
	}
	return tr, nil
}

// decodeEvent decodes a single event whose data begins at pos. running carries the
// last channel status for running-status continuation and is updated in place.
func decodeEvent(data []byte, pos, end, delta, abs int, running *byte) (ParsedEvent, int, error) {
	if pos >= end {
		return ParsedEvent{}, pos, fmt.Errorf("event status at offset %d: %w", pos, ErrTruncated)
	}
	ev := ParsedEvent{Tick: abs, Delta: delta, Channel: -1, Key: -1, Velocity: -1, Bend: -1}
	c := data[pos]
	switch {
	case c == statusMeta:
		return decodeMeta(data, pos, end, ev)
	case c == statusSysExStart || c == statusSysExEnd:
		return decodeSysEx(data, pos, end, ev)
	case c >= 0x80:
		*running = c
		return decodeChannel(data, pos+1, end, ev, c)
	default: // running status: reuse last channel status, c is the first data byte
		if *running < 0x80 {
			return ev, pos, fmt.Errorf("running status with no prior status at offset %d", pos)
		}
		return decodeChannel(data, pos, end, ev, *running)
	}
}

// decodeChannel decodes a channel voice message; dataPos points at the first data
// byte (status already consumed by the caller for non-running events).
func decodeChannel(data []byte, dataPos, end int, ev ParsedEvent, status byte) (ParsedEvent, int, error) {
	ev.Status = status
	ev.Channel = int(status & 0x0F)
	want := channelDataLen(status)
	if dataPos+want > end {
		return ev, dataPos, fmt.Errorf("channel message %#x: %w", status, ErrTruncated)
	}
	d0 := int(data[dataPos])
	d1 := 0
	if want == 2 {
		d1 = int(data[dataPos+1])
	}
	fillChannel(&ev, status, d0, d1)
	return ev, dataPos + want, nil
}

// fillChannel populates the decoded fields of a channel event from its data bytes.
func fillChannel(ev *ParsedEvent, status byte, d0, d1 int) {
	switch status & 0xF0 {
	case statusNoteOn:
		ev.Key, ev.Velocity = d0, d1
		if d1 == 0 { // note-on velocity 0 is a note-off
			ev.Kind = KindNoteOff
		} else {
			ev.Kind = KindNoteOn
		}
	case statusNoteOff:
		ev.Key, ev.Velocity, ev.Kind = d0, d1, KindNoteOff
	case statusControl:
		ev.Key, ev.Velocity, ev.Kind = d0, d1, KindController
	case statusProgram:
		ev.Key, ev.Kind = d0, KindProgramChange
	case statusChanAfter:
		ev.Velocity, ev.Kind = d0, KindChannelAftertouch
	case statusPolyAfter:
		ev.Key, ev.Velocity, ev.Kind = d0, d1, KindPolyAftertouch
	case statusPitchBend:
		ev.Bend, ev.Kind = d0|(d1<<7), KindPitchBend
	}
}

// decodeMeta decodes an FF meta event whose 0xFF byte is at pos.
func decodeMeta(data []byte, pos, end int, ev ParsedEvent) (ParsedEvent, int, error) {
	ev.Status = statusMeta
	if pos+2 > end {
		return ev, pos, fmt.Errorf("meta type: %w", ErrTruncated)
	}
	ev.MetaType = data[pos+1]
	ln, n, ok := decodeVLQ(data[pos+2 : end])
	if !ok {
		return ev, pos, fmt.Errorf("meta length: %w", ErrTruncated)
	}
	dataStart := pos + 2 + n
	if dataStart+ln > end {
		return ev, pos, fmt.Errorf("meta payload (type %#x, len %d): %w", ev.MetaType, ln, ErrTruncated)
	}
	ev.Data = append([]byte(nil), data[dataStart:dataStart+ln]...)
	classifyMeta(&ev, ev.Data)
	return ev, dataStart + ln, nil
}

// classifyMeta decodes well-known meta events; everything else is KindMeta.
func classifyMeta(ev *ParsedEvent, payload []byte) {
	switch ev.MetaType {
	case metaEndOfTrack:
		ev.Kind = KindEndOfTrack
	case metaTempo:
		ev.Kind = KindTempo
		if len(payload) == 3 {
			ev.MicrosPerQ = int(payload[0])<<16 | int(payload[1])<<8 | int(payload[2])
		} else {
			ev.MicrosPerQ = defaultMicrosPerQtr
		}
		if ev.MicrosPerQ > 0 {
			ev.BPM = 60000000 / ev.MicrosPerQ
		}
	case metaTimeSig:
		ev.Kind = KindTimeSig
		if len(payload) >= 2 {
			ev.Numerator = int(payload[0])
			ev.Denominator = 1 << payload[1] // stored as log2 of the denominator
		}
	default:
		ev.Kind = KindMeta
	}
}

// decodeSysEx stores an F0/F7 system-exclusive event (its body is length-prefixed
// by a VLQ in SMF). We keep the payload bytes; we never interpret them.
func decodeSysEx(data []byte, pos, end int, ev ParsedEvent) (ParsedEvent, int, error) {
	ev.Status = data[pos]
	ev.Kind = KindSysEx
	ln, n, ok := decodeVLQ(data[pos+1 : end])
	if !ok {
		return ev, pos, fmt.Errorf("sysex length: %w", ErrTruncated)
	}
	dataStart := pos + 1 + n
	if dataStart+ln > end {
		return ev, pos, fmt.Errorf("sysex payload (len %d): %w", ln, ErrTruncated)
	}
	ev.Data = append([]byte(nil), data[dataStart:dataStart+ln]...)
	return ev, dataStart + ln, nil
}

// channelDataLen returns how many data bytes a channel message of this status
// carries (1 for program-change and channel-aftertouch, 2 for the rest).
func channelDataLen(status byte) int {
	switch status & 0xF0 {
	case statusProgram, statusChanAfter:
		return 1
	default:
		return 2
	}
}

// decodeVLQ decodes a MIDI variable-length quantity from the front of b. It returns
// the value, the number of bytes consumed, and ok=false on truncation (no
// terminating byte within maxVLQBytes or the slice). It never panics.
func decodeVLQ(b []byte) (val, n int, ok bool) {
	for i := 0; i < len(b) && i < maxVLQBytes; i++ {
		val = val<<7 | int(b[i]&0x7F)
		n++
		if b[i]&0x80 == 0 {
			return val, n, true
		}
	}
	return 0, 0, false
}

func beU16(b []byte) int { return int(b[0])<<8 | int(b[1]) }

func beU32(b []byte) int {
	return int(b[0])<<24 | int(b[1])<<16 | int(b[2])<<8 | int(b[3])
}
