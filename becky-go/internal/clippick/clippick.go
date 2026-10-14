// Package clippick picks short-form clips out of a long transcript the way
// openshorts does (X:\AI-2\content_generators\openshorts, clip_selection.py +
// main.get_viral_clips, reverse-engineered 2026-10-13).
//
// Why this shape, measured by openshorts on real jobs and kept here:
//
//   - The model RANKS whole ~90 s windows of transcript against each other; it
//     never judges one small candidate alone. becky-moment's old path judged
//     structural candidates one at a time and "structure alone cannot rank
//     interestingness" - 112,625 candidates all scored 0.985-1.000.
//   - Every window is scored and the shortlist is the global top N: a batch that
//     SELECTS "up to 3" caps the shortlist and starves short videos.
//   - The model never does timing arithmetic: its in/out points are snapped onto
//     real word boundaries plus half the pause around them (p90 of its raw
//     proposals was 0.4-0.5 s off the nearest word).
//   - Rules live in code, not the prompt: the clip floor, trimming by score
//     (never by position), and dropping clips that repeat 25% of a better one.
//
// The package is pure: the model call is a func the caller supplies, so every
// rule here is unit-tested without a model.
package clippick

import (
	"math"
	"sort"
	"strings"
)

// Segment is one transcript line (a sentence or ASR segment).
type Segment struct {
	Start, End float64
	Text       string
}

// Word is one transcribed word - the ground truth for every cut point.
type Word struct {
	Text       string
	Start, End float64
}

