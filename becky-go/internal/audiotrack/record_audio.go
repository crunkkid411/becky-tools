//go:build audio

package audiotrack

// record_audio.go (`-tags audio` build) — the REAL mic-capture path.
//
// Compiled ONLY under `-tags audio`, where internal/audioengine's cgo/miniaudio
// backend is available. It wires RecordToWAV to the existing, hardware-tested
// audioengine.MiniaudioBackend.RecordWAV (real WASAPI capture-to-file) rather than
// duplicating any capture code — exactly the "reuse an existing capture path" the
// task calls for. After capture it re-imports the written WAV via ImportWAV so the
// caller gets a Clip ready to place on a track.
//
// Device choice reuses audioengine's documented device-default rule (SelectDefaults):
// prefer Jordan's pro interface (e.g. the Steinberg UR12) for input when present,
// else the OS default. A nil input degrades to the OS default inside the backend.
// Every failure is a wrapped Go error — never a panic (degrade-never-crash).

import (
	"fmt"

	"becky-go/internal/audioengine"
)

// RecordToWAV records `seconds` of audio from the preferred input device and writes a
// WAV at path via the native miniaudio backend, then re-imports it as a Clip. This is
// the real capture used when the engine is built with `-tags audio`. sampleRate<=0 /
// channels<=0 let the backend pick (48 kHz / mono).
func RecordToWAV(path string, seconds float64, sampleRate, channels int) (*Clip, error) {
	if path == "" {
		return nil, fmt.Errorf("audiotrack: record needs an output .wav path")
	}
	if seconds <= 0 {
		return nil, fmt.Errorf("audiotrack: record needs a positive duration (got %g s)", seconds)
	}
	backend := audioengine.NewMiniaudioBackend(sampleRate, channels)

	// Pick the preferred input device via the engine's device-default rule. If
	// enumeration fails we fall back to the OS default (nil input) rather than erroring.
	var input *audioengine.Device
	if devices, err := backend.Enumerate(); err == nil {
		if sel := audioengine.SelectDefaults(devices); sel.Input != nil {
			input = sel.Input
		}
	}

	if err := backend.RecordWAV(input, path, seconds, sampleRate, channels); err != nil {
		return nil, fmt.Errorf("audiotrack: record to %q failed: %w", path, err)
	}
	clip, err := ImportWAV(path)
	if err != nil {
		return nil, fmt.Errorf("audiotrack: recorded %q but could not re-import it: %w", path, err)
	}
	return clip, nil
}
