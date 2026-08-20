// jumpcuts.go makes becky-short render a window AS BECKY-CUT WOULD HAVE CUT
// IT — dead air removed, kept spans butted together — instead of one
// unbroken continuous take.
//
// It NEVER reimplements becky-cut's silence/VAD detection: it shells out to
// `becky-cut <video> --dry-run` (decide only, nothing rendered or written —
// see cmd/cut/main.go and the identical pattern in cmd/clip/autocut.go) and
// intersects the "keep" decisions with this short's window. becky-cut
// analyses the WHOLE source file regardless of the window asked for, so the
// call is cached per source (cutCache) and never repeated per short.
//
// Render architecture: one ffmpeg INPUT per kept span (mirroring
// internal/reel's proven multi-clip concat — see renderJumpcutShort), each
// with its own local, 0-based crop path and its own frame-quantised trim, then
// concatenated. Captions are built in ONE subs.Build call across every span,
// so they land on the CONCATENATED (cut) output timeline for free — that is
// exactly what internal/subs.Segment already means ("one kept span of a
// source on the output timeline").
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"becky-go/internal/config"
	"becky-go/internal/crop"
	"becky-go/internal/mediainfo"
	"becky-go/internal/shotcut"
	"becky-go/internal/subs"
	"becky-go/internal/transcribex"
)

// jumpcutNoopEps is how much removed time counts as "nothing to cut". A
// becky-cut decision boundary can land a few milliseconds inside the window
// from float rounding; that is not a jumpcut, it's noise, and routing it
// through the whole multi-input concat machinery for no visible change would
// just be a slower way to render the same continuous take.
const jumpcutNoopEps = 0.05

// edgeSliverSeconds is how short a FIRST or LAST span may be before an
// unframeable one is dropped instead of refusing the whole short. The window's
// edges land part-way through a shot, so a fragment of a few frames there is a
// boundary artefact, not a shot Jordan chose. Interior spans never qualify.
const edgeSliverSeconds = 0.5

// jumpcutMinSpan drops a keep-span shorter than this AFTER intersecting with
// the window (an edge span becky-cut kept can be clipped down to a sliver by
// the window boundary). Too short to be a real beat of speech; its time is
// still counted as removed.
const jumpcutMinSpan = 0.15

// keepSpan is one KEPT span of a source, in the source's own seconds —
// becky-cut's "keep" decision, the unit becky-short jumpcuts on.
type keepSpan struct {
	In, Out float64
}

// jumpcutPlan is the pacing decision for one short: which spans of the window
// survive the cut, and how much time that removes.
type jumpcutPlan struct {
	Spans          []keepSpan
	RemovedSeconds float64
	// ExistingCuts/PreservedCuts/Cuts are only set by planShotSpans, when the
	// SOURCE ITSELF already carries hard cuts inside the window (Finding 1,
	// research/jordan-edit-reverse-engineered.md) — zero/nil for the plain
	// becky-cut silence path (planJumpcuts), which has no notion of a "shot".
	ExistingCuts  int
	PreservedCuts int
	Cuts          []float64
	// RescuedWords is how many words the silence threshold cut and the
	// transcript put back (raw-footage path only). Reported, never hidden: a
	// non-zero value means becky-cut's threshold disagreed with what was
	// actually said, and Jordan should be able to see that it did.
	RescuedWords int
	// ProtectedEdges is how many span edges the word guard had to pull back
	// because the tightening would have cut through speech (already-edited
	// path only — wordguard.go). Same honesty rule as RescuedWords: a non-zero
	// value means the trim and the transcript disagreed, and the transcript won.
	ProtectedEdges int
}

// planJumpcuts intersects becky-cut's WHOLE-FILE keep decisions (cached per
// source in cache) with j's window, dropping any resulting sliver shorter
// than jumpcutMinSpan. Pure once cache is populated — no I/O — so it is
// unit-tested directly against a canned cache.
//
// This is the RAW-FOOTAGE path: no existing cuts, so becky-cut's own silence
// threshold decides where the cuts go. planPacing only reaches this when
// shotcut.Detect found nothing to preserve.
func planJumpcuts(cache *cutCache, j job) (jumpcutPlan, error) {
	all, err := cache.wholeFileSpans(j.Src)
	if err != nil {
		return jumpcutPlan{}, err
	}
	windowDur := j.Out - j.In
	var kept []keepSpan
	var keptDur float64
	for _, s := range all {
		in, out := math.Max(s.In, j.In), math.Min(s.Out, j.Out)
		if out-in < jumpcutMinSpan {
			continue
		}
		kept = append(kept, keepSpan{In: in, Out: out})
		keptDur += out - in
	}
	return jumpcutPlan{Spans: kept, RemovedSeconds: windowDur - keptDur}, nil
}

