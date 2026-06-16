package audioengine

import (
	"math"
	"testing"
)

// TestMIDIToHz verifies the equal-temperament formula for well-known reference
// pitches. A4 = MIDI 69 = 440 Hz is the anchor; the others follow 2^(n/12).
func TestMIDIToHz(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		note int
		want float64 // Hz; accepted to 0.01% tolerance
	}{
		{"A4 anchor", 69, 440.0},
		{"C4 middle C", 60, 261.626},
		{"A3 one octave below A4", 57, 220.0},
		{"A5 one octave above A4", 81, 880.0},
		{"A0 lowest piano key", 21, 27.5},
		{"C8 highest piano key", 108, 4186.009},
		{"MIDI 0", 0, 8.1758},
		{"MIDI 127", 127, 12543.854},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := MIDIToHz(tc.note)
			diff := math.Abs(got-tc.want) / tc.want
			if diff > 0.0001 {
				t.Errorf("MIDIToHz(%d) = %.4f Hz, want %.4f Hz (%.4f%% off)",
					tc.note, got, tc.want, diff*100)
			}
		})
	}
}

// TestRenderScheduleEmpty verifies that an empty event list produces a zeroed
// buffer of the requested length — silence, degrade path, not an error.
func TestRenderScheduleEmpty(t *testing.T) {
	t.Parallel()
	const sr, n = 44100, 512
	buf := RenderSchedule(nil, sr, n)
	if len(buf) != n {
		t.Fatalf("len(buf) = %d, want %d", len(buf), n)
	}
	for i, s := range buf {
		if s != 0 {
			t.Errorf("buf[%d] = %v, want 0 (silence for empty schedule)", i, s)
			break
		}
	}
}

// TestRenderScheduleInvalidParams verifies that invalid sampleRate or numSamples
// returns nil without panicking (degrade-never-crash invariant).
func TestRenderScheduleInvalidParams(t *testing.T) {
	t.Parallel()
	ev := []ScheduledEvent{{SampleOffset: 0, Note: 69, On: true, Velocity: 100}}
	tests := []struct {
		name       string
		sampleRate int
		numSamples int64
	}{
		{"zero samples", 44100, 0},
		{"negative samples", 44100, -1},
		{"zero rate", 0, 512},
		{"negative rate", -1, 512},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := RenderSchedule(ev, tc.sampleRate, tc.numSamples)
			if got != nil {
				t.Errorf("want nil for invalid params, got len=%d", len(got))
			}
		})
	}
}

// TestRenderScheduleNoteOnProducesSound verifies that a note-on at SampleOffset N
// produces silent samples before N and nonzero samples after N+attack.
func TestRenderScheduleNoteOnProducesSound(t *testing.T) {
	t.Parallel()
	const sr = 48000
	const onsetOffset = 100

	events := []ScheduledEvent{
		{SampleOffset: onsetOffset, Note: 69, On: true, Velocity: 100, Channel: 0},
	}
	buf := RenderSchedule(events, sr, 2000)
	if buf == nil {
		t.Fatal("RenderSchedule returned nil")
	}

	// All samples before the onset must be zero.
	for i := 0; i < onsetOffset; i++ {
		if buf[i] != 0 {
			t.Errorf("buf[%d] = %v before onset %d, want 0", i, buf[i], onsetOffset)
		}
	}

	// Well after the attack ramp the sine should be nonzero.
	atkLen := attackSamples(sr)
	checkAt := onsetOffset + atkLen + 10
	if checkAt >= 2000 {
		t.Skip("buffer too short for this check")
	}
	anyNonZero := false
	for i := checkAt; i < 2000; i++ {
		if buf[i] != 0 {
			anyNonZero = true
			break
		}
	}
	if !anyNonZero {
		t.Error("all samples after onset+attack are zero — expected nonzero audio")
	}
}

// TestRenderScheduleFrequency440Hz checks that a 440 Hz note produces
// approximately 440 zero-crossings per second (coarse spectral verification
// without requiring an FFT).
func TestRenderScheduleFrequency440Hz(t *testing.T) {
	t.Parallel()
	const sr = 48000
	const totalSamples = sr * 2 // 2 seconds

	events := []ScheduledEvent{
		{SampleOffset: 0, Note: 69, On: true, Velocity: 100, Channel: 0},
	}
	buf := RenderSchedule(events, sr, totalSamples)
	if buf == nil {
		t.Fatal("RenderSchedule returned nil")
	}

	// Skip the attack ramp so we count crossings on the steady-state sine.
	atkLen := attackSamples(sr) + 10

	// Count pos→neg zero-crossings in the sustain window.
	crossings := 0
	for i := atkLen; i < totalSamples-1; i++ {
		if buf[i] >= 0 && buf[i+1] < 0 {
			crossings++
		}
	}

	sustainSecs := float64(totalSamples-atkLen) / float64(sr)
	expected := int(math.Round(440.0 * sustainSecs))
	tol := int(math.Round(float64(expected) * 0.05)) // ±5%
	if crossings < expected-tol || crossings > expected+tol {
		t.Errorf("zero-crossing count = %d, expected %d±%d for 440 Hz",
			crossings, expected, tol)
	}
}

