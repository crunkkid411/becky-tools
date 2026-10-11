// escalate.go - becky-identify's self-escalation for faces (2026-10-10).
//
// Two face models read every face: ArcFace (buffalo_l) and LVFace-B, on the same
// aligned crop. Code settles the obvious frames with no model call:
//   - both models pick the same person and clear their bars -> named;
//   - both scores far below any match -> unknown.
//
// Every other frame is a GRAY case (the models disagree, or one is unsure). For
// those, System One (internal/route) picks the next step from a menu, each step
// described by what that tool is measurably good at; code only follows a sure
// pick, else it takes the safe lane. System One never names anyone.
//
// Measured on Jordan's footage (research/kb-final-face-voice-check-2026-10-10.md):
// Gemma-4 E4B comparing two head crops was right 22/24 and never said "same" for
// a different person; Gemma-4 12B was WORSE (13/24, it called Jordan and Shelby
// the same person), so this step always uses E4B.
package main

import (
	"context"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // enrolled prints can be PNG
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"becky-go/internal/avlm"
	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/faceembed"
	"becky-go/internal/falcon"
	"becky-go/internal/osintexport"
	"becky-go/internal/route"
	"becky-go/internal/systemone"
)

const (
	// LVFace scale (measured): wrong person at most 0.107 across 123 photos.
	lvBar       = 0.30 // LVFace must reach this to agree on a name
	lvMarginMin = 0.10 // ...and beat its own runner-up by this much
	lvFloor     = 0.15 // below this LVFace sees nobody it knows
	arcFloor    = 0.40 // below this ArcFace sees nobody it knows
	maxRounds   = 3    // routing rounds per gray face
)

// The routing menu. Each Strength is what System One reads when it picks.
var (
	stepGemma  = route.Step{Key: "gemma_compare", Strength: "Gemma-4 looks at this face next to an enrolled photo of the candidate and says same person or not. Strong on hair, beard, sex, tattoos and face shape; measured 22 of 24 right and never said 'same' for a different person. Needs a reasonably visible face. About 10-30 seconds."}
	stepFalcon = route.Step{Key: "falcon_zoom", Strength: "Falcon-Perception finds where each person is in the frame, then the face models re-read that person zoomed in at full resolution. Strong when the face is small, far away, or there are several people. About 20 seconds."}
	stepFrames = route.Step{Key: "more_frames", Strength: "Read 4 more frames from the second around this moment with both face models. Strong when the face is blurred, turned, or covered in this one frame. About 10 seconds."}
	stepVoice  = route.Step{Key: "voice_check", Strength: "Do not name from the face. Keep it as a hint that only counts if the voice in the same video independently matches the same person. Strong when the picture is poor but the person talks."}
	stepHold   = route.Step{Key: "hold", Strength: "Stop and list this face for Jordan to check by eye. Right when nothing else can settle it."}
)

// faceVerdict is what code makes of one frame-face.
type faceVerdict int

const (
	verdictNo faceVerdict = iota
	verdictGray
	verdictYes
)

// faceRead is both models' best guess for one face.
type faceRead struct {
	arcName, lvName string
	arc, lv         float64
	lvMargin        float64
	hasLV           bool
}

// classify settles the obvious cases (pure; unit-tested).
func classify(r faceRead, threshold float64) faceVerdict {
	if !r.hasLV { // LVFace missing: ArcFace alone, as before, plus a gray band
		switch {
		case r.arc >= threshold:
			return verdictYes
		case r.arc < arcFloor:
			return verdictNo
		}
		return verdictGray
	}
	if r.arcName == r.lvName && r.arc >= threshold && r.lv >= lvBar && r.lvMargin >= lvMarginMin {
		return verdictYes
	}
	if r.arc < arcFloor && r.lv < lvFloor {
		return verdictNo
	}
	return verdictGray
}

