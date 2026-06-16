package audioengine

// sequencer_test.go — table-driven tests for the pure-Go scheduling layer.
//
// All tests follow Arrange-Act-Assert and use the internal package (same
// package audioengine) so they can construct Transport directly. No cgo,
// no model weights, no OS resources — runs cleanly in CI.

import (
	"reflect"
	"testing"

	"becky-go/internal/dawmodel"
)

// mustTransport is a test helper that builds a Transport or fatally fails.
func mustTransport(tb testing.TB, bpm float64, ppq, sr int) *Transport {
	tb.Helper()
	tr, err := NewTransport(bpm, ppq, sr)
	if err != nil {
		tb.Fatalf("NewTransport(%v, %d, %d): %v", bpm, ppq, sr, err)
	}
	return tr
}

// ---- helpers ----------------------------------------------------------------

// makeDrumGrid builds a minimal DrumGrid for testing without depending on the
// Arrangement.DrumGridOf path (which needs a full arrangement). It places lanes
// and steps directly so tests are self-contained.
//
// lanes is a map of MIDI note number -> slice of step indices that are ON.
// steps is the total step count (len of On/Vel per lane).
func makeDrumGrid(steps, stepTicks, channel int, lanes map[int][]int) *dawmodel.DrumGrid {
	g := &dawmodel.DrumGrid{
		Steps:     steps,
		Bars:      1,
		StepTicks: stepTicks,
		Channel:   channel,
	}
	// Build lanes in note-ascending order so the test expectation is stable.
	noteOrder := make([]int, 0, len(lanes))
	for note := range lanes {
		noteOrder = append(noteOrder, note)
	}
	// sort manually (small test data — no extra import needed)
	for i := 0; i < len(noteOrder); i++ {
		for j := i + 1; j < len(noteOrder); j++ {
			if noteOrder[i] > noteOrder[j] {
				noteOrder[i], noteOrder[j] = noteOrder[j], noteOrder[i]
			}
		}
	}
	for _, note := range noteOrder {
		on := make([]bool, steps)
		vel := make([]int, steps)
		for _, s := range lanes[note] {
			on[s] = true
			vel[s] = 100
		}
		g.Lanes = append(g.Lanes, dawmodel.Lane{
			Name: "test",
			Note: note,
			On:   on,
			Vel:  vel,
		})
	}
	return g
}

// ---- SequenceDrumGrid -------------------------------------------------------

func TestSequenceDrumGrid_nilGrid(t *testing.T) {
	// Arrange
	tr := mustTransport(t, 120, 480, 48000)

	// Act
	evs, err := SequenceDrumGrid(nil, tr)

	// Assert
	if err != nil {
		t.Fatalf("nil grid: unexpected error: %v", err)
	}
	if len(evs) != 0 {
		t.Errorf("nil grid: expected 0 events, got %d", len(evs))
	}
}

func TestSequenceDrumGrid_emptyLanes(t *testing.T) {
	// Arrange
	tr := mustTransport(t, 120, 480, 48000)
	g := &dawmodel.DrumGrid{Steps: 16, Bars: 1, StepTicks: 120, Channel: 9}

	// Act
	evs, err := SequenceDrumGrid(g, tr)

	// Assert
	if err != nil {
		t.Fatalf("empty lanes: unexpected error: %v", err)
	}
	if len(evs) != 0 {
		t.Errorf("empty lanes: expected 0 events, got %d", len(evs))
	}
}

func TestSequenceDrumGrid_nilTransport(t *testing.T) {
	// Arrange
	g := makeDrumGrid(16, 120, 9, map[int][]int{36: {0}})

	// Act
	_, err := SequenceDrumGrid(g, nil)

	// Assert
	if err == nil {
		t.Fatal("expected error for nil transport, got nil")
	}
}

