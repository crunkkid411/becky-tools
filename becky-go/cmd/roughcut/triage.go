package main

// triage.go — Gemma-4 reviews every pending marker BEFORE it ever reaches
// Jordan. His direct correction (2026-08-24, after watching the round-2
// timeline): "All those flags on the timeline asking for human review need
// to be reviewed by at least one VISION model first... There is no reason to
// ask me to watch the timeline choice if gemma4 has not already done so...
// it ABSOLUTELY can watch up to 30 seconds at a time (because it will likely
// need to know what comes before and after the marker with the question)."
//
// Unlike watchpass.go's --watch (a blanket pass over every KEPT block, the
// becky-clip "AN LLM MUST WATCH THE OUTPUT BEFORE IT SHIPS" rule Jordan says
// explicitly does NOT transfer to becky-roughcut's long-form documentary use
// case), this only looks at spans someone ALREADY flagged - a review/retake
// marker - with context padding, and answers the SPECIFIC concern already
// written into that marker's own title. Jordan only ever sees a flag the
// model could not already resolve for him.
//
// Standalone by design, same hardware reason as --watch: Gemma-4 (llama-
// server, ~5GB VRAM) cannot run alongside the LR-ASD speaking sweep on this
// machine's 8GB card ("ONE MODEL AT A TIME", SKILL.md VIDEO CLIPPING rule
// #5). Run --triage-markers once the GPU is free.
//
// Never cuts or shortens anything - same invariant as speakingCorroboration
// and detectBadTakes' RETAKE? markers: a detector (or a model) is a signal,
// never a verdict. This pass may only REMOVE a marker (when Gemma-4 confirms
// the concern was a false alarm) or ANNOTATE one that stays (so Jordan sees
// the model's read right on the timeline marker itself) - it never places a
// cut, and a marker it cannot confidently resolve always survives.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"becky-go/internal/avlm"
	"becky-go/internal/beckyio"
	"becky-go/internal/config"
)

const (
	triagePadSec    = 6.0  // context before/after the flagged span - Jordan's "what comes before and after"
	triageWindowCap = 28.0 // stay under Gemma-4's ~30s audio/video window cap
)

// triageVerdict is one marker's review result.
type triageVerdict struct {
	Marker  pendingMarker `json:"marker"`
	Verdict string        `json:"verdict"` // "resolved" | "needs_review" | "skipped"
	Reason  string        `json:"reason,omitempty"`
}

// triageWindow returns the [start,end] SOURCE-time window to watch for one
// marker: the flagged span itself plus context padding on both sides,
// clamped so the total never exceeds Gemma-4's window cap. A marker with no
// real span (TEnd <= T, e.g. an older artifact written before TEnd existed)
// is treated as a single instant.
func triageWindow(pm pendingMarker) (start, end float64) {
	spanStart, spanEnd := pm.T, pm.TEnd
	if spanEnd < spanStart {
		spanEnd = spanStart
	}
	start = spanStart - triagePadSec
	if start < 0 {
		start = 0
	}
	end = spanEnd + triagePadSec
	if over := (end - start) - triageWindowCap; over > 0 {
		start += over / 2
		end -= over / 2
		if end-start > triageWindowCap { // the flagged span alone still exceeds the cap - center on it
			mid := (spanStart + spanEnd) / 2
			start, end = mid-triageWindowCap/2, mid+triageWindowCap/2
		}
	}
	if start < 0 {
		start = 0
	}
	return start, end
}

const triageSystemPrompt = "You are a professional video editor's assistant. An automated rough-cut " +
	"tool flagged one moment on the timeline for a human to review. You are watching a short window " +
	"around that exact moment, including context before and after it."

