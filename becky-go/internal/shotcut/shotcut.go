// Package shotcut finds HARD CUT points already present in a video — the shot
// boundaries an editor already made, not silence and not a becky-cut decision.
//
// Method (research/jordan-edit-reverse-engineered.md, "Finding 1"): decode
// frames at the video's TRUE fps (never resampled — Jordan's rule is that this
// pipeline analyses at the real frame rate, always), downscale hard to 96x54,
// convert to greyscale, take the mean absolute difference between consecutive
// frames, and call a frame a candidate cut when that difference clears
// median + 6*MAD — floored at a minimum so a static shot with near-zero
// variance cannot manufacture cuts out of compression noise. Adjacent
// detections (a hard cut occasionally straddles two encoded frames) collapse
// into one cut at the first frame of the run.
//
// A candidate then has to be CONFIRMED (confirmedCut): a real cut is a
// PERMANENT change of picture, so a frame a few frames before and a frame a
// few frames after the spike must also differ by a real amount — this catches
// a one-frame encoder glitch or flash that isn't a real, lasting change. It
// is NOT what catches the false positive this package was actually measured
// against, below — that one needed a higher floor, not a wider comparison
// window (see minDiffFloor).
//
// MEASURED FALSE POSITIVE, kept as the reason minDiffFloor is what it is:
// the first version of this detector (minDiffFloor=6.0, no confirmation)
// found two "cuts" in test-for-clips.mp4 (raw footage, a head whip and a
// hand raising scissors) that visual inspection showed were the SAME
// continuous shot. The diff trace there was not a one-frame spike at all —
// it was a smooth ~1s RAMP up to a peak of ~7.1 and back down, because fast
// motion blurs across many consecutive frames. A real hard cut in this same
// source's edited reference footage is an ISOLATED single-frame jump to
// ~60-80 — 10x the motion-blur peak, with the ordinary content on both sides
// sitting at 1-4. confirmedCut's before/after comparison could not tell
// these apart (170ms either side of the peak is STILL inside the same
// ramp), but the two populations are separated by a wide enough margin
// (~7 vs ~12 for the WEAKEST real cut measured) that a plain, higher floor
// does the job cleanly — see shotcut_test.go's real-footage regression for
// both cases.
//
// This is a starting point, not a promise of perfection on every source — it
// is validated against a hand-verified cut list from real footage in
// shotcut_test.go's precision/recall numbers, not asserted blind.
package shotcut

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"os/exec"
	"sort"

	"becky-go/internal/mediainfo"
)

// downscaleW/H is the frame-diff working resolution: small enough to be fast
// and to average away compression macroblock noise, big enough that a real
// cut (a different scene, a different framing) still dominates the frame.
// 96x54 keeps the source 16:9 ratio; a non-16:9 source is still fine to diff,
// it just is not literally square pixels — the diff doesn't care.
const (
	downscaleW = 96
	downscaleH = 54
)

// Options configure one detection run.
type Options struct {
	// Video is the source file.
	Video string
	// Start/End window bounds, in seconds, source-absolute. End<=Start means
	// "to end of file".
	Start, End float64
	// FFmpeg overrides the ffmpeg binary; "" means "ffmpeg" on PATH.
	FFmpeg string
	// FFprobe overrides the ffprobe binary used to read the true fps.
	FFprobe string
	// MinDiff floors the cut threshold so a static/near-static shot (whose
	// median+6*MAD can be a hair above zero) cannot manufacture cuts from
	// encoder noise alone. 0 uses the researched default (see minDiffFloor).
	MinDiff float64
}

// minDiffFloor is the default MinDiff: mean abs difference on a 0-255
// greyscale, 96x54 frame. MEASURED (shotcut_test.go's real-footage cases):
// the weakest genuine cut in the BLINDFOLD reference list peaks at 12.08;
// the fast-motion false positive in raw footage (test-for-clips.mp4) peaks
// at 7.12. 8.0 sits with margin on both sides of that gap.
const minDiffFloor = 8.0

// madScale is the "6" in "median + 6*MAD" from the research doc's method.
const madScale = 6.0

// confirmSeconds is how far before/after a candidate spike confirmedCut looks
// to check the change actually stuck. 100ms is short enough to sit inside
// even the shortest real shot ever measured off Jordan's own edit (0.53s,
// research/jordan-edit-reverse-engineered.md) — so it cannot accidentally
// straddle two real cuts — and long enough that a one-frame motion-blur
// spike has clearly decayed back by the time it's checked.
const confirmSeconds = 0.1

