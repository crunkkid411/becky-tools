package main

// leadtrim.go — trims non-speech lead-in time off the START of each fresh
// speaking span: the exact gap SKILL.md's rough-cut definition calls out by
// name - "the speaker adjusting their position before delivering a line -
// which a human editor then has to go trim." Jordan, 2026-08-25, after the
// splice-order fix restored ~26 minutes of exactly this kind of dead air
// that a coordinate bug had been (unreliably, accidentally) stripping out:
// "did we ever get the lip-sync thing working on gpu?? if not, then just use
// fucking gemma4 to judge when i start talking because this is still
// useless."
//
// Structurally safe by construction, unlike narrativetrim.go (which Jordan
// has ruled out entirely - see feedback-roughcut-never-cut-content-only-
// retakes.md): this can only move a span's START later, shrinking it from
// the front. It never touches a span's END, never merges/drops/judges a
// span's CONTENT, and a span the model is not confident about is left
// completely alone (fail closed). LR-ASD (already computed, cached from the
// overnight sweep) answers the easy cases for free; Gemma-4 is only asked
// about a fresh start LR-ASD cannot already confirm is speech from frame
// one - the same escalate-only-when-unclear shape CLAUDE.md's corroboration
// ladder describes.
//
// STANDALONE pass (--trim-lead-in), same shape as --triage-markers/
// --burn-quote-overlays: reads an EXISTING vegas_cut.json, trims in place,
// and ripple-shifts every event/quote/marker after each trim earlier by
// however much was removed - simpler than narrativetrim's ripple-delete
// (applyNarrativeCuts) because nothing is ever dropped, only shrunk from one
// edge, so a single forward pass with a running cumulative shift suffices.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"becky-go/internal/avlm"
	"becky-go/internal/beckyio"
	"becky-go/internal/config"
)

const (
	// leadTrimMinGapSec: a source-time gap at least this long between one
	// event's Out and the next event's In (same source clip) - or any clip
	// change, or a quote splice - marks a genuine cut: a "fresh start"
	// candidate. Smaller gaps are just refineWordEdges/splitOnWordGaps
	// slicing one continuous keep into consecutive dialogue-carrying pieces,
	// not a real pause.
	leadTrimMinGapSec = 0.3
	// leadTrimConfidentSpeakingFrac: LR-ASD covering the first second of a
	// fresh start at or above this confidence means he is CLEARLY already
	// talking from frame one - skip, nothing to trim, no need to ask Gemma-4.
	leadTrimConfidentSpeakingFrac = 0.5
	// leadTrimWindowPadBefore/After: how much of the clip Gemma-4 watches per
	// candidate - a little before the cut point (so it can see him NOT yet
	// talking) plus enough after to see the line actually begin. Small and
	// fast, well under avlm's ~28s cap.
	leadTrimWindowPadBefore = 1.5
	leadTrimWindowAfterCap  = 8.0
	// leadTrimMaxTrimSec caps how much can ever be removed from one span's
	// front however confident the model sounds - a sanity ceiling against a
	// misparsed or wildly wrong answer eating real content. Deliberately
	// smaller than the window itself.
	leadTrimMaxTrimSec = 6.0
)

// leadTrimCandidate is one "fresh start" - a point where a real cut just
// happened in SOURCE time, so the seconds right after it are worth checking
// for lead-in dead air.
type leadTrimCandidate struct {
	EventIdx int
	Source   string
	SrcStart float64
	SrcEnd   float64
}

// findFreshStarts scans events (already TL-sorted, post-splice) for genuine
// cut points: the very first event, a clip change, or a source-time gap from
// the previous event on the SAME clip. TL is always contiguous post-splice
// (butt-joined), so it carries no information about whether a real pause
// happened in the source - only In/Out does.
func findFreshStarts(events []tlEvent) []leadTrimCandidate {
	var out []leadTrimCandidate
	for i, e := range events {
		fresh := i == 0
		if i > 0 {
			prev := events[i-1]
			fresh = prev.Source != e.Source || (e.In-prev.Out) >= leadTrimMinGapSec
		}
		if fresh {
			out = append(out, leadTrimCandidate{EventIdx: i, Source: e.Source, SrcStart: e.In, SrcEnd: e.Out})
		}
	}
	return out
}

// speakingConfidentAtStart reports whether LR-ASD already shows confident
// speech covering the first second of [t, t+1) - if so there is nothing to
// trim and Gemma-4 need not be asked.
func speakingConfidentAtStart(speaking []speakingWindow, t float64) bool {
	for _, w := range speaking {
		lo, hi := maxF(w.Start, t), minF(w.End, t+1.0)
		if hi-lo <= 0 {
			continue
		}
		if w.BestFrac >= leadTrimConfidentSpeakingFrac {
			return true
		}
	}
	return false
}

