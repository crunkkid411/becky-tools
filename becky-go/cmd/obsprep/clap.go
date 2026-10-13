package main

// clap.go - find Jordan's sync clap: the audio spike (mic track) and the video
// frame where his hands come together. Pure measurement, no model; the sound
// labeler (BEATs, labels.go) only confirms the spike sounds like a clap.
//
// Audio: the spike is the sharpest attack out of quiet in the first minutes of
// the mic track; its time is the onset (first sample at half the peak).
//
// Picture: Jordan steps frame by frame and picks the first frame his hands are
// together. Measured on his footage (2026-10-12 and four clips he synced by
// hand): the hands swing in fast, then STOP when they meet. So inside +/-20
// frames of the spike (his words: "certainly less than 20 frames away"), the
// motion is measured where the window moves most (the hands). A frame ends a
// swing when the change INTO it is still part of the swing and the change OUT
// of it is small (< 35% of the swing); the biggest swing that stops is the
// clap (a fidget before it is a smaller swing). His webcams repeat frames
// (OBS duplicates), so a repeated frame is skipped when looking for "the next
// picture". Picked 165/87/134/537 on the five clips checked by eye.

import (
	"encoding/binary"
	"fmt"
	"math"
	"os/exec"
	"sort"
)

const (
	searchSecs   = 120.0 // he claps at the start of a recording
	sampleRate   = 48000
	minPeak      = 0.05 // -26 dBFS: quieter than this is not a clap
	minRiseDB    = 30.0 // attack above the 100 ms before it
	maxCands     = 5
	frameWindow  = 20   // Jordan: the spike is certainly < 20 frames from the clap frame
	dupMotion    = 0.5  // mean pixel change below this = OBS repeated the frame
	stopFraction = 0.35 // after contact the picture changes < 35% of the swing
	minBurst     = 2.5  // the swing must stand out this much over the window's usual motion
	motionW      = 640
	motionH      = 360
	gridX        = 16
	gridY        = 9
)

// spike is one candidate clap in the audio.
type spike struct {
	Onset  float64 // seconds into the file
	PeakDB float64
	RiseDB float64
}

// micSamples decodes the first searchSecs of audio stream idx as mono float32.
func micSamples(ffmpeg, file string, idx int) ([]float32, error) {
	out, err := exec.Command(ffmpeg, "-v", "error", "-t", fmt.Sprint(searchSecs), "-i", file,
		"-map", fmt.Sprintf("0:a:%d", idx), "-ac", "1", "-ar", fmt.Sprint(sampleRate), "-f", "f32le", "-").Output()
	if err != nil {
		return nil, fmt.Errorf("could not read the audio of %s: %w", file, err)
	}
	x := make([]float32, len(out)/4)
	for i := range x {
		x[i] = math.Float32frombits(binary.LittleEndian.Uint32(out[i*4:]))
	}
	return x, nil
}

// findSpikes returns the sharpest attacks, sharpest first.
func findSpikes(x []float32) []spike {
	ms := sampleRate / 1000
	n := len(x) / ms
	env := make([]float64, n)
	sq := make([]float64, n)
	for i := 0; i < n; i++ {
		for _, v := range x[i*ms : (i+1)*ms] {
			a := math.Abs(float64(v))
			env[i] = math.Max(env[i], a)
			sq[i] += a * a
		}
		sq[i] /= float64(ms)
	}
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return env[order[a]] > env[order[b]] })
	var out []spike
	var taken []int
	for _, i := range order {
		if env[i] < minPeak || len(taken) >= 40 {
			break
		}
		near := false
		for _, t := range taken {
			if abs(i-t) < 500 {
				near = true
				break
			}
		}
		if near {
			continue
		}
		taken = append(taken, i)
		lo, hi := max(0, i-100), max(0, i-5)
		pre := 1e-12
		if hi > lo {
			s := 0.0
			for _, v := range sq[lo:hi] {
				s += v
			}
			pre += s / float64(hi-lo)
		}
		rise := 20 * math.Log10(env[i]/math.Sqrt(pre))
		if rise < minRiseDB {
			continue
		}
		// onset: first sample in the 10 ms before the peak at half its height
		a0 := max(0, (i-10)*ms)
		on := a0
		for j := a0; j < (i+1)*ms; j++ {
			if math.Abs(float64(x[j])) >= 0.5*env[i] {
				on = j
				break
			}
		}
		out = append(out, spike{Onset: float64(on) / sampleRate, PeakDB: 20 * math.Log10(env[i]), RiseDB: rise})
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].RiseDB > out[b].RiseDB })
	if len(out) > maxCands {
		out = out[:maxCands]
	}
	return out
}

