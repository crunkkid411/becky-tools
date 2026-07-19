package main

// qmd.go is becky-review's "smart" transcript search: it runs Stage-1 RECALL via the
// shared internal/qmd client and resolves every candidate back to the PRECISE .srt cue
// (the .md corpus qmd indexes is only a LOCATOR with fewer timestamps). Read-only: it
// shells qmd and reads .srt sidecars; it never writes. The Vulkan/index-pin env + the
// hybrid->BM25 fallback live in internal/qmd so the judge (cmd/becky-judge) shares them.

import (
	"math"
	"strconv"
	"strings"

	"becky-go/internal/footage"
	"becky-go/internal/qmd"
	"becky-go/internal/sidecar"
)

// QmdResult is the qmd_search reply: resolved results (same shape as keyword Search)
// plus which engine actually answered (hybrid vs keyword fallback vs unavailable).
type QmdResult struct {
	Results []SearchResult `json:"results"`
	Mode    string         `json:"mode"`
	Note    string         `json:"note,omitempty"`
}

// QmdSearch runs the smart transcript search and resolves every hit to a precise .srt
// cue. An empty query returns nothing (not an error).
func (a *App) QmdSearch(query string) QmdResult {
	query = strings.TrimSpace(query)
	if query == "" {
		return QmdResult{Results: []SearchResult{}, Mode: "keyword"}
	}
	hits, mode, note := qmd.Search(query)
	out := make([]SearchResult, 0, len(hits))
	seen := map[string]bool{}
	for _, h := range hits {
		r, ok := a.resolveQmdHit(h)
		if !ok || strings.TrimSpace(r.Text) == "" {
			continue // skip unresolvable or blank (frontmatter-only) hits — no useful row
		}
		key := r.Source + "|" + strconv.FormatFloat(math.Round(r.Start*10)/10, 'f', 1, 64)
		if seen[key] {
			continue // a window can resolve to the same cue as another — keep one
		}
		seen[key] = true
		out = append(out, r)
	}
	// Same referent-tracking as keyword Search: "add clip 3" after a smart search
	// should resolve against what was actually shown, not go stale.
	a.mu.Lock()
	a.lastSearchHits = searchResultsToCandidates(out)
	a.mu.Unlock()
	return QmdResult{Results: out, Mode: mode, Note: note}
}

// resolveQmdHit maps one qmd hit to a SearchResult: find the indexed video by its
// transcript name, then snap the snippet's coarse timecode to the PRECISE .srt cue.
// A hit whose video isn't in the open folder is returned as a transcript-only result
// (shown, not playable). false only when the source can't be determined at all.
func (a *App) resolveQmdHit(h qmd.Hit) (SearchResult, bool) {
	srtName := qmd.SourceName(h)
	if srtName == "" {
		return SearchResult{}, false
	}
	t := qmd.FirstTimecode(h.Snippet)
	v, ok := a.videoByTranscript(srtName)
	if !ok {
		return SearchResult{
			Source:         "",
			Name:           strings.TrimSpace(h.Title),
			Start:          maxF(t, 0),
			Text:           qmd.CleanSnippet(h.Snippet),
			Timecode:       mmss(maxF(t, 0)),
			Score:          h.Score,
			TranscriptOnly: true,
		}, true
	}
	start, end, text := a.resolveCue(v.TranscriptPath, t)
	if strings.TrimSpace(text) == "" {
		text = qmd.CleanSnippet(h.Snippet) // degrade to the qmd window text
	}
	return SearchResult{
		Source:   v.Path,
		Name:     v.Name,
		Date:     v.Meta.Date,
		Start:    start,
		End:      end,
		Text:     text,
		Timecode: mmss(start),
		Score:    h.Score,
	}, true
}

// videoByTranscript finds the indexed video whose transcript sidecar basename matches
// srtName (case-insensitive). This is how a qmd .md hit reaches a playable video.
func (a *App) videoByTranscript(srtName string) (footage.Video, bool) {
	want := strings.ToLower(baseName(srtName))
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, v := range a.index.Videos {
		if v.HasTranscript && strings.ToLower(baseName(v.TranscriptPath)) == want {
			return v, true
		}
	}
	return footage.Video{}, false
}

// resolveCue parses the real .srt and returns the PRECISE cue nearest time t (the .md
// only keeps coarse timecodes, so we snap to the actual transcript). t<0 (no timecode
// in the snippet) returns the first cue. A missing/unparseable .srt degrades to t.
func (a *App) resolveCue(srtPath string, t float64) (float64, float64, string) {
	sub, err := sidecar.ParseSubtitle(srtPath)
	if err != nil || len(sub.Segments) == 0 {
		return maxF(t, 0), 0, ""
	}
	if t < 0 {
		s := sub.Segments[0]
		return s.Start, s.End, s.Text
	}
	best := sub.Segments[0]
	bestD := math.Abs(best.Start - t)
	for _, s := range sub.Segments {
		if d := math.Abs(s.Start - t); d < bestD {
			bestD, best = d, s
		}
	}
	return best.Start, best.End, best.Text
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
