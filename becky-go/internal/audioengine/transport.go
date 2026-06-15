package audioengine

import (
	"errors"
	"fmt"
	"math"
)

// transport.go is the pure-Go timekeeper (SPEC §2.2): it owns play/stop state,
// tempo (bpm), resolution (ppq), sample rate, and the playhead position. The
// load-bearing job is the tick<->sample conversion (SPEC §2.3) that the native
// Phase-2 scheduler relies on: tick->sample is computed ONCE here, in Go, so the
// C audio callback never does tempo math under the deadline.
//
// The math is deterministic at a fixed (bpm, ppq, sampleRate): same inputs ->
// same sample offsets, honoring becky's determinism invariant.

// PPQDefault is the pulses-per-quarter-note resolution shared with becky-compose
// (SPEC §2.1) so SMF stems load with no resolution mismatch.
const PPQDefault = 480

// PlayState is the transport's run state.
type PlayState string

const (
	// StateStopped means the playhead is parked and not advancing.
	StateStopped PlayState = "stopped"
	// StatePlaying means the playhead advances with each rendered block.
	StatePlaying PlayState = "playing"
)

// ErrInvalidTransport is returned by NewTransport when a parameter is out of
// range. It is a typed error (degrade-never-crash) so callers report a clear
// message instead of dividing by zero downstream (CLAUDE.md §2).
var ErrInvalidTransport = errors.New("invalid transport parameters")

// Transport is the Go-side clock. It is NOT run on the audio thread; it produces
// sample-stamped events ahead of the playhead (SPEC §2.2). positionTicks is the
// current playhead in ticks; state is play/stop.
type Transport struct {
	bpm           float64
	ppq           int
	sampleRate    int
	positionTicks float64
	state         PlayState
}

// NewTransport builds a validated transport. bpm must be > 0, ppq > 0,
// sampleRate > 0; otherwise ErrInvalidTransport is returned (wrapped with the
// offending values) and no Transport is produced.
func NewTransport(bpm float64, ppq, sampleRate int) (*Transport, error) {
	if !(bpm > 0) || ppq <= 0 || sampleRate <= 0 {
		return nil, fmt.Errorf("%w: bpm=%v ppq=%d sampleRate=%d", ErrInvalidTransport, bpm, ppq, sampleRate)
	}
	return &Transport{
		bpm:        bpm,
		ppq:        ppq,
		sampleRate: sampleRate,
		state:      StateStopped,
	}, nil
}

// BPM returns the tempo in beats per minute.
func (t *Transport) BPM() float64 { return t.bpm }

// PPQ returns the pulses-per-quarter-note resolution.
func (t *Transport) PPQ() int { return t.ppq }

// SampleRate returns the sample rate in Hz.
func (t *Transport) SampleRate() int { return t.sampleRate }

// State returns the current play/stop state.
func (t *Transport) State() PlayState { return t.state }

// PositionTicks returns the current playhead position in ticks.
func (t *Transport) PositionTicks() float64 { return t.positionTicks }

// Play sets the transport playing. Idempotent.
func (t *Transport) Play() { t.state = StatePlaying }

// Stop sets the transport stopped (playhead retained). Idempotent.
func (t *Transport) Stop() { t.state = StateStopped }

// Rewind parks the playhead at tick 0 and stops.
func (t *Transport) Rewind() {
	t.positionTicks = 0
	t.state = StateStopped
}

// SamplesPerTick is the core identity (SPEC §2.3):
//
//	samplesPerTick = (60 / bpm) * sampleRate / ppq
//
// i.e. seconds-per-beat * frames-per-second / ticks-per-beat. At a fixed tempo
// this is a single constant the scheduler multiplies by a tick to get a frame.
func (t *Transport) SamplesPerTick() float64 {
	secondsPerBeat := 60.0 / t.bpm
	return secondsPerBeat * float64(t.sampleRate) / float64(t.ppq)
}

// TickToSample converts an absolute tick to an absolute sample frame, rounding to
// the nearest whole frame so an event lands on a real sample (SPEC §2.3). This is
// the conversion the transport does ONCE in Go before pushing an event to the
// native MIDI ring.
func (t *Transport) TickToSample(tick float64) int64 {
	return int64(math.Round(tick * t.SamplesPerTick()))
}

// SampleToTick is the inverse of TickToSample: it maps an absolute sample frame
// back to a (fractional) tick. Used to place the playhead from a sample position
// reported by the native meter ring.
func (t *Transport) SampleToTick(sample int64) float64 {
	spt := t.SamplesPerTick()
	if spt == 0 {
		return 0
	}
	return float64(sample) / spt
}

// AdvanceSamples moves the playhead forward by n sample frames when playing,
// converting frames to ticks via the current tempo. A non-positive n or a stopped
// transport is a no-op (degrade, not error). Returns the new tick position.
func (t *Transport) AdvanceSamples(n int64) float64 {
	if n <= 0 || t.state != StatePlaying {
		return t.positionTicks
	}
	t.positionTicks += t.SampleToTick(n)
	return t.positionTicks
}

// SeekTicks parks the playhead at an absolute tick. Negative ticks clamp to 0
// (degrade, not error) so a bad seek can never produce a negative playhead.
func (t *Transport) SeekTicks(tick float64) {
	if tick < 0 {
		tick = 0
	}
	t.positionTicks = tick
}
