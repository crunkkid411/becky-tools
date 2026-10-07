package main

// picture.go - what the picture shows, for the breath check and the cut list.
// Jordan, 2026-10-06: "text based editing is not enough, that's why becky-tools
// exists", and the small models are there "specifically for the purpose of
// providing more data for better decision making". Each one is a specialist,
// none of them is a verdict alone:
//
//  1. pyhelpers/picture_signals.py, 10 frames a second: insightface buffalo_l
//     (his face box and how far his mouth is open, from the 68 3D landmarks),
//     MediaPipe pose (shoulders and wrists), MediaPipe Face Landmarker (its
//     jawOpen score: a second face model) and MediaPipe Object Detector (a
//     drink raised into the top half of the frame). Cached by 0.1 s grid time in
//     becky-edit\<video>.picture-v2.json, so a re-run only measures new frames.
//  2. Falcon-Perception: boxes for a bottle, a cup or a phone - WHERE an object
//     is and how big, which Gemma does poorly.
//  3. Gemma-4 E4B: "is he holding something up toward the camera?" - WHAT is
//     happening, which a box cannot say.
// On the 27-livestream test frames Falcon and Gemma agreed on all 8: the toast
// and a held bottle yes; hands to head, arms spread, hair touches, a gesture no.
// MediaPipe (research/mediapipe-capabilities-2026-10.md, Jordan: "we ALSO use
// mediapipe in the same way"): the raised bottle at the toast, and jawOpen held
// on both of his big facial expressions - and on none of the breaths or movements.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"becky-go/internal/avlm"
	"becky-go/internal/config"
	"becky-go/internal/crop"
	"becky-go/internal/pyhelpers"
)

const (
	picFPS = 10.0

	// ponytail: calibration knobs, set on the 27-livestream against the breaths
	// Jordan listed and the movements and faces in the frame strips (research
	// doc). Another camera distance may need them moved.
	faceSeenMin = 0.75 // share of frames where his face is found
	faceSizeMax = 1.30 // largest / smallest face width: leaning in or out
	shoulderMax = 0.20 // shoulders rising or falling, in shoulder widths
	mouthOpen   = 0.22 // inner lips / eye distance: a wide-open mouth (a breath peaks near 0.15)
	mouthHeld   = 4    // frames (0.4 s) the mouth stays open = a facial expression
	jawOpen     = 0.6  // MediaPipe jawOpen: expressions 0.79-0.81, breaths at most 0.53
	jawHeld     = 3    // frames in a row (expressions held it 6-7)
	visible     = 0.5  // MediaPipe landmark visibility that counts

	raisedScore = 0.3  // a MediaPipe drink box this sure (the toast 0.43-0.48, breaths 0.13 or less)...
	raisedArea  = 0.05 // ...this big (toast 12-19% of the frame)...
	raisedTop   = 0.5  // ...centred in the top half (the bottle held at his chest sits at 0.70)

	heldConf   = 0.9 // a Falcon box this sure...
	heldHeight = 0.2 // ...and this tall (share of the frame): in his hand, not on the desk (~0.04)
)

var heldQueries = []string{"bottle", "cup", "phone"}

// picFrame is one sampled frame (pyhelpers/picture_signals.py).
type picFrame struct {
	T     float64              `json:"t"`
	Face  []float64            `json:"face,omitempty"` // centre x, centre y, width (fractions of the frame)
	Mouth float64              `json:"mouth,omitempty"`
	Body  map[string][]float64 `json:"body,omitempty"` // ls rs lw rw: x, y, visibility
	Jaw   float64              `json:"jaw,omitempty"`  // MediaPipe jawOpen
	Objs  []picObj             `json:"objs,omitempty"` // MediaPipe drink boxes
	Gest  [][]any              `json:"gest,omitempty"` // MediaPipe gestures: [name, score] per hand
	Expr  map[string]float64   `json:"expr,omitempty"` // MediaPipe expression scores (brow, eyes, smile, ...)
	Head  []float64            `json:"head,omitempty"` // MediaPipe head [pitch, yaw] in degrees
}