const triagePromptTemplate = "The automated tool's note was: %q\n\n" +
	"Watch and listen, then decide whether a human still needs to look at this. Answer in this exact " +
	"format:\n\nVERDICT: RESOLVED or NEEDS_REVIEW\nREASON: <one short sentence>\n\n" +
	"RESOLVED means you can confidently answer the tool's own question yourself from what you just " +
	"watched - the concern is a false alarm. NEEDS_REVIEW means you are not confident, or you confirm " +
	"something really is off and a human should decide what to do about it. When unsure, say " +
	"NEEDS_REVIEW - never guess RESOLVED."

// triageMarkers runs each pending marker past Gemma-4 with a window of
// context around its flagged span, and returns every verdict (including
// "skipped" when the model is not available or the source clip cannot be
// found, so nothing is ever silently dropped just because the GPU was busy).
func triageMarkers(cfg config.Config, marks []pendingMarker, clipPaths map[string]string, verbose bool) []triageVerdict {
	model, mmproj, _ := cfg.GemmaAVLM()
	logf := func(format string, a ...any) { beckyio.Logf(verbose, format, a...) }
	runner := avlm.New(model, mmproj, cfg.LlamaServer, "", cfg.FFmpeg, cfg.FFprobe, logf)

	if err := runner.Ready(); err != nil {
		out := make([]triageVerdict, len(marks))
		for i, m := range marks {
			out[i] = triageVerdict{Marker: m, Verdict: "skipped", Reason: "gemma4 not available: " + err.Error()}
		}
		return out
	}

	ctx := context.Background()
	out := make([]triageVerdict, 0, len(marks))
	for _, m := range marks {
		path, ok := clipPaths[stemOf(m.Source)]
		if !ok {
			out = append(out, triageVerdict{Marker: m, Verdict: "skipped", Reason: "source clip not found: " + m.Source})
			continue
		}
		start, end := triageWindow(m)
		res, err := runner.Analyze(ctx, avlm.Options{
			Clip:         path,
			SystemPrompt: triageSystemPrompt,
			Prompt:       fmt.Sprintf(triagePromptTemplate, m.Title),
			WindowStart:  start,
			WindowSec:    end - start,
			FPS:          1.0,
			MaxTokens:    150,
			Temperature:  0.2,
			Seed:         42,
			Verbose:      verbose,
		})
		v := triageVerdict{Marker: m}
		if err != nil {
			v.Verdict, v.Reason = "skipped", err.Error()
		} else {
			v.Verdict, v.Reason = parseTriageVerdict(res.Text)
		}
		out = append(out, v)
		logf("triage %s %q [%.1f,%.1f]: %s %s", m.Source, m.Title, start, end, v.Verdict, v.Reason)
	}
	return out
}

// parseTriageVerdict reads the model's free-text answer. Fails closed to
// NEEDS_REVIEW (the opposite default from parseWatchVerdict's fail-open to
// PASS, deliberately): watchpass.go's pass only ever ADDS a flag, so a parse
// failure there must not invent one; this pass only ever REMOVES an
// already-existing marker, so a parse failure here must not disappear one.
func parseTriageVerdict(raw string) (verdict, reason string) {
	up := strings.ToUpper(raw)
	reason = extractReasonField(raw, up)
	if strings.Contains(up, "RESOLVED") && !strings.Contains(up, "NEEDS_REVIEW") {
		return "resolved", reason
	}
	return "needs_review", reason
}

// applyTriageVerdicts filters and annotates an already-built marker list
// using triage results. Joined on (timeline position, title) rather than
// source+t: titles are NOT globally unique (two speaking-corroboration
// markers on the same clip can share an identical percentage and title,
// distinguished only by where they land on the timeline - measured on real
// output the night this was built), but that pair together always is, and
// it sidesteps needing mapToTimeline's TLStart (which vegas_cut.json does
// not persist - see event's `json:"-"` tag on TLStart) a second time.
func applyTriageVerdicts(existing []markerOut, verdicts []triageVerdict) (kept []markerOut, resolved int) {
	type key struct {
		tl    float64
		title string
	}
	drop := map[key]bool{}
	reason := map[key]string{}
	for _, v := range verdicts {
		k := key{v.Marker.TL, v.Marker.Title}
		if v.Verdict == "resolved" {
			drop[k] = true
			continue
		}
		if v.Reason != "" {
			reason[k] = v.Reason
		}
	}
	for _, mk := range existing {
		k := key{mk.T, mk.Title}
		if drop[k] {
			resolved++
			continue
		}
		if r, ok := reason[k]; ok {
			mk.Title = fmt.Sprintf("%s [gemma4: %s]", mk.Title, r)
		}
		kept = append(kept, mk)
	}
	return kept, resolved
}

