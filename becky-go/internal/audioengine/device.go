// Package audioengine is the pure-Go, deterministic FOUNDATION for becky's DAW
// audio engine (SPEC-BECKY-DAW-ENGINE.md). It owns the parts that must compile
// and test on model-free / GPU-free CI: the device model + selection rule, the
// transport/clock with sample<->tick math, and the host-boundary interfaces
// (AudioBackend, PluginHost) that the native Phase-2 cgo layer will implement.
//
// What is DELIBERATELY NOT here: the real-time audio callback, miniaudio/WASAPI,
// the lock-free rings, the DSP nodes, and VST3/CLAP hosting. Those need cgo and
// real hardware, would not build green headlessly, and are an explicit later
// "native Phase-2" step (SPEC §1, §5, §6). This package defines the contract
// that native layer must satisfy, and nothing that links a C library.
//
// Invariants honored here (CLAUDE.md §2): deterministic (same input -> same
// output), degrade-never-crash (typed results + notes, never a panic), and
// Windows-path-safe via internal/pathx where paths are touched.
package audioengine

import "becky-go/internal/pathx"

// DeviceKind is whether a device captures (input) or plays (output) audio. A
// physical interface usually exposes BOTH as two separate Device entries.
type DeviceKind string

const (
	// KindInput is a capture device (mic / line-in).
	KindInput DeviceKind = "input"
	// KindOutput is a playback device (speakers / monitors / headphones).
	KindOutput DeviceKind = "output"
)

// Device models one audio endpoint as the native enumerator (miniaudio/WASAPI in
// Phase-2) will report it. It is intentionally backend-agnostic: the Phase-2
// AudioBackend.Enumerate populates these from real WASAPI device info; the pure-Go
// foundation reasons over them (selection rule) without any audio library.
type Device struct {
	// ID is the stable backend handle/identifier for the device. On WASAPI this
	// is the device ID string; tests use synthetic ids. May be a path-like value
	// on some backends, so display logic uses pathx (see DisplayName).
	ID string `json:"id"`
	// Name is the human-readable device name as reported by the OS.
	Name string `json:"name"`
	// Kind is input or output.
	Kind DeviceKind `json:"kind"`
	// IsInterface marks Jordan's pro AUDIO INTERFACE (vs the laptop built-in).
	// The selection rule prefers an interface for BOTH input and output when one
	// is present (SPEC device-default rule).
	IsInterface bool `json:"is_interface"`
	// IsDefault is whether the OS reports this as the system default endpoint for
	// its Kind. Used only as a tiebreak among non-interface devices.
	IsDefault bool `json:"is_default"`
	// Channels is the device's channel count (e.g. 2 for stereo).
	Channels int `json:"channels"`
	// SampleRate is the device's native/default sample rate in Hz.
	SampleRate int `json:"sample_rate"`
}

// DisplayName is a short label for the device, separator-agnostic so an id that
// originated as a Windows path still renders its final element on Linux/CI
// (CLAUDE.md §2 path invariant). Falls back to the raw Name/ID.
func (d Device) DisplayName() string {
	if d.Name != "" {
		return pathx.Base(d.Name)
	}
	return pathx.Base(d.ID)
}
