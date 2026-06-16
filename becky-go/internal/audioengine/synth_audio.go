//go:build audio

package audioengine

// synth_audio.go — cgo play path for RenderSchedule output.
//
// Compiled ONLY under `-tags audio`. Converts a []ScheduledEvent schedule into
// audible sound through the existing miniaudio backend by:
//
//  1. Calling RenderSchedule (pure Go, no build tag) to produce a float32 buffer.
//  2. Encoding the buffer as an IEEE-float32 WAV into a temporary file.
//  3. Calling MiniaudioBackend.PlayWAV (existing becky_play_wav C path) to play.
//
// The audio callback that drains the WAV (native_bridge.c::playbackCallback) is
// PURE C and never enters the Go runtime, preserving the threading discipline
// from host.go: Go never runs on the real-time audio thread.
//
// The temporary WAV is removed after playback. If removal fails the file is left
// in os.TempDir — not a crash, just transient disk noise (degrade-never-crash).

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"

	"becky-go/internal/dawmodel"
)

// PlayScheduleAudio renders a []ScheduledEvent to audio (sine synth, no drum kit)
// and plays it through the chosen output device (nil → OS default). Blocks until
// playback completes. sampleRate ≤ 0 falls back to 48000.
func PlayScheduleAudio(events []ScheduledEvent, output *Device, sampleRate int) error {
	if sampleRate <= 0 {
		sampleRate = 48000
	}
	if len(events) == 0 {
		return fmt.Errorf("synth: no events to play (empty schedule)")
	}
	numSamples := DurationSamples(events, sampleRate)
	if numSamples <= 0 {
		return fmt.Errorf("synth: DurationSamples returned 0")
	}
	buf := RenderSchedule(events, sampleRate, numSamples)
	if buf == nil {
		return fmt.Errorf("synth: RenderSchedule returned nil")
	}
	return playBuffer(buf, output, sampleRate)
}