const leadTrimSystemPrompt = "You are a video editor's assistant. You are watching the moment right around a " +
	"cut point in raw footage - the seconds just before it may show the person NOT yet speaking (settling in, " +
	"adjusting their position, silent) before they begin their line."

// leadTrimPromptTemplate asks for one short observation BEFORE the number,
// not the number alone. A bare "just answer with a number" prompt measured
// 2026-08-25: a small local model defaulted to the same lazy "0" answer on
// every single candidate regardless of actual content, rather than doing the
// visual work - the same failure mode triage.go/watchpass.go's REASON field
// already sidesteps by forcing an articulated observation first.
const leadTrimPromptTemplate = "Watch this window carefully. In one short sentence, describe what the person " +
	"is doing at the very start - already mid-sentence, or settling in/silent first. Then on its own line " +
	"write exactly:\nSECONDS: <n>\nwhere <n> is how many seconds INTO THIS WINDOW real speech actually " +
	"begins (0 if they are already speaking in the first frame; a decimal like 1.4 if there is a real gap " +
	"first; -1 if you genuinely cannot tell)."

// leadTrimVerdict is Gemma-4's read on one candidate: how many seconds into
// the watched window real speech begins, or a negative/unparsed value
// meaning "not confident" - which always means DO NOT TRIM.
type leadTrimVerdict struct {
	EventIdx         int
	OffsetIntoWindow float64
	Confident        bool
}

// parseLeadTrimSeconds finds the "SECONDS: <n>" line the prompt asks for and
// extracts <n>. Tolerates a missing colon/case/stray punctuation the same
// way triage.go's extractReasonField does. A negative value, or no
// parseable line at all, means "not confident" - fail closed.
func parseLeadTrimSeconds(raw string) (seconds float64, ok bool) {
	up := strings.ToUpper(raw)
	i := strings.Index(up, "SECONDS")
	if i < 0 {
		return 0, false
	}
	rest := raw[i+len("SECONDS"):]
	var b strings.Builder
	started := false
	for _, r := range rest {
		switch {
		case r == ':' && !started:
			continue
		case (r >= '0' && r <= '9') || r == '.' || r == '-':
			started = true
			b.WriteRune(r)
		case started:
			goto done
		}
	}
done:
	tok := b.String()
	if tok == "" || tok == "-" || tok == "." {
		return 0, false
	}
	v, err := strconv.ParseFloat(tok, 64)
	if err != nil || v < 0 {
		return 0, false
	}
	return v, true
}

// judgeLeadTrims escalates every candidate LR-ASD could not already confirm
// is speech-from-frame-one to Gemma-4, one small window each. A model that is
// unavailable returns no verdicts (nothing trimmed) rather than block the
// run - same degrade-never-crash shape as triageMarkers/judgeNarrativeBeats.
func judgeLeadTrims(cfg config.Config, cands []leadTrimCandidate, clipPaths map[string]string,
	speakingByStem map[string][]speakingWindow, verbose bool) []leadTrimVerdict {

	logf := func(f string, a ...any) { beckyio.Logf(verbose, f, a...) }
	model, mmproj, _ := cfg.GemmaAVLM()
	runner := avlm.New(model, mmproj, cfg.LlamaServer, "", cfg.FFmpeg, cfg.FFprobe, logf)
	if err := runner.Ready(); err != nil {
		logf("lead-trim: local model unavailable: %v - no trims made", err)
		return nil
	}

	ctx := context.Background()
	var out []leadTrimVerdict
	for _, c := range cands {
		stem := stemOf(c.Source)
		speaking := speakingByStem[stem]
		if speakingConfidentAtStart(speaking, c.SrcStart) {
			continue // already confidently speaking from frame one - nothing to check
		}
		path, ok := clipPaths[stem]
		if !ok {
			continue
		}
		winStart := c.SrcStart - leadTrimWindowPadBefore
		if winStart < 0 {
			winStart = 0
		}
		winLen := c.SrcEnd - winStart
		if cap := leadTrimWindowPadBefore + leadTrimWindowAfterCap; winLen > cap {
			winLen = cap
		}
		res, err := runner.Analyze(ctx, avlm.Options{
			Clip:         path,
			SystemPrompt: leadTrimSystemPrompt,
			Prompt:       leadTrimPromptTemplate,
			WindowStart:  winStart,
			WindowSec:    winLen,
			FPS:          2.0,
			MaxTokens:    100,
			Temperature:  0.0,
			Seed:         42,
			Verbose:      verbose,
		})
		if err != nil {
			logf("lead-trim %s [%.1f]: skipped (%v)", stem, c.SrcStart, err)
			continue
		}
		offsetInClip, ok := parseLeadTrimSeconds(res.Text)
		if !ok {
			logf("lead-trim %s [%.1f]: not confident, leaving as-is (%q)", stem, c.SrcStart, res.Text)
			continue
		}
		// offsetInClip is seconds from winStart; convert to an offset from
		// the CANDIDATE's own start (c.SrcStart), which is what actually gets
		// trimmed - the pad-before seconds are context, not trimmable.
		offset := (winStart + offsetInClip) - c.SrcStart
		if offset <= 0 {
			logf("lead-trim %s [%.1f]: already speaking at the start, nothing to trim", stem, c.SrcStart)
			continue
		}
		if offset > leadTrimMaxTrimSec {
			offset = leadTrimMaxTrimSec
		}
		if offset > (c.SrcEnd - c.SrcStart) {
			logf("lead-trim %s [%.1f]: model's answer would eat the whole span - skipping, not trusting it", stem, c.SrcStart)
			continue
		}
		out = append(out, leadTrimVerdict{EventIdx: c.EventIdx, OffsetIntoWindow: offset, Confident: true})
		logf("lead-trim %s [%.1f]: trimming %.2fs of lead-in", stem, c.SrcStart, offset)
	}
	return out
}

