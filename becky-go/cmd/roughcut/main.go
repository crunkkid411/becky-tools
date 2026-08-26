// becky-roughcut — one dumb call that turns a folder of raw takes into a
// true rough cut sitting in Vegas Pro.
//
// A rough cut is an EDIT (WE_TRIED.md, Jordan's definition): it plays start to
// finish with nothing in it that isn't content. This tool makes every cut the
// way a human editor would, from signals only, and never touches the source
// media:
//
//  1. takes are ordered by EMBEDDED creation_time (filesystem times are lies);
//  2. meaningless silence is cut by duration, not raw volume: the audio is
//     loudnorm'd for ANALYSIS, and any quiet stretch >= --pause (0.75s) is a
//     jump cut - word gaps and breaths are shorter, thinking pauses and take
//     gaps are longer (detect.go has the measured reasoning);
//  3. short ad-libs ("What.", "Okay.") get wider padding (0.5s/0.7s);
//  4. every cut boundary is snapped sample-accurate onto a quiet
//     zero-crossing of the ORIGINAL audio so no cut pops (zcross.go);
//  5. abandoned re-takes: chains of >=3 restarts are CUT to a fixpoint;
//     two-clean-alternate-take re-wordings become RETAKE? markers and the
//     human picks in the morning (badtake.go);
//  6. a QA gate checks every 3+ word transcript cue still HAS its words on
//     the timeline (onset and offset covered); a cue split by an intentional
//     jump cut is fine, a cue whose words vanished is not;
//  7. the result is emitted as cut.yaml, library.yaml, qa.json and a
//     vegas_cut.json that vegas/BeckyRoughCut.cs assembles into a populated
//     Vegas Pro timeline (quote markers + per-clip regions included) with
//     --launch-vegas - fully unattended.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/mediainfo"
	"becky-go/internal/proc"
	"becky-go/internal/quotes"
	"becky-go/internal/sampledecode"
)

type span struct {
	Start, End float64
}

type clip struct {
	Path         string
	Stem         string
	CreationTime string
	Duration     float64
	FPS          float64
	Width        int
	Height       int
	SRT          string
}

type event struct {
	Source   string  `json:"source"`
	In       float64 `json:"in"`
	Out      float64 `json:"out"`
	TLStart  float64 `json:"-"`
	Dialogue string  `json:"-"`
}

type markerIn struct {
	Source     string  `json:"source"`
	SourceTime float64 `json:"source_time"`
	Title      string  `json:"title"`
	Kind       string  `json:"kind"`
}

type markerOut struct {
	T     float64 `json:"t"`
	Title string  `json:"title"`
}

// qaCue is one transcript cue reported in qa.json (dropped words, or words cut
// as an abandoned retake) so Jordan can audit every removal.
type qaCue struct {
	Source string  `json:"source"`
	Start  float64 `json:"start"`
	End    float64 `json:"end"`
	Text   string  `json:"text"`
}

// pendingMarker is a review flag not yet placed on the timeline. T/TEnd are
// SOURCE time (the flagged span); TL is filled in once mapToTimeline places
// it (0 until then). Exported + json-tagged so a run can persist the placed
// subset to pending_markers.json for the standalone --triage-markers pass
// (triage.go) to pick up later, once the GPU is free of the LR-ASD sweep.
type pendingMarker struct {
	Source string  `json:"source"`
	Title  string  `json:"title"`
	Kind   string  `json:"kind"`
	T      float64 `json:"t"`
	TEnd   float64 `json:"t_end"`
	TL     float64 `json:"tl"`
}

