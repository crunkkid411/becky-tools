package audiohost

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"becky-go/internal/seam"
)

// transport is the minimal slice of *seam.Sidecar this client needs. Defining
// it here lets unit tests inject a fake sidecar's controller without spawning a
// real subprocess (*seam.Sidecar satisfies it).
type transport interface {
	Call(ctx context.Context, typ seam.MessageType, name string, args interface{}) (json.RawMessage, error)
	Close()
}

// Client is a typed, concurrency-safe driver for one becky-audio-host process.
//
// Construct it with Open (which spawns the real sidecar) and always Close it.
// Every method is safe for concurrent use: the underlying seam controller
// correlates responses by id, so calls may overlap. Methods validate the host's
// response shape and return a wrapped error rather than panicking on bad data.
type Client struct {
	sc   transport
	path string

	closeOnce sync.Once
}

// Open spawns becky-audio-host and returns a ready Client. The exe is located
// via ResolveHost (env override → next to the running binary → native build
// output). If it cannot be found, Open returns a *NotFoundError with a
// plain-language message telling Jordan how to build it — it never panics.
//
// The provided context governs the subprocess lifetime: cancelling it kills the
// host. Pass context.Background() for a long-lived host and rely on Close.
func Open(ctx context.Context) (*Client, error) {
	path, searched := resolveHostVerbose()
	if path == "" {
		return nil, &NotFoundError{Searched: searched}
	}
	sc, err := seam.Start(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("audiohost: start %s: %w", path, err)
	}
	return &Client{sc: sc, path: path}, nil
}

// newClientFromTransport wraps an injected transport. Used by tests to drive the
// client against a fake seam sidecar with no real subprocess.
func newClientFromTransport(t transport) *Client {
	return &Client{sc: t, path: "(injected)"}
}

// Path returns the resolved path of the host executable this client drives.
func (c *Client) Path() string { return c.path }

// Close shuts the host down by closing its stdin (the host exits on stdin
// close). It is idempotent and safe to call from any goroutine.
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		if c.sc != nil {
			c.sc.Close()
		}
	})
}

// call is the shared command/query helper: it sends the verb, then unmarshals
// the host's data payload into out. A nil out skips decoding (void verbs).
func (c *Client) call(ctx context.Context, typ seam.MessageType, verb string, args interface{}, out interface{}) error {
	if c.sc == nil {
		return fmt.Errorf("audiohost: client not open")
	}
	data, err := c.sc.Call(ctx, typ, verb, args)
	if err != nil {
		return fmt.Errorf("audiohost: %s: %w", verb, err)
	}
	if out == nil {
		return nil
	}
	if len(data) == 0 {
		return fmt.Errorf("audiohost: %s: empty response", verb)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("audiohost: %s: decode response: %w", verb, err)
	}
	return nil
}

// Ping checks the host is alive and returns its reported version.
func (c *Client) Ping(ctx context.Context) (version string, err error) {
	var r struct {
		Pong    bool   `json:"pong"`
		Version string `json:"version"`
	}
	if err := c.call(ctx, seam.TypeQuery, "ping", nil, &r); err != nil {
		return "", err
	}
	return r.Version, nil
}

// --- audio device verbs ---

// Devices lists the host's audio output devices (audio.devices). It marks the
// system default and whether an ASIO backend is available.
func (c *Client) Devices(ctx context.Context) (DevicesResult, error) {
	var r DevicesResult
	err := c.call(ctx, seam.TypeQuery, "audio.devices", nil, &r)
	return r, err
}

// OpenOptions configure audio.open. Nil pointers let the host pick its defaults
// (system default device, prefers ASIO > WASAPI > MME).
type OpenOptions struct {
	Device     *int `json:"device,omitempty"`
	SampleRate *int `json:"samplerate,omitempty"`
	Buffer     *int `json:"buffer,omitempty"`
}

// OpenAudio opens an output stream (audio.open). Pass a zero OpenOptions to use
// the system default interface at the host's default rate/buffer.
func (c *Client) OpenAudio(ctx context.Context, opts OpenOptions) (OpenResult, error) {
	var r OpenResult
	err := c.call(ctx, seam.TypeCommand, "audio.open", opts, &r)
	return r, err
}

// StartAudio starts the output stream (audio.start).
func (c *Client) StartAudio(ctx context.Context) error {
	return c.call(ctx, seam.TypeCommand, "audio.start", nil, nil)
}

// StopAudio stops the output stream (audio.stop).
func (c *Client) StopAudio(ctx context.Context) error {
	return c.call(ctx, seam.TypeCommand, "audio.stop", nil, nil)
}

// --- VST verbs ---

// ScanVST enumerates .vst3 plugins under dir (vst.scan). An empty dir lets the
// host use its platform default VST3 directory (recursive into vendor
// subfolders). Each plugin is probed in a child process, so a faulting plugin is
// reported (Crashed=true) and skipped, never fatal.
func (c *Client) ScanVST(ctx context.Context, dir string) (ScanResult, error) {
	args := map[string]interface{}{"recursive": true}
	if dir != "" {
		args["dir"] = dir
	}
	var r ScanResult
	err := c.call(ctx, seam.TypeQuery, "vst.scan", args, &r)
	return r, err
}

