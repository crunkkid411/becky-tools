// becky-livestream - the livestream clip-down workflow (Jordan, 2026-10-05):
// ONE call edits a livestream recording down to the topics he asks for and
// leaves a saved VEGAS project next to the footage. Every step runs by itself,
// every time, in this order; a model is asked ONLY what to keep.
//
//	becky-livestream --model gemma|qwen|claude [--guidance "what to keep"] [video]
//
// No video: the one video in the current folder (several: it asks which).
// No --guidance: <video name>.guidance.txt, then guidance.txt beside the video,
// else it asks for one line.
//
//  1. transcript, two passes (becky-transcribe: Parakeet + WhisperX second
//     opinion + Jordan's word list), cached beside the footage in becky-edit\
//  2. becky-cut --dry-run: where the silence is (cached)
//  3. the content decision (select.go): Gemma or Qwen lead and the other one
//     reviews, or Claude through Jordan's subscription
//  4. cut points from the audio (edges.go), loud cuts flagged
//  5. publish check on the kept frames (publish.go)
//  6. breath examples (breath.go) - markers only, nothing is cut
//  7. VEGAS: new project, keep list, BeckyCut.cs for the dead air, save as
//     <folder name>-<model>.veg (an existing project is never overwritten)
//  8. edit check (verify.go): the timeline vs the plan, re-transcribed
//  9. regions/markers for everything a person should look at, save, report
//
// The footage is only ever READ. Everything written goes into becky-edit\
// and the new .veg.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"becky-go/internal/config"
)

var videoExt = map[string]bool{".mp4": true, ".mov": true, ".mkv": true, ".m4v": true, ".webm": true, ".avi": true}

const defaultGuidance = "Keep the main topics Jordan talks about. Cut chat interaction, super chats, breaks and stream housekeeping."

type run struct {
	media, work, stem, tag, label, guidance string
	fps, duration, threshold                float64
	cfg                                     config.Config
	started                                 time.Time
	times                                   []string
}

