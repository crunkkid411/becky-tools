// becky-short — cut a vertical short out of a long video, framed on the subject.
//
// One dumb call. Give it a video and a window (or a reel from becky-hits/
// becky-moment) and it renders a 9:16 file with the crop FOLLOWING the person,
// not a fixed centre box.
//
//	becky-short --video stream.mp4 --start 12906 --end 12937 --out clip.mp4
//	becky-short --reel moments.reel.json --outdir shorts\
//
// Why this exists in the shape it does:
//
//   - Most of Jordan's own footage is ALREADY 1080x1920, so the job is usually a
//     push-in on the subject rather than a pan across a wide shot. Ingested
//     landscape footage needs the pan. Both are "put a rect of the target aspect
//     around the person", so there is one path, not two.
//   - The crop is framed on shoulders and headroom (MediaPipe Pose), not on a
//     face box. A face-centred crop is the most recognisable tell of an
//     auto-generated short: head dead centre, gestures cut off.
//   - Every render REPORTS the fraction of sampled instants where a body was
//     actually found. Below --min-coverage it refuses rather than shipping a
//     plausible file built from carried-forward guesses. A short that renders
//     fine but frames the wrong thing is the failure mode this whole pipeline
//     exists to avoid, and it cannot be spotted from an exit code.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/crop"
	"becky-go/internal/pathx"
)

