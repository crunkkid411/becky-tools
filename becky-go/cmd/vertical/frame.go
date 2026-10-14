package main

// frame.go - the pure decisions: which layout a short gets, the source
// rectangle each piece shows, where the captions and the hook go, where the
// punch-ins land. Every rule that came from openshorts says so; Jordan's own
// habits win where they disagree (vegas\edit-learning\habits.md).

import (
	"math"
	"slices"
	"strings"
)

// box is a face, fractions of the picture, top-left origin.
type box struct{ X, Y, W, H float64 }

func (b box) cx() float64 { return b.X + b.W/2 }
func (b box) cy() float64 { return b.Y + b.H/2 }

// sample is what the face finder saw at one moment of the timeline.
type sample struct {
	T     float64 `json:"t"`
	Faces []box   `json:"faces"`
}

// main face: the largest one in the picture.
func (s sample) main() (box, bool) {
	if len(s.Faces) == 0 {
		return box{}, false
	}
	best := s.Faces[0]
	for _, f := range s.Faces[1:] {
		if f.W*f.H > best.W*best.H {
			best = f
		}
	}
	return best, true
}

const (
	outW, outH = 1080.0, 1920.0
	outAspect  = outW / outH
)

// Layouts. "single" is the default and the safe answer (openshorts: "none is
// usually right"); the others need the picture to say so on most samples.
const (
	single     = "single"
	split      = "split"
	screencast = "screencast"
)

// chooseLayout: SPLIT when two faces of a usable size sit side by side on at
// least half the samples (openshorts MIN_COEXISTENCE 0.5); SCREENCAST when one
// small face sits off to a side on most samples (a webcam corner over a screen;
// openshorts MIN_FACE_WIDTH 0.05 keeps a stamp-sized cam out); otherwise single.
func chooseLayout(ss []sample) (string, string) {
	if len(ss) == 0 {
		return single, "no face was seen, so the centre of the picture"
	}
	two, corner := 0, 0
	for _, s := range ss {
		if a, b, ok := pair(s); ok && math.Abs(a.cx()-b.cx()) >= 0.25 {
			two++
		}
		if len(s.Faces) == 1 {
			f := s.Faces[0]
			if f.W >= 0.05 && f.W < 0.12 && (f.cx() < 0.3 || f.cx() > 0.7) {
				corner++
			}
		}
	}
	n := float64(len(ss))
	switch {
	case float64(two) >= 0.5*n:
		return split, "two people side by side on most of it"
	case float64(corner) >= 0.6*n:
		return screencast, "a small face off to the side, like a webcam over a screen"
	}
	return single, "one person"
}

// pair is the two largest faces when both are big enough to fill half a frame.
func pair(s sample) (box, box, bool) {
	if len(s.Faces) < 2 {
		return box{}, box{}, false
	}
	fs := slices.Clone(s.Faces)
	slices.SortFunc(fs, func(a, b box) int {
		if a.W*a.H > b.W*b.H {
			return -1
		}
		return 1
	})
	a, b := fs[0], fs[1]
	if a.W < 0.05 || b.W < 0.05 {
		return box{}, box{}, false
	}
	if a.cx() > b.cx() {
		a, b = b, a // left one first: the left person goes on top
	}
	return a, b, true
}

// place is one BeckyVertical.cs place line: the source rectangle shown in the
// output band [Top, Bottom] (fractions of the output height, from the top).
type place struct {
	Layer          int
	X0, Y0, X1, Y1 float64
	Top, Bottom    float64
}

// rectFor is the source rectangle with the band's aspect, centred on (cx, cy)
// and kept inside the picture. srcAspect = source width / height in pixels.
func rectFor(cx, cy, srcAspect, bandAspect float64) (x0, y0, x1, y1 float64) {
	w, h := bandAspect/srcAspect, 1.0
	if w > 1 { // a tall source: the full width, cut top and bottom
		w, h = 1, srcAspect/bandAspect
	}
	x0 = math.Max(0, math.Min(1-w, cx-w/2))
	y0 = math.Max(0, math.Min(1-h, cy-h/2))
	return x0, y0, x0 + w, y0 + h
}