type picObj struct {
	What  string  `json:"what"`
	Score float64 `json:"score"`
	Area  float64 `json:"area"` // share of the frame
	CY    float64 `json:"cy"`   // box centre, 0 = top
}

func (o picObj) raised() bool {
	return o.Score >= raisedScore && o.Area >= raisedArea && o.CY < raisedTop
}

// picCache is where the measured frames are kept (v3: with gestures, expression
// scores, head angle, nose and elbows; frames measured before them are measured
// again).
func picCache(work, stem string) string { return filepath.Join(work, stem+".picture-v3.json") }

// mediapipeModel is a MediaPipe model next to the pose model.
func mediapipeModel(cfg config.Config, name string) string {
	return filepath.Join(filepath.Dir(cfg.PoseModel), name)
}

type pictures struct {
	frames map[int]picFrame // key: round(t * 10)
}

func picKey(t float64) int { return int(math.Round(t * picFPS)) }

// poseModel is the MediaPipe model the picture check was calibrated with (the
// "full" one) when it sits next to the configured one.
func poseModel(cfg config.Config) string {
	if full := strings.Replace(cfg.PoseModel, "heavy", "full", 1); full != cfg.PoseModel {
		if _, err := os.Stat(full); err == nil {
			return full
		}
	}
	return cfg.PoseModel
}

// loadPictures measures every 0.1 s grid frame of the spans that the cache does
// not have yet (one helper call) and returns all of them.
func loadPictures(cfg config.Config, media, cachePath string, spans []span) (*pictures, error) {
	p := &pictures{frames: map[int]picFrame{}}
	if b, err := os.ReadFile(cachePath); err == nil {
		var rows []picFrame
		if json.Unmarshal(b, &rows) == nil {
			for _, r := range rows {
				p.frames[picKey(r.T)] = r
			}
		}
	}
	var need []span
	for _, k := range missingKeys(p.frames, spans) {
		a := float64(k[0]) / picFPS
		need = append(need, span{a, float64(k[1]+1) / picFPS})
	}
	if len(need) == 0 {
		return p, nil
	}
	mesh, objects := mediapipeModel(cfg, "face_landmarker.task"), mediapipeModel(cfg, "efficientdet_lite0_int8.tflite")
	gestures := mediapipeModel(cfg, "gesture_recognizer.task")
	for _, f := range []string{cfg.FacePython, poseModel(cfg), mesh, objects, gestures, filepath.Join(cfg.FaceModelRoot, "models", "buffalo_l")} {
		if _, err := os.Stat(f); err != nil {
			return p, fmt.Errorf("the picture models are missing (%s)", f)
		}
	}
	script, err := pyhelpers.Materialize("picture_signals.py", pyhelpers.PictureSignals)
	if err != nil {
		return p, err
	}
	spanFile := cachePath + ".spans.json"
	js := make([][2]float64, len(need))
	for i, s := range need {
		js[i] = [2]float64{s.A, s.B}
	}
	writeJSON(spanFile, js)
	defer os.Remove(spanFile)
	cmd := exec.Command(cfg.FacePython, script, "--video", media, "--spans", spanFile,
		"--face-root", cfg.FaceModelRoot, "--pose", poseModel(cfg), "--face-mesh", mesh, "--objects", objects, "--gestures", gestures, "--ffmpeg", cfg.FFmpeg)
	cmd.Env = crop.ChildEnv(cfg)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, runErr := cmd.Output()
	var res struct {
		OK     bool       `json:"ok"`
		Reason string     `json:"reason"`
		Frames []picFrame `json:"frames"`
	}
	if err := json.Unmarshal([]byte(lastJSONLine(string(out))), &res); err != nil {
		return p, fmt.Errorf("the picture check gave no answer: %v (%s)", runErr, lastLines(stderr.String(), 2))
	}
	if !res.OK {
		return p, fmt.Errorf("%s", res.Reason)
	}
	for _, r := range res.Frames {
		p.frames[picKey(r.T)] = r
	}
	rows := make([]picFrame, 0, len(p.frames))
	for _, r := range p.frames {
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].T < rows[j].T })
	writeJSON(cachePath, rows)
	return p, nil
}