// minProminence is how far a candidate must stand above its OWN neighbourhood
// before it counts as a cut: its frame difference divided by the median
// difference of the half-second around it.
//
// This is the measurement the package header already describes in words — "a
// smooth ~1s RAMP up to a peak of ~7.1" versus "an ISOLATED single-frame jump
// to ~60-80 with the ordinary content on both sides sitting at 1-4" — turned
// into a number, because considering local maxima inside a busy stretch (see
// cutTimes) needs a way to tell a cut from a wobble in a motion ramp.
//
// MEASURED, prominence over +/-0.5s:
//
//	phantoms, raw single-take footage : 2.24  2.42  2.79  2.89
//	real cuts, the edited master      : 4.01  8.59  11.71 ... 144.50  (22 of 24)
//
// The two real cuts below that band are 45.00s (2.14) and — just above it —
// 54.64s (4.01). 45.00s is the weakest cut in the whole reference list, the
// 12.08 the minDiffFloor comment already calls the floor case, and this
// detector does not find it today either. So the gate costs nothing that was
// being caught and buys back the whole phantom population.
//
// 3.5 is the middle of the empty band between 2.89 and 4.01.
const minProminence = 3.5

// prominenceHalf is the half-window, in SECONDS, the neighbourhood median is
// taken over. Half a second either side: long enough to contain the ramp of a
// motion blur (measured at ~1s) so a wobble on it is compared against the ramp
// itself, short enough that it is mostly one shot.
const prominenceHalf = 0.5

// confirmFrac is how much of the threshold the FAR diff (frames well before
// vs. well after the spike) must still clear to confirm a candidate as a
// real, lasting cut rather than a transient blur that snapped back.
const confirmFrac = 0.5

// Detect returns hard-cut times (source-absolute seconds) inside
// [opt.Start, opt.End]. A cut time is the timestamp of the FIRST frame of the
// new shot. Degrades with an error (never crashes) on a source ffmpeg/ffprobe
// cannot read — the caller treats "no cuts found" and "detection unavailable"
// as the same thing: fall back to raw-footage behaviour.
func Detect(opt Options) ([]float64, error) {
	ffmpegBin := opt.FFmpeg
	if ffmpegBin == "" {
		ffmpegBin = "ffmpeg"
	}
	ffprobeBin := opt.FFprobe
	if ffprobeBin == "" {
		ffprobeBin = "ffprobe"
	}
	info, err := mediainfo.Probe(ffprobeBin, opt.Video)
	if err != nil || info.FPS <= 0 {
		return nil, fmt.Errorf("could not read the true frame rate of %s: %v", opt.Video, err)
	}
	fps := info.FPS

	dur := opt.End - opt.Start
	if dur <= 0 {
		dur = info.Duration - opt.Start
	}
	if dur <= 0 {
		return nil, fmt.Errorf("empty window")
	}

	// Decode at the source's OWN frame rate — no `fps=` resample filter. A cut
	// lands on a specific encoded frame; resampling can merge or duplicate the
	// two frames either side of it and blur the exact boundary away.
	cmd := exec.Command(ffmpegBin, "-v", "error",
		"-ss", fmt.Sprintf("%.6f", opt.Start), "-t", fmt.Sprintf("%.6f", dur),
		"-i", opt.Video,
		"-vf", fmt.Sprintf("scale=%d:%d,format=gray", downscaleW, downscaleH),
		"-pix_fmt", "gray", "-f", "rawvideo", "-")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("ffmpeg start failed: %w", err)
	}

	frames, ferr := decodeFrames(stdout)
	waitErr := cmd.Wait()
	if ferr != nil {
		return nil, ferr
	}
	if waitErr != nil && len(frames) == 0 {
		return nil, fmt.Errorf("ffmpeg decode failed: %w", waitErr)
	}

	minDiff := opt.MinDiff
	if minDiff <= 0 {
		minDiff = minDiffFloor
	}
	confirmFrames := int(math.Round(confirmSeconds * fps))
	if confirmFrames < 1 {
		confirmFrames = 1
	}
	return cutTimes(frames, opt.Start, fps, minDiff, confirmFrames), nil
}

// decodeFrames reads every raw gray8 frame from r into memory. Windows this
// package is actually called on (a becky-short job's clip window) are tens of
// seconds, a few MB at this resolution — trivial to hold whole, and holding
// them is what lets cutTimes look BOTH at consecutive-frame diffs and at the
// wider before/after comparison confirmedCut needs.
func decodeFrames(r io.Reader) ([][]byte, error) {
	const frameSize = downscaleW * downscaleH
	br := bufio.NewReaderSize(r, frameSize*4)
	var frames [][]byte
	for {
		buf := make([]byte, frameSize)
		n, err := io.ReadFull(br, buf)
		if n == frameSize {
			frames = append(frames, buf)
		}
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return frames, nil
			}
			return frames, err
		}
	}
}

