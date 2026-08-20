// Package ground answers WHERE, as a box, for a thing named in plain English.
//
// WHY IT EXISTS. becky's framing signals are all PERSON detectors — MediaPipe
// Pose, InsightFace, LR-ASD. When they fail, the crop path had exactly one
// answer: a dead-centre rect. Jordan, 2026-08-20: "defaulting to center crop is
// not okay - it makes the video end up in the recycle bin. If there's a REASON
// to focus on center (like an inanimate object, or it's simply correctly framed
// already, etc) that's totally fine, but assuming center crop is correct is
// wrong - every frame needs to be meticulously approved."
//
// THE MEASUREMENT THAT PICKED THE MODEL (2026-08-20, on his own footage, GPU up):
//
//	Gemma-4 E4B, 3x3 keyframe grid, "how many people are cut off by the edge"
//	  -> "clipped: 0" for all nine tiles, including five where a person is
//	     plainly sheared by the left edge. It counted FACES correctly (2 in the
//	     two-shots). It cannot localise.
//	Gemma-4 12B, same grid -> 141s and an EMPTY response; 6.7GB spills off an
//	     8GB card. Not viable per span.
//	Reka Edge 2603, "Detect: person" -> "00,00,21,100;34,07,94,100", tight and
//	     correct against the frame, in 1.7s.
//
// So the division of labour is not a preference, it is what the hardware
// measured: a VLM says WHAT is in a shot, a grounded detector says WHERE it is.
// This package is the WHERE. Nothing here guesses a position.
//
// It also owns the llama-server holding Reka, because a caller that has to
// start a model server is not making one dumb call.
package ground

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"becky-go/internal/config"
	"becky-go/internal/pyhelpers"
)

