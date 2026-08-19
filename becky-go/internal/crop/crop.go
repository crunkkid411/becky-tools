// Package crop turns a video window into a 9:16 (or any aspect) crop that
// FOLLOWS the subject, and into the ffmpeg arguments that render it.
//
// The subject-finding is MediaPipe Pose in internal/pyhelpers/crop_path.py; this
// package owns the deterministic half — running that helper, turning its sampled
// path into an ffmpeg filter, and degrading honestly when the helper cannot run.
//
// Framing on the body rather than the face is the point. A crop centred on a face
// box puts the head dead centre and cuts off gestures, which is the single most
// recognisable tell of an auto-generated short. Shoulders-and-headroom is what an
// operator frames, and becky had no body-level signal at all before this.
package crop

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strings"

	"becky-go/internal/config"
	"becky-go/internal/pyhelpers"
)

// Rect is one crop rectangle in SOURCE pixels at time T (seconds from the start
// of the window). Dimensions are even, because yuv420p cannot encode odd ones.
type Rect struct {
	T float64 `json:"t"`
	X int     `json:"x"`
	Y int     `json:"y"`
	W int     `json:"w"`
	H int     `json:"h"`
}

// Path is the sampled camera path over one window.
type Path struct {
	OK      bool    `json:"ok"`
	Reason  string  `json:"reason,omitempty"`
	SrcW    int     `json:"src_w"`
	SrcH    int     `json:"src_h"`
	FPS     float64 `json:"fps"`
	Aspect  float64 `json:"aspect"`
	Sampled int     `json:"sampled"`
	Found   int     `json:"found"`
	// LongestGap is the longest unbroken stretch, in seconds, where the subject
	// was not detected at all. Gate on this rather than on average coverage:
	// misses cluster, and a clip that is 92% covered can still contain a dead
	// patch that renders a stale crop for seconds at a time.
	LongestGap float64 `json:"longest_gap_s"`
	Rects      []Rect  `json:"path"`
}

// Options configure one crop-path run.
type Options struct {
	Video  string
	Start  float64
	End    float64
	Aspect string  // "9:16"
	FPS    float64 // samples per second
	Model  string  // pose_landmarker_*.task
	// CutTimes are hard-cut boundaries ALREADY KNOWN inside [Start,End]
	// (internal/shotcut, source-absolute seconds) — the source is already
	// edited, and the camera-path smoother must reset at each one instead of
	// blending one shot's framing into the next across the cut (Finding 2,
	// research/jordan-edit-reverse-engineered.md). Empty is the common case:
	// most callers already scope one Run to exactly one shot, so there is
	// nothing inside the window to split.
	CutTimes []float64
}

// Coverage reports the fraction of sampled instants where a body was actually
// found. It is the honesty signal for this stage: a path built mostly from
// carried-forward guesses should not be presented as a followed subject.
func (p Path) Coverage() float64 {
	if p.Sampled == 0 {
		return 0
	}
	return float64(p.Found) / float64(p.Sampled)
}

// Run executes the pose helper and returns the sampled camera path. A helper that
// cannot run returns an error; the caller degrades to StaticCenter and says so,
// rather than crashing (becky's degrade-never-crash rule).
func Run(cfg config.Config, opt Options) (Path, error) {
	script, err := pyhelpers.Materialize("crop_path.py", pyhelpers.CropPath)
	if err != nil {
		return Path{}, err
	}
	if opt.Aspect == "" {
		opt.Aspect = "9:16"
	}
	// FPS 0 is passed straight through: the helper reads it as "every frame",
	// which is the right default for an offline edit. Clamping it to 8 here is
	// what made the crop trail the subject - it only knew where he was 8 times a
	// second on 30 fps footage.
	args := []string{script,
		"--video", opt.Video,
		"--model", opt.Model,
		"--start", trimF(opt.Start),
		"--end", trimF(opt.End),
		"--aspect", opt.Aspect,
		"--fps", trimF(opt.FPS),
	}
	if len(opt.CutTimes) > 0 {
		parts := make([]string, len(opt.CutTimes))
		for i, c := range opt.CutTimes {
			parts[i] = trimF(c)
		}
		args = append(args, "--cut-times", strings.Join(parts, ","))
	}

	cmd := exec.Command(pythonFor(cfg), args...)
	cmd.Env = childEnv(cfg)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return Path{}, fmt.Errorf("crop helper failed: %v\n%s", err, tail(stderr.String()))
	}

	var p Path
	if err := json.Unmarshal([]byte(lastJSONLine(stdout.String())), &p); err != nil {
		return Path{}, fmt.Errorf("could not parse crop helper output: %w\n%s", err, tail(stdout.String()))
	}
	if !p.OK {
		return p, fmt.Errorf("crop helper declined: %s", p.Reason)
	}
	if len(p.Rects) == 0 {
		return p, fmt.Errorf("crop helper returned no path")
	}
	return p, nil
}

