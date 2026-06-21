// Package dawmodel is becky-daw's editable arrangement model: the heart of
// becky-canvas's DAW. It turns the write-once composition output (and any imported
// .mid, via internal/music's ParseSMF) into a mutable, VISUAL-FIRST surface —
// notes as editable blobs, a drum grid as steps x lanes, a per-track mixer — and a
// set of PURE, reversible edit operations. Every edit produces a NEW state (the
// becky immutability rule); when an edit overrides an auto-generated value it
// appends to a CORRECTIONS LOG, the substrate becky learns Jordan's preferences
// from.
//
// Invariants (becky house rules):
//   - Deterministic: same input -> same model; clips/notes are kept sorted so
//     serialization order never depends on map iteration.
//   - Degrade-never-crash: bad MIDI returns a wrapped error plus any partial model;
//     no index is unguarded; no edit op panics on an unknown ID.
//   - Round-trips through the existing byte-stable SMF writer: ParseSMF -> model ->
//     ToFile -> ParseSMF yields the same notes.
//
// This package is the model only; the becky-canvas GUI (immediate-mode DrawList)
// consumes it. No GUI, no audio, no cgo here.
package dawmodel

import (
	"sort"

	"becky-go/internal/music"
)

// KindMIDI and KindAudio classify a Track. Audio tracks carry waveform peaks for
// the visual surface; MIDI tracks carry editable notes (piano-roll / drum-grid).
const (
	KindMIDI  = "midi"
	KindAudio = "audio"
)

// Note is one editable note: a single object the piano roll moves/resizes/retunes
// as a unit. It is the visual "note blob" — its rectangle is (Start,Pitch) x
// (Dur,1 semitone). ID is stable across edits (selection, undo) and never reused.
type Note struct {
	ID    uint64 `json:"id"`
	Start int    `json:"start"` // absolute ticks
	Dur   int    `json:"dur"`   // ticks (>0)
	Pitch int    `json:"pitch"` // 0..127
	Vel   int    `json:"vel"`   // 1..127
	Ch    int    `json:"ch"`    // 0..15
}

// Peak is one visual waveform sample for an audio clip: min/max amplitude over a
// time window, an editable point on the clip's overview. Audio editing in becky is
// VISUAL-FIRST — Jordan fixes by eye against these peaks.
type Peak struct {
	Tick int     `json:"tick"`
	Min  float64 `json:"min"`
	Max  float64 `json:"max"`
}

// Clip is an editable region on a track. A MIDI clip holds Notes (piano roll /
// drum grid); an audio clip holds Peaks. Notes are kept sorted by (Start,Pitch)
// for cheap hit-testing and deterministic output.
type Clip struct {
	Name    string `json:"name"`
	Channel int    `json:"channel"`
	Program int    `json:"program"`        // GM program, -1 = percussion/none
	Offset  int    `json:"offset"`         // lane offset in ticks (clip start on the timeline)
	File    string `json:"file,omitempty"` // audio source path for an audio / bounced clip
	Notes   []Note `json:"notes,omitempty"`
	Peaks   []Peak `json:"peaks,omitempty"`
}

// Track is one lane of the arrangement: MIDI or audio, with a mixer strip.
type Track struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"` // KindMIDI | KindAudio
	Clips   []Clip `json:"clips"`
	Strip   Strip  `json:"strip"`
	Bounced bool   `json:"bounced,omitempty"` // MIDI+FX baked to the audio clip; heavy VSTs can be bypassed
}

// Arrangement is the whole editable session: tracks, transport, mixer buses, and
// the corrections log. It is treated as immutable — edit operations return a new
// Arrangement (a deep copy with the one change applied).
type Arrangement struct {
	Genre       string       `json:"genre"`
	Root        string       `json:"root"`
	Scale       string       `json:"scale"`
	BPM         int          `json:"bpm"`
	PPQ         int          `json:"ppq"`
	Num         int          `json:"num"` // time-signature numerator
	Den         int          `json:"den"` // time-signature denominator
	Tracks      []Track      `json:"tracks"`
	Buses       []Bus        `json:"buses"`
	Corrections []Correction `json:"corrections,omitempty"`
	NextID      uint64       `json:"nextId"` // monotonic note-ID allocator (never reused)
}

