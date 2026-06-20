// Package videopreview is the typed Go client for the becky-video-preview sidecar
// (the Rust+wgpu GPU video-preview process; GUI-RULES.md §1 Phase 4). It speaks
// the NDJSON/stdio seam (SEAM-PROTOCOL.md) via internal/seam, turning the raw
// query/command verbs (video.open / video.frame / video.overlay / video.window)
// into a small, ergonomic Go API the NLE shell calls.
//
// Design (mirrors becky's "thin client over a native sidecar" pattern):
//
//   - Open(path)            — video.open  -> Info{Width,Height,FPS,DurationSec,Frames}
//   - Info()                — the cached Info from the last Open (no round-trip)
//   - Frame(timeSec)        — video.frame -> a PNG path on disk for that frame
//   - Overlay(timeSec,text) — video.overlay -> a PNG with the forensic lower-third
//   - WindowArgs(path)      — the argv to spawn a dedicated on-screen preview window
//
// The sidecar binary is resolved next to the running executable, or via the
// BECKY_VIDEO_PREVIEW env override, or on PATH. If it is absent the client
// degrades with a clear typed error (ErrSidecarMissing) — it NEVER crashes the
// caller (CLAUDE.md §2, the w==nil pattern). Binary data (the PNG frames) never
// crosses the seam: the client hands the sidecar an output PATH and reads the
// file the sidecar wrote there (SEAM-PROTOCOL.md rule 5).
//
// The client is fully unit-testable WITHOUT a real subprocess or GPU: construct
// one over a faked seam controller (NewWithController, see fake usage in the
// tests) and assert it sends the right verbs/args and parses the responses.
package videopreview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"becky-go/internal/seam"
)

// SidecarName is the base name of the video-preview sidecar binary (no extension).
const SidecarName = "becky-video-preview"

// envSidecar overrides the resolved sidecar path (an absolute path to the exe).
const envSidecar = "BECKY_VIDEO_PREVIEW"

// callTimeout bounds a single seam round-trip. Frame decode + GPU render + PNG
// write is well under a second on real hardware; this generous cap means a wedged
// sidecar surfaces as a typed error instead of hanging the UI.
const callTimeout = 30 * time.Second

// ErrSidecarMissing is returned by Open/Start when the sidecar binary cannot be
// located. Callers (the NLE window) turn it into one quiet in-window line and keep
// working — they never crash on it.
var ErrSidecarMissing = errors.New("becky-video-preview not found (build native/video-preview, or set BECKY_VIDEO_PREVIEW)")

// ErrNotOpen is returned when Frame/Overlay/Info is called before a successful Open.
var ErrNotOpen = errors.New("no video open: call Open first")

// Info is the metadata returned by video.open: the sidecar probes the file with
// ffprobe and reports its geometry, frame rate, and length. Frames is derived from
// duration*fps when the container omits an exact frame count.
type Info struct {
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	FPS         float64 `json:"fps"`
	DurationSec float64 `json:"durationSec"`
	Frames      int64   `json:"frames"`
}