// defaultTighten is Jordan's OWN measured tightening rate on already-edited
// footage — 150ms per cut, not a raw-footage silence threshold
// (research/jordan-edit-reverse-engineered.md, "Finding 2": he removes 10.3%
// of a 32s span across 22 cuts; becky-cut --dry-run alone removed 51% of a
// raw window, the wrong instinct entirely once the source is already cut).
// Used only where becky-cut finds no real dead air right at a given
// boundary (boundaryTighten); --tighten overrides it.
const defaultTighten = 0.15

// tightenSearchRadius is how far from an existing cut boundary boundaryTighten
// looks for a becky-cut REMOVE (dead-air) decision to derive the trim from
// REAL silence at that exact cut, instead of always applying the flag
// default blindly.
const tightenSearchRadius = 0.4

// boundaryTighten returns the TOTAL trim at one existing-cut boundary: the
// length of a becky-cut REMOVE span found within tightenSearchRadius of the
// boundary — real dead air, trimmed exactly, capped so a long silence
// elsewhere in the shot can't swallow the whole cut — else flagDefault.
// Split in half by the caller: half comes off the end of the earlier span,
// half off the start of the later one, so the boundary itself is centred.
func boundaryTighten(removeSpans []keepSpan, boundary, flagDefault float64) float64 {
	for _, rs := range removeSpans {
		if rs.Out < boundary-tightenSearchRadius || rs.In > boundary+tightenSearchRadius {
			continue
		}
		d := rs.Out - rs.In
		if d <= 0 {
			continue
		}
		if capAt := flagDefault * 4; d > capAt {
			d = capAt
		}
		return d
	}
	return flagDefault
}

// planShotSpans builds a jumpcutPlan for a window where the SOURCE ITSELF
// already carries hard cuts (Finding 1, research/jordan-edit-reverse-engineered.md):
// Jordan did not choose most of his cuts, he inherited them — 11 of 14
// confidently-aligned cuts in his own vertical land within 100ms of a cut
// that already existed in the master, 8 of them frame-exact. The existing
// shot boundaries become the span boundaries, PRESERVED AS-IS; becky-cut is
// used only to TIGHTEN a small amount at each one (boundaryTighten), never
// to invent a new cut list or apply a raw-footage silence threshold.
//
// cuts are source-absolute seconds inside (j.In, j.Out) (internal/shotcut).
// Pure — no I/O — given cuts, removeSpans and words already resolved.
//
// words are the source's word-level timings over this window and are the HARD
// GUARD on every trim: a boundary may tighten into silence freely and may never
// tighten into speech (wordguard.go). Pass nil only when no transcript exists —
// then the old, unguarded behaviour applies and the caller says so.
func planShotSpans(cuts []float64, removeSpans []keepSpan, j job, tighten float64, words []subs.Word) jumpcutPlan {
	bounds := []float64{j.In}
	existing := 0
	for _, c := range cuts {
		if c <= j.In || c >= j.Out {
			continue
		}
		existing++
		// A cut too close to the window's own edge cannot be a usable span
		// boundary — the resulting sliver span would be dropped anyway
		// (jumpcutMinSpan), so it never becomes one.
		if c > j.In+jumpcutMinSpan && c < j.Out-jumpcutMinSpan {
			bounds = append(bounds, c)
		}
	}
	bounds = append(bounds, j.Out)

	var spans []keepSpan
	var removed float64
	protected := 0
	for i := 0; i < len(bounds)-1; i++ {
		in, out := bounds[i], bounds[i+1]
		if i > 0 {
			// Tighten the START of this span, then CLAMP: the trim may eat
			// silence, never a word. Without this the half-trim derived from a
			// silence lying entirely on the OTHER side of the boundary shears
			// the first word off every shot — the bug that made the render
			// unusable.
			want := in + boundaryTighten(removeSpans, bounds[i], tighten)/2
			got := clampInToWords(want, in, words)
			if got < want {
				protected++
			}
			removed += got - in
			in = got
		}
		if i < len(bounds)-2 {
			want := out - boundaryTighten(removeSpans, bounds[i+1], tighten)/2
			got := clampOutToWords(want, out, words)
			if got > want {
				protected++
			}
			removed += out - got
			out = got
		}
		if out-in < jumpcutMinSpan {
			continue
		}
		spans = append(spans, keepSpan{In: in, Out: out})
	}
	return jumpcutPlan{Spans: spans, RemovedSeconds: removed, ExistingCuts: existing,
		PreservedCuts: len(bounds) - 2, Cuts: cuts, ProtectedEdges: protected}
}

