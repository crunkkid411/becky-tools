package main

// posture.go - where Jordan looks on every sentence: at the camera (talking
// to the people who will watch the edit) or down at his screen (reading chat).
// Jordan, 2026-10-08: "I was clearly hunched over and reading the screen - the
// EXACT body language I described to you when I'm reading chat". On the apology
// stream (2026-10-07) head down 12+ degrees, the head dropping out of the face
// model, or the face low in the frame came before 13 of 14 chat reads and 6 of
// 28 ordinary moments. His usual head angle depends on the camera, so every
// measure is against his own median in this stream.

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"

	"becky-go/internal/config"
)

const (
	postureFPS  = 2.0
	postureLead = 3.0 // seconds before a sentence that count: he reads, then answers

	// ponytail: calibration knobs, set on the apology stream and the 27-livestream.
	// On the 27-livestream (camera above him) his head angle barely moves when he
	// reads; his face drops in the frame. With faceLow 0.06, 89 of the 153 lines
	// Claude labelled chat came out "reading" and 0 of the 45 lines it kept (0.08: 76 / 0).
	headDown = 12.0 // degrees below his usual head angle
	faceLow  = 0.06 // face centre this much lower than usual (share of the frame height)
)

type postureFrames struct {
	frames       []picFrame // sorted by time
	pitch, faceY float64    // his usual head angle and face height (medians)
}

// loadPosture measures the whole stream at postureFPS once and keeps it.
func loadPosture(cfg config.Config, media, work, stem string, duration float64, logf func(string, ...any)) (*postureFrames, error) {
	path := filepath.Join(work, stem+".posture.json")
	var rows []picFrame
	if b, err := os.ReadFile(path); err != nil || json.Unmarshal(b, &rows) != nil || len(rows) == 0 {
		logf("posture: measuring where he looks, every %.1f s of the stream (a 15-minute stream takes about 7 minutes)...", 1/postureFPS)
		if rows, err = measureFrames(cfg, media, path, []span{{0, duration}}, postureFPS); err != nil {
			return nil, err
		}
		writeJSON(path, rows)
	}
	return newPosture(rows), nil
}

func newPosture(rows []picFrame) *postureFrames {
	p := &postureFrames{frames: rows}
	sort.Slice(p.frames, func(i, j int) bool { return p.frames[i].T < p.frames[j].T })
	var pitch, ys []float64
	for _, f := range rows {
		if len(f.Head) == 2 {
			pitch = append(pitch, f.Head[0])
		}
		if len(f.Face) == 3 {
			ys = append(ys, f.Face[1])
		}
	}
	p.pitch, p.faceY = median(pitch), median(ys)
	return p
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return math.NaN()
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return s[len(s)/2]
}

// look is one frame: "reading" (head down / face low / head dropped out of the
// face models while his body is there), "camera", or "gone" (nobody there).
func (p *postureFrames) look(f picFrame) string {
	face, head := len(f.Face) == 3, len(f.Head) == 2
	switch {
	case head && f.Head[0] <= p.pitch-headDown:
		return "reading"
	case face && f.Face[1] >= p.faceY+faceLow:
		return "reading"
	case face || head:
		return "camera"
	case len(f.Body) > 0:
		return "reading"
	}
	return "gone"
}

// posture is the share of frames of each look.
type posture struct {
	Reading float64 `json:"reading"`
	Camera  float64 `json:"camera"`
	Gone    float64 `json:"gone"`
}

// during: the looks from postureLead before a until b.
func (p *postureFrames) during(a, b float64) posture {
	var out posture
	n := 0.0
	i := sort.Search(len(p.frames), func(i int) bool { return p.frames[i].T >= a-postureLead })
	for ; i < len(p.frames) && p.frames[i].T <= b; i++ {
		switch p.look(p.frames[i]) {
		case "reading":
			out.Reading++
		case "camera":
			out.Camera++
		default:
			out.Gone++
		}
		n++
	}
	if n > 0 {
		out.Reading, out.Camera, out.Gone = out.Reading/n, out.Camera/n, out.Gone/n
	}
	return out
}

// reading: he spends most of the sentence (and the seconds before it) looking
// at his screen.
func (q posture) reading() bool { return q.Reading >= 0.5 }

// words describes the posture for a model reading the transcript.
func (q posture) words() string {
	switch {
	case q.Reading+q.Camera+q.Gone == 0:
		return "not measured"
	case q.Gone >= 0.5:
		return "out of the picture"
	case q.Reading >= 0.5:
		return fmt.Sprintf("looking down at his screen, reading (%d%% of the time)", int(math.Round(100*q.Reading)))
	case q.Reading >= 0.25:
		return "glancing down at his screen now and then"
	}
	return "looking at the camera"
}
