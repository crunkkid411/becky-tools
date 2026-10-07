package main

// publish.go - report item #10, the publish-safety check. Local models never
// notice a copyright or privacy problem unless a step asks, so this step asks
// (Jordan loved the regions on the apology edit: memory
// feedback-flag-publish-risks-as-regions):
//
//  1. a frame every 2 s of the kept edit (cached by source time, so the three
//     model runs share them);
//  2. becky-ocr reads every frame; becky-ocr's own address pattern plus phone,
//     email, booking-code and ID patterns run over what it read;
//  3. Gemma-4 looks at the frames where the picture changes (and one every
//     10 s) and answers three fixed questions: another creator's content on
//     screen? a document with names? readable private details?
//
// An OCR pattern hit is concrete text and always becomes a region. A vision-only
// hit becomes a region when two or more frames in a row agree; a single frame is
// listed in the report only (a lone weak signal is a candidate, not a finding).

import (
	"context"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"becky-go/internal/avlm"
)

const (
	frameStep    = 2.0 // seconds of source between sampled frames
	visionEvery  = 5   // also ask Gemma about every 5th frame (10 s) on a static picture
	changeLevel  = 14  // mean grey-level change (0-255) that counts as a new picture
	ocrMinConf   = 0.6 // OCR lines below this are ignored for the patterns
	ocrHeavyText = 40  // a frame with this many characters of text is shown to Gemma
)

