// becky-shots — a written shot log for a video: where each shot starts and ends, and a short
// plain description of it (who/what is in it, where, shot type, camera, light), the way an editor
// logs footage. buttercut calls this the "visual transcript"; Director calls it the scene index.
//
//	becky-shots <video> [--output shots.json] [--db <file>] [--identify identify.json] [--people people.txt]
//
// Shots: ffmpeg's scene-change score cuts the video wherever the picture changes (cut threshold
// 0.3); pieces under 1 s join the shot before them, and shots longer than --max-shot seconds are
// split evenly so a long take still gets described as it changes.
//
// Descriptions: becky's Gemma-4 E4B (config gemma_model, the QAT build) watches a few frames of
// each shot AND hears its sound, and writes 1-3 sentences. Only what is seen or heard.
//
// Names (Jordan, 2026-10-10: "we absolutely need to NAME people if we know their names. If we don't,
// that's fine"): a person is named when becky KNOWS who it is - becky-identify placed them in the
// shot by voice/face (--identify; the shot's `people` lists them), or Jordan's own notes describe
// them (--people, default people.txt beside the video: plain lines like "the man with bright green
// hair is Hair Jordan"). Anyone else stays "a man", "a woman". Never a guessed name.
//
// The shot log is kept twice: the readable JSON (--output, e.g. next to the pipeline's other
// files) and the video database (config video_db, or --db): each description is stored with its
// EmbeddingGemma 2 text vector, so becky-mediasearch finds shots by what was written about them
// as well as by the pictures themselves.
//
// JSON out, exit 0. Degrade, never crash: no Gemma 4 = a note and no descriptions; no
// EmbeddingGemma 2 = descriptions written to the JSON but not to the database (noted).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"becky-go/internal/avlm"
	"becky-go/internal/beckydb"
	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/llmlocal"
	"becky-go/internal/mediainfo"
	"becky-go/internal/people"
	"becky-go/internal/proc"
)

const (
	cutThreshold = 0.3 // ffmpeg scene score that counts as a cut
	minShot      = 1.0 // seconds; shorter pieces join the shot before
	docPrefix    = "title: none | text: "
)

const describePrompt = `Describe this shot for a video editor's footage log in 1 to 3 short sentences: who or what is in it, the setting, the shot type (wide, medium, close-up), any camera movement, and the lighting. Mention speech or notable sounds only if they matter. Only describe what you can see or hear. Write ONE description of the whole shot, not one per frame, and no timestamps.`

const systemPrompt = `You log video footage for an editor. Describe only what is visible or audible. When the message tells you who someone is (becky identified them, or the editor's notes describe them and they fit), call them by that name instead of "a man" or "a woman". Never invent a name: anyone not covered is "a man", "a woman", "a child". No preamble.`

// Shot is one logged shot.
type Shot struct {
	Index       int      `json:"index"`
	Start       float64  `json:"start"`
	End         float64  `json:"end"`
	People      []string `json:"people,omitempty"` // named by becky-identify (voice/face) in this shot
	Description string   `json:"description"`
}

// Output is the becky-shots JSON contract.
type Output struct {
	Video     string  `json:"video"`
	Duration  float64 `json:"duration"`
	Describer string  `json:"describer,omitempty"`
	Database  string  `json:"database,omitempty"`
	Shots     []Shot  `json:"shots"`
	Note      string  `json:"note,omitempty"`
}