func main() {
	pause := flag.Float64("pause", defaultPauseSec, "quiet stretches this long (s) are jump cuts; shorter pauses stay")
	vadThreshold := flag.Float64("vad-threshold", 0.5, "Silero VAD sensitivity for the junk-keep sanity pass")
	vadSpeechPct := flag.Float64("vad-speech-pct", 0.0, "opt-in Silero junk-keep filter (%% speech); 0 = off - Silero proved untrustworthy on quiet-mic footage")
	outDir := flag.String("out", "", "artifact dir (default <project-dir>/_roughcut)")
	markersIn := flag.String("markers", "", "optional markers.json: [{source, source_time, title, kind}] placed on the timeline")
	quotesIn := flag.String("quotes", "", "optional verified quotes json: [{q, source, in, out}] inserted SEQUENTIALLY at their marker (main edit stops, quote plays, main resumes)")
	vegasScript := flag.String("vegas-script", "", "path to vegas/BeckyRoughCut.cs (default: found next to this exe)")
	launchVegas := flag.Bool("launch-vegas", false, "after building, launch Vegas Pro headless and populate the timeline (save .veg, exit)")
	vegasOnly := flag.Bool("vegas-only", false, "STANDALONE mode: launch Vegas headless on the EXISTING vegas_cut.json and exit - no detection, no re-run. Use after --triage-markers (or any other vegas_cut.json edit) so the .veg actually reflects it, without redoing full detection and overwriting that edit.")
	watch := flag.Bool("watch", false, "STANDALONE mode: an LLM (Gemma-4) watches every merged block of an EXISTING vegas_cut.json and writes watch_report.json. Run this once the GPU is free of any other model (LR-ASD sweep, etc) - see watchpass.go.")
	triage := flag.Bool("triage-markers", false, "STANDALONE mode: an LLM (Gemma-4) reviews every pending review/retake marker from an EXISTING run (pending_markers.json), with context before and after, and either resolves it (drops it from vegas_cut.json) or keeps it annotated with the model's own read. Run once the GPU is free - see triage.go.")
	narrativeTrim := flag.Bool("narrative-trim", false, "STANDALONE mode: an LLM (Gemma-4) judges every remaining beat of narration in an EXISTING vegas_cut.json against -target-minutes and removes only the beats it is confident are redundant/tangential (never a unique fact). Run after --triage-markers when the cut is still too long. See narrativetrim.go.")
	targetMinutes := flag.Float64("target-minutes", 58.0, "used with -narrative-trim: the length to cut toward")
	burnOverlays := flag.Bool("burn-quote-overlays", false, "STANDALONE mode: burns becky-review-3's forensic lower-third overlay (no filename/name, no captions) into every already-placed quote clip in an EXISTING vegas_cut.json, in place. Safe to run before or after --triage-markers. See overlay.go.")
	verbose := flag.Bool("verbose", false, "progress on stderr")

	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: becky-roughcut <project-dir> [options]\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	dir := flag.Arg(0)
	// flags typed AFTER the positional (the natural way) are not parsed by the
	// first pass - re-parse the tail, same pattern as cmd/cut.
	if flag.NArg() > 1 {
		if err := flag.CommandLine.Parse(flag.Args()[1:]); err != nil {
			beckyio.Fatalf("flags: %v", err)
		}
	}
	if dir == "" {
		beckyio.Fatalf("usage: becky-roughcut <project-dir> [options]")
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		beckyio.Fatalf("project dir not found: %s", dir)
	}
	dir, _ = filepath.Abs(dir)
	out := *outDir
	if out == "" {
		out = filepath.Join(dir, "_roughcut")
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		beckyio.Fatalf("create out dir: %v", err)
	}

	if *watch {
		if err := runWatchPass(out, *verbose); err != nil {
			beckyio.Fatalf("%v", err)
		}
		return
	}
	if *vegasOnly {
		if err := launchVegasPro(out, *vegasScript, *verbose); err != nil {
			beckyio.Fatalf("%v", err)
		}
		return
	}
	if *triage {
		if err := runTriagePass(out, *verbose); err != nil {
			beckyio.Fatalf("%v", err)
		}
		return
	}
	if *narrativeTrim {
		if err := runNarrativeTrimPass(out, *targetMinutes, *verbose); err != nil {
			beckyio.Fatalf("%v", err)
		}
		return
	}
	if *burnOverlays {
		if err := runBurnOverlaysPass(out, *verbose); err != nil {
			beckyio.Fatalf("%v", err)
		}
		return
	}

	clips, err := inventory(dir)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}
	beckyio.Logf(*verbose, "%d clips ordered by embedded creation_time", len(clips))

	var events []event
	var dropped, cutAsRetake []qaCue
	var markers []markerOut
	var pendingMarkers []pendingMarker
	gains := map[string]float64{}
	summaries := map[string]string{}
	totalKeep := 0.0

	for _, c := range clips {
		cues := parseCues(c)

		wav, err := extract16k(c, out)
		if err != nil {
			beckyio.Logf(true, "clip %s: %v (skipped)", c.Stem, err)
			continue
		}
		gain, noiseFloor, err := calibrate(wav)
		if err != nil {
			beckyio.Logf(true, "clip %s: %v (skipped)", c.Stem, err)
			os.Remove(wav)
			continue
		}
		gains[filepath.Base(c.Path)] = gain
		norm, err := normalize(wav, gain)
		if err != nil {
			beckyio.Logf(true, "clip %s: %v (skipped)", c.Stem, err)
			os.Remove(wav)
			continue
		}
		sils, err := silences(norm, noiseFloor, *pause)
		if err != nil {
			beckyio.Logf(true, "clip %s: %v (skipped)", c.Stem, err)
			os.Remove(wav)
			os.Remove(norm)
			continue
		}
		var keeps []span
		if len(cues) > 0 {
			keeps = keepsFromTranscript(cues, sils, *pause)
		} else {
			keeps = keepsFromSilences(sils, c.Duration)
		}
		words := loadWords(out, c.Stem)
		keeps = refineWordEdges(keeps, words)
		keeps = splitOnWordGaps(keeps, words, *pause)
		keeps = adlibRoom(cues, keeps)
		keeps = vadSanity(norm, keeps, *vadThreshold, *vadSpeechPct, *verbose)
		os.Remove(norm)

		takes := detectBadTakes(cues)
		for _, bt := range takes {
			if bt.Confident {
				keeps = subtract(keeps, span{bt.Start, bt.End})
				for i := bt.FirstCue; i <= bt.LastCue; i++ {
					cutAsRetake = append(cutAsRetake, qaCue{c.Stem, cues[i].Start, cues[i].End, cues[i].Text})
				}
			} else {
				pendingMarkers = append(pendingMarkers, pendingMarker{
					Source: c.Stem,
					T:      bt.Start,
					TEnd:   bt.End,
					Title:  fmt.Sprintf("RETAKE? %s: %s", c.Stem, cueText(cues, bt.FirstCue, bt.LastCue)),
					Kind:   "retake",
				})
			}
		}

		// snap, then rescue whatever the snaps and retake-cuts left uncovered
		// (rescueMissedCues trims its OWN padding to real words internally -
		// see its doc comment for why the whole list is never re-refined
		// here), then snap again so rescued boundaries also land on quiet
		// crossings.
		keeps = snapKeeps(wav, c, keeps, out, *verbose)
		keeps = rescueMissedCues(cues, keeps, words)
		keeps = snapKeeps(wav, c, keeps, out, *verbose)
		os.Remove(wav)

		speaking := loadSpeaking(out, c)
			for _, cut := range speakingConfidentCuts(keeps, speaking, words) {
				keeps = subtract(keeps, cut)
			}
		pendingMarkers = append(pendingMarkers, speakingCorroboration(c.Stem, keeps, speaking)...)
		rcCut, rcMark := 0, 0
		for _, bt := range takes {
			if bt.Confident {
				rcCut++
			} else {
				rcMark++
			}
		}
		summaries[c.Stem] = writeDossier(out, c, gain, cues, keeps, speaking, rcCut, rcMark)

		for _, k := range keeps {
			events = append(events, event{Source: c.Path, In: k.Start, Out: k.End, Dialogue: dialogueOver(cues, k)})
			totalKeep += k.End - k.Start
		}

		// QA: a cue is dropped when its WORDS are gone - onset or offset not
		// covered by any keep - and it was not cut as a confident retake. A cue
		// split by an intentional mid-sentence jump cut still has both ends.
		for _, cue := range cues {
			if len(strings.Fields(cue.Text)) < 3 {
				continue
			}
			if cueInConfidentTake(cue, takes) {
				continue
			}
			if !wordsCovered(cue, keeps) {
				dropped = append(dropped, qaCue{c.Stem, cue.Start, cue.End, cue.Text})
			}
		}
	}

	// Assemble the timeline: events in clip order are already chronological.
	tl := 0.0
	for i := range events {
		events[i].TLStart = tl
		tl += events[i].Out - events[i].In
	}

	// Markers: caller-supplied (quotes) mapped from source time to timeline.
	if *markersIn != "" {
		var mi struct {
			Markers []markerIn `json:"markers"`
		}
		b, rErr := os.ReadFile(*markersIn)
		if rErr != nil {
			beckyio.Fatalf("read markers: %v", rErr)
		}
		if jErr := json.Unmarshal(b, &mi); jErr != nil {
			beckyio.Fatalf("parse markers: %v", jErr)
		}
		for _, m := range mi.Markers {
			if t, ok := mapToTimeline(events, m.Source, m.SourceTime); ok {
				markers = append(markers, markerOut{t, m.Title})
			} else {
				beckyio.Logf(true, "marker has no home on the timeline (lead-in cut away?): %s", m.Title)
				markers = append(markers, markerOut{tl, m.Title + " [lead-in was cut - review]"})
			}
		}
	}
	var placedPending []pendingMarker
	for _, pm := range pendingMarkers {
		if t, ok := mapToTimeline(events, pm.Source, pm.T); ok {
			markers = append(markers, markerOut{t, pm.Title})
			pm.TL = t
			placedPending = append(placedPending, pm)
		}
	}
	sort.SliceStable(markers, func(i, j int) bool { return markers[i].T < markers[j].T })

	// Sequential quote insertion: the main edit stops, the quote plays on
	// its own tracks, the main edit resumes - the listener hears one thing at
	// a time. Without -quotes the layout is the plain four-track-less cut.
	lay := spliceLayout(events, markers, nil)
	if *quotesIn != "" {
		qs, qErr := loadQuotes(*quotesIn)
		if qErr != nil {
			beckyio.Fatalf("read quotes: %v", qErr)
		}
		qs = burnQuoteOverlays(qs, out, *verbose)
		lay = spliceLayout(events, markers, qs)
	}

	placedPending = reshiftPendingTL(placedPending, markers, lay.Markers)

	finalTL := 0.0
	for _, e := range lay.Events {
		if t := e.TL + (e.Out - e.In); t > finalTL {
			finalTL = t
		}
	}
	for _, q := range lay.Quotes {
		if t := q.TL + (q.Out - q.In); t > finalTL {
			finalTL = t
		}
	}

	if err := writeArtifacts(out, dir, lay, dropped, cutAsRetake, totalKeep, finalTL, gains); err != nil {
		beckyio.Fatalf("%v", err)
	}
	if err := writeLibrary(out, dir, clips, summaries); err != nil {
		beckyio.Fatalf("%v", err)
	}
	if err := writePendingMarkers(out, placedPending); err != nil {
		beckyio.Fatalf("%v", err)
	}

	if *launchVegas {
		if err := launchVegasPro(out, *vegasScript, *verbose); err != nil {
			beckyio.Fatalf("%v", err)
		}
	}

	beckyio.PrintJSON(map[string]any{
		"project":          dir,
		"clips":            len(clips),
		"events":           len(events),
		"timeline_seconds": tl,
		"markers":          len(markers),
		"dropped_cues":     len(dropped),
		"retakes_cut":      len(cutAsRetake),
		"out":              out,
	})
}