// reshiftPendingTL corrects TL on each pendingMarker to the position it
// ACTUALLY lands at once quotes are spliced in. spliceLayout shifts every
// marker to make room for inserted quotes (splice.go: out.Markers[i] =
// preSplice[i].T + shiftAt(...), same index, same order - a straight
// positional map) - preSplice must be the exact slice passed into
// spliceLayout, and postSplice its returned lay.Markers, so index i means
// the same marker in both. placedPending's TL was captured BEFORE that
// shift, so a later triage pass matching on TL would silently miss every
// marker after the first quote insertion (measured 2026-08-25: only 8 of
// 180 pending markers matched a real quoted 81-minute cut - the rest never
// got a chance to be dropped or annotated, no matter what Gemma-4 said).
func reshiftPendingTL(pending []pendingMarker, preSplice, postSplice []markerOut) []pendingMarker {
	shifted := map[[2]any]float64{}
	for i, m := range preSplice {
		if i < len(postSplice) {
			shifted[[2]any{m.T, m.Title}] = postSplice[i].T
		}
	}
	for i := range pending {
		if t, ok := shifted[[2]any{pending[i].TL, pending[i].Title}]; ok {
			pending[i].TL = t
		}
	}
	return pending
}

// loadPendingMarkers reads pending_markers.json; a missing file (an older
// run, or a run with nothing pending) is zero markers, not an error.
func loadPendingMarkers(out string) ([]pendingMarker, error) {
	b, err := os.ReadFile(filepath.Join(out, "pending_markers.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var pm []pendingMarker
	if err := json.Unmarshal(b, &pm); err != nil {
		return nil, err
	}
	return pm, nil
}

// runTriagePass is becky-roughcut's --triage-markers entry point: reads
// pending_markers.json + an existing vegas_cut.json from a prior detection
// run, reviews every pending marker with Gemma-4, rewrites vegas_cut.json's
// marker list in place (resolved markers dropped, kept ones annotated - every
// other field, including caller-supplied quote markers, passed through
// untouched), and writes marker_triage.json as the full report.
func runTriagePass(out string, verbose bool) error {
	pending, err := loadPendingMarkers(out)
	if err != nil {
		return err
	}

	vb, err := os.ReadFile(filepath.Join(out, "vegas_cut.json"))
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := json.Unmarshal(vb, &doc); err != nil {
		return err
	}
	var typed struct {
		Events  []event     `json:"events"`
		Markers []markerOut `json:"markers"`
	}
	if err := json.Unmarshal(vb, &typed); err != nil {
		return err
	}

	clipPaths := map[string]string{}
	for _, e := range typed.Events {
		clipPaths[stemOf(e.Source)] = e.Source
	}

	verdicts := triageMarkers(config.Load(), pending, clipPaths, verbose)
	kept, resolved := applyTriageVerdicts(typed.Markers, verdicts)
	doc["markers"] = kept

	nb, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "vegas_cut.json"), nb, 0o644); err != nil {
		return err
	}

	beckyio.Logf(true, "marker triage: %d reviewed, %d resolved (dropped), %d kept on the timeline",
		len(verdicts), resolved, len(kept))

	report, _ := json.MarshalIndent(map[string]any{
		"total":    len(verdicts),
		"resolved": resolved,
		"kept":     len(kept),
		"results":  verdicts,
	}, "", "  ")
	return os.WriteFile(filepath.Join(out, "marker_triage.json"), report, 0o644)
}