// planPacing decides how to pace one short's window: if the SOURCE already
// carries hard cuts inside it, PRESERVE them (planShotSpans); otherwise fall
// back to becky-cut's own silence-based jumpcuts exactly as before
// (planJumpcuts) — raw footage with no existing edit is the other real case,
// and it must render unchanged.
//
// Shot detection runs over the WHOLE source once and is cached, then filtered to
// this window - it is NOT scoped to [j.In,j.Out].
//
// That is a correctness requirement, not an optimisation. The cut threshold is
// derived from the footage's own diff distribution (median + 6*MAD), so a short
// window of mostly-static footage lowers it and lets motion through that a
// whole-file scan rejects. Measured on test-for-clips.mp4: the whole file
// reports ZERO cuts, while the window [102.40,138.72] reported two - 125.967 and
// 126.100, 133 milliseconds apart - and the frames show one continuous shot with
// his hand sweeping close to the lens. The same footage must not be classified
// differently depending on where a moment happened to start.
//
// A detection failure (ffmpeg missing, etc.) degrades to the raw-footage path
// rather than failing the render.
func planPacing(cfg config.Config, cache *cutCache, j job, tighten float64, logf transcribex.Logf) (jumpcutPlan, error) {
	all, err := cache.wholeFileCuts(cfg, j.Src)
	cuts := cutsWithin(all, j.In, j.Out)
	if err != nil || len(cuts) == 0 {
		plan, perr := planJumpcuts(cache, j)
		if perr != nil {
			return plan, perr
		}
		// RAW-FOOTAGE PATH ONLY. The shot path preserves boundaries the source
		// already has and only tightens at them, so it cannot swallow a word;
		// this path is the one driven by an absolute silence threshold, and on
		// quiet footage that threshold deletes speech. See wordrescue.go for the
		// measurement — 6.5 seconds of Jordan talking, cut from a window with no
		// silence in it at all.
		words, _, wErr := transcribex.EnsureWords(j.Src, logf)
		if wErr != nil || len(words) == 0 {
			return plan, nil // no transcript to check against; leave the plan alone
		}
		before := spansDuration(plan.Spans)
		spans, rescued := rescueWords(plan.Spans, words, j.In, j.Out, wordRescuePad)
		if rescued > 0 {
			plan.Spans = spans
			plan.RemovedSeconds -= spansDuration(spans) - before
			plan.RescuedWords = rescued
		}
		return plan, nil
	}
	// ALREADY-EDITED PATH. The boundaries themselves are Jordan's (inherited
	// from the master) and are preserved exactly; only the TIGHTENING at each
	// one is ours, and it is the thing that was cutting his words off. Read the
	// word timings so the guard in wordguard.go can clamp every trim to silence.
	// A missing transcript degrades to the old unguarded behaviour rather than
	// failing the render — and planPacing's caller reports that it did.
	words, _, wErr := transcribex.EnsureWords(j.Src, logf)
	if wErr != nil {
		words = nil
	}
	return planShotSpans(cuts, cache.wholeFileRemoveSpans(j.Src), j, tighten, words), nil
}

// cutsWithinSpan filters cuts to the ones strictly INSIDE (in,out) — the ones
// this one span's own crop.Run call must reset the smoother at. Normally
// empty: planShotSpans already puts a span boundary at every usable cut, so
// a span IS one shot. Kept as a real filter (not assumed empty) so a span
// that for any reason still spans more than one shot is still handled
// correctly rather than silently smoothing across it.
func cutsWithinSpan(cuts []float64, in, out float64) []float64 {
	var inside []float64
	for _, c := range cuts {
		if c > in && c < out {
			inside = append(inside, c)
		}
	}
	return inside
}

// cutCache memoises becky-cut's whole-file --dry-run decisions (and any
// failure to get them) per source path, so a --reel job cutting several
// shorts from the same source runs becky-cut exactly once for it — becky-cut
// always analyses the whole file, never just a window. Both KEEP and REMOVE
// (dead-air) decisions are cached from the same run: keep spans drive the
// raw-footage jumpcut path (planJumpcuts), remove spans inform how much to
// TIGHTEN at an existing shot boundary (planShotSpans/boundaryTighten) —
// never a second becky-cut invocation for the second use.
type cutCache struct {
	spans       map[string][]keepSpan
	removeSpans map[string][]keepSpan
	errs        map[string]error
	shotCuts    map[string][]float64
}

func newCutCache() *cutCache {
	return &cutCache{spans: map[string][]keepSpan{}, removeSpans: map[string][]keepSpan{},
		errs: map[string]error{}, shotCuts: map[string][]float64{}}
}

func (c *cutCache) wholeFileSpans(src string) ([]keepSpan, error) {
	if s, ok := c.spans[src]; ok {
		return s, nil
	}
	if err, ok := c.errs[src]; ok {
		return nil, err
	}
	keep, remove, err := c.compute(src)
	if err != nil {
		c.errs[src] = err
		return nil, err
	}
	c.spans[src] = keep
	c.removeSpans[src] = remove
	return keep, nil
}

