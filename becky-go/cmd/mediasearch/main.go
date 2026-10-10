// becky-mediasearch — find the moment in videos, sound files or pictures that matches a plain
// description ("the dog barks", "a man in a red jacket waves") or looks like a given photo.
//
//	becky-mediasearch "<what to find>" [file-or-folder...] [--top 5] [--every-frame] [--no-check]
//	becky-mediasearch --like <photo-or-sound.wav> [file-or-folder...]
//	becky-mediasearch --add <file-or-folder...>        (put footage in the video database, no search)
//
// With no file or folder it searches everything already in the video database.
//
// Model: Google's EmbeddingGemma 2 (Q8_0 GGUF + its vision/audio projector, llama-server): text,
// pictures, sound and short clips land in ONE meaning space, so a sentence can be compared with a
// frame or a sound directly. This is the job the other two embedders cannot do: becky-search's
// Qwen3-Embedding and qmd's EmbeddingGemma v1 read text only.
//
// What it looks at, per video: one frame every second ("picture": what is in the shot), four
// frames a second apart as one input every 2 s ("action": what is happening), and 5-second
// stretches of sound every 2.5 s ("sound"). --every-frame reads every frame instead. Either way the
// best picture/action hits are then re-read frame by frame around the match, so "frame" in the
// result is the exact best frame.
//
// The video database (config video_db, beckydb schema: the same database family that holds
// transcripts) keeps everything it has read. Never-seen files are read on their first search
// (about 26 frames a second on the RTX 3070) and added; later searches are instant. A file is
// re-read when it changes or a different --step is asked for. --db <file> uses another database
// (e.g. a case's forensic.db).
//
// Gemma 4 double-check (Google's recommended pairing: EmbeddingGemma retrieves, Gemma reads): the
// top hits go to becky's Gemma-4 E4B (config gemma_model, the QAT build; BECKY_AVLM_VARIANT=12b for
// the 12B), which sees the frames AND hears the sound of each moment and answers yes/no.
// Confirmed hits come first; "no" hits stay at the bottom, marked. --no-check skips it.
//
// --transcript <becky-transcribe JSON> (with ONE file; becky-pipeline passes it) adds each spoken line
// as a "said" row, so the same search covers what is SAID as well as seen and heard. With
// --identify (becky-identify JSON) a line is labelled with the speaker's NAME when becky knows the
// voice ("Hair Jordan: ..."), so "find where Hair Jordan talks about his bald friend" works.
//
// --reel <file.json> also saves the video hits Gemma did not reject as a becky clip list (half a
// second of handle either side), in order: `becky-reel` renders it as one preview video and
// `becky-otio` turns it into a timeline for an editor.
//
// JSON out, exit 0. Degrade, never crash: a missing model or a file that will not decode is a
// "note" in the output, and the other files are still searched.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"becky-go/internal/avlm"
	"becky-go/internal/beckydb"
	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/edl"
	"becky-go/internal/llmlocal"
	"becky-go/internal/mediainfo"
	"becky-go/internal/people"
)

const modelID = "EmbeddingGemma 2 (Q8_0)"

// candidates is how many scored windows come back from the database before merging into moments.
const candidates = 400

var (
	videoExt = map[string]bool{".mp4": true, ".mov": true, ".mkv": true, ".avi": true, ".webm": true, ".m4v": true, ".wmv": true, ".flv": true, ".ts": true, ".mts": true}
	audioExt = map[string]bool{".wav": true, ".mp3": true, ".m4a": true, ".flac": true, ".ogg": true, ".opus": true, ".aac": true, ".wma": true}
	imageExt = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".bmp": true}
)

// Source is one file this run looked at.
type Source struct {
	File    string `json:"file"`
	Kind    string `json:"kind"`    // video | audio | image
	Indexed string `json:"indexed"` // added | in database | failed
	Items   int    `json:"items"`
	Said    int    `json:"said,omitempty"` // transcript lines added (--transcript)
	Note    string `json:"note,omitempty"`
}

