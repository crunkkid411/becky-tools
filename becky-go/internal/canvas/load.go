package canvas

import (
	"encoding/json"
	"fmt"
	"os"

	"becky-go/internal/music"
	"becky-go/internal/pathx"
)

// Load reads a becky-compose project.json from path and builds a DAW-mode Scene from
// it deterministically. Degrade, never crash (CLAUDE.md §2): a missing/unreadable/
// malformed file returns a typed, wrapped error AND a usable empty DAW scene, so the
// canvas still opens. pathx.Base is used for the title so a Windows-style input path
// (C:\...\project.json) still yields a clean name when running on Linux/CI.
func Load(path string) (Scene, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return NewScene(ModeDAW), fmt.Errorf("read project %q: %w", pathx.Base(path), err)
	}
	scene, err := SceneFromProjectJSON(data)
	if err != nil {
		return NewScene(ModeDAW), fmt.Errorf("parse project %q: %w", pathx.Base(path), err)
	}
	scene.Title = pathx.Base(path)
	return scene, nil
}

// SceneFromProjectJSON parses raw project.json bytes into a DAW-mode Scene. Separated
// from Load so the mapping is testable without touching the filesystem.
func SceneFromProjectJSON(data []byte) (Scene, error) {
	var p music.Project
	if err := json.Unmarshal(data, &p); err != nil {
		return NewScene(ModeDAW), fmt.Errorf("unmarshal project: %w", err)
	}
	return SceneFromProject(p), nil
}

// SceneFromProject maps a parsed becky-compose Project into a deterministic DAW-mode
// Scene. The project's tempo/ppq become the shared transport; each ProjTrack becomes
// a lane (one clip spanning a default arrangement length); the routing edges carry
// straight across as the canvas DAG. Output is fully sorted — no map order leaks in.
func SceneFromProject(p music.Project) Scene {
	s := NewScene(ModeDAW)
	s.Tool = "becky-canvas"
	s.Transport = transportFromProject(p)
	s.Viewport = viewportFor(p)

	s.Tracks = tracksFromProject(p)
	sortTracks(s.Tracks)

	s.Routing = routingFromProject(p)
	sortRouting(s.Routing)
	return s
}

// transportFromProject lifts tempo + resolution off the project. A zero PPQ falls
// back to becky-compose's standard 480 so the timeline still has a usable scale.
func transportFromProject(p music.Project) Transport {
	return Transport{BPM: p.Tempo, PPQ: ppqOf(p)}
}

// ppqOf returns the project's resolution, falling back to becky-compose's standard
// 480 PPQ when the project omits or zeroes it (degrade to a usable scale).
func ppqOf(p music.Project) int {
	if p.PPQ <= 0 {
		return music.PPQ
	}
	return p.PPQ
}

// viewportFor returns a default viewport whose zoom suits the project's resolution
// (so a quarter note is a comfortable width regardless of PPQ).
func viewportFor(p music.Project) Viewport {
	v := NewViewport()
	v.PxPerTick = 96.0 / float64(ppqOf(p)) // aim for ~96px per quarter note
	return v
}

// defaultClipBars is the placeholder clip length, in bars, given to each lane until
// the real MIDI is scanned (the headless foundation does not parse the .mid files —
// the GUI/decoder fills exact lengths and the waveform/pitch placeholders).
const defaultClipBars = 8

// tracksFromProject builds one lane per ProjTrack: MIDI lanes (compose emits MIDI),
// each with a single placeholder clip spanning a default arrangement and an empty
// pitch lane the piano-roll/pitch surface fills.
func tracksFromProject(p music.Project) []Track {
	barTicks := int64(ppqOf(p)) * 4 // 4/4 bar
	clipLen := barTicks * defaultClipBars

	out := make([]Track, 0, len(p.Tracks))
	for _, pt := range p.Tracks {
		out = append(out, trackFromProj(pt, clipLen))
	}
	return out
}

// trackFromProj maps one ProjTrack to a Track lane with its placeholder clip + lanes.
func trackFromProj(pt music.ProjTrack, clipLen int64) Track {
	return Track{
		ID:      pt.ID,
		Name:    pt.ID,
		Kind:    LaneMIDI, // becky-compose emits MIDI stems
		Channel: pt.Channel,
		Bus:     pt.Out,
		Source:  pt.Midi,
		Clips: []Clip{{
			ID:    pt.ID + ".clip0",
			Name:  pt.ID,
			Start: 0,
			Len:   clipLen,
		}},
		Lane: Lane{Pitch: newPitchLane(0, 0)},
	}
}

// routingFromProject carries the project's routing edges across as canvas DAG edges.
func routingFromProject(p music.Project) []RouteEdge {
	out := make([]RouteEdge, 0, len(p.Routing))
	for _, e := range p.Routing {
		out = append(out, RouteEdge{From: e.From, To: e.To, Kind: e.Kind, Note: e.Note})
	}
	return out
}
