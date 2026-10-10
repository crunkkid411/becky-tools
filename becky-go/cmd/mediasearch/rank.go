package main

import (
	"math"
	"sort"

	"becky-go/internal/beckydb"
)

// item is one embedded piece of a source before it is stored: a single frame ("picture"), a few
// frames as one input ("action") or a stretch of sound ("sound").
type item struct {
	Kind  string
	Start float64
	End   float64
	Vec   []float32
}

// Hit is one place in one source that matches the query.
type Hit struct {
	File  string  `json:"file"`
	Kind  string  `json:"kind"`  // picture | action | sound | said (a transcript line) | shot (a becky-shots description)
	Start float64 `json:"start"` // seconds (0 for a still image)
	End   float64 `json:"end"`
	Frame float64 `json:"frame,omitempty"` // best single frame (picture/action), found frame by frame
	Score float64 `json:"score"`           // similarity to the query, 0..1
	What  string  `json:"what,omitempty"`  // shot / said hits: the shot description or the spoken line
	// Gemma is the Gemma 4 double-check: "yes", "no" or "" (not checked), with its own words.
	Gemma     string `json:"gemma,omitempty"`
	GemmaWhy  string `json:"gemma_why,omitempty"`
	Confirmed bool   `json:"confirmed"`
}

// near: windows scoring within this of their group's best count as part of the same moment.
const near = 0.03

// rank turns scored database rows into moments: touching strong windows of the same file and kind
// merge into one hit (a dog barking for 6 s is one moment, not three windows) that keeps its best
// score and best frame. Best first.
func rank(rows []beckydb.MediaHit) []Hit {
	type key struct{ file, kind string }
	groups := map[key][]beckydb.MediaHit{}
	for _, r := range rows {
		k := key{r.SourceFile, r.Kind}
		groups[k] = append(groups[k], r)
	}
	var hits []Hit
	for k, list := range groups {
		sort.Slice(list, func(i, j int) bool { return list[i].Start < list[j].Start })
		best := 0.0
		for _, x := range list {
			best = math.Max(best, x.Similarity)
		}
		cur := -1 // index in hits of the strong moment being extended
		for _, x := range list {
			strong := x.Similarity >= best-near
			if strong && cur >= 0 && x.Start <= hits[cur].End {
				h := &hits[cur]
				h.End = math.Max(h.End, x.End)
				if x.Similarity > h.Score {
					h.Score, h.Frame, h.What = x.Similarity, frameOf(k.kind, x.Start, x.End), x.Label
				}
				continue
			}
			hits = append(hits, Hit{File: k.file, Kind: k.kind, Start: x.Start, End: x.End, Frame: frameOf(k.kind, x.Start, x.End), Score: x.Similarity, What: x.Label})
			cur = -1
			if strong {
				cur = len(hits) - 1
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	return hits
}

func frameOf(kind string, start, end float64) float64 {
	switch kind {
	case "sound":
		return 0
	case "picture":
		return start
	}
	return (start + end) / 2
}

// overlaps reports whether two hits cover the same moment of the same file.
func overlaps(a, b Hit) bool {
	return a.File == b.File && a.Start < b.End+1 && b.Start < a.End+1
}

// top keeps the n best hits, dropping any that repeat a moment already kept (so the list never
// shows the same few seconds as picture, action and sound).
func top(hits []Hit, n int) []Hit {
	var out []Hit
	for _, h := range hits {
		dup := false
		for _, k := range out {
			if overlaps(h, k) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, h)
		}
		if len(out) == n {
			break
		}
	}
	return out
}