// Output is the becky-mediasearch JSON contract.
type Output struct {
	Query    string   `json:"query,omitempty"`
	Like     string   `json:"like,omitempty"`
	Model    string   `json:"model"`
	Database string   `json:"database"`
	Checker  string   `json:"checker,omitempty"`
	Sources  []Source `json:"sources"`
	Hits     []Hit    `json:"hits"`
	Reel     string   `json:"reel,omitempty"` // the clip list written by --reel
	Note     string   `json:"note,omitempty"`
}

type options struct {
	query, like, db, reel, transcript, identify string
	paths                                       []string
	step                                        float64
	top                                         int
	check, addOnly                              bool
}

func main() {
	like := flag.String("like", "", "find things that look/sound like this picture or .wav file instead of a description")
	n := flag.Int("top", 5, "how many moments to return")
	step := flag.Float64("step", 1, "seconds between frames read from each video")
	every := flag.Bool("every-frame", false, "read every single frame (slow the first time, then kept)")
	noCheck := flag.Bool("no-check", false, "skip the Gemma 4 double-check")
	add := flag.Bool("add", false, "only add the files to the video database, no search")
	db := flag.String("db", "", "video database file (default: config video_db)")
	reel := flag.String("reel", "", "also save the video hits as a becky clip list (for becky-reel / becky-otio)")
	out := flag.String("output", "", "output file (default: stdout)")
	verbose := flag.Bool("verbose", false, "show progress on stderr")
	transcript := flag.String("transcript", "", "becky-transcribe JSON of the ONE file given: its lines become searchable too")
	identify := flag.String("identify", "", "becky-identify JSON of that file: spoken lines get the speaker's name")
	args := beckyio.Positional(flag.CommandLine, os.Args[1:])
	o := options{like: *like, db: *db, reel: *reel, transcript: *transcript, identify: *identify, step: *step, top: *n, check: !*noCheck, addOnly: *add}
	if !o.addOnly && o.like == "" && len(args) > 0 {
		o.query, args = args[0], args[1:]
	}
	o.paths = args
	if (!o.addOnly && o.query == "" && o.like == "") || (o.addOnly && len(o.paths) == 0) {
		beckyio.Fatalf(`usage: becky-mediasearch "<what to find>" [file-or-folder...] [--top 5] [--every-frame] [--no-check]` + "\n" +
			`       becky-mediasearch --like <photo-or-sound.wav> [file-or-folder...]` + "\n" +
			`       becky-mediasearch --add <file-or-folder...>`)
	}
	if *every {
		o.step = 0
	}
	logf := func(f string, a ...any) { beckyio.Logf(*verbose, f, a...) }
	res := run(config.Load(), o, logf)

	data, _ := json.MarshalIndent(res, "", "  ")
	if *out == "" {
		fmt.Println(string(data))
		return
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		beckyio.Fatalf("write output: %v", err)
	}
}