type shortOut struct {
	Out      string  `json:"out"`
	Source   string  `json:"source"`
	Start    float64 `json:"start"`
	End      float64 `json:"end"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	Sampled  int     `json:"sampled"`
	Found    int     `json:"found"`
	Coverage float64 `json:"coverage"`
	Followed bool    `json:"followed"`
	Captions int     `json:"captions"`
	// Jumpcuts is true when this short was actually cut the way becky-cut would
	// have cut it (dead air removed, kept spans butted together) rather than
	// rendered as one continuous take — see planJumpcuts/renderJumpcutShort.
	Jumpcuts bool `json:"jumpcuts"`
	// KeepSpans/RemovedSeconds are the pacing decision itself, not just its
	// result: how many spans becky-cut kept inside this window, and how many
	// seconds of dead air came out. Reported even when Jumpcuts is false (e.g.
	// becky-cut found nothing worth cutting) so the note isn't the only signal.
	KeepSpans      int     `json:"keep_spans"`
	RemovedSeconds float64 `json:"removed_seconds"`
	// ExistingCuts/PreservedCuts are only set when the SOURCE ITSELF already
	// carried hard cuts inside this window (planShotSpans) — Jordan
	// inherited these cuts rather than choosing them, so RemovedSeconds in
	// that mode means TIGHTENED, not removed dead air. Both are 0 for a
	// window with no detected existing cuts (raw footage — see Jumpcuts /
	// KeepSpans above for that path instead).
	ExistingCuts  int    `json:"existing_cuts,omitempty"`
	PreservedCuts int    `json:"preserved_cuts,omitempty"`
	Note          string `json:"note,omitempty"`
}

type report struct {
	Shorts    []shortOut `json:"shorts"`
	Skipped   []string   `json:"skipped,omitempty"`
	Notes     []string   `json:"notes,omitempty"`
	PoseModel string     `json:"pose_model,omitempty"`
}

// reel is the subset of becky-hits' reel we need.
type reel struct {
	Name  string `json:"name"`
	Clips []struct {
		Source string  `json:"source"`
		In     float64 `json:"in"`
		Out    float64 `json:"out"`
		Label  string  `json:"label"`
	} `json:"clips"`
}

func main() {
	var (
		video     = flag.String("video", "", "source video")
		start     = flag.Float64("start", 0, "window start (seconds)")
		end       = flag.Float64("end", 0, "window end (seconds)")
		reelPath  = flag.String("reel", "", "a becky-hits reel: render every clip in it")
		out       = flag.String("out", "", "output file (single clip)")
		outDir    = flag.String("outdir", "", "output folder (--reel mode)")
		aspect    = flag.String("aspect", "9:16", "target aspect, width:height")
		sampleFPS = flag.Float64("sample-fps", 0, "how often to look for the subject; 0 = EVERY FRAME (default)")
		maxGap    = flag.Float64("max-gap", 2.0, "refuse if the subject is undetected for this many seconds in a row; "+
			"a glance away is normal and the last good framing covers it, but a real absence "+
			"means there is no honest crop of that window")
		minCov   = flag.Float64("min-coverage", 0.6, "refuse if the subject was found in less than this fraction of samples")
		center   = flag.Bool("center", false, "skip pose entirely and use a static centre crop")
		captions = flag.Bool("captions", true, "burn word-timed captions into the short (--captions=false to skip)")
		jumpcuts = flag.Bool("jumpcuts", true, "cut dead air the way becky-cut would (jumpcuts), instead of "+
			"one unbroken continuous take; --jumpcuts=false renders the old continuous window")
		tighten = flag.Float64("tighten", defaultTighten, "seconds to trim (total) at each EXISTING cut this "+
			"short preserves, when becky-cut finds no real dead air right at that cut to trim instead — "+
			"default is Jordan's own measured tightening rate on already-edited footage (150ms/cut, "+
			"research/jordan-edit-reverse-engineered.md), not a raw-footage silence threshold")
		selftest = flag.Bool("selftest", false, "run the offline proof and exit")
		verbose  = flag.Bool("verbose", false, "progress to stderr")
	)
	flag.Parse()

	if *selftest {
		os.Exit(runSelftest())
	}

	cfg := config.Load()
	asp, err := crop.ParseAspect(*aspect)
	if err != nil {
		fail(err)
	}
	outW, outH := crop.OutputSize(asp)

	var jobs []job
	switch {
	case *reelPath != "":
		jobs, err = jobsFromReel(*reelPath, *outDir, outW, outH)
		if err != nil {
			fail(err)
		}
	case *video != "":
		if *end <= *start {
			fail(fmt.Errorf("--end must be greater than --start"))
		}
		o := *out
		if o == "" {
			o = defaultOut(*video, *start, *outDir)
		}
		jobs = []job{{Src: *video, In: *start, Out: *end, Dst: o}}
	default:
		fail(fmt.Errorf("need --video (with --start/--end) or --reel"))
	}

	rep := report{PoseModel: pathx.Base(cfg.PoseModel)}
	if cfg.PoseModel == "" && !*center {
		rep.Notes = append(rep.Notes,
			"no MediaPipe pose model found — every short falls back to a STATIC CENTRE crop; "+
				"run scripts/get-mediapipe-models.ps1 to enable subject framing")
	}

	// One cache for the whole run: becky-cut analyses the WHOLE source file
	// regardless of any one short's window, so a --reel job with several shorts
	// cut from the same source must run it once, not once per short.
	cutCache := newCutCache()
	for _, j := range jobs {
		s, err := render(cfg, j, asp, outW, outH, *sampleFPS, *minCov, *maxGap, *center, *captions, *jumpcuts, *tighten, cutCache, *verbose)
		if err != nil {
			rep.Skipped = append(rep.Skipped, fmt.Sprintf("%s @ %.2f: %v", pathx.Base(j.Src), j.In, err))
			continue
		}
		rep.Shorts = append(rep.Shorts, s)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(rep)
	if len(rep.Shorts) == 0 {
		os.Exit(1)
	}
}

// cmdsName is the sendcmd script's filename. Bare, never a path - see FilterChain.
const cmdsName = "crop.cmds"

type job struct {
	Src, Dst string
	In, Out  float64
}

func render(cfg config.Config, j job, asp float64, outW, outH int, sampleFPS, minCov, maxGap float64,
	forceCenter, withCaptions, useJumpcuts bool, tighten float64, cache *cutCache, verbose bool) (shortOut, error) {

	j = absoluteJob(j)

	res := shortOut{Out: j.Dst, Source: j.Src, Start: j.In, End: j.Out, Width: outW, Height: outH}

	// Decide the pacing FIRST: is this a continuous window, does the SOURCE
	// already carry hard cuts to preserve (planShotSpans), or does becky-cut
	// say part of it is raw-footage dead air (planJumpcuts)? A short with no
	// jumpcuts is still a usable short, so any failure here (no becky-cut, it
	// errored, nothing left after intersecting its decisions with this
	// window) degrades to the old continuous render below rather than
	// refusing the whole job.
	if useJumpcuts {
		plan, jcErr := planPacing(cfg, cache, j, tighten)
		switch {
		case jcErr != nil:
			note(&res, "jumpcuts unavailable: "+firstLine(jcErr)+"; continuous render")
		case len(plan.Spans) == 0:
			note(&res, "becky-cut found no keep segments in this window; continuous render")
		case plan.ExistingCuts == 0 && plan.RemovedSeconds < jumpcutNoopEps:
			// becky-cut agrees the window is already tight — nothing to cut.
			res.KeepSpans = len(plan.Spans)
		default:
			res.KeepSpans = len(plan.Spans)
			res.RemovedSeconds = plan.RemovedSeconds
			res.ExistingCuts = plan.ExistingCuts
			res.PreservedCuts = plan.PreservedCuts
			res.Jumpcuts = true
			if plan.ExistingCuts > 0 {
				note(&res, fmt.Sprintf("source already edited: preserved %d/%d existing cuts, tightened %.3fs total "+
					"(inherited the cuts, did not re-cut with a silence threshold)",
					plan.PreservedCuts, plan.ExistingCuts, plan.RemovedSeconds))
			}
			return renderJumpcutShort(cfg, j, plan.Spans, plan.Cuts, res, asp, outW, outH, sampleFPS, minCov, maxGap,
				forceCenter, withCaptions, verbose)
		}
	}

	// nil cut times: reaching here means either --jumpcuts=false (the flag's
	// documented contract is the UNCHANGED continuous render) or jumpcuts was
	// on but found no existing shots to preserve — either way there is
	// nothing known to split this window on.
	cr, err := resolveCrop(cfg, j.Src, j.In, j.Out, fmt.Sprintf("%d:%d", outW, outH), sampleFPS, minCov, maxGap, forceCenter, nil)
	if err != nil {
		return res, err
	}
	rects := cr.Rects
	res.Sampled, res.Found, res.Coverage, res.Followed = cr.Sampled, cr.Found, cr.Coverage, cr.Followed
	if cr.Note != "" {
		note(&res, cr.Note)
	}

	// The per-frame crop path is handed to ffmpeg as a sendcmd script. It has to
	// live in a directory ffmpeg runs FROM, because sendcmd's parser treats the
	// colon in a Windows absolute path as its own separator.
	workDir, err := os.MkdirTemp("", "becky-short-")
	if err != nil {
		return res, err
	}
	defer os.RemoveAll(workDir)

	var chain string
	if len(rects) > 0 {
		if err := os.WriteFile(filepath.Join(workDir, cmdsName), []byte(crop.SendcmdFile(rects)), 0o644); err != nil {
			return res, err
		}
		chain = crop.FilterChain(rects, outW, outH, cmdsName)
	} else {
		w, h, err := probeSize(j.Src)
		if err != nil {
			return res, err
		}
		r := crop.StaticCenter(w, h, asp)
		chain = fmt.Sprintf("crop=%d:%d:%d:%d,scale=%d:%d:flags=lanczos", r.W, r.H, r.X, r.Y, outW, outH)
	}

	// Captions come AFTER the crop and scale, so they are laid on the finished
	// 9:16 frame at a fixed size rather than being scaled with the source.
	if withCaptions {
		fps, ferr := sourceFPS(cfg.FFprobe, j.Src)
		if ferr != nil {
			note(&res, "caption timing not frame-aligned: "+ferr.Error())
		}
		srt, n, cerr := captionSRT(j.Src, j.In, j.Out, fps, workDir,
			func(f string, a ...any) { logIfShort(verbose, f, a...) })
		switch {
		case cerr != nil:
			// A short without captions is still a usable short, so this degrades
			// with a note rather than refusing the render.
			note(&res, "no captions: "+cerr.Error())
		case n == 0:
			note(&res, "no captions: nothing is said in this window")
		default:
			chain += "," + captionFilter(srt, j.Src)
			res.Captions = n
		}
	}

	if d := filepath.Dir(j.Dst); d != "" && d != "." {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return res, err
		}
	}
	args := crop.RenderArgs(j.Src, j.In, j.Out-j.In, chain, j.Dst)
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

// cropResult is one window's subject-framing decision: either a followed
// per-frame path (Rects non-empty) or an honest static fallback (Rects nil,
// Note explains why). Shared by the continuous render path and by each
// jumpcut span in renderJumpcutShort, so both apply the SAME framing gates.
type cropResult struct {
	Rects    []crop.Rect
	Sampled  int
	Found    int
	Coverage float64
	Followed bool
	Note     string
}

// resolveCrop decides the crop for ONE [start,end] window of src. Pulled out
// of render() so a jumpcut short can call it once per kept span, each getting
// its OWN local (0-based) camera path — see renderJumpcutShort's doc comment
// for why that, not a single whole-window path sliced after the fact, is the
// safe way to avoid putting the crop on the wrong timeline after a cut.
// cutTimes are existing shot boundaries (internal/shotcut) already known to
// fall inside [start,end] — see renderJumpcutShort's doc comment for why
// this is normally empty (each jumpcut span already IS one shot) and is
// still threaded through defensively rather than assumed.
func resolveCrop(cfg config.Config, src string, start, end float64, aspect string,
	sampleFPS, minCov, maxGap float64, forceCenter bool, cutTimes []float64) (cropResult, error) {
	if forceCenter {
		return cropResult{Note: "--center: static crop, subject not tracked"}, nil
	}
	if cfg.PoseModel == "" {
		return cropResult{}, nil
	}
	p, err := crop.Run(cfg, crop.Options{
		Video: src, Start: start, End: end, Aspect: aspect, FPS: sampleFPS, Model: cfg.PoseModel,
		CutTimes: cutTimes,
	})
	switch {
	case err != nil:
		return cropResult{Note: "subject framing unavailable (" + err.Error() + "); STATIC CENTRE crop"}, nil
	case p.LongestGap > maxGap:
		// Refuse on a CLUSTERED absence even when the average looks fine.
		return cropResult{}, fmt.Errorf("the subject is off screen for %.1fs in a row (limit %.1fs) — "+
			"not rendering a short that would hold a stale crop through it; "+
			"pass --max-gap to allow it or --center for a static crop",
			p.LongestGap, maxGap)
	case p.Coverage() < minCov:
		// Refuse rather than ship a followed-looking file that mostly guessed.
		return cropResult{}, fmt.Errorf("subject found in only %.0f%% of samples (need %.0f%%) — "+
			"not rendering a short that would frame the wrong thing; pass --center to force a static crop",
			p.Coverage()*100, minCov*100)
	default:
		return cropResult{Rects: p.Rects, Sampled: p.Sampled, Found: p.Found, Coverage: p.Coverage(), Followed: true}, nil
	}
}

// firstLine trims an error down to its first line, for a report note that
// should not carry a whole stderr dump.
func firstLine(err error) string {
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

func jobsFromReel(path, outDir string, outW, outH int) ([]job, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r reel
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("not a reel: %w", err)
	}
	if len(r.Clips) == 0 {
		return nil, fmt.Errorf("reel has no clips")
	}
	if outDir == "" {
		outDir = filepath.Join(filepath.Dir(path), "shorts")
	}
	var out []job
	for i, c := range r.Clips {
		if c.Out <= c.In {
			continue
		}
		name := fmt.Sprintf("%02d_%s.mp4", i+1, safeStem(pathx.Base(c.Source)))
		out = append(out, job{Src: c.Source, In: c.In, Out: c.Out, Dst: filepath.Join(outDir, name)})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("reel has no clips with a positive duration")
	}
	return out, nil
}

func defaultOut(video string, start float64, outDir string) string {
	if outDir == "" {
		outDir = filepath.Join(filepath.Dir(video), "shorts")
	}
	return filepath.Join(outDir, fmt.Sprintf("%s_%06.0f.mp4", safeStem(pathx.Base(video)), start))
}

func safeStem(name string) string {
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	if len(stem) > 48 {
		stem = stem[:48]
	}
	var b strings.Builder
	for _, r := range stem {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

func probeSize(src string) (int, int, error) {
	cmd := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height", "-of", "csv=p=0:s=x", src)
	b, err := cmd.Output()
	if err != nil {
		return 0, 0, fmt.Errorf("ffprobe failed on %s: %w", pathx.Base(src), err)
	}
	var w, h int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(b)), "%dx%d", &w, &h); err != nil || w == 0 || h == 0 {
		return 0, 0, fmt.Errorf("could not read the size of %s", pathx.Base(src))
	}
	return w, h, nil
}

func ffmpegBin(cfg config.Config) string {
	if cfg.FFmpeg != "" {
		return cfg.FFmpeg
	}
	return "ffmpeg"
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 900 {
		return "…" + s[len(s)-900:]
	}
	return s
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "becky-short:", err)
	os.Exit(2)
}

// note appends to the result's note without losing an earlier one - a short can
// degrade in more than one way at once, and hiding the first failure behind the
// second is how a quietly-worse render passes for a good one.
func note(res *shortOut, msg string) {
	if res.Note == "" {
		res.Note = msg
		return
	}
	res.Note += "; " + msg
}

func logIfShort(verbose bool, format string, a ...any) {
	beckyio.Logf(verbose, format, a...)
}

// absoluteJob resolves a job's source and destination to absolute paths.
//
// ffmpeg runs with its working directory set to the temp folder holding the
// sendcmd script - sendcmd's parser treats a Windows drive colon as its own
// separator, so the script has to be a bare filename and ffmpeg has to run from
// its directory. That makes every RELATIVE path resolve against the TEMP folder
// instead of the user's: `--out clip.mp4` wrote the short into the temp dir,
// which was then deleted, and the run reported "ffmpeg reported success but
// wrote no file". The render succeeded; the file was thrown away.
func absoluteJob(j job) job {
	if abs, err := filepath.Abs(j.Src); err == nil {
		j.Src = abs
	}
	if abs, err := filepath.Abs(j.Dst); err == nil {
		j.Dst = abs
	}
	return j
}
