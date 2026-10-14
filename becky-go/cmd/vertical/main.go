// becky-vertical - vertical shorts from a widescreen VEGAS project, built on
// the VEGAS timeline so Jordan can adjust anything by hand afterwards.
//
// It is Jordan's own workflow (vegas\JORDANS-WORKFLOWS.md, section 2 "Create
// Vertical Shorts from existing widescreen video"), every step:
//
//  1. Project properties 1080 x 1920       -> BeckyVertical.cs "size"
//  2. Save As "<name>-VERTICAL.veg"        -> the original is never saved over
//     3+4. all video, "Match Output Aspect"   -> BeckyVertical.cs "match" (VEGAS's own math)
//  5. every clip framed on its subject     -> his face, found by InsightFace, aims each
//     piece's Pan/Crop; two people side by side get SPLIT (one above the other), a
//     small face over a screen gets SCREENCAST (the screen above, the face below) -
//     two video tracks, the copy on "Becky Layout" above (openshorts' layouts)
//     6+7. regions around the shorts, named with their titles, rendered to the
//     month's "rendered" folder with his Voukoder "TikTok NVENC" template
//
// plus captions as Titles & Text events (his "becky-captions" preset, cut by the
// cli-cut chunker), a short on-screen title kept off his face (openshorts choose_y),
// and punch-ins / face zooms that Gemma-4 picks line by line (his 1.27x size, via
// BeckyFX; the loudest beats when no model can start).
// The shorts are the ones Jordan lists (--shorts), else becky-moment picks them;
// footage with no speech gets a picture description every few seconds instead of words.
//
//	becky-vertical <project.veg> [--shorts list.txt] [--count 3] [--no-render] [--fresh]
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"becky-go/internal/config"
	"becky-go/internal/subs"
	"becky-go/internal/vegasctl"
)

// renderTemplate is his own Voukoder template, the one his September vertical
// came from. VEGAS's MAGIX "TikTok QSV" writes a sheared, unwatchable picture
// at 1080 wide, and MAGIX "TikTok" fails outright (tested 2026-10-13).
const renderTemplate = "TikTok NVENC"

type short struct {
	Start     float64  `json:"start"`
	End       float64  `json:"end"`
	Title     string   `json:"title"`
	Hook      string   `json:"hook"`
	OnScreen  string   `json:"on_screen,omitempty"`
	Layout    string   `json:"layout,omitempty"`
	Why       string   `json:"why,omitempty"`
	Captions  int      `json:"captions,omitempty"`
	Effects   []effect `json:"effects,omitempty"`
	EffectsBy string   `json:"effects_by,omitempty"`
	Reframes  int      `json:"reframes,omitempty"`
	Render    string   `json:"render,omitempty"`
}

func main() {
	list := flag.String("shorts", "", "Jordan's list: one short per line, \"<start> <end> <title>\" in the widescreen project's time (1:23, 83 or 0:01:23)")
	count := flag.Int("count", 3, "how many shorts becky picks when there is no list")
	minSec := flag.Float64("min", 20, "shortest short becky picks (seconds)")
	maxSec := flag.Float64("max", 60, "longest short becky picks (seconds)")
	noRender := flag.Bool("no-render", false, "build the project but do not render")
	fresh := flag.Bool("fresh", false, "pick the shorts and look at the faces again")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: becky-vertical <project.veg> [--shorts list.txt]")
		os.Exit(2)
	}
	if err := run(flag.Arg(0), *list, *count, *minSec, *maxSec, !*noRender, *fresh); err != nil {
		fmt.Println()
		fmt.Println("STOPPED - " + err.Error())
		os.Exit(1)
	}
}

