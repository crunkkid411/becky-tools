// Package canvasbridge is the spine adapter for Becky Canvas (CANVAS-BLUEPRINT.md
// Step 1): it projects the rich, editable dawmodel.Arrangement onto the canvas.Scene
// the Gio window draws, and loads a becky-compose project.json INTO an Arrangement.
//
// The direction of truth is one-way: the Arrangement is the single source of musical
// truth; the Scene is a DERIVED render-cache rebuilt after every edit (never edited
// directly). Unlike internal/canvas's old music.Project→Scene shortcut (which used
// placeholder clip lengths and an empty pitch lane because it never parsed the .mid),
// this adapter carries the REAL notes, so clip lengths and the piano-roll pitch lane
// are exact.
//
// It lives in its own package because internal/canvas does not import dawmodel and
// must not (a cycle); canvasbridge imports both, plus composearr for the import side.
// Deterministic + degrade-never-crash: a nil arrangement yields an empty-but-valid
// scene, never a panic.
package canvasbridge

import (
	"fmt"
	"sort"

	"becky-go/internal/canvas"
	"becky-go/internal/composearr"
	"becky-go/internal/dawmodel"
)

// SceneFromArrangement projects an editable arrangement onto the deterministic
// canvas.Scene the window renders. Tracks become lanes (with real note-derived clip
// lengths + pitch lanes); buses' sidechain edges become routing edges. Same input ->
// byte-identical scene (sorted, map-free), per the canvas determinism contract.
func SceneFromArrangement(a *dawmodel.Arrangement) canvas.Scene {
	s := canvas.NewScene(canvas.ModeDAW)
	s.Tool = "becky-canvas"
	if a == nil {
		return s
	}

	s.Title = a.Genre
	s.Transport = canvas.Transport{BPM: a.BPM, PPQ: ppqOf(a)}
	s.Viewport = canvas.NewViewport()
	s.Viewport.PxPerTick = 96.0 / float64(ppqOf(a)) // ~96px per quarter note

	s.Tracks = make([]canvas.Track, 0, len(a.Tracks))
	for _, t := range a.Tracks {
		s.Tracks = append(s.Tracks, laneFromTrack(t, ppqOf(a)))
	}
	sortTracks(s.Tracks)

	s.Routing = routingFromBuses(a.Buses)
	sortRouting(s.Routing)
	return s
}

// laneFromTrack maps one editable track to a canvas lane. The lane's clips carry the
// real note extent; a MIDI lane gets a pitch lane spanning its notes (the surface the
// piano roll draws). An audio track gets a waveform lane placeholder.
func laneFromTrack(t dawmodel.Track, ppq int) canvas.Track {
	ct := canvas.Track{
		ID:     t.ID,
		Name:   t.ID,
		Kind:   laneKind(t.Kind),
		Bus:    t.Strip.Bus,
		Muted:  t.Strip.Mute,
		Soloed: t.Strip.Solo,
	}

	lo, hi := 127, 0
	hasNotes := false
	for _, c := range t.Clips {
		ct.Channel = c.Channel
		start := int64(c.Offset)
		end := start
		for _, n := range c.Notes {
			hasNotes = true
			if n.Pitch < lo {
				lo = n.Pitch
			}
			if n.Pitch > hi {
				hi = n.Pitch
			}
			if e := int64(c.Offset + n.Start + n.Dur); e > end {
				end = e
			}
		}
		clipLen := end - start
		if clipLen <= 0 {
			clipLen = int64(ppq) * 4 // a 4/4 bar, so an empty clip is still visible
		}
		ct.Clips = append(ct.Clips, canvas.Clip{
			ID:    t.ID + "." + c.Name,
			Name:  c.Name,
			Start: start,
			Len:   clipLen,
		})
	}

	if ct.Kind == canvas.LaneMIDI {
		if !hasNotes {
			lo, hi = 36, 84 // C2..C6 default span
		}
		ct.Lane.Pitch = &canvas.PitchLane{Lo: lo, Hi: hi, Points: pitchPoints(t)}
	} else if ct.Kind == canvas.LaneAudio {
		ct.Lane.Wave = &canvas.WaveLane{}
	}
	return ct
}

// pitchPoints flattens a track's notes into the pitch-lane contour the piano roll
// renders: one point at each note's absolute start, sorted by (tick, pitch).
func pitchPoints(t dawmodel.Track) []canvas.PitchPoint {
	var pts []canvas.PitchPoint
	for _, c := range t.Clips {
		for _, n := range c.Notes {
			pts = append(pts, canvas.PitchPoint{
				Tick:  int64(c.Offset + n.Start),
				Pitch: float64(n.Pitch),
			})
		}
	}
	sort.SliceStable(pts, func(i, j int) bool {
		if pts[i].Tick != pts[j].Tick {
			return pts[i].Tick < pts[j].Tick
		}
		return pts[i].Pitch < pts[j].Pitch
	})
	return pts
}

// routingFromBuses turns each bus's sidechain sources into canvas routing edges.
func routingFromBuses(buses []dawmodel.Bus) []canvas.RouteEdge {
	var out []canvas.RouteEdge
	for _, b := range buses {
		for _, src := range b.Sidechain {
			out = append(out, canvas.RouteEdge{
				From: src, To: b.ID, Kind: "sidechain",
				Note: "ducks " + b.ID,
			})
		}
	}
	return out
}

// ArrangementFromProjectFile loads a becky-compose project.json (+ its .mid stems)
// into an editable Arrangement, so opening a project in the canvas yields the rich
// model. Thin wrapper over composearr (the same converter becky-reaper uses).
func ArrangementFromProjectFile(path string) (*dawmodel.Arrangement, error) {
	proj, baseDir, err := composearr.LoadProject(path)
	if err != nil {
		return nil, fmt.Errorf("load project: %w", err)
	}
	return composearr.FromProject(proj, baseDir)
}

func ppqOf(a *dawmodel.Arrangement) int {
	if a == nil || a.PPQ <= 0 {
		return 480
	}
	return a.PPQ
}

func laneKind(k string) canvas.LaneKind {
	if k == dawmodel.KindAudio {
		return canvas.LaneAudio
	}
	return canvas.LaneMIDI
}

// sortTracks / sortRouting mirror internal/canvas's deterministic ordering so the
// derived scene is byte-stable regardless of arrangement track order.
func sortTracks(ts []canvas.Track) {
	sort.SliceStable(ts, func(i, j int) bool { return ts[i].ID < ts[j].ID })
}

func sortRouting(es []canvas.RouteEdge) {
	sort.SliceStable(es, func(i, j int) bool {
		if es[i].From != es[j].From {
			return es[i].From < es[j].From
		}
		if es[i].To != es[j].To {
			return es[i].To < es[j].To
		}
		return es[i].Kind < es[j].Kind
	})
}
