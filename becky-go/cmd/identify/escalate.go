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
	"encoding/json"
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
	"becky-go/internal/forensicrun"
	"becky-go/internal/route"
	"becky-go/internal/systemone"
)

const (
	// LVFace scale (measured): wrong person at most 0.107 across 123 photos.
	lvBar       = 0.30 // LVFace must reach this to agree on a name
	lvMarginMin = 0.10 // ...and beat its own runner-up by this much
	lvFloor     = 0.15 // below this LVFace sees nobody it knows
	arcFloor    = 0.40 // below this ArcFace sees nobody it knows
	scrubWindow = 2.0  // seconds either side of the moment the frame-by-frame search may walk
)

// The routing menu. Each Strength is what System One reads when it picks. There is
// no "ask Jordan" step on it: code lists a face for Jordan only after EVERY check
// below has run and none settled it (Jordan, 2026-10-10: due diligence first).
var (
	stepGemma     = route.Step{Key: "gemma_compare", Strength: "Gemma-4 compares this person with an enrolled photo of the candidate twice: the face, and the whole person (hair colour and style, tattoos, piercings, build, sex). It names only when the face matches and nothing lasting contradicts it: measured 21 of 24 right and never a wrong name; unsure cases come back unclear. Needs a visible face. About 20-40 seconds."}
	stepFalcon    = route.Step{Key: "falcon_zoom", Strength: "Falcon-Perception finds where each person is in the frame, then the face models re-read that person zoomed in at full resolution. Strong when the face is small, far away, or there are several people. About 20 seconds."}
	stepRevSearch = route.Step{Key: "reverse_search", Strength: "Search the web (Yandex and Bing) for this face; both face models check every result picture, and the pages that show the SAME face are read for the candidate's name or another enrolled person's. Strong when the person is online (social media, YouTube, news, mugshots). About 1-2 minutes."}
	stepVoice     = route.Step{Key: "voice_check", Strength: "The voice in this video already matched the candidate. Do not name from the face alone: keep it as a hint that counts only together with that voice match. Strong when the picture is poor but the person talks."}
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
	people     string                      // "Name|alias,alias;..." for reverse search
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

func (e *escalator) menu(g grayFace) []route.Step {
	var m []route.Step
	if fileExists(e.cfg.GemmaModel) && fileExists(e.cfg.GemmaMMProj) {
		m = append(m, stepGemma)
	}
	if falcon.Ready() {
		m = append(m, stepFalcon)
	}
	if fileExists(e.cfg.RevSearchPython) {
		m = append(m, stepRevSearch)
	}
	if e.voiceNames[g.name] {
		m = append(m, stepVoice)
	}
	return m
}

// escalate settles one gray face. It returns a named face signal, or an
// unidentified entry, plus the trail.
//
// Order: (1) code steps through the video ONE FRAME at a time around the moment
// for a clear shot - no model choice needed; (2) System One picks among the
// remaining checks, one at a time, until one settles it; (3) only when every check
// has run does code list the face for Jordan, with what each check found.
func (e *escalator) escalate(g grayFace, fps float64) (*Identification, *Unidentified, FaceRoute) {
	trail := FaceRoute{Candidate: g.name, Timestamp: round3(g.ts), ArcFace: round4(g.read.arc), LVFace: round4(g.read.lv)}
	if id, unid, done := e.scrubStep(&g, fps, &trail); done {
		return id, unid, trail
	}
	menu := e.menu(g)
	tried := []string{"frame_scrub"}
	for len(menu) > 0 {
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
		case stepRevSearch.Key:
			ans, who, why := e.reverseSearch(g)
			rr.Result = why
			trail.Rounds = append(trail.Rounds, rr)
			switch ans {
			case "yes":
				trail.Outcome = "named"
				return e.named(g, fps, "cosine+web"), nil, trail
			case "no":
				trail.Outcome = "not-them"
				return nil, e.notThem(g, "web pages showing the same face name "+who), trail
			}
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
		case stepFalcon.Key:
			recs, frames, err := e.falconZoom(g)
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
	}
	// Every check ran and none settled it: only now is the face listed for Jordan.
	trail.Rounds = append(trail.Rounds, RouteRound{Step: "hold", By: "code", Result: "every check ran and none settled it; listed for Jordan with what each one found"})
	trail.Outcome = "held"
	return nil, e.held(g, trail.Rounds), trail
}

// scrubStep looks for a clear shot of this face by stepping through the video ONE
// FRAME at a time outward from the moment, both directions (faceembed.Scrub). A
// clear shot that both models name settles it; a clear shot that matches nobody
// is a stranger; otherwise the clearest view carries on to the routed checks.
func (e *escalator) scrubStep(g *grayFace, fps float64, trail *FaceRoute) (*Identification, *Unidentified, bool) {
	rr := RouteRound{Step: "frame_scrub", By: "code"}
	dir, err := os.MkdirTemp(e.tmp, "scrub_")
	var res faceembed.ScrubResult
	if err == nil {
		res, err = faceembed.Scrub(e.cfg, e.input, g.ts, scrubWindow, fps, e.rotation, g.rec.BBox, dir, e.device, e.verbose)
	}
	if err == nil {
		res = sameFaceViews(res, g.rec.Vector)
	}
	if err != nil || len(res.Views) == 0 {
		rr.Result = fmt.Sprintf("stepped through %d of %d frames one at a time; no other view of this face", res.Looked, res.Total)
		if err != nil {
			rr.Result = "could not step through the frames: " + err.Error()
		}
		trail.Rounds = append(trail.Rounds, rr)
		return nil, nil, false
	}
	for i, v := range res.Views { // any view both models agree on settles it, even as someone else
		if r := read(v, e.enrolled); classify(r, e.threshold) == verdictYes {
			rr.Result = fmt.Sprintf("stepped through %d of %d frames one at a time; at %.2fs both models name %s (ArcFace %.2f, LVFace %.2f)", res.Looked, res.Total, res.Times[i], r.arcName, r.arc, r.lv)
			trail.Rounds, trail.Outcome = append(trail.Rounds, rr), "named"
			g.name, g.read, g.rec, g.frame, g.ts = r.arcName, r, v, v.Path, res.Times[i]
			return e.named(*g, fps, "cosine+lvface+frame_scrub"), nil, true
		}
	}
	best := res.Views[0]
	r := read(best, e.enrolled)
	shot := "no clear shot in the window"
	if best.Clear {
		shot = "clear shot"
	}
	rr.Result = fmt.Sprintf("stepped through %d of %d frames one at a time; best view at %.2fs (%s): ArcFace %.2f, LVFace %.2f", res.Looked, res.Total, res.Times[0], shot, r.arc, r.lv)
	trail.Rounds = append(trail.Rounds, rr)
	if best.Clear && classify(r, e.threshold) == verdictNo {
		trail.Outcome = "not-them"
		return nil, e.notThem(*g, "a clear shot, found by stepping through the video frame by frame, matched nobody enrolled"), true
	}
	g.read, g.rec, g.frame, g.ts = r, best, best.Path, res.Times[0] // the clearest view goes on to the checks
	return nil, nil, false
}

// sameTrack: ArcFace between two views of the same person in one video stays well
// above this; a cut to someone else drops near 0 (face_embed.py SAME_TRACK).
const sameTrack = 0.25

// sameFaceViews keeps only the scrub views that are the face we started from, so a
// cut or a person walking into the spot can never stand in for it (pure; unit-tested).
func sameFaceViews(res faceembed.ScrubResult, start []float64) faceembed.ScrubResult {
	if len(start) == 0 {
		return res
	}
	v0 := normalize(start)
	out := res
	out.Views, out.Times = nil, nil
	for i, v := range res.Views {
		if len(v.Vector) > 0 && cosine(normalize(v.Vector), v0) >= sameTrack {
			out.Views = append(out.Views, v)
			out.Times = append(out.Times, res.Times[i])
		}
	}
	return out
}

// revOut is the part of becky-revsearch's JSON this step reads.
type revOut struct {
	SameFace []struct {
		Text  string   `json:"text"`
		Page  string   `json:"page"`
		Names []string `json:"names"`
	} `json:"same_face"`
	NamesSeen map[string]int `json:"names_seen"`
	Summary   string         `json:"summary"`
}

// reverseSearch runs becky-revsearch on the face (head and hair) and reads the
// pages that show the SAME face (both face models) for enrolled names.
// It returns yes (they name the candidate), no (they name someone else, who), or unclear.
func (e *escalator) reverseSearch(g grayFace) (string, string, string) {
	tag := fmt.Sprintf("%.0f", g.ts*10)
	q, err := headCrop(g.frame, g.rec.BBox, filepath.Join(e.tmp, "rev_"+tag+".jpg"))
	if err != nil {
		return "unclear", "", "could not crop the face: " + err.Error()
	}
	// Compare with the face as both models already read it: a hair-covered face found
	// only with the border retry may not be found again in the crop.
	faceFile := filepath.Join(e.tmp, "rev_"+tag+".json")
	fb, _ := json.Marshal(map[string][]float64{"vector": g.rec.Vector, "vector2": g.rec.Vector2})
	if err := os.WriteFile(faceFile, fb, 0o644); err != nil {
		return "unclear", "", "could not save the face for the search: " + err.Error()
	}
	args := []string{q, "--face", faceFile, "--names", e.people, "--out", filepath.Join(e.tmp, "rev_"+tag)}
	if e.device != "" {
		args = append(args, "--device", e.device)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	raw, err := forensicrun.RunTool(ctx, "becky-revsearch", args...)
	var o revOut
	if err != nil || json.Unmarshal(raw, &o) != nil {
		return "unclear", "", fmt.Sprintf("reverse image search did not run: %v", err)
	}
	return readRevSearch(o, g.name)
}

// readRevSearch decides from the same-face pages (pure; unit-tested).
func readRevSearch(o revOut, candidate string) (string, string, string) {
	other, otherN := "", 0
	for n, c := range o.NamesSeen {
		if n != candidate && (c > otherN || c == otherN && n < other) {
			other, otherN = n, c
		}
	}
	cand := o.NamesSeen[candidate]
	var titles []string
	for i, s := range o.SameFace {
		if i == 3 {
			break
		}
		titles = append(titles, oneLine(s.Text+" "+s.Page))
	}
	why := o.Summary
	if len(titles) > 0 {
		why += " - e.g. " + strings.Join(titles, " / ")
	}
	switch {
	case cand > 0 && cand > otherN:
		return "yes", "", why
	case otherN > 0 && otherN > cand:
		return "no", other, why
	}
	return "unclear", "", why
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

// Two questions, two crops (measured on 24 photos x 3 people, 2026-10-10, Gemma-4 E4B):
//   - the FACE question on head crops: 23/24 right, but once said "same" for a stranger
//     who looks like Clancy;
//   - the WHOLE-PERSON question on head-to-chest crops (hair colour and style, tattoos,
//     piercings, build, sex): it catches what a face model cannot see.
//
// Named only when the face says yes AND the whole person does not say no: 21/24 right,
// 0 wrong names; the 3 misses come back "unclear" and routing goes on.
const (
	gemmaFacePrompt = "Do these two pictures show the same person? Judge the face only: face shape, eyes, nose, mouth, jaw, " +
		"facial hair and sex. Answer with exactly one word: yes, no or unclear."
	gemmaPersonPrompt = `Picture 1 and picture 2: is this the same person? Compare everything you can see, one line each, answering same, different or cant_see:
face: (face shape, eyes, nose, mouth, jaw)
hair: (colour, style, length - an unusual colour such as neon green is a strong clue either way)
facial hair:
tattoos:
piercings:
build: (body shape and size)
sex:
Then the last line, exactly: same person: yes, no or unclear. Say yes only if the face matches and nothing clearly contradicts it; say no if the face or any lasting feature (tattoos, sex, build) clearly differs.`
)

// gemmaVerdict combines the two answers (pure; unit-tested).
func gemmaVerdict(face, person string) string {
	switch {
	case face == "no":
		return "no"
	case face == "yes" && person != "no":
		return "yes"
	}
	return "unclear" // the face is unsure, or the face and the whole person disagree
}

// gemmaSame asks Gemma-4 E4B whether the gray face and the candidate's closest
// enrolled photo show the same person: the face (head crops) and the whole person
// (hair, tattoos, piercings, build; head-to-chest crops).
func (e *escalator) gemmaSame(g grayFace) (string, string) {
	ref, ok := e.closestRef(g)
	if !ok {
		return "unclear", "no enrolled photo to compare with"
	}
	tag := fmt.Sprintf("%.0f", g.ts*10)
	a, err := headCrop(g.frame, g.rec.BBox, filepath.Join(e.tmp, "q_"+tag+".jpg"))
	if err != nil {
		return "unclear", "could not crop the face: " + err.Error()
	}
	b, err := headCrop(ref.Path, ref.BBox, filepath.Join(e.tmp, "ref_"+safeName(g.name)+".jpg"))
	if err != nil {
		return "unclear", "could not crop the enrolled photo: " + err.Error()
	}
	pa, err1 := personCrop(g.frame, g.rec.BBox, filepath.Join(e.tmp, "qp_"+tag+".jpg"))
	pb, err2 := personCrop(ref.Path, ref.BBox, filepath.Join(e.tmp, "refp_"+safeName(g.name)+".jpg"))
	if err := e.startGemma(); err != nil {
		return "unclear", "Gemma could not start: " + err.Error()
	}
	face, err := e.askGemma([]string{a, b}, gemmaFacePrompt, 10)
	if err != nil {
		return "unclear", "Gemma did not answer: " + err.Error()
	}
	face = gemmaWord(face)
	person, seen := "unclear", "whole-person view not available"
	if err1 == nil && err2 == nil {
		if txt, err := e.askGemma([]string{pa, pb}, gemmaPersonPrompt, 160); err == nil {
			person, seen = personWord(txt), oneLine(txt)
		}
	}
	v := gemmaVerdict(face, person)
	return v, fmt.Sprintf("Gemma-4 E4B vs %s: face %s, whole person %s (%s) -> %s", filepath.Base(ref.Path), face, person, seen, v)
}

func (e *escalator) startGemma() error {
	if e.gemma != nil {
		return nil
	}
	// Always E4B: the 12B was measured worse at this exact comparison.
	e.gemma = avlm.New(e.cfg.GemmaModel, e.cfg.GemmaMMProj, e.cfg.LlamaServer, "", e.cfg.FFmpeg, e.cfg.FFprobe, nil)
	stop, err := e.gemma.Start(context.Background())
	if err != nil {
		e.gemma = nil
		return err
	}
	e.stopGemma = stop
	return nil
}

func (e *escalator) askGemma(imgs []string, prompt string, maxTokens int) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	res, err := e.gemma.AnalyzeImages(ctx, imgs, avlm.ImageOptions{Prompt: prompt, MaxTokens: maxTokens, Temperature: 0.01})
	return res.Text, err
}

// personWord reads the "same person: yes|no|unclear" line of the whole-person answer.
func personWord(s string) string {
	low := strings.ToLower(s)
	i := strings.LastIndex(low, "same person:")
	if i < 0 {
		return "unclear"
	}
	return gemmaWord(low[i+len("same person:"):])
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 400 {
		s = s[:400]
	}
	return s
}

// personCrop cuts head to chest around a face box (hair, piercings, neck and chest
// tattoos, build): 1.2 face-widths each side, 0.9 above, 4 below.
func personCrop(src string, box []float64, dst string) (string, error) {
	img, err := loadImage(src)
	if err != nil {
		return "", err
	}
	if len(box) != 4 {
		return "", fmt.Errorf("no face box")
	}
	w, h := box[2]-box[0], box[3]-box[1]
	r := image.Rect(int(box[0]-w*1.2), int(box[1]-h*0.9), int(box[2]+w*1.2), int(box[3]+h*4)).Intersect(img.Bounds())
	if r.Empty() {
		return "", fmt.Errorf("face box outside the picture")
	}
	return dst, saveJPEG(dst, crop(img, r))
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

// held is the only way a face reaches Jordan: after every check ran, with what each one found.
func (e *escalator) held(g grayFace, rounds []RouteRound) *Unidentified {
	why := fmt.Sprintf("ArcFace %.2f (%s), LVFace %.2f (%s)", g.read.arc, g.read.arcName, g.read.lv, g.read.lvName)
	for _, r := range rounds {
		if r.Step != "hold" {
			why += "; " + r.Step + ": " + r.Result
		}
	}
	return &Unidentified{Type: "face", Description: fmt.Sprintf("possible %s at %.1fs: every check ran and none settled it - needs a person to check", g.name, g.ts),
		Confidence: round4(g.read.arc), Candidate: g.name, CandidateConfidence: round4(g.read.arc), WhyUnnamed: why}
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