func run(cfg config.Config, o options, logf func(string, ...any)) Output {
	if o.db == "" {
		o.db = cfg.VideoDB
	}
	res := Output{Query: o.query, Like: o.like, Model: modelID, Database: o.db, Sources: []Source{}, Hits: []Hit{}}
	_ = os.MkdirAll(filepath.Dir(o.db), 0o755)
	db, err := beckydb.Open(cfg, o.db)
	if err == nil {
		err = db.EnsureMediaSchema()
	}
	if err != nil {
		res.Note = "the video database could not be opened: " + err.Error()
		return res
	}
	files := expand(o.paths)
	if len(o.paths) > 0 && len(files) == 0 {
		res.Note = "no video, sound or picture files found in " + strings.Join(o.paths, ", ")
		return res
	}
	emb := llmlocal.NewMediaEmbedClient(cfg.MediaEmbedModel, cfg.MediaEmbedMMProj, cfg.LlamaServer, logf)
	defer emb.Close()
	if err := emb.Available(); err != nil {
		res.Note = "EmbeddingGemma 2 is not set up (" + err.Error() + "); run scripts\\get-embeddinggemma2.ps1"
		return res
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Hour)
	defer cancel()

	infos := map[string]mediainfo.Info{}
	for _, f := range files {
		src := ensureIndexed(ctx, cfg, db, emb, f, o.step, infos, logf)
		res.Sources = append(res.Sources, src)
	}
	if o.transcript != "" && len(files) == 1 {
		known, _ := people.Load(o.identify)
		if n, err := addSaid(ctx, db, emb, files[0], o.transcript, known); err != nil {
			res.Note = "the transcript was not added: " + err.Error()
		} else {
			res.Sources[0].Said = n
		}
	}
	if o.addOnly {
		return res
	}
	if len(files) == 0 {
		all, _ := db.ListMediaFiles()
		if len(all) == 0 {
			res.Note = "the video database is empty: name a file or folder to search (it is added on the way)"
			return res
		}
		logf("mediasearch: searching all %d files in the video database", len(all))
	}

	q, err := queryVector(ctx, emb, o.query, o.like)
	if err != nil {
		res.Note = "could not read the search: " + err.Error()
		return res
	}
	rows, err := db.SearchMedia(q, files, candidates)
	if err != nil {
		res.Note = "video database search failed: " + err.Error()
		return res
	}
	best := top(rank(rows), o.top)
	for _, h := range best {
		if _, ok := infos[h.File]; !ok {
			if info, err := mediainfo.Probe(cfg.FFprobe, h.File); err == nil {
				infos[h.File] = info
			}
		}
	}
	if o.step > 0 {
		refine(ctx, cfg.FFmpeg, emb, best, q, infos, logf)
	}
	emb.Close() // free the graphics card for Gemma

	switch {
	case o.check && o.query != "" && len(best) > 0:
		res.Checker = confirm(ctx, cfg, best, o.query, infos, logf)
	case o.check && o.like != "":
		res.Checker = "not run: a picture/sound search has no words for Gemma to check"
	}
	sort.SliceStable(best, func(i, j int) bool { return order(best[i]) < order(best[j]) })
	res.Hits = best
	if o.reel != "" {
		r := reelOf(best, o.query+o.like, infos)
		if len(r.Clips) == 0 {
			res.Note = "no video moment to put in the clip list"
		} else if err := edl.Save(o.reel, r); err != nil {
			res.Note = "clip list not saved: " + err.Error()
		} else {
			res.Reel = o.reel
		}
	}
	return res
}

// reelOf turns the video hits Gemma did not reject into a clip list, half a second of handle
// either side, clamped to the video.
func reelOf(hits []Hit, name string, infos map[string]mediainfo.Info) edl.Reel {
	r := edl.Reel{Version: "1", Name: name, Overlay: edl.Overlay{Position: "bottom"}}
	for _, h := range hits {
		if h.Gemma == "no" || !videoExt[strings.ToLower(filepath.Ext(h.File))] {
			continue
		}
		in, out := max(0, h.Start-0.5), h.End+0.5
		if d := infos[h.File].Duration; d > 0 {
			out = min(out, d)
		}
		label := h.What
		if label == "" {
			label = h.Kind + ": " + name
		}
		r.Clips = append(r.Clips, edl.Clip{ID: fmt.Sprintf("c%d", len(r.Clips)+1), Source: h.File, In: in, Out: out, Label: label,
			Meta: edl.ClipMeta{SourceFPS: infos[h.File].FPS}})
	}
	return r
}

func order(h Hit) int {
	switch h.Gemma {
	case "yes":
		return 0
	case "no":
		return 2
	}
	return 1
}

