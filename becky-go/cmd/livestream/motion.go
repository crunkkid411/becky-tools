package main

// motion.go - how much the picture moves, ten times a second: the breath
// check's second signal (Jordan, 2026-10-06: "fast movement in my chair does
// not = breath, and that does change the nature of the edit"). ffmpeg shrinks
// every frame to 72x128 gray; a frame's score is its mean pixel change from the
// frame before. Scores are judged against his own median while talking, so a
// camera nearer or farther away does not change the rule. Cached beside the
// footage: decoding a long stream takes minutes.
//
// ponytail: whole-frame change catches fast movement (what he named) but can
// pass a slow hand in the hair as still (1 of 10 spots checked by eye); a
// person-pose signal is the upgrade if slow movements matter.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
)

const (
	motionFPS = 10
	motionW   = 72
	motionH   = 128
)

type motionTrack struct {
	Media string    `json:"media"`
	Size  int64     `json:"size"`
	FPS   float64   `json:"fps"`
	Score []float64 `json:"score"` // Score[i]: change from frame i-1 to frame i (i/FPS seconds); Score[0] = 0
}

// loadMotion measures the picture once and caches it (keyed by path and size).
func loadMotion(ffmpeg, media, cache string) (motionTrack, error) {
	st, err := os.Stat(media)
	if err != nil {
		return motionTrack{}, err
	}
	var m motionTrack
	if b, err := os.ReadFile(cache); err == nil && json.Unmarshal(b, &m) == nil &&
		m.Media == media && m.Size == st.Size() && m.FPS == motionFPS && len(m.Score) > 0 {
		return m, nil
	}
	cmd := exec.Command(ffmpeg, "-v", "error", "-i", media, "-an", "-vf",
		fmt.Sprintf("fps=%d,scale=%d:%d,format=gray", motionFPS, motionW, motionH), "-f", "rawvideo", "-")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return motionTrack{}, err
	}
	if err := cmd.Start(); err != nil {
		return motionTrack{}, fmt.Errorf("could not start ffmpeg: %w", err)
	}
	m = motionTrack{Media: media, Size: st.Size(), FPS: motionFPS, Score: frameScores(bufio.NewReaderSize(out, 1<<20))}
	if err := cmd.Wait(); err != nil {
		return motionTrack{}, fmt.Errorf("ffmpeg could not read the picture: %v (%s)", err, lastLines(stderr.String(), 2))
	}
	if len(m.Score) < 2 {
		return motionTrack{}, fmt.Errorf("the video gave no frames to measure")
	}
	writeJSON(cache, m)
	return m, nil
}

// frameScores reads raw gray frames until the stream ends.
func frameScores(r io.Reader) []float64 {
	const n = motionW * motionH
	prev, cur := make([]byte, n), make([]byte, n)
	var scores []float64
	for i := 0; ; i++ {
		if _, err := io.ReadFull(r, cur); err != nil {
			return scores
		}
		s := 0.0
		if i > 0 {
			sum := 0
			for k := range cur {
				d := int(cur[k]) - int(prev[k])
				if d < 0 {
					d = -d
				}
				sum += d
			}
			s = float64(sum) / n
		}
		scores = append(scores, s)
		prev, cur = cur, prev
	}
}

// meanOver is the mean score of the frames from a to b seconds (both ends included).
func (m motionTrack) meanOver(a, b float64) (mean, peak float64, ok bool) {
	i0, i1 := int(a*m.FPS), int(b*m.FPS)
	i0 = max(i0, 0)
	i1 = min(i1, len(m.Score)-1)
	if i1 < i0 {
		return 0, 0, false
	}
	sum := 0.0
	for _, s := range m.Score[i0 : i1+1] {
		sum += s
		peak = max(peak, s)
	}
	return sum / float64(i1-i0+1), peak, true
}

// baseline is his median movement while talking (words longer than 0.1 s).
func (m motionTrack) baseline(words []Word) float64 {
	var v []float64
	for _, w := range words {
		if w.End-w.Start <= 0.1 {
			continue
		}
		if mean, _, ok := m.meanOver(w.Start, w.End); ok {
			v = append(v, mean)
		}
	}
	if len(v) == 0 {
		return 0
	}
	sort.Float64s(v)
	if len(v)%2 == 1 {
		return v[len(v)/2]
	}
	return (v[len(v)/2-1] + v[len(v)/2]) / 2
}

// movement is the mean and peak change over [a,b], as multiples of base.
func (m motionTrack) movement(a, b, base float64) (mean, peak float64) {
	mn, pk, ok := m.meanOver(a, b)
	if !ok || base <= 0 {
		return 0, 0
	}
	return mn / base, pk / base
}
