package audioengine

// synth.go — pure-Go polyphonic synthesiser for pattern playback.
//
// This file has NO build tag: every function here is headless, cgo-free, and
// fully unit-testable on CI (no hardware, no audio library). The cgo play path
// in synth_audio.go (//go:build audio) calls RenderSchedule to produce a
// float32 buffer, encodes it as a temporary WAV, and hands it to the existing
// becky_play_wav path — so the Go runtime never runs on the audio callback thread
// (the threading discipline from host.go is fully preserved).
//
// Voice model (simple, audibly correct):
//
//	note-on  → sine tone at MIDIToHz(note), amplitude = velocity/127.
//	           4 ms linear attack, then sustain at full level until note-off.
//	note-off → 4 ms linear release, then silence.
//	Channel 9 (GM percussion) gets a 60 ms exponential decay regardless of
//	           note-off timing so drum hits sound punchy at any BPM.
//
// Polyphony: up to MaxVoices simultaneous notes. When the cap is reached the
// oldest sounding voice is stolen (deterministic: lowest startAt sample).
//
// Determinism: same []ScheduledEvent + sampleRate + numSamples → bit-identical
// output buffer (no random, no OS state, only fixed-point-equivalent math).

import (
	"math"
)

// MaxVoices is the polyphony cap. 32 covers dense drum+piano patterns without
// glitches; each extra voice costs one sine per sample (cheap on modern CPUs).
const MaxVoices = 32

// MIDIToHz converts a MIDI note number to its fundamental frequency in Hz
// using equal-temperament (A4 = MIDI 69 = 440 Hz). Valid for MIDI 0–127.
//
//	freq = 440 × 2^((note−69)/12)
func MIDIToHz(note int) float64 {
	return 440.0 * math.Pow(2.0, float64(note-69)/12.0)
}

// attackSamples returns the attack ramp length for the given sample rate (~4 ms).
func attackSamples(sampleRate int) int {
	n := int(math.Round(4.0 * float64(sampleRate) / 1000.0))
	if n < 1 {
		n = 1
	}
	return n
}

// releaseSamples returns the release ramp length (~4 ms).
func releaseSamples(sampleRate int) int { return attackSamples(sampleRate) }

// drumDecaySamples returns the percussion decay length (~60 ms).
func drumDecaySamples(sampleRate int) int {
	n := int(math.Round(60.0 * float64(sampleRate) / 1000.0))
	if n < 1 {
		n = 1
	}
	return n
}

// envStage identifies the current ADSR-lite stage of a voice.
type envStage int

const (
	stageAttack  envStage = iota // amplitude rises linearly from 0→1
	stageSustain                 // amplitude held at 1 until note-off
	stageRelease                 // amplitude falls linearly 1→0 after note-off
	stageDecay                   // percussion: amplitude falls 1→0 over decayLen
	stageDone                    // voice is silent; slot can be reused
)

// voice is one active synthesiser voice.
type voice struct {
	note       int      // MIDI note number (0–127)
	ch         int      // MIDI channel (9 = GM percussion)
	phase      float64  // current waveform phase [0, 2π)
	phaseStep  float64  // phase increment per sample = 2π·freq/sampleRate
	amplitude  float64  // peak amplitude = velocity/127
	stage      envStage // current envelope stage
	envSample  int      // samples elapsed in the current stage
	attackLen  int      // attack ramp length in samples
	releaseLen int      // release ramp length in samples
	decayLen   int      // percussion decay length in samples
	startAt    int64    // absolute sample this voice started (voice-stealing key)
}

// tick advances the voice by one sample and returns the PCM value.
// Returns 0 when stageDone. The caller sums all live voice outputs.
func (v *voice) tick() float32 {
	if v.stage == stageDone {
		return 0
	}

	// Compute the envelope multiplier for this sample.
	var env float64
	switch v.stage {
	case stageAttack:
		env = float64(v.envSample) / float64(v.attackLen)
		v.envSample++
		if v.envSample >= v.attackLen {
			v.stage = stageSustain
			v.envSample = 0
		}
	case stageSustain:
		env = 1.0
	case stageRelease:
		env = 1.0 - float64(v.envSample)/float64(v.releaseLen)
		if env < 0 {
			env = 0
		}
		v.envSample++
		if v.envSample >= v.releaseLen {
			v.stage = stageDone
		}
	case stageDecay:
		env = 1.0 - float64(v.envSample)/float64(v.decayLen)
		if env < 0 {
			env = 0
		}
		v.envSample++
		if v.envSample >= v.decayLen {
			v.stage = stageDone
		}
	}

	// Synthesise one sample (sine wave).
	sample := float32(v.amplitude * env * math.Sin(v.phase))

	// Advance waveform phase; wrap to keep float precision.
	v.phase += v.phaseStep
	if v.phase >= 2*math.Pi {
		v.phase -= 2 * math.Pi
	}

	return sample
}