// ensureIndexed makes sure a file's vectors are in the database and current.
func ensureIndexed(ctx context.Context, cfg config.Config, db *beckydb.DB, e *llmlocal.Client, f string, step float64, infos map[string]mediainfo.Info, logf func(string, ...any)) Source {
	ext := strings.ToLower(filepath.Ext(f))
	src := Source{File: f, Kind: "video"}
	switch {
	case audioExt[ext]:
		src.Kind = "audio"
	case imageExt[ext]:
		src.Kind = "image"
	}
	st, err := os.Stat(f)
	if err != nil {
		src.Indexed, src.Note = "failed", err.Error()
		return src
	}
	want := beckydb.MediaFile{SourceFile: f, Size: st.Size(), Mtime: st.ModTime().Unix(), Step: step, Model: modelID}
	if src.Kind == "image" {
		want.Step = 0
	}
	if have, ok, _ := db.GetMediaFile(f); ok && have.Size == want.Size && have.Mtime == want.Mtime && have.Step == want.Step && have.Model == want.Model {
		src.Indexed, src.Items = "in database", have.Items
		return src
	}
	var items []item
	if src.Kind == "image" {
		items, err = indexImage(ctx, e, f)
	} else {
		info, perr := mediainfo.Probe(cfg.FFprobe, f)
		if perr != nil {
			info = mediainfo.Info{HasVideo: src.Kind == "video", HasAudio: true}
		}
		infos[f] = info
		logf("mediasearch: reading %s for the first time", filepath.Base(f))
		items, err = indexVideo(ctx, e, cfg.FFmpeg, f, info.FPS, step, info.HasVideo && src.Kind == "video", info.HasAudio, logf)
	}
	if err != nil {
		src.Indexed, src.Note = "failed", "could not read this file: "+err.Error()
		return src
	}
	stored := make([]beckydb.MediaItem, len(items))
	for i, it := range items {
		stored[i] = beckydb.MediaItem{Kind: it.Kind, Start: it.Start, End: it.End, Vec: it.Vec}
	}
	if err := db.ReplaceMedia(want, stored); err != nil {
		src.Indexed, src.Note = "failed", "could not save to the video database: "+err.Error()
		return src
	}
	src.Indexed, src.Items = "added", len(items)
	return src
}

// queryVector embeds the description (with EmbeddingGemma's search prefix) or the --like file.
func queryVector(ctx context.Context, e *llmlocal.Client, query, like string) ([]float64, error) {
	var in any
	var err error
	switch ext := strings.ToLower(filepath.Ext(like)); {
	case like == "":
		in = "task: search result | query: " + query
	case imageExt[ext]:
		in, err = llmlocal.ImageInput(like)
	case ext == ".wav":
		in, err = llmlocal.AudioInput(like)
	default:
		return nil, fmt.Errorf("--like takes a picture (.jpg/.png/...) or a .wav sound file, not %s", filepath.Base(like))
	}
	if err != nil {
		return nil, err
	}
	v, err := e.EmbedMedia(ctx, []any{in})
	if err != nil {
		return nil, err
	}
	return v[0], nil
}

// expand turns files and folders (searched recursively) into absolute media file paths.
func expand(paths []string) []string {
	var out []string
	add := func(p string) {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		out = append(out, p)
	}
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		if !st.IsDir() {
			add(p)
			continue
		}
		_ = filepath.WalkDir(p, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			if videoExt[ext] || audioExt[ext] || imageExt[ext] {
				add(path)
			}
			return nil
		})
	}
	return out
}