// frameResult is the wire shape of a video.frame / video.overlay response. Only
// the fields the client uses are decoded; extra fields (backend, timecode, ...)
// are tolerated.
type frameResult struct {
	Out      string  `json:"out"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	TimeSec  float64 `json:"timeSec"`
	Backend  string  `json:"backend,omitempty"`
	Timecode string  `json:"timecode,omitempty"`
}

// Client is a connection to one running video-preview sidecar. It is safe for
// concurrent use; the underlying seam controller serialises its own I/O.
type Client struct {
	sc     *seam.Sidecar
	ownsSc bool // true when Client.Close should shut the sidecar down (real subprocess)
	tmpDir string

	mu     sync.Mutex
	path   string // the currently-open source video ("" if none)
	info   Info   // cached metadata from the last Open
	opened bool
}

// Start spawns the video-preview sidecar subprocess and returns a Client wired to
// it. ctx controls the subprocess lifetime (cancelling it kills the sidecar). If
// the binary cannot be located, it returns ErrSidecarMissing and a nil Client —
// the caller degrades, it does not crash.
//
// tmpDir is where Frame/Overlay write their PNGs (one per request); pass "" to use
// the OS temp dir. The caller owns cleanup of that dir.
func Start(ctx context.Context, tmpDir string) (*Client, error) {
	exePath, err := resolveSidecar()
	if err != nil {
		return nil, err
	}
	sc, err := seam.Start(ctx, exePath)
	if err != nil {
		return nil, fmt.Errorf("videopreview: start sidecar: %w", err)
	}
	if tmpDir == "" {
		tmpDir = os.TempDir()
	}
	return &Client{sc: sc, ownsSc: true, tmpDir: tmpDir}, nil
}

// NewWithController builds a Client over an already-constructed seam controller.
// This is the seam for unit tests (pass a seam.FakeSidecar's Controller()) and for
// any caller that wants to manage the sidecar lifetime itself. The Client will NOT
// close the controller in Close (ownsSc=false).
func NewWithController(sc *seam.Sidecar, tmpDir string) *Client {
	if tmpDir == "" {
		tmpDir = os.TempDir()
	}
	return &Client{sc: sc, ownsSc: false, tmpDir: tmpDir}
}

// Close shuts the sidecar down (only when this Client started it). Idempotent.
func (c *Client) Close() {
	if c.ownsSc && c.sc != nil {
		c.sc.Close()
	}
}

// Ping checks the sidecar is alive and speaking the protocol (command "ping").
// Returns the reported version, or an error. Useful as a liveness probe after Start.
func (c *Client) Ping(ctx context.Context) (string, error) {
	data, err := c.call(ctx, seam.TypeCommand, "ping", nil)
	if err != nil {
		return "", err
	}
	var r struct {
		Pong    bool   `json:"pong"`
		Version string `json:"version"`
	}
	_ = json.Unmarshal(data, &r)
	return r.Version, nil
}

// Open tells the sidecar to open the video at path and returns its metadata. It
// caches the path + Info so Info() and subsequent Frame/Overlay calls need no
// re-open. A failed open clears any previously-open state.
func (c *Client) Open(ctx context.Context, path string) (Info, error) {
	data, err := c.call(ctx, seam.TypeCommand, "video.open", map[string]string{"path": path})
	if err != nil {
		c.setClosed()
		return Info{}, err
	}
	var info Info
	if err := json.Unmarshal(data, &info); err != nil {
		c.setClosed()
		return Info{}, fmt.Errorf("videopreview: decode video.open: %w", err)
	}
	c.mu.Lock()
	c.path = path
	c.info = info
	c.opened = true
	c.mu.Unlock()
	return info, nil
}

// Info returns the metadata of the currently-open video, or ErrNotOpen.
func (c *Client) Info() (Info, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.opened {
		return Info{}, ErrNotOpen
	}
	return c.info, nil
}

// IsOpen reports whether a video is currently open.
func (c *Client) IsOpen() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.opened
}

// Path returns the currently-open source video path ("" if none).
func (c *Client) Path() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.path
}

// Frame requests a frame-accurate PNG of the open video at timeSec and returns the
// path of the PNG the sidecar wrote. The client picks the output path (under tmpDir)
// and passes it in the args; the sidecar writes the file there (binary never crosses
// the seam). Requires a prior successful Open.
func (c *Client) Frame(ctx context.Context, timeSec float64) (string, error) {
	src, err := c.currentPath()
	if err != nil {
		return "", err
	}
	out := c.framePath("frame", timeSec)
	args := map[string]interface{}{"path": src, "timeSec": timeSec, "out": out}
	data, err := c.call(ctx, seam.TypeQuery, "video.frame", args)
	if err != nil {
		return "", err
	}
	return c.resolveFrameOut(data, out)
}

// Overlay is like Frame but burns the forensic lower-third (running original-file
// timecode + the supplied caption text) into the returned PNG. Requires a prior Open.
func (c *Client) Overlay(ctx context.Context, timeSec float64, text string) (string, error) {
	src, err := c.currentPath()
	if err != nil {
		return "", err
	}
	out := c.framePath("overlay", timeSec)
	args := map[string]interface{}{"path": src, "timeSec": timeSec, "out": out, "text": text}
	data, err := c.call(ctx, seam.TypeQuery, "video.overlay", args)
	if err != nil {
		return "", err
	}
	return c.resolveFrameOut(data, out)
}

// WindowArgs returns the argv to spawn a dedicated, on-screen preview WINDOW for
// path. Per the sidecar protocol, video.window over stdio only returns a launch
// hint; the engine spawns a separate process `becky-video-preview --window <path>`.
// Returns ErrSidecarMissing if the binary can't be resolved. The caller execs this
// itself (the window owns its own blocking event loop, separate from the seam).
func WindowArgs(path string) (exePath string, args []string, err error) {
	exePath, err = resolveSidecar()
	if err != nil {
		return "", nil, err
	}
	return exePath, []string{"--window", path}, nil
}

// --- internals -------------------------------------------------------------------

// call sends one seam command/query with a bounded timeout derived from ctx.
func (c *Client) call(ctx context.Context, typ seam.MessageType, name string, args interface{}) (json.RawMessage, error) {
	if c.sc == nil {
		return nil, ErrSidecarMissing
	}
	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	data, err := c.sc.Call(cctx, typ, name, args)
	if err != nil {
		return nil, fmt.Errorf("videopreview: %s: %w", name, err)
	}
	return data, nil
}

// currentPath returns the open source path or ErrNotOpen.
func (c *Client) currentPath() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.opened {
		return "", ErrNotOpen
	}
	return c.path, nil
}

// setClosed marks no video open (used when an Open fails).
func (c *Client) setClosed() {
	c.mu.Lock()
	c.opened = false
	c.path = ""
	c.info = Info{}
	c.mu.Unlock()
}

// resolveFrameOut decodes a frame/overlay response, preferring the "out" the
// sidecar reports (it is authoritative) but falling back to the path we requested.
func (c *Client) resolveFrameOut(data json.RawMessage, requested string) (string, error) {
	var r frameResult
	if err := json.Unmarshal(data, &r); err != nil {
		return "", fmt.Errorf("videopreview: decode frame response: %w", err)
	}
	if r.Out != "" {
		return r.Out, nil
	}
	return requested, nil
}

// framePath builds a deterministic-per-(kind,time) PNG output path under tmpDir.
// Reusing the same name for the same timestamp lets the sidecar overwrite cleanly
// and keeps scrub-cache churn bounded (one file per distinct millisecond + kind).
func (c *Client) framePath(kind string, timeSec float64) string {
	ms := int64(timeSec*1000 + 0.5)
	name := fmt.Sprintf("becky-nle-%s-%d.png", kind, ms)
	return filepath.Join(c.tmpDir, name)
}

// resolveSidecar locates the becky-video-preview binary, in priority order:
//  1. $BECKY_VIDEO_PREVIEW (explicit override; used as-is)
//  2. next to the running executable (the shipped layout)
//  3. next to the CWD and ./bin (dev layout)
//  4. native/video-preview/target/release (a local cargo build, dev convenience)
//  5. on PATH (a bare name)
//
// Returns ErrSidecarMissing when none resolve.
func resolveSidecar() (string, error) {
	name := SidecarName
	if runtime.GOOS == "windows" {
		name += ".exe"
	}

	if override := os.Getenv(envSidecar); override != "" {
		if isFile(override) {
			return override, nil
		}
		// An override that doesn't exist is a clear, actionable miss.
		return "", fmt.Errorf("%w (BECKY_VIDEO_PREVIEW=%q does not exist)", ErrSidecarMissing, override)
	}

	if self, err := os.Executable(); err == nil {
		if cand := filepath.Join(filepath.Dir(self), name); isFile(cand) {
			return cand, nil
		}
	}
	if wd, err := os.Getwd(); err == nil {
		for _, cand := range []string{
			filepath.Join(wd, name),
			filepath.Join(wd, "bin", name),
			filepath.Join(wd, "native", "video-preview", "target", "release", name),
			filepath.Join(wd, "..", "native", "video-preview", "target", "release", name),
		} {
			if isFile(cand) {
				return cand, nil
			}
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	return "", ErrSidecarMissing
}

// isFile reports whether path is an existing regular file.
func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