// placesFor is the place lines for one piece of a short. faces are the samples
// inside that piece; the main face (median of the largest one) aims the crop.
// Jordan rarely pans (19 pans in 90 projects, habits.md), so one framing per
// piece.
func placesFor(layout string, ss []sample, srcAspect float64) []place {
	switch layout {
	case split:
		var lx, ly, rx, ry []float64
		for _, s := range ss {
			if a, b, ok := pair(s); ok {
				lx, ly, rx, ry = append(lx, a.cx()), append(ly, a.cy()), append(rx, b.cx()), append(ry, b.cy())
			}
		}
		if len(lx) == 0 {
			break // nobody side by side in this piece: frame it like a single
		}
		half := outW / (outH / 2)
		ax0, ay0, ax1, ay1 := rectFor(median(lx), median(ly), srcAspect, half)
		bx0, by0, bx1, by1 := rectFor(median(rx), median(ry), srcAspect, half)
		return []place{{1, ax0, ay0, ax1, ay1, 0, 0.5}, {0, bx0, by0, bx1, by1, 0.5, 1}}
	case screencast:
		cx, cy, ok := mainFace(ss)
		if !ok {
			break
		}
		// openshorts content_bands: the whole picture keeps its full width on top
		top := math.Min(0.6, outAspect/srcAspect)
		fx0, fy0, fx1, fy1 := rectFor(cx, cy, srcAspect, outW/(outH*(1-top)))
		return []place{{1, 0, 0, 1, 1, 0, top}, {0, fx0, fy0, fx1, fy1, top, 1}}
	}
	cx, cy, ok := mainFace(ss)
	if !ok {
		cx, cy = 0.5, 0.5 // Match Output Aspect's own centre
	}
	x0, y0, x1, y1 := rectFor(cx, cy, srcAspect, outAspect)
	return []place{{0, x0, y0, x1, y1, 0, 1}}
}

// Re-aiming. One fixed framing loses him when he leans or walks (the goodbye
// update's first test: half his face out of frame at 0:50). Jordan almost never
// pans (19 in 90 projects, habits.md), so the crop does what he does instead: a
// hard cut to a new framing. A new framing starts where his face sits in the
// outer part of the current crop (off its middle 50%) for 2 samples running;
// a framing is never shorter than 2 s.
const (
	reaimSafe = 0.25 // half-width of the crop's middle band, fraction of the crop width
	reaimRun  = 2    // samples off-centre before re-aiming (1 s apart)
	reaimMin  = 2.0  // shortest framing, seconds
)

// reaim splits [a, b) where the single-layout crop must move. Each returned
// range carries its own samples.
func reaim(ss []sample, a, b, srcAspect float64) ([][2]float64, [][]sample) {
	cropW := math.Min(1, outAspect/srcAspect)
	var cuts []float64
	start, off, last := 0, 0, a // start: the current framing's first sample
	for i, s := range ss {
		f, ok := s.main()
		cx, _, aimed := mainFace(ss[start : i-off]) // its aim, without the off-centre run
		if !ok || !aimed || math.Abs(f.cx()-cx) <= reaimSafe*cropW {
			off = 0
			continue
		}
		if off++; off < reaimRun {
			continue
		}
		j := i - reaimRun + 1 // the first off-centre sample
		if at := ss[j].T; at-last >= reaimMin && b-at >= reaimMin {
			cuts = append(cuts, at)
			start, last = j, at
		}
		off = 0
	}
	bounds := append(append([]float64{a}, cuts...), b)
	var spans [][2]float64
	var groups [][]sample
	for k := 0; k+1 < len(bounds); k++ {
		var g []sample
		for _, s := range ss {
			if s.T >= bounds[k] && s.T < bounds[k+1] {
				g = append(g, s)
			}
		}
		spans, groups = append(spans, [2]float64{bounds[k], bounds[k+1]}), append(groups, g)
	}
	return spans, groups
}