// LoadVST instantiates the plugin at path (vst.load) and returns its Instance
// (id, name, params, hasEditor, channels). The returned InstanceID is the handle
// for every later per-instance verb.
func (c *Client) LoadVST(ctx context.Context, path string) (Instance, error) {
	return c.LoadVSTOptions(ctx, path, 0, 0)
}

// LoadVSTOptions instantiates the plugin at path with an explicit sample rate
// and buffer size (zero = host default).
func (c *Client) LoadVSTOptions(ctx context.Context, path string, sampleRate, buffer int) (Instance, error) {
	var r Instance
	if path == "" {
		return r, fmt.Errorf("audiohost: LoadVST: empty path")
	}
	args := map[string]interface{}{"path": path}
	if sampleRate > 0 {
		args["samplerate"] = sampleRate
	}
	if buffer > 0 {
		args["buffer"] = buffer
	}
	err := c.call(ctx, seam.TypeCommand, "vst.load", args, &r)
	return r, err
}

// ParamList returns the current parameter values of a loaded instance
// (vst.param.list).
func (c *Client) ParamList(ctx context.Context, instanceID int) ([]Param, error) {
	var r struct {
		InstanceID int     `json:"instanceId"`
		Params     []Param `json:"params"`
	}
	err := c.call(ctx, seam.TypeQuery, "vst.param.list",
		map[string]interface{}{"instanceId": instanceID}, &r)
	return r.Params, err
}

// SetParam sets a normalized [0,1] parameter value on a loaded instance
// (vst.param.set).
func (c *Client) SetParam(ctx context.Context, instanceID int, paramID uint32, value float64) error {
	return c.call(ctx, seam.TypeCommand, "vst.param.set", map[string]interface{}{
		"instanceId": instanceID,
		"paramId":    paramID,
		"value":      value,
	}, nil)
}

// NoteOn queues a VST3 NoteOn for the next process block (note.on).
func (c *Client) NoteOn(ctx context.Context, instanceID, pitch int, velocity float64) error {
	return c.call(ctx, seam.TypeCommand, "note.on", map[string]interface{}{
		"instanceId": instanceID,
		"pitch":      pitch,
		"velocity":   velocity,
	}, nil)
}

// NoteOff queues a VST3 NoteOff for the next process block (note.off).
func (c *Client) NoteOff(ctx context.Context, instanceID, pitch int) error {
	return c.call(ctx, seam.TypeCommand, "note.off", map[string]interface{}{
		"instanceId": instanceID,
		"pitch":      pitch,
	}, nil)
}

// RenderOptions configure an offline render. SampleRate and Buffer are optional
// (host defaults apply when zero).
type RenderOptions struct {
	SampleRate int
	Buffer     int
}

// renderArgs builds the render args map shared by Render and RenderPath.
func renderArgs(events []NoteEvent, durationSec float64, outWav string, opts RenderOptions) map[string]interface{} {
	args := map[string]interface{}{
		"durationSec": durationSec,
		"out":         outWav,
	}
	if len(events) > 0 {
		args["events"] = events
	}
	if opts.SampleRate > 0 {
		args["sampleRate"] = opts.SampleRate
	}
	if opts.Buffer > 0 {
		args["buffer"] = opts.Buffer
	}
	return args
}

// Render offline-renders a previously loaded instance to outWav (render verb),
// applying the given timed note events over durationSec. The host writes a WAV
// to outWav and returns the measured peak/RMS plus NonSilent corroboration.
//
// Audio does not cross the seam: outWav is a path the host writes directly.
func (c *Client) Render(ctx context.Context, instanceID int, events []NoteEvent, durationSec float64, outWav string, opts RenderOptions) (RenderResult, error) {
	var r RenderResult
	if outWav == "" {
		return r, fmt.Errorf("audiohost: Render: empty out path")
	}
	args := renderArgs(events, durationSec, outWav, opts)
	args["instanceId"] = instanceID
	err := c.call(ctx, seam.TypeCommand, "render", args, &r)
	return r, err
}

// RenderPath loads + offline-renders the plugin at vst3Path in one call (the
// host instantiates a temporary instance), writing to outWav. This is the
// quickest way to prove the Go→host→VST3→WAV chain end to end.
func (c *Client) RenderPath(ctx context.Context, vst3Path string, events []NoteEvent, durationSec float64, outWav string, opts RenderOptions) (RenderResult, error) {
	var r RenderResult
	if vst3Path == "" {
		return r, fmt.Errorf("audiohost: RenderPath: empty plugin path")
	}
	if outWav == "" {
		return r, fmt.Errorf("audiohost: RenderPath: empty out path")
	}
	args := renderArgs(events, durationSec, outWav, opts)
	args["path"] = vst3Path
	err := c.call(ctx, seam.TypeCommand, "render", args, &r)
	return r, err
}

// Shutdown asks the host to exit cleanly (shutdown verb), then closes the
// client. Prefer this over Close when you want a graceful host exit.
func (c *Client) Shutdown(ctx context.Context) error {
	err := c.call(ctx, seam.TypeCommand, "shutdown", nil, nil)
	c.Close()
	return err
}
