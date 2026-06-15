package dawmodel

import (
	"fmt"
	"sort"

	"becky-go/internal/music"
)

// drumgrid.go is the step-sequencer view: the same 16-step lattice becky-compose
// generates from, now as an editable steps x lanes grid (SPEC §4). A DrumGrid is
// DERIVED from a clip's percussion notes (quantize each note start to the nearest
// step, bucket by MIDI note number = lane), and can be COMPILED back to notes.
// Both directions are deterministic. This is the editor that reads becky-compose's
// drum stem.

// DefaultSteps is one bar of 1/16 cells (the becky-compose drum lattice).
const DefaultSteps = 16

// Lane is one percussion voice in the grid: a MIDI note number + a per-step
// on/off + velocity. Empty cells have Vel 0. The lane is the visual row Jordan
// clicks; Name is a human label (kick/clap/hat) resolved from GM percussion.
type Lane struct {
	Name string `json:"name"`
	Note int    `json:"note"` // GM percussion note number (channel-9 key)
	On   []bool `json:"on"`   // length == grid Steps*Bars
	Vel  []int  `json:"vel"`  // length == grid Steps*Bars; 0 where !On
}

// DrumGrid is a clip rendered as steps x lanes over one or more bars.
type DrumGrid struct {
	Steps     int    `json:"steps"`     // cells per bar (16)
	Bars      int    `json:"bars"`      // bars covered
	StepTicks int    `json:"stepTicks"` // ticks per cell
	Channel   int    `json:"channel"`   // MIDI channel (9 for GM drums)
	Lanes     []Lane `json:"lanes"`
}

// gmDrum maps common GM percussion note numbers to readable lane names.
var gmDrum = map[int]string{
	35: "kick", 36: "kick", 38: "snare", 40: "snare", 37: "rim", 39: "clap",
	42: "hat", 44: "hat", 46: "ohat", 49: "crash", 51: "ride", 41: "tom",
	43: "tom", 45: "tom", 47: "tom", 48: "tom", 50: "tom",
}

// laneName returns a readable name for a percussion note (falls back to "note##").
func laneName(note int) string {
	if n, ok := gmDrum[note]; ok {
		return n
	}
	return fmt.Sprintf("note%d", note)
}

// DrumGridOf derives a step grid from a clip's notes. It quantizes each note's
// start to the nearest step, groups by note number into lanes, and sizes the grid
// to cover the clip. stepTicks defaults to a 1/16 at the arrangement PPQ when <=0.
func (a *Arrangement) DrumGridOf(trackID, clipName string, stepTicks int) (*DrumGrid, error) {
	_, c := a.findClip(trackID, clipName)
	if c == nil {
		return nil, fmt.Errorf("drum grid: clip %q/%q not found", trackID, clipName)
	}
	if stepTicks <= 0 {
		stepTicks = a.PPQ / 4
	}
	if stepTicks <= 0 {
		stepTicks = music.StepTicks
	}
	bars := barsFor(c.Notes, stepTicks)
	g := &DrumGrid{Steps: DefaultSteps, Bars: bars, StepTicks: stepTicks, Channel: c.Channel}
	cells := bars * DefaultSteps
	byNote := map[int]*Lane{}
	for _, n := range c.Notes {
		step := divRound(n.Start, stepTicks)
		if step < 0 || step >= cells {
			continue
		}
		ln := byNote[n.Pitch]
		if ln == nil {
			ln = &Lane{Name: laneName(n.Pitch), Note: n.Pitch, On: make([]bool, cells), Vel: make([]int, cells)}
			byNote[n.Pitch] = ln
		}
		ln.On[step] = true
		if n.Vel > ln.Vel[step] { // keep the loudest hit if two collapse to one cell
			ln.Vel[step] = clampVel(n.Vel)
		}
	}
	g.Lanes = sortedLanes(byNote)
	return g, nil
}

// barsFor computes how many 16-step bars the notes span (at least one).
func barsFor(notes []Note, stepTicks int) int {
	maxStep := 0
	for _, n := range notes {
		if s := divRound(n.Start, stepTicks); s > maxStep {
			maxStep = s
		}
	}
	return maxStep/DefaultSteps + 1
}

// sortedLanes returns lanes ordered by note number (deterministic, no map order).
func sortedLanes(byNote map[int]*Lane) []Lane {
	notes := make([]int, 0, len(byNote))
	for k := range byNote {
		notes = append(notes, k)
	}
	sort.Ints(notes)
	out := make([]Lane, 0, len(notes))
	for _, k := range notes {
		out = append(out, *byNote[k])
	}
	return out
}

// SetStep toggles/sets one grid cell and returns a NEW grid (immutability). on=true
// with vel<=0 uses a sensible default ("normal"). Out-of-range indices are ignored.
func (g *DrumGrid) SetStep(laneIdx, step int, on bool, vel int) *DrumGrid {
	out := g.cloneGrid()
	if laneIdx < 0 || laneIdx >= len(out.Lanes) {
		return out
	}
	ln := &out.Lanes[laneIdx]
	if step < 0 || step >= len(ln.On) {
		return out
	}
	ln.On[step] = on
	if on {
		if vel <= 0 {
			vel = music.Vel("normal")
		}
		ln.Vel[step] = clampVel(vel)
	} else {
		ln.Vel[step] = 0
	}
	return out
}

func (g *DrumGrid) cloneGrid() *DrumGrid {
	out := *g
	out.Lanes = make([]Lane, len(g.Lanes))
	for i, ln := range g.Lanes {
		ln.On = append([]bool(nil), ln.On...)
		ln.Vel = append([]int(nil), ln.Vel...)
		out.Lanes[i] = ln
	}
	return &out
}

// Compile renders the grid back into clip Notes (each on-cell -> a note of half a
// step length, the becky-compose drum-note convention). Deterministic order:
// lanes by note number, then steps ascending. dur defaults to half a step.
func (g *DrumGrid) Compile(idAlloc func() uint64) []Note {
	dur := maxInt(g.StepTicks/2, 1)
	var notes []Note
	for _, ln := range g.Lanes {
		for step, on := range ln.On {
			if !on {
				continue
			}
			vel := ln.Vel[step]
			if vel <= 0 {
				vel = music.Vel("normal")
			}
			notes = append(notes, Note{
				ID: idAlloc(), Start: step * g.StepTicks, Dur: dur,
				Pitch: ln.Note, Vel: clampVel(vel), Ch: g.Channel,
			})
		}
	}
	sortNotes(notes)
	return notes
}

// ApplyDrumGrid replaces a clip's notes with the compiled grid and returns the new
// arrangement. This is the drum machine "Apply" — the grid edit becomes notes that
// save through the same byte-stable writer.
func (a *Arrangement) ApplyDrumGrid(trackID, clipName string, g *DrumGrid) (*Arrangement, error) {
	out := a.clone()
	_, c := out.findClip(trackID, clipName)
	if c == nil {
		return a, fmt.Errorf("apply drum grid: clip %q/%q not found", trackID, clipName)
	}
	c.Notes = g.Compile(out.allocID)
	c.Channel = g.Channel
	return out, nil
}