var (
	phoneRe = regexp.MustCompile(`\(?\b\d{3}\)?[-.\s]\d{3}[-.\s]\d{4}\b`)
	emailRe = regexp.MustCompile(`(?i)\b[\w.+-]+@[\w-]+\.[a-z]{2,}\b`)
	ssnRe   = regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`)
	codeCue = regexp.MustCompile(`(?i)confirmation|booking|reservation|record locator|\bpnr\b|itinerary|ticket|case (no|number)|report (no|number)`)
	codeRe  = regexp.MustCompile(`\b[A-Z0-9]{6}\b`)
)

// Finding is one publish risk, in source time.
type Finding struct {
	Kind     string  `json:"kind"`
	Src0     float64 `json:"src0"`
	Src1     float64 `json:"src1"`
	Evidence string  `json:"evidence"`
	Region   bool    `json:"region"` // strong enough to put on the timeline
}

type frameInfo struct {
	Src     float64      `json:"src"`
	File    string       `json:"file"`
	OCR     []string     `json:"ocr,omitempty"`
	OCRDone bool         `json:"ocr_done"`
	Vision  *visionReply `json:"vision,omitempty"`
}

type visionReply struct {
	OtherCreator bool   `json:"other_creator_content"`
	Document     bool   `json:"document_with_names"`
	Private      bool   `json:"private_info"`
	What         string `json:"what"`
}

// gridFrames: source times on the global 2 s grid that the edit keeps.
func gridFrames(pieces []span) []float64 {
	var out []float64
	for _, p := range pieces {
		for t := math.Ceil(p.A/frameStep) * frameStep; t < p.B; t += frameStep {
			out = append(out, math.Round(t*1000)/1000)
		}
	}
	sort.Float64s(out)
	return out
}

func extractFrame(ffmpeg, media string, t float64, out string) error {
	cmd := exec.Command(ffmpeg, "-v", "error", "-y", "-ss", fmt.Sprintf("%.3f", t), "-i", media,
		"-frames:v", "1", "-vf", "scale=-2:1280", "-q:v", "3", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, lastLines(string(b), 2))
	}
	return nil
}

// thumb is a 16x16 grey thumbnail for "did the picture change?".
func thumb(path string) []float64 {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	img, err := jpeg.Decode(f)
	if err != nil {
		return nil
	}
	b := img.Bounds()
	out := make([]float64, 256)
	n := make([]float64, 256)
	for y := b.Min.Y; y < b.Max.Y; y += 4 {
		for x := b.Min.X; x < b.Max.X; x += 4 {
			r, g, bl, _ := img.At(x, y).RGBA()
			i := ((y-b.Min.Y)*16/b.Dy())*16 + (x-b.Min.X)*16/b.Dx()
			out[i] += (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(bl)) / 257
			n[i]++
		}
	}
	for i := range out {
		if n[i] > 0 {
			out[i] /= n[i]
		}
	}
	return out
}

func thumbDiff(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 255
	}
	d := 0.0
	for i := range a {
		d += math.Abs(a[i] - b[i])
	}
	return d / float64(len(a))
}

// ocrPatterns returns the pattern hits in one frame's text.
func ocrPatterns(lines []string, addressLines []string) []string {
	var hits []string
	text := strings.Join(lines, " ")
	for _, a := range addressLines {
		hits = append(hits, "address: "+a)
	}
	for _, m := range phoneRe.FindAllString(text, 3) {
		hits = append(hits, "phone number: "+m)
	}
	for _, m := range emailRe.FindAllString(text, 3) {
		hits = append(hits, "email: "+m)
	}
	for _, m := range ssnRe.FindAllString(text, 2) {
		hits = append(hits, "ID number: "+m)
	}
	if codeCue.MatchString(text) {
		for _, m := range codeRe.FindAllString(text, 3) {
			if strings.ContainsAny(m, "0123456789") && strings.ContainsAny(m, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
				hits = append(hits, "booking/case code: "+m)
			}
		}
	}
	return hits
}

// runOCR reads every frame that has not been read yet (one becky-ocr call).
func runOCR(frames []*frameInfo, work string, logf func(string, ...any)) (map[string][]string, error) {
	addr := map[string][]string{}
	var todo []*frameInfo
	for _, f := range frames {
		if !f.OCRDone {
			todo = append(todo, f)
		}
	}
	if len(todo) == 0 {
		return addr, nil
	}
	bin, err := beckyBin("becky-ocr")
	if err != nil {
		return addr, err
	}
	dir, err := os.MkdirTemp(work, "ocr-")
	if err != nil {
		return addr, err
	}
	defer os.RemoveAll(dir)
	byName := map[string]*frameInfo{}
	for _, f := range todo {
		b, err := os.ReadFile(f.File)
		if err != nil {
			continue
		}
		name := filepath.Base(f.File)
		if os.WriteFile(filepath.Join(dir, name), b, 0o644) == nil {
			byName[name] = f
		}
	}
	out := filepath.Join(work, "ocr-last.json")
	logf("publish check: reading the text in %d frames...", len(byName))
	if res, err := exec.Command(bin, "--frames-dir", dir, "--output", out).CombinedOutput(); err != nil {
		return addr, fmt.Errorf("becky-ocr: %v (%s)", err, lastLines(string(res), 2))
	}
	var rep struct {
		Results []struct {
			FramePath string `json:"frame_path"`
			Lines     []struct {
				Text       string  `json:"text"`
				Confidence float64 `json:"confidence"`
				Category   string  `json:"category"`
			} `json:"lines"`
		} `json:"results"`
	}
	b, err := os.ReadFile(out)
	if err != nil {
		return addr, err
	}
	if err := json.Unmarshal(b, &rep); err != nil {
		return addr, fmt.Errorf("becky-ocr answer: %w", err)
	}
	for _, r := range rep.Results {
		f := byName[filepath.Base(r.FramePath)]
		if f == nil {
			continue
		}
		f.OCRDone = true
		f.OCR = nil
		for _, l := range r.Lines {
			if l.Confidence >= ocrMinConf {
				f.OCR = append(f.OCR, l.Text)
				if l.Category == "candidate_address" {
					addr[f.File] = append(addr[f.File], l.Text)
				}
			}
		}
	}
	for _, f := range todo { // a frame becky-ocr skipped (no text) is done too
		f.OCRDone = true
	}
	return addr, nil
}

const visionPrompt = `Look at this frame from Hair Jordan's livestream. It will be published on YouTube. Answer with JSON only:
{"other_creator_content": true if a video, livestream, TikTok, YouTube channel or social media post from ANOTHER person is shown on screen,
 "document_with_names": true if a document, police report, court paper, letter, email, text message or chat that shows people's names is readable,
 "private_info": true if a street address, phone number, email address, license plate, booking or confirmation code or ID number is readable,
 "what": "what is on screen, at most 12 words"}
Jordan talking to his camera by himself is not a risk.`

func parseVision(s string) (*visionReply, error) {
	i, j := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if i < 0 || j <= i {
		return nil, fmt.Errorf("no JSON in %q", s)
	}
	var v visionReply
	if err := json.Unmarshal([]byte(s[i:j+1]), &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// publishCheck runs the whole step and returns findings in source time.
func publishCheck(media, ffmpeg, ffprobe, gemma, mmproj, server string, pieces []span, work string, logf func(string, ...any)) ([]Finding, []string) {
	var notes []string
	cachePath := filepath.Join(work, "publish-cache.json")
	cache := map[string]*frameInfo{}
	if b, err := os.ReadFile(cachePath); err == nil {
		_ = json.Unmarshal(b, &cache)
	}
	save := func() {
		b, _ := json.MarshalIndent(cache, "", " ")
		_ = os.WriteFile(cachePath, b, 0o644)
	}
	frameDir := filepath.Join(work, "frames")
	_ = os.MkdirAll(frameDir, 0o755)

	times := gridFrames(pieces)
	frames := make([]*frameInfo, 0, len(times))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	failed := 0
	logf("publish check: %d frames of the edit (one every %.0f s)...", len(times), frameStep)
	for _, t := range times {
		key := fmt.Sprintf("%09.3f", t)
		f := cache[key]
		if f == nil {
			f = &frameInfo{Src: t, File: filepath.Join(frameDir, key+".jpg")}
			cache[key] = f
		}
		frames = append(frames, f)
		if _, err := os.Stat(f.File); err == nil {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(f *frameInfo) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := extractFrame(ffmpeg, media, f.Src, f.File); err != nil {
				mu.Lock()
				failed++
				mu.Unlock()
			}
		}(f)
	}
	wg.Wait()
	if failed > 0 {
		notes = append(notes, fmt.Sprintf("%d frames could not be extracted", failed))
	}

	addr, err := runOCR(frames, work, logf)
	if err != nil {
		notes = append(notes, "text reading (OCR) did not run: "+err.Error())
	}
	save()

	// Which frames Gemma looks at: a new picture, heavy text, or one every 10 s.
	var ask []*frameInfo
	var prev []float64
	for i, f := range frames {
		th := thumb(f.File)
		changed := thumbDiff(th, prev) > changeLevel
		prev = th
		if f.Vision == nil && (i == 0 || changed || i%visionEvery == 0 || len(strings.Join(f.OCR, " ")) >= ocrHeavyText) {
			ask = append(ask, f)
		}
	}
	if len(ask) > 0 {
		logf("publish check: Gemma-4 is looking at %d frames...", len(ask))
		r := avlm.New(gemma, mmproj, server, "", ffmpeg, ffprobe, nil)
		if vegasOpen() {
			r.NGL = 0 // never take the graphics memory an open VEGAS needs (picture.go)
			logf("  (VEGAS is open, so Gemma runs on the processor and leaves the graphics card to VEGAS)")
		}
		if err := r.Ready(); err != nil {
			notes = append(notes, "the vision check did not run: "+err.Error())
		} else if stop, err := r.Start(context.Background()); err != nil {
			notes = append(notes, "the vision check did not start: "+err.Error())
		} else {
			bad := 0
			for k, f := range ask {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				res, err := r.AnalyzeImage(ctx, f.File, avlm.ImageOptions{Prompt: visionPrompt, MaxTokens: 160, Temperature: 0.01})
				cancel()
				if err == nil {
					f.Vision, err = parseVision(res.Text)
				}
				if err != nil {
					bad++
				}
				if (k+1)%25 == 0 {
					logf("  %d/%d", k+1, len(ask))
					save()
				}
			}
			stop()
			if bad > 0 {
				notes = append(notes, fmt.Sprintf("Gemma gave no usable answer for %d of %d frames", bad, len(ask)))
			}
		}
		save()
	}
	return mergeFindings(frames, addr), notes
}

// mergeFindings turns per-frame hits into spans: consecutive frames (2 s apart)
// with the same kind of hit become one finding.
func mergeFindings(frames []*frameInfo, addr map[string][]string) []Finding {
	type hit struct {
		src      float64
		evidence string
	}
	byKind := map[string][]hit{}
	for _, f := range frames {
		for _, h := range ocrPatterns(f.OCR, addr[f.File]) {
			kind := "Readable " + strings.SplitN(h, ":", 2)[0]
			byKind[kind] = append(byKind[kind], hit{f.Src, h})
		}
		if v := f.Vision; v != nil {
			if v.OtherCreator {
				byKind["Another creator's content on screen"] = append(byKind["Another creator's content on screen"], hit{f.Src, v.What})
			}
			if v.Document {
				byKind["A document with names on screen"] = append(byKind["A document with names on screen"], hit{f.Src, v.What})
			}
			if v.Private {
				byKind["Private details on screen"] = append(byKind["Private details on screen"], hit{f.Src, v.What})
			}
		}
	}
	var out []Finding
	for kind, hs := range byKind {
		sort.Slice(hs, func(i, j int) bool { return hs[i].src < hs[j].src })
		ocr := strings.HasPrefix(kind, "Readable ")
		for i := 0; i < len(hs); {
			j := i
			for j+1 < len(hs) && hs[j+1].src-hs[j].src <= frameStep*visionEvery+0.01 {
				j++
			}
			ev := []string{}
			seen := map[string]bool{}
			for _, h := range hs[i : j+1] {
				if !seen[h.evidence] && len(ev) < 3 {
					seen[h.evidence] = true
					ev = append(ev, h.evidence)
				}
			}
			out = append(out, Finding{Kind: kind, Src0: math.Max(0, hs[i].src-frameStep/2), Src1: hs[j].src + frameStep/2,
				Evidence: strings.Join(ev, "; "), Region: ocr || j > i})
			i = j + 1
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Src0 < out[j].Src0 })
	return out
}
