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
	Note     string  `json:"note,omitempty"`
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
		maxGap    = flag.Float64("max-gap", 0.8, "refuse if the subject is undetected for this many seconds in a row")
		minCov    = flag.Float64("min-coverage", 0.6, "refuse if the subject was found in less than this fraction of samples")
		center    = flag.Bool("center", false, "skip pose entirely and use a static centre crop")
		selftest  = flag.Bool("selftest", false, "run the offline proof and exit")
		verbose   = flag.Bool("verbose", false, "progress to stderr")
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

	for _, j := range jobs {
		s, err := render(cfg, j, asp, outW, outH, *sampleFPS, *minCov, *maxGap, *center, *verbose)
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
	forceCenter, verbose bool) (shortOut, error) {

	res := shortOut{Out: j.Dst, Source: j.Src, Start: j.In, End: j.Out, Width: outW, Height: outH}

	var rects []crop.Rect
	if !forceCenter && cfg.PoseModel != "" {
		p, err := crop.Run(cfg, crop.Options{
			Video: j.Src, Start: j.In, End: j.Out,
			Aspect: fmt.Sprintf("%d:%d", outW, outH), FPS: sampleFPS, Model: cfg.PoseModel,
		})
		switch {
		case err != nil:
			res.Note = "subject framing unavailable (" + err.Error() + "); STATIC CENTRE crop"
		case p.LongestGap > maxGap:
			// Refuse on a CLUSTERED absence even when the average looks fine.
			return res, fmt.Errorf("the subject is off screen for %.1fs in a row (limit %.1fs) — "+
				"not rendering a short that would hold a stale crop through it; "+
				"pass --max-gap to allow it or --center for a static crop",
				p.LongestGap, maxGap)
		case p.Coverage() < minCov:
			// Refuse rather than ship a followed-looking file that mostly guessed.
			return res, fmt.Errorf("subject found in only %.0f%% of samples (need %.0f%%) — "+
				"not rendering a short that would frame the wrong thing; pass --center to force a static crop",
				p.Coverage()*100, minCov*100)
		default:
			rects = p.Rects
			res.Sampled, res.Found, res.Coverage = p.Sampled, p.Found, p.Coverage()
			res.Followed = true
		}
	} else if forceCenter {
		res.Note = "--center: static crop, subject not tracked"
	}

	// The per-frame crop path is handed to ffmpeg as a sendcmd script. It has to
	// live in a directory ffmpeg runs FROM, because sendcmd's parser treats the
	// colon in a Windows absolute path as its own separator.
	var chain, workDir string
	if len(rects) > 0 {
		dir, err := os.MkdirTemp("", "becky-short-")
		if err != nil {
			return res, err
		}
		defer os.RemoveAll(dir)
		if err := os.WriteFile(filepath.Join(dir, cmdsName), []byte(crop.SendcmdFile(rects)), 0o644); err != nil {
			return res, err
		}
		workDir = dir
		chain = crop.FilterChain(rects, outW, outH, cmdsName)
	} else {
		w, h, err := probeSize(j.Src)
		if err != nil {
			return res, err
		}
		r := crop.StaticCenter(w, h, asp)
		chain = fmt.Sprintf("crop=%d:%d:%d:%d,scale=%d:%d:flags=lanczos", r.W, r.H, r.X, r.Y, outW, outH)
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
