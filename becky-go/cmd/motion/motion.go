package main

// motion.go computes a per-frame motion-energy signal at TRUE source fps using a
// deterministic dense frame-difference. This is the zero-VRAM, no-model core of
// becky-motion: it cannot miss a sub-second burst by construction because it looks
// at every frame, not a 1-fps subset.
//
// Method (deterministic, offline, source never modified):
//  1. ffmpeg decodes the clip (read-only) at source fps to a small WxW grayscale raw
//     stream — exactly the proven raw-gray pattern used by cmd/events, but DENSE
//     (every frame) instead of 1 fps.
//  2. Motion energy per frame = mean absolute per-pixel intensity difference vs the
//     previous frame, normalized to 0..1. High = lots of pixels changed fast.
//
// The CUDA decode path is best-effort and transparently falls back to CPU, so the
// signal always computes. No OpenCV/optical-flow dependency is required (the spec's
// "degrade gracefully: no opencv -> ffmpeg-only frame-diff" — this IS that path,
// chosen as the robust default).

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/shotcut"
)

// diffGrid is the WxW grayscale resolution each frame is downscaled to before
// differencing. 64x64 keeps the signal robust to codec noise while staying cheap
// (4096 bytes/frame); it mirrors the spirit of the events aHash downscale but at a
// finer grid so localized motion (a hand) still registers.
const diffGrid = 64

// gridBytes is one downscaled frame in bytes.
const gridBytes = diffGrid * diffGrid

// signalResult carries the normalized per-frame motion signal plus the RAW peak (in
// 0..255 mean-abs-delta units) used to apply an absolute floor. The raw peak is what
// distinguishes a genuinely static clip (peak ~0, just codec dithering) from one with
// real movement — per-clip normalization alone would otherwise amplify codec noise on
// a dead clip into a false "burst."
type signalResult struct {
	Norm    []float64 // normalized 0..1 per-frame motion energy
	RawPeak float64   // maximum raw mean-abs grayscale delta (0..255 units)
	// CutFrames are the sampled-frame indices where a HARD CUT was detected and
	// removed from the signal. Reported rather than hidden: a source with cuts
	// is an edited source, and the caller should know.
	CutFrames []int
}

// motionSignal computes the per-frame motion-energy series for [start, end]. Each
// value is the energy of the transition INTO sampled-frame i+1. sampleFPS is the fps
// actually decoded.
func motionSignal(ffmpeg, input string, start, dur, sampleFPS float64, cuda, verbose bool) (signalResult, error) {
	raw, err := decodeGray(ffmpeg, input, start, dur, sampleFPS, cuda, verbose)
	if err != nil && cuda {
		beckyio.Logf(verbose, "cuda decode failed (%v); retrying on cpu", err)
		raw, err = decodeGray(ffmpeg, input, start, dur, sampleFPS, false, verbose)
	}
	if err != nil {
		return signalResult{}, err
	}
	if len(raw) < 2 {
		return signalResult{}, fmt.Errorf("decoded only %d frame(s); need >= 2 to measure motion", len(raw))
	}
	beckyio.Logf(verbose, "decoded %d frames at %.3f fps; computing dense frame-difference", len(raw), sampleFPS)

	// Raw (un-normalized) mean-abs-difference per consecutive frame pair.
	rawSig := make([]float64, len(raw)-1)
	rawPeak := 0.0
	for i := 1; i < len(raw); i++ {
		v := meanAbsDiff(raw[i-1], raw[i])
		rawSig[i-1] = v
		if v > rawPeak {
			rawPeak = v
		}
	}
	cuts := suppressCuts(raw, rawSig)
	if len(cuts) > 0 {
		beckyio.Logf(verbose, "suppressed %d hard cut(s) from the motion signal", len(cuts))
		rawPeak = 0
		for _, v := range rawSig {
			if v > rawPeak {
				rawPeak = v
			}
		}
	}
	return signalResult{Norm: normalize(rawSig), RawPeak: rawPeak, CutFrames: cuts}, nil
}

// cutSpread is how many frames either side of a detected cut are suppressed
// with it. 2 frames at 30fps is 67ms, which covers the motion blur around a
// splice without swallowing real movement on either side of it.
const cutSpread = 2

