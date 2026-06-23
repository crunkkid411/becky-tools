package editmodel

import (
	"sort"

	"becky-go/internal/edl"
)

// ToReel flattens the project's video timeline into an edl.Reel — the frozen
// clip-list contract internal/reel (ffmpeg render + forensic lower-third) and
// internal/kdenlive (MLT writer that Shotcut opens) already consume. becky-edit
// never reimplements rendering; it hands the host this Reel.
//
// The Reel is gapless/sequential (the forensic compilation is a concatenation),
// so clips are taken from the FIRST video track in timeline order (by Pos, then
// id for stability). Per-clip Overlay values come from each clip's Meta; the
// reel-wide defaults come from the project Overlay. Audio-only tracks are not
// rendered here (the compilation's audio rides with its video clips) — a full
// multi-track mixdown is a host/MLT concern, out of scope for the quick render.
func (p *Project) ToReel() edl.Reel {
	r := edl.Reel{
		Version: "1",
		Name:    p.Name,
		Overlay: p.Overlay,
		Clips:   []edl.Clip{},
	}
	vt := p.firstVideoTrack()
	if vt == nil {
		return r
	}
	clips := append([]Clip(nil), vt.Clips...)
	sort.SliceStable(clips, func(i, j int) bool {
		if clips[i].Pos != clips[j].Pos {
			return clips[i].Pos < clips[j].Pos
		}
		return clips[i].ID < clips[j].ID
	})
	for _, c := range clips {
		if c.Dur() <= 0 {
			continue // skip zero-length clips (never emit an empty render span)
		}
		r.Clips = append(r.Clips, edl.Clip{
			ID:     c.ID,
			Source: c.Source,
			In:     c.In,
			Out:    c.Out,
			Label:  c.Label,
			Meta:   c.Meta,
		})
	}
	return r
}

// firstVideoTrack returns the lowest-Index video track, or nil if none has clips.
func (p *Project) firstVideoTrack() *Track {
	var best *Track
	for i := range p.Tracks {
		if p.Tracks[i].Kind != KindVideo {
			continue
		}
		if best == nil || p.Tracks[i].Index < best.Index {
			best = &p.Tracks[i]
		}
	}
	return best
}

// FromReel rebuilds a Project from an edl.Reel — the inverse of ToReel, used when
// becky-edit opens a previously-built compilation (or imports one becky-clip
// produced). Clips land on V1 laid end-to-end in Reel order; positions are the
// running compilation offsets. ClipSeq is advanced past the imported ids so new
// clips never collide.
func FromReel(r edl.Reel, fps float64) *Project {
	p := New(r.Name, fps)
	p.Overlay = r.Overlay
	vt := p.TrackByIndex(0)
	var pos float64
	for _, c := range r.Clips {
		ec := Clip{
			ID:     c.ID,
			Source: c.Source,
			In:     c.In,
			Out:    c.Out,
			Pos:    pos,
			Label:  c.Label,
			Meta:   c.Meta,
		}
		vt.Clips = append(vt.Clips, ec)
		pos += ec.Dur()
		if n := clipSeqOf(c.ID); n > p.ClipSeq {
			p.ClipSeq = n
		}
	}
	return p
}

// clipSeqOf parses the numeric suffix of a "c<N>" id so FromReel can advance
// ClipSeq past imported ids. Non-matching ids yield 0.
func clipSeqOf(id string) int {
	if len(id) < 2 || id[0] != 'c' {
		return 0
	}
	n := 0
	for _, r := range id[1:] {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
