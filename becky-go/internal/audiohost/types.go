// Package audiohost is the Go client for the native becky-audio-host C++ sidecar.
//
// becky-audio-host is the C++ VST3/audio host (GUI-RULES.md Phases 2-3). The Go
// engine drives it over the NDJSON-stdio seam (internal/seam, SEAM-PROTOCOL.md).
// This package is a thin, typed, concurrency-safe wrapper that maps 1:1 to the
// host's verbs (audio.devices / audio.open / audio.start / audio.stop /
// vst.scan / vst.load / vst.param.list / vst.param.set / note.on / note.off /
// render).
//
// Audio samples NEVER cross this seam: it is a control plane only. The host
// owns its own realtime thread; offline render() writes a WAV to a path we name.
//
// Degrade-never-crash: if the host exe is absent, Open returns a typed
// *NotFoundError with a plain-language message (how to build it) instead of a
// panic. Every method validates the host response shape and returns a wrapped
// error on malformed data.
package audiohost

import "fmt"

// PluginClass is one VST3 class advertised by a scanned plugin module.
// A single .vst3 bundle may expose several (e.g. effect + controller).
type PluginClass struct {
	Name     string `json:"name"`
	Category string `json:"category"`
	Vendor   string `json:"vendor"`
	Version  string `json:"version"`
}

// Plugin is one entry from a vst.scan result: a .vst3 on disk and whether it
// loaded cleanly. A plugin that faulted on load is reported with Crashed=true
// and skipped by the host, never fatal.
type Plugin struct {
	Path     string        `json:"path"`
	Name     string        `json:"name"`
	Category string        `json:"category"`
	Loadable bool          `json:"loadable"`
	Crashed  bool          `json:"crashed"`
	Error    string        `json:"error"`
	Classes  []PluginClass `json:"classes"`
}

// ScanResult is the full vst.scan response: the directory scanned, the count,
// how many faulted, and every plugin found.
type ScanResult struct {
	Dir     string   `json:"dir"`
	Count   int      `json:"count"`
	Crashed int      `json:"crashed"`
	Plugins []Plugin `json:"plugins"`
}

// Param is one VST3 parameter exposed by a loaded plugin. Values are normalized
// to [0,1]. Default is the plugin's default; Current is the live value.
type Param struct {
	ID        uint32  `json:"id"`
	Title     string  `json:"title"`
	Units     string  `json:"units"`
	Default   float64 `json:"default"`
	StepCount int     `json:"stepCount"`
	Current   float64 `json:"current"`
}

// Instance is a loaded plugin (one vst.load). InstanceID is the handle used by
// every later per-instance verb (param.list/set, note.on/off, render).
type Instance struct {
	InstanceID  int     `json:"instanceId"`
	Name        string  `json:"name"`
	Path        string  `json:"path"`
	OutChannels int     `json:"outChannels"`
	SampleRate  int     `json:"sampleRate"`
	HasEditor   bool    `json:"hasEditor"`
	Params      []Param `json:"params"`
}

// Device is one audio output device from audio.devices. The exact per-device
// field names emitted by the C++ host are not yet locked down in the spec, so
// the common fields are decoded leniently and the raw JSON is kept for
// forward-compat.
type Device struct {
	Index    int    `json:"index"`
	Name     string `json:"name"`
	HostAPI  string `json:"hostApi"`
	Channels int    `json:"maxOutputChannels"`
	Default  bool   `json:"default"`
	ASIO     bool   `json:"asio"`
}

// DevicesResult is the audio.devices response. The host marks the system
// default and whether an ASIO backend is available.
type DevicesResult struct {
	DefaultOutput  int      `json:"default_output"`
	DefaultHostAPI string   `json:"default_host_api"`
	ASIOAvailable  bool     `json:"asio_available"`
	Devices        []Device `json:"devices"`
}

// OpenResult is the audio.open response (device opened for output).
type OpenResult struct {
	Device     int    `json:"device"`
	SampleRate int    `json:"samplerate"`
	Buffer     int    `json:"buffer"`
	HostAPI    string `json:"hostApi"`
}

// RenderResult is the render response: where the WAV was written and the
// measured audio statistics. NonSilent is the host's own corroboration that the
// plugin actually produced sound (peak above the silence floor).
type RenderResult struct {
	Out          string  `json:"out"`
	Name         string  `json:"name"`
	Frames       int64   `json:"frames"`
	Channels     int     `json:"channels"`
	SampleRate   int     `json:"sampleRate"`
	Peak         float64 `json:"peak"`
	PeakDb       float64 `json:"peakDb"`
	RMS          float64 `json:"rms"`
	RMSDb        float64 `json:"rmsDb"`
	NonSilent    bool    `json:"nonSilent"`
	IsEffect     bool    `json:"isEffect"`
	IsInstrument bool    `json:"isInstrument"`
}

// NoteEvent is one timed MIDI event for an offline render. Type is "noteOn" or
// "noteOff"; TimeSec is the absolute offset from the start of the render.
type NoteEvent struct {
	Type     string  `json:"type"`
	TimeSec  float64 `json:"timeSec"`
	Pitch    int     `json:"pitch"`
	Velocity float64 `json:"velocity,omitempty"`
	Channel  int     `json:"channel,omitempty"`
}

// NoteOn returns a noteOn NoteEvent at t seconds.
func NoteOn(t float64, pitch int, velocity float64) NoteEvent {
	return NoteEvent{Type: "noteOn", TimeSec: t, Pitch: pitch, Velocity: velocity}
}

// NoteOff returns a noteOff NoteEvent at t seconds.
func NoteOff(t float64, pitch int) NoteEvent {
	return NoteEvent{Type: "noteOff", TimeSec: t, Pitch: pitch}
}

// NotFoundError is returned by Open when the becky-audio-host executable cannot
// be located. It carries the paths searched so the caller can tell Jordan
// exactly how to build it.
type NotFoundError struct {
	Searched []string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf(
		"becky-audio-host not found (searched: %v). Build it: native/audio-host/scripts/build.ps1, "+
			"or set BECKY_AUDIO_HOST to the built becky-audio-host.exe",
		e.Searched)
}