// playBuffer encodes a rendered float32 buffer to a temporary IEEE-float32 WAV and
// plays it through becky_play_wav (pure-C audio callback — Go never runs on the
// audio thread). The temp WAV is removed after playback (degrade-never-crash).
func playBuffer(buf []float32, output *Device, sampleRate int) error {
	if len(buf) == 0 {
		return fmt.Errorf("synth: empty render buffer")
	}
	tmp, err := os.CreateTemp("", "becky-synth-*.wav")
	if err != nil {
		return fmt.Errorf("synth: cannot create temp WAV: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if err := writeFloat32WAV(tmp, buf, sampleRate, 1); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("synth: WAV encode failed: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("synth: WAV file close failed: %w", err)
	}

	backend := NewMiniaudioBackend(sampleRate, 1)
	// Enumerate to populate idIndex; on failure fall back to the OS default device.
	if _, enumErr := backend.Enumerate(); enumErr != nil {
		output = nil
	}
	if err := backend.PlayWAV(output, tmpPath); err != nil {
		return fmt.Errorf("synth: playback failed: %w", err)
	}
	return nil
}

// PlayPatternAudio sequences a dawmodel.Arrangement, renders it with Jordan's drum
// kit (real one-shot samples for channel-9 notes; sine fallback when the kit is
// absent), and plays it. loops ≥ 2 renders exactly ONE 4/4 bar and tiles it `loops`
// times for a seamless continuous loop; loops ≤ 1 plays the pattern once with a
// release tail. Entry point for `becky-daw-engine --play-pattern-audio [--loops N]`.
func PlayPatternAudio(arr *dawmodel.Arrangement, output *Device, sampleRate, loops int) error {
	if arr == nil {
		return fmt.Errorf("synth: nil arrangement")
	}
	if sampleRate <= 0 {
		sampleRate = 48000
	}
	if loops < 1 {
		loops = 1
	}
	bpm := arr.BPM
	if bpm <= 0 {
		bpm = 120
	}
	ppq := arr.PPQ
	if ppq <= 0 {
		ppq = PPQDefault
	}

	tr, err := NewTransport(float64(bpm), ppq, sampleRate)
	if err != nil {
		return fmt.Errorf("synth: bad transport: %w", err)
	}

	var allNotes []dawmodel.Note
	for _, track := range arr.Tracks {
		if track.Kind != dawmodel.KindMIDI {
			continue
		}
		for _, clip := range track.Clips {
			allNotes = append(allNotes, clip.Notes...)
		}
	}

	evs, err := SequenceNotes(allNotes, tr)
	if err != nil {
		return fmt.Errorf("synth: sequencing failed: %w", err)
	}
	if len(evs) == 0 {
		return fmt.Errorf("synth: no MIDI notes found in arrangement (degrade)")
	}

	kit := LoadDefaultDrumKit(sampleRate) // real drum samples when present; nil → sine

	var buf []float32
	if loops <= 1 {
		buf = RenderScheduleWithKit(evs, sampleRate, DurationSamples(evs, sampleRate), kit)
	} else {
		// Seamless loop: render exactly one 4/4 bar, then tile it `loops` times.
		loopLen := tr.TickToSample(float64(4 * ppq))
		if loopLen <= 0 {
			loopLen = DurationSamples(evs, sampleRate)
		}
		bar := RenderScheduleWithKit(evs, sampleRate, loopLen, kit)
		buf = make([]float32, 0, int(loopLen)*loops)
		for i := 0; i < loops; i++ {
			buf = append(buf, bar...)
		}
	}
	return playBuffer(buf, output, sampleRate)
}

// writeFloat32WAV writes a mono IEEE-float32 WAV (WAVE_FORMAT_IEEE_FLOAT = 0x0003)
// into w. Layout (all little-endian):
//
//	RIFF header (12 B): "RIFF", riffSize, "WAVE"
//	fmt  chunk  (26 B): "fmt ", 18, tag=3, channels, sampleRate, byteRate,
//	                    blockAlign, bitsPerSample=32, cbSize=0
//	data chunk  (8+N B): "data", dataSize, raw float32 samples
//
// miniaudio's decoder reads this format natively with zero conversion overhead.
func writeFloat32WAV(w *os.File, samples []float32, sampleRate, channels int) error {
	const (
		wFormatIEEEFloat uint16 = 3
		bitsPerSample    uint16 = 32
		fmtBodySize      uint32 = 18 // 16 (PCM fields) + 2 (cbSize for IEEE float)
	)
	ch := uint16(channels)
	sr := uint32(sampleRate)
	blockAlign := ch * (bitsPerSample / 8) // bytes per interleaved frame
	byteRate := sr * uint32(blockAlign)
	dataSize := uint32(len(samples)) * uint32(blockAlign)
	// Total RIFF data = "WAVE"(4) + "fmt "(4) + fmtSize(4) + fmtBody(18) + "data"(4) + dataLen(4) + data
	riffSize := 4 + 8 + fmtBodySize + 8 + dataSize

	le := binary.LittleEndian
	write4 := func(tag string) error { _, err := w.Write([]byte(tag)); return err }
	writeU16 := func(v uint16) error { var b [2]byte; le.PutUint16(b[:], v); _, err := w.Write(b[:]); return err }
	writeU32 := func(v uint32) error { var b [4]byte; le.PutUint32(b[:], v); _, err := w.Write(b[:]); return err }

	// RIFF header.
	for _, fn := range []func() error{
		func() error { return write4("RIFF") },
		func() error { return writeU32(riffSize) },
		func() error { return write4("WAVE") },
		// fmt chunk.
		func() error { return write4("fmt ") },
		func() error { return writeU32(fmtBodySize) },
		func() error { return writeU16(wFormatIEEEFloat) },
		func() error { return writeU16(ch) },
		func() error { return writeU32(sr) },
		func() error { return writeU32(byteRate) },
		func() error { return writeU16(blockAlign) },
		func() error { return writeU16(bitsPerSample) },
		func() error { return writeU16(0) }, // cbSize = 0
		// data chunk tag + size.
		func() error { return write4("data") },
		func() error { return writeU32(dataSize) },
	} {
		if err := fn(); err != nil {
			return err
		}
	}

	// Sample data: encode all float32 values to little-endian bytes in one pass.
	databuf := make([]byte, len(samples)*4)
	for i, s := range samples {
		bits := math.Float32bits(s)
		databuf[i*4+0] = byte(bits)
		databuf[i*4+1] = byte(bits >> 8)
		databuf[i*4+2] = byte(bits >> 16)
		databuf[i*4+3] = byte(bits >> 24)
	}
	_, err := w.Write(databuf)
	return err
}