// Box is one detection in FRACTIONS of frame width/height, which is what
// internal/crop consumes.
type Box struct {
	Label string  `json:"label"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	W     float64 `json:"w"`
	H     float64 `json:"h"`
}

// CenterX is the box's horizontal centre as a fraction of frame width.
func (b Box) CenterX() float64 { return b.X + b.W/2 }

// CenterY is the box's vertical centre as a fraction of frame height.
func (b Box) CenterY() float64 { return b.Y + b.H/2 }

// Area is the box's share of the frame.
func (b Box) Area() float64 { return b.W * b.H }

// Detection is one sampled instant.
type Detection struct {
	T     float64 `json:"t"`
	Boxes []Box   `json:"boxes"`
}

// Result is one grounded window.
type Result struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
	// Target is what was actually detected — either the caller's --target, or,
	// when the caller passed none and no person was found, the noun phrase the
	// model itself named (Named).
	Target string  `json:"target,omitempty"`
	Named  string  `json:"named,omitempty"`
	FPS    float64 `json:"fps,omitempty"`
	Frames int     `json:"frames,omitempty"`
	// FoundFrac is the share of sampled frames that produced any box, and
	// Stable says the boxes agree with each other across the window. An
	// UNSTABLE result is a hint about which region matters, never a camera path
	// — ground.py's own words, and the caller must honour the distinction.
	FoundFrac  float64     `json:"found_frac,omitempty"`
	MedianJump float64     `json:"median_jump,omitempty"`
	Stable     bool        `json:"stable,omitempty"`
	Note       string      `json:"note,omitempty"`
	Detections []Detection `json:"detections,omitempty"`
}

// Options configure one grounding run.
type Options struct {
	Video string
	Start float64
	End   float64
	// Target is what to find, plain English. EMPTY is the normal case and is
	// self-orchestrating: ground.py probes for a person first and only asks the
	// open "what is this shot about" question when there is nobody in it.
	Target string
	// FPS is how often to ground. Grounding costs ~1.7s/frame on the 3070, so
	// this is deliberately coarser than tracking. 0 uses DefaultFPS.
	FPS float64
	// Timeout is the per-frame server timeout in seconds. 0 uses 120.
	Timeout float64
}

// DefaultFPS is one sample a second. Grounding decides WHERE TO POINT for a
// whole shot, not a per-frame camera path — the pose tracker does that when it
// works. One a second is enough to notice a subject move within a shot and is
// half the cost of two.
const DefaultFPS = 1.0

// MaxSamples caps the frames spent on ONE span however long it is. Grounding
// answers "where does this shot point", which does not get more true with more
// samples, and each call costs seconds.
const MaxSamples = 8.0

// MinSamples is how many frames a span must contribute however short it is, so
// that "nothing grounded" always means "looked and found nothing" rather than
// "never looked". Three is the smallest count where ground.py's own stability
// test can distinguish a held subject from a single lucky sighting.
const MinSamples = 3.0

// Runner holds a live Reka Edge server and grounds windows against it.
// Use New, defer Close, then call Run as many times as needed — the server
// start is the expensive part (~45s) and must not be paid per window.
type Runner struct {
	cfg    config.Config
	url    string
	stop   func()
	script string
	Logf   func(string, ...any)
}

// New starts a Reka Edge llama-server and returns a Runner bound to it.
//
// REASONING IS DISABLED ON PURPOSE. Reka's chat template ships thinking=1, and
// with it on, free-form answers come back as meta-commentary because the real
// answer went into the thinking block — while `Detect:` keeps working. That is
// exactly the kind of half-broken that ships (ground.py's docstring, found by
// running it).
func New(cfg config.Config, logf func(string, ...any)) (*Runner, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if cfg.RekaModel == "" || !fileExists(cfg.RekaModel) {
		return nil, fmt.Errorf("grounding model not found (config reka_model=%q)", cfg.RekaModel)
	}
	if cfg.RekaMMProj == "" || !fileExists(cfg.RekaMMProj) {
		return nil, fmt.Errorf("grounding vision projector not found (config reka_mmproj=%q)", cfg.RekaMMProj)
	}
	if cfg.LlamaServer == "" || !fileExists(cfg.LlamaServer) {
		return nil, fmt.Errorf("llama-server not found (config llama_server=%q)", cfg.LlamaServer)
	}
	script, err := pyhelpers.Materialize("ground.py", pyhelpers.Ground)
	if err != nil {
		return nil, err
	}

	port, err := freePort()
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("http://127.0.0.1:%d", port)

	cmd := exec.Command(cfg.LlamaServer,
		"-m", cfg.RekaModel,
		"--mmproj", cfg.RekaMMProj,
		"-ngl", "99",
		"-c", "4096", // measured: one 640px frame + prompt is ~120 tokens; 8192 spilled off the card
		"-fa", "on",
		"--no-warmup",
		"--reasoning-budget", "0", // see doc comment: thinking=1 breaks free-form answers
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(port),
	)
	logFile, _ := os.CreateTemp("", "becky_ground_server_*.log")
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("cannot start the grounding server: %w", err)
	}
	stop := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
		if logFile != nil {
			logFile.Close()
		}
	}

	logf("ground: starting Reka Edge on %s ...", url)
	if err := waitHealthy(url, 180*time.Second); err != nil {
		stop()
		return nil, err
	}
	logf("ground: ready on %s", url)
	return &Runner{cfg: cfg, url: url, stop: stop, script: script, Logf: logf}, nil
}

// Close terminates the grounding server.
func (r *Runner) Close() {
	if r != nil && r.stop != nil {
		r.stop()
		r.stop = nil
	}
}

// Run grounds one window. A helper that cannot answer returns ok=false with a
// reason rather than an error — degrade, never crash — so the caller can put
// the reason straight into its report.
func (r *Runner) Run(opt Options) (Result, error) {
	if opt.FPS <= 0 {
		opt.FPS = DefaultFPS
	}
	// A SHORT SPAN MUST STILL GET ENOUGH FRAMES TO ANSWER. At 2fps a 0.36s span
	// decodes zero or one frame, and "nothing grounded" then means "nothing was
	// LOOKED AT" — which reads identically in the output and is a completely
	// different fact. Measured: becky refused the whole burger short over a
	// 0.36s sliver at the window edge that grounding never actually saw.
	//
	// So the sample RATE rises for a short span until the sample COUNT is
	// usable. The cost is bounded by construction: a short span is short.
	if d := opt.End - opt.Start; d > 0 {
		if need := MinSamples / d; need > opt.FPS {
			opt.FPS = need
		}
	}
	// And a LONG span must not cost minutes. Framing is one decision for the
	// whole shot, so past MaxSamples more frames buy nothing: the rate is capped
	// so a 60-second span costs the same eight calls a 8-second one does.
	if d := opt.End - opt.Start; d > 0 {
		if cap := MaxSamples / d; cap < opt.FPS {
			opt.FPS = cap
		}
	}
	if opt.Timeout <= 0 {
		opt.Timeout = 120
	}
	args := []string{r.script,
		"--video", opt.Video,
		"--server", r.url,
		"--start", trimF(opt.Start),
		"--end", trimF(opt.End),
		"--fps", trimF(opt.FPS),
		"--ffmpeg", r.cfg.FFmpeg,
		"--timeout", trimF(opt.Timeout),
	}
	if opt.Target != "" {
		args = append(args, "--target", opt.Target)
	}

	cmd := exec.Command(pythonFor(r.cfg), args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return Result{}, fmt.Errorf("grounding helper failed: %v\n%s", err, tail(stderr.String()))
	}
	var res Result
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &res); err != nil {
		return Result{}, fmt.Errorf("grounding helper returned unreadable JSON: %w", err)
	}
	return res, nil
}

// Best returns the single box the crop should aim at across the whole window,
// and how many sampled instants contributed to it.
//
// THE RULE, and it is Jordan's: "a wrong focal point is worse than a centre
// crop". So this is deliberately conservative — it returns ok=false unless the
// detections actually agree. The caller must then REFUSE the span, not centre it.
//
// Among boxes in one frame it takes the LARGEST, because on a two-shot the
// larger figure is the one the shot is built around; ties on area are broken by
// whichever sits nearer the frame's own centre, which is the only place a
// tie-break may look at the centre at all.
func Best(res Result) (Box, int, bool) {
	b, n, _, ok := BestWithSpread(res)
	return b, n, ok
}

// BestWithSpread is Best plus how far the CHOSEN subject's centre wanders
// across the window, as a fraction of frame width.
//
// The distinction matters and getting it wrong refused a real short. A
// three-shot has people at x=0.00-0.21 and x=0.34-0.94; the UNION of everything
// detected is 94% of the frame, and reading that as "the subject moves across
// 94% of the frame" is simply the wrong measurement — three subjects standing
// still is not one subject running. A 9:16 crop of 16:9 is 31.6% of the width
// and can never hold three people anyway, so the job was always to CHOOSE one.
//
// So the spread is measured over the chosen box only, frame to frame.
func BestWithSpread(res Result) (Box, int, float64, bool) {
	if !res.OK {
		return Box{}, 0, 0, false
	}
	var picked []Box
	for _, d := range res.Detections {
		if b, ok := largest(d.Boxes); ok {
			picked = append(picked, b)
		}
	}
	if len(picked) == 0 {
		return Box{}, 0, 0, false
	}
	var sx, sy, sw, sh float64
	lo, hi := 1.0, 0.0
	for _, b := range picked {
		cx := b.CenterX()
		sx += cx
		sy += b.CenterY()
		sw += b.W
		sh += b.H
		if cx < lo {
			lo = cx
		}
		if cx > hi {
			hi = cx
		}
	}
	n := float64(len(picked))
	cx, cy, w, h := sx/n, sy/n, sw/n, sh/n
	return Box{Label: res.Target, X: cx - w/2, Y: cy - h/2, W: w, H: h}, len(picked), hi - lo, true
}

// OccludedFrac is the share of SIGHTED frames whose largest box covers at least
// minArea of the frame — i.e. frames where the camera is blocked rather than
// pointed at something.
//
// It must be counted per frame and never averaged into the mean box. Measured on
// the BLINDFOLD master at 553-556s, where a shirt swings into the lens:
//
//	t=554  area 0.62  "person"
//	t=555  area 1.00  "checkerboard"   <- the lens is covered
//
// The mean of those is 0.81, under any sane occlusion threshold, so an averaged
// test called a blocked camera a subject and framed it. Two thirds of the frames
// being blocked is the fact that matters, and averaging destroys it.
func OccludedFrac(res Result, minArea float64) float64 {
	seen, blocked := 0, 0
	for _, d := range res.Detections {
		b, ok := largest(d.Boxes)
		if !ok {
			continue
		}
		seen++
		if b.Area() >= minArea {
			blocked++
		}
	}
	if seen == 0 {
		return 0
	}
	return float64(blocked) / float64(seen)
}

func largest(boxes []Box) (Box, bool) {
	best := Box{}
	found := false
	for _, b := range boxes {
		if !found || b.Area() > best.Area() ||
			(b.Area() == best.Area() && absf(b.CenterX()-0.5) < absf(best.CenterX()-0.5)) {
			best, found = b, true
		}
	}
	return best, found
}

// --- small helpers, deliberately local so this package stays standalone ---

func absf(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func fileExists(p string) bool {
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func waitHealthy(url string, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	client := &http.Client{Timeout: 3 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("the grounding server did not become ready within %s", limit)
}

func pythonFor(cfg config.Config) string {
	if cfg.Python != "" {
		return cfg.Python
	}
	return "python"
}

func trimF(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func tail(s string) string {
	if len(s) > 600 {
		return s[len(s)-600:]
	}
	return s
}
