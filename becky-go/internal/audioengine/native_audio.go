//go:build audio

// native_audio.go is the REAL audio backend: a cgo bridge to miniaudio (WASAPI on
// Windows) that satisfies the same AudioBackend / DeviceEnumerator contracts the
// pure-Go StubBackend does. It is built ONLY under `-tags audio` so the default
// `go build ./...` stays pure-Go and green on model-free / GPU-free CI (CLAUDE.md
// §3). When this file is in the build, MiniaudioBackend is available alongside the
// stubs; nothing here changes the default build.
//
// Threading discipline (SPEC §1.1, §6.2): the realtime audio callback lives in
// native_bridge.c as PURE C and never enters the Go runtime, so a GC pause can
// never land in the audio deadline. Go only calls the blocking control-plane C
// functions below, each on a normal goroutine. cgo does NOT run Go on the audio
// thread here.
//
// Degrade-never-crash (CLAUDE.md §2): every C call returns a miniaudio result
// code; a non-zero code becomes a typed Go error with a plain-language message —
// never a panic or segfault.
package audioengine

/*
#cgo CFLAGS: -I${SRCDIR}
#cgo windows LDFLAGS: -lole32 -lwinmm
#cgo linux LDFLAGS: -ldl -lm -lpthread
#include <stdlib.h>
#include "native_bridge.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"strings"
	"unsafe"

	"becky-go/internal/pathx"
)

// maxEnumDevices caps how many endpoints we read out of the C enumerator in one
// call. Generous for a real machine; bounds the fixed C array.
const maxEnumDevices = 64

// ErrAudioBackend wraps every native audio failure so callers can detect "the
// real backend errored" distinctly from ErrNativeUnavailable (the stub's signal).
var ErrAudioBackend = errors.New("miniaudio backend error")

// MiniaudioBackend is the cgo-backed AudioBackend. It enumerates real WASAPI
// devices, opens a running output stream for Start/Stop, and (via RecordWAV /
// PlayWAV) does real capture-to-file and file playback. It holds no Go state on
// the audio thread — all realtime work is in native_bridge.c.
//
// SampleRate / Channels are the requested format for Start's running stream and
// for RecordWAV's default; zero values fall back to 48000 / sensible defaults in
// the C layer.
type MiniaudioBackend struct {
	// SampleRate requested for the engine output stream (0 -> 48000).
	SampleRate int
	// Channels requested for the engine output stream (0 -> 2 for playback).
	Channels int
	// idIndex maps the most recent Enumerate's Device.ID back to the C-side
	// device-id table index, so Start/Record/Play can open a specific device.
	idIndex map[string]int
}

// NewMiniaudioBackend builds a backend with the given requested output format.
// Pass 0s to accept the C-side defaults (48000 Hz, stereo out).
func NewMiniaudioBackend(sampleRate, channels int) *MiniaudioBackend {
	return &MiniaudioBackend{
		SampleRate: sampleRate,
		Channels:   channels,
		idIndex:    map[string]int{},
	}
}

// resultError turns a miniaudio result code (0 == success) into a typed Go error
// with a plain-language prefix, or nil on success. This is the single degrade
// funnel so no C failure ever becomes a panic.
func resultError(action string, code C.int) error {
	if code == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s failed (miniaudio code %d)", ErrAudioBackend, action, int(code))
}

// builtinHints are substrings that mark a device as a built-in / OS endpoint
// rather than Jordan's pro interface. The IsInterface heuristic (below) is the
// inverse: a device is treated as the pro interface when its name does NOT match
// any of these AND it is not the OS default. This feeds the existing
// SelectDefaults "prefer the interface" rule without any extra config.
var builtinHints = []string{
	"speakers", "realtek", "built-in", "internal", "high definition audio",
	"microphone array", "stereo mix", "headphones", "communications",
}

// looksLikeInterface applies the documented IsInterface heuristic:
//   - NOT the OS default endpoint (the default is almost always the built-in),
//     AND
//   - the device name contains none of the built-in hint substrings.
//
// Rationale: a discrete audio interface (Focusrite, UAD, MOTU, etc.) is a
// non-default endpoint whose name is a brand/product, not "Speakers (Realtek)".
// This is a heuristic, not ground truth — it only needs to be right often enough
// to make SelectDefaults prefer the interface; the user can override later.
func looksLikeInterface(name string, isDefault bool) bool {
	if isDefault {
		return false
	}
	low := strings.ToLower(name)
	for _, hint := range builtinHints {
		if strings.Contains(low, hint) {
			return false
		}
	}
	return true
}

// Enumerate queries real WASAPI endpoints and maps them to the existing Device
// struct, setting IsInterface via looksLikeInterface. It also records the C-side
// idIndex per Device.ID so a later Start/Record/Play can open that exact device.
// On any native failure it returns a typed error and an empty list (degrade).
func (b *MiniaudioBackend) Enumerate() ([]Device, error) {
	var cArr [maxEnumDevices]C.becky_device_info
	var count C.int
	code := C.becky_enumerate(&cArr[0], C.int(maxEnumDevices), &count)
	if err := resultError("device enumeration", code); err != nil {
		return nil, err
	}
	defer C.becky_enumerate_free()

	n := int(count)
	devices := make([]Device, 0, n)
	if b.idIndex == nil {
		b.idIndex = map[string]int{}
	}
	for i := 0; i < n; i++ {
		ci := cArr[i]
		name := C.GoString(&ci.name[0])
		kind := KindOutput
		if ci.isCapture != 0 {
			kind = KindInput
		}
		isDefault := ci.isDefault != 0
		id := deviceID(name, kind, int(ci.idIndex))
		devices = append(devices, Device{
			ID:          id,
			Name:        name,
			Kind:        kind,
			IsInterface: looksLikeInterface(name, isDefault),
			IsDefault:   isDefault,
			Channels:    int(ci.channels),
			SampleRate:  int(ci.sampleRate),
		})
		b.idIndex[id] = int(ci.idIndex)
	}
	return devices, nil
}

// deviceID builds a stable, readable Device.ID from the endpoint name, kind, and
// the C id-table index. pathx.Base keeps it separator-agnostic in case a backend
// ever reports a path-like name (CLAUDE.md §2 path invariant).
func deviceID(name string, kind DeviceKind, idIndex int) string {
	base := pathx.Base(name)
	return fmt.Sprintf("%s:%d:%s", kind, idIndex, base)
}

// idIndexFor returns the C device-id table index for a chosen Device, or -1 to
// mean "use the OS default" when the device is nil or unknown (degrade path).
func (b *MiniaudioBackend) idIndexFor(d *Device) C.int {
	if d == nil {
		return C.int(-1)
	}
	if idx, ok := b.idIndex[d.ID]; ok {
		return C.int(idx)
	}
	return C.int(-1)
}

// Start opens the selected output device and begins a real, continuously-running
// stream (silence today; the realtime mixer fills it later — SPEC §4). It is the
// honest AudioBackend.Start: a real device open with a live C callback. A nil
// Output degrades to the OS default device.
func (b *MiniaudioBackend) Start(sel Selection) error {
	code := C.becky_stream_start(b.idIndexFor(sel.Output),
		C.int(b.SampleRate), C.int(b.Channels))
	return resultError("output stream start", code)
}

// Stop closes the running output stream cleanly. Safe to call when already
// stopped (the C layer no-ops).
func (b *MiniaudioBackend) Stop() error {
	C.becky_stream_stop()
	return nil
}

// RecordWAV records from the chosen input device (or the OS default when input is
// nil) for `seconds` and writes a float32 WAV to path. sampleRate/channels<=0
// fall back to 48000 / mono in the C layer. Blocks until the recording is done.
// Returns a typed error on any native failure (degrade-never-crash).
func (b *MiniaudioBackend) RecordWAV(input *Device, path string, seconds float64, sampleRate, channels int) error {
	if path == "" {
		return fmt.Errorf("%w: record needs an output .wav path", ErrAudioBackend)
	}
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	code := C.becky_record_wav(b.idIndexFor(input), cPath,
		C.double(seconds), C.int(sampleRate), C.int(channels))
	return resultError("record to "+pathx.Base(path), code)
}

// PlayWAV plays a WAV file through the chosen output device (or the OS default
// when output is nil), blocking until the file finishes. Returns a typed error
// on any native failure (missing file, no device, etc.).
func (b *MiniaudioBackend) PlayWAV(output *Device, path string) error {
	if path == "" {
		return fmt.Errorf("%w: play needs an input .wav path", ErrAudioBackend)
	}
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	code := C.becky_play_wav(b.idIndexFor(output), cPath)
	return resultError("play "+pathx.Base(path), code)
}

// Compile-time assertions: the real backend satisfies the same boundary contracts
// the stub does, so the two can never drift (CLAUDE.md §2 — the contract is the
// single source of truth).
var (
	_ AudioBackend     = (*MiniaudioBackend)(nil)
	_ DeviceEnumerator = (*MiniaudioBackend)(nil)
)