func (r *run) logf(f string, a ...any) {
	fmt.Printf("[%s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(f, a...))
}

func (r *run) step(name string, since time.Time) {
	r.times = append(r.times, fmt.Sprintf("%s %s", name, time.Since(since).Round(time.Second)))
}

func fatal(msg string) {
	fmt.Println()
	fmt.Println("STOPPED: " + msg)
	os.Exit(1)
}

func main() {
	model := flag.String("model", "", "who makes the content calls: gemma, qwen or claude")
	guidance := flag.String("guidance", "", "what to keep, in plain words")
	noVegas := flag.Bool("no-vegas", false, "make the plan and the checks, but no VEGAS project")
	fresh := flag.Bool("fresh", false, "decide again even if this model already decided with the same guidance")
	flag.Parse()

	r := &run{cfg: config.Load(), started: time.Now()}
	switch strings.ToLower(*model) {
	case "gemma", "gemma4":
		r.tag, r.label = "gemma4", "Gemma-4 E4B (reviewed by Qwen3.5-4B)"
	case "qwen", "qwen3.5":
		r.tag, r.label = "qwen3.5", "Qwen3.5-4B (reviewed by Gemma-4 E4B)"
	case "claude":
		r.tag, r.label = "claude", "Claude ("+claudeModel()+", your subscription)"
	default:
		fatal("say which model makes the content calls: --model gemma, --model qwen or --model claude")
	}
	var err error
	if r.media, err = pickVideo(flag.Arg(0)); err != nil {
		fatal(err.Error())
	}
	r.stem = strings.TrimSuffix(filepath.Base(r.media), filepath.Ext(r.media))
	r.work = filepath.Join(filepath.Dir(r.media), "becky-edit")
	if err := os.MkdirAll(r.work, 0o755); err != nil {
		fatal(err.Error())
	}
	r.guidance = pickGuidance(*guidance, r.media)
	r.logf("video: %s", filepath.Base(r.media))
	r.logf("model: %s", r.label)
	r.logf("keep: %s", r.guidance)
	if err := r.probe(); err != nil {
		fatal(err.Error())
	}
	r.workflow(*fresh, *noVegas)
}

// pickVideo: the dragged file, else the one video in the current folder, else ask.
func pickVideo(arg string) (string, error) {
	if arg != "" {
		abs, err := filepath.Abs(arg)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(abs); err != nil {
			return "", fmt.Errorf("cannot find the video %s", arg)
		}
		return abs, nil
	}
	dir, _ := os.Getwd()
	ents, _ := os.ReadDir(dir)
	var vids []string
	for _, e := range ents {
		if !e.IsDir() && videoExt[strings.ToLower(filepath.Ext(e.Name()))] && !strings.Contains(strings.ToLower(e.Name()), "_edited") {
			vids = append(vids, filepath.Join(dir, e.Name()))
		}
	}
	switch len(vids) {
	case 0:
		return "", fmt.Errorf("no video here - drag the livestream video onto this .bat")
	case 1:
		return vids[0], nil
	}
	fmt.Println("Which video? Type its number and press Enter:")
	for i, v := range vids {
		fmt.Printf("  %d  %s\n", i+1, filepath.Base(v))
	}
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	n, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || n < 1 || n > len(vids) {
		return "", fmt.Errorf("no video picked")
	}
	return vids[n-1], nil
}

func pickGuidance(flagged, media string) string {
	if s := strings.TrimSpace(flagged); s != "" {
		return s
	}
	stem := strings.TrimSuffix(media, filepath.Ext(media))
	for _, f := range []string{stem + ".guidance.txt", filepath.Join(filepath.Dir(media), "guidance.txt")} {
		if b, err := os.ReadFile(f); err == nil && strings.TrimSpace(string(b)) != "" {
			return strings.Join(strings.Fields(string(b)), " ")
		}
	}
	fmt.Println("What should stay in the edit? Type one line and press Enter")
	fmt.Println("(just Enter = keep the main topics, cut chat and breaks):")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if s := strings.TrimSpace(line); s != "" {
		return s
	}
	return defaultGuidance
}

// probe reads the frame rate and length.
func (r *run) probe() error {
	out, err := exec.Command(r.cfg.FFprobe, "-v", "error", "-select_streams", "v:0", "-show_entries",
		"stream=r_frame_rate:format=duration", "-of", "json", r.media).Output()
	if err != nil {
		return fmt.Errorf("could not read the video (%v)", err)
	}
	var p struct {
		Streams []struct {
			Rate string `json:"r_frame_rate"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if json.Unmarshal(out, &p) != nil || len(p.Streams) == 0 {
		return fmt.Errorf("the video has no picture")
	}
	num, den, ok := strings.Cut(p.Streams[0].Rate, "/")
	n, _ := strconv.ParseFloat(num, 64)
	d := 1.0
	if ok {
		d, _ = strconv.ParseFloat(den, 64)
	}
	r.fps = n / d
	r.duration, _ = strconv.ParseFloat(p.Format.Duration, 64)
	if r.fps <= 0 || r.duration <= 0 {
		return fmt.Errorf("could not read the frame rate or length")
	}
	return nil
}

// cached runs make() unless path already exists.
func cached(path string, make func() error) (bool, error) {
	if st, err := os.Stat(path); err == nil && st.Size() > 0 {
		return true, nil
	}
	return false, make()
}

func (r *run) workflow(fresh, noVegas bool) {
	// 1. transcript
	t0 := time.Now()
	transcript := filepath.Join(r.work, r.stem+".transcript.json")
	hit, err := cached(transcript, func() error {
		bin, err := beckyBin("becky-transcribe")
		if err != nil {
			return err
		}
		r.logf("transcribing (two passes; a 15-minute stream takes about 3 minutes)...")
		out, err := exec.Command(bin, r.media, "--output", transcript, "--cleanup").CombinedOutput()
		if err != nil {
			_ = os.Remove(transcript)
			return fmt.Errorf("transcription failed: %v (%s)", err, lastLines(string(out), 3))
		}
		return nil
	})
	if err != nil {
		fatal(err.Error())
	}
	if hit {
		r.logf("transcript: using the one already made")
	}
	words, err := loadWords(transcript)
	if err != nil {
		fatal(err.Error())
	}
	r.step("transcript", t0)

	// 2. silence map
	t0 = time.Now()
	cutPath := filepath.Join(r.work, "becky-cut.json")
	if _, err := cached(cutPath, func() error {
		bin, err := beckyBin("becky-cut")
		if err != nil {
			return err
		}
		r.logf("measuring the silences (becky-cut)...")
		out, err := exec.Command(bin, r.media, "--dry-run").Output()
		if err != nil {
			return fmt.Errorf("becky-cut failed: %v", err)
		}
		return os.WriteFile(cutPath, out, 0o644)
	}); err != nil {
		fatal(err.Error())
	}
	cr, err := loadCutReport(cutPath)
	if err != nil {
		fatal(err.Error())
	}
	keeps := cr.keeps()
	r.threshold = cr.ThresholdDB
	wav := filepath.Join(r.work, "source16k.wav")
	if _, err := cached(wav, func() error {
		out, err := exec.Command(r.cfg.FFmpeg, "-v", "error", "-y", "-i", r.media, "-vn", "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", wav).CombinedOutput()
		if err != nil {
			_ = os.Remove(wav)
			return fmt.Errorf("could not read the audio: %v (%s)", err, lastLines(string(out), 2))
		}
		return nil
	}); err != nil {
		fatal(err.Error())
	}
	au, err := readWAV16(wav)
	if err != nil {
		fatal(err.Error())
	}
	r.step("silence map", t0)

	// 3. the content decision
	t0 = time.Now()
	ss := splitSentences(words)
	r.logf("%d words, %d sentences", len(words), len(ss))
	sel, err := r.decide(ss, fresh)
	if err != nil {
		fatal(err.Error())
	}
	r.step("content decision", t0)

	keep := make([]bool, len(words))
	for _, d := range sel.Decisions {
		if d.Keep && d.ID < len(ss) {
			for w := ss[d.ID].W0; w <= ss[d.ID].W1; w++ {
				keep[w] = true
			}
		}
	}

	// 4. cut points
	ranges := contentRanges(words, keep, keeps, au, r.fps, r.duration)
	if len(ranges) == 0 {
		fatal("the model kept nothing - check the guidance (" + r.guidance + ")")
	}
	predicted := finalPieces(ranges, keeps, r.fps)
	loud := loudEdges(ranges, cr.ThresholdDB)
	r.logf("%d kept sections, %.1f of %.1f minutes before the dead air comes out", len(ranges), sumRanges(ranges)/60, r.duration/60)

	// 5. publish check (before VEGAS: Gemma's vision model and VEGAS never share the GPU)
	t0 = time.Now()
	gm, gp, _ := r.cfg.GemmaAVLM()
	findings, pubNotes := publishCheck(r.media, r.cfg.FFmpeg, r.cfg.FFprobe, gm, gp, r.cfg.LlamaServer, predicted, r.work, r.logf)
	r.step("publish check", t0)

	// 6. breath examples
	breaths, breathTotal := breathMarks(breathSpots(words, predicted, au, cr.ThresholdDB))

	plan := map[string]any{"model": r.label, "guidance": r.guidance, "ranges": ranges, "predicted_pieces": predicted,
		"loud_edges": loud, "findings": findings, "fps": r.fps}
	writeJSON(filepath.Join(r.work, "plan-"+r.tag+".json"), plan)
	if noVegas {
		r.report(sel, ss, ranges, predicted, loud, findings, pubNotes, breaths, breathTotal, nil, nil, "")
		return
	}

	// 7. VEGAS
	t0 = time.Now()
	veg := freeVegName(filepath.Join(filepath.Dir(r.media), filepath.Base(filepath.Dir(r.media))+"-"+r.tag+".veg"))
	ps, health, err := buildInVegas(r.media, r.work, veg, ranges, r.fps, r.logf)
	if err != nil {
		fatal(err.Error())
	}
	r.step("VEGAS build", t0)

	// 8. edit check
	t0 = time.Now()
	ver := verifyEdit(ps, predicted, r.fps, au, words, keep, r.work, r.tag, health, r.logf)
	r.step("edit check", t0)

	// 9. regions and markers, save, report
	marks := r.marks(sel, ss, ranges, findings, breaths, &ver, ps)
	if err := addMarks(marks, r.logf); err != nil {
		fatal("the edit is saved, but the regions could not be added: " + err.Error())
	}
	r.report(sel, ss, ranges, predicted, loud, findings, pubNotes, breaths, breathTotal, &ver, marks, veg)
}

// decide runs (or reuses) this model's content decision.
func (r *run) decide(ss []Sentence, fresh bool) (Selection, error) {
	path := filepath.Join(r.work, "selection-"+r.tag+".json")
	var old Selection
	if b, err := os.ReadFile(path); err == nil && !fresh && json.Unmarshal(b, &old) == nil &&
		old.Guidance == r.guidance && len(old.Decisions) == len(ss) {
		r.logf("content decision: using %s's earlier decision (same guidance; --fresh decides again)", r.tag)
		return old, nil
	}
	gm, _, _ := r.cfg.GemmaAVLM()
	qm, _, _ := r.cfg.Qwen()
	gemma := localModelSpec{name: "gemma4", model: gm, server: r.cfg.LlamaServer}
	qwen := localModelSpec{name: "qwen3.5", model: qm, server: r.cfg.LlamaServer}
	var sel Selection
	var err error
	switch r.tag {
	case "gemma4":
		sel, err = runLocal(gemma, qwen, ss, r.guidance, r.logf)
	case "qwen3.5":
		sel, err = runLocal(qwen, gemma, ss, r.guidance, r.logf)
	default:
		sel, err = runClaude(ss, r.guidance, r.work, r.logf)
	}
	if err != nil {
		return sel, err
	}
	writeJSON(path, sel)
	return sel, nil
}

func sumRanges(rs []Range) float64 {
	t := 0.0
	for _, x := range rs {
		t += x.Out - x.In
	}
	return t
}

func writeJSON(path string, v any) {
	b, _ := json.MarshalIndent(v, "", " ")
	_ = os.WriteFile(path, b, 0o644)
}

func short(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-3]) + "..."
}

// marks builds every region and marker on the finished timeline.
func (r *run) marks(sel Selection, ss []Sentence, ranges []Range, findings []Finding, breaths []breath, ver *Verification, ps []piece) []mark {
	var ms []mark
	// unsure content calls (runs of consecutive unsure sentences that were kept)
	for i := 0; i < len(sel.Decisions); i++ {
		d := sel.Decisions[i]
		if !d.Unsure || !d.Keep {
			continue
		}
		j := i
		for j+1 < len(sel.Decisions) && sel.Decisions[j+1].Unsure && sel.Decisions[j+1].Keep {
			j++
		}
		if t0, t1, ok := toTimeline(ps, ss[d.ID].Start, ss[sel.Decisions[j].ID].End); ok {
			ms = append(ms, mark{At: t0, Len: math.Max(t1-t0, 0.5),
				Label: short(fmt.Sprintf("Unsure - %s: \"%s\"", d.Note, ss[d.ID].Text), 230)})
		}
		i = j
	}
	for _, f := range findings {
		if !f.Region {
			continue
		}
		if t0, t1, ok := toTimeline(ps, f.Src0, f.Src1); ok {
			ms = append(ms, mark{At: t0, Len: math.Max(t1-t0, 1), Label: short("Publish check - "+f.Kind+": "+f.Evidence, 230)})
		}
	}
	for _, s := range ver.Missing {
		if s.Words >= 3 {
			ms = append(ms, mark{At: math.Max(0, s.Timeline-0.5), Len: 2, Label: short("Check - planned words not heard here: \""+s.Text+"\"", 230)})
		}
	}
	ms = append(ms, loudEdgeMarks(ranges, ps, r.threshold)...)
	for n, b := range breaths {
		if t0, _, ok := toTimeline(ps, b.A, b.B); ok {
			ms = append(ms, mark{At: t0, Label: breathLabel(n+1, len(breaths), b)})
		}
	}
	return ms
}

// loudEdgeMarks: a marker on every cut that sits inside speech and is still loud.
func loudEdgeMarks(rs []Range, ps []piece, threshold float64) []mark {
	var ms []mark
	for _, rg := range rs {
		if rg.InHow != "becky-cut" && rg.InHow != "file-start" && rg.InDB > threshold {
			if t, _, ok := toTimeline(ps, rg.In, rg.In+0.04); ok {
				ms = append(ms, mark{At: t, Label: fmt.Sprintf("Cut inside speech, still loud (%.0f dB) - check the start of \"%s\"", rg.InDB, short(rg.First, 60))})
			}
		}
		if rg.OutHow != "becky-cut" && rg.OutHow != "file-end" && rg.OutDB > threshold {
			if _, t, ok := toTimeline(ps, rg.Out-0.04, rg.Out); ok {
				ms = append(ms, mark{At: t, Label: fmt.Sprintf("Cut inside speech, still loud (%.0f dB) - check the end of \"%s\"", rg.OutDB, short(rg.Last, 60))})
			}
		}
	}
	return ms
}
