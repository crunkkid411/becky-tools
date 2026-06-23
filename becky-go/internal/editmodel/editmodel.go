// Package editmodel is the SHARED, living editor state for becky-edit (the
// forensic NLE; SPEC-BECKY-NLE.md). It is the single source of truth that BOTH
// the human (editing in the Shotcut host) and the built-in model (the embedded
// Gemma-4 agent, via internal/ctlagent + internal/edittools) read and mutate.
//
// The design goal Jordan set: the model and the program "share state — regardless
// of if I manually make edits or if it does things". So every mutation — whether
// it came from a human event mirrored in from the host, or from a tool the model
// called — produces a NEW Project with Rev bumped (copy-on-write; CLAUDE.md
// coding-style immutability). The bridge (cmd/becky-edit) holds the current
// Project and swaps it on each applied change; the model always sees the latest.
//
// Two views of the state, on purpose:
//   - the FULL Project (this file): tracks, clips, positions, in/out, effects +
//     params, playhead, selection, markers, overlay — everything the program needs.
//   - the COMPACT Digest (digest.go): one terse line per clip + the cursors. This
//     is what goes into the model's context each turn — "minimal enough that it's
//     not overloaded, but it can't be ignorant of what it's doing" (Jordan). The
//     same minimal-but-sufficient discipline video-db/Director uses for its LLM.
//
// Pure Go, no I/O, no models, no ffmpeg — fully table-testable. Times are seconds
// (float64). Sources are ABSOLUTE paths and are only ever READ (the forensic
// originals-are-immutable rule lives in the bridge/render layer, never here).
package editmodel

import (
	"fmt"

	"becky-go/internal/edl"
)

// Kind classifies a track. Video and audio tracks behave the same structurally;
// the distinction drives routing, the mixer, and which effects are legal.
type Kind string

const (
	KindVideo Kind = "video"
	KindAudio Kind = "audio"
)

// Effect is one filter applied to a clip, with its named float parameters. The
// Name is from the host's effect vocabulary (e.g. "brightness", "fadeIn",
// "volume"); edittools validates Name + Params against the allowlist before any
// effect lands here, so the model can never invent an arbitrary filter.
type Effect struct {
	ID     string             `json:"id"`     // stable within the clip, e.g. "c2fx1"
	Name   string             `json:"name"`   // allowlisted effect name
	Params map[string]float64 `json:"params"` // named scalar params (gain, opacity, seconds…)
}

// Clone returns a deep copy of the effect (its Params map is copied).
func (e Effect) Clone() Effect {
	cp := e
	if e.Params != nil {
		cp.Params = make(map[string]float64, len(e.Params))
		for k, v := range e.Params {
			cp.Params[k] = v
		}
	}
	return cp
}

// Clip is one [In,Out] span of a single source, placed at Pos on its track's
// timeline. In/Out are seconds into Source; Pos is seconds on the timeline. This
// mirrors edl.Clip (so we can render via internal/reel + internal/kdenlive) but
// adds the live-editor fields the host needs: Pos, Track membership, Effects.
type Clip struct {
	ID      string       `json:"id"`              // stable, e.g. "c1"
	Source  string       `json:"source"`          // ABSOLUTE path to the source video (read-only)
	In      float64      `json:"in"`              // seconds into source
	Out     float64      `json:"out"`             // seconds into source
	Pos     float64      `json:"pos"`             // seconds on the timeline (clip start)
	Label   string       `json:"label,omitempty"` // e.g. the quote text
	Effects []Effect     `json:"effects,omitempty"`
	Meta    edl.ClipMeta `json:"meta"`
}

// Dur is the clip's source span (Out-In), clamped to >= 0.
func (c Clip) Dur() float64 {
	d := c.Out - c.In
	if d < 0 {
		return 0
	}
	return d
}

// End is the clip's timeline end (Pos + Dur).
func (c Clip) End() float64 { return c.Pos + c.Dur() }

// Clone returns a deep copy of the clip (Effects slice + each Effect's params).
func (c Clip) Clone() Clip {
	cp := c
	if c.Effects != nil {
		cp.Effects = make([]Effect, len(c.Effects))
		for i, e := range c.Effects {
			cp.Effects[i] = e.Clone()
		}
	}
	return cp
}

// Track is one lane of the timeline holding clips in timeline order.
type Track struct {
	Index int     `json:"index"` // 0-based lane index (0 = topmost video / first audio)
	Name  string  `json:"name"`
	Kind  Kind    `json:"kind"`
	Mute  bool    `json:"mute,omitempty"`
	Gain  float64 `json:"gain,omitempty"` // audio track gain in dB (0 = unity)
	Clips []Clip  `json:"clips"`
}

// Clone returns a deep copy of the track and all its clips.
func (t Track) Clone() Track {
	cp := t
	cp.Clips = make([]Clip, len(t.Clips))
	for i, c := range t.Clips {
		cp.Clips[i] = c.Clone()
	}
	return cp
}

// Marker is a labelled point on the timeline (a forensic note / chapter).
type Marker struct {
	ID    string  `json:"id"`
	At    float64 `json:"at"` // seconds on the timeline
	Label string  `json:"label,omitempty"`
}

