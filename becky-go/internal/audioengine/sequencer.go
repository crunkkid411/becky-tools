package audioengine

// sequencer.go — pure-Go, allocation-light scheduling layer (SPEC §3).
//
// It converts a DrumGrid or a slice of piano-roll Notes into a flat,
// deterministically-ordered []ScheduledEvent that the (Phase-2, cgo) audio
// callback can walk linearly without doing any music math under the deadline.
//
// Determinism contract:
//   - Primary sort key  : SampleOffset ascending.
//   - Secondary sort key: Note (pitch/MIDI note number) ascending.
//   - Tie-break         : note-off events sort BEFORE note-on events at the
//                         same (SampleOffset, Note). This follows the standard
//                         MIDI "running-off-before-on" convention that prevents
//                         a stuck note when a new hit lands on the same tick
//                         as the previous hit's tail.
//
// Becky invariants honoured:
//   - Empty pattern → (nil, nil) — degrade, never error.
//   - No network, no OS, no cgo — pure Go and stdlib only.
//   - No map-iteration order anywhere; all loops are over slices.

import (
	"fmt"
	"sort"

	"becky-go/internal/dawmodel"
)

// ScheduledEvent is one note-on or note-off event with its absolute sample
// offset. The audio callback walks a []ScheduledEvent in order and fires each
// event when the playhead crosses SampleOffset.
//
// Fields are intentionally flat so the Phase-2 native layer can copy them into
// a C struct or a lock-free ring without allocation.
type ScheduledEvent struct {
	// SampleOffset is the absolute sample frame at which this event fires,
	// computed once by TickToSample so the audio callback does zero math.
	SampleOffset int64
	// Note is the MIDI note number (0..127). For drums this is the GM
	// percussion key; for piano-roll notes it is the pitch.
	Note int
	// On is true for note-on, false for note-off.
	On bool
	// Velocity is the MIDI velocity (1..127). Zero on note-off events (MIDI
	// spec: note-off velocity is ignored by most synthesisers).
	Velocity int
	// Channel is the MIDI channel (0..15). Drum lanes use channel 9 (GM
	// percussion); piano-roll notes carry their own channel.
	Channel int
}

// SequenceDrumGrid expands a DrumGrid into a deterministically-ordered slice
// of ScheduledEvents. Each active step produces a note-on at the step's sample
// offset and a note-off half a step later (the same convention DrumGrid.Compile
// uses: dur = StepTicks/2, minimum 1 tick, so hits are distinct even at fast
// tempos). The grid's Channel field is forwarded to every event.
//
// An empty or nil grid, or a grid with no active steps, returns (nil, nil) —
// degrade, not an error.
func SequenceDrumGrid(g *dawmodel.DrumGrid, t *Transport) ([]ScheduledEvent, error) {
	if g == nil || len(g.Lanes) == 0 {
		return nil, nil
	}
	if t == nil {
		return nil, fmt.Errorf("sequencer: transport must not be nil")
	}

	// dur matches DrumGrid.Compile: half a step, minimum 1 tick.
	dur := g.StepTicks / 2
	if dur < 1 {
		dur = 1
	}

	var events []ScheduledEvent
	for _, lane := range g.Lanes {
		for step, on := range lane.On {
			if !on {
				continue
			}
			vel := lane.Vel[step]
			if vel <= 0 {
				vel = 80 // sensible drum default (matches music.Vel("normal"))
			}
			onTick := float64(step * g.StepTicks)
			offTick := float64(step*g.StepTicks + dur)

			events = append(events, ScheduledEvent{
				SampleOffset: t.TickToSample(onTick),
				Note:         lane.Note,
				On:           true,
				Velocity:     vel,
				Channel:      g.Channel,
			})
			events = append(events, ScheduledEvent{
				SampleOffset: t.TickToSample(offTick),
				Note:         lane.Note,
				On:           false,
				Velocity:     0,
				Channel:      g.Channel,
			})
		}
	}

	sortEvents(events)
	return events, nil
}

// SequenceNotes converts a slice of dawmodel.Note (piano-roll notes) into a
// deterministically-ordered slice of ScheduledEvents. Each note produces a
// note-on at Note.Start and a note-off at Note.Start+Note.Dur. Notes with
// Dur <= 0 are skipped (degrade, not an error).
//
// An empty or nil slice returns (nil, nil).
func SequenceNotes(notes []dawmodel.Note, t *Transport) ([]ScheduledEvent, error) {
	if len(notes) == 0 {
		return nil, nil
	}
	if t == nil {
		return nil, fmt.Errorf("sequencer: transport must not be nil")
	}

	events := make([]ScheduledEvent, 0, len(notes)*2)
	for _, n := range notes {
		if n.Dur <= 0 {
			continue // degrade: malformed note, skip it
		}
		events = append(events, ScheduledEvent{
			SampleOffset: t.TickToSample(float64(n.Start)),
			Note:         n.Pitch,
			On:           true,
			Velocity:     n.Vel,
			Channel:      n.Ch,
		})
		events = append(events, ScheduledEvent{
			SampleOffset: t.TickToSample(float64(n.Start + n.Dur)),
			Note:         n.Pitch,
			On:           false,
			Velocity:     0,
			Channel:      n.Ch,
		})
	}

	sortEvents(events)
	return events, nil
}

// sortEvents sorts events in-place by (SampleOffset ASC, Note ASC,
// Off-before-On). The tie-break — note-off sorts before note-on at the same
// sample+note — is the standard MIDI "running-off-before-on" convention that
// prevents stuck notes when a new hit replaces the tail of the previous one.
func sortEvents(ev []ScheduledEvent) {
	sort.SliceStable(ev, func(i, j int) bool {
		a, b := ev[i], ev[j]
		if a.SampleOffset != b.SampleOffset {
			return a.SampleOffset < b.SampleOffset
		}
		if a.Note != b.Note {
			return a.Note < b.Note
		}
		// Off (false) sorts before On (true): note-off fires first.
		if a.On != b.On {
			return !a.On // off-before-on
		}
		return false // identical — stable sort preserves input order
	})
}