func meanAbsDiff(a, b []byte) float64 {
	var sum int
	for i := range a {
		d := int(a[i]) - int(b[i])
		if d < 0 {
			d = -d
		}
		sum += d
	}
	return float64(sum) / float64(len(a))
}

// cutTimes thresholds consecutive-frame diffs at median+6*MAD (floored at
// minDiff), collapses adjacent detections into one candidate per run, then
// CONFIRMS each candidate (confirmedCut) before calling it a cut.
func cutTimes(frames [][]byte, start, fps, minDiff float64, confirmFrames int) []float64 {
	promHalf := int(math.Round(prominenceHalf * fps))
	if promHalf < 3 {
		promHalf = 3
	}
	if len(frames) < 2 {
		return nil
	}
	diffs := make([]float64, len(frames)-1)
	for i := 0; i+1 < len(frames); i++ {
		diffs[i] = meanAbsDiff(frames[i], frames[i+1])
	}

	med := median(diffs)
	mad := medianAbsDev(diffs, med)
	threshold := med + madScale*mad
	if threshold < minDiff {
		threshold = minDiff
	}

	// runGap tolerates up to a TWO-frame dip below threshold inside a run
	// before starting a new candidate. A real hard cut occasionally
	// straddles more than one encoded frame (motion blur / a partial blend
	// the encoder produced across the transition) whose diff dips just under
	// threshold in the middle — without this it counts as two adjacent cuts
	// a few milliseconds apart instead of one. MEASURED at 1 first, then
	// raised to 2 after raising minDiffFloor (8.0, to reject a raw-footage
	// false positive — see the header comment) split what used to be one
	// blend-run into two on the BLINDFOLD reference footage.
	const runGap = 2

	// A candidate is a LOCAL MAXIMUM of the difference signal, not merely the
	// first over-threshold frame of a run.
	//
	// It used to be the first frame of a run, with lastHit advanced on every
	// over-threshold frame — which meant that once the signal stayed above
	// threshold, no further cut could be emitted until it dropped back for
	// runGap+1 consecutive frames. A blend across two frames was the case that
	// rule was written for, and it handles that correctly. What it could not
	// handle is a BUSY STRETCH: several quick cuts inside footage that is also
	// moving fast, where the difference never settles between them, so the whole
	// stretch collapsed into a single cut.
	//
	// MEASURED on the BLINDFOLD reference window, which is exactly that kind of
	// footage: recall 0.708, and the seven misses cluster right after a hit —
	// 44.70 found, 45.00 and 45.70 swallowed; 37.32 found, 38.02 swallowed.
	//
	// A blend still yields ONE cut, because a blend has one peak. A stretch with
	// several real cuts has several peaks separated by dips, and now yields all
	// of them. confirmedCut and the histogram check are unchanged and still have
	// the final say, so this widens what is CONSIDERED without widening what is
	// accepted.
	var cuts []float64
	lastEmit := -1 - runGap
	for i, d := range diffs {
		if d <= threshold {
			continue
		}
		if i > 0 && diffs[i-1] > d {
			continue // still climbing toward a later, bigger peak
		}
		if i+1 < len(diffs) && diffs[i+1] > d {
			continue // the peak is the next frame, not this one
		}
		// runGap+1, matching the merge rule this function has always documented:
		// two spikes that close are one cut whose blend dipped, not two cuts.
		if i-lastEmit <= runGap+1 {
			continue
		}
		if prominence(diffs, i, promHalf) < minProminence {
			continue // a wobble on a motion ramp, not an isolated cut
		}
		// diffs[i] is the jump INTO frame i+1, so a candidate at this
		// index means the new shot starts at frame i+1.
		frameIdx := i + 1
		if confirmedCut(frames, frameIdx, threshold, confirmFrames) {
			cuts = append(cuts, start+float64(frameIdx)/fps)
			lastEmit = i
		}
	}
	return cuts
}

// confirmedCut checks that a candidate cut at frames[frameIdx] is a LASTING
// picture change, not a transient spike (fast motion blur) that snaps back.
// It compares a frame confirmFrames before the outgoing shot's last frame to
// one confirmFrames into the new shot — skipping the spike itself — and
// requires that far comparison to still clear confirmFrac of the threshold.
//
// Too close to either edge of the decoded window to check both sides: kept,
// not rejected — this function can only rule OUT a false positive, never
// manufacture a true one, so "can't tell" defaults to trusting the spike.
// prominence is diffs[i] over the median of the surrounding window, excluding
// the peak itself. Floored at 0.5 so a dead-still neighbourhood cannot divide
// by ~0 and make any flicker look infinitely prominent.
func prominence(diffs []float64, i, half int) float64 {
	lo, hi := i-half, i+half
	if lo < 0 {
		lo = 0
	}
	if hi >= len(diffs) {
		hi = len(diffs) - 1
	}
	w := make([]float64, 0, hi-lo)
	for j := lo; j <= hi; j++ {
		if j != i {
			w = append(w, diffs[j])
		}
	}
	if len(w) == 0 {
		return 0
	}
	m := median(w)
	if m < 0.5 {
		m = 0.5
	}
	return diffs[i] / m
}