func TestSequenceDrumGrid_kickOnStep0(t *testing.T) {
	// Arrange: 120 BPM, 480 PPQ, 48000 Hz → 50 samples/tick.
	// stepTicks = 120 (1/16 note at 480 PPQ).
	// Step 0 tick = 0 → sample 0. dur = 60 ticks → off at sample 3000.
	tr := mustTransport(t, 120, 480, 48000)
	g := makeDrumGrid(16, 120, 9, map[int][]int{
		36: {0}, // kick on step 0 only
	})

	// Act
	evs, err := SequenceDrumGrid(g, tr)

	// Assert
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(evs) != 2 {
		t.Fatalf("expected 2 events (on+off), got %d", len(evs))
	}
	on, off := evs[0], evs[1]
	if !on.On {
		t.Errorf("first event should be note-on, got note-off")
	}
	if on.SampleOffset != 0 {
		t.Errorf("note-on sample: want 0, got %d", on.SampleOffset)
	}
	if on.Note != 36 {
		t.Errorf("note-on MIDI note: want 36 (kick), got %d", on.Note)
	}
	if on.Channel != 9 {
		t.Errorf("note-on channel: want 9, got %d", on.Channel)
	}
	if on.Velocity <= 0 {
		t.Errorf("note-on velocity: want > 0, got %d", on.Velocity)
	}
	if off.On {
		t.Errorf("second event should be note-off, got note-on")
	}
	// dur = 120/2 = 60 ticks → 60*50 = 3000 samples
	if off.SampleOffset != 3000 {
		t.Errorf("note-off sample: want 3000, got %d", off.SampleOffset)
	}
	if off.Velocity != 0 {
		t.Errorf("note-off velocity: want 0 (MIDI convention), got %d", off.Velocity)
	}
}

func TestSequenceDrumGrid_kickAndSnare4x16(t *testing.T) {
	// Arrange: classic "four on the floor + snare on 2&4" in a 16-step bar.
	// 120 BPM, 480 PPQ, 48000 Hz → 50 samples/tick.
	// stepTicks = 120 (1/16 note at 480 PPQ).
	// Kick (note 36) on steps 0, 4, 8, 12 (quarter notes).
	// Snare (note 38) on steps 4, 12 (beats 2 & 4).
	//
	// Expected event count: kick 4 × 2 + snare 2 × 2 = 12 events.
	tr := mustTransport(t, 120, 480, 48000)
	g := makeDrumGrid(16, 120, 9, map[int][]int{
		36: {0, 4, 8, 12},
		38: {4, 12},
	})

	// Act
	evs, err := SequenceDrumGrid(g, tr)

	// Assert
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(evs) != 12 {
		t.Fatalf("expected 12 events, got %d", len(evs))
	}

	// Verify sort invariant: SampleOffset must be non-decreasing.
	for i := 1; i < len(evs); i++ {
		if evs[i].SampleOffset < evs[i-1].SampleOffset {
			t.Errorf("events[%d].SampleOffset %d < events[%d].SampleOffset %d (not sorted)",
				i, evs[i].SampleOffset, i-1, evs[i-1].SampleOffset)
		}
	}

	// Spot-check step-0 on/off pair.
	if evs[0].Note != 36 || !evs[0].On || evs[0].SampleOffset != 0 {
		t.Errorf("events[0]: want kick-on@0, got note=%d on=%v offset=%d",
			evs[0].Note, evs[0].On, evs[0].SampleOffset)
	}
	if evs[1].Note != 36 || evs[1].On || evs[1].SampleOffset != 3000 {
		t.Errorf("events[1]: want kick-off@3000, got note=%d on=%v offset=%d",
			evs[1].Note, evs[1].On, evs[1].SampleOffset)
	}

	// At step 4 sample 24000: kick-on(36) and snare-on(38) land together.
	// Sort: same sample → lower note first → kick before snare.
	step4Start := 2 // events[2] = kick-on at step 4
	if evs[step4Start].Note != 36 || !evs[step4Start].On || evs[step4Start].SampleOffset != 24000 {
		t.Errorf("events[%d]: want kick-on@24000, got note=%d on=%v offset=%d",
			step4Start, evs[step4Start].Note, evs[step4Start].On, evs[step4Start].SampleOffset)
	}
	if evs[step4Start+1].Note != 38 || !evs[step4Start+1].On || evs[step4Start+1].SampleOffset != 24000 {
		t.Errorf("events[%d]: want snare-on@24000, got note=%d on=%v offset=%d",
			step4Start+1, evs[step4Start+1].Note, evs[step4Start+1].On, evs[step4Start+1].SampleOffset)
	}
}

func TestSortEvents_offBeforeOn_sameNoteSameSample(t *testing.T) {
	// Verify the tie-break: at the same (SampleOffset, Note), note-off fires
	// before note-on. This is the MIDI "running-off-before-on" rule.
	evs := []ScheduledEvent{
		{SampleOffset: 100, Note: 60, On: true, Velocity: 80, Channel: 0},
		{SampleOffset: 100, Note: 60, On: false, Velocity: 0, Channel: 0},
		{SampleOffset: 100, Note: 60, On: true, Velocity: 90, Channel: 0},
	}
	sortEvents(evs)

	if evs[0].On {
		t.Errorf("first event at same (sample, note) should be note-off, got note-on (vel=%d)", evs[0].Velocity)
	}
	if !evs[1].On || !evs[2].On {
		t.Errorf("events[1] and [2] should be note-on after the note-off")
	}
}