// read scores one face record against the enrolled people with both models.
func read(f faceembed.Face, enrolled []enrolledFace) faceRead {
	var r faceRead
	v := normalize(f.Vector)
	for _, e := range enrolled {
		if s := cosine(v, e.vector); s > r.arc {
			r.arc, r.arcName = s, e.name
		}
	}
	if len(f.Vector2) == 0 {
		return r
	}
	v2 := normalize(f.Vector2)
	second := -1.0
	r.lv = -1
	for _, e := range enrolled {
		if len(e.vector2) == 0 {
			continue
		}
		r.hasLV = true
		s := cosine(v2, e.vector2)
		if s > r.lv {
			second, r.lv, r.lvName = r.lv, s, e.name
		} else if s > second {
			second = s
		}
	}
	if !r.hasLV {
		r.lv = 0
		return r
	}
	r.lvMargin = r.lv - math.Max(second, 0)
	return r
}

// grayFace is one unsettled face, the best one per candidate name.
type grayFace struct {
	name  string
	read  faceRead
	rec   faceembed.Face
	frame string
	ts    float64
}

// RouteRound / FaceRoute are the audit trail in the output (routing[]).
type RouteRound struct {
	Step       string  `json:"step"`
	By         string  `json:"by"`
	Confidence float64 `json:"confidence,omitempty"`
	Picked     string  `json:"system_one_leaned,omitempty"`
	Result     string  `json:"result"`
}

type FaceRoute struct {
	Candidate string       `json:"candidate"`
	Timestamp float64      `json:"timestamp"`
	ArcFace   float64      `json:"arcface"`
	LVFace    float64      `json:"lvface"`
	Rounds    []RouteRound `json:"rounds"`
	Outcome   string       `json:"outcome"`
}

// escalator holds what the steps need for one becky-identify run.
type escalator struct {
	cfg        config.Config
	input      string
	rotation   int
	device     string
	verbose    bool
	threshold  float64
	enrolled   []enrolledFace
	refs       map[string][]faceembed.Face // enrolled records per name (for Gemma's reference photo)
	voiceNames map[string]bool             // names the voice step already heard
	hasAudio   bool
	tmp        string
	gemma      *avlm.Runner
	stopGemma  func()
}

func (e *escalator) close() {
	if e.stopGemma != nil {
		e.stopGemma()
	}
}

func (e *escalator) menu() []route.Step {
	var m []route.Step
	if fileExists(e.cfg.GemmaModel) && fileExists(e.cfg.GemmaMMProj) {
		m = append(m, stepGemma)
	}
	if falcon.Ready() {
		m = append(m, stepFalcon)
	}
	m = append(m, stepFrames)
	if e.hasAudio {
		m = append(m, stepVoice)
	}
	return append(m, stepHold)
}