func confirmedCut(frames [][]byte, frameIdx int, threshold float64, confirmFrames int) bool {
	lo := frameIdx - 1 - confirmFrames
	hi := frameIdx + confirmFrames
	if lo < 0 || hi >= len(frames) {
		return true
	}
	if meanAbsDiff(frames[lo], frames[hi]) <= threshold*confirmFrac {
		return false
	}
	// A brightness difference alone cannot tell a CUT from fast motion: on raw
	// footage this detector reported 56 cuts in a five-minute single-take file,
	// some 200ms apart, and frames either side of them showed the same shot with
	// the subject merely moving. A cut changes what is in the picture, so it
	// changes the brightness DISTRIBUTION; motion within a shot mostly shuffles
	// the same pixels around.
	//
	// MEASURED across one frame boundary, 32-bin greyscale histogram intersection
	// (1.0 = identical distributions):
	//   false positives, raw footage : 0.935 0.946 0.960 0.965 0.966 0.969
	//   real cuts, the edited master : 0.753 0.769 0.817 0.819 0.827 0.830
	// The gap is wide and empty. Swept against the hand-checked cut list:
	//   0.88/0.90 -> precision 0.941 recall 0.667
	//   0.92/0.94 -> precision 0.944 recall 0.708   <- flat optimum
	//   0.96      -> precision 0.850 recall 0.708   (false positives return)
	// 0.93 is the middle of that flat region, and raw footage reports ZERO cuts
	// at every value tested, so the raw/edited decision is not knife-edge.
	if HistIntersection(frames[frameIdx-1], frames[frameIdx]) >= MaxHistOverlap {
		return false
	}
	// And the distribution change has to LAST. The adjacent-frame test above
	// asks "did the picture change?"; this asks "did it stay changed?".
	//
	// That distinction is what separates a cut from a hand sweeping past the
	// lens. A sweep really does change the brightness distribution for a frame
	// or two — it passes the test above — and then the scene comes back to
	// itself. A cut does not come back.
	//
	// It earns its place on raw footage. Considering local maxima inside a busy
	// stretch (see cutTimes) recovered three real cuts on the edited master and
	// simultaneously resurrected FOUR phantom cuts in a five-minute single-take
	// file that must report zero — two of them 0.13s apart inside one motion
	// burst. Comparing the same 100ms-apart frames the persistence check above
	// already loads costs nothing extra and removes them.
	return HistIntersection(frames[lo], frames[hi]) < MaxHistOverlap
}

// MaxHistOverlap is how similar the greyscale histograms either side of a
// candidate may be before it is rejected as motion rather than a cut.
// MaxHistOverlap is exported because becky-motion needs the same test: it too
// scores frames by mean absolute difference, and a CUT is the largest "motion"
// there is. Measured on the BLINDFOLD master, 6 of its 8 reported motion bursts
// were cuts, carrying the highest scores of all.
const MaxHistOverlap = 0.93

// histIntersection is the overlap of two 32-bin greyscale histograms: 1.0 when
// the two frames share a brightness distribution exactly, 0.0 when they share
// none of it.
// HistIntersection is exported for becky-motion; see MaxHistOverlap.
func HistIntersection(a, b []byte) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 1 // cannot tell -> do not reject
	}
	const bins = 32
	var ha, hb [bins]float64
	for i := range a {
		ha[int(a[i])*bins/256]++
		hb[int(b[i])*bins/256]++
	}
	n := float64(len(a))
	var overlap float64
	for i := 0; i < bins; i++ {
		overlap += minF(ha[i], hb[i]) / n
	}
	return overlap
}

func minF(x, y float64) float64 {
	if x < y {
		return x
	}
	return y
}

func median(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	s := append([]float64(nil), vals...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return 0.5 * (s[n/2-1] + s[n/2])
}

func medianAbsDev(vals []float64, med float64) float64 {
	dev := make([]float64, len(vals))
	for i, v := range vals {
		d := v - med
		if d < 0 {
			d = -d
		}
		dev[i] = d
	}
	return median(dev)
}