func logf(f string, a ...any) {
	fmt.Printf("[%s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(f, a...))
}

func run(veg, list string, count int, minSec, maxSec float64, render, fresh bool) error {
	veg, _ = filepath.Abs(veg)
	if !strings.EqualFold(filepath.Ext(veg), ".veg") {
		return fmt.Errorf("%s is not a VEGAS project (.veg)", veg)
	}
	if _, err := os.Stat(veg); err != nil {
		return fmt.Errorf("%s was not found", veg)
	}
	if strings.HasSuffix(strings.ToUpper(strings.TrimSuffix(filepath.Base(veg), ".veg")), "-VERTICAL") {
		return fmt.Errorf("that is already a vertical project - give the widescreen one")
	}
	cfg := config.Load()
	dir := filepath.Dir(veg)
	work := filepath.Join(dir, "becky-edit", "vertical")
	if err := os.MkdirAll(work, 0o755); err != nil {
		return err
	}
	out, overwrite := vegasctl.ProjectPath(strings.TrimSuffix(veg, filepath.Ext(veg))+"-VERTICAL.veg", work)

	// steps 1-2 happen on a copy: open his project, Save As the vertical one
	logf("VEGAS: starting it (or using the open one)...")
	if err := vegasctl.Launch(); err != nil {
		return err
	}
	if _, err := vegasctl.Call(5*time.Minute, "open_project", "path="+veg); err != nil {
		return err
	}
	save := []string{"save", "path=" + out}
	if overwrite {
		save = append(save, "overwrite=true")
	}
	if _, err := vegasctl.Call(2*time.Minute, save...); err != nil {
		return err
	}
	logf("saved as %s - your project is untouched", filepath.Base(out))
	evs, fps, err := vegasctl.Timeline()
	if err != nil {
		return err
	}
	dur := 0.0
	for _, e := range evs {
		dur = math.Max(dur, e.TimelineEnd)
	}
	words := timelineWords(evs, transcriptFor)
	logf("timeline: %d events, %.1f minutes, %d words", len(evs), dur/60, len(words))

	// the shorts: his list, else becky-moment
	var shorts []short
	if list != "" {
		if shorts, err = readList(list); err != nil {
			return err
		}
	} else if shorts, err = pick(cfg, evs, words, dur, work, count, minSec, maxSec, fresh); err != nil {
		return err
	}
	if len(shorts) == 0 {
		return fmt.Errorf("no shorts to make")
	}

	var job, fx strings.Builder
	job.WriteString("size\t1080\t1920\nmatch\n")
	logf("starting Gemma-4 for the effects...")
	fxModel := effectModel(cfg)
	if fxModel != nil {
		defer fxModel.Close()
	}
	frameOf := func(t float64) int64 { return int64(math.Round(t * fps)) }
	for i := range shorts {
		s := &shorts[i]
		logf("short %d: %s (%s-%s) - looking at the faces...", i+1, s.Title, clock(s.Start), clock(s.End))
		cache := filepath.Join(work, fmt.Sprintf("faces-%d-%.0f.json", i+1, s.Start*1000))
		if fresh {
			_ = os.Remove(cache)
		}
		ss, err := facesIn(cfg, evs, s.Start, s.End, filepath.Join(work, "frames"), cache)
		if err != nil {
			return err
		}
		s.Layout, s.Why = chooseLayout(ss)
		srcAspect := 16.0 / 9
		if src, _, ok := srcAt(evs, s.Start); ok {
			if w, h := sourceSize(cfg, src); w > 0 {
				srcAspect = w / h
			}
		}
		// step 5: each piece of the timeline framed on him; a single-person piece
		// cuts to a new framing when he moves out of it (reaim)
		var placed [][]place
		var spans [][2]float64
		for _, e := range evs {
			a, b := math.Max(s.Start, e.Timeline), math.Min(s.End, e.TimelineEnd)
			if e.Kind != "video" || b-a < 1/fps {
				continue
			}
			var in []sample
			for _, x := range ss {
				if x.T >= a && x.T < b {
					in = append(in, x)
				}
			}
			parts, groups := [][2]float64{{a, b}}, [][]sample{in}
			if s.Layout == single {
				parts, groups = reaim(in, a, b, srcAspect)
				s.Reframes += len(parts) - 1
			}
			for k, sp := range parts {
				ps := placesFor(s.Layout, groups[k], srcAspect)
				f0 := frameOf(sp[0])
				for _, p := range ps {
					fmt.Fprintf(&job, "place\t%d\t%d\t%d\t%.5f\t%.5f\t%.5f\t%.5f\t%.5f\t%.5f\n", p.Layer, f0, frameOf(sp[1])-f0, p.X0, p.Y0, p.X1, p.Y1, p.Top, p.Bottom)
				}
				placed, spans = append(placed, ps), append(spans, sp)
			}
		}
		placesAt := func(t float64) []place {
			for k, sp := range spans {
				if t >= sp[0] && t < sp[1] {
					return placed[k]
				}
			}
			return nil
		}
		// faces (and heads) as they will be seen in the vertical frame, between a and b
		seen := func(a, b float64, head bool) ([]box, int) {
			var hs []box
			n := 0
			for _, x := range ss {
				if x.T < a || x.T >= b {
					continue
				}
				n++
				for _, f := range x.Faces {
					if o, ok := toOutput(f, placesAt(x.T)); ok {
						if head {
							o = headBox(o)
						}
						hs = append(hs, o)
					}
				}
			}
			return hs, n
		}
		heads := func(a, b float64) ([]box, int) { return seen(a, b, true) }

		// captions: the cli-cut chunker (22 characters, pauses, lowercase) on his words
		all, n := heads(s.Start, s.End)
		capY := captionY(s.Layout, all, n)
		var sw []subs.Word
		var said []word
		for _, w := range words {
			if w.Start >= s.Start && w.Start < s.End {
				sw = append(sw, subs.Word{Word: w.Word, Start: w.Start, End: math.Min(w.End, s.End)})
				said = append(said, w)
			}
		}
		opt := subs.ShortOptions()
		opt.FPS = fps
		for _, c := range subs.BuildWithWords([]subs.Segment{{Source: "timeline", Start: s.Start, End: s.End, Words: sw}}, opt) {
			// a caption never shows before its first word is said (the builder
			// stretches the first one back to the start of the short)
			if len(c.Words) > 0 {
				c.Start = math.Max(c.Start, c.Words[0].Start)
			}
			f0, f1 := frameOf(s.Start+c.Start), frameOf(s.Start+c.End)
			if f1 > f0 {
				fmt.Fprintf(&job, "text\tcaption\t%d\t%d\t0.5\t%.3f\t1\t%s\n", f0, f1-f0, 1-capY, clean(c.Text))
				s.Captions++
			}
		}

		// the on-screen title: the first 5 s, short like his ("TakingBack2007"),
		// so the title rather than the spoken hook line, never on his face
		if s.OnScreen = s.Title; s.OnScreen == "" {
			s.OnScreen = s.Hook
		}
		if s.OnScreen != "" {
			fs, n := seen(s.Start, s.Start+5, false)
			y, scale, lines := hookPlace(s.OnScreen, fs, n, capY)
			f0 := frameOf(s.Start)
			fmt.Fprintf(&job, "text\thook\t%d\t%d\t0.5\t%.3f\t%.3f\t%s\n", f0, frameOf(math.Min(s.End, s.Start+5))-f0, 1-y, scale, clean(strings.Join(lines, "\n")))
		}
		fmt.Fprintf(&job, "region\t%d\t%d\t%s\n", frameOf(s.Start), frameOf(s.End)-frameOf(s.Start), clean(s.Title))

		// effects: single layout only (a split or screencast already moves the eye).
		// The model picks from the closed list; without it, the loudness beats.
		if s.Layout == single {
			var ok bool
			if s.Effects, ok = pickEffects(fxModel, sentences(said), s.Start, s.End-s.Start); ok {
				s.EffectsBy = "Gemma-4 read the lines"
			} else {
				s.Effects, s.EffectsBy = nil, "the loudest beats (the model could not run)"
				for _, t := range beats(envelope(cfg, evs, s.Start, s.End), s.End-s.Start) {
					s.Effects = append(s.Effects, effect{T: t, Len: punchRise + punchHold, Kind: fxPunch, Why: "loud beat"})
				}
			}
			for _, e := range s.Effects {
				at := s.Start + e.T
				hs, _ := seen(at-1, at+e.Len+1, false) // a zoom aims at his face (habits.md)
				cx, cy := 0.5, 0.35
				if len(hs) > 0 {
					var xs, ys []float64
					for _, h := range hs {
						xs, ys = append(xs, h.cx()), append(ys, h.cy())
					}
					cx, cy = median(xs), median(ys)
				}
				scale, ramp := punchScale, frameOf(at+punchRise)-frameOf(at)
				if e.Kind == fxFaceZoom {
					scale, ramp = fxZoomScale, fxZoomRamp
				}
				f0 := frameOf(at)
				fmt.Fprintf(&fx, "zoom\t%d\t%d\t%.2f\t%.3f\t%.3f\t%d\n", f0, frameOf(at+e.Len)-f0, scale, cx, cy, ramp)
			}
		}
		logf("  %s (%s), %d captions, %d effects (%s), %d re-framings", s.Layout, s.Why, s.Captions, len(s.Effects), s.EffectsBy, s.Reframes)
	}
	if fxModel != nil {
		fxModel.Close() // ~11 GB the VEGAS render needs (00:37 2026-10-14: 1 GB left mid-render)
	}

	res, err := vegasctl.RunJob("BeckyVertical.cs", filepath.Join(work, "vertical-job.txt"), job.String(), 20*time.Minute)
	if err != nil {
		return err
	}
	logf("VEGAS: %s", res)
	if fx.Len() > 0 {
		if res, err = vegasctl.RunJob("BeckyFX.cs", filepath.Join(work, "punch-in-job.txt"), fx.String(), 10*time.Minute); err != nil {
			return err
		}
		logf("VEGAS punch-ins: %s", res)
	}
	if _, err := vegasctl.Call(2*time.Minute, "save"); err != nil {
		return err
	}
	vegasctl.Stamp(out, work)

	if render {
		rdir := filepath.Join(filepath.Dir(dir), "rendered")
		var rj strings.Builder
		for i := range shorts {
			s := &shorts[i]
			s.Render = vegasctl.FreeName(filepath.Join(rdir, kebab(s.Title)+".mp4"))
			fmt.Fprintf(&rj, "render\t%d\t%d\t%s\t%s\n", frameOf(s.Start), frameOf(s.End)-frameOf(s.Start), renderTemplate, s.Render)
		}
		logf("VEGAS: rendering %d shorts to %s...", len(shorts), rdir)
		vegasctl.Restore() // a minimized VEGAS renders pure black
		if res, err = vegasctl.RunJob("BeckyVertical.cs", filepath.Join(work, "render-job.txt"), rj.String(), time.Duration(len(shorts))*20*time.Minute); err != nil {
			return err
		}
		logf("VEGAS: %s", res)
	}
	return report(work, veg, out, shorts)
}

// pick asks becky-moment (the openshorts two-pass picker) for the shorts, from
// the words on the timeline - or, with no speech, from the picture lines.
func pick(cfg config.Config, evs []vegasctl.Event, words []word, dur float64, work string, count int, minSec, maxSec float64, fresh bool) ([]short, error) {
	picked := filepath.Join(work, "picked.json")
	if fresh {
		_ = os.Remove(picked)
	}
	if _, err := os.Stat(picked); err != nil {
		segs := sentences(words)
		if len(words) < 30 {
			var err error
			if segs, err = visualLines(cfg, evs, dur, work, logf); err != nil {
				return nil, err
			}
			words = nil
		}
		tr := filepath.Join(work, "timeline.transcript.json")
		b, _ := json.Marshal(map[string]any{"segments": segs, "words": words})
		if err := os.WriteFile(tr, b, 0o644); err != nil {
			return nil, err
		}
		bin, err := vegasctl.Bin("becky-moment")
		if err != nil {
			return nil, err
		}
		logf("becky-moment: picking %d shorts (%.0f-%.0f s)...", count, minSec, maxSec)
		cmd := exec.Command(bin, "--transcript", tr, "--top", strconv.Itoa(count), "--min", fmt.Sprint(minSec), "--max", fmt.Sprint(maxSec), "--extend", "0",
			"--no-face", "--no-audio", // a timeline transcript has no video beside it; becky-vertical looks at the faces itself
			"--verbose", "--out", picked)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("becky-moment could not pick the shorts: %w", err)
		}
	}
	var rep struct {
		Moments []struct {
			Start, End  float64
			Title, Hook string
		} `json:"moments"`
	}
	b, err := os.ReadFile(picked)
	if err != nil || json.Unmarshal(b, &rep) != nil {
		return nil, fmt.Errorf("becky-moment's answer was unreadable (%s)", picked)
	}
	var out []short
	segs := sentences(words)
	for _, m := range rep.Moments {
		out = append(out, wholeSentences(short{Start: m.Start, End: m.End, Title: m.Title, Hook: m.Hook}, segs))
	}
	return out, nil
}

