package main

// media.go - what becky reads from the footage: the words (on the timeline),
// the faces, the loudness, and for footage with no speech, a one-line picture
// description every few seconds in place of the words.

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"becky-go/internal/avlm"
	"becky-go/internal/config"
	"becky-go/internal/faceembed"
	"becky-go/internal/vegasctl"
)

type word struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// transcriptFor finds becky-transcribe's words for a media file: the footage
// folder's becky-edit\<stem>.transcript.json, or <stem>.transcript.json beside it.
func transcriptFor(media string) []word {
	stem := strings.TrimSuffix(filepath.Base(media), filepath.Ext(media))
	dir := filepath.Dir(media)
	for _, p := range []string{filepath.Join(dir, "becky-edit", stem+".transcript.json"), filepath.Join(dir, stem+".transcript.json")} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var t struct {
			Words []word `json:"words"`
		}
		if json.Unmarshal(b, &t) == nil && len(t.Words) > 0 {
			return t.Words
		}
	}
	return nil
}

// timelineWords puts every word where it plays on the timeline, through the
// AUDIO events (an OBS recording's sound sits a few frames off its picture).
// A word cut in half by an event edge goes with the event holding its start.
func timelineWords(evs []vegasctl.Event, words func(string) []word) []word {
	var out []word
	seen := map[string][]word{}
	for _, e := range evs {
		if e.Kind != "audio" || (e.Rate != 0 && e.Rate != 1) {
			continue
		}
		ws, ok := seen[e.Source]
		if !ok {
			ws = words(e.Source)
			seen[e.Source] = ws
		}
		for _, w := range ws {
			if w.Start >= e.In && w.Start < e.Out {
				d := e.Timeline - e.In
				out = append(out, word{w.Word, w.Start + d, math.Min(w.End, e.Out) + d})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	// two audio tracks of the same recording carry the same words: keep one
	var dedup []word
	for _, w := range out {
		if n := len(dedup); n > 0 && dedup[n-1].Word == w.Word && math.Abs(dedup[n-1].Start-w.Start) < 0.05 {
			continue
		}
		dedup = append(dedup, w)
	}
	return dedup
}

// srcAt is the video source and the time inside it at timeline time t.
func srcAt(evs []vegasctl.Event, t float64) (string, float64, bool) {
	for _, e := range evs {
		if e.Kind == "video" && t >= e.Timeline && t < e.TimelineEnd {
			return e.Source, e.In + (t - e.Timeline), true
		}
	}
	return "", 0, false
}

// frames saves one picture every 1/fps seconds of the timeline range, 960 px
// wide, and returns them with their timeline times.
func frames(cfg config.Config, evs []vegasctl.Event, a, b, fps float64, dir string) ([]string, []float64, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, err
	}
	var paths []string
	var times []float64
	for t := a; t < b; t += 1 / fps {
		src, s, ok := srcAt(evs, t)
		if !ok {
			continue
		}
		p := filepath.Join(dir, fmt.Sprintf("t%09.3f.jpg", t))
		if _, err := os.Stat(p); err != nil {
			cmd := exec.Command(ffmpeg(cfg), "-y", "-loglevel", "error", "-ss", fmt.Sprintf("%.3f", s), "-i", src,
				"-frames:v", "1", "-vf", "scale=960:-2", "-q:v", "3", p)
			if out, err := cmd.CombinedOutput(); err != nil {
				return nil, nil, fmt.Errorf("ffmpeg frame at %.1fs: %v %s", t, err, strings.TrimSpace(string(out)))
			}
		}
		paths, times = append(paths, p), append(times, t)
	}
	return paths, times, nil
}

func ffmpeg(cfg config.Config) string {
	if cfg.FFmpeg != "" {
		return cfg.FFmpeg
	}
	return "ffmpeg"
}

// facesIn runs the face finder (InsightFace, on the processor so it never
// fights a model for the graphics card) over the range: every face it sees.
func facesIn(cfg config.Config, evs []vegasctl.Event, a, b float64, dir, cache string) ([]sample, error) {
	var ss []sample
	if bs, err := os.ReadFile(cache); err == nil && json.Unmarshal(bs, &ss) == nil {
		return ss, nil
	}
	paths, times, err := frames(cfg, evs, a, b, 1, dir)
	if err != nil {
		return nil, err
	}
	fs, err := faceembed.EmbedAll(cfg, paths, "cpu", false)
	if err != nil {
		return nil, fmt.Errorf("face finder: %w", err)
	}
	for i, f := range fs {
		if i >= len(times) {
			break
		}
		s := sample{T: times[i]}
		w, h := imageSize(paths[i])
		for _, r := range f.All {
			if len(r.BBox) == 4 && w > 0 && r.DetScore >= 0.5 {
				s.Faces = append(s.Faces, box{r.BBox[0] / w, r.BBox[1] / h, (r.BBox[2] - r.BBox[0]) / w, (r.BBox[3] - r.BBox[1]) / h})
			}
		}
		ss = append(ss, s)
	}
	b2, _ := json.Marshal(ss)
	_ = os.WriteFile(cache, b2, 0o644)
	return ss, nil
}

// imageSize reads a JPEG's width and height from its frame header.
func imageSize(path string) (float64, float64) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, 0
	}
	for i := 2; i+9 < len(b); {
		if b[i] != 0xFF {
			i++
			continue
		}
		m := b[i+1]
		if m >= 0xC0 && m <= 0xC3 {
			return float64(binary.BigEndian.Uint16(b[i+7:])), float64(binary.BigEndian.Uint16(b[i+5:]))
		}
		i += 2 + int(binary.BigEndian.Uint16(b[i+2:]))
	}
	return 0, 0
}