// applyLeadTrims shrinks each verdict's event by its offset (from the FRONT
// only) and ripple-shifts every later event/quote/marker earlier by however
// much was removed before it. Nothing is ever dropped - every block survives,
// some just start later and run shorter - so a single forward pass with a
// running cumulative shift is enough (simpler than narrativetrim's
// ripple-delete, which has to handle whole blocks disappearing).
func applyLeadTrims(events []tlEvent, quotes []quoteOut, markers []markerOut,
	verdicts []leadTrimVerdict) (newEvents []tlEvent, newQuotes []quoteOut, newMarkers []markerOut, trimmedSec float64) {

	trimAt := map[int]float64{}
	for _, v := range verdicts {
		trimAt[v.EventIdx] = v.OffsetIntoWindow
		trimmedSec += v.OffsetIntoWindow
	}

	type shiftPoint struct{ tl, cum float64 }
	var shifts []shiftPoint
	cum := 0.0

	newEvents = make([]tlEvent, len(events))
	for i, e := range events {
		e.TL -= cum
		if d, ok := trimAt[i]; ok {
			e.In += d
			cum += d
		}
		newEvents[i] = e
		shifts = append(shifts, shiftPoint{events[i].TL, cum})
	}
	sort.SliceStable(shifts, func(i, j int) bool { return shifts[i].tl < shifts[j].tl })

	shiftFor := func(t float64) float64 {
		s := 0.0
		for _, p := range shifts {
			if p.tl <= t {
				s = p.cum
			} else {
				break
			}
		}
		return s
	}

	newQuotes = make([]quoteOut, len(quotes))
	for i, q := range quotes {
		q.TL -= shiftFor(q.TL)
		newQuotes[i] = q
	}
	newMarkers = make([]markerOut, len(markers))
	for i, m := range markers {
		m.T -= shiftFor(m.T)
		newMarkers[i] = m
	}
	return newEvents, newQuotes, newMarkers, trimmedSec
}

// runLeadTrimPass is becky-roughcut's --trim-lead-in entry point.
func runLeadTrimPass(out string, verbose bool) error {
	vb, err := os.ReadFile(filepath.Join(out, "vegas_cut.json"))
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := json.Unmarshal(vb, &doc); err != nil {
		return err
	}
	var typed struct {
		Events  []tlEvent   `json:"events"`
		Quotes  []quoteOut  `json:"quotes"`
		Markers []markerOut `json:"markers"`
	}
	if err := json.Unmarshal(vb, &typed); err != nil {
		return err
	}

	cands := findFreshStarts(typed.Events)

	clipPaths := map[string]string{}
	speakingByStem := map[string][]speakingWindow{}
	for _, c := range cands {
		stem := stemOf(c.Source)
		if _, ok := clipPaths[stem]; ok {
			continue
		}
		clipPaths[stem] = c.Source
		speakingByStem[stem] = loadSpeaking(out, clip{Path: c.Source, Stem: stem})
	}

	verdicts := judgeLeadTrims(config.Load(), cands, clipPaths, speakingByStem, verbose)
	newEvents, newQuotes, newMarkers, trimmedSec := applyLeadTrims(typed.Events, typed.Quotes, typed.Markers, verdicts)

	doc["events"] = newEvents
	doc["quotes"] = newQuotes
	doc["markers"] = newMarkers

	nb, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "vegas_cut.json"), nb, 0o644); err != nil {
		return err
	}

	beckyio.Logf(true, "lead-trim: %d fresh starts checked, %d trimmed, %.1fs of lead-in dead air removed",
		len(cands), len(verdicts), trimmedSec)

	report, _ := json.MarshalIndent(map[string]any{
		"candidates":  len(cands),
		"trimmed":     len(verdicts),
		"trimmed_sec": trimmedSec,
	}, "", "  ")
	return os.WriteFile(filepath.Join(out, "lead_trim.json"), report, 0o644)
}