// wholeSentences: a short starts where the thought starts and ends on a full
// sentence. The first renders (2026-10-14) all opened on a continuation ("And
// threatening people.") and ended mid-sentence where becky-moment hit its 60 s
// cap ("Said footage of my-"). The start walks back over sentences that carry
// on from the one before (And / But / Because / Or / Then, or a lowercase first
// word) with under 1.5 s between them, or that run straight on from it (under
// 0.15 s: no clean place to cut, "apologized. That" lost its "That"), at most
// 10 s; the end is the last
// sentence that finishes by becky-moment's end. (His own shorts.txt times are
// used exactly as he wrote them.)
func wholeSentences(s short, segs []segment) short {
	i := 0
	for i < len(segs) && segs[i].End <= s.Start {
		i++
	}
	if i == len(segs) {
		return s
	}
	for i > 0 && (continues(segs[i].Text) || segs[i].Start-segs[i-1].End < 0.15) && segs[i].Start-segs[i-1].End <= 1.5 && s.Start-segs[i-1].Start <= 10 {
		i--
	}
	// a quarter second of lead-in, never into the word before: word times run
	// late, and a cut right on "That" clipped it (render 2026-10-14)
	lead := segs[i].Start - 0.25
	if i > 0 {
		lead = math.Max(lead, segs[i-1].End)
	}
	s.Start = math.Min(s.Start, lead)
	for k := len(segs) - 1; k > i; k-- {
		if segs[k].End <= s.End+0.5 {
			s.End = segs[k].End
			break
		}
	}
	return s
}