// noteOff triggers the release stage. Percussion voices (ch 9) ignore it.
func (v *voice) noteOff() {
	if v.ch == 9 || v.stage == stageDone {
		return
	}
	v.stage = stageRelease
	v.envSample = 0
}

// polyphony manages the fixed pool of MaxVoices concurrent voices.
type polyphony struct {
	voices     [MaxVoices]voice
	used       [MaxVoices]bool
	sampleRate int
}

func newPolyphony(sampleRate int) *polyphony {
	return &polyphony{sampleRate: sampleRate}
}

// noteOn activates a voice for the given event. When all slots are taken the
// oldest voice (lowest startAt) is stolen — deterministic under any input order.
func (p *polyphony) noteOn(ev ScheduledEvent) {
	// Try a free or done slot first.
	for i := range p.voices {
		if !p.used[i] || p.voices[i].stage == stageDone {
			p.startVoice(i, ev)
			return
		}
	}
	// Steal the oldest voice.
	oldest := 0
	for i := 1; i < MaxVoices; i++ {
		if p.voices[i].startAt < p.voices[oldest].startAt {
			oldest = i
		}
	}
	p.startVoice(oldest, ev)
}

func (p *polyphony) startVoice(i int, ev ScheduledEvent) {
	sr := p.sampleRate
	vel := ev.Velocity
	if vel <= 0 {
		vel = 64
	}
	v := &p.voices[i]
	v.note = ev.Note
	v.ch = ev.Channel
	v.phase = 0
	v.phaseStep = 2 * math.Pi * MIDIToHz(ev.Note) / float64(sr)
	v.amplitude = float64(vel) / 127.0
	v.attackLen = attackSamples(sr)
	v.releaseLen = releaseSamples(sr)
	v.decayLen = drumDecaySamples(sr)
	v.startAt = ev.SampleOffset
	v.envSample = 0
	if ev.Channel == 9 {
		v.stage = stageDecay // percussion: straight to decay, note-off ignored
	} else {
		v.stage = stageAttack
	}
	p.used[i] = true
}

// noteOff releases any matching live voice for the given note+channel.
func (p *polyphony) noteOff(ev ScheduledEvent) {
	for i := range p.voices {
		if !p.used[i] {
			continue
		}
		v := &p.voices[i]
		if v.note == ev.Note && v.ch == ev.Channel && v.stage != stageDone {
			v.noteOff()
		}
	}
}

// tick mixes all active voices into one sample and returns the sum.
func (p *polyphony) tick() float32 {
	var sum float32
	for i := range p.voices {
		if p.used[i] {
			sum += p.voices[i].tick()
		}
	}
	return sum
}

// RenderSchedule renders a sorted []ScheduledEvent into a mono float32 PCM
// buffer of numSamples frames at the given sampleRate.
//
// Preconditions:
//   - events must be sorted by SampleOffset ascending (SequenceDrumGrid and
//     SequenceNotes both guarantee this).
//   - sampleRate > 0, numSamples > 0.
//
// The output is run through a tanh soft-limiter so the result is always in
// (-1, +1) even at full polyphony — no clipping, no distortion.
//
// Returns nil for invalid inputs; returns a zeroed buffer for an empty event
// list (silence). Both are degrade paths, not panics.
func RenderSchedule(events []ScheduledEvent, sampleRate int, numSamples int64) []float32 {
	if numSamples <= 0 || sampleRate <= 0 {
		return nil
	}
	buf := make([]float32, numSamples)
	if len(events) == 0 {
		return buf // silence
	}

	poly := newPolyphony(sampleRate)
	ei := 0 // index into events; we walk it forward monotonically
	for s := int64(0); s < numSamples; s++ {
		// Fire all events whose SampleOffset has been reached.
		for ei < len(events) && events[ei].SampleOffset <= s {
			ev := events[ei]
			ei++
			if ev.On {
				poly.noteOn(ev)
			} else {
				poly.noteOff(ev)
			}
		}
		// Mix all active voices; tanh soft-limit, then clamp to [-0.999, 0.999]
		// so float32 rounding never reaches exactly ±1.0.
		raw := poly.tick()
		limited := math.Tanh(float64(raw))
		if limited > 0.999 {
			limited = 0.999
		} else if limited < -0.999 {
			limited = -0.999
		}
		buf[s] = float32(limited)
	}
	return buf
}

// DurationSamples returns the number of samples needed to hold the full render
// of an event list: the last event's SampleOffset plus a 1-second tail so
// release/decay envelopes have room to decay to silence.
//
// Returns 0 for an empty list or invalid sampleRate (degrade, not an error).
func DurationSamples(events []ScheduledEvent, sampleRate int) int64 {
	if len(events) == 0 || sampleRate <= 0 {
		return 0
	}
	last := events[len(events)-1].SampleOffset
	tail := int64(sampleRate) // 1 s tail
	return last + tail
}