// suppressCuts removes hard CUTS from the motion signal, in place, and returns
// the frame indices it removed.
//
// A cut is the largest frame difference there is, so on any edited source it
// dominates the burst list - and becky-motion exists to hand becky-validate the
// exact window to look at. MEASURED on the BLINDFOLD master, 20-55s: SIX of its
// EIGHT motion bursts peaked within 31ms of a known cut, and they carried the
// top scores (0.79-0.98). The tool was pointing the model at the edit, not at
// the action.
//
// A cut changes what is IN the picture, so it changes the brightness
// DISTRIBUTION; motion within a shot mostly shuffles the same pixels. That is
// the same discriminator internal/shotcut uses, with the same measured
// threshold, applied to the frames becky-motion had already decoded.
//
// The cut frame's value is replaced with the mean of its neighbours rather than
// zero: a cut is not a moment of stillness, and punching a hole in the signal
// would create a false calm the burst detector could latch onto.
func suppressCuts(frames [][]byte, sig []float64) []int {
	seen := map[int]bool{}
	var cut []int
	for i := range sig {
		// sig[i] is the transition from frames[i] into frames[i+1].
		if shotcut.HistIntersection(frames[i], frames[i+1]) >= shotcut.MaxHistOverlap {
			continue
		}
		// A cut does not elevate ONE frame. Motion blur either side of the splice
		// and the sampled grid not landing exactly on it spread the difference
		// across a few frames: suppressing only the exact frame left bursts still
		// peaking 42-80ms away from the cut, i.e. one to two frames off.
		for j := i - cutSpread; j <= i+cutSpread; j++ {
			if j >= 0 && j < len(sig) && !seen[j] {
				seen[j] = true
				cut = append(cut, j)
			}
		}
	}
	sort.Ints(cut)
	for _, i := range cut {
		var sum float64
		var n int
		for _, j := range []int{i - 1, i + 1} {
			if j >= 0 && j < len(sig) && !seen[j] {
				sum += sig[j]
				n++
			}
		}
		if n > 0 {
			sig[i] = sum / float64(n)
		} else {
			sig[i] = 0
		}
	}
	return cut
}

// decodeGray runs ffmpeg once to emit a dense WxW grayscale raw stream and slices it
// into per-frame byte buffers. -ss/-t restrict to the analysis window; fps forces a
// constant sample rate so frame index maps cleanly to time. The source is only read.
func decodeGray(ffmpeg, input string, start, dur, fps float64, cuda, verbose bool) ([][]byte, error) {
	args := []string{"-y"}
	if cuda {
		args = append(args, "-hwaccel", "cuda")
	}
	if start > 0 {
		args = append(args, "-ss", fmt.Sprintf("%.3f", start))
	}
	args = append(args, "-i", input)
	if dur > 0 {
		args = append(args, "-t", fmt.Sprintf("%.3f", dur))
	}
	args = append(args,
		"-an", // no audio: motion is a video measurement
		"-vf", fmt.Sprintf("fps=%g,scale=%d:%d,format=gray", fps, diffGrid, diffGrid),
		"-f", "rawvideo", "-pix_fmt", "gray",
		"-loglevel", "error", "-")

	cmd := exec.Command(ffmpeg, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	frames, readErr := readGrayFrames(stdout)
	waitErr := cmd.Wait()
	if waitErr != nil {
		return nil, fmt.Errorf("ffmpeg dense decode: %v: %s", waitErr, tail(errBuf.String()))
	}
	if readErr != nil {
		return nil, readErr
	}
	return frames, nil
}

// readGrayFrames consumes the raw gray stream gridBytes at a time. A trailing partial
// frame (shorter than gridBytes) is dropped.
func readGrayFrames(r io.Reader) ([][]byte, error) {
	br := bufio.NewReaderSize(r, gridBytes*8)
	var frames [][]byte
	for {
		buf := make([]byte, gridBytes)
		_, err := io.ReadFull(br, buf)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read gray frame: %w", err)
		}
		frames = append(frames, buf)
	}
	return frames, nil
}

// meanAbsDiff is the mean absolute per-pixel intensity difference between two equal
// grayscale frames, in raw 0..255 units. It is the deterministic motion-energy
// kernel: a quick touch that shifts a region of pixels produces a sharp single-frame
// spike that 1-fps sampling would average away or miss entirely.
func meanAbsDiff(a, b []byte) float64 {
	var sum int64
	for i := range a {
		d := int(a[i]) - int(b[i])
		if d < 0 {
			d = -d
		}
		sum += int64(d)
	}
	return float64(sum) / float64(len(a))
}

// normalize scales a raw signal to 0..1 by its own maximum, so motion_score is
// "fraction of the clip's peak motion." A clip with no motion (max ~= 0) yields all
// zeros, which is the correct, honest answer for a static frame sequence.
func normalize(sig []float64) []float64 {
	max := 0.0
	for _, v := range sig {
		if v > max {
			max = v
		}
	}
	out := make([]float64, len(sig))
	if max <= 0 {
		return out
	}
	for i, v := range sig {
		out[i] = v / max
	}
	return out
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 800 {
		return s[len(s)-800:]
	}
	return s
}