// wholeFileRemoveSpans is the dead-air twin of wholeFileSpans, for tightening
// near an existing cut. Best-effort: a source becky-cut can't decide for
// (not built, timed out, ...) just returns nil, and the caller falls back to
// the flag default tighten amount rather than failing the whole render —
// tightening is a refinement on top of Part A's cuts, not a requirement of
// them.
func (c *cutCache) wholeFileRemoveSpans(src string) []keepSpan {
	if r, ok := c.removeSpans[src]; ok {
		return r
	}
	keep, remove, err := c.compute(src)
	if err != nil {
		c.errs[src] = err
		return nil
	}
	c.spans[src] = keep
	c.removeSpans[src] = remove
	return remove
}

func (c *cutCache) compute(src string) (keep, remove []keepSpan, err error) {
	bin, ok := resolveCutBin()
	if !ok {
		return nil, nil, fmt.Errorf("becky-cut not found — build it with build-all-tools.bat (or set BECKY_CUT to its path)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), cutTimeout())
	defer cancel()
	out, err := runBeckyCutDryRun(ctx, bin, src)
	if err != nil {
		return nil, nil, err
	}
	return parseCutDecisions(out)
}

// cutTimeout bounds one becky-cut --dry-run exec. Detection on a long video
// can take a while even with no render; BECKY_CUT_TIMEOUT (a Go duration like
// "20m") overrides it — same env var cmd/clip's autocut.go honours.
func cutTimeout() time.Duration {
	if d := strings.TrimSpace(os.Getenv("BECKY_CUT_TIMEOUT")); d != "" {
		if parsed, err := time.ParseDuration(d); err == nil && parsed > 0 {
			return parsed
		}
	}
	return 15 * time.Minute
}

// runBeckyCutDryRun runs `becky-cut <videoPath> --dry-run`, which only
// DECIDES and renders/writes nothing.
func runBeckyCutDryRun(ctx context.Context, cutBin, videoPath string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, cutBin, videoPath, "--dry-run")
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("becky-cut failed: %w\n%s", err, tail(errBuf.String()))
	}
	return out, nil
}

// resolveCutBin finds the becky-cut executable: BECKY_CUT env -> next to the
// running exe -> PATH. Same order and same degrade-to-("",false) contract as
// cmd/clip/autocut.go's resolveCutBin (a separate `main` package can't import
// that one, so this is the small, deliberately duplicated twin).
func resolveCutBin() (string, bool) {
	if p := strings.TrimSpace(os.Getenv("BECKY_CUT")); p != "" {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if fileExistsAt(p) {
			return p, true
		}
		return "", false
	}
	if exe, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(exe), cutExeName())
		if fileExistsAt(cand) {
			return cand, true
		}
	}
	if p, err := exec.LookPath("becky-cut"); err == nil {
		return p, true
	}
	return "", false
}

func cutExeName() string {
	if runtime.GOOS == "windows" {
		return "becky-cut.exe"
	}
	return "becky-cut"
}