// envelope is the loudness (RMS) of every 0.2 s of the timeline range, from
// the audio events' sources.
func envelope(cfg config.Config, evs []vegasctl.Event, a, b float64) []float64 {
	const rate = 16000
	per := int(beatWindow * rate)
	var env []float64
	for t := a; t < b; t += beatWindow {
		env = append(env, 0)
	}
	for _, e := range evs {
		if e.Kind != "audio" {
			continue
		}
		t0, t1 := math.Max(a, e.Timeline), math.Min(b, e.TimelineEnd)
		if t1 <= t0 {
			continue
		}
		s0 := e.In + (t0 - e.Timeline)
		out, err := exec.Command(ffmpeg(cfg), "-loglevel", "error", "-ss", fmt.Sprintf("%.3f", s0), "-t", fmt.Sprintf("%.3f", t1-t0),
			"-i", e.Source, "-vn", "-ac", "1", "-ar", "16000", "-f", "s16le", "-").Output()
		if err != nil {
			continue
		}
		first := int(math.Round((t0 - a) / beatWindow))
		for k := 0; (k+1)*per*2 <= len(out); k++ {
			sum := 0.0
			for j := 0; j < per; j++ {
				v := float64(int16(binary.LittleEndian.Uint16(out[(k*per+j)*2:]))) / 32768
				sum += v * v
			}
			if i := first + k; i < len(env) {
				env[i] = math.Max(env[i], math.Sqrt(sum/float64(per)))
			}
		}
	}
	return env
}

// Silent footage (openshorts' "Visual (no-transcript) clip selection"): a
// local model cannot watch a whole video the way Gemini does, so becky looks
// at one picture every few seconds, writes one line about it, and the shorts
// picker reads those lines in place of the words. Same picker, same rules.
const visualEvery = 3.0

func visualLines(cfg config.Config, evs []vegasctl.Event, dur float64, dir string, logf func(string, ...any)) ([]segment, error) {
	cache := filepath.Join(dir, "visual-lines.json")
	var segs []segment
	if b, err := os.ReadFile(cache); err == nil && json.Unmarshal(b, &segs) == nil && len(segs) > 0 {
		return segs, nil
	}
	paths, times, err := frames(cfg, evs, 0, dur, 1/visualEvery, filepath.Join(dir, "visual-frames"))
	if err != nil {
		return nil, err
	}
	model, mmproj, _ := cfg.GemmaAVLM()
	r := avlm.New(model, mmproj, cfg.LlamaServer, "", cfg.FFmpeg, cfg.FFprobe, logf)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Hour)
	defer cancel()
	stop, err := r.Start(ctx)
	if err != nil {
		return nil, fmt.Errorf("the picture model could not start: %w", err)
	}
	defer stop()
	logf("no speech: describing %d pictures (one every %.0f s) for the shorts picker...", len(paths), visualEvery)
	for i, p := range paths {
		res, err := r.AnalyzeImage(ctx, p, avlm.ImageOptions{MaxTokens: 60,
			Prompt: "One short sentence: what is happening in this video frame? Name the people's actions and anything surprising, funny or dramatic. No guessing beyond what is visible."})
		if err != nil {
			return nil, err
		}
		segs = append(segs, segment{Start: times[i], End: times[i] + visualEvery, Text: "[picture] " + strings.TrimSpace(strings.ReplaceAll(res.Text, "\n", " "))})
	}
	b, _ := json.Marshal(segs)
	_ = os.WriteFile(cache, b, 0o644)
	return segs, nil
}

type segment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

// sentences groups timeline words into transcript lines for the picker: a
// line ends on . ? ! or a pause over a second.
func sentences(ws []word) []segment {
	var out []segment
	var cur []string
	start := 0.0
	for i, w := range ws {
		if len(cur) == 0 {
			start = w.Start
		}
		cur = append(cur, w.Word)
		end := strings.HasSuffix(strings.TrimRight(w.Word, `"')`), ".") || strings.ContainsAny(w.Word[max(0, len(w.Word)-1):], "?!")
		if end || i == len(ws)-1 || ws[i+1].Start-w.End > 1 {
			out = append(out, segment{start, w.End, strings.Join(cur, " ")})
			cur = nil
		}
	}
	return out
}