// New returns an empty Arrangement with sane transport defaults.
func New() *Arrangement {
	return &Arrangement{BPM: 120, PPQ: music.PPQ, Num: 4, Den: 4}
}

// clone returns a deep copy of the arrangement so edit ops never mutate the
// caller's state. Slices are copied; this is the immutability boundary.
func (a *Arrangement) clone() *Arrangement {
	out := *a
	out.Tracks = make([]Track, len(a.Tracks))
	for i, t := range a.Tracks {
		out.Tracks[i] = cloneTrack(t)
	}
	out.Buses = make([]Bus, len(a.Buses))
	for i, b := range a.Buses {
		b.Sidechain = append([]string(nil), b.Sidechain...)
		b.FX = append([]FXSlot(nil), b.FX...)
		out.Buses[i] = b
	}
	out.Corrections = append([]Correction(nil), a.Corrections...)
	return &out
}

func cloneTrack(t Track) Track {
	clips := make([]Clip, len(t.Clips))
	for i, c := range t.Clips {
		clips[i] = cloneClip(c)
	}
	t.Clips = clips
	t.Strip.FX = append([]FXSlot(nil), t.Strip.FX...) // deep-copy the insert chain
	return t
}

func cloneClip(c Clip) Clip {
	c.Notes = append([]Note(nil), c.Notes...)
	c.Peaks = append([]Peak(nil), c.Peaks...)
	return c
}

// allocID returns the next never-reused note ID.
func (a *Arrangement) allocID() uint64 {
	a.NextID++
	return a.NextID
}

// sortNotes keeps a clip's notes in deterministic (Start, Pitch, ID) order.
func sortNotes(notes []Note) {
	sort.SliceStable(notes, func(i, j int) bool {
		if notes[i].Start != notes[j].Start {
			return notes[i].Start < notes[j].Start
		}
		if notes[i].Pitch != notes[j].Pitch {
			return notes[i].Pitch < notes[j].Pitch
		}
		return notes[i].ID < notes[j].ID
	})
}

// findClip returns pointers to the track and clip identified by trackID/clipName,
// or (nil,nil) when not found. Used internally by edit ops on a cloned tree.
func (a *Arrangement) findClip(trackID, clipName string) (*Track, *Clip) {
	for ti := range a.Tracks {
		t := &a.Tracks[ti]
		if t.ID != trackID {
			continue
		}
		for ci := range t.Clips {
			if t.Clips[ci].Name == clipName {
				return t, &t.Clips[ci]
			}
		}
	}
	return nil, nil
}

// noteByID returns a pointer to the note with id in c, or nil.
func (c *Clip) noteByID(id uint64) *Note {
	for i := range c.Notes {
		if c.Notes[i].ID == id {
			return &c.Notes[i]
		}
	}
	return nil
}

// AddTrack returns a new arrangement with an empty track appended.
func (a *Arrangement) AddTrack(id, kind string) *Arrangement {
	out := a.clone()
	if kind != KindAudio {
		kind = KindMIDI
	}
	out.Tracks = append(out.Tracks, Track{ID: id, Kind: kind, Strip: defaultStrip(id)})
	return out
}

// TrackByID returns a copy of the named track and whether it was found.
func (a *Arrangement) TrackByID(id string) (Track, bool) {
	for _, t := range a.Tracks {
		if t.ID == id {
			return cloneTrack(t), true
		}
	}
	return Track{}, false
}

// NoteCount totals the notes across every MIDI clip (a quick model sanity probe).
func (a *Arrangement) NoteCount() int {
	n := 0
	for _, t := range a.Tracks {
		for _, c := range t.Clips {
			n += len(c.Notes)
		}
	}
	return n
}