// ---- SequenceNotes ----------------------------------------------------------

func TestSequenceNotes_nil(t *testing.T) {
	// Arrange
	tr := mustTransport(t, 120, 480, 48000)

	// Act
	evs, err := SequenceNotes(nil, tr)

	// Assert
	if err != nil {
		t.Fatalf("nil notes: unexpected error: %v", err)
	}
	if len(evs) != 0 {
		t.Errorf("nil notes: expected 0 events, got %d", len(evs))
	}
}

func TestSequenceNotes_empty(t *testing.T) {
	tr := mustTransport(t, 120, 480, 48000)
	evs, err := SequenceNotes([]dawmodel.Note{}, tr)
	if err != nil {
		t.Fatalf("empty notes: unexpected error: %v", err)
	}
	if len(evs) != 0 {
		t.Errorf("empty notes: expected 0 events, got %d", len(evs))
	}
}

func TestSequenceNotes_nilTransport(t *testing.T) {
	notes := []dawmodel.Note{{ID: 1, Start: 0, Dur: 480, Pitch: 60, Vel: 80, Ch: 0}}
	_, err := SequenceNotes(notes, nil)
	if err == nil {
		t.Fatal("expected error for nil transport, got nil")
	}
}

func TestSequenceNotes_singleNote(t *testing.T) {
	// Arrange: 120 BPM, 480 PPQ, 48000 Hz → 50 samples/tick.
	// Note at tick 0, dur 480 (one beat = 24000 samples).
	tr := mustTransport(t, 120, 480, 48000)
	notes := []dawmodel.Note{
		{ID: 1, Start: 0, Dur: 480, Pitch: 60, Vel: 80, Ch: 0},
	}

	// Act
	evs, err := SequenceNotes(notes, tr)

	// Assert
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(evs) != 2 {
		t.Fatalf("expected 2 events, got %d", len(evs))
	}
	on, off := evs[0], evs[1]
	if !on.On || on.SampleOffset != 0 || on.Note != 60 || on.Velocity != 80 || on.Channel != 0 {
		t.Errorf("note-on: got {On=%v Offset=%d Note=%d Vel=%d Ch=%d}",
			on.On, on.SampleOffset, on.Note, on.Velocity, on.Channel)
	}
	if off.On || off.SampleOffset != 24000 || off.Note != 60 || off.Velocity != 0 {
		t.Errorf("note-off: got {On=%v Offset=%d Note=%d Vel=%d}",
			off.On, off.SampleOffset, off.Note, off.Velocity)
	}
}

func TestSequenceNotes_overlappingNotes(t *testing.T) {
	// Arrange: two notes that overlap in time.
	// Note A: pitch 60, start 0, dur 960 (2 beats) → on@0, off@48000.
	// Note B: pitch 64, start 480 (1 beat), dur 480 → on@24000, off@48000.
	// Expected sorted: on@0(60), on@24000(64), off@48000(60), off@48000(64).
	// At offset 48000 both notes end; sort by note ASC: 60 before 64.
	tr := mustTransport(t, 120, 480, 48000) // 50 samples/tick
	notes := []dawmodel.Note{
		{ID: 1, Start: 0, Dur: 960, Pitch: 60, Vel: 80, Ch: 0},
		{ID: 2, Start: 480, Dur: 480, Pitch: 64, Vel: 90, Ch: 0},
	}

	// Act
	evs, err := SequenceNotes(notes, tr)

	// Assert
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(evs) != 4 {
		t.Fatalf("expected 4 events, got %d; events: %+v", len(evs), evs)
	}

	want := []ScheduledEvent{
		{SampleOffset: 0, Note: 60, On: true, Velocity: 80, Channel: 0},
		{SampleOffset: 24000, Note: 64, On: true, Velocity: 90, Channel: 0},
		{SampleOffset: 48000, Note: 60, On: false, Velocity: 0, Channel: 0},
		{SampleOffset: 48000, Note: 64, On: false, Velocity: 0, Channel: 0},
	}
	if !reflect.DeepEqual(evs, want) {
		t.Errorf("events mismatch:\ngot  %+v\nwant %+v", evs, want)
	}
}