func parseCues(c clip) []quotes.Cue {
	if c.SRT == "" {
		return nil
	}
	cues, err := quotes.ParseSRTFile(c.SRT)
	if err != nil {
		beckyio.Logf(true, "clip %s: %v", c.Stem, err)
		return nil
	}
	return cues
}

// adlibRoom gives single-word ad-libs ("What.", "Okay.", "Um.") their wider
// padding (WE_TRIED.md step 5): they are delivered with visual emphasis and
// intentional silence around them, so the default margins would strangle them.
func adlibRoom(cues []quotes.Cue, keeps []span) []span {
	for _, cue := range cues {
		if n := len(strings.Fields(cue.Text)); n < 1 || n > 2 {
			continue
		}
		lo, hi := cue.Start-0.5, cue.End+0.7
		covered := false
		for i, k := range keeps {
			if cue.Start >= k.Start && cue.End <= k.End {
				covered = true
				if lo < keeps[i].Start {
					keeps[i].Start = lo
				}
				if hi > keeps[i].End {
					keeps[i].End = hi
				}
			}
		}
		if !covered {
			keeps = append(keeps, span{lo, hi})
		}
	}
	sort.SliceStable(keeps, func(i, j int) bool { return keeps[i].Start < keeps[j].Start })
	var merged []span
	for _, k := range keeps {
		if len(merged) > 0 && k.Start <= merged[len(merged)-1].End+0.05 {
			if k.End > merged[len(merged)-1].End {
				merged[len(merged)-1].End = k.End
			}
			continue
		}
		merged = append(merged, k)
	}
	return merged
}