// StaticCenter is the honest degrade: the largest centred rect of the target
// aspect. It follows nothing, so a caller that uses it must SAY so in its output
// instead of implying the subject was tracked.
func StaticCenter(srcW, srcH int, aspect float64) Rect {
	w := float64(srcW)
	h := w / aspect
	if h > float64(srcH) {
		h = float64(srcH)
		w = h * aspect
	}
	iw, ih := int(w)&^1, int(h)&^1
	return Rect{X: ((srcW - iw) / 2) &^ 1, Y: ((srcH - ih) / 2) &^ 1, W: iw, H: ih}
}

// ParseAspect turns "9:16" into 0.5625 (width/height).
func ParseAspect(s string) (float64, error) {
	if s == "" {
		return 9.0 / 16.0, nil
	}
	if i := strings.IndexByte(s, ':'); i >= 0 {
		var w, h float64
		if _, err := fmt.Sscanf(s[:i], "%g", &w); err != nil {
			return 0, fmt.Errorf("bad aspect %q", s)
		}
		if _, err := fmt.Sscanf(s[i+1:], "%g", &h); err != nil || h == 0 {
			return 0, fmt.Errorf("bad aspect %q", s)
		}
		return w / h, nil
	}
	var v float64
	if _, err := fmt.Sscanf(s, "%g", &v); err != nil || v <= 0 {
		return 0, fmt.Errorf("bad aspect %q", s)
	}
	return v, nil
}

// maxKeyframes caps how deep the ffmpeg expression may nest. ffmpeg's expression
// parser fails outright on a deeply nested if() chain, and at 8 samples/sec a
// 31-second clip is 250 samples - which is exactly how the first real run failed.
// Reducing to significant points keeps the camera path identical to the eye while
// keeping the expression well inside what ffmpeg will parse.
const maxKeyframes = 48

// reduce drops samples that lie on the straight line between their neighbours,
// within tol pixels. The smoother deliberately produces long still HOLDS broken by
// short glides, so a whole hold collapses to its two endpoints and a glide keeps
// only its corners. Shape is preserved; the sample count is not.
func reduce(rects []Rect, pick func(Rect) int, tol float64) []Rect {
	if len(rects) < 3 {
		return rects
	}
	out := []Rect{rects[0]}
	for i := 1; i < len(rects)-1; i++ {
		prev, cur, next := out[len(out)-1], rects[i], rects[i+1]
		span := next.T - prev.T
		if span <= 0 {
			continue
		}
		// Where would interpolation put this point if it were dropped?
		f := (cur.T - prev.T) / span
		want := float64(pick(prev)) + f*float64(pick(next)-pick(prev))
		if math.Abs(want-float64(pick(cur))) > tol {
			out = append(out, cur)
		}
	}
	return append(out, rects[len(rects)-1])
}

// FilterExpr builds the ffmpeg `crop` x (or y) expression: piecewise-linear
// interpolation between the significant points of the path, so the crop moves
// smoothly between samples instead of stepping at each one.
//
// The rects are already smoothed by the helper; this fills the gaps between
// samples and then reduces the result to the points that actually matter. Built
// innermost-last so evaluation short-circuits on the first matching segment.
func FilterExpr(rects []Rect, pick func(Rect) int) string {
	if len(rects) == 0 {
		return "0"
	}
	if len(rects) == 1 {
		return fmt.Sprintf("%d", pick(rects[0]))
	}
	// Raise the tolerance until the expression is something ffmpeg will parse. A
	// couple of pixels of slack on a 1080-wide crop is invisible; a filter graph
	// ffmpeg rejects is a short that never renders.
	kept := rects
	for tol := 0.5; tol <= 64; tol *= 2 {
		kept = reduce(rects, pick, tol)
		if len(kept) <= maxKeyframes {
			break
		}
	}
	if len(kept) > maxKeyframes {
		kept = kept[:maxKeyframes]
	}
	if len(kept) == 1 {
		return fmt.Sprintf("%d", pick(kept[0]))
	}

	e := fmt.Sprintf("%d", pick(kept[len(kept)-1]))
	for i := len(kept) - 2; i >= 0; i-- {
		t0, t1 := kept[i].T, kept[i+1].T
		v0, v1 := pick(kept[i]), pick(kept[i+1])
		if t1 <= t0 {
			continue // duplicate timestamps cannot be interpolated across
		}
		if v0 == v1 {
			// A held value needs no ramp; this keeps the expression short over
			// the long still holds the smoother deliberately produces.
			e = fmt.Sprintf("if(lt(t,%s),%d,%s)", trimF(t1), v0, e)
			continue
		}
		seg := fmt.Sprintf("(%d+(%d)*(t-%s)/%s)", v0, v1-v0, trimF(t0), trimF(t1-t0))
		e = fmt.Sprintf("if(lt(t,%s),%s,%s)", trimF(t1), seg, e)
	}
	return e
}

