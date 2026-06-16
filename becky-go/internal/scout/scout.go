// Package scout assesses a YouTube playlist for content that could improve or
// extend becky-tools. Jordan collects "look at this" videos in playlists the way
// he collects model cards in Chrome (see becky-radar); this tool turns that
// playlist into becky action: for each video it gathers the text becky CAN read
// offline (title, channel, description, tags, captions) and asks one question —
// "does this name something becky should adopt, upgrade, or build?"
//
// It follows becky's forensic discipline (FORENSIC-OUTPUT-PHILOSOPHY.md):
// CORROBORATE, then CONCLUDE. A video becomes a stated "relevant" finding only
// when at least two INDEPENDENT signals agree — e.g. it names a model becky
// already tracks (freshness manifest) AND it sits in a becky capability domain;
// or a becky-domain keyword AND an independent model assessor agreeing. A lone
// weak signal is a "candidate" to review; zero signals is silently skipped (a
// flood of maybes a human must hand-sort is a tool failure, so off-topic videos
// are counted, not enumerated).
//
// Boundaries are interfaces with deterministic fakes so the whole pipeline runs
// in CI with no network and no model: PlaylistSource (the yt-dlp/YouTube fetch —
// the single explicit, logged online step, wired by the local agent) and Assessor
// (the optional local-model opinion). With no Assessor wired the tool degrades to
// a deterministic-only floor — it never crashes.
package scout

import (
	"sort"
	"strings"

	"becky-go/internal/freshness"
	"becky-go/internal/pathx"
)

// Version is the reported tool version string.
const Version = "v1.0.0"

