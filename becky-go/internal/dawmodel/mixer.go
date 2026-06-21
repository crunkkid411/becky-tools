package dawmodel

import "fmt"

// mixer.go is the per-track mixer model (SPEC §5.2): a channel strip per track plus
// bus routing. It edits params only — the becky-canvas DAW ENGINE reads gain/pan to
// render audio. Mute/solo are flags; a single declared sidechain edge is the
// Cubase-killer (one declaration, not 100 clicks). All edits are PURE and return a
// NEW arrangement.

// Strip is one channel strip: gain (linear, engine-interpreted), pan (-1..1),
// mute/solo flags, and the bus it routes to.
type Strip struct {
	Gain float64 `json:"gain"` // 0..2 (1 = unity)
	Pan  float64 `json:"pan"`  // -1 (L) .. 0 (C) .. 1 (R)
	Mute bool    `json:"mute"`
	Solo bool    `json:"solo"`
	Bus  string  `json:"bus"` // destination bus id

	// FX is the ordered insert chain on this track (FX[0] first in the signal path).
	// Filled when Jordan finalizes / loads his plugin chain; the actual plugin state
	// is loaded by the C++ VST3 host from PresetRef. Empty during lightweight writing.
	FX []FXSlot `json:"fx,omitempty"`
}

// FXSlot is one VST insert on a strip or bus: the plugin name, its VST3 class id, a
// reference to its saved state (a .vstpreset / state file the C++ host loads via
// IComponent::setState), and a bypass flag. Mirrors internal/fxchain.Plugin — this is
// the in-arrangement state; fxchain is the user's saved library of reusable chains.
type FXSlot struct {
	Name      string `json:"name"`
	ClassID   string `json:"class_id,omitempty"`
	PresetRef string `json:"preset_ref,omitempty"`
	Bypass    bool   `json:"bypass,omitempty"`
}

// Bus is a mix bus with its own routing + optional sidechain sources. Matches the
// shape of music.Project's routing so a becky mix stays portable plain JSON.
type Bus struct {
	ID        string   `json:"id"`
	Out       string   `json:"out"`                 // where this bus routes (another bus / master)
	Sidechain []string `json:"sidechain,omitempty"` // source node ids ducking this bus
	FX        []FXSlot `json:"fx,omitempty"`        // the bus's insert chain (comp/EQ/etc.)
}

// defaultStrip returns a unity strip routed to a bus chosen by the track's role.
func defaultStrip(trackID string) Strip {
	return Strip{Gain: 1, Pan: 0, Bus: busForTrack(trackID)}
}

// busForTrack mirrors music.busFor: bass -> bus.808, drums -> bus.drums, sfx ->
// bus.fx, everything pitched -> bus.music.
func busForTrack(name string) string {
	switch name {
	case "bass":
		return "bus.808"
	case "drums":
		return "bus.drums"
	case "sfx":
		return "bus.fx"
	default:
		return "bus.music"
	}
}

// SetGain sets a track's fader and returns the new arrangement. An override of a
// non-unity auto value is logged so becky learns Jordan's level preferences.
func (a *Arrangement) SetGain(trackID string, gain float64) (*Arrangement, error) {
	out := a.clone()
	t := out.trackPtr(trackID)
	if t == nil {
		return a, fmt.Errorf("set gain: track %q not found", trackID)
	}
	old := t.Strip.Gain
	gain = clampGain(gain)
	if old != gain {
		out.logCorrection("gain", trackID, 0, ftoa(old), ftoa(gain))
	}
	t.Strip.Gain = gain
	return out, nil
}

// SetPan sets a track's pan (-1..1) and returns the new arrangement.
func (a *Arrangement) SetPan(trackID string, pan float64) (*Arrangement, error) {
	out := a.clone()
	t := out.trackPtr(trackID)
	if t == nil {
		return a, fmt.Errorf("set pan: track %q not found", trackID)
	}
	t.Strip.Pan = clampPan(pan)
	return out, nil
}

// SetMute toggles a strip's mute flag and returns the new arrangement.
func (a *Arrangement) SetMute(trackID string, mute bool) (*Arrangement, error) {
	out := a.clone()
	t := out.trackPtr(trackID)
	if t == nil {
		return a, fmt.Errorf("set mute: track %q not found", trackID)
	}
	t.Strip.Mute = mute
	return out, nil
}

// SetSolo toggles a strip's solo flag and returns the new arrangement.
func (a *Arrangement) SetSolo(trackID string, solo bool) (*Arrangement, error) {
	out := a.clone()
	t := out.trackPtr(trackID)
	if t == nil {
		return a, fmt.Errorf("set solo: track %q not found", trackID)
	}
	t.Strip.Solo = solo
	return out, nil
}

// RouteTo changes the bus a track feeds and returns the new arrangement.
func (a *Arrangement) RouteTo(trackID, bus string) (*Arrangement, error) {
	out := a.clone()
	t := out.trackPtr(trackID)
	if t == nil {
		return a, fmt.Errorf("route: track %q not found", trackID)
	}
	t.Strip.Bus = bus
	return out, nil
}

// AddSidechain declares "duck <bus> off <source>" as one edge on the bus (the
// one-declaration sidechain). Idempotent: a duplicate source is not re-added.
func (a *Arrangement) AddSidechain(busID, source string) (*Arrangement, error) {
	out := a.clone()
	b := out.busPtr(busID)
	if b == nil {
		out.Buses = append(out.Buses, Bus{ID: busID, Out: "bus.master"})
		b = &out.Buses[len(out.Buses)-1]
	}
	for _, s := range b.Sidechain {
		if s == source {
			return out, nil // already declared
		}
	}
	b.Sidechain = append(b.Sidechain, source)
	return out, nil
}

// SoloedTracks lists the IDs of tracks currently soloed (deterministic order).
func (a *Arrangement) SoloedTracks() []string {
	var out []string
	for _, t := range a.Tracks {
		if t.Strip.Solo {
			out = append(out, t.ID)
		}
	}
	return out
}

func (a *Arrangement) trackPtr(id string) *Track {
	for i := range a.Tracks {
		if a.Tracks[i].ID == id {
			return &a.Tracks[i]
		}
	}
	return nil
}

func (a *Arrangement) busPtr(id string) *Bus {
	for i := range a.Buses {
		if a.Buses[i].ID == id {
			return &a.Buses[i]
		}
	}
	return nil
}

func clampGain(g float64) float64 {
	if g < 0 {
		return 0
	}
	if g > 2 {
		return 2
	}
	return g
}

func clampPan(p float64) float64 {
	if p < -1 {
		return -1
	}
	if p > 1 {
		return 1
	}
	return p
}