// escalate settles one gray face. It returns a named face signal, or an
// unidentified entry, plus the trail.
func (e *escalator) escalate(g grayFace, fps float64) (*Identification, *Unidentified, FaceRoute) {
	trail := FaceRoute{Candidate: g.name, Timestamp: round3(g.ts), ArcFace: round4(g.read.arc), LVFace: round4(g.read.lv)}
	menu := e.menu()
	var tried []string
	for round := 0; round < maxRounds; round++ {
		d := route.Next(context.Background(), route.Case{
			Tool:    "becky-identify",
			Task:    "Two face models are unsure whether the face in the picture is " + g.name + ". Pick the step most likely to settle it.",
			State:   e.state(g, tried),
			Images:  []string{g.frame},
			Menu:    menu,
			Default: menu[0].Key, // safe lane: the first step still on the menu (Gemma, if installed)
			Extra: map[string]systemone.Question{
				"face_clear":     systemone.Noul("The face in the picture is large, sharp and facing the camera."),
				"several_people": systemone.Noul("More than one person is in the picture."),
			},
		})
		rr := RouteRound{Step: d.Step, By: d.By, Confidence: d.Confidence, Picked: d.Picked}
		beckyio.Logf(e.verbose, "face: %s at %.1fs is unsure -> %s (%s)", g.name, g.ts, d.Step, d.By)
		tried = append(tried, d.Step)
		menu = route.Without(menu, d.Step)

		switch d.Step {
		case stepVoice.Key:
			rr.Result = "kept as a hint that needs the voice to agree"
			trail.Rounds, trail.Outcome = append(trail.Rounds, rr), "needs-voice"
			return e.voiceHint(g, fps), nil, trail
		case stepHold.Key:
			rr.Result = "listed for Jordan to check"
			trail.Rounds, trail.Outcome = append(trail.Rounds, rr), "held"
			return nil, e.held(g), trail
		case stepGemma.Key:
			same, why := e.gemmaSame(g)
			rr.Result = why
			trail.Rounds = append(trail.Rounds, rr)
			switch same {
			case "yes":
				trail.Outcome = "named"
				return e.named(g, fps, "cosine+gemma"), nil, trail
			case "no":
				trail.Outcome = "not-them"
				return nil, e.notThem(g, "Gemma compared it with an enrolled photo and said it is a different person"), trail
			}
		case stepFalcon.Key, stepFrames.Key:
			var recs []faceembed.Face
			var frames []string
			var err error
			if d.Step == stepFalcon.Key {
				recs, frames, err = e.falconZoom(g)
			} else {
				recs, frames, err = e.moreFrames(g)
			}
			if err != nil {
				rr.Result = "could not run: " + err.Error()
				trail.Rounds = append(trail.Rounds, rr)
				continue
			}
			v, better, i := e.bestOf(g.name, recs)
			rr.Result = fmt.Sprintf("re-read %d face(s): ArcFace %.2f, LVFace %.2f", len(recs), better.arc, better.lv)
			trail.Rounds = append(trail.Rounds, rr)
			switch v {
			case verdictYes:
				trail.Outcome = "named"
				g.read = better
				return e.named(g, fps, "cosine+lvface+"+d.Step), nil, trail
			case verdictNo:
				trail.Outcome = "not-them"
				return nil, e.notThem(g, "a closer look with both face models matched nobody enrolled"), trail
			}
			if i >= 0 && better.arc > g.read.arc { // keep the best view for the next round
				g.read, g.rec, g.frame = better, recs[i], frames[i]
			}
		}
		if len(menu) == 0 {
			break
		}
	}
	trail.Outcome = "held"
	return nil, e.held(g), trail
}

func (e *escalator) state(g grayFace, tried []string) map[string]any {
	s := map[string]any{
		"candidate":          g.name,
		"arcface_score":      round4(g.read.arc),
		"arcface_name_bar":   e.threshold,
		"arcface_pick":       g.read.arcName,
		"faces_in_frame":     g.rec.NFaces,
		"face_width_px":      faceWidth(g.rec),
		"tight_crop_padded":  g.rec.Padded,
		"voice_heard_them":   e.voiceNames[g.name],
		"video_has_audio":    e.hasAudio,
		"steps_already_done": tried,
	}
	if g.read.hasLV {
		s["lvface_score"], s["lvface_name_bar"], s["lvface_pick"], s["lvface_margin"] = round4(g.read.lv), lvBar, g.read.lvName, round4(g.read.lvMargin)
		s["models_agree"] = g.read.arcName == g.read.lvName
	}
	return s
}

// bestOf re-reads faces and returns the best verdict for the candidate name.
func (e *escalator) bestOf(name string, recs []faceembed.Face) (faceVerdict, faceRead, int) {
	best, bestV, at := faceRead{}, verdictNo, -1
	for i, f := range recs {
		if !f.Found || len(f.Vector) == 0 {
			continue
		}
		r := read(f, e.enrolled)
		if r.arcName != name && (!r.hasLV || r.lvName != name) {
			continue // this view is of somebody else
		}
		v := classify(r, e.threshold)
		if at < 0 || v > bestV || (v == bestV && r.arc > best.arc) {
			best, bestV, at = r, v, i
		}
	}
	if at < 0 {
		return verdictGray, faceRead{}, -1 // nothing new learned; let routing continue
	}
	return bestV, best, at
}

