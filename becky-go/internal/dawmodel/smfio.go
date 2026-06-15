package dawmodel

import (
	"fmt"
	"sort"

	"becky-go/internal/music"
)

// pendingNote tracks an open note-on awaiting its matching note-off while parsing.
type pendingNote struct {
	start int
	vel   int
}

// noteKey identifies an open note by channel+pitch so overlapping note-ons on
// different pitches/channels pair correctly.
type noteKey struct {
	ch    int
	pitch int
}

// FromSMF parses Standard MIDI File bytes into an editable Arrangement: it reuses
// internal/music's ParseSMF (the byte-stable reader), pairs note-on/note-off into
// editable Notes, and pulls tempo/timesig/track-name meta. One Clip per parsed
// track (channel-split when a single track carries multiple channels, e.g.
// format 0). On malformed input it returns any partial arrangement plus a wrapped
// error — degrade, never crash.
func FromSMF(data []byte) (*Arrangement, error) {
	a := New()
	song, perr := music.ParseSMF(data)
	if song == nil {
		return a, fmt.Errorf("parse smf: %w", perr)
	}
	if song.Division > 0 && !song.SMPTE {
		a.PPQ = song.Division
	}
	for ti, pt := range song.Tracks {
		applyTrackMeta(a, pt)
		for _, c := range clipsFromTrack(a, ti, pt) {
			a.Tracks = append(a.Tracks, Track{
				ID: c.Name, Kind: KindMIDI, Clips: []Clip{c}, Strip: defaultStrip(c.Name),
			})
		}
	}
	if perr != nil {
		return a, fmt.Errorf("parse smf (partial): %w", perr)
	}
	return a, nil
}

// applyTrackMeta lifts the first tempo/timesig found into the arrangement transport.
func applyTrackMeta(a *Arrangement, pt music.ParsedTrack) {
	for _, e := range pt.Events {
		switch e.Kind {
		case music.KindTempo:
			if a.BPM == 120 && e.BPM > 0 {
				a.BPM = e.BPM
			}
		case music.KindTimeSig:
			if e.Numerator > 0 {
				a.Num, a.Den = e.Numerator, e.Denominator
			}
		}
	}
}

// clipsFromTrack converts one parsed track's events into editable clips, grouped
// by channel (so a format-0 multi-channel track splits into one clip per channel).
func clipsFromTrack(a *Arrangement, ti int, pt music.ParsedTrack) []Clip {
	name := trackName(pt, ti)
	program := programOf(pt)
	byCh := map[int][]Note{}
	open := map[noteKey][]pendingNote{}
	for _, e := range pt.Events {
		pairEvent(a, e, byCh, open)
	}
	closeDangling(a, byCh, open) // unmatched note-ons close with minimum length

	channels := make([]int, 0, len(byCh))
	for ch := range byCh {
		channels = append(channels, ch)
	}
	sort.Ints(channels)
	var out []Clip
	for _, ch := range channels {
		notes := byCh[ch]
		sortNotes(notes)
		cn := name
		if len(channels) > 1 {
			cn = fmt.Sprintf("%s.ch%d", name, ch)
		}
		out = append(out, Clip{Name: cn, Channel: ch, Program: program, Notes: notes})
	}
	if len(out) == 0 { // a meta-only track (tempo map) still becomes an empty clip
		out = append(out, Clip{Name: name, Channel: 0, Program: program})
	}
	return out
}

// pairEvent folds one parsed event into the per-channel note builder.
func pairEvent(a *Arrangement, e music.ParsedEvent, byCh map[int][]Note, open map[noteKey][]pendingNote) {
	switch e.Kind {
	case music.KindNoteOn:
		k := noteKey{e.Channel, e.Key}
		open[k] = append(open[k], pendingNote{start: e.Tick, vel: e.Velocity})
	case music.KindNoteOff:
		k := noteKey{e.Channel, e.Key}
		stack := open[k]
		if len(stack) == 0 {
			return // an unmatched note-off: ignore (degrade, don't crash)
		}
		p := stack[0]
		open[k] = stack[1:]
		dur := e.Tick - p.start
		if dur <= 0 {
			dur = 1
		}
		byCh[e.Channel] = append(byCh[e.Channel], Note{
			ID: a.allocID(), Start: p.start, Dur: dur, Pitch: e.Key, Vel: clampVel(p.vel), Ch: e.Channel,
		})
	}
}

// closeDangling closes any note-on that never saw a note-off (truncated/odd input):
// it is materialized as a minimum-length note so no data is silently dropped.
func closeDangling(a *Arrangement, byCh map[int][]Note, open map[noteKey][]pendingNote) {
	keys := make([]noteKey, 0, len(open))
	for k := range open {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].ch != keys[j].ch {
			return keys[i].ch < keys[j].ch
		}
		return keys[i].pitch < keys[j].pitch
	})
	for _, k := range keys {
		for _, p := range open[k] {
			byCh[k.ch] = append(byCh[k.ch], Note{
				ID: a.allocID(), Start: p.start, Dur: 1, Pitch: k.pitch, Vel: clampVel(p.vel), Ch: k.ch,
			})
		}
	}
}

// trackName returns the track-name meta or a positional fallback.
func trackName(pt music.ParsedTrack, idx int) string {
	for _, e := range pt.Events {
		if e.Kind == music.KindMeta && e.MetaType == 0x03 && len(e.Data) > 0 {
			return string(e.Data)
		}
	}
	return fmt.Sprintf("track%d", idx)
}

// programOf returns the first program-change found on a track, or -1 if none.
func programOf(pt music.ParsedTrack) int {
	for _, e := range pt.Events {
		if e.Kind == music.KindProgramChange {
			return e.Key
		}
	}
	return -1
}

// ToFile renders the whole arrangement back into a byte-stable music.File: track 0
// carries tempo/timesig, then one music.Track per clip, each note emitted through
// the existing Track.Note encoder so the SMF bytes stay identical to the writer's.
// Notes are placed at Offset+Start so lane offsets survive the round-trip.
func (a *Arrangement) ToFile() *music.File {
	f := music.NewFile(a.PPQ)
	meta := f.AddTrack()
	meta.Tempo(0, a.BPM)
	meta.TimeSig(0, a.Num, denOrFour(a.Den))
	for _, t := range a.Tracks {
		for _, c := range t.Clips {
			tr := f.AddTrack()
			tr.Name(0, c.Name)
			if c.Program >= 0 {
				tr.Program(0, c.Channel, c.Program)
			}
			notes := append([]Note(nil), c.Notes...)
			sortNotes(notes)
			for _, n := range notes {
				tr.Note(c.Offset+n.Start, n.Dur, n.Ch, n.Pitch, n.Vel)
			}
		}
	}
	return f
}

// ToSMF returns the arrangement as SMF bytes (ToFile then encode).
func (a *Arrangement) ToSMF() []byte { return a.ToFile().Bytes() }

func denOrFour(den int) int {
	if den <= 0 {
		return 4
	}
	return den
}

// clampVel keeps a velocity in the valid MIDI range (1..127); 0 maps to 1 so an
// editable note never becomes a silent ghost the writer would treat as a note-off.
func clampVel(v int) int {
	if v < 1 {
		return 1
	}
	if v > 127 {
		return 127
	}
	return v
}