func continues(text string) bool {
	w := strings.Trim(strings.Fields(text + " x")[0], `"',.`)
	if w == "" || unicode.IsLower(rune(w[0])) {
		return true
	}
	switch strings.ToLower(w) {
	case "and", "but", "because", "cause", "or", "then":
		return true
	}
	return false
}

var listLine = regexp.MustCompile(`^\s*([\d:.]+)\s*(?:-|to|\s)\s*([\d:.]+)\s+(.+?)\s*$`)

// readList reads Jordan's list: "<start> <end> <title>" per line.
func readList(path string) ([]short, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []short
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m := listLine.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("%s line %d: want \"<start> <end> <title>\", got %q", path, n, line)
		}
		a, err1 := seconds(m[1])
		b, err2 := seconds(m[2])
		if err1 != nil || err2 != nil || b <= a {
			return nil, fmt.Errorf("%s line %d: the times %q and %q do not make a range", path, n, m[1], m[2])
		}
		out = append(out, short{Start: a, End: b, Title: m[3]})
	}
	return out, sc.Err()
}

// seconds reads 83, 1:23, 1:23.5 or 0:01:23.
func seconds(s string) (float64, error) {
	t := 0.0
	for _, p := range strings.Split(s, ":") {
		v, err := strconv.ParseFloat(p, 64)
		if err != nil {
			return 0, err
		}
		t = t*60 + v
	}
	return t, nil
}