func mainFace(ss []sample) (float64, float64, bool) {
	var xs, ys []float64
	for _, s := range ss {
		if f, ok := s.main(); ok {
			xs, ys = append(xs, f.cx()), append(ys, f.cy())
		}
	}
	if len(xs) == 0 {
		return 0, 0, false
	}
	return median(xs), median(ys), true
}

// toOutput maps a source face through the place lines into the output frame
// (fractions, top-left origin); false when no band shows it.
func toOutput(f box, ps []place) (box, bool) {
	for _, p := range ps {
		w, h := p.X1-p.X0, p.Y1-p.Y0
		if f.cx() < p.X0 || f.cx() > p.X1 || f.cy() < p.Y0 || f.cy() > p.Y1 || w <= 0 || h <= 0 {
			continue
		}
		bh := p.Bottom - p.Top
		return box{X: (f.X - p.X0) / w, Y: p.Top + (f.Y-p.Y0)/h*bh, W: f.W / w, H: f.H / h * bh}, true
	}
	return box{}, false
}

// headBox grows a face box to the whole head and hair (openshorts head_box:
// 0.15 of the width each side, 0.6 of the height above, 0.2 below).
func headBox(f box) box {
	return box{X: f.X - 0.15*f.W, Y: f.Y - 0.6*f.H, W: 1.3 * f.W, H: 1.8 * f.H}
}

func overlap(a box, top, bottom float64) float64 {
	y0, y1 := math.Max(a.Y, top), math.Min(a.Y+a.H, bottom)
	if y1 <= y0 {
		return 0
	}
	x0, x1 := math.Max(a.X, 0), math.Min(a.X+a.W, 1)
	if x1 <= x0 {
		return 0
	}
	return (x1 - x0) * (y1 - y0)
}

// Caption line centre, from the top. Jordan's own: Location y 0.4 = 0.60 down
// (his September goodbye-youtube-VERTICAL); SPLIT puts them on the seam between
// the two people and SCREENCAST under the face (openshorts SEAM_CAPTIONS /
// BOTTOM_CAPTIONS). If a head covers that line on a third of the short or more,
// the openshorts bottom band (one lean toward the camera is not enough).
const captionLine = 0.035 // one caption line's height, fraction of the frame

func captionY(layout string, heads []box, samples int) float64 {
	y := map[string]float64{single: 0.60, split: 0.50, screencast: 0.86}[layout]
	if layout == single {
		covered := 0
		for _, h := range heads {
			if overlap(h, y-captionLine, y+captionLine) > 0 {
				covered++
			}
		}
		if float64(covered) >= float64(max(samples, 1))/3 {
			return 0.79
		}
	}
	return y
}

// The on-screen title. Jordan's own (September goodbye-youtube-VERTICAL): one
// short line, "TakingBack2007", Montserrat Black at 1.52, centred 14.6% down -
// over his hair. So: at most 3 lines, his size shrunk only as far as needed to
// fit them; placed like openshorts choose_y (pulled toward his spot, the text
// shrinks when every spot is bad) but scored on FACES, not heads: his own title
// sits on his hair, never on his eyes. Never over the captions or the
// platform's bottom buttons. Line height and letter width are measured on the
// first real renders (2026-10-13): a 5-line spoken "hook" at 1.52 covered his
// face and the captions because the old guess (0.045 a line) was half the truth.
const (
	hookTopMargin  = 0.06
	hookBottomUI   = 0.80
	hookHisSpot    = 0.146 // his title's centre (Location y 0.854)
	hookMove       = 0.05
	hookStep       = 0.005
	hookGoodEnough = 0.10 // a tenth of his face covered, on average
	hookScale      = 1.52
	hookLineH      = 0.08 // one line at 1.52, fraction of the frame (measured: 150 of 1920 px)
	hookLineChars  = 12   // letters a line at 1.52 (~83 px a letter, measured) with a margin
	hookMaxLines   = 3
	hookCapGap     = 0.03 // clear space to the captions: touching, they read as one block (render 2026-10-14)
	hookMinScale   = 0.9
)