// confirm asks Gemma 4 about each hit: for a video moment it sees the frames and hears the sound;
// for a picture it sees the picture. Returns the checker's name (or why it could not run).
func confirm(ctx context.Context, cfg config.Config, hits []Hit, query string, infos map[string]mediainfo.Info, logf func(string, ...any)) string {
	model, mmproj, label := cfg.GemmaAVLM()
	r := avlm.New(model, mmproj, cfg.LlamaServer, "", cfg.FFmpeg, cfg.FFprobe, logf)
	if err := r.Ready(); err != nil {
		return "not run: Gemma 4 is not set up (" + err.Error() + ")"
	}
	stop, err := r.Start(ctx)
	if err != nil {
		return "not run: Gemma 4 did not start (" + err.Error() + ")"
	}
	defer stop()
	prompt := fmt.Sprintf(`Does this show or contain: "%s"? Answer "yes" or "no" first, then one short sentence on what you actually see or hear.`, query)
	for i := range hits {
		h := &hits[i]
		var out avlm.Result
		var err error
		if imageExt[strings.ToLower(filepath.Ext(h.File))] {
			out, err = r.AnalyzeImage(ctx, h.File, avlm.ImageOptions{Prompt: prompt, MaxTokens: 80})
		} else {
			start, end := window(*h, infos[h.File].Duration)
			out, err = r.Analyze(ctx, avlm.Options{Clip: h.File, Prompt: prompt, WindowStart: start, WindowSec: end - start, FPS: 2, MaxTokens: 80})
		}
		if err != nil {
			h.GemmaWhy = "could not check: " + err.Error()
			continue
		}
		h.Gemma, h.GemmaWhy = verdict(out.Text)
		h.Confirmed = h.Gemma == "yes"
		logf("mediasearch: %s at %.1fs: %s", filepath.Base(h.File), h.Start, out.Text)
	}
	return "Gemma 4 " + label
}

// window is the stretch Gemma is shown for a hit: the moment plus a second either side.
// At most checkSec long, centred on the best frame: a 27 s shot at 2 fps overflowed Gemma's context.
func window(h Hit, dur float64) (float64, float64) {
	start, end := max(0, h.Start-1), h.End+1
	if end-start > checkSec {
		mid := h.Frame
		if mid <= 0 || h.Kind == "sound" || h.Kind == "said" {
			mid = (h.Start + h.End) / 2
		}
		start = max(0, mid-checkSec/2)
		end = start + checkSec
	}
	if dur > 0 && end > dur {
		end = dur
	}
	return start, end
}

// checkSec is the longest stretch Gemma is shown per hit (12 frames at 2 fps + its sound).
const checkSec = 6.0

// verdict reads Gemma's "yes, ..." / "no - ..." answer.
func verdict(text string) (string, string) {
	t := strings.TrimSpace(text)
	low := strings.ToLower(strings.TrimLeft(t, `*"' `))
	switch {
	case strings.HasPrefix(low, "yes"):
		return "yes", t
	case strings.HasPrefix(low, "no"):
		return "no", t
	}
	return "", t
}

// addSaid stores each transcript line of a file as a "said" row (EmbeddingGemma 2 text vector,
// the line in label), so one search covers what is said as well as seen and heard.
func addSaid(ctx context.Context, db *beckydb.DB, e *llmlocal.Client, file, transcript string, known []people.Span) (int, error) {
	data, err := os.ReadFile(transcript)
	if err != nil {
		return 0, err
	}
	var t struct {
		Segments []struct {
			Start   float64 `json:"start"`
			End     float64 `json:"end"`
			Text    string  `json:"text"`
			Speaker string  `json:"speaker"`
		} `json:"segments"`
	}
	if err := json.Unmarshal(data, &t); err != nil {
		return 0, fmt.Errorf("read %s: %w", filepath.Base(transcript), err)
	}
	var items []beckydb.MediaItem
	var inputs []any
	for _, s := range t.Segments {
		text := strings.TrimSpace(s.Text)
		if text == "" {
			continue
		}
		label := text
		if name := people.Speaking(known, s.Start, s.End); name != "" {
			label = name + ": " + text // becky-identify knows this voice
		} else if s.Speaker != "" {
			label = s.Speaker + ": " + text
		}
		items = append(items, beckydb.MediaItem{Kind: "said", Start: s.Start, End: s.End, Label: label})
		inputs = append(inputs, "title: none | text: "+label)
	}
	if len(items) == 0 {
		return 0, nil
	}
	vecs, err := embedAll(ctx, e, len(inputs), func(i int) (any, error) { return inputs[i], nil }, func(string, ...any) {})
	if err != nil {
		return 0, err
	}
	for i := range items {
		items[i].Vec = vecs[i]
	}
	return len(items), db.ReplaceKind(file, "said", items)
}
