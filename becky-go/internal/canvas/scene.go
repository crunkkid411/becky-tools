package canvas

import (
	"encoding/json"
	"sort"
)

// Scene is the deterministic top-level model the GUI renders: the mode tabs, the
// active mode, the track lanes, the shared transport+viewport, the routing edges
// (the DAW differentiator), and the corrections log Jordan's by-eye fixes append to.
// It is plain data — sorted and map-free — so emitting it is byte-identical for the
// same input (becky's offline+deterministic ethos, SPEC §5).
type Scene struct {
	SchemaVersion int            `json:"schemaVersion"`
	Tool          string         `json:"tool"`
	Title         string         `json:"title"`
	Modes         []Mode         `json:"modes"`       // tab order (deterministic)
	ActiveMode    Mode           `json:"activeMode"`  // the surface currently shown
	Transport     Transport      `json:"transport"`   // shared clock (tempo/ppq/playhead)
	Viewport      Viewport       `json:"viewport"`    // the camera onto the timeline
	Tracks        []Track        `json:"tracks"`      // lane stack, ordered
	Routing       []RouteEdge    `json:"routing"`     // audio/sidechain graph edges
	Corrections   CorrectionsLog `json:"corrections"` // by-eye fix history (learning hook)
}

// RouteEdge mirrors becky-compose's routing edge as the canvas's DAG edge: a single
// declared link (a sidechain is ONE edge, not 100 clicks — SPEC §5). Kept as plain
// data the DAW mode's mixer reads.
type RouteEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`           // audio | sidechain | control
	Note string `json:"note,omitempty"` // why the edge exists
}

// SchemaVersion of the emitted scene.json. Bump on a breaking shape change.
const SchemaVersion = 1

// NewScene returns an empty scene for a given mode, with the full deterministic mode
// tab set, sane viewport, and an empty corrections log. It is the floor every loader
// builds on, and the degrade target when a project can't be read (a usable empty
// canvas, never a crash).
func NewScene(mode Mode) Scene {
	if !ValidMode(string(mode)) {
		mode = ModeAsk
	}
	return Scene{
		SchemaVersion: SchemaVersion,
		Tool:          "becky-canvas",
		Modes:         Modes(),
		ActiveMode:    mode,
		Transport:     Transport{},
		Viewport:      NewViewport(),
		Tracks:        []Track{},
		Routing:       []RouteEdge{},
		Corrections:   NewCorrectionsLog(),
	}
}

// sortTracks orders the lane stack deterministically by ID so the stack never
// reshuffles between runs (no map-iteration order leaks into the scene).
func sortTracks(ts []Track) {
	sort.SliceStable(ts, func(i, j int) bool { return ts[i].ID < ts[j].ID })
}

// sortRouting orders routing edges deterministically (From, then To, then Kind).
func sortRouting(es []RouteEdge) {
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

// JSON marshals the scene as indented, deterministic JSON (the scene.json contract).
// Go's encoder emits struct fields in declaration order and our slices are pre-sorted,
// so the same scene always produces byte-identical output.
func (s Scene) JSON() ([]byte, error) {
	return json.MarshalIndent(s, "", "  ")
}