// hookLines wraps the text for a scale: more letters fit as it shrinks.
func hookLines(text string, scale float64) ([]string, bool) {
	n := int(float64(hookLineChars) * hookScale / scale)
	ls := wrap(text, n)
	fits := len(ls) <= hookMaxLines
	for _, l := range ls {
		fits = fits && len(l) <= n
	}
	return ls, fits
}

// hookPlace returns the block's centre (fraction from the top), its scale and
// its lines.
func hookPlace(text string, faces []box, samples int, capY float64) (float64, float64, []string) {
	fit := hookScale
	for ; fit > hookMinScale; fit -= 0.04 {
		if _, ok := hookLines(text, fit); ok {
			break
		}
	}
	samples = max(samples, 1)
	bestY, bestS, bestCost := hookHisSpot, fit, math.Inf(1)
	for _, sc := range []float64{fit, fit * 0.85, fit * 0.72} {
		ls, _ := hookLines(text, sc)
		bh := float64(len(ls)) * hookLineH * sc / hookScale
		for y := hookTopMargin; y+bh <= hookBottomUI+1e-9; y += hookStep {
			if y < capY+captionLine+hookCapGap && y+bh > capY-captionLine-hookCapGap {
				continue // never over the captions, and never touching them
			}
			cost := hookMove * math.Abs(y+bh/2-hookHisSpot)
			for _, f := range faces {
				if a := f.W * f.H; a > 0 {
					cost += overlap(f, y, y+bh) / a / float64(samples)
				}
			}
			if cost < bestCost {
				bestY, bestS, bestCost = y+bh/2, sc, cost
			}
		}
		if bestCost <= hookGoodEnough {
			break
		}
	}
	ls, _ := hookLines(text, bestS)
	return bestY, bestS, ls
}

// wrap breaks text into lines of at most n characters, on spaces.
func wrap(text string, n int) []string {
	var lines []string
	cur := ""
	for _, w := range strings.Fields(text) {
		if cur != "" && len(cur)+1+len(w) > n {
			lines = append(lines, cur)
			cur = ""
		}
		if cur != "" {
			cur += " "
		}
		cur += w
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

// Punch-ins (openshorts punch_in.emphasis_times): a beat is a 0.2 s window
// whose loudness reaches 60% of the way from the clip's median to its peak, at
// least 18 s after the last one - their sweep found that the steadiest rate.
// The size and speed are Jordan's: his median push-in is 1.27x (habits.md), and
// a punch-in is fast (openshorts rise 0.25 s), held, then cut back.
const (
	beatWindow     = 0.2
	beatProminence = 0.60
	beatMinGap     = 18.0
	punchScale     = 1.27
	punchRise      = 0.25
	punchHold      = 1.30
)

// beats are the punch-in times, seconds from the start of the envelope; none in
// the first second (the hook is reading) or the last two.
func beats(env []float64, total float64) []float64 {
	if len(env) == 0 {
		return nil
	}
	base := median(env)
	peak := slices.Max(env)
	if peak-base <= 1e-9 {
		return nil
	}
	thr := base + (peak-base)*beatProminence
	var out []float64
	last := math.Inf(-1)
	for i, v := range env {
		t := float64(i) * beatWindow
		if v >= thr && t-last >= beatMinGap && t >= 1 && t+punchRise+punchHold <= total-2 {
			out = append(out, t)
			last = t
		}
	}
	return out
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}
