//go:build audio

package audioengine

// machine_audio.go — cgo play path for the 16-pad drummachine.Machine. Compiled
// ONLY under `-tags audio`. It mirrors synth_audio.go exactly: render a float32
// buffer with the pure-Go RenderMachine (no build tag), encode it to a temporary
// IEEE-float32 WAV via writeFloat32WAV, and play it through the existing
// becky_play_wav C path (the audio callback never enters the Go runtime).
//
// Two entry points the canvas GUI execs the engine for:
//   - PlayMachineLoop : ▶ on a pattern — tile one bar-cycle N times seamlessly.
//   - PlayPadOneShot  : a pad click — audition that single pad instantly.

import (
	"fmt"

	"becky-go/internal/drummachine"
)

// PlayMachineLoop renders the Machine's pattern with kit and plays it through
// output (nil → OS default), blocking until done. loops >= 2 renders exactly ONE
// pattern bar-cycle (MachineLoopSamples) and tiles it `loops` times for a seamless
// continuous loop; loops <= 1 plays the pattern once with a 1-second release tail.
// Entry point for `becky-daw-engine --play-machine <machine.json> [--loops N]`.
//
// kit may be nil (full sine fallback). sampleRate <= 0 falls back to 48000.
func PlayMachineLoop(m *drummachine.Machine, kit *MachineKit, output *Device, sampleRate, loops int) error {
	if m == nil {
		return fmt.Errorf("machine: nil machine")
	}
	if sampleRate <= 0 {
		sampleRate = 48000
	}
	if loops < 1 {
		loops = 1
	}

	pat, ok := m.PatternForScene(0)
	if !ok {
		return fmt.Errorf("machine: no playable pattern (degrade)")
	}
	events := SequenceMachinePattern(m, pat, sampleRate)
	if len(events) == 0 {
		return fmt.Errorf("machine: pattern is empty (no audible hits)")
	}

	var buf []float32
	if loops <= 1 {
		buf = RenderMachine(events, sampleRate, MachineDurationSamples(events, sampleRate), kit)
	} else {
		loopLen := MachineLoopSamples(m, pat, sampleRate)
		if loopLen <= 0 {
			loopLen = MachineDurationSamples(events, sampleRate)
		}
		bar := RenderMachine(events, sampleRate, loopLen, kit)
		buf = make([]float32, 0, int(loopLen)*loops)
		for i := 0; i < loops; i++ {
			buf = append(buf, bar...)
		}
	}
	if buf == nil {
		return fmt.Errorf("machine: render returned nil")
	}
	return playBuffer(buf, output, sampleRate)
}

// PlayPadOneShot auditions a single pad once at velocity vel (1..127), through
// output (nil → OS default). It applies that pad's Level/Pitch/Decay just like the
// pattern path, using the loaded sample (or the sine fallback when the pad has
// none). This is the instant audition the GUI fires on a pad click.
//
// padIndex out of range, or a nil machine, is a typed degrade error (never panic).
func PlayPadOneShot(m *drummachine.Machine, kit *MachineKit, output *Device, padIndex, vel, sampleRate int) error {
	if m == nil {
		return fmt.Errorf("machine: nil machine")
	}
	if padIndex < 0 || padIndex >= drummachine.PadCount || padIndex >= len(m.Kit.Pads) {
		return fmt.Errorf("machine: pad index %d out of range", padIndex)
	}
	if sampleRate <= 0 {
		sampleRate = 48000
	}
	if vel <= 0 {
		vel = 100
	}

	p := m.Kit.Pads[padIndex]
	ev := MachineEvent{
		SampleOffset: 0,
		Pad:          padIndex,
		Velocity:     clampVelInt(vel),
		Level:        clampUnit(p.Level),
		Pan:          clampPan(p.Pan),
		PitchSemis:   p.PitchSemitones,
		DecaySec:     maxZero(p.Decay),
		ChokeGroup:   0, // a one-shot audition never chokes anything
		Note:         p.MidiNote,
	}
	events := []MachineEvent{ev}
	buf := RenderMachine(events, sampleRate, MachineDurationSamples(events, sampleRate), kit)
	if buf == nil {
		return fmt.Errorf("machine: pad render returned nil")
	}
	return playBuffer(buf, output, sampleRate)
}
