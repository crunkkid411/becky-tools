// windows.go - the DEFAULT picker since 2026-10-13: openshorts' two passes
// (internal/clippick). Jordan, 2026-10-13: "becky-tools clipping workflow is
// also broken and useless" - the structural candidates were judged one at a
// time and could not be ranked; openshorts has the model rank whole windows of
// the transcript against each other, then pick the clip inside the best ones,
// and code snaps every cut onto a word. --picker structure is the old path.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"becky-go/internal/clippick"
	"becky-go/internal/config"
	"becky-go/internal/llmlocal"
	"becky-go/internal/pathx"
	"becky-go/internal/sidecar"
)

// pickWindows runs the openshorts picker over every transcript and returns the
// best `top` clips across all of them.
func pickWindows(cfg config.Config, sources []string, top int, minDur, maxDur float64, logf func(string, ...any)) ([]momentOut, string, []string) {
	model := cfg.GemmaModel12B
	if _, err := os.Stat(model); err != nil {
		model = cfg.GemmaModel
	}
	if model == "" {
		return nil, "", []string{"no local model configured (gemma_model)"}
	}
	c := llmlocal.NewWarmClientCtx(model, cfg.LlamaServer, 16384, logf)
	if err := c.Available(); err != nil {
		return nil, "", []string{"the picker could not start its model: " + err.Error()}
	}
	defer c.Close()
	chat := func(system, user string, schema map[string]any, maxTok int) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		return c.Chat(ctx, system, user, llmlocal.Options{MaxTokens: maxTok, ResponseFormat: schema})
	}

	var notes []string
	var all []momentOut
	for _, src := range sources {
		sub, err := sidecar.ParseSubtitle(src)
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s: %v", pathx.Base(src), err))
			continue
		}
		var segs []clippick.Segment
		dur := 0.0
		for _, s := range sub.Segments {
			segs = append(segs, clippick.Segment{Start: s.Start, End: s.End, Text: s.Text})
			dur = max(dur, s.End)
		}
		words := wordsFor(src)
		if len(words) == 0 {
			notes = append(notes, pathx.Base(src)+": no word timings next to it, so the cuts sit where the model put them (not snapped onto words)")
		}
		logf("%s: picking clips (%d lines, %.0f min)...", pathx.Base(src), len(segs), dur/60)
		clips, err := clippick.Pick(chat, segs, words, dur, clippick.Options{MinSec: minDur, MaxSec: maxDur, ScoreBatch: 3, Logf: logf})
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s: %v", pathx.Base(src), err))
			continue
		}
		for _, cl := range clips {
			all = append(all, momentOut{
				Source: src, Start: cl.Start, End: cl.End, Duration: cl.End - cl.Start,
				Score: float64(cl.Score) / 100, Confidence: "picked", Basis: cl.Why,
				Text: textIn(sub.Segments, cl.Start, cl.End), Hook: cl.Hook, Title: cl.Title,
				TikTok: cl.DescTikTok, Instagram: cl.DescInstagram, WindowScore: cl.WindowScore,
			})
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Score > all[j].Score })
	if top > 0 && len(all) > top {
		all = all[:top]
	}
	return all, "local:" + pathx.Base(model) + " (openshorts two-pass picker)", notes
}

// wordsFor reads the word timings of becky-transcribe's JSON next to the
// transcript (or the transcript itself when it is that JSON).
func wordsFor(src string) []clippick.Word {
	cands := []string{src}
	if media := mediaFor(src); media != "" {
		stem := strings.TrimSuffix(media, filepath.Ext(media))
		cands = append(cands, stem+".transcript.json", stem+".json")
	}
	for _, p := range cands {
		if !strings.HasSuffix(strings.ToLower(p), ".json") {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var t struct {
			Words []struct {
				Word  string  `json:"word"`
				Start float64 `json:"start"`
				End   float64 `json:"end"`
			} `json:"words"`
		}
		if json.Unmarshal(b, &t) == nil && len(t.Words) > 0 {
			out := make([]clippick.Word, 0, len(t.Words))
			for _, w := range t.Words {
				out = append(out, clippick.Word{Text: w.Word, Start: w.Start, End: w.End})
			}
			return out
		}
	}
	return nil
}

func textIn(segs []sidecar.Segment, a, b float64) string {
	var t []string
	for _, s := range segs {
		if s.End > a && s.Start < b {
			t = append(t, strings.TrimSpace(s.Text))
		}
	}
	return strings.Join(t, " ")
}