// Video is one playlist entry, normalized to the text becky can read offline.
type Video struct {
	ID          string   `json:"id"`
	URL         string   `json:"url"`
	Title       string   `json:"title"`
	Channel     string   `json:"channel,omitempty"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Transcript  string   `json:"transcript,omitempty"` // captions; may be empty
	Position    int      `json:"position"`             // 1-based order in the playlist
}

// Playlist is a resolved playlist: its identity plus its videos in order.
type Playlist struct {
	ID     string  `json:"id"`
	Title  string  `json:"title,omitempty"`
	URL    string  `json:"url"`
	Videos []Video `json:"videos"`
}

// PlaylistSource resolves a playlist reference (URL or id) into its videos with
// text metadata and, when available, captions. The real implementation shells
// out to yt-dlp (the one online step, logged); the fake returns canned videos so
// matching and corroboration logic is unit-tested with no network.
//
// Local-agent contract (helper op "playlist", or shell yt-dlp directly):
//
//	in : {"ref": string}
//	out: {"id": str, "title": str, "url": str,
//	      "videos": [{"id","url","title","channel","description",
//	                  "tags":[...], "transcript": str, "position": int}, ...]}
//
// yt-dlp recipe: `yt-dlp --flat-playlist -J <ref>` for the entry list, then per
// video `yt-dlp -J --write-auto-subs --sub-format vtt --skip-download <url>` for
// description/tags/channel and the auto-captions (VTT → plain transcript text).
type PlaylistSource interface {
	Playlist(ref string) (Playlist, error)
}

// Assessment is an independent model opinion of one video against becky. It is
// OPTIONAL: when no Assessor is wired the tool runs on the deterministic floor
// alone. When present, a relevant=true verdict counts as one extra independent
// corroborating signal (never the sole basis for a stated conclusion).
type Assessment struct {
	Relevant   bool     `json:"relevant"`
	Tools      []string `json:"becky_tools,omitempty"` // becky tools it relates to
	Ideas      []string `json:"ideas,omitempty"`       // concrete improve/extend ideas
	Kind       string   `json:"kind,omitempty"`        // "improve" | "extend" | ""
	Why        string   `json:"why,omitempty"`
	Confidence float64  `json:"confidence,omitempty"`
}

// Assessor is the model boundary: an independent judgment of a video given
// becky's capability catalog. The real impl drives a local llama.cpp text model
// (Qwen3 / Gemma) with --temp 0 for determinism; the fake is canned.
//
// Local-agent contract (helper op "assess"):
//
//	in : {"video": {title, channel, description, tags, transcript},
//	      "catalog": [{domain, tools:[...], note}]}
//	out: {"relevant": bool, "becky_tools": [...], "ideas": [...],
//	      "kind": "improve|extend", "why": str, "confidence": 0..1}
type Assessor interface {
	Assess(v Video, catalog []Capability) (Assessment, error)
}

// DepMatch records that a video named a model/library becky already tracks — the
// "improve" case (an upgrade candidate for an existing tool), cross-referenced
// into the becky-freshness manifest exactly as becky-radar does for browsing.
type DepMatch struct {
	DependencyID string   `json:"dependency_id"`
	Name         string   `json:"name"`
	UsedBy       []string `json:"used_by"`
	BeckyPinned  string   `json:"becky_pinned"`
}

// Item is one assessed video in the report, with its agreeing signals and the
// plain-language conclusion.
type Item struct {
	Video
	Score      int        `json:"score"`       // count of INDEPENDENT signals that agreed
	Signals    []string   `json:"signals"`     // which fired: dep-match | capability | assessor
	Kind       string     `json:"kind"`        // improve (existing tool) | extend (new)
	BeckyTools []string   `json:"becky_tools"` // becky tools implicated
	DepMatches []DepMatch `json:"dep_matches,omitempty"`
	Ideas      []string   `json:"ideas,omitempty"`
	Verdict    string     `json:"verdict"`
}

// Report is the full deterministic output of a scout run.
type Report struct {
	Tool       string `json:"tool"`
	Playlist   string `json:"playlist"` // the resolved playlist title/URL
	PlaylistID string `json:"playlist_id,omitempty"`
	Assessed   int    `json:"assessed"`   // videos considered
	Relevant   []Item `json:"relevant"`   // ≥2 agreeing signals → CONCLUDED
	Candidates []Item `json:"candidates"` // exactly 1 signal → review
	Skipped    int    `json:"skipped"`    // off-topic videos (counted, not listed)
	Model      string `json:"model"`      // which assessor produced the opinion signal
	Degraded   bool   `json:"degraded"`
	Note       string `json:"note,omitempty"`
}

// Build runs the whole pipeline: resolve the playlist, assess every video
// against becky's capability catalog and freshness manifest, corroborate the
// independent signals, and assemble a stably-sorted report. A source error never
// crashes — it degrades to an empty report with a plain-language note.
//
// assessor may be nil (deterministic floor only). deps is the freshness manifest
// (the set of models/tools becky already tracks). catalog is becky's capability
// map; pass nil to use the built-in DefaultCatalog.
func Build(src PlaylistSource, ref string, deps []freshness.Dependency, catalog []Capability, assessor Assessor) Report {
	if catalog == nil {
		catalog = DefaultCatalog()
	}
	model := "deterministic floor (no model wired)"
	if assessor != nil {
		model = "model-assessed (independent corroborating signal)"
	}
	rep := Report{
		Tool:       "becky-scout " + Version,
		Playlist:   ref,
		Relevant:   []Item{},
		Candidates: []Item{},
		Model:      model,
	}

	pl, err := src.Playlist(ref)
	if err != nil {
		rep.Degraded = true
		rep.Note = "couldn't read the playlist: " + err.Error() +
			" — nothing to assess. Is the playlist URL public and is yt-dlp installed?"
		return rep
	}
	if pl.Title != "" {
		rep.Playlist = pl.Title
	}
	rep.PlaylistID = pl.ID

	for _, v := range pl.Videos {
		rep.Assessed++
		item := assessVideo(v, deps, catalog, assessor)
		switch {
		case item.Score >= 2:
			rep.Relevant = append(rep.Relevant, item)
		case item.Score == 1:
			rep.Candidates = append(rep.Candidates, item)
		default:
			rep.Skipped++
		}
	}
	sortItems(rep.Relevant)
	sortItems(rep.Candidates)
	return rep
}

// assessVideo gathers the independent signals for one video and concludes.
//
// The three signals are deliberately INDEPENDENT so agreement is real evidence:
//  1. dep-match  — names a model/library in becky's freshness manifest (improve).
//  2. capability — sits in a becky capability domain by keyword (relates to a tool).
//  3. assessor   — an optional local model independently calls it relevant.
func assessVideo(v Video, deps []freshness.Dependency, catalog []Capability, assessor Assessor) Item {
	hay := haystack(v)

	depMatches := matchDeps(hay, deps)
	caps := matchCapabilities(hay, catalog)

	it := Item{Video: v, Signals: []string{}, BeckyTools: []string{}}

	// Signal 1: freshness-manifest hit → "improve" (upgrade an existing tool).
	if len(depMatches) > 0 {
		it.Score++
		it.Signals = append(it.Signals, "dep-match")
		it.DepMatches = depMatches
		it.Kind = "improve"
		for _, d := range depMatches {
			it.BeckyTools = appendUnique(it.BeckyTools, d.UsedBy...)
		}
	}

	// Signal 2: becky capability-domain keyword hit → relates to a becky tool.
	if len(caps) > 0 {
		it.Score++
		it.Signals = append(it.Signals, "capability")
		for _, c := range caps {
			it.BeckyTools = appendUnique(it.BeckyTools, c.Tools...)
			it.Ideas = appendUnique(it.Ideas, c.Note)
		}
		if it.Kind == "" {
			// In a becky domain but naming nothing becky already tracks → extend.
			it.Kind = "extend"
		}
	}

	// Signal 3: independent model opinion (optional).
	if assessor != nil {
		if a, err := assessor.Assess(v, catalog); err == nil && a.Relevant {
			it.Score++
			it.Signals = append(it.Signals, "assessor")
			it.BeckyTools = appendUnique(it.BeckyTools, a.Tools...)
			it.Ideas = appendUnique(it.Ideas, a.Ideas...)
			if a.Kind != "" {
				it.Kind = a.Kind
			} else if it.Kind == "" {
				it.Kind = "extend"
			}
		}
	}

	sort.Strings(it.BeckyTools)
	it.Verdict = verdict(it)
	return it
}

// verdict states the conclusion in plain language, scaled to corroboration.
func verdict(it Item) string {
	switch {
	case it.Score == 0:
		return "off-topic for becky"
	case it.Score == 1 && it.Kind == "improve":
		return "CANDIDATE — names a model/tool becky already tracks; one signal only, review whether it's an upgrade."
	case it.Score == 1:
		return "CANDIDATE — touches a becky capability area; one signal only, review whether there's something to build."
	case it.Kind == "improve":
		return "RELEVANT — corroborated: names a model/tool becky tracks AND sits in a becky capability area. Likely an UPGRADE for " +
			strings.Join(it.BeckyTools, ", ") + "."
	default:
		return "RELEVANT — corroborated by " + strings.Join(it.Signals, " + ") +
			": a becky capability area with no tracked tool yet. Likely a tool/model to EXTEND becky with."
	}
}

// matchDeps returns the becky-tracked dependencies named in the haystack — the
// corroborated "improve" case. Mirrors becky-radar's manifest cross-reference.
func matchDeps(hay string, deps []freshness.Dependency) []DepMatch {
	var out []DepMatch
	for _, d := range deps {
		if refMatches(d, hay) {
			out = append(out, DepMatch{
				DependencyID: d.ID,
				Name:         d.Name,
				UsedBy:       d.UsedBy,
				BeckyPinned:  d.Pinned,
			})
		}
	}
	return out
}

// refMatches reports whether dependency d is named in the lower-cased haystack:
// the upstream ref's basename (org/repo → repo, OS-agnostic via pathx.Base) or a
// meaningful (≥5-char) word of the dependency name. Same rule as becky-radar.
func refMatches(d freshness.Dependency, hay string) bool {
	if base := strings.ToLower(pathx.Base(d.Upstream.Ref)); len(base) >= 4 && strings.Contains(hay, base) {
		return true
	}
	for _, w := range strings.Fields(strings.ToLower(d.Name)) {
		if len(w) >= 5 && strings.Contains(hay, w) {
			return true
		}
	}
	return false
}

// haystack builds the lower-cased text to scan for one video.
func haystack(v Video) string {
	var b strings.Builder
	b.WriteString(v.Title)
	b.WriteByte(' ')
	b.WriteString(v.Channel)
	b.WriteByte(' ')
	b.WriteString(v.Description)
	b.WriteByte(' ')
	b.WriteString(strings.Join(v.Tags, " "))
	b.WriteByte(' ')
	b.WriteString(v.Transcript)
	return strings.ToLower(b.String())
}

// appendUnique appends items not already present, preserving order.
func appendUnique(dst []string, items ...string) []string {
	for _, s := range items {
		if s == "" {
			continue
		}
		found := false
		for _, e := range dst {
			if e == s {
				found = true
				break
			}
		}
		if !found {
			dst = append(dst, s)
		}
	}
	return dst
}

// sortItems orders items stably: highest score first, then earliest playlist
// position, then video id — fully deterministic.
func sortItems(items []Item) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Score != items[j].Score {
			return items[i].Score > items[j].Score
		}
		if items[i].Position != items[j].Position {
			return items[i].Position < items[j].Position
		}
		return items[i].ID < items[j].ID
	})
}