// wordsCovered: the first and last 0.25s of a cue (the actual words) each sit
// inside some keep. The middle may be split by an intentional jump cut - a
// pause longer than conversational pace mid-sentence is exactly what this
// tool cuts.
func wordsCovered(cue quotes.Cue, keeps []span) bool {
	head := cue.Start + 0.25
	tail := cue.End - 0.25
	if tail < head {
		head, tail = cue.Start, cue.End
	}
	// short cues get a tight tolerance: a shaved fraction of a half-second
	// cue IS a clipped word, not a jump-cut artifact.
	tol := 0.2
	if cue.End-cue.Start < 1.2 {
		tol = 0.05
	}
	headIn, tailIn := false, false
	for _, k := range keeps {
		if k.Start-tol <= cue.Start && head <= k.End+tol {
			headIn = true
		}
		if k.Start-0.45 <= tail && cue.End <= k.End+tol {
			tailIn = true
		}
	}
	return headIn && tailIn
}

func cueInConfidentTake(cue quotes.Cue, takes []badTake) bool {
	for _, bt := range takes {
		if bt.Confident && cue.Start >= bt.Start-0.05 && cue.End <= bt.End+0.05 {
			return true
		}
	}
	return false
}

// inventory finds the source videos and orders them by EMBEDDED creation_time
// (WE_TRIED.md: filesystem times are lies; the container tag is ground truth).
func inventory(dir string) ([]clip, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	cfg := config.Load()
	var clips []clip
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext != ".mp4" && ext != ".mov" && ext != ".mkv" && ext != ".avi" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		mi, pErr := mediainfo.Probe(cfg.FFprobe, path)
		if pErr != nil || mi.Duration < 1 {
			continue
		}
		stem := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		c := clip{
			Path: path, Stem: stem,
			Duration: mi.Duration, FPS: mi.FPS,
			Width: mi.Width, Height: mi.Height,
			CreationTime: creationTime(cfg.FFprobe, path),
		}
		for _, cand := range []string{stem + ".srt", stem + ".SRT", stem + "_parakeet_transcription.srt"} {
			if _, sErr := os.Stat(filepath.Join(dir, cand)); sErr == nil {
				c.SRT = filepath.Join(dir, cand)
				break
			}
		}
		clips = append(clips, c)
	}
	if len(clips) == 0 {
		return nil, fmt.Errorf("no source videos found in %s", dir)
	}
	sort.SliceStable(clips, func(i, j int) bool { return clips[i].CreationTime < clips[j].CreationTime })
	return clips, nil
}

