package main

// splice.go — sequential quote insertion.
//
// Jordan's edit grammar (2026-08-24): "I mention what he said, then it plays
// a clip of what he said, then it returns to me talking." Quotes are NOT
// overlays that play over his voice - the listener can't listen to two videos
// at once. So the main edit STOPS at each quote marker, the quote plays on its
// own two tracks, and the main edit resumes after it. Everything after a
// quote shifts by the quote's length; a main event the marker falls inside is
// split. The delivered project has exactly four tracks: his video, his audio,
// quotes video, quotes audio.

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
)

// quoteIn is one verified quote clip (see quotes_verify flow): the corpus
// media, the checked in/out, and the marker title it belongs to.
type quoteIn struct {
	Q      string  `json:"q"`
	Source string  `json:"source"`
	In     float64 `json:"in"`
	Out    float64 `json:"out"`
}

type quoteOut struct {
	Source string  `json:"source"`
	In     float64 `json:"in"`
	Out    float64 `json:"out"`
	TL     float64 `json:"tl"`
}

type tlEvent struct {
	Source   string  `json:"source"`
	In       float64 `json:"in"`
	Out      float64 `json:"out"`
	TL       float64 `json:"tl"`
	Dialogue string  `json:"dialogue,omitempty"`
}

type layout struct {
	Events  []tlEvent
	Quotes  []quoteOut
	Markers []markerOut
	Regions []regionOut
}

type regionOut struct {
	T     float64 `json:"t"`
	Len   float64 `json:"len"`
	Label string  `json:"label"`
}

func loadQuotes(path string) ([]quoteIn, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var q []quoteIn
	if err := json.Unmarshal(b, &q); err != nil {
		return nil, err
	}
	return q, nil
}

func sameTitle(a, b string) bool {
	if a == b {
		return true
	}
	if len(a) > 45 && len(b) > 45 {
		return strings.HasPrefix(a, b[:45]) || strings.HasPrefix(b, a[:45])
	}
	return false
}

// spliceLayout inserts the quotes sequentially at their markers and returns
// the final four-track layout. events/markers carry the no-insertion timeline
// positions; the result carries final ones.
//
// BUG FIXED 2026-08-25 (Jordan: "the clips are out of fucking order"): place()
// used to clip against e.In/e.Out directly - SOURCE-clip-relative seconds,
// restarting near 0 for every clip - as if they were timeline positions. Two
// different clips very commonly have overlapping source-relative ranges (clip
// 2 and clip 9 both have SOME content at source-second ~45), so a splice
// window bounded in real timeline coordinates would silently admit whichever
// clip's OWN early seconds happened to undercut the bound, not whichever
// event actually belonged there - scattering every clip's later material
// across later windows instead of keeping it contiguous. e.TLStart (set by
// main.go right before this call: `events[i].TLStart = tl; tl += out-in`) is
// already each event's correct pre-splice timeline position - the same axis
// markers' m.T lives on - and is what place() must clip against instead.
func spliceLayout(events []event, markers []markerOut, quotes []quoteIn) layout {
	type splicePt struct {
		orig float64
		qs   []quoteIn
		len  float64
	}
	var pts []splicePt
	for _, m := range markers {
		var qs []quoteIn
		for _, q := range quotes {
			if sameTitle(q.Q, m.Title) {
				qs = append(qs, q)
			}
		}
		if len(qs) == 0 {
			continue
		}
		var l float64
		for _, q := range qs {
			l += q.Out - q.In
		}
		pts = append(pts, splicePt{m.T, qs, l})
	}
	sort.SliceStable(pts, func(i, j int) bool { return pts[i].orig < pts[j].orig })

	shiftAt := func(orig float64) float64 {
		var s float64
		for _, p := range pts {
			if p.orig < orig {
				s += p.len
			}
		}
		return s
	}

	// place main content whose PRE-SPLICE TIMELINE position (e.TLStart) falls
	// in [a,b) onto the cursor - never e.In/e.Out, see the func doc comment.
	// A window that lands mid-event clips it at the edge and offsets the
	// SOURCE in/out by the same amount, so the emitted sub-event still points
	// at the right seconds of the right clip.
	var out layout
	cursor := 0.0
	place := func(a, b float64) {
		if b <= a {
			return
		}
		for _, e := range events {
			eLo, eHi := e.TLStart, e.TLStart+(e.Out-e.In)
			lo, hi := eLo, eHi
			if lo < a {
				lo = a
			}
			if hi > b {
				hi = b
			}
			if hi <= lo {
				continue
			}
			srcLo := e.In + (lo - eLo)
			srcHi := e.In + (hi - eLo)
			out.Events = append(out.Events, tlEvent{e.Source, srcLo, srcHi, cursor, e.Dialogue})
			cursor += hi - lo
		}
	}

	pos := 0.0
	total := 0.0
	for _, e := range events {
		if end := e.TLStart + (e.Out - e.In); end > total {
			total = end
		}
	}
	for _, p := range pts {
		place(pos, p.orig)
		qt := cursor
		for _, q := range p.qs {
			out.Quotes = append(out.Quotes, quoteOut{q.Source, q.In, q.Out, qt})
			qt += q.Out - q.In
		}
		cursor = qt
		pos = p.orig
	}
	place(pos, total+1)

	for _, m := range markers {
		out.Markers = append(out.Markers, markerOut{m.T + shiftAt(m.T), m.Title})
	}

	// regions: one per source clip over its final main events
	first := map[string]int{}
	for i, e := range out.Events {
		if _, ok := first[e.Source]; !ok {
			first[e.Source] = i
		}
	}
	for src, i := range first {
		last := i
		for j := i; j < len(out.Events) && out.Events[j].Source == src; j++ {
			last = j
		}
		end := out.Events[last].TL + (out.Events[last].Out - out.Events[last].In)
		out.Regions = append(out.Regions, regionOut{out.Events[i].TL, end - out.Events[i].TL, baseName(src)})
	}
	sort.SliceStable(out.Regions, func(i, j int) bool { return out.Regions[i].T < out.Regions[j].T })
	return out
}

func baseName(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