func TestSequenceNotes_zeroDurSkipped(t *testing.T) {
	// A note with Dur <= 0 is malformed and must be silently skipped (degrade).
	tr := mustTransport(t, 120, 480, 48000)
	notes := []dawmodel.Note{
		{ID: 1, Start: 0, Dur: 0, Pitch: 60, Vel: 80, Ch: 0},   // bad — skip
		{ID: 2, Start: 0, Dur: -1, Pitch: 61, Vel: 80, Ch: 0},  // bad — skip
		{ID: 3, Start: 0, Dur: 480, Pitch: 62, Vel: 80, Ch: 0}, // good
	}
	evs, err := SequenceNotes(notes, tr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(evs) != 2 {
		t.Fatalf("expected 2 events (only the good note), got %d", len(evs))
	}
	if evs[0].Note != 62 {
		t.Errorf("expected pitch 62 (the good note), got %d", evs[0].Note)
	}
}

// ---- determinism ------------------------------------------------------------

func TestSequenceDrumGrid_deterministic(t *testing.T) {
	// Same grid + same transport → identical output on two separate calls.
	tr := mustTransport(t, 137.5, PPQDefault, 44100)
	g := makeDrumGrid(16, PPQDefault/4, 9, map[int][]int{
		36: {0, 4, 8, 12},
		38: {4, 12},
		42: {0, 2, 4, 6, 8, 10, 12, 14},
	})

	a, errA := SequenceDrumGrid(g, tr)
	if errA != nil {
		t.Fatalf("first call error: %v", errA)
	}
	b, errB := SequenceDrumGrid(g, tr)
	if errB != nil {
		t.Fatalf("second call error: %v", errB)
	}

	if !reflect.DeepEqual(a, b) {
		t.Errorf("non-deterministic output:\nfirst  %+v\nsecond %+v", a, b)
	}
}

func TestSequenceNotes_deterministic(t *testing.T) {
	// Same notes + same transport → identical output on two separate calls.
	tr := mustTransport(t, 120, PPQDefault, 48000)
	notes := []dawmodel.Note{
		{ID: 1, Start: 0, Dur: 480, Pitch: 60, Vel: 80, Ch: 0},
		{ID: 2, Start: 240, Dur: 720, Pitch: 67, Vel: 100, Ch: 0},
		{ID: 3, Start: 960, Dur: 480, Pitch: 64, Vel: 70, Ch: 1},
	}

	a, errA := SequenceNotes(notes, tr)
	if errA != nil {
		t.Fatalf("first call error: %v", errA)
	}
	b, errB := SequenceNotes(notes, tr)
	if errB != nil {
		t.Fatalf("second call error: %v", errB)
	}

	if !reflect.DeepEqual(a, b) {
		t.Errorf("non-deterministic output:\nfirst  %+v\nsecond %+v", a, b)
	}
}

// ---- sort invariant comprehensive check ------------------------------------

func TestSortEvents_invariants(t *testing.T) {
	// Build an unsorted batch and verify the output satisfies all sort rules.
	evs := []ScheduledEvent{
		{SampleOffset: 500, Note: 64, On: true},
		{SampleOffset: 100, Note: 60, On: false},
		{SampleOffset: 100, Note: 60, On: true},
		{SampleOffset: 100, Note: 36, On: true},
		{SampleOffset: 0, Note: 60, On: true},
		{SampleOffset: 500, Note: 60, On: false},
		{SampleOffset: 500, Note: 60, On: true},
	}
	sortEvents(evs)

	// Rule 1: SampleOffset non-decreasing.
	for i := 1; i < len(evs); i++ {
		if evs[i].SampleOffset < evs[i-1].SampleOffset {
			t.Errorf("rule1 violation: events[%d].SampleOffset %d < events[%d].SampleOffset %d",
				i, evs[i].SampleOffset, i-1, evs[i-1].SampleOffset)
		}
	}

	// Rule 2: within same SampleOffset, Note non-decreasing.
	for i := 1; i < len(evs); i++ {
		if evs[i].SampleOffset == evs[i-1].SampleOffset && evs[i].Note < evs[i-1].Note {
			t.Errorf("rule2 violation: events[%d].Note %d < events[%d].Note %d at offset %d",
				i, evs[i].Note, i-1, evs[i-1].Note, evs[i].SampleOffset)
		}
	}

	// Rule 3: within same (SampleOffset, Note), Off sorts before On.
	for i := 1; i < len(evs); i++ {
		a, b := evs[i-1], evs[i]
		if a.SampleOffset == b.SampleOffset && a.Note == b.Note && a.On && !b.On {
			t.Errorf("rule3 violation: events[%d] note-on before events[%d] note-off at (offset=%d, note=%d)",
				i-1, i, a.SampleOffset, a.Note)
		}
	}
}
