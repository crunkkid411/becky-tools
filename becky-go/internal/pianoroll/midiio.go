package pianoroll

// midiio.go is the .mid bridge for the clip model. It REUSES internal/music's
// Standard MIDI File writer (music.File / Track.Note) and reader (music.ParseSMF)
// — the SMF byte codec is NOT reinvented here. Export writes a one-track type-1 SMF
// from a Clip; import reads any SMF and pairs note-on/note-off events back into a
// Clip's value notes (mirroring internal/dawmodel.smfio's pairing, but yielding the
// standalone pianoroll.Note). Round-trip is exact for the note fields this model
// holds: pitch, start, length, velocity, channel.

import (
	"fmt"

	"becky-go/internal/music"
)

// Default transport written into an exported .mid when the caller does not specify
// one. These are metadata only — they do not affect note ticks — but a DAW needs a
// tempo/time-signature to interpret the file.
const (
	defaultBPM = 120
	defaultNum = 4
	defaultDen = 4
)

// ExportOpts carries the transport metadata stamped into the exported .mid. A zero
// value means "use the defaults" (120 BPM, 4/4). Notes keep their own ticks; PPQ
// comes from the clip.
type ExportOpts struct {
	BPM int // tempo in BPM (0 => defaultBPM)
	Num int // time-signature numerator (0 => defaultNum)
	Den int // time-signature denominator, a power of two (0 => defaultDen)
}

func (o ExportOpts) resolve() ExportOpts {
	if o.BPM <= 0 {
		o.BPM = defaultBPM
	}
	if o.Num <= 0 {
		o.Num = defaultNum
	}
	if o.Den <= 0 {
		o.Den = defaultDen
	}
	return o
}

// ToFile renders the clip into a music.File (one track) using the shared SMF
// writer, so the produced bytes are byte-stable and identical to the rest of
// becky-canvas's MIDI output. The track carries a name, tempo, time-signature, and
// one (note-on, note-off) pair per note via Track.Note.
func (c *Clip) ToFile(opts ExportOpts) *music.File {
	o := opts.resolve()
	f := music.NewFile(c.PPQ)
	tr := f.AddTrack()
	if c.Name != "" {
		tr.Name(0, c.Name)
	}
	tr.Tempo(0, o.BPM)
	tr.TimeSig(0, o.Num, o.Den)
	// Notes are already kept sorted; the writer sorts again by (tick, insertion),
	// so emitting in slice order is deterministic.
	for _, n := range c.Notes {
		tr.Note(n.Start, n.Length, n.Channel, n.Pitch, n.Velocity)
	}
	return f
}

// MIDIBytes returns the clip as encoded .mid bytes (a convenience over ToFile).
func (c *Clip) MIDIBytes(opts ExportOpts) []byte {
	return c.ToFile(opts).Bytes()
}

// pending tracks an open note-on awaiting its note-off while parsing an SMF.
type pending struct {
	start int
	vel   int
}

// voiceKey identifies a sounding voice for note-on/off pairing: a (channel, pitch)
// can have several simultaneous note-ons (rare but legal), so pendings are a stack.
type voiceKey struct {
	ch, pitch int
}

// FromSMF builds a Clip from raw .mid bytes, collapsing every track's note events
// into one clip (the piano roll edits a single clip surface). It uses
// music.ParseSMF for the byte decode and pairs note-ons with note-offs (a note-on
// with velocity 0 is a note-off, per the MIDI spec, which music.ParseSMF already
// classifies as KindNoteOff). On malformed/truncated input it returns the partial
// clip decoded so far PLUS a wrapped error (degrade, never crash). The clip's PPQ
// is taken from the SMF division (DefaultPPQ when the file uses SMPTE timing).
func FromSMF(data []byte) (*Clip, error) {
	song, err := music.ParseSMF(data)
	clip := NewClip(divisionPPQ(song))
	if song != nil {
		clip.Notes = notesFromSong(song)
		sortNotes(clip.Notes)
		clip.growToFit()
	}
	if err != nil {
		return clip, fmt.Errorf("pianoroll: parsing MIDI: %w", err)
	}
	return clip, nil
}

// divisionPPQ returns the song's ticks-per-quarter, or DefaultPPQ when the song is
// nil or uses SMPTE timing (which this tick-based model does not represent).
func divisionPPQ(song *music.ParsedSong) int {
	if song == nil || song.SMPTE || song.Division <= 0 {
		return DefaultPPQ
	}
	return song.Division
}

// notesFromSong walks every track's events, pairing note-ons with their matching
// note-offs into clip notes. A still-open note at end of track is closed with a
// length of 1 tick rather than dropped, so a truncated/odd file still yields usable
// notes (degrade, never crash).
func notesFromSong(song *music.ParsedSong) []Note {
	var notes []Note
	for _, tr := range song.Tracks {
		open := map[voiceKey][]pending{}
		for _, e := range tr.Events {
			switch e.Kind {
			case music.KindNoteOn:
				k := voiceKey{e.Channel, e.Key}
				open[k] = append(open[k], pending{start: e.Tick, vel: e.Velocity})
			case music.KindNoteOff:
				k := voiceKey{e.Channel, e.Key}
				stack := open[k]
				if len(stack) == 0 {
					continue // a note-off with no matching note-on: ignore
				}
				p := stack[len(stack)-1]
				open[k] = stack[:len(stack)-1]
				notes = append(notes, noteFrom(k, p, e.Tick))
			}
		}
		// Close any dangling note-ons deterministically (sorted by voice).
		notes = append(notes, closeDangling(open)...)
	}
	return notes
}

// noteFrom builds a clip note from a paired (note-on, note-off), guaranteeing a
// length of at least 1 tick and a velocity of at least 1.
func noteFrom(k voiceKey, p pending, offTick int) Note {
	length := offTick - p.start
	if length < 1 {
		length = 1
	}
	vel := p.vel
	if vel < 1 {
		vel = 1
	}
	return Note{Pitch: k.pitch, Start: p.start, Length: length, Velocity: vel, Channel: k.ch}
}

// closeDangling turns note-ons that never saw a note-off into 1-tick notes, in a
// deterministic order (by channel, then pitch, then start) so output never depends
// on map iteration.
func closeDangling(open map[voiceKey][]pending) []Note {
	var out []Note
	for k, stack := range open {
		for _, p := range stack {
			out = append(out, noteFrom(k, p, p.start+1))
		}
	}
	sortNotes(out)
	return out
}