// SendcmdFile builds an ffmpeg `sendcmd` script that moves the crop EVERY frame.
//
// This replaces an expression-based crop that was capped at 48 keyframes for a
// whole clip, with the tolerance escalating to 64px and a hard truncation after
// that - so on a busy shot the crop silently FROZE at the last kept keyframe and
// stayed there. That is not a tuning knob, it is the tracker visibly giving up,
// and it is indistinguishable from lag.
//
// A command file has no length limit, so the rendered crop follows the tracked
// path exactly: no decimation, no tolerance, no cap. A line is emitted only when
// the rect actually changes, which keeps the file small over the long holds the
// smoother produces without losing a single real move.
func SendcmdFile(rects []Rect) string {
	var b strings.Builder
	lastX, lastY := -1, -1
	for _, r := range rects {
		if r.X == lastX && r.Y == lastY {
			continue
		}
		fmt.Fprintf(&b, "%s crop x %d, crop y %d;\n", trimF(r.T), r.X, r.Y)
		lastX, lastY = r.X, r.Y
	}
	if b.Len() == 0 && len(rects) > 0 {
		fmt.Fprintf(&b, "0 crop x %d, crop y %d;\n", rects[0].X, rects[0].Y)
	}
	return b.String()
}

// CropSize returns the constant crop dimensions for a path: the smallest rect, so
// the window never asks for pixels outside the frame while x/y animate.
func CropSize(rects []Rect) (int, int) {
	w, h := rects[0].W, rects[0].H
	for _, r := range rects {
		if r.W < w {
			w = r.W
		}
		if r.H < h {
			h = r.H
		}
	}
	return w, h
}

// FilterChain returns the -vf value: read the per-frame crop positions from
// cmdsName, crop, then scale to the output size. cmdsName must be a BARE
// filename and ffmpeg must run with its directory as the working directory -
// sendcmd's parser treats a Windows drive colon as its own separator, so an
// absolute path fails with "No such file or directory".
func FilterChain(rects []Rect, outW, outH int, cmdsName string) string {
	w, h := CropSize(rects)
	return fmt.Sprintf("sendcmd=f=%s,crop=%d:%d:%d:%d,scale=%d:%d:flags=lanczos",
		cmdsName, w, h, rects[0].X, rects[0].Y, outW, outH)
}

// RenderArgs builds the ffmpeg argument list for one short. Seeking happens
// BEFORE -i (fast, keyframe-accurate) and the duration is passed as -t, matching
// how internal/reel renders.
func RenderArgs(src string, start, dur float64, chain, out string) []string {
	return []string{
		"-y",
		"-ss", trimF(start),
		"-t", trimF(dur),
		"-i", src,
		"-vf", chain,
		"-c:v", "libx264", "-preset", "medium", "-crf", "18",
		"-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "160k",
		"-movflags", "+faststart",
		out,
	}
}

// OutputSize picks the render size for a target aspect: 1080 on the short edge,
// which is the standard for every vertical social format.
func OutputSize(aspect float64) (int, int) {
	if aspect <= 1 {
		h := int(math.Round(1080/aspect)) &^ 1
		return 1080, h
	}
	w := int(math.Round(1080*aspect)) &^ 1
	return w, 1080
}

func trimF(v float64) string {
	s := fmt.Sprintf("%.6f", v)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

func pythonFor(cfg config.Config) string {
	// The pose stack lives beside insightface/cv2 in the face interpreter, which
	// is the one config already resolves with FacePyLib on PYTHONPATH.
	if cfg.FacePython != "" {
		return cfg.FacePython
	}
	return cfg.Python
}

func childEnv(cfg config.Config) []string {
	env := os.Environ()
	if cfg.FacePyLib != "" {
		env = append(env, "PYTHONPATH="+cfg.FacePyLib+string(os.PathListSeparator)+os.Getenv("PYTHONPATH"))
	}
	return env
}

func lastJSONLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if strings.HasPrefix(l, "{") {
			return l
		}
	}
	return strings.TrimSpace(s)
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 800 {
		return "…" + s[len(s)-800:]
	}
	return s
}