// missingKeys lists the runs [first, last] of 0.1 s grid frames inside the spans
// that are not measured yet.
func missingKeys(have map[int]picFrame, spans []span) [][2]int {
	want := map[int]bool{}
	for _, s := range spans {
		for k := int(math.Ceil(s.A*picFPS - 1e-6)); float64(k) <= s.B*picFPS+1e-6; k++ {
			if _, ok := have[k]; !ok {
				want[k] = true
			}
		}
	}
	keys := make([]int, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	var runs [][2]int
	for _, k := range keys {
		if n := len(runs); n > 0 && runs[n-1][1] == k-1 {
			runs[n-1][1] = k
			continue
		}
		runs = append(runs, [2]int{k, k})
	}
	return runs
}

// picLook sums up the picture over one span.
type picLook struct {
	Frames    int     `json:"frames"`
	FaceSeen  float64 `json:"face_seen"`  // share of frames with his face
	FaceSize  float64 `json:"face_size"`  // largest / smallest face width
	Shoulders float64 `json:"shoulders"`  // rise or fall, in shoulder widths
	Mouth     float64 `json:"mouth"`      // widest mouth opening
	MouthHeld int     `json:"mouth_held"` // longest run of open-mouth frames
	Jaw       float64 `json:"jaw"`        // highest MediaPipe jawOpen
	JawHeld   int     `json:"jaw_held"`   // longest run of jawOpen frames
	HandsUp   int     `json:"hands_up"`   // frames with a wrist above his shoulders
	Raised    int     `json:"raised"`     // frames with a drink raised (MediaPipe)
	RaisedBox picObj  `json:"raised_box"` // the surest raised drink
}

func (p *pictures) look(a, b float64) picLook {
	var l picLook
	minW, maxW := math.Inf(1), 0.0
	var sy, sw []float64
	run, jawRun := 0, 0
	faces := 0
	for k := int(math.Ceil(a*picFPS - 1e-6)); float64(k) <= b*picFPS+1e-6; k++ {
		f, ok := p.frames[k]
		if !ok {
			run, jawRun = 0, 0
			continue
		}
		l.Frames++
		if len(f.Face) == 3 {
			faces++
			minW, maxW = math.Min(minW, f.Face[2]), math.Max(maxW, f.Face[2])
			l.Mouth = math.Max(l.Mouth, f.Mouth)
		}
		if len(f.Face) == 3 && f.Mouth >= mouthOpen {
			run++
			l.MouthHeld = max(l.MouthHeld, run)
		} else {
			run = 0
		}
		l.Jaw = math.Max(l.Jaw, f.Jaw)
		if f.Jaw >= jawOpen {
			jawRun++
			l.JawHeld = max(l.JawHeld, jawRun)
		} else {
			jawRun = 0
		}
		raised := false
		for _, o := range f.Objs {
			if o.raised() {
				raised = true
				if o.Score > l.RaisedBox.Score {
					l.RaisedBox = o
				}
			}
		}
		if raised {
			l.Raised++
		}
		ls, rs := f.Body["ls"], f.Body["rs"]
		if len(ls) == 3 && len(rs) == 3 && ls[2] >= visible && rs[2] >= visible {
			sy = append(sy, (ls[1]+rs[1])/2)
			sw = append(sw, math.Abs(ls[0]-rs[0]))
			top := math.Min(ls[1], rs[1])
			for _, w := range [][]float64{f.Body["lw"], f.Body["rw"]} {
				if len(w) == 3 && w[2] >= visible && w[1] < top {
					l.HandsUp++
					break
				}
			}
		}
	}
	if l.Frames > 0 {
		l.FaceSeen = float64(faces) / float64(l.Frames)
	}
	if faces > 1 {
		l.FaceSize = maxW / minW
	} else if faces == 1 {
		l.FaceSize = 1
	}
	if len(sy) > 1 {
		sort.Float64s(sw)
		if w := sw[len(sw)/2]; w > 0 {
			lo, hi := sy[0], sy[0]
			for _, y := range sy {
				lo, hi = math.Min(lo, y), math.Max(hi, y)
			}
			l.Shoulders = (hi - lo) / w
		}
	}
	return l
}

// faceHeld: insightface and MediaPipe each say whether his mouth is held open.
func (l picLook) faceHeld() (insight, mediapipe bool) {
	return l.MouthHeld >= mouthHeld, l.JawHeld >= jawHeld
}

// faceWhy names the face model(s) that saw the held-open mouth.
func (l picLook) faceWhy() string {
	in, mp := l.faceHeld()
	var parts []string
	if in {
		parts = append(parts, fmt.Sprintf("insightface: mouth open %.1f s", float64(l.MouthHeld)/picFPS))
	}
	if mp {
		parts = append(parts, fmt.Sprintf("MediaPipe: jaw open %.1f s", float64(l.JawHeld)/picFPS))
	}
	return strings.Join(parts, "; ")
}

// verdict is what the picture rules out ("" = nothing: a breath may be cut
// here), with the detail for the report. Leaning to "keep": one model seeing
// the raised drink or the held-open mouth is enough to place no region. Hands
// alone do NOT rule a breath out: Jordan wants the breath with his arms spreading
// and with his hands to his head cut (pictureVerdict only asks for a clearer
// breath while a hand is up).
func (l picLook) verdict() (string, string) {
	in, mp := l.faceHeld()
	switch {
	case l.Frames < 3:
		return vUnclear, "the picture could not be checked"
	case l.Raised > 0:
		b := l.RaisedBox
		return vHeld, fmt.Sprintf("MediaPipe: a %s held up (%.2f sure, %.0f%% of the frame, top half)", b.What, b.Score, 100*b.Area)
	case in || mp:
		return vFace, l.faceWhy()
	case l.FaceSeen < faceSeenMin:
		return vMovement, fmt.Sprintf("his face is out of view in %.0f%% of it", 100*(1-l.FaceSeen))
	case l.FaceSize > faceSizeMax:
		return vMovement, fmt.Sprintf("he leans in or out (face size x%.2f)", l.FaceSize)
	case l.Shoulders > shoulderMax:
		return vMovement, fmt.Sprintf("his shoulders move %.2f shoulder widths", l.Shoulders)
	}
	return "", ""
}

// handsUpBreath is the breath score a region needs while a wrist is above his
// shoulders. 27-livestream: his hands-to-head breath (which he wants cut) 0.57,
// the other hands-up breaths 0.57-0.67; hands rustling in his hair 0.32 and 0.43.
const handsUpBreath = 0.5

// pictureVerdict is verdict plus the one picture rule that needs the sound: with
// a hand up (MediaPipe pose) only a confident breath gets a region. A hand never
// rules a clear breath out - Jordan wants the breath with his hands to his head
// cut - but a faint "breath" while his hands move is more likely his hair or
// clothes.
func pictureVerdict(l picLook, br float64) (string, string) {
	if v, why := l.verdict(); v != "" {
		return v, why
	}
	if l.HandsUp > 0 && br < handsUpBreath {
		return vMovement, fmt.Sprintf("MediaPipe: a hand above his shoulders (%.1f s) and only a faint breath (%.2f)",
			float64(l.HandsUp)/picFPS, br)
	}
	return "", ""
}

// heldCheck is Falcon's and Gemma's answer for one frame.
type heldCheck struct {
	Falcon    string `json:"falcon,omitempty"` // "bottle 1.00, 61% of the frame tall"
	FalconYes bool   `json:"falcon_yes"`
	Gemma     string `json:"gemma,omitempty"` // Gemma's own words
	GemmaYes  bool   `json:"gemma_yes"`
	Done      bool   `json:"done"`
}

func (h heldCheck) yes() bool { return h.FalconYes || h.GemmaYes }

func (h heldCheck) why() string {
	var parts []string
	if h.FalconYes {
		parts = append(parts, "Falcon: "+h.Falcon)
	}
	if h.GemmaYes {
		parts = append(parts, "Gemma: "+h.Gemma)
	}
	return strings.Join(parts, "; ")
}

const heldPrompt = `Look at this frame from a livestream. Is the man holding something up toward the camera, for example raising a drink for a toast or showing an object to the viewer? Touching his hair or face does not count. Answer with JSON only: {"holding_up": true or false, "what": "what his hands are doing, at most 8 words"}`

// vegasOpen: VEGAS is running. Gemma then runs on the processor: VEGAS (~3 GB
// of graphics memory) + Gemma on the GPU (~5.2 GB) does not fit in 8 GB, and an
// out-of-memory VEGAS with unsaved work would cost Jordan that work.
func vegasOpen() bool { return vegasCount() > 0 }

// vegasCount is how many VEGAS windows are running.
func vegasCount() int {
	out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq vegas180.exe", "/NH").Output()
	if err != nil {
		return 0
	}
	return strings.Count(strings.ToLower(string(out)), "vegas180.exe")
}

// gemmaRunner is the Gemma-4 vision runner, on the processor while VEGAS is open.
func gemmaRunner(cfg config.Config, logf func(string, ...any)) *avlm.Runner {
	gm, gp, _ := cfg.GemmaAVLM()
	r := avlm.New(gm, gp, cfg.LlamaServer, "", cfg.FFmpeg, cfg.FFprobe, nil)
	if vegasOpen() {
		r.NGL = 0
		logf("  (VEGAS is open, so Gemma runs on the processor and leaves the graphics card to VEGAS)")
	}
	return r
}

// checkHeld asks Falcon and Gemma about one frame at each time (cached by time
// in becky-edit\held-cache.json). A model that cannot run leaves its half empty
// and says so in the notes: the other one still answers.
func checkHeld(cfg config.Config, media, work string, times []float64, logf func(string, ...any)) (map[string]heldCheck, []string) {
	cachePath := filepath.Join(work, "held-cache.json")
	cache := map[string]heldCheck{}
	if b, err := os.ReadFile(cachePath); err == nil {
		_ = json.Unmarshal(b, &cache)
	}
	dir := filepath.Join(work, "held-frames")
	_ = os.MkdirAll(dir, 0o755)
	var todo []string
	for _, t := range times {
		k := fmt.Sprintf("%09.3f", t)
		if cache[k].Done {
			continue
		}
		f := filepath.Join(dir, k+".jpg")
		if _, err := os.Stat(f); err != nil {
			if err := exec.Command(cfg.FFmpeg, "-v", "error", "-y", "-ss", strconv.FormatFloat(t, 'f', 3, 64), "-i", media,
				"-frames:v", "1", "-vf", "scale=-2:768", f).Run(); err != nil {
				continue
			}
		}
		todo = append(todo, k)
	}
	if len(todo) == 0 {
		return cache, nil
	}
	var notes []string
	logf("  Falcon + Gemma: is he holding something up? (%d frames)", len(todo))
	falcon, err := falconBoxes(cfg, dir, todo)
	if err != nil {
		notes = append(notes, "Falcon did not run: "+err.Error())
	}
	gemma, err := gemmaHeld(cfg, dir, todo, logf)
	if err != nil {
		notes = append(notes, "Gemma did not run: "+err.Error())
	}
	for _, k := range todo {
		h := cache[k]
		if v, ok := falcon[k]; ok {
			h.Falcon, h.FalconYes = v.Falcon, v.FalconYes
		}
		if v, ok := gemma[k]; ok {
			h.Gemma, h.GemmaYes = v.Gemma, v.GemmaYes
		}
		_, fok := falcon[k]
		_, gok := gemma[k]
		h.Done = fok && gok
		cache[k] = h
	}
	writeJSON(cachePath, cache)
	return cache, notes
}

// falconBoxes runs Falcon once per query over the frames and keeps, per frame,
// the strongest box that looks held (sure, and tall enough not to be a bottle
// standing on the desk).
func falconBoxes(cfg config.Config, dir string, keys []string) (map[string]heldCheck, error) {
	fdir := `X:\AI-2\becky-tools\models\falcon-perception`
	py := filepath.Join(fdir, "venv", "Scripts", "python.exe")
	if _, err := os.Stat(py); err != nil {
		return nil, fmt.Errorf("its Python is missing (%s)", py)
	}
	script, err := pyhelpers.Materialize("falcon_detect.py", pyhelpers.FalconDetect)
	if err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp("", "becky-held-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	for _, k := range keys {
		b, err := os.ReadFile(filepath.Join(dir, k+".jpg"))
		if err != nil {
			continue
		}
		_ = os.WriteFile(filepath.Join(tmp, k+".jpg"), b, 0o644)
	}
	out := map[string]heldCheck{}
	for _, q := range heldQueries {
		raw, err := exec.Command(py, script, "--model-dir", fdir, "--frames", tmp, "--query", q).Output()
		if err != nil && len(raw) == 0 {
			return out, fmt.Errorf("%v", err)
		}
		for _, line := range strings.Split(string(raw), "\n") {
			var fl falconLine
			if json.Unmarshal([]byte(strings.TrimSpace(line)), &fl) != nil || !fl.OK {
				continue
			}
			k := strings.TrimSuffix(fl.Frame, ".jpg")
			h := out[k]
			for _, b := range fl.Boxes {
				if b.Confidence >= heldConf && b.H >= heldHeight && !h.FalconYes {
					h.FalconYes = true
					h.Falcon = fmt.Sprintf("a %s (%.2f sure, %.0f%% of the frame tall)", q, b.Confidence, 100*b.H)
				}
			}
			out[k] = h
		}
	}
	return out, nil
}

type falconLine struct {
	OK    bool   `json:"ok"`
	Frame string `json:"frame"`
	Boxes []struct {
		X, Y, W, H float64
		Confidence float64
	} `json:"boxes"`
}

// gemmaHeld asks Gemma the held-up question about each frame.
func gemmaHeld(cfg config.Config, dir string, keys []string, logf func(string, ...any)) (map[string]heldCheck, error) {
	r := gemmaRunner(cfg, logf)
	if err := r.Ready(); err != nil {
		return nil, err
	}
	stop, err := r.Start(context.Background())
	if err != nil {
		return nil, err
	}
	defer stop()
	out := map[string]heldCheck{}
	for _, k := range keys {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		res, err := r.AnalyzeImage(ctx, filepath.Join(dir, k+".jpg"), avlm.ImageOptions{Prompt: heldPrompt, MaxTokens: 80, Temperature: 0.01})
		cancel()
		if err != nil {
			continue
		}
		var v struct {
			HoldingUp bool   `json:"holding_up"`
			What      string `json:"what"`
		}
		s := res.Text
		if i, j := strings.Index(s, "{"), strings.LastIndex(s, "}"); i >= 0 && j > i && json.Unmarshal([]byte(s[i:j+1]), &v) == nil {
			out[k] = heldCheck{Gemma: strings.TrimSpace(v.What), GemmaYes: v.HoldingUp}
		}
	}
	return out, nil
}