// frameMotion decodes frames f0-1..f1 (grey, small) and returns motion[f] =
// mean change INTO frame f, measured where the window moves most (the hands).
func frameMotion(ffmpeg, file string, f0, f1 int, fps float64) (map[int]float64, error) {
	out, err := exec.Command(ffmpeg, "-v", "error", "-t", fmt.Sprintf("%.4f", float64(f1+2)/fps), "-i", file,
		"-vf", fmt.Sprintf("select='between(n\\,%d\\,%d)',scale=%d:%d", f0-1, f1, motionW, motionH),
		"-vsync", "0", "-f", "rawvideo", "-pix_fmt", "gray", "-").Output()
	if err != nil {
		return nil, fmt.Errorf("could not read the picture of %s: %w", file, err)
	}
	size := motionW * motionH
	nf := len(out) / size
	if nf < 3 {
		return nil, fmt.Errorf("could not read the frames around the clap in %s", file)
	}
	cw, ch := motionW/gridX, motionH/gridY
	cells := make([][]float64, nf-1) // cells[k] = per-cell change from frame k to k+1
	total := make([]float64, gridX*gridY)
	for k := 0; k < nf-1; k++ {
		a, b := out[k*size:(k+1)*size], out[(k+1)*size:(k+2)*size]
		c := make([]float64, gridX*gridY)
		for y := 0; y < gridY*ch; y++ {
			for x := 0; x < gridX*cw; x++ {
				d := float64(a[y*motionW+x]) - float64(b[y*motionW+x])
				c[(y/ch)*gridX+x/cw] += math.Abs(d)
			}
		}
		for i := range c {
			c[i] /= float64(cw * ch)
			total[i] += c[i]
		}
		cells[k] = c
	}
	// the 10% of cells that move most across the window
	sorted := append([]float64(nil), total...)
	sort.Float64s(sorted)
	thr := sorted[int(0.9*float64(len(sorted)-1))]
	m := map[int]float64{}
	for k, c := range cells {
		s, n := 0.0, 0
		for i, v := range c {
			if total[i] >= thr {
				s += v
				n++
			}
		}
		m[f0+k] = s / float64(n)
	}
	return m, nil
}

// contactFrame applies the rule above to motion (motion[f] = change into f).
// ok is false when no swing stands out near the spike.
func contactFrame(motion map[int]float64, spikeFrame int) (frame int, ok bool, why string) {
	lo, hi := spikeFrame-frameWindow, spikeFrame+frameWindow
	peak := -1.0
	var usual []float64
	for f := lo; f <= hi; f++ {
		v, has := motion[f]
		if !has {
			continue
		}
		if v >= dupMotion {
			usual = append(usual, v)
		}
		peak = math.Max(peak, v)
	}
	if peak <= 0 || len(usual) == 0 {
		return 0, false, "no picture around the spike"
	}
	sort.Float64s(usual)
	med := usual[len(usual)/2]
	if peak < minBurst*med {
		return 0, false, fmt.Sprintf("no hand swing near the spike (strongest motion %.1f, usual %.1f)", peak, med)
	}
	// Every frame that ENDS a swing is a candidate: the change into it is still
	// part of the swing, the change out of it is small. The biggest swing that
	// stops wins - fidgeting before the clap is a smaller swing.
	best, bestScore := 0, 0.0
	for f := lo; f <= hi; f++ {
		cur, in := motion[f]
		if !in || cur < dupMotion {
			continue // a repeat of the frame before it
		}
		swing := 0.0
		for g := f - 3; g <= f; g++ {
			swing = math.Max(swing, motion[g])
		}
		if swing < minBurst*med || cur < stopFraction*swing {
			continue
		}
		next, has := 0.0, false
		for g := f + 1; ; g++ {
			v, in := motion[g]
			if !in {
				break
			}
			if v >= dupMotion {
				next, has = v, true
				break
			}
		}
		if !has || next >= stopFraction*swing {
			continue
		}
		if score := swing - next; score > bestScore {
			best, bestScore = f, score
		}
	}
	if bestScore == 0 {
		return 0, false, "the hands never come to rest after the swing"
	}
	return best, true, ""
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