func clock(t float64) string {
	s := int(t)
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// clean keeps a job-file field on one line (a hook's own line break is \n in RTF terms later).
func clean(s string) string {
	return strings.NewReplacer("\t", " ", "\r", "", "\n", "\\n").Replace(strings.TrimSpace(s))
}

// kebab is Jordan's file naming: lower case, words joined by "-", nothing else.
func kebab(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if r == '\'' || r == '’' {
			continue // "he's" is "hes", not "he-s"
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	k := strings.TrimSuffix(b.String(), "-")
	if k == "" {
		k = "short"
	}
	return k
}

func sourceSize(cfg config.Config, src string) (float64, float64) {
	probe := cfg.FFprobe
	if probe == "" {
		probe = "ffprobe"
	}
	out, err := exec.Command(probe, "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=width,height", "-of", "csv=p=0", src).Output()
	if err != nil {
		return 0, 0
	}
	var w, h float64
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%f,%f", &w, &h); err != nil || h == 0 {
		return 0, 0
	}
	return w, h
}

func report(work, veg, out string, shorts []short) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Vertical shorts\n\nFrom `%s`, saved as `%s` (your project is untouched).\n\n", veg, out)
	for i, s := range shorts {
		fmt.Fprintf(&b, "## %d. %s\n\n- %s to %s on the widescreen timeline\n- Layout: %s (%s)\n- On-screen title (first 5 s): %s\n- Captions: %d\n- Cuts to a new framing when he moves: %d\n- Effects, chosen by %s:\n",
			i+1, s.Title, clock(s.Start), clock(s.End), s.Layout, s.Why, s.OnScreen, s.Captions, s.Reframes, s.EffectsBy)
		for _, e := range s.Effects {
			fmt.Fprintf(&b, "  - %.1f s %s: %q (%s)\n", e.T, e.Kind, e.Line, e.Why)
		}
		if s.Render != "" {
			fmt.Fprintf(&b, "- Rendered: `%s`\n", s.Render)
		}
		b.WriteString("\n")
	}
	path := filepath.Join(work, "report.md")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return err
	}
	j, _ := json.MarshalIndent(shorts, "", "  ")
	_ = os.WriteFile(filepath.Join(work, "shorts.json"), j, 0o644)
	fmt.Println()
	fmt.Printf("DONE - %d vertical shorts\n", len(shorts))
	fmt.Printf("  VEGAS project: %s\n", out)
	for _, s := range shorts {
		if s.Render != "" {
			fmt.Printf("  %s\n", s.Render)
		}
	}
	fmt.Printf("  Report: %s\n", path)
	return nil
}