func main() {
	out := flag.String("output", "", "output file (default: stdout)")
	db := flag.String("db", "", "video database file (default: config video_db)")
	maxShot := flag.Float64("max-shot", 20, "split shots longer than this many seconds")
	identify := flag.String("identify", "", "becky-identify JSON: name the people it identified")
	notes := flag.String("people", "", "plain notes on who is who (default: people.txt beside the video)")
	verbose := flag.Bool("verbose", false, "show progress on stderr")
	args := beckyio.Positional(flag.CommandLine, os.Args[1:])
	if len(args) != 1 {
		beckyio.Fatalf("usage: becky-shots <video> [--output shots.json] [--db <file>] [--identify identify.json] [--people people.txt]")
	}
	logf := func(f string, a ...any) { beckyio.Logf(*verbose, f, a...) }
	video, _ := filepath.Abs(args[0])
	if *notes == "" {
		*notes = filepath.Join(filepath.Dir(video), "people.txt")
	}
	known, err := people.Load(*identify)
	if err != nil {
		logf("shots: names from %s not used: %v", *identify, err)
	}
	peopleNotes, _ := os.ReadFile(*notes) // missing = no notes
	res := run(config.Load(), video, *db, *maxShot, known, strings.TrimSpace(string(peopleNotes)), logf)

	data, _ := json.MarshalIndent(res, "", "  ")
	if *out == "" {
		fmt.Println(string(data))
		return
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		beckyio.Fatalf("write output: %v", err)
	}
}

func run(cfg config.Config, video, dbPath string, maxShot float64, known []people.Span, notes string, logf func(string, ...any)) Output {
	res := Output{Video: video, Shots: []Shot{}}
	info, err := mediainfo.Probe(cfg.FFprobe, video)
	if err != nil || !info.HasVideo || info.Duration <= 0 {
		res.Note = "not a readable video"
		if err != nil {
			res.Note += ": " + err.Error()
		}
		return res
	}
	res.Duration = info.Duration
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Hour)
	defer cancel()

	cuts, err := sceneCuts(ctx, cfg.FFmpeg, video)
	if err != nil {
		res.Note = "could not find shot changes (" + err.Error() + "); the video is logged as one long take"
	}
	spans := shotSpans(cuts, info.Duration, maxShot)
	logf("shots: %d shots in %s", len(spans), filepath.Base(video))
	for i, s := range spans {
		res.Shots = append(res.Shots, Shot{Index: i + 1, Start: round(s[0]), End: round(s[1]), People: people.In(known, s[0], s[1])})
	}

	model, mmproj, label := cfg.GemmaAVLM()
	r := avlm.New(model, mmproj, cfg.LlamaServer, "", cfg.FFmpeg, cfg.FFprobe, logf)
	if err := r.Ready(); err != nil {
		res.Note = join(res.Note, "shots found but not described: Gemma 4 is not set up ("+err.Error()+")")
		return res
	}
	stop, err := r.Start(ctx)
	if err != nil {
		res.Note = join(res.Note, "shots found but not described: Gemma 4 did not start ("+err.Error()+")")
		return res
	}
	res.Describer = "Gemma 4 " + label
	for i := range res.Shots {
		s := &res.Shots[i]
		length := s.End - s.Start
		// About four frames whatever the shot length (2 fps cap for very short shots).
		fps := math.Min(2, math.Max(0.2, 4/length))
		got, err := r.Analyze(ctx, avlm.Options{Clip: video, Prompt: shotPrompt(s.People, notes), SystemPrompt: systemPrompt,
			WindowStart: s.Start, WindowSec: length, FPS: fps, MaxTokens: 120})
		if err != nil {
			logf("shots: shot %d not described: %v", s.Index, err)
			continue
		}
		s.Description = strings.TrimSpace(got.Text)
		logf("shots: %d/%d %.1f-%.1fs: %s", s.Index, len(res.Shots), s.Start, s.End, s.Description)
	}
	stop() // free the graphics card for the embedder

	if dbPath == "" {
		dbPath = cfg.VideoDB
	}
	if err := store(ctx, cfg, dbPath, video, res.Shots, logf); err != nil {
		res.Note = join(res.Note, "descriptions are in this file but not in the video database: "+err.Error())
	} else {
		res.Database = dbPath
	}
	return res
}

