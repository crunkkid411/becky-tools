package audioengine

import "errors"

// host.go defines the HOST-BOUNDARY interfaces between the pure-Go control plane
// (this package) and the native Phase-2 cgo layer (SPEC §6). The Go side decides
// WHAT and WHEN ahead of time; the native C/C++ side does THE WORK under the
// audio deadline (SPEC §6.1). Nothing here links a C library — these are the
// contracts the native implementations (miniaudio/WASAPI backend, CLAP/VST3
// plugin host) must satisfy.
//
// Phase-2 is DEFERRED, not done. The real-time audio callback, the lock-free
// rings, the DSP, and plugin hosting all require cgo + real hardware and would
// not build green headlessly (CLAUDE.md §3). The stubs below let the pure-Go
// foundation compile, test, and degrade cleanly today; the native build tag
// (//go:build cgo) will provide the working implementations later.

// ErrNativeUnavailable is returned by every stub backend/host method. It signals
// "the native Phase-2 layer is not wired in this build" — a typed degrade, never
// a panic (CLAUDE.md §2). Callers report it plainly and continue with whatever
// pure-Go work is possible (e.g. listing devices from an injected enumerator).
var ErrNativeUnavailable = errors.New("native audio/plugin layer not built (Phase-2, requires cgo)")

// AudioBackend is the device + transport control plane the native miniaudio/
// WASAPI layer implements (SPEC §1.2, §6.1). Go calls Enumerate/Start/Stop on a
// goroutine; the real-time callback that drains the MIDI ring and walks the
// compiled schedule lives entirely in C behind Start (SPEC §6.2) and is NOT
// exposed here — Go never runs on the audio thread.
//
// Phase-2 native contract (what the cgo implementation must do):
//   - Enumerate: query WASAPI for all input+output endpoints, mark the pro
//     interface IsInterface=true, return them as []Device. No audio library is
//     allowed to leak across this boundary — only the Device value type crosses.
//   - Start(Selection): open the chosen input/output (Selection from
//     SelectDefaults), install the pure-C data callback, begin streaming. WASAPI
//     shared mode by default (~10 ms floor); exclusive opt-in later (SPEC §1.2).
//   - Stop: stop the stream and release the device, idempotently.
type AudioBackend interface {
	// Enumerate lists all audio devices the OS reports. The pure-Go foundation
	// then applies SelectDefaults to the result.
	Enumerate() ([]Device, error)
	// Start opens the selected devices and begins the real-time audio stream.
	Start(sel Selection) error
	// Stop ends the stream and releases the devices. Safe to call when stopped.
	Stop() error
}

// PluginHost is the SELECTIVE plugin host the native C++ shim implements (SPEC
// §5.3). It loads a Jordan-declared allowlisted CLAP (Phase-2) or VST3 (Phase-3,
// behind //go:build vst3) plugin and presents it to the graph as just another
// node. Determinism caveat: a hosted plugin is opaque and may be non-deterministic
// — that is a declared, UI-labeled "deterministic:false" exception (SPEC §5.2).
//
// Phase-2 native contract (mirrors the bh_* C ABI in SPEC §5.3): Scan validates a
// plugin path against the allowlist and returns its descriptor; Instantiate
// activates it at a sample rate / max block and returns an opaque handle the
// graph node wraps. The audio + MIDI + parameter plumbing (bh_process,
// bh_param_set, bh_state_save/load) lives in the native layer behind that handle.
type PluginHost interface {
	// Scan validates an allowlisted plugin path and returns its descriptor
	// (params, ports). It does NOT scan the whole system — default-deny (SPEC §5.2).
	Scan(path string) (PluginDescriptor, error)
	// Instantiate activates a scanned plugin at the given sample rate and maximum
	// block size and returns an opaque native handle id for the graph node to wrap.
	Instantiate(path string, sampleRate, maxBlock int) (PluginHandle, error)
}

// PluginFormat identifies the plugin ABI a descriptor was loaded through.
type PluginFormat string

const (
	// FormatCLAP is the MIT-licensed CLever Audio Plugin C ABI (Phase-2 first
	// target — SPEC §5.1).
	FormatCLAP PluginFormat = "clap"
	// FormatVST3 is the now-MIT VST3 SDK (Phase-3, behind //go:build vst3 —
	// SPEC §5.1, §5.3).
	FormatVST3 PluginFormat = "vst3"
)

// PluginParam describes one automatable parameter exposed by a hosted plugin
// (maps to bh_param_list in the SPEC §5.3 C ABI).
type PluginParam struct {
	ID      uint32  `json:"id"`
	Name    string  `json:"name"`
	Min     float64 `json:"min"`
	Max     float64 `json:"max"`
	Default float64 `json:"default"`
}

// PluginDescriptor is the metadata a Scan returns: identity, format, and the
// automatable parameter list. Audio/MIDI port counts let the graph wire it up.
type PluginDescriptor struct {
	Path         string        `json:"path"`
	Name         string        `json:"name"`
	Format       PluginFormat  `json:"format"`
	AudioInputs  int           `json:"audio_inputs"`
	AudioOutputs int           `json:"audio_outputs"`
	Params       []PluginParam `json:"params"`
	// Deterministic is false for any hosted third-party plugin — a declared,
	// UI-labeled opt-out of the determinism invariant (SPEC §5.2).
	Deterministic bool `json:"deterministic"`
}

// PluginHandle is the opaque native handle id for an instantiated plugin. The Go
// graph node carries it and the native layer maps it to the live C++ instance.
type PluginHandle uint64

// --- Stub implementations (no cgo). These let the pure-Go foundation compile,
// run, and DEGRADE today; the native build replaces them. ---

// StubBackend is the no-cgo AudioBackend used until the native layer exists. Every
// method degrades to ErrNativeUnavailable — it never panics and never pretends to
// have hardware. The CLI uses an INJECTED enumerator for the headless device demo
// (see cmd/daw-engine), so StubBackend's Enumerate is the honest "nothing here yet".
type StubBackend struct{}

// Enumerate reports that no native enumerator is wired (returns an empty list +
// ErrNativeUnavailable). The CLI injects its own DeviceEnumerator instead.
func (StubBackend) Enumerate() ([]Device, error) { return nil, ErrNativeUnavailable }

// Start degrades: there is no real-time stream without the native layer.
func (StubBackend) Start(Selection) error { return ErrNativeUnavailable }

// Stop degrades cleanly (nothing to stop).
func (StubBackend) Stop() error { return ErrNativeUnavailable }

// StubPluginHost is the no-cgo PluginHost. It refuses to load anything until the
// native CLAP/VST3 shim is built, so a project that declares a plugin degrades to
// a clear message rather than a crash.
type StubPluginHost struct{}

// Scan degrades: no plugin can be inspected without the native shim.
func (StubPluginHost) Scan(string) (PluginDescriptor, error) {
	return PluginDescriptor{}, ErrNativeUnavailable
}

// Instantiate degrades: no plugin can be activated without the native shim.
func (StubPluginHost) Instantiate(string, int, int) (PluginHandle, error) {
	return 0, ErrNativeUnavailable
}

// Compile-time assertions that the stubs satisfy the boundary interfaces, so the
// contract can never silently drift from the native implementations.
var (
	_ AudioBackend = StubBackend{}
	_ PluginHost   = StubPluginHost{}
)