func (e *escalator) moreFrames(g grayFace) ([]faceembed.Face, []string, error) {
	var paths []string
	for i, dt := range []float64{-1, -0.5, 0.5, 1} {
		t := g.ts + dt
		if t < 0 {
			continue
		}
		p := filepath.Join(e.tmp, fmt.Sprintf("near_%.0f_%d.jpg", g.ts*10, i))
		if osintexport.ExtractFrameRotated(e.cfg.FFmpeg, e.input, t, p, "jpg", faceJPEGQuality, e.rotation) == nil {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return nil, nil, fmt.Errorf("no nearby frames")
	}
	recs, err := faceembed.EmbedBoth(e.cfg, paths, e.device, e.verbose)
	return recs, paths, err
}

func (e *escalator) falconZoom(g grayFace) ([]faceembed.Face, []string, error) {
	dir, err := os.MkdirTemp(e.tmp, "falcon_")
	if err != nil {
		return nil, nil, err
	}
	img, err := loadImage(g.frame)
	if err != nil {
		return nil, nil, err
	}
	if err := saveJPEG(filepath.Join(dir, "f.jpg"), img); err != nil {
		return nil, nil, err
	}
	found, err := falcon.Find(dir, "person")
	if err != nil {
		return nil, nil, err
	}
	b := img.Bounds()
	W, H := float64(b.Dx()), float64(b.Dy())
	var paths []string
	for i, box := range found["f.jpg"] {
		if box.Confidence < 0.5 {
			continue
		}
		// a little room around the person so the face detector has margin
		r := image.Rect(int((box.X-0.05)*W), int((box.Y-0.05)*H), int((box.X+box.W+0.05)*W), int((box.Y+box.H+0.05)*H)).Intersect(b)
		p := filepath.Join(dir, fmt.Sprintf("person_%d.jpg", i))
		if saveJPEG(p, crop(img, r)) == nil {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return nil, nil, fmt.Errorf("Falcon found no person")
	}
	recs, err := faceembed.EmbedBoth(e.cfg, paths, e.device, e.verbose)
	return recs, paths, err
}

const gemmaPrompt = "Do these two pictures show the same person? Compare face shape, eyes, nose, facial hair, sex and tattoos; " +
	"hair colour can change. Answer with exactly one word: yes, no or unclear."

// gemmaSame asks Gemma-4 E4B whether the gray face and the candidate's closest
// enrolled photo show the same person (head crops, as measured).
func (e *escalator) gemmaSame(g grayFace) (string, string) {
	ref, ok := e.closestRef(g)
	if !ok {
		return "unclear", "no enrolled photo to compare with"
	}
	a, err := headCrop(g.frame, g.rec.BBox, filepath.Join(e.tmp, fmt.Sprintf("q_%.0f.jpg", g.ts*10)))
	if err != nil {
		return "unclear", "could not crop the face: " + err.Error()
	}
	b, err := headCrop(ref.Path, ref.BBox, filepath.Join(e.tmp, "ref_"+safeName(g.name)+".jpg"))
	if err != nil {
		return "unclear", "could not crop the enrolled photo: " + err.Error()
	}
	if e.gemma == nil {
		// Always E4B: the 12B was measured worse at this exact comparison.
		e.gemma = avlm.New(e.cfg.GemmaModel, e.cfg.GemmaMMProj, e.cfg.LlamaServer, "", e.cfg.FFmpeg, e.cfg.FFprobe, nil)
		stop, err := e.gemma.Start(context.Background())
		if err != nil {
			e.gemma = nil
			return "unclear", "Gemma could not start: " + err.Error()
		}
		e.stopGemma = stop
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	res, err := e.gemma.AnalyzeImages(ctx, []string{a, b}, avlm.ImageOptions{Prompt: gemmaPrompt, MaxTokens: 10, Temperature: 0.01})
	if err != nil {
		return "unclear", "Gemma did not answer: " + err.Error()
	}
	ans := gemmaWord(res.Text)
	return ans, fmt.Sprintf("Gemma-4 E4B compared it with %s: %s", filepath.Base(ref.Path), ans)
}

// gemmaWord reads Gemma's one-word answer; anything else counts as unclear.
func gemmaWord(s string) string {
	w := strings.Trim(strings.ToLower(strings.Fields(s + " unclear")[0]), " .,!\"'*")
	if w == "yes" || w == "no" {
		return w
	}
	return "unclear"
}

// closestRef is the candidate's enrolled photo whose face is nearest this one.
func (e *escalator) closestRef(g grayFace) (faceembed.Face, bool) {
	var best faceembed.Face
	bestSim := -2.0
	v := normalize(g.rec.Vector)
	for _, r := range e.refs[g.name] {
		if s := cosine(v, normalize(r.Vector)); s > bestSim && len(r.BBox) == 4 {
			best, bestSim = r, s
		}
	}
	return best, bestSim > -2
}

func (e *escalator) named(g grayFace, fps float64, match string) *Identification {
	return &Identification{Type: "face", Name: g.name, Confidence: round4(g.read.arc), Match: match,
		Frames: []FrameRef{{Frame: int(g.ts*fps + 0.5), Timestamp: round3(g.ts)}}}
}

// voiceHint is a face signal kept under the solo-naming floor, so fusion names
// the person only when the voice independently agrees.
func (e *escalator) voiceHint(g grayFace, fps float64) *Identification {
	id := e.named(g, fps, "cosine (needs voice)")
	id.Confidence = math.Min(id.Confidence, faceSoloFloor-0.0001)
	return id
}

func (e *escalator) held(g grayFace) *Unidentified {
	return &Unidentified{Type: "face", Description: "possible " + g.name + ": the face models could not settle it - needs a person to check",
		Confidence: round4(g.read.arc), Candidate: g.name, CandidateConfidence: round4(g.read.arc),
		WhyUnnamed: fmt.Sprintf("ArcFace %.2f (%s), LVFace %.2f (%s); escalation did not settle it", g.read.arc, g.read.arcName, g.read.lv, g.read.lvName)}
}

func (e *escalator) notThem(g grayFace, why string) *Unidentified {
	return &Unidentified{Type: "face", Description: "unidentified face", Confidence: round4(g.read.arc),
		WhyUnnamed: "looked a little like " + g.name + ", but " + why}
}

// headCrop cuts head + hair around a face box (2x the box, more room above), the
// crop Gemma was measured on.
func headCrop(src string, box []float64, dst string) (string, error) {
	img, err := loadImage(src)
	if err != nil {
		return "", err
	}
	if len(box) != 4 {
		return "", fmt.Errorf("no face box")
	}
	w, h := box[2]-box[0], box[3]-box[1]
	r := image.Rect(int(box[0]-w*0.5), int(box[1]-h*0.8), int(box[2]+w*0.5), int(box[3]+h*0.4)).Intersect(img.Bounds())
	if r.Empty() {
		return "", fmt.Errorf("face box outside the picture")
	}
	return dst, saveJPEG(dst, crop(img, r))
}

func loadImage(p string) (image.Image, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}

func crop(img image.Image, r image.Rectangle) image.Image {
	if s, ok := img.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok {
		return s.SubImage(r)
	}
	return img
}

func saveJPEG(p string, img image.Image) error {
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 92}); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func faceWidth(f faceembed.Face) int {
	if len(f.BBox) != 4 {
		return 0
	}
	return int(f.BBox[2] - f.BBox[0])
}

func safeName(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 128 && (r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return r
		}
		return '_'
	}, s)
}

func round3(f float64) float64 { return math.Round(f*1000) / 1000 }

// sortedGray orders gray faces by name so runs are deterministic.
func sortedGray(m map[string]grayFace) []grayFace {
	out := make([]grayFace, 0, len(m))
	for _, g := range m {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}