// store embeds each description (EmbeddingGemma 2, document prefix) and saves it as a "shot" row.
func store(ctx context.Context, cfg config.Config, dbPath, video string, shots []Shot, logf func(string, ...any)) error {
	var described []Shot
	for _, s := range shots {
		if s.Description != "" {
			described = append(described, s)
		}
	}
	if len(described) == 0 {
		return fmt.Errorf("no shot was described")
	}
	emb := llmlocal.NewMediaEmbedClient(cfg.MediaEmbedModel, cfg.MediaEmbedMMProj, cfg.LlamaServer, logf)
	defer emb.Close()
	if err := emb.Available(); err != nil {
		return fmt.Errorf("EmbeddingGemma 2 is not set up (%v)", err)
	}
	inputs := make([]any, len(described))
	for i, s := range described {
		inputs[i] = docPrefix + s.Description
	}
	vecs, err := emb.EmbedMedia(ctx, inputs)
	if err != nil {
		return err
	}
	items := make([]beckydb.MediaItem, len(described))
	for i, s := range described {
		v := make([]float32, len(vecs[i]))
		for k, x := range vecs[i] {
			v[k] = float32(x)
		}
		items[i] = beckydb.MediaItem{Kind: "shot", Start: s.Start, End: s.End, Label: s.Description, Vec: v}
	}
	_ = os.MkdirAll(filepath.Dir(dbPath), 0o755)
	db, err := beckydb.Open(cfg, dbPath)
	if err == nil {
		err = db.EnsureMediaSchema()
	}
	if err == nil {
		err = db.ReplaceKind(video, "shot", items)
	}
	return err
}

var ptsRe = regexp.MustCompile(`pts_time:([0-9.]+)`)

// sceneCuts returns the times (seconds) where ffmpeg's scene score passes cutThreshold. Frames
// are scored at 320 px wide: cuts do not need detail, and it keeps a long video fast.
func sceneCuts(ctx context.Context, ffmpeg, video string) ([]float64, error) {
	cmd := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-nostats", "-i", video, "-an",
		"-vf", fmt.Sprintf("scale=320:-2,select='gt(scene,%g)',showinfo", cutThreshold), "-f", "null", "-")
	proc.NoWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg: %v", err)
	}
	var cuts []float64
	for _, m := range ptsRe.FindAllStringSubmatch(string(out), -1) {
		if t, err := strconv.ParseFloat(m[1], 64); err == nil {
			cuts = append(cuts, t)
		}
	}
	sort.Float64s(cuts)
	return cuts, nil
}

// shotSpans turns cut times into [start,end] shots covering 0..dur: pieces shorter than minShot
// join the shot before them, and shots longer than maxShot are split into equal parts.
func shotSpans(cuts []float64, dur, maxShot float64) [][2]float64 {
	bounds := []float64{0}
	for _, c := range cuts {
		if c-bounds[len(bounds)-1] >= minShot && dur-c >= minShot {
			bounds = append(bounds, c)
		}
	}
	bounds = append(bounds, dur)
	var spans [][2]float64
	for i := 0; i+1 < len(bounds); i++ {
		a, b := bounds[i], bounds[i+1]
		n := 1
		if maxShot > 0 {
			n = int(math.Ceil((b - a) / maxShot))
		}
		for k := 0; k < n; k++ {
			spans = append(spans, [2]float64{a + (b-a)*float64(k)/float64(n), a + (b-a)*float64(k+1)/float64(n)})
		}
	}
	return spans
}

func round(x float64) float64 { return math.Round(x*1000) / 1000 }

func join(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}

// shotPrompt adds what becky knows about who is in the shot to the describe prompt.
func shotPrompt(named []string, notes string) string {
	p := describePrompt
	if len(named) > 0 {
		p += "\n\nbecky identified these people in this shot (by voice or face): " + strings.Join(named, ", ") + ". Call them by name (for example \"" + named[0] + " talks to the camera\"), not \"a man\" or \"a woman\"."
	}
	if notes != "" {
		p += "\n\nWho is who, from the editor's notes:\n" + notes + "\nIf a person in this shot fits one of these notes, use that name instead of \"a man\" or \"a woman\"."
	}
	return p
}
