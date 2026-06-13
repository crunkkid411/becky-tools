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
	"strings"

	"becky-go/internal/beckyio"
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
	return signalResult{Norm: normalize(rawSig), RawPeak: rawPeak}, nil
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
