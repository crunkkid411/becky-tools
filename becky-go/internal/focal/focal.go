// Package focal answers "where should the camera point when there is no face?"
//
// Every framing signal becky has is a PERSON detector — MediaPipe Pose,
// InsightFace, LR-ASD. On a shot with nobody in it they all return nothing and
// the crop falls back to dead centre, which on a 16:9 source is the "split the
// difference" framing Jordan's own edit specifically avoids. Measured on the
// BLINDFOLD render, five of seventeen spans took that fallback and landed on a
// green wall and a striped sleeve.
//
// Jordan's RULE 4 is about exactly this case:
//
//	"the camera changes to POV style, where NO FACES are visible - at this point
//	 the clip itself is obviously meant to be the focal point... The correct
//	 framing is to ensure that Robby is shown standing up and walking away, but
//	 making sure the snake is the focal point at the very first frame in which it
//	 starts to move"
//
// The thing he describes is defined by its MOTION, and motion is measurable
// without any model at all. This package takes the frame-difference signal
// becky-motion already computes — which collapses each pair of frames to ONE
// number — and keeps the spatial part it throws away.
//
// It deliberately answers only the horizontal question. A 9:16 crop of 16:9 has
// its width to spend and (before a punch-in) no vertical freedom at all, so X is
// the decision that matters and the one the measurement supports.
//
// It refuses more often than it answers, on purpose. A wrong focal point is
// worse than a centre crop: centre is merely uninformative, wrong is a shot of
// the wall while the joke happens off-frame. Two things have to hold before it
// commits — the moving region has to be a REGION rather than the whole frame
// (a whole-frame change is a camera move, not a subject), and the centroid has
// to STAY somewhere across the span rather than jumping around.
package focal

import (
	"fmt"
	"math"
	"os/exec"
	"sort"
	"strings"
)

const (
	// Grid is the NxN grayscale resolution each frame is decoded to. Same value
	// cmd/motion uses; a 64x64 gray frame is 4KB, so a whole span costs almost
	// nothing to hold. The aspect squash is harmless: X maps back linearly.
	Grid = 64

	// hotFrac: a cell counts as "moving" when its difference is at least this
	// fraction of the frame's peak difference. Half the peak keeps the actual
	// subject and drops compression shimmer.
	hotFrac = 0.5

	// maxHotArea: if more than this fraction of the grid is moving, the whole
	// picture changed — a camera pan, a cut, a light change — and there is no
	// object to point at. Refuse rather than aim at the centre of everything.
	maxHotArea = 0.55

	// minHotArea: a couple of cells is noise, not a subject.
	minHotArea = 0.004

	// maxSpread: how far the per-pair centroid may wander, as a fraction of
	// frame width, before the aim is called unstable. 0.08 is about half a
	// 9:16 crop's width on a 16:9 source, so a wobble wider than this could
	// point the crop somewhere genuinely different frame to frame.
	maxSpread = 0.08

	// minPairs: fewer usable frame pairs than this is not evidence.
	minPairs = 8
)

// Aim is where to point, and whether to believe it.
type Aim struct {
	X      float64 // 0..1 across the frame width
	Stable bool
	Spread float64 // median absolute deviation of the per-pair centroid, in frame widths
	Pairs  int     // usable frame pairs the aim was measured from
	Frames int     // frames decoded
	Reason string  // why it is not stable, when it is not
}

// CentroidX returns the horizontal centre of the moving region between two
// grayscale frames, as a fraction of frame width, plus the fraction of the grid
// that was moving. ok is false when there is nothing to point at — either the
// motion is noise-sized or it covers so much of the frame that it is the camera
// moving rather than a subject.
//
// Pure, so the whole decision rule is testable without ffmpeg or a video.
func CentroidX(prev, cur []byte, grid int) (x, hotArea float64, ok bool) {
	if grid <= 0 || len(prev) != grid*grid || len(cur) != grid*grid {
		return 0, 0, false
	}
	diff := make([]float64, grid*grid)
	peak := 0.0
	for i := range diff {
		d := math.Abs(float64(cur[i]) - float64(prev[i]))
		diff[i] = d
		if d > peak {
			peak = d
		}
	}
	if peak < 6 { // nothing moved beyond codec dithering
		return 0, 0, false
	}

	thr := peak * hotFrac
	var sumW, sumWX, hot float64
	for i, d := range diff {
		if d < thr {
			continue
		}
		hot++
		col := float64(i % grid)
		sumW += d
		sumWX += d * col
	}
	hotArea = hot / float64(grid*grid)
	if hotArea < minHotArea || hotArea > maxHotArea || sumW == 0 {
		return 0, hotArea, false
	}
	// +0.5 puts the centroid at the middle of its cell rather than its left edge.
	return (sumWX/sumW + 0.5) / float64(grid), hotArea, true
}

// Summarise turns per-pair centroids into one aim plus an honest stability call.
func Summarise(xs []float64, frames int) Aim {
	a := Aim{Pairs: len(xs), Frames: frames}
	if len(xs) < minPairs {
		a.Reason = fmt.Sprintf("only %d usable frame pair(s), need %d", len(xs), minPairs)
		return a
	}
	med := medianOf(xs)
	devs := make([]float64, len(xs))
	for i, x := range xs {
		devs[i] = math.Abs(x - med)
	}
	a.X = med
	a.Spread = medianOf(devs)
	if a.Spread > maxSpread {
		a.Reason = fmt.Sprintf("the moving thing does not stay put (spread %.3f of frame width, limit %.3f)",
			a.Spread, maxSpread)
		return a
	}
	a.Stable = true
	return a
}

// Find decodes [start,end] of video at its own frame rate as a Grid x Grid
// grayscale stream and measures where the motion is.
//
// fps is the video's TRUE frame rate — this is a measurement of what the viewer
// sees, and becky's rule is that those happen at the footage's own rate.
func Find(ffmpeg, video string, start, end, fps float64) (Aim, error) {
	dur := end - start
	if dur <= 0 {
		return Aim{Reason: "empty span"}, nil
	}
	if fps <= 0 {
		fps = 30
	}
	frames, err := decodeGrid(ffmpeg, video, start, dur, fps)
	if err != nil {
		return Aim{Reason: "could not decode the span"}, err
	}
	if len(frames) < 2 {
		return Aim{Frames: len(frames), Reason: "fewer than two frames decoded"}, nil
	}
	var xs []float64
	for i := 1; i < len(frames); i++ {
		if x, _, ok := CentroidX(frames[i-1], frames[i], Grid); ok {
			xs = append(xs, x)
		}
	}
	return Summarise(xs, len(frames)), nil
}

func decodeGrid(ffmpeg, video string, start, dur, fps float64) ([][]byte, error) {
	args := []string{"-y"}
	if start > 0 {
		args = append(args, "-ss", fmt.Sprintf("%.3f", start))
	}
	args = append(args, "-i", video, "-t", fmt.Sprintf("%.3f", dur),
		"-an",
		"-vf", fmt.Sprintf("fps=%g,scale=%d:%d,format=gray", fps, Grid, Grid),
		"-f", "rawvideo", "-pix_fmt", "gray",
		"-loglevel", "error", "-")

	cmd := exec.Command(ffmpeg, args...)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(errBuf.String()))
	}
	n := Grid * Grid
	var frames [][]byte
	for off := 0; off+n <= len(out); off += n {
		f := make([]byte, n)
		copy(f, out[off:off+n])
		frames = append(frames, f)
	}
	return frames, nil
}

func medianOf(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	m := len(c) / 2
	if len(c)%2 == 1 {
		return c[m]
	}
	return (c[m-1] + c[m]) / 2
}
