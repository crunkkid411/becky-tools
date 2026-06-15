package audioengine

import (
	"errors"
	"math"
	"testing"
)

func TestNewTransport_validation(t *testing.T) {
	cases := []struct {
		name       string
		bpm        float64
		ppq        int
		sampleRate int
		wantErr    bool
	}{
		{"valid", 120, 480, 48000, false},
		{"zero bpm", 0, 480, 48000, true},
		{"negative bpm", -120, 480, 48000, true},
		{"NaN bpm", math.NaN(), 480, 48000, true},
		{"zero ppq", 120, 0, 48000, true},
		{"negative ppq", 120, -480, 48000, true},
		{"zero sampleRate", 120, 480, 0, true},
		{"negative sampleRate", 120, 480, -48000, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr, err := NewTransport(c.bpm, c.ppq, c.sampleRate)
			if c.wantErr {
				if err == nil {
					t.Fatal("expected ErrInvalidTransport, got nil")
				}
				if !errors.Is(err, ErrInvalidTransport) {
					t.Errorf("expected ErrInvalidTransport, got %v", err)
				}
				if tr != nil {
					t.Error("expected nil transport on error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tr == nil {
				t.Fatal("expected a transport")
			}
		})
	}
}

func TestTransport_samplesPerTick(t *testing.T) {
	// At 120 bpm, 480 PPQ, 48000 Hz:
	//   secondsPerBeat = 0.5, framesPerBeat = 24000, perTick = 24000/480 = 50.
	tr, err := NewTransport(120, 480, 48000)
	if err != nil {
		t.Fatal(err)
	}
	if got := tr.SamplesPerTick(); got != 50.0 {
		t.Errorf("SamplesPerTick: got %v want 50", got)
	}
}

func TestTransport_tickToSample_vectors(t *testing.T) {
	tr, err := NewTransport(120, 480, 48000) // 50 samples/tick
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		tick float64
		want int64
	}{
		{0, 0},
		{1, 50},
		{480, 24000}, // one beat -> 0.5 s @ 48k
		{960, 48000}, // two beats -> 1.0 s
		{240, 12000}, // half a beat
		{1, 50},      // repeat to confirm purity
	}
	for _, c := range cases {
		if got := tr.TickToSample(c.tick); got != c.want {
			t.Errorf("TickToSample(%v): got %d want %d", c.tick, got, c.want)
		}
	}
}

func TestTransport_tickToSample_rounding(t *testing.T) {
	// 90 bpm, 480 PPQ, 44100 Hz -> samplesPerTick = (60/90)*44100/480 = 61.25.
	// math.Round is half-away-from-zero: tick 1 -> 61.25 -> 61; tick 2 -> 122.5 -> 123.
	tr, err := NewTransport(90, 480, 44100)
	if err != nil {
		t.Fatal(err)
	}
	if got := tr.TickToSample(1); got != 61 {
		t.Errorf("rounding: got %d want 61 (61.25 -> nearest)", got)
	}
	if got := tr.TickToSample(2); got != 123 {
		t.Errorf("rounding: got %d want 123 (122.5 -> half away from zero)", got)
	}
}

func TestTransport_sampleToTick_inverse(t *testing.T) {
	tr, err := NewTransport(120, 480, 48000) // 50 samples/tick
	if err != nil {
		t.Fatal(err)
	}
	for _, sample := range []int64{0, 50, 100, 24000, 48000} {
		tick := tr.SampleToTick(sample)
		back := tr.TickToSample(tick)
		if back != sample {
			t.Errorf("round-trip sample %d -> tick %v -> sample %d", sample, tick, back)
		}
	}
}

func TestTransport_advanceAndState(t *testing.T) {
	tr, err := NewTransport(120, 480, 48000) // 50 samples/tick
	if err != nil {
		t.Fatal(err)
	}
	// Stopped: AdvanceSamples is a no-op.
	if got := tr.AdvanceSamples(48000); got != 0 {
		t.Errorf("advance while stopped: got %v want 0", got)
	}
	if tr.State() != StateStopped {
		t.Errorf("state: got %q want stopped", tr.State())
	}

	tr.Play()
	if tr.State() != StatePlaying {
		t.Errorf("state after Play: got %q want playing", tr.State())
	}
	// 48000 samples at 50/tick = 960 ticks.
	if got := tr.AdvanceSamples(48000); got != 960 {
		t.Errorf("advance: got %v want 960", got)
	}
	// Non-positive advance is a no-op.
	if got := tr.AdvanceSamples(0); got != 960 {
		t.Errorf("advance(0): got %v want 960 (unchanged)", got)
	}
	if got := tr.AdvanceSamples(-100); got != 960 {
		t.Errorf("advance(-100): got %v want 960 (unchanged)", got)
	}

	tr.Stop()
	if got := tr.AdvanceSamples(48000); got != 960 {
		t.Errorf("advance after Stop: got %v want 960 (unchanged)", got)
	}

	tr.Rewind()
	if tr.PositionTicks() != 0 || tr.State() != StateStopped {
		t.Errorf("rewind: pos=%v state=%q want 0/stopped", tr.PositionTicks(), tr.State())
	}
}

func TestTransport_seekClampsNegative(t *testing.T) {
	tr, err := NewTransport(120, 480, 48000)
	if err != nil {
		t.Fatal(err)
	}
	tr.SeekTicks(-500) // degrade, not error
	if tr.PositionTicks() != 0 {
		t.Errorf("negative seek should clamp to 0, got %v", tr.PositionTicks())
	}
	tr.SeekTicks(720)
	if tr.PositionTicks() != 720 {
		t.Errorf("seek: got %v want 720", tr.PositionTicks())
	}
}

func TestTransport_deterministic(t *testing.T) {
	// Same params -> identical conversion across fresh transports.
	mk := func() *Transport {
		tr, err := NewTransport(137.5, PPQDefault, 44100)
		if err != nil {
			t.Fatal(err)
		}
		return tr
	}
	a, b := mk(), mk()
	for _, tick := range []float64{0, 1, 17, 480, 1923, 100000} {
		if a.TickToSample(tick) != b.TickToSample(tick) {
			t.Errorf("non-deterministic TickToSample at tick %v", tick)
		}
	}
}