// Project is the whole living editor state. Rev is a monotonic revision that bumps
// on every mutation, so the bridge and the model can tell when the state changed
// underneath them. ClipSeq backs NewClipID so ids stay unique across the session
// even after deletes.
type Project struct {
	Name      string      `json:"name"`
	Folder    string      `json:"folder,omitempty"` // case folder (read-only originals)
	FPS       float64     `json:"fps"`              // project frame rate (timeline math)
	Tracks    []Track     `json:"tracks"`
	Markers   []Marker    `json:"markers,omitempty"`
	Overlay   edl.Overlay `json:"overlay"`             // forensic lower-third defaults
	Playhead  float64     `json:"playhead"`            // seconds on the timeline
	Selection []string    `json:"selection,omitempty"` // selected clip IDs
	Rev       int         `json:"rev"`                 // monotonic; bumps on every mutation
	ClipSeq   int         `json:"clip_seq"`            // backs NewClipID
}

// New builds an empty project with one video track and one audio track — the
// minimal sane timeline. fps<=0 falls back to edl.DefaultFPS.
func New(name string, fps float64) *Project {
	if fps <= 0 {
		fps = edl.DefaultFPS
	}
	return &Project{
		Name: name,
		FPS:  fps,
		Tracks: []Track{
			{Index: 0, Name: "V1", Kind: KindVideo, Clips: []Clip{}},
			{Index: 1, Name: "A1", Kind: KindAudio, Clips: []Clip{}},
		},
		Overlay: edl.Overlay{Position: "bottom"},
	}
}

// Clone returns a deep copy of the whole project. Mutators clone, edit, and bump
// Rev so callers never share backing arrays — the copy-on-write contract.
func (p *Project) Clone() *Project {
	cp := *p
	cp.Tracks = make([]Track, len(p.Tracks))
	for i, t := range p.Tracks {
		cp.Tracks[i] = t.Clone()
	}
	if p.Markers != nil {
		cp.Markers = append([]Marker(nil), p.Markers...)
	}
	if p.Selection != nil {
		cp.Selection = append([]string(nil), p.Selection...)
	}
	return &cp
}

// BumpRev increments Rev in place. Mutators call this on the clone they return so
// the Rev discipline is one obvious call.
func (p *Project) BumpRev() { p.Rev++ }

// NewClipID returns the next stable clip id ("c1", "c2", …) and advances ClipSeq.
// It mutates the receiver, so call it on a clone, not on shared state.
func (p *Project) NewClipID() string {
	p.ClipSeq++
	return fmt.Sprintf("c%d", p.ClipSeq)
}

// TrackByIndex returns a pointer to the track with the given Index, or nil. The
// pointer is into the receiver's slice — only valid on a clone you own.
func (p *Project) TrackByIndex(idx int) *Track {
	for i := range p.Tracks {
		if p.Tracks[i].Index == idx {
			return &p.Tracks[i]
		}
	}
	return nil
}

// FindClip locates a clip by id and returns its track index, position within the
// track's Clips slice, and a copy of the clip. found is false when no clip matches.
func (p *Project) FindClip(id string) (trackIdx, clipPos int, clip Clip, found bool) {
	for ti := range p.Tracks {
		for ci := range p.Tracks[ti].Clips {
			if p.Tracks[ti].Clips[ci].ID == id {
				return p.Tracks[ti].Index, ci, p.Tracks[ti].Clips[ci], true
			}
		}
	}
	return 0, 0, Clip{}, false
}

// ClipCount is the total number of clips across all tracks.
func (p *Project) ClipCount() int {
	n := 0
	for _, t := range p.Tracks {
		n += len(t.Clips)
	}
	return n
}

// HasClip reports whether a clip id exists.
func (p *Project) HasClip(id string) bool {
	_, _, _, ok := p.FindClip(id)
	return ok
}

// Duration is the timeline end: the maximum clip End across all tracks.
func (p *Project) Duration() float64 {
	var max float64
	for _, t := range p.Tracks {
		for _, c := range t.Clips {
			if e := c.End(); e > max {
				max = e
			}
		}
	}
	return max
}

// Validate checks structural invariants (used by tests + the bridge's selftest):
// unique clip ids, non-negative times, In<=Out, selection referencing real clips.
// It returns the first problem found, or nil. It never mutates.
func (p *Project) Validate() error {
	if p.FPS <= 0 {
		return fmt.Errorf("project fps must be > 0, got %g", p.FPS)
	}
	seen := map[string]bool{}
	for _, t := range p.Tracks {
		for _, c := range t.Clips {
			if c.ID == "" {
				return fmt.Errorf("clip with empty id on track %d", t.Index)
			}
			if seen[c.ID] {
				return fmt.Errorf("duplicate clip id %q", c.ID)
			}
			seen[c.ID] = true
			if c.Source == "" {
				return fmt.Errorf("clip %q has empty source", c.ID)
			}
			if c.In < 0 || c.Out < 0 || c.Pos < 0 {
				return fmt.Errorf("clip %q has a negative time (in=%g out=%g pos=%g)", c.ID, c.In, c.Out, c.Pos)
			}
			if c.Out < c.In {
				return fmt.Errorf("clip %q has out<in (%g<%g)", c.ID, c.Out, c.In)
			}
		}
	}
	for _, id := range p.Selection {
		if !seen[id] {
			return fmt.Errorf("selection references missing clip %q", id)
		}
	}
	return nil
}
