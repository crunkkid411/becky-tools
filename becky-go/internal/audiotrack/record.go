//go:build !audio

package audiotrack

// record.go (default build) — the mic-capture boundary.
//
// Recording from the microphone is the audio/cgo/hardware boundary of Becky Canvas:
// it needs a real input device, a real-time capture callback, and the miniaudio/
// WASAPI backend in internal/audioengine, which is compiled ONLY under `-tags audio`
// (cgo + the mingw CC). The default pure-Go build that `go build ./...` and CI use
// therefore CANNOT capture audio, and this file is the HONEST stub for it: it returns
// a clear, typed "needs native audio backend" error instead of fabricating samples.
//
// The real implementation lives in record_audio.go (`//go:build audio`), which wires
// RecordToWAV to audioengine.MiniaudioBackend.RecordWAV. So the SAME call records for
// real when the engine is built with `-tags audio`, and degrades with a plain-language
// error otherwise — never a panic, never fake audio (CLAUDE.md HONESTY MANDATE +
// degrade-never-crash).

import "errors"

// ErrRecordingUnavailable is returned by RecordToWAV in the default (non-audio) build.
// It signals that mic capture requires the native audio backend (build with
// `-tags audio`), distinct from a genuine device/IO failure.
var ErrRecordingUnavailable = errors.New(
	"audiotrack: mic recording needs the native audio backend — rebuild with `-tags audio` " +
		"(internal/audioengine cgo/miniaudio path); the default pure-Go build cannot capture audio")

// RecordToWAV records `seconds` of audio from the OS default (or interface) input
// device and writes a WAV at path, then returns a Clip pointing at it. In the default
// build it does nothing but return ErrRecordingUnavailable — it NEVER writes a file or
// fabricates samples. The `-tags audio` build (record_audio.go) performs the real
// capture via internal/audioengine.
//
// sampleRate<=0 and channels<=0 mean "let the backend choose" (48 kHz / mono).
func RecordToWAV(path string, seconds float64, sampleRate, channels int) (*Clip, error) {
	return nil, ErrRecordingUnavailable
}