func fileExistsAt(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

// cutDecisionReport is the subset of becky-cut's --dry-run JSON this parser
// needs — see cmd/cut/main.go's decisions(). Everything else (codec, fps,
// vad_applied, ...) is intentionally ignored.
type cutDecisionReport struct {
	Decisions []struct {
		Status string  `json:"status"`
		Start  float64 `json:"start"`
		End    float64 `json:"end"`
	} `json:"decisions"`
}

// parseCutDecisions parses becky-cut's --dry-run stdout into its "keep" and
// "cut" (removed dead-air) spans, in order. PURE. An unparseable payload is an
// error; the caller (cutCache) degrades by caching it, never by crashing.
func parseCutDecisions(stdout []byte) (keep, remove []keepSpan, err error) {
	var rep cutDecisionReport
	if err := json.Unmarshal(stdout, &rep); err != nil {
		return nil, nil, fmt.Errorf("unexpected becky-cut output: %w", err)
	}
	for _, d := range rep.Decisions {
		sp := keepSpan{In: d.Start, Out: d.End}
		if d.Status == "keep" {
			keep = append(keep, sp)
		} else {
			remove = append(remove, sp)
		}
	}
	return keep, remove, nil
}

// renderJumpcutShort renders a short from N kept spans instead of one
// continuous window: one ffmpeg input per span (-ss/-t exactly like
// internal/reel's per-clip inputs), each cropped/scaled with its OWN local
// (span-relative, 0-based) camera path so there is no cross-cut timeline
// remapping to get wrong, then concatenated.
//
// Frame-count quantisation (framesForSpan) is COPIED from
// internal/reel/args.go's framesFor — that function is unexported so a
// different `main` package cannot call it, but the math must match exactly:
// a real 88-clip reel drifted +1.27s because ffmpeg's own -ss/-t/fps rounding
// at each boundary doesn't reliably land on a whole frame count. Forcing each
// span's OUTPUT frame count independently of that rounding is what fixes it,
// here as much as there.
//
// Captions are ONE subs.Build call across every span (captionSRTJumpcut),
// which is what makes them land on the CUT timeline instead of the original
// continuous window — see internal/subs.Segment's doc comment.
//
// cuts are the existing shot boundaries this plan was built from (nil for
// the raw-footage/becky-cut-only path). Each span already gets its OWN
// crop.Run call below with cuts filtered to strictly inside that one span
// (cutsWithinSpan) — normally empty, since planShotSpans already puts a span
// boundary at every usable cut, so a span already IS one shot; still passed
// through rather than assumed, so the smoother never blends across a cut a
// span happens to still contain (Finding 2, research/jordan-edit-reverse-engineered.md).
func renderJumpcutShort(cfg config.Config, j job, spans []keepSpan, cuts []float64, res shortOut, asp float64, outW, outH int,
	sampleFPS, minCov, maxGap float64, forceCenter, focalPoint, withCaptions bool, capStyle string, asig *audioSigCache, verbose bool) (shortOut, error) {

	info, err := mediainfo.Probe(cfg.FFprobe, j.Src)
	if err != nil || info.FPS <= 0 {
		return res, fmt.Errorf("could not read frame rate of %s (needed to quantise the cuts): %v", filepath.Base(j.Src), err)
	}
	fps := info.FPS

	workDir, err := os.MkdirTemp("", "becky-short-jc-")
	if err != nil {
		return res, err
	}
	defer os.RemoveAll(workDir)

	aspectStr := fmt.Sprintf("%d:%d", outW, outH)
	// segmentReadPad, copied from internal/reel/args.go: headroom on each
	// input's -t read window so the per-span filter chain always has enough
	// decoded frames on hand for trim=end_frame to cut EXACTLY the target
	// count; the padding itself is discarded by that trim.
	pad := 6.0 / fps

	var staticRect crop.Rect
	haveStatic := false
	staticChain := func() (string, error) {
		if !haveStatic {
			w, h, err := probeSize(j.Src)
			if err != nil {
				return "", err
			}
			staticRect = crop.StaticCenter(w, h, asp)
			haveStatic = true
		}
		r := staticRect
		return fmt.Sprintf("crop=%d:%d:%d:%d,scale=%d:%d:flags=lanczos", r.W, r.H, r.X, r.Y, outW, outH), nil
	}

	// A short must not END on footage with nothing to look at. --review caught
	// exactly that on the first two shorts the one-click chain produced; see
	// deadtail.go. A face-less shot mid-clip is deliberate (RULE 4) and is left
	// alone - only the tail is trimmed, and only while it fails to track.
	var (
		inputArgs                []string
		chains                   []string
		vLabels, aLabels         []string
		totalSampled, totalFound int
		allFollowed              = true
		notes                    []string
		degraded                 int
		dropped                  []int
	)
	if !forceCenter {
		trimmed, droppedSec, droppedSpans := trimDeadTail(cfg, j, spans, aspectStr, sampleFPS, minCov, maxGap, cuts)
		if droppedSpans > 0 {
			spans = trimmed
			res.RemovedSeconds += droppedSec
			notes = append(notes, fmt.Sprintf("trimmed %.2fs of dead tail (%d span(s) at the end with no "+
				"trackable subject) so the short does not end on nothing", droppedSec, droppedSpans))
		}
	}

	// PASS 1 — DECIDE THE FRAMING FOR EVERY SPAN BEFORE RENDERING ANY OF IT.
	// Separated from the render loop below because a span can turn out to be
	// unframeable, and dropping one has to happen BEFORE spans is used to build
	// the concat graph and the caption timeline (both index it directly).
	crops := make([]cropResult, 0, len(spans))
	kept := make([]keepSpan, 0, len(spans))
	for i, sp := range spans {
		cr, err := resolveCrop(cfg, j.Src, sp.In, sp.Out, aspectStr, sampleFPS, minCov, maxGap, forceCenter,
			cutsWithinSpan(cuts, sp.In, sp.Out))
		if err != nil {
			// THE SPAN COULD NOT BE FRAMED, and by the time we are here
			// resolveCrop has ALREADY asked the grounded detector what the shot
			// is about (groundaim.go). So this is not "the pose tracker gave
			// up" — it is "nothing in this footage could be named or located".
			//
			// This used to re-run resolveCrop with forceCenter=true and carry
			// on. That is the code path Jordan is describing: "simply failing to
			// crop and leaving it in the center screen (which is essentially
			// nonsense) for some of the crops - we can't have that" (2026-08-20).
			// A short with one arbitrary centre span is, in his words, in the
			// recycle bin — so shipping it is worth less than not shipping it.
			//
			// becky-moment offers ten candidates. Losing one to an honest
			// refusal leaves nine; shipping a centred one loses his trust in all
			// ten. Refuse, and name the timestamp so it is checkable.
			//
			// THE ONE EXCEPTION: A SLIVER AT THE WINDOW EDGE IS NOT A SHOT. The
			// window's start and end land wherever the moment did, usually
			// part-way through a shot, leaving a fragment of a few frames at each
			// end. Measured: a 0.36s fragment at [533.00,533.36] refused the
			// entire burger short. Dropping a fragment costs a third of a second;
			// refusing costs all of it. Only the FIRST and LAST span qualify — a
			// failure in the middle is a real shot, and silently dropping it
			// would jump-cut the footage somewhere Jordan never chose.
			if (i == 0 || i == len(spans)-1) && sp.Out-sp.In < edgeSliverSeconds {
				where := "end"
				if i == 0 {
					where = "start"
				}
				dropped = append(dropped, i)
				res.RemovedSeconds += sp.Out - sp.In
				notes = append(notes, fmt.Sprintf(
					"dropped a %.2fs fragment at the %s of the window that had nothing to frame",
					sp.Out-sp.In, where))
				continue
			}
			return res, fmt.Errorf("kept span %d/%d at [%.2f,%.2f] of the source cannot be framed "+
				"on anything: %w", i+1, len(spans), sp.In, sp.Out, err)
		}
		// A GROUNDED span was not TRACKED, and the two must never be conflated in
		// the coverage number. becky-short once claimed 0.952 on the BLINDFOLD
		// render while an independent face pass over the RENDERED FILE measured
		// 0.18, because degraded spans were left out of both the numerator and
		// the denominator. An object is a legitimate thing to frame; it is still
		// not a tracked subject, so it counts in the denominator only.
		if cr.Grounded {
			degraded++
			cr.Sampled, cr.Found = untrackedSamples(sp.Out-sp.In, fps), 0
			cr.Coverage, cr.Followed = 0, false
		}
		crops = append(crops, cr)
		kept = append(kept, sp)
	}
	if len(dropped) > 0 {
		spans = kept
	}
	if len(spans) == 0 {
		return res, fmt.Errorf("nothing in this window could be framed on anything")
	}

	// PASS 2 — RENDER.
	for i, sp := range spans {
		cr := crops[i]
		totalSampled += cr.Sampled
		totalFound += cr.Found
		if !cr.Followed {
			allFollowed = false
		}
		if cr.Note != "" && !containsNote(notes, cr.Note) {
			notes = append(notes, cr.Note)
		}

		var vchain string
		if len(cr.Rects) > 0 {
			cmdsName := fmt.Sprintf("crop%d.cmds", i)
			if err := os.WriteFile(filepath.Join(workDir, cmdsName), []byte(crop.SendcmdFile(cr.Rects)), 0o644); err != nil {
				return res, err
			}
			vchain = crop.FilterChain(cr.Rects, outW, outH, cmdsName)
		} else {
			vchain, err = staticChain()
			if err != nil {
				return res, err
			}
		}

		n := framesForSpan(sp, fps)
		segDur := float64(n) / fps

		inputArgs = append(inputArgs, "-ss", secondsArg(sp.In), "-t", secondsArg(sp.Out-sp.In+pad), "-i", j.Src)

		vLabel := fmt.Sprintf("[v%d]", i)
		chains = append(chains, fmt.Sprintf("[%d:v]%s,setsar=1,fps=%s,trim=end_frame=%d,format=yuv420p,setpts=PTS-STARTPTS%s",
			i, vchain, fpsArg(fps), n, vLabel))
		vLabels = append(vLabels, vLabel)

		if info.HasAudio {
			aLabel := fmt.Sprintf("[a%d]", i)
			chains = append(chains, fmt.Sprintf(
				"[%d:a]aresample=async=1,aformat=sample_rates=48000:channel_layouts=stereo,atrim=duration=%s,asetpts=PTS-STARTPTS%s",
				i, secondsArg(segDur), aLabel))
			aLabels = append(aLabels, aLabel)
		}
	}

	res.Sampled, res.Found = totalSampled, totalFound
	if totalSampled > 0 {
		res.Coverage = float64(totalFound) / float64(totalSampled)
	}
	res.Followed = allFollowed
	// Refusing a span is honest; refusing the WHOLE short because a minority of
	// spans could not be tracked is not. More than half degraded means the
	// window itself is not a talking-head short, and that IS worth refusing.
	if tooManyDegraded(degraded, len(spans)) {
		return res, fmt.Errorf("%d of %d kept spans had no trackable person - this window is not a talking-head short; pass --center to force a static crop",
			degraded, len(spans))
	}
	if degraded > 0 {
		note(&res, fmt.Sprintf("%d of %d spans had no trackable person, so becky asked what the shot "+
			"is about and framed THAT instead of centring (see the per-span notes)", degraded, len(spans)))
	}

	for _, n := range notes {
		note(&res, n)
	}

	vOut := "[vout]"
	var concat string
	if info.HasAudio {
		var inter strings.Builder
		for i := range spans {
			inter.WriteString(vLabels[i])
			inter.WriteString(aLabels[i])
		}
		// Loudness normalisation goes INSIDE the graph here: this path renders
		// through -filter_complex, and -af cannot be combined with it.
		concat = fmt.Sprintf("%sconcat=n=%d:v=1:a=1%s[araw];[araw]%s[aout]",
			inter.String(), len(spans), vOut, crop.LoudnormFilter)
	} else {
		concat = fmt.Sprintf("%sconcat=n=%d:v=1:a=0%s", strings.Join(vLabels, ""), len(spans), vOut)
	}
	graph := strings.Join(chains, ";") + ";" + concat

	// Captions land on the CUT timeline: one subs.Build call over the kept
	// spans laid end to end (each IS an internal/subs.Segment), so the .srt is
	// already timed to what the concat above actually outputs.
	capCount := 0
	var srtToSave string
	if withCaptions {
		logf := func(f string, a ...any) { logIfShort(verbose, f, a...) }

		var burnFilter, sidecar string
		var n int
		var cerr error
		if capStyle == "jordan" {
			var ass string
			ass, sidecar, n, cerr = captionASSJumpcut(cfg, j.Src, j.In, j.Out, fps, spans, outW, outH, workDir, asig, logf)
			if cerr == nil && n > 0 {
				burnFilter = captionFilterASS(ass)
			}
		} else {
			var srt string
			srt, n, cerr = captionSRTJumpcut(j.Src, j.In, j.Out, fps, spans, workDir, logf)
			sidecar = srt
			if cerr == nil && n > 0 {
				burnFilter = captionFilter(srt, j.Src)
			}
		}
		switch {
		case cerr != nil:
			note(&res, "no captions: "+cerr.Error())
		case n == 0:
			note(&res, "no captions: nothing is said in the kept spans")
		default:
			vSub := "[vsub]"
			graph += ";" + vOut + burnFilter + vSub
			vOut = vSub
			capCount = n
			srtToSave = sidecar
		}
	}
	res.Captions = capCount

	if d := filepath.Dir(j.Dst); d != "" && d != "." {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return res, err
		}
	}

	args := []string{"-y", "-hide_banner", "-loglevel", "error"}
	args = append(args, inputArgs...)
	args = append(args, "-filter_complex", graph, "-map", vOut)
	if info.HasAudio {
		args = append(args, "-map", "[aout]", "-c:a", "aac", "-b:a", "160k")
	} else {
		args = append(args, "-an")
	}
	args = append(args, "-c:v", "libx264", "-preset", "medium", "-crf", "18",
		"-pix_fmt", "yuv420p", "-movflags", "+faststart", j.Dst)

	cmd := exec.Command(ffmpegBin(cfg), args...)
	cmd.Dir = workDir
	var stderr strings.Builder
	if verbose {
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stderr = &stderr
	}
	if err := cmd.Run(); err != nil {
		return res, fmt.Errorf("ffmpeg failed: %v\n%s", err, tail(stderr.String()))
	}
	st, err := os.Stat(j.Dst)
	if err != nil || st.Size() == 0 {
		return res, fmt.Errorf("ffmpeg reported success but wrote no file")
	}
	if srtToSave != "" {
		if err := saveCaptionSidecar(j.Dst, srtToSave); err != nil {
			note(&res, "could not save caption sidecar: "+err.Error())
		}
	}
	return res, nil
}

