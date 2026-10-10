package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"becky-go/internal/llmlocal"
	"becky-go/internal/mediainfo"
)

// What one index covers. picture = a single frame every `step` seconds (what is IN the shot);
// action = `actionFrames` consecutive frames as ONE input (what is HAPPENING: a wave, a fall);
// sound = `soundWin`-second stretches of audio every `soundHop` seconds.
const (
	actionFrames = 4
	soundWin     = 5.0
	soundHop     = 2.5
	frameWidth   = 448 // the model looks at 224x224; 448 keeps detail for its own resize
	batch        = 8   // inputs per embedding request
)

type embedder interface {
	EmbedMedia(ctx context.Context, inputs []any) ([][]float64, error)
}

// indexVideo extracts frames (every `step` s; step <= 0 = every frame) and the sound track, and
// embeds pictures, actions and sound. dur is the probed duration; hasVideo/hasAudio which streams
// exist. All extraction goes to a temp dir that is removed afterwards.
func indexVideo(ctx context.Context, e embedder, ffmpeg, path string, fps, step float64, hasVideo, hasAudio bool, logf func(string, ...any)) ([]item, error) {
	work, err := os.MkdirTemp("", "becky_mediasearch_*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	var items []item

	if hasVideo {
		rate := 1 / step
		if step <= 0 {
			rate = fps
		}
		if rate <= 0 {
			rate = 1
		}
		pattern := filepath.Join(work, "f%06d.jpg")
		args := []string{"-v", "error", "-y", "-i", path, "-vf", fmt.Sprintf("fps=%g,scale=%d:-2", rate, frameWidth), "-q:v", "3", pattern}
		if out, err := exec.CommandContext(ctx, ffmpeg, args...).CombinedOutput(); err != nil {
			return nil, fmt.Errorf("ffmpeg frames: %v: %s", err, tail(string(out)))
		}
		frames, _ := filepath.Glob(filepath.Join(work, "f*.jpg"))
		sort.Strings(frames)
		logf("mediasearch: %d frames from %s", len(frames), filepath.Base(path))
		at := func(i int) float64 { return float64(i) / rate }

		var srcs [][]string // the frame files behind each input
		var meta []item
		for i, f := range frames {
			srcs = append(srcs, []string{f})
			meta = append(meta, item{Kind: "picture", Start: at(i), End: at(i) + 1/rate})
		}
		// Actions: actionFrames frames one second apart as ONE input, every 2 s (so an action
		// clip spans ~4 s whatever the frame step).
		per := int(math.Max(1, math.Round(rate)))
		for i := 0; i+per*(actionFrames-1) < len(frames); i += 2 * per {
			var fs []string
			for k := 0; k < actionFrames; k++ {
				fs = append(fs, frames[i+k*per])
			}
			srcs = append(srcs, fs)
			meta = append(meta, item{Kind: "action", Start: at(i), End: at(i+per*(actionFrames-1)) + 1})
		}
		vecs, err := embedAll(ctx, e, len(srcs), func(i int) (any, error) { return imagesInput(srcs[i]) }, logf)
		if err != nil {
			return nil, err
		}
		for i := range meta {
			meta[i].Vec = vecs[i]
		}
		items = append(items, meta...)
		for _, f := range frames {
			os.Remove(f)
		}
	}

	if hasAudio {
		wav := filepath.Join(work, "sound.wav")
		args := []string{"-v", "error", "-y", "-i", path, "-vn", "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", wav}
		if out, err := exec.CommandContext(ctx, ffmpeg, args...).CombinedOutput(); err != nil {
			return items, fmt.Errorf("ffmpeg sound: %v: %s", err, tail(string(out)))
		}
		pcm, err := readPCM16(wav)
		if err != nil {
			return items, err
		}
		var files []string
		var meta []item
		total := float64(len(pcm)) / 16000
		for t := 0.0; t < total; t += soundHop {
			end := math.Min(t+soundWin, total)
			if end-t < 1 && t > 0 {
				break
			}
			f := filepath.Join(work, fmt.Sprintf("s%06d.wav", len(meta)))
			if err := writePCM16(f, pcm[int(t*16000):int(end*16000)]); err != nil {
				return items, err
			}
			files = append(files, f)
			meta = append(meta, item{Kind: "sound", Start: t, End: end})
		}
		vecs, err := embedAll(ctx, e, len(files), func(i int) (any, error) { return llmlocal.AudioInput(files[i]) }, logf)
		if err != nil {
			return items, err
		}
		for i := range meta {
			meta[i].Vec = vecs[i]
		}
		items = append(items, meta...)
	}
	return items, nil
}

// indexImage embeds one still picture.
func indexImage(ctx context.Context, e embedder, path string) ([]item, error) {
	in, err := llmlocal.ImageInput(path)
	if err != nil {
		return nil, err
	}
	v, err := e.EmbedMedia(ctx, []any{in})
	if err != nil {
		return nil, err
	}
	return []item{{Kind: "picture", Vec: f32(v[0])}}, nil
}

// imagesInput is one embedding input made of one or more frames (several = one "action" input).
func imagesInput(files []string) (any, error) {
	var parts []any
	for _, f := range files {
		in, err := llmlocal.ImageInput(f)
		if err != nil {
			return nil, err
		}
		parts = append(parts, in.(map[string]any)["content"].([]any)...)
	}
	return map[string]any{"content": parts}, nil
}

// embedAll embeds n inputs, `batch` at a time, building each input only when its batch is sent
// (every frame of an hour-long video never sits in memory at once).
func embedAll(ctx context.Context, e embedder, n int, input func(int) (any, error), logf func(string, ...any)) ([][]float32, error) {
	out := make([][]float32, 0, n)
	for i := 0; i < n; i += batch {
		j := min(i+batch, n)
		ins := make([]any, 0, j-i)
		for k := i; k < j; k++ {
			in, err := input(k)
			if err != nil {
				return nil, err
			}
			ins = append(ins, in)
		}
		v, err := e.EmbedMedia(ctx, ins)
		if err != nil {
			return nil, err
		}
		for _, x := range v {
			out = append(out, f32(x))
		}
		if (i/batch)%50 == 0 {
			logf("mediasearch: embedded %d/%d", j, n)
		}
	}
	return out, nil
}

func f32(v []float64) []float32 {
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(x)
	}
	return out
}