// creationTime reads the container's creation_time tag. Missing tag sorts the
// clip LAST rather than inventing an order.
func creationTime(ffprobe, path string) string {
	cmd := exec.Command(ffprobe, "-v", "error", "-show_entries", "format_tags=creation_time", "-of", "default=nw=1", path)
	proc.NoWindow(cmd)
	outB, err := cmd.Output()
	if err != nil {
		return "9999" + filepath.Base(path)
	}
	for _, line := range strings.Split(string(outB), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "TAG:creation_time="); ok && v != "" {
			return v
		}
	}
	return "9999" + filepath.Base(path)
}

// snapKeeps moves every keep boundary onto a quiet zero-crossing of the
// ORIGINAL audio (zcross.go) and writes the per-clip audio profile. Degrade,
// never delete: no decode, no snap, boundaries stay where detection put them.
func snapKeeps(wav string, c clip, keeps []span, out string, verbose bool) []span {
	au, err := sampledecode.DecodeWAVFile(wav)
	if err != nil {
		beckyio.Logf(true, "%s: wav decode failed (%v) - boundaries unsnapped", c.Stem, err)
		return keeps
	}
	quietDB := roomTone(au) + 6
	out2 := make([]span, len(keeps))
	for i, k := range keeps {
		s, e := k.Start, k.End
		if v, ok := snapBoundary(au.Samples, au.SampleRate, s, quietDB); ok {
			s = v
		}
		if v, ok := snapBoundary(au.Samples, au.SampleRate, e, quietDB); ok {
			e = v
		}
		if e <= s { // a snap must never invert a span
			s, e = k.Start, k.End
		}
		out2[i] = span{s, e}
	}
	prof, _ := json.MarshalIndent(map[string]any{
		"source": c.Path, "sample_rate": au.SampleRate, "room_db": quietDB - 6,
	}, "", "  ")
	os.WriteFile(filepath.Join(out, c.Stem+".audio_profile.json"), prof, 0o644)
	beckyio.Logf(verbose, "%s: %d keeps snapped at room %.1f dB", c.Stem, len(keeps), quietDB-6)
	return out2
}