// TestRenderScheduleDeterminism verifies that calling RenderSchedule twice
// with identical inputs produces bit-identical output.
func TestRenderScheduleDeterminism(t *testing.T) {
	t.Parallel()
	events := []ScheduledEvent{
		{SampleOffset: 0, Note: 60, On: true, Velocity: 80, Channel: 0},
		{SampleOffset: 100, Note: 64, On: true, Velocity: 90, Channel: 0},
		{SampleOffset: 200, Note: 67, On: true, Velocity: 70, Channel: 0},
		{SampleOffset: 400, Note: 60, On: false, Velocity: 0, Channel: 0},
	}
	buf1 := RenderSchedule(events, 44100, 2000)
	buf2 := RenderSchedule(events, 44100, 2000)
	if len(buf1) != len(buf2) {
		t.Fatalf("lengths differ: %d vs %d", len(buf1), len(buf2))
	}
	for i := range buf1 {
		if buf1[i] != buf2[i] {
			t.Errorf("buf[%d]: run1=%v run2=%v (output not deterministic)", i, buf1[i], buf2[i])
			break
		}
	}
}

// TestRenderScheduleOutputRange verifies the tanh soft-limiter keeps all
// samples within (-1, +1) even when MaxVoices simultaneous voices are sounding.
func TestRenderScheduleOutputRange(t *testing.T) {
	t.Parallel()
	const sr = 44100

	// Fire MaxVoices simultaneous note-ons at sample 0.
	events := make([]ScheduledEvent, MaxVoices)
	for i := range events {
		events[i] = ScheduledEvent{
			SampleOffset: 0,
			Note:         60 + i%12,
			On:           true,
			Velocity:     127,
			Channel:      0,
		}
	}
	buf := RenderSchedule(events, sr, 512)
	if buf == nil {
		t.Fatal("RenderSchedule returned nil")
	}
	for i, s := range buf {
		if s >= 1.0 || s <= -1.0 {
			t.Errorf("buf[%d] = %v is outside (-1,+1) — soft limiter broken", i, s)
			return
		}
	}
}

// TestRenderSchedulePercussionIgnoresNoteOff verifies that a channel-9 voice
// keeps sounding after a note-off (GM convention: drums ignore note-off).
func TestRenderSchedulePercussionIgnoresNoteOff(t *testing.T) {
	t.Parallel()
	const sr = 48000
	events := []ScheduledEvent{
		{SampleOffset: 0, Note: 36, On: true, Velocity: 100, Channel: 9},
		{SampleOffset: 10, Note: 36, On: false, Velocity: 0, Channel: 9},
	}
	buf := RenderSchedule(events, sr, 500)
	if buf == nil {
		t.Fatal("RenderSchedule returned nil")
	}
	// The kick decay lasts ~60 ms ≈ 2880 samples at 48 kHz.
	// Samples well past the note-off (sample 10) should still be nonzero.
	anyNonZero := false
	for i := 50; i < 200; i++ {
		if buf[i] != 0 {
			anyNonZero = true
			break
		}
	}
	if !anyNonZero {
		t.Error("percussion voice silent after note-off — should decay for ~60 ms")
	}
}

// TestDurationSamples verifies the 1-second tail calculation.
func TestDurationSamples(t *testing.T) {
	t.Parallel()
	const sr = 48000
	events := []ScheduledEvent{
		{SampleOffset: 0, Note: 60, On: true},
		{SampleOffset: 1000, Note: 60, On: false},
	}
	got := DurationSamples(events, sr)
	want := int64(1000) + int64(sr) // last offset + 1 s tail
	if got != want {
		t.Errorf("DurationSamples = %d, want %d", got, want)
	}

	if n := DurationSamples(nil, sr); n != 0 {
		t.Errorf("DurationSamples(nil, sr) = %d, want 0", n)
	}
	if n := DurationSamples(events, 0); n != 0 {
		t.Errorf("DurationSamples(events, 0) = %d, want 0", n)
	}
}