// readPCM16 returns the samples of a mono 16-bit WAV written by ffmpeg (finds the "data" chunk).
func readPCM16(path string) ([]int16, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	for i := 12; i+8 <= len(b); {
		id, n := string(b[i:i+4]), int(binary.LittleEndian.Uint32(b[i+4:i+8]))
		if id == "data" {
			end := min(i+8+n, len(b))
			pcm := make([]int16, (end-i-8)/2)
			for k := range pcm {
				pcm[k] = int16(binary.LittleEndian.Uint16(b[i+8+2*k:]))
			}
			return pcm, nil
		}
		i += 8 + n + n%2
	}
	return nil, fmt.Errorf("%s: no audio data", path)
}

// writePCM16 writes mono 16 kHz 16-bit samples as a WAV file.
func writePCM16(path string, pcm []int16) error {
	b := make([]byte, 44+2*len(pcm))
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+2*len(pcm)))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], 16000)
	binary.LittleEndian.PutUint32(b[28:], 32000)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(2*len(pcm)))
	for i, s := range pcm {
		binary.LittleEndian.PutUint16(b[44+2*i:], uint16(s))
	}
	return os.WriteFile(path, b, 0o644)
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		return s[len(s)-300:]
	}
	return s
}

// refine re-reads every frame around each picture/action hit (at the file's own frame rate) and
// moves Hit.Frame to the single best-matching frame. Only the few top hits are re-read, so the
// result is frame-accurate without reading every frame of every video.
func refine(ctx context.Context, ffmpeg string, e embedder, hits []Hit, q []float64, infos map[string]mediainfo.Info, logf func(string, ...any)) {
	for i := range hits {
		h := &hits[i]
		info := infos[h.File]
		if h.Kind == "sound" || h.Kind == "said" || imageExt[strings.ToLower(filepath.Ext(h.File))] || info.FPS <= 0 {
			continue
		}
		// ponytail: one second either side of the best window frame; widen if hits land off-frame.
		start, end := max(0, h.Frame-1), h.Frame+1
		if info.Duration > 0 {
			end = min(end, info.Duration)
		}
		work, err := os.MkdirTemp("", "becky_mediasearch_refine_*")
		if err != nil {
			continue
		}
		// fps = the file's own rate, so output frame k (0-based) is at start + k/fps.
		args := []string{"-v", "error", "-y", "-ss", fmt.Sprintf("%.3f", start), "-i", h.File, "-t", fmt.Sprintf("%.3f", end-start),
			"-vf", fmt.Sprintf("fps=%g,scale=%d:-2", info.FPS, frameWidth), "-q:v", "3", "-start_number", "0",
			filepath.Join(work, "%06d.jpg")}
		var frames []string
		if out, err := exec.CommandContext(ctx, ffmpeg, args...).CombinedOutput(); err == nil {
			frames, _ = filepath.Glob(filepath.Join(work, "*.jpg"))
			sort.Strings(frames)
		} else {
			logf("mediasearch: frame-by-frame pass skipped for %s: %s", filepath.Base(h.File), tail(string(out)))
		}
		if len(frames) > 0 {
			vecs, err := embedAll(ctx, e, len(frames), func(k int) (any, error) { return llmlocal.ImageInput(frames[k]) }, logf)
			if err == nil {
				bestK, bestS := 0, -1.0
				for k, v := range vecs {
					if s := dot(v, q); s > bestS {
						bestK, bestS = k, s
					}
				}
				h.Frame = start + float64(bestK)/info.FPS
				h.Score = max(h.Score, bestS)
			}
		}
		os.RemoveAll(work)
	}
}

func dot(a []float32, b []float64) float64 {
	s := 0.0
	for i := range a {
		s += float64(a[i]) * b[i]
	}
	return s
}