// roomTone is the p10 of the 0.1s RMS envelope: the noise floor this clip's
// zero-crossing snap judges "quiet" against.
func roomTone(au *sampledecode.Audio) float64 {
	win := au.SampleRate / 10
	if win < 1 {
		win = 1
	}
	var env []float64
	for i := 0; i < len(au.Samples); i += win {
		end := i + win
		if end > len(au.Samples) {
			end = len(au.Samples)
		}
		var sum float64
		for j := i; j < end; j++ {
			v := float64(au.Samples[j])
			sum += v * v
		}
		env = append(env, dbfs(math.Sqrt(sum/float64(end-i))))
	}
	sort.Float64s(env)
	if len(env) == 0 {
		return -60
	}
	return env[len(env)/10]
}

func subtract(keeps []span, cut span) []span {
	var out []span
	for _, k := range keeps {
		switch {
		case cut.End <= k.Start || cut.Start >= k.End:
			out = append(out, k)
		default:
			if cut.Start > k.Start {
				out = append(out, span{k.Start, cut.Start})
			}
			if cut.End < k.End {
				out = append(out, span{cut.End, k.End})
			}
		}
	}
	return out
}

func dialogueOver(cues []quotes.Cue, k span) string {
	var parts []string
	for _, c := range cues {
		if c.End > k.Start && c.Start < k.End && c.Text != "" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, " ")
}

func cueText(cues []quotes.Cue, a, b int) string {
	var parts []string
	for i := a; i <= b && i < len(cues); i++ {
		parts = append(parts, cues[i].Text)
	}
	s := strings.Join(parts, " ")
	if len(s) > 120 {
		s = s[:120] + "..."
	}
	return s
}

// mapToTimeline converts a source-file time into assembled-timeline seconds.
// A time that fell into a cut gap maps to the end of the event before it (the
// lead-in is where the quote marker belongs).
//
// Matches on the STEM (extension stripped from both sides): pendingMarkers
// for retakes and speaking-corroboration are built from c.Stem ("HJOC7106"),
// while events[i].Source is the full c.Path ("...\HJOC7106.MP4") - matching
// on the bare basename compared "hjoc7106" against "hjoc7106.mp4" and NEVER
// matched, so every retake-marker and corroboration-marker silently vanished
// before landing on the timeline (measured 2026-08-24 night: `markers` never
// moved off the count baked into the static markers.json, no matter how many
// pendingMarkers were generated).
func mapToTimeline(events []event, source string, t float64) (float64, bool) {
	base := stemOf(source)
	var prevEnd *float64
	for i := range events {
		if stemOf(events[i].Source) != base {
			continue
		}
		if t >= events[i].In && t <= events[i].Out {
			return events[i].TLStart + (t - events[i].In), true
		}
		if t < events[i].In && prevEnd != nil {
			return *prevEnd, true
		}
		e := events[i].TLStart + (events[i].Out - events[i].In)
		prevEnd = &e
	}
	if prevEnd != nil {
		return *prevEnd, true
	}
	return 0, false
}

// stemOf is the lowercased basename with its extension removed, so a marker
// source built from a bare stem ("HJOC7106") matches an event source built
// from a full path ("...\HJOC7106.MP4") - see mapToTimeline's doc comment.
func stemOf(p string) string {
	b := strings.ToLower(filepath.Base(p))
	return strings.TrimSuffix(b, filepath.Ext(b))
}
