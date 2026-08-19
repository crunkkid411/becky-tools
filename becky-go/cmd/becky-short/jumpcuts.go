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
// Pure — no I/O — given cuts and removeSpans already resolved.
func planShotSpans(cuts []float64, removeSpans []keepSpan, j job, tighten float64) jumpcutPlan {
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
	for i := 0; i < len(bounds)-1; i++ {
		in, out := bounds[i], bounds[i+1]
		if i > 0 {
			trim := boundaryTighten(removeSpans, bounds[i], tighten) / 2
			in += trim
			removed += trim
		}
		if i < len(bounds)-2 {
			trim := boundaryTighten(removeSpans, bounds[i+1], tighten) / 2
			out -= trim
			removed += trim
		}
		if out-in < jumpcutMinSpan {
			continue
		}
		spans = append(spans, keepSpan{In: in, Out: out})
	}
	return jumpcutPlan{Spans: spans, RemovedSeconds: removed, ExistingCuts: existing,
		PreservedCuts: len(bounds) - 2, Cuts: cuts}
}

// planPacing decides how to pace one short's window: if the SOURCE already
// carries hard cuts inside it, PRESERVE them (planShotSpans); otherwise fall
// back to becky-cut's own silence-based jumpcuts exactly as before
// (planJumpcuts) — raw footage with no existing edit is the other real case,
// and it must render unchanged.
//
// Shot detection is scoped to just this window (shotcut.Detect decodes only
// [j.In,j.Out], unlike becky-cut it is cheap enough to run per job with no
// cache), so a detection failure (ffmpeg missing, etc.) degrades to the
// raw-footage path rather than failing the render.
func planPacing(cfg config.Config, cache *cutCache, j job, tighten float64) (jumpcutPlan, error) {
	cuts, err := shotcut.Detect(shotcut.Options{
		Video: j.Src, Start: j.In, End: j.Out, FFmpeg: cfg.FFmpeg, FFprobe: cfg.FFprobe,
	})
	if err != nil || len(cuts) == 0 {
		return planJumpcuts(cache, j)
	}
	return planShotSpans(cuts, cache.wholeFileRemoveSpans(j.Src), j, tighten), nil
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
}

func newCutCache() *cutCache {
	return &cutCache{spans: map[string][]keepSpan{}, removeSpans: map[string][]keepSpan{}, errs: map[string]error{}}
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
	sampleFPS, minCov, maxGap float64, forceCenter, withCaptions, verbose bool) (shortOut, error) {

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

	var (
		inputArgs                []string
		chains                   []string
		vLabels, aLabels         []string
		totalSampled, totalFound int
		allFollowed              = true
		notes                    []string
		degraded                 int
	)

	for i, sp := range spans {
		cr, err := resolveCrop(cfg, j.Src, sp.In, sp.Out, aspectStr, sampleFPS, minCov, maxGap, forceCenter,
			cutsWithinSpan(cuts, sp.In, sp.Out))
		if err != nil {
			// A span the tracker cannot follow is not a reason to throw away the
			// other eighteen. Measured on the BLINDFOLD master (a three-person
			// table scene): span 3 of 19 found a subject in 46% of samples and
			// the entire short was refused over it.
			//
			// Jordan's own edit of that same footage contains a shot with NO
			// FACE AT ALL - 1.27 seconds of a pointing finger - so "every span
			// must hold a trackable subject" is not a rule he works to. Fall
			// back to a static centre crop for THIS span, say so, carry on.
			cr, err = resolveCrop(cfg, j.Src, sp.In, sp.Out, aspectStr, sampleFPS, minCov, maxGap, true,
				cutsWithinSpan(cuts, sp.In, sp.Out))
			if err != nil {
				return res, fmt.Errorf("kept span %d/%d [%.2f,%.2f]: %w", i+1, len(spans), sp.In, sp.Out, err)
			}
			degraded++
		}
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
		return res, fmt.Errorf("%d of %d kept spans had no trackable subject - this window is not a talking-head short; pass --center to force a static crop",
			degraded, len(spans))
	}
	if degraded > 0 {
		note(&res, fmt.Sprintf("%d of %d spans fell back to a static crop (no trackable subject there)", degraded, len(spans)))
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
		concat = fmt.Sprintf("%sconcat=n=%d:v=1:a=1%s[aout]", inter.String(), len(spans), vOut)
	} else {
		concat = fmt.Sprintf("%sconcat=n=%d:v=1:a=0%s", strings.Join(vLabels, ""), len(spans), vOut)
	}
	graph := strings.Join(chains, ";") + ";" + concat

	// Captions land on the CUT timeline: one subs.Build call over the kept
	// spans laid end to end (each IS an internal/subs.Segment), so the .srt is
	// already timed to what the concat above actually outputs.
	capCount := 0
	if withCaptions {
		srt, n, cerr := captionSRTJumpcut(j.Src, j.In, j.Out, fps, spans, workDir,
			func(f string, a ...any) { logIfShort(verbose, f, a...) })
		switch {
		case cerr != nil:
			note(&res, "no captions: "+cerr.Error())
		case n == 0:
			note(&res, "no captions: nothing is said in the kept spans")
		default:
			vSub := "[vsub]"
			graph += ";" + vOut + captionFilter(srt, j.Src) + vSub
			vOut = vSub
			capCount = n
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
	words = subs.WordsInRange(words, winIn-capWordPad, winOut+capWordPad)
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
