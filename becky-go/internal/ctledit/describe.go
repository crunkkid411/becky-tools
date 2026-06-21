package ctledit

import "becky-go/internal/dawmodel"

// describe.go is the introspection side of the dual human+agent operability rule
// (STANDARDS-CANVAS-UX.md §3 — the ARIA analog): it emits the stable, addressable
// IDs of everything in an arrangement so an agent can DISCOVER what it can act on,
// the way a human reads the panels. Read-only; never mutates.

// SceneInfo is a flat, agent-readable map of an arrangement's addressable elements.
type SceneInfo struct {
	BPM    int         `json:"bpm"`
	Root   string      `json:"root,omitempty"`
	Scale  string      `json:"scale,omitempty"`
	Tracks []TrackInfo `json:"tracks"`
}

// TrackInfo describes one track and the handles on it an agent can target.
type TrackInfo struct {
	ID     string     `json:"id"`
	Kind   string     `json:"kind"`
	Gain   float64    `json:"gain"`
	Pan    float64    `json:"pan"`
	Muted  bool       `json:"muted"`
	Soloed bool       `json:"soloed"`
	Bus    string     `json:"bus,omitempty"`
	Clips  []ClipInfo `json:"clips"`
}

// ClipInfo describes one clip; for drum clips it lists the lane names so an agent
// can target a lane by name (e.g. euclid_lane on "kick").
type ClipInfo struct {
	Name    string   `json:"name"`
	Channel int      `json:"channel"`
	Notes   int      `json:"notes"`
	IsDrum  bool     `json:"is_drum"`
	Lanes   []string `json:"lanes,omitempty"`
}

// Describe returns the addressable scene for an arrangement. Safe on nil.
func Describe(a *dawmodel.Arrangement) SceneInfo {
	var si SceneInfo
	if a == nil {
		return si
	}
	si.BPM, si.Root, si.Scale = a.BPM, a.Root, a.Scale
	for _, t := range a.Tracks {
		ti := TrackInfo{ID: t.ID, Kind: t.Kind}
		ti.Gain, ti.Pan = t.Strip.Gain, t.Strip.Pan
		ti.Muted, ti.Soloed, ti.Bus = t.Strip.Mute, t.Strip.Solo, t.Strip.Bus
		for _, c := range t.Clips {
			ci := ClipInfo{Name: c.Name, Channel: c.Channel, Notes: len(c.Notes)}
			ci.IsDrum = c.Channel == 9 || drumByNotes(c)
			if ci.IsDrum {
				if g, err := a.DrumGridOf(t.ID, c.Name, 0); err == nil && g != nil {
					for _, ln := range g.Lanes {
						ci.Lanes = append(ci.Lanes, ln.Name)
					}
				}
			}
			ti.Clips = append(ti.Clips, ci)
		}
		si.Tracks = append(si.Tracks, ti)
	}
	return si
}

func drumByNotes(c dawmodel.Clip) bool {
	for _, n := range c.Notes {
		if n.Ch == 9 {
			return true
		}
	}
	return false
}
