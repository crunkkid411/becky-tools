// Package talkingface finds the face that is actually TALKING during a stretch of speech
// and saves a close-up of it, so a vision model judging "who is this speaker" looks at
// the right face instead of guessing from a frame with several people (2026-10-11).
//
// It runs becky-speaking (face tracking + LR-ASD lip-motion-against-sound, MIT, 2025) on
// the stretch. Picked by the research in HANDOFF-LOG 2026-10-11: LR-ASD was already on
// this PC and nothing newer beat it; MediaPipe's mouth-open score fires on chewing and
// laughing, so it is not used.
package talkingface

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"time"

	"becky-go/internal/config"
	"becky-go/internal/faceembed"
	"becky-go/internal/forensicrun"
)

// maxWindow keeps one becky-speaking call short (it decodes every frame of the window).
const maxWindow = 8.0

type speakingOut struct {
	OK         bool   `json:"ok"`
	TrackID    *int   `json:"speaker_track_id"`
	Confidence string `json:"confidence"`
	Tracks     []struct {
		ID       int  `json:"id"`
		Speaking bool `json:"speaking"`
		Boxes    []struct {
			T    float64    `json:"t"`
			BBox [4]float64 `json:"bbox"`
		} `json:"boxes"`
	} `json:"tracks"`
}

// Crop saves a close-up of the face talking between start and end and returns its path
// ("" with the reason when no face is clearly talking - a normal outcome: the speaker
// may be off camera).
func Crop(ffmpeg, video string, start, end float64) (string, string) {
	if end-start > maxWindow {
		mid := (start + end) / 2
		start, end = mid-maxWindow/2, mid+maxWindow/2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	raw, err := forensicrun.RunTool(ctx, "becky-speaking", "--video", video,
		"--start", fmt.Sprintf("%.2f", start), "--end", fmt.Sprintf("%.2f", end), "--boxes")
	if err != nil {
		return "", "becky-speaking failed: " + err.Error()
	}
	var out speakingOut
	if json.Unmarshal(raw, &out) != nil || !out.OK || out.TrackID == nil {
		return "", "no face is clearly talking"
	}
	t, bb, ok := pickBox(out, (start+end)/2)
	if !ok {
		return "", "no face is clearly talking"
	}
	f, err := os.CreateTemp("", "becky_talkingface_*.jpg")
	if err != nil {
		return "", err.Error()
	}
	p := f.Name()
	f.Close()
	cmd := exec.Command(ffmpeg, "-ss", fmt.Sprintf("%.3f", t), "-i", video, "-frames:v", "1",
		"-vf", cropFilter(bb)+",scale=384:-2", "-q:v", "2", "-y", "-loglevel", "error", p)
	if cmd.Run() != nil {
		os.Remove(p)
		return "", "could not cut the close-up"
	}
	if st, err := os.Stat(p); err != nil || st.Size() == 0 {
		os.Remove(p)
		return "", "could not cut the close-up"
	}
	return p, ""
}

// pickBox is the talking track's box nearest the middle of the window (pure). Only a
// "conclusion" counts, or a lone track that LR-ASD says is speaking.
func pickBox(out speakingOut, mid float64) (float64, [4]float64, bool) {
	for _, tr := range out.Tracks {
		if tr.ID != *out.TrackID || len(tr.Boxes) == 0 {
			continue
		}
		if out.Confidence != "conclusion" && !(len(out.Tracks) == 1 && tr.Speaking) {
			return 0, [4]float64{}, false
		}
		best := tr.Boxes[0]
		for _, b := range tr.Boxes {
			if math.Abs(b.T-mid) < math.Abs(best.T-mid) {
				best = b
			}
		}
		return best.T, best.BBox, true
	}
	return 0, [4]float64{}, false
}

// cropFilter is an ffmpeg crop around the face with a margin (head and shoulders), for
// boxes in pixels or in fractions of the frame (pure).
func cropFilter(b [4]float64) string {
	w, h := b[2]-b[0], b[3]-b[1]
	x, y := b[0]-0.4*w, b[1]-0.3*h
	w, h = w*1.8, h*1.7
	if b[2] <= 1.0 && b[3] <= 1.0 { // fractions
		x, y = math.Max(0, x), math.Max(0, y)
		w, h = math.Min(w, 1-x), math.Min(h, 1-y)
		return fmt.Sprintf("crop=iw*%.4f:ih*%.4f:iw*%.4f:ih*%.4f", w, h, x, y)
	}
	return fmt.Sprintf("crop=min(iw\\,%.0f):min(ih\\,%.0f):max(0\\,min(iw-%.0f\\,%.0f)):max(0\\,min(ih-%.0f\\,%.0f))",
		w, h, w, x, h, y)
}

// Face-match bars (ArcFace cosine, the same scale as becky-identify's --face-threshold
// 0.55: one person ~0.59+, the closest look-alike seen ~0.50). Between them is "unsure"
// and the caller asks Gemma.
const (
	faceSame      = 0.55
	faceDifferent = 0.30
)

// Compare embeds both groups of pictures in one face-model call and says whether they
// show the same face: "yes" | "no" | "unsure", with the similarity (-1 when a side had
// no face the model could read).
func Compare(cfg config.Config, a, b []string) (string, float64) {
	recs, err := faceembed.Embed(cfg, append(append([]string(nil), a...), b...), "", false)
	if err != nil || len(recs) != len(a)+len(b) {
		return "unsure", -1
	}
	va, vb := meanFace(recs[:len(a)]), meanFace(recs[len(a):])
	if va == nil || vb == nil {
		return "unsure", -1
	}
	sim := 0.0
	for i := range va {
		sim += va[i] * vb[i]
	}
	return verdict(sim), sim
}

func verdict(sim float64) string {
	switch {
	case sim >= faceSame:
		return "yes"
	case sim < faceDifferent:
		return "no"
	}
	return "unsure"
}

// meanFace is the normalized mean of the found faces (nil when none was found).
func meanFace(recs []faceembed.Face) []float64 {
	var sum []float64
	for _, r := range recs {
		if !r.Found || len(r.Vector) == 0 {
			continue
		}
		if sum == nil {
			sum = make([]float64, len(r.Vector))
		}
		for i, x := range r.Vector {
			sum[i] += x
		}
	}
	n := 0.0
	for _, x := range sum {
		n += x * x
	}
	if n == 0 {
		return nil
	}
	for i := range sum {
		sum[i] /= math.Sqrt(n)
	}
	return sum
}