// Window is one scoring window, aligned to segment boundaries.
type Window struct {
	ID    string  `json:"id"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

// Clip is one picked clip with the copy the detail pass wrote for it.
type Clip struct {
	Start          float64    `json:"start"`
	End            float64    `json:"end"`
	Window         string     `json:"source_window_id"`
	Score          int        `json:"predicted_score"`
	Hook           string     `json:"viral_hook_text"`
	Title          string     `json:"video_title_for_youtube_short"`
	DescTikTok     string     `json:"video_description_for_tiktok"`
	DescInstagram  string     `json:"video_description_for_instagram"`
	Why            string     `json:"why"`
	Proposed       [2]float64 `json:"proposed"` // the model's own in/out, before snapping
	WindowScore    int        `json:"window_score"`
	WindowRationle string     `json:"window_reason,omitempty"`
}

// BuildWindows grows windows segment by segment to about windowSec (up to
// 1.25x for the closing segment) and starts the next one about overlapSec
// before the previous end, snapped to a segment start - so a sentence, and
// usually a moment, is never split between windows.
func BuildWindows(segs []Segment, duration, windowSec, overlapSec float64) []Window {
	var s []Segment
	for _, g := range segs {
		if t := strings.TrimSpace(g.Text); t != "" {
			s = append(s, Segment{g.Start, g.End, t})
		}
	}
	var out []Window
	for i, n := 0, len(s); i < n; {
		start := s[i].Start
		j := i
		for j+1 < n && s[j+1].End-start <= windowSec*1.25 {
			j++
			if s[j].End-start >= windowSec {
				break
			}
		}
		var texts []string
		for k := i; k <= j; k++ {
			texts = append(texts, s[k].Text)
		}
		out = append(out, Window{ID: windowID(len(out) + 1), Start: round3(start), End: round3(s[j].End), Text: strings.Join(texts, " ")})
		if j >= n-1 {
			break
		}
		target := s[j].End - overlapSec
		k := i + 1
		for k <= j && s[k].Start < target {
			k++
		}
		i = max(k, i+1)
	}
	if len(out) == 0 {
		var all []string
		for _, g := range s {
			all = append(all, g.Text)
		}
		out = append(out, Window{ID: windowID(1), Start: 0, End: round3(duration), Text: strings.Join(all, " ")})
	}
	return out
}

func windowID(n int) string {
	return "window_" + pad3(n)
}

func pad3(n int) string {
	s := []byte("000")
	for i := 2; i >= 0 && n > 0; i-- {
		s[i] = byte('0' + n%10)
		n /= 10
	}
	return string(s)
}

// ScoreBatches splits windows into near-equal batches: a trailing batch of one
// window would be judged against nothing.
func ScoreBatches(ws []Window, size int) [][]Window {
	n := len(ws)
	if n == 0 {
		return nil
	}
	size = max(size, 1)
	nb := (n + size - 1) / size
	base, extra := n/nb, n%nb
	var out [][]Window
	start := 0
	for i := range nb {
		take := base
		if i < extra {
			take++
		}
		out = append(out, ws[start:start+take])
		start += take
	}
	return out
}

// ShortlistTarget is how many scored windows the detail pass gets: grows with
// the video, floored at 3, capped at 10.
func ShortlistTarget(duration float64) int {
	return max(3, min(10, int(duration/90)+2))
}

// ClipCountTargets is the clip floor and ceiling for a shortlist of n windows.
// openshorts measured users who got 1-3 clips came back 0.4% of the time
// against 16.1% for 4-9, and the model left alone collapses to one clip.
func ClipCountTargets(n int) (int, int) {
	n = max(n, 1)
	low := max(2, min(6, n/2+2))
	high := min(12, max(4, n*2))
	return min(low, high), high
}

// TrimToBest keeps the max best clips BY SCORE and returns them in time order.
// Slicing the list instead kept the earliest clips and silently dropped the
// back half of the video.
func TrimToBest(cs []Clip, maxClips int) []Clip {
	maxClips = max(maxClips, 1)
	if len(cs) <= maxClips {
		return cs
	}
	idx := make([]int, len(cs))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return cs[idx[a]].Score > cs[idx[b]].Score })
	idx = idx[:maxClips]
	sort.Ints(idx)
	out := make([]Clip, 0, maxClips)
	for _, i := range idx {
		out = append(out, cs[i])
	}
	return out
}

// DedupeOverlapping drops a clip that shares ratio (0.25) of the shorter clip
// with a better-scored one; survivors come back in time order.
func DedupeOverlapping(cs []Clip, ratio float64) []Clip {
	idx := make([]int, len(cs))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return cs[idx[a]].Score > cs[idx[b]].Score })
	var kept []int
	for _, i := range idx {
		c := cs[i]
		clash := false
		for _, k := range kept {
			o := cs[k]
			ov := math.Min(c.End, o.End) - math.Max(c.Start, o.Start)
			shorter := math.Max(1e-6, math.Min(c.End-c.Start, o.End-o.Start))
			if ov > 0 && ov/shorter >= ratio {
				clash = true
				break
			}
		}
		if !clash {
			kept = append(kept, i)
		}
	}
	sort.Ints(kept)
	out := make([]Clip, 0, len(kept))
	for _, i := range kept {
		out = append(out, cs[i])
	}
	return out
}

// Snap puts a proposed clip onto real word boundaries: the nearest word start
// (within 1.5 s) minus half the pause before it (at most 0.35 s), the nearest
// word end plus half the pause after it (at most 0.45 s), then repairs the
// length on word boundaries. It falls back to the proposal when it cannot.
func Snap(start, end float64, words []Word, duration, minDur, maxDur float64) (float64, float64) {
	const search, maxLead, maxTail = 1.5, 0.35, 0.45
	orig0, orig1 := round3(start), round3(end)
	if len(words) == 0 {
		return orig0, orig1
	}
	ns, ne := start, end
	if i := nearest(words, start, func(w Word) float64 { return w.Start }, search); i >= 0 {
		lead := maxLead
		if i > 0 {
			lead = math.Min(maxLead, math.Max(0, words[i].Start-words[i-1].End)/2)
		}
		ns = math.Max(0, words[i].Start-lead)
	}
	if i := nearest(words, end, func(w Word) float64 { return w.End }, search); i >= 0 {
		tail := maxTail
		if i+1 < len(words) {
			tail = math.Min(maxTail, math.Max(0, words[i+1].Start-words[i].End)/2)
		}
		ne = math.Min(duration, words[i].End+tail)
	}
	if ne-ns < minDur {
		repaired := false
		for _, w := range words {
			if w.End >= ns+minDur {
				if w.End-ns <= maxDur {
					ne, repaired = math.Min(duration, w.End+0.2), true
				}
				break
			}
		}
		if !repaired {
			return orig0, orig1
		}
	}
	if ne-ns > maxDur {
		limit := ns + maxDur
		best := -1.0
		for _, w := range words {
			if w.End > ns && w.End <= limit {
				best = w.End
			}
		}
		if best > 0 {
			ne = best + 0.2
		} else {
			ne = limit
		}
		ne = math.Min(ne, math.Min(ns+maxDur, duration))
	}
	if ne <= ns || ne-ns < minDur {
		return orig0, orig1
	}
	return round3(ns), round3(ne)
}

func nearest(words []Word, t float64, at func(Word) float64, search float64) int {
	best, bestD := -1, math.Inf(1)
	for i, w := range words {
		if d := math.Abs(at(w) - t); d <= search && d < bestD {
			best, bestD = i, d
		}
	}
	return best
}

func round3(f float64) float64 { return math.Round(f*1000) / 1000 }