// framesForSpan quantises a kept span's OUTPUT frame count. COPIED from
// internal/reel/args.go's framesFor (unexported there) — see this file's
// header comment for why that duplication, not an import, is correct here.
func framesForSpan(sp keepSpan, fps float64) int {
	n := int(math.Round((sp.Out - sp.In) * fps))
	if n < 1 {
		n = 1
	}
	return n
}

// secondsArg renders a seconds value for ffmpeg -ss/-t at MICROSECOND
// precision — matching internal/reel/args.go's formatSeconds, which exists
// because millisecond precision can round a frame-exact timestamp past its
// true frame boundary and skip a frame on an accurate -ss seek.
func secondsArg(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	return fmt.Sprintf("%.6f", sec)
}

// fpsArg renders a frame rate for the `fps=` filter as a trimmed decimal.
// internal/reel/escape.go's formatRate additionally special-cases the NTSC
// family as an exact rational (30000/1001); skipped here as a real
// simplification, not an oversight — ffmpeg's fps filter accepts a plain
// decimal too, and the quantisation that actually prevents drift is
// framesForSpan's trim=end_frame, not this string's precision.
func fpsArg(fps float64) string {
	s := fmt.Sprintf("%.6f", fps)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

func containsNote(notes []string, s string) bool {
	for _, n := range notes {
		if n == s {
			return true
		}
	}
	return false
}

// captionCuesJumpcut is captionCues generalised to N kept spans instead of
// one window. The words are windowed to this short first (same capWordPad
// safety margin as the single-segment path — see captionCues' doc comment),
// then subs.Build lays every span end to end, which is what puts the cues on
// the CONCATENATED (cut) output timeline rather than the original continuous
// window.
func captionCuesJumpcut(words []subs.Word, winIn, winOut float64, spans []keepSpan, fps float64) []subs.Cue {
	// Slice to the KEPT spans, not to the whole window.
	//
	// WordsPerSegment rescues a word that overlaps no segment by retiming it onto
	// the nearest cut - right when a reel's clips tile the source, and wrong here
	// twice over: every word becky-cut DELIBERATELY REMOVED overlaps no kept span,
	// so all of it was rescued back in and crammed onto the spans that survived.
	//
	// Measured on a real render (26.16s window, 18.86s of dead air removed, 7.3s
	// out): 16 cues compressed into the first 3.3 seconds, one of them with a
	// ZERO duration (00:00:02,300 --> 00:00:02,300), against audio that runs the
	// full 7.2s. --review caught it - "11/16 burned cues match no words in the
	// rendered audio at all".
	//
	// This is the same rescue bug already fixed for the single-window path
	// (110 captions -> 18); it came back through the jumpcut path.
	words = wordsInSpans(words, spans)
	if len(words) == 0 {
		return nil
	}
	segs := make([]subs.Segment, len(spans))
	for i, sp := range spans {
		segs[i] = subs.Segment{Source: "clip", Start: sp.In, End: sp.Out, Words: words}
	}
	opt := subs.DefaultOptions()
	opt.GapSeconds = subs.AutoGapSeconds(words)
	opt.FPS = fps
	return subs.Build(segs, opt)
}

// captionSRTJumpcut is captionSRT for a jumpcut short: same transcript
// source, cues built across every kept span instead of one window.
func captionSRTJumpcut(video string, winIn, winOut, fps float64, spans []keepSpan, dir string, logf transcribex.Logf) (string, int, error) {
	words, _, err := transcribex.EnsureWords(video, logf)
	if err != nil {
		return "", 0, err
	}
	cues := captionCuesJumpcut(words, winIn, winOut, spans, fps)
	if len(cues) == 0 {
		return "", 0, nil
	}
	return writeSRT(cues, dir)
}

// tooManyDegraded decides when falling back stops being a graceful degrade and
// starts being the wrong window.
//
// A MINORITY of untrackable spans is normal and expected: Jordan's own edit of
// the same footage holds 1.27 seconds on a pointing finger with no face in
// frame at all. A MAJORITY means this window is not a talking-head short, and
// refusing it is the honest answer (FORENSIC-OUTPUT-PHILOSOPHY.md - thirty good
// shorts and ten honest refusals beats forty where six are quietly wrong).
func tooManyDegraded(degraded, total int) bool {
	return total > 0 && degraded*2 > total
}

// untrackedSamples is how many samples a DEGRADED span contributes to the
// coverage denominator. It was never tracked, so every one of them counts as
// "sampled, subject not found".
//
// Returning zero here is what made the reported coverage a lie: with degraded
// spans absent from both the numerator and the denominator, the number described
// only the spans that worked. becky-short claimed 0.952 on the BLINDFOLD render
// while an independent face pass over the RENDERED FILE measured 0.18. Counting
// them honestly gives 0.579.
func untrackedSamples(dur, fps float64) int {
	n := int(dur * fps)
	if n < 1 {
		return 1 // a span short enough to round to zero is still one unframed sample
	}
	return n
}

// wholeFileCuts detects the source's hard cuts ONCE and caches them. See
// planPacing for why this must not be scoped to a window.
func (c *cutCache) wholeFileCuts(cfg config.Config, src string) ([]float64, error) {
	if v, ok := c.shotCuts[src]; ok {
		return v, nil
	}
	cuts, err := shotcut.Detect(shotcut.Options{
		Video: src, FFmpeg: cfg.FFmpeg, FFprobe: cfg.FFprobe,
	})
	if err != nil {
		return nil, err
	}
	c.shotCuts[src] = cuts
	return cuts, nil
}

// cutsWithin returns the cuts strictly inside (in,out).
func cutsWithin(cuts []float64, in, out float64) []float64 {
	var got []float64
	for _, t := range cuts {
		if t > in && t < out {
			got = append(got, t)
		}
	}
	return got
}

// wordsInSpans keeps only the words that actually survive the cut - the ones
// overlapping a kept span, with the same small pad captionSRT uses for
// Parakeet's clock drift against the cut points.
func wordsInSpans(words []subs.Word, spans []keepSpan) []subs.Word {
	var out []subs.Word
	for _, w := range words {
		for _, sp := range spans {
			if w.End > sp.In-capWordPad && w.Start < sp.Out+capWordPad {
				out = append(out, w)
				break
			}
		}
	}
	return out
}
