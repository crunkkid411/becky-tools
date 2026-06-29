// becky-clip — the forensic transcript-based video compilation editor
// (SPEC-BECKY-CLIP.md). This file owns the cross-platform App: the in-memory
// edl.Reel (the timeline state), the read-only case-folder index, folder-scoped
// path security, and the engine wiring (footage search, transcript loading, reel
// render/export, the becky assistant). It carries NO build tag, so it
// compiles on every OS and is unit-testable without a window.
//
// The WebView2 window (window_gui.go, //go:build gui && windows) is a thin shell
// over this App: it serves App.MediaHandler over localhost and binds App.Call to
// JS. The headless main (main.go, //go:build !gui || !windows) keeps
// `go build ./...` green everywhere and exposes the same App via a small CLI for
// smoke-testing.
//
// HARD INVARIANTS (CLAUDE.md §2): source videos are NEVER opened for write (only
// the small <video>.beckymeta.json sidecar + chosen output files are written);
// the media server only serves paths under the opened case folder; offline by
// default (the assistant's Tier-2 is opt-in); degrade, never crash.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"becky-go/internal/assistant"
	"becky-go/internal/config"
	"becky-go/internal/edl"
	"becky-go/internal/footage"
	"becky-go/internal/mediainfo"
	"becky-go/internal/sidecar"
)

// App is the becky-clip backend. One App backs one window/session. All mutating
// methods take the lock so the HTTP server goroutine and the JS bridge goroutine
// can call concurrently. The Reel is the single source of truth for the timeline.
type App struct {
	mu sync.Mutex

	// folder is the absolute, cleaned case-folder root. Empty until OpenFolder.
	// Every served media path MUST resolve under it (path security).
	folder string

	// index is the read-only filename+sidecar map of the case folder. Rebuilt on
	// OpenFolder; carries no media bytes.
	index footage.FolderIndex

	// reel is the in-memory timeline (the compilation being assembled). Mutated by
	// add/remove/reorder/overlay; saved/loaded as <name>.reel.json.
	reel edl.Reel

	// reelPath is where Save writes (the last opened/saved .reel.json), or "".
	reelPath string

	// markers are timeline markers kept BESIDE the reel (not part of the edl.Reel
	// render contract, which a parallel agent owns). Compilation-timeline seconds.
	markers []MarkerView

	// nextID is the monotonic counter for stable clip IDs ("c1", "c2", …).
	nextID int

	// router is the becky assistant (cost-tiered). Built lazily on first use
	// so a session with no chat never spawns a model. nil until built.
	router *assistant.Router

	// online toggles the assistant's Tier-2 frontier (Claude) escalation. becky-clip
	// defaults it ON (Jordan explicitly wants the chat backed by his Claude) — the
	// GUI toggle turns it off for pure-offline forensic work.
	online bool

	// workDir is where transient outputs (frame stills, proxies, anchors) land —
	// a becky-owned dir, never the case folder.
	workDir string

	// http is the lazily-started loopback media+shell server (server.go).
	http httpState

	cfg config.Config
}

// NewApp builds an empty App with config loaded and a fresh empty reel. The
// session starts with no folder open and offline (Tier-2 off).
func NewApp() *App {
	a := &App{
		cfg: config.Load(),
		// Default ON so the chat uses Claude (CLI/OAuth or API key) out of the box —
		// the user's explicit ask. Harmless if no frontier backend is present
		// (the assistant just falls to the local model / keyword search).
		online:  true,
		workDir: defaultWorkDir(),
	}
	a.reel = newReel("Untitled compilation")
	return a
}

// newReel returns a fresh empty reel with sane forensic-overlay defaults: the
// lower-third is OFF by default (Jordan toggles it on) but pre-configured so a
// single toggle shows filename + original timecode + date + person + location.
func newReel(name string) edl.Reel {
	return edl.Reel{
		Version: "1",
		Name:    name,
		Clips:   []edl.Clip{},
		Overlay: edl.Overlay{
			Enabled:      false,
			ShowFilename: true,
			ShowTimecode: true,
			ShowDate:     true,
			ShowLink:     true,
			ShowPerson:   true,
			ShowLocation: true,
			Position:     "bottom",
		},
		Created: time.Now().UTC().Format("2006-01-02T15:04:05Z"),
	}
}

// defaultWorkDir is the becky-owned scratch dir for stills/proxies/anchors. It is
// NEVER the case folder (originals stay untouched). Honors BECKY_CLIP_WORKDIR.
func defaultWorkDir() string {
	if d := strings.TrimSpace(os.Getenv("BECKY_CLIP_WORKDIR")); d != "" {
		return d
	}
	return filepath.Join(os.TempDir(), "becky-clip")
}

// ---- folder + index -------------------------------------------------------

// OpenFolder indexes a case folder (read-only) and makes it the media-serving
// scope. It leaves the timeline untouched — the detective can keep their reel
// while switching source folders. Returns the indexed videos for the UI.
func (a *App) OpenFolder(folder string) (FolderView, error) {
	abs, err := filepath.Abs(folder)
	if err != nil {
		abs = folder
	}
	abs = filepath.Clean(abs)
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return FolderView{}, fmt.Errorf("not a folder: %s", folder)
	}
	idx, err := footage.Index(abs)
	if err != nil {
		// Index degrades internally (skips unreadable subtrees); a hard error here
		// is rare, but surface it rather than swallow.
		return FolderView{}, fmt.Errorf("index folder: %w", err)
	}

	a.mu.Lock()
	a.folder = abs
	a.index = idx
	a.mu.Unlock()

	return a.folderView(), nil
}

// PickFolderResult is the reply for the pick_folder verb: whether the user
// actually chose a folder (Picked) and, if so, the indexed FolderView. A
// cancelled dialog returns Picked=false with an empty Folder — a no-op, never an
// error — so the UI simply does nothing.
type PickFolderResult struct {
	Picked bool       `json:"picked"`
	Folder FolderView `json:"folder"`
}

// pickFolderFn is the seam over the OS folder dialog: it defaults to the
// platform pickFolder (Windows FolderBrowserDialog / non-Windows no-op) but can
// be overridden in tests so PickFolder's wiring is exercised without popping a
// real dialog. Production never reassigns it.
var pickFolderFn = pickFolder

// PickFolder opens the native OS "choose folder" dialog (Windows: a real
// FolderBrowserDialog; other OSes: a no-op stub) and, if the user picks one,
// indexes it via OpenFolder — exactly the existing folder-index flow, just fed by
// a real picker instead of a typed path. An empty return (cancelled) is reported
// as Picked=false, not an error. A dialog/exec failure surfaces as an error so
// the UI can fall back to a path prompt.
func (a *App) PickFolder() (PickFolderResult, error) {
	dir, err := pickFolderFn()
	if err != nil {
		return PickFolderResult{}, fmt.Errorf("folder picker failed: %w", err)
	}
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return PickFolderResult{Picked: false}, nil // user cancelled — no-op
	}
	fv, err := a.OpenFolder(dir)
	if err != nil {
		return PickFolderResult{}, err
	}
	return PickFolderResult{Picked: true, Folder: fv}, nil
}

// FolderView is the LEFT-panel payload: the open root + each video with whether
// it has a transcript and a short meta summary. OrphanCount is how many
// transcripts in the tree paired to NO indexed video (their source video is
// absent / still a ".part") — searchable but not playable; the UI can note it so
// a folder of loose transcripts doesn't look empty.
type FolderView struct {
	Root        string      `json:"root"`
	Videos      []VideoView `json:"videos"`
	OrphanCount int         `json:"orphan_count,omitempty"`
}

// VideoView is one indexed video for the UI list.
type VideoView struct {
	Path          string  `json:"path"`
	Name          string  `json:"name"`
	HasTranscript bool    `json:"has_transcript"`
	Date          string  `json:"date,omitempty"`
	Person        string  `json:"person,omitempty"`
	Location      string  `json:"location,omitempty"`
	SourceFPS     float64 `json:"source_fps,omitempty"`
}

func (a *App) folderView() FolderView {
	a.mu.Lock()
	defer a.mu.Unlock()
	// Newest FILE first for the pre-search list: copy the index videos and order
	// them by file modification time, newest at the top, with a stable name tiebreak
	// for determinism. We sort a copy so the shared index keeps its canonical order.
	vids := make([]footage.Video, len(a.index.Videos))
	copy(vids, a.index.Videos)
	sort.SliceStable(vids, func(i, j int) bool {
		if vids[i].Mtime != vids[j].Mtime {
			return vids[i].Mtime > vids[j].Mtime
		}
		return vids[i].Name < vids[j].Name
	})
	fv := FolderView{Root: a.index.Root, Videos: make([]VideoView, 0, len(vids)), OrphanCount: len(a.index.Orphans)}
	for _, v := range vids {
		fv.Videos = append(fv.Videos, VideoView{
			Path:          v.Path,
			Name:          v.Name,
			HasTranscript: v.HasTranscript,
			Date:          v.Meta.Date,
			Person:        v.Meta.Person,
			Location:      v.Meta.Location,
			SourceFPS:     v.Meta.SourceFPS,
		})
	}
	return fv
}

// ---- transcript (the clickable cue list for one video) --------------------

// Cue is one transcript line for the LEFT list: a timestamped, clickable region
// of a specific source. Click → seek; double-click → add_clip.
type Cue struct {
	Source   string  `json:"source"`
	Name     string  `json:"name"`
	Start    float64 `json:"start"`
	End      float64 `json:"end"`
	Text     string  `json:"text"`
	Timecode string  `json:"timecode"` // m:ss for the UI
}

// Transcript returns the parsed cue list for the video named name (basename) in
// the open folder. Degrade-never-crash: no transcript / parse failure yields an
// empty list (not a fatal error).
func (a *App) Transcript(name string) ([]Cue, error) {
	v, ok := a.lookupVideo(name)
	if !ok {
		return nil, fmt.Errorf("no such video in folder: %s", name)
	}
	if !v.HasTranscript {
		return []Cue{}, nil
	}
	sub, err := sidecar.ParseSubtitle(v.TranscriptPath)
	if err != nil {
		return []Cue{}, nil // degrade: a bad transcript shows as "no cues"
	}
	out := make([]Cue, 0, len(sub.Segments))
	for _, seg := range sub.Segments {
		out = append(out, Cue{
			Source:   v.Path,
			Name:     v.Name,
			Start:    seg.Start,
			End:      seg.End,
			Text:     seg.Text,
			Timecode: mmss(seg.Start),
		})
	}
	return out, nil
}

// lookupVideo finds an indexed video by basename (the UI refers to sources by
// name, not absolute path).
func (a *App) lookupVideo(name string) (footage.Video, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.index.VideoByName(name)
}

// ProbeResult is the reply for the probe verb: a source's true duration in
// seconds (float). The frontend uses it to clamp timeline trim/extend so a clip
// can't be dragged past the end of its source. Duration is 0 when the source
// isn't probe-able (no ffprobe, unreadable, not in the folder) — a degrade, not an
// error, so the UI just falls back to its own bounds.
type ProbeResult struct {
	Duration float64 `json:"duration"`
}

// Probe returns the duration (seconds) of a source video via ffprobe. The source
// must be an indexed video in the open folder (path security — probe can only
// touch originals the case folder already knows). Degrade-never-crash: an
// unresolved source or an ffprobe failure returns {duration: 0}, never an error,
// so the timeline UI keeps working without ffprobe. Read-only: the video bytes are
// only inspected, never written.
func (a *App) Probe(source string) ProbeResult {
	v, ok := a.resolveSource(source)
	if !ok {
		return ProbeResult{Duration: 0}
	}
	ff := strings.TrimSpace(os.Getenv("BECKY_FFPROBE"))
	if ff == "" {
		ff = "ffprobe"
	}
	info, err := mediainfo.Probe(ff, v.Path)
	if err != nil || info.Duration < 0 {
		return ProbeResult{Duration: 0}
	}
	return ProbeResult{Duration: info.Duration}
}

// ---- search (keyword across the folder's transcripts) ---------------------

// SearchResult is one transcript hit for the LEFT results list. A video-backed
// hit carries Source = the source video path (click→seek+play, dblclick→add). A
// transcript-only hit (TranscriptOnly=true) comes from an orphaned transcript
// whose video isn't in the folder yet: Source is "", Name is the derived episode
// title, and it is NOT playable/extractable — the GUI shows the quote with an
// honest "transcript only" badge.
type SearchResult struct {
	Source         string  `json:"source"`
	Name           string  `json:"name"`
	Start          float64 `json:"start"`
	End            float64 `json:"end"`
	Text           string  `json:"text"`
	Timecode       string  `json:"timecode"`
	Score          float64 `json:"score"`
	TranscriptOnly bool    `json:"transcript_only,omitempty"`
}

// Search runs the deterministic Tier-0 keyword grep across the open folder's
// transcripts — BOTH the videos that have a transcript (footage.GrepTranscripts,
// playable hits) AND the orphaned transcripts whose video isn't indexed yet
// (footage.GrepOrphans, transcript-only hits). The query is split into terms; an
// empty query returns nothing. Playable hits are listed first (they're
// actionable), then transcript-only hits; each group is ranked best-first and
// separately capped so a flood of either never swamps the panel.
func (a *App) Search(query string) []SearchResult {
	terms := searchTerms(query)
	a.mu.Lock()
	idx := a.index
	a.mu.Unlock()

	// maxPlayable + maxTranscriptOnly cap each group independently: a case with
	// 418 orphan transcripts must not bury the handful of playable hits, and vice
	// versa. The combined list stays bounded for the UI.
	const maxPlayable = 200
	const maxTranscriptOnly = 200

	video := footage.GrepTranscripts(idx, terms)
	if len(video) > maxPlayable {
		video = video[:maxPlayable]
	}
	orphans := footage.GrepOrphans(idx, terms)
	if len(orphans) > maxTranscriptOnly {
		orphans = orphans[:maxTranscriptOnly]
	}

	out := make([]SearchResult, 0, len(video)+len(orphans))
	for _, c := range video {
		out = append(out, SearchResult{
			Source:   c.Source,
			Name:     c.Name,
			Start:    c.Timestamp,
			End:      c.End,
			Text:     c.Text,
			Timecode: mmss(c.Timestamp),
			Score:    c.Score,
		})
	}
	for _, c := range orphans {
		out = append(out, SearchResult{
			Source:         "", // no playable/extractable video for an orphan transcript
			Name:           c.Name,
			Start:          c.Timestamp,
			End:            c.End,
			Text:           c.Text,
			Timecode:       mmss(c.Timestamp),
			Score:          c.Score,
			TranscriptOnly: true,
		})
	}
	return out
}

// searchTerms splits a query into literal grep terms. A fully-quoted query is a
// single literal phrase; otherwise it splits on whitespace. Blank terms drop.
func searchTerms(query string) []string {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil
	}
	if len(q) >= 2 && q[0] == '"' && q[len(q)-1] == '"' {
		inner := strings.TrimSpace(q[1 : len(q)-1])
		if inner != "" {
			return []string{inner}
		}
		return nil
	}
	return strings.Fields(q)
}

// ---- reel mutation (the timeline) -----------------------------------------

// TimelineView is the CENTER-BOTTOM + overlay-state payload the UI renders.
type TimelineView struct {
	Name        string       `json:"name"`
	Clips       []ClipView   `json:"clips"`
	Overlay     edl.Overlay  `json:"overlay"`
	DurationSec float64      `json:"duration_sec"`
	ReelPath    string       `json:"reel_path,omitempty"`
	Markers     []MarkerView `json:"markers,omitempty"`
}

// ClipView is one timeline clip for the strip, with its on-timeline position
// (StartSec) precomputed so the UI lays out a contiguous strip.
type ClipView struct {
	ID        string  `json:"id"`
	Source    string  `json:"source"`
	Name      string  `json:"name"`
	In        float64 `json:"in"`
	Out       float64 `json:"out"`
	StartSec  float64 `json:"start_sec"` // position on the compilation timeline
	DurSec    float64 `json:"dur_sec"`
	Label     string  `json:"label,omitempty"`
	Date      string  `json:"date,omitempty"`
	Person    string  `json:"person,omitempty"`
	Location  string  `json:"location,omitempty"`
	Link      string  `json:"link,omitempty"`
	SourceFPS float64 `json:"source_fps,omitempty"`
}

// MarkerView is a timeline marker (compilation-timeline position).
type MarkerView struct {
	At    float64 `json:"at"`
	Label string  `json:"label,omitempty"`
}

// AddClip appends a clip {source,in,out,label} to the reel, pulling per-video
// meta (date/person/location/fps) from the read-only sidecar. The source must be
// a video in the open folder (path security: an add can only reference indexed
// originals). Returns the updated timeline.
func (a *App) AddClip(source string, in, out float64, label string) (TimelineView, error) {
	if out < in {
		in, out = out, in
	}
	v, ok := a.resolveSource(source)
	if !ok {
		return TimelineView{}, fmt.Errorf("clip source is not in the open folder: %s", source)
	}

	a.mu.Lock()
	a.nextID++
	clip := edl.Clip{
		ID:     fmt.Sprintf("c%d", a.nextID),
		Source: v.Path,
		In:     clampNonNeg(in),
		Out:    clampNonNeg(out),
		Label:  strings.TrimSpace(label),
		Meta: edl.ClipMeta{
			Date:      v.Meta.Date,
			Link:      v.Meta.Link,
			Person:    v.Meta.Person,
			Location:  v.Meta.Location,
			SourceFPS: v.Meta.SourceFPS,
		},
	}
	a.reel.Clips = append(a.reel.Clips, clip)
	a.mu.Unlock()

	return a.Timeline(), nil
}

// RemoveClip drops the clip with the given id. Unknown id is a no-op error.
func (a *App) RemoveClip(id string) (TimelineView, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]edl.Clip, 0, len(a.reel.Clips))
	found := false
	for _, c := range a.reel.Clips {
		if c.ID == id {
			found = true
			continue
		}
		out = append(out, c)
	}
	if !found {
		return a.timelineLocked(), fmt.Errorf("no clip %q", id)
	}
	a.reel.Clips = out
	return a.timelineLocked(), nil
}

// Reorder moves the clip with id to zero-based position to (clamped into range).
// Returns the updated timeline. The move is a stable remove-then-insert.
func (a *App) Reorder(id string, to int) (TimelineView, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	from := -1
	for i, c := range a.reel.Clips {
		if c.ID == id {
			from = i
			break
		}
	}
	if from < 0 {
		return a.timelineLocked(), fmt.Errorf("no clip %q", id)
	}
	if to < 0 {
		to = 0
	}
	if to >= len(a.reel.Clips) {
		to = len(a.reel.Clips) - 1
	}
	if to == from {
		return a.timelineLocked(), nil
	}
	moved := a.reel.Clips[from]
	rest := make([]edl.Clip, 0, len(a.reel.Clips)-1)
	rest = append(rest, a.reel.Clips[:from]...)
	rest = append(rest, a.reel.Clips[from+1:]...)
	out := make([]edl.Clip, 0, len(a.reel.Clips))
	out = append(out, rest[:to]...)
	out = append(out, moved)
	out = append(out, rest[to:]...)
	a.reel.Clips = out
	return a.timelineLocked(), nil
}

// SetTrim updates a clip's In/Out (a manual trim). Out<In is swapped; negatives
// clamp to zero. Returns the updated timeline.
func (a *App) SetTrim(id string, in, out float64) (TimelineView, error) {
	if out < in {
		in, out = out, in
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.reel.Clips {
		if a.reel.Clips[i].ID == id {
			a.reel.Clips[i].In = clampNonNeg(in)
			a.reel.Clips[i].Out = clampNonNeg(out)
			return a.timelineLocked(), nil
		}
	}
	return a.timelineLocked(), fmt.Errorf("no clip %q", id)
}

// SetLabel renames a clip's Label.
func (a *App) SetLabel(id, text string) (TimelineView, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.reel.Clips {
		if a.reel.Clips[i].ID == id {
			a.reel.Clips[i].Label = strings.TrimSpace(text)
			return a.timelineLocked(), nil
		}
	}
	return a.timelineLocked(), fmt.Errorf("no clip %q", id)
}

// SetOverlay toggles or sets one overlay field by name. Boolean fields accept a
// value; "position" accepts "bottom"/"top"; "enabled" toggles the whole
// lower-third. Returns the updated timeline.
func (a *App) SetOverlay(field string, value bool, position string) (TimelineView, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch strings.ToLower(strings.TrimSpace(field)) {
	case "enabled":
		a.reel.Overlay.Enabled = value
	case "filename", "show_filename":
		a.reel.Overlay.ShowFilename = value
	case "timecode", "show_timecode":
		a.reel.Overlay.ShowTimecode = value
	case "date", "show_date":
		a.reel.Overlay.ShowDate = value
	case "link", "show_link":
		a.reel.Overlay.ShowLink = value
	case "person", "show_person":
		a.reel.Overlay.ShowPerson = value
	case "location", "show_location":
		a.reel.Overlay.ShowLocation = value
	case "position":
		p := strings.ToLower(strings.TrimSpace(position))
		if p != "top" {
			p = "bottom"
		}
		a.reel.Overlay.Position = p
	default:
		return a.timelineLocked(), fmt.Errorf("unknown overlay field %q", field)
	}
	return a.timelineLocked(), nil
}

// AddMarker drops a marker at a compilation-timeline position.
func (a *App) AddMarker(at float64, label string) TimelineView {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.markers = append(a.markers, MarkerView{At: clampNonNeg(at), Label: strings.TrimSpace(label)})
	sort.SliceStable(a.markers, func(i, j int) bool { return a.markers[i].At < a.markers[j].At })
	return a.timelineLocked()
}

// Timeline returns the current timeline view (locks internally).
func (a *App) Timeline() TimelineView {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.timelineLocked()
}

// timelineLocked builds the TimelineView; caller holds a.mu.
func (a *App) timelineLocked() TimelineView {
	clips := make([]ClipView, 0, len(a.reel.Clips))
	var cursor float64
	for _, c := range a.reel.Clips {
		dur := c.Dur()
		clips = append(clips, ClipView{
			ID:        c.ID,
			Source:    c.Source,
			Name:      baseName(c.Source),
			In:        c.In,
			Out:       c.Out,
			StartSec:  cursor,
			DurSec:    dur,
			Label:     c.Label,
			Date:      c.Meta.Date,
			Person:    c.Meta.Person,
			Location:  c.Meta.Location,
			Link:      c.Meta.Link,
			SourceFPS: c.Meta.SourceFPS,
		})
		cursor += dur
	}
	mk := make([]MarkerView, len(a.markers))
	copy(mk, a.markers)
	return TimelineView{
		Name:        a.reel.Name,
		Clips:       clips,
		Overlay:     a.reel.Overlay,
		DurationSec: a.reel.Duration(),
		ReelPath:    a.reelPath,
		Markers:     mk,
	}
}

// ---- save / load reel -----------------------------------------------------

// SaveReel writes the in-memory reel to path (or the last reelPath). Only the
// small JSON is written — never a source video.
func (a *App) SaveReel(path string) (string, error) {
	a.mu.Lock()
	if strings.TrimSpace(path) == "" {
		path = a.reelPath
	}
	if strings.TrimSpace(path) == "" {
		path = filepath.Join(a.workDir, slugName(a.reel.Name)+".reel.json")
	}
	r := a.reel
	a.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create reel dir: %w", err)
	}
	if err := edl.Save(path, r); err != nil {
		return "", err
	}
	a.mu.Lock()
	a.reelPath = path
	a.mu.Unlock()
	return path, nil
}

// LoadReel replaces the in-memory reel with one read from path. Clip IDs are
// re-synced so future adds don't collide; markers reset (not part of the reel).
func (a *App) LoadReel(path string) (TimelineView, error) {
	r, err := edl.Load(path)
	if err != nil {
		return TimelineView{}, err
	}
	a.mu.Lock()
	a.reel = r
	a.reelPath = path
	a.nextID = maxClipID(r.Clips)
	a.markers = nil
	a.mu.Unlock()
	return a.Timeline(), nil
}

// ---- path security (the load-bearing forensic guard) ----------------------

// ResolveMediaPath validates that reqPath (an absolute path requested by the
// page) is a real file UNDER the open case folder OR inside the becky work dir
// (for proxies/stills the engine produced). Anything else — traversal, a path
// outside the scope, a directory — is rejected. This is what stops the localhost
// media server from serving arbitrary disk. Read-only by construction (callers
// only ServeFile it).
func (a *App) ResolveMediaPath(reqPath string) (string, bool) {
	if strings.TrimSpace(reqPath) == "" {
		return "", false
	}
	abs, err := filepath.Abs(reqPath)
	if err != nil {
		return "", false
	}
	abs = filepath.Clean(abs)

	a.mu.Lock()
	folder := a.folder
	work := a.workDir
	a.mu.Unlock()

	if !underRoot(abs, folder) && !underRoot(abs, work) {
		return "", false
	}
	fi, err := os.Stat(abs)
	if err != nil || fi.IsDir() {
		return "", false
	}
	return abs, true
}

// underRoot reports whether abs is the root itself or a descendant of it. Both
// are pre-cleaned. An empty root matches nothing (no folder open ⇒ serve
// nothing).
func underRoot(abs, root string) bool {
	if root == "" {
		return false
	}
	root = filepath.Clean(root)
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	// Reject any path that climbs out of root ("..").
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// resolveSource validates that a clip source is an indexed video in the open
// folder and returns it (so add_clip can only reference real originals). It
// matches by cleaned absolute path first, then by basename.
func (a *App) resolveSource(source string) (footage.Video, bool) {
	abs, err := filepath.Abs(source)
	if err != nil {
		abs = source
	}
	abs = filepath.Clean(abs)
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, v := range a.index.Videos {
		if filepath.Clean(v.Path) == abs {
			return v, true
		}
	}
	base := baseName(source)
	for _, v := range a.index.Videos {
		if v.Name == base {
			return v, true
		}
	}
	return footage.Video{}, false
}

// ---- assistant (becky) ----------------------------------------------------

// ensureRouter builds (once) the cost-tiered assistant router with the
// production backends from config. The local model GGUF is BECKY_CLIP_MODEL (a
// text GGUF); the corrections log learns approved proposals. Backends
// self-degrade, so this never fails.
func (a *App) ensureRouter() *assistant.Router {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.router != nil {
		return a.router
	}
	// Let the user supply an Anthropic API key without touching environment
	// variables (his explicit ask) — env first, then a plain-text key file. The
	// claude CLI (Claude Code OAuth) path needs none of this.
	ensureAnthropicKeyEnv(a.workDir)
	localModel := strings.TrimSpace(os.Getenv("BECKY_CLIP_MODEL"))
	if localModel == "" {
		// Default the OFFLINE chat brain to local Gemma-4 E4B (Jordan's rule: local by
		// default, Claude only when "use Claude" is on). Only affects the offline path —
		// online still routes to Claude. BECKY_CLIP_MODEL overrides if set.
		if m, _, _ := a.cfg.GemmaAVLM(); m != "" {
			localModel = m
		}
	}
	corrLog := filepath.Join(a.workDir, "corrections.jsonl")
	a.router = assistant.NewDefaultRouter(
		localModel,
		a.cfg.LlamaServer,
		"opus",             // deep model alias
		"claude-haiku-4-5", // mid model alias
		corrLog,
		func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, "[becky] "+format+"\n", args...)
		},
	)
	return a.router
}

// ensureAnthropicKeyEnv lets a non-dev supply an Anthropic API key WITHOUT setting
// an environment variable (the user's explicit ask): if ANTHROPIC_API_KEY isn't
// already set, it reads a key from BECKY_ANTHROPIC_KEY or a plain-text
// "anthropic_key.txt" placed next to the becky-clip exe, in the work dir, or in the
// user config dir, and exports it so the API backend (built next) picks it up. The
// claude CLI (Claude Code OAuth) path needs none of this. Best-effort: any failure
// just leaves the API backend unavailable (the chat then uses the CLI / local).
func ensureAnthropicKeyEnv(workDir string) {
	if strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")) != "" {
		return
	}
	if k := strings.TrimSpace(os.Getenv("BECKY_ANTHROPIC_KEY")); k != "" {
		_ = os.Setenv("ANTHROPIC_API_KEY", k)
		return
	}
	for _, p := range anthropicKeyFiles(workDir) {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if k := strings.TrimSpace(string(b)); k != "" {
			_ = os.Setenv("ANTHROPIC_API_KEY", k)
			return
		}
	}
}

// anthropicKeyFiles lists the plain-text key-file locations checked in order: next
// to the exe, in the work dir, then the OS user-config dir.
func anthropicKeyFiles(workDir string) []string {
	var paths []string
	if exe, err := os.Executable(); err == nil {
		paths = append(paths, filepath.Join(filepath.Dir(exe), "anthropic_key.txt"))
	}
	if workDir != "" {
		paths = append(paths, filepath.Join(workDir, "anthropic_key.txt"))
	}
	if cfg, err := os.UserConfigDir(); err == nil {
		paths = append(paths, filepath.Join(cfg, "becky-clip", "anthropic_key.txt"))
	}
	return paths
}

// SetOnline toggles the assistant's Tier-2 frontier escalation (opt-in).
func (a *App) SetOnline(on bool) {
	a.mu.Lock()
	a.online = on
	a.mu.Unlock()
}

// Ask runs one becky turn. It assembles the per-turn Context (the compact
// timeline view + the folder index + the online/budget gates), calls the router,
// and returns the Proposal for the UI to render with ✓/✗. Nothing mutates here —
// approval flows through ApplyProposal.
func (a *App) Ask(ctx context.Context, utterance string) (assistant.Proposal, error) {
	r := a.ensureRouter()

	a.mu.Lock()
	idx := a.index
	online := a.online
	cx := assistant.Context{
		FolderRoot: a.folder,
		Index:      &idx,
		DB:         "", // no forensic.db wired in the GUI yet (Tier-0 grep covers search)
		Timeline:   a.timelineStateLocked(),
		Online:     online,
		Budget:     a.budget(),
	}
	a.mu.Unlock()

	// Assist is the CHAT brain (not the action-only Handle): a Tier-0 command runs
	// instantly, a "find every time X" ask runs the retrieval funnel, and anything
	// else (a question, a fuzzy request) is ANSWERED by Claude (CLI/OAuth or API
	// key) when available — so becky is a real assistant, not a keyword grep.
	return r.Assist(ctx, utterance, cx, nil)
}

// BeckyStatus reports which AI backends are usable right now (claude CLI / API key
// / local model) plus the current online toggle, so the GUI can tell the user — in
// plain language — what is powering the chat and how to enable more. It builds the
// router (cheap) to query each backend's Available().
func (a *App) BeckyStatus() assistant.BackendStatus {
	st := a.ensureRouter().Status()
	a.mu.Lock()
	st.Online = a.online
	a.mu.Unlock()
	return st
}

// budget returns a generous per-session Tier-2 budget so opt-in online turns can
// run (a turn cap guards runaway spend). Only consulted when online is on.
func (a *App) budget() *assistant.Budget {
	return &assistant.Budget{MaxUSD: 0, MaxTurns: 40}
}

// timelineStateLocked maps the reel into the assistant's compact view. Caller
// holds a.mu.
func (a *App) timelineStateLocked() assistant.TimelineState {
	clips := make([]assistant.ClipRef, 0, len(a.reel.Clips))
	for _, c := range a.reel.Clips {
		clips = append(clips, assistant.ClipRef{
			ID: c.ID, Source: c.Source, In: c.In, Out: c.Out, Label: c.Label,
		})
	}
	ov := a.reel.Overlay
	return assistant.TimelineState{
		Clips: clips,
		Overlay: map[string]bool{
			"enabled":  ov.Enabled,
			"filename": ov.ShowFilename,
			"timecode": ov.ShowTimecode,
			"date":     ov.ShowDate,
			"person":   ov.ShowPerson,
			"location": ov.ShowLocation,
			"link":     ov.ShowLink,
		},
	}
}

// ApplyProposal applies an approved proposal (the human's ✓): it asks the router
// for the actions, executes the mutating ones against the Reel, and reports which
// external ExecCommands the GUI should run (search/find_quotes shell-outs). The
// router logs the approval for habit learning. Returns the updated timeline +
// the exec commands.
func (a *App) ApplyProposal(id string) (TimelineView, []assistant.ExecCommand, error) {
	r := a.ensureRouter()
	actions, execs, err := r.Apply(id)
	if err != nil {
		return a.Timeline(), nil, err
	}
	a.applyActions(actions)
	return a.Timeline(), execs, nil
}

// RejectProposal discards a pending proposal (the human's ✗).
func (a *App) RejectProposal(id string) {
	a.ensureRouter().Reject(id)
}

// applyActions executes the mutating verbs of an approved proposal against the
// Reel. Read/new-file verbs (search/find_quotes/preview/grab/export) are handled
// by the GUI via ExecCommands or its own handlers; here we apply the timeline
// mutations the assistant proposed.
func (a *App) applyActions(actions []assistant.Action) {
	for _, act := range actions {
		switch act.Verb {
		case assistant.VerbAddClip:
			src := argStr(act, "source")
			in := tcOrSeconds(argStr(act, "in"))
			out := tcOrSeconds(argStr(act, "out"))
			_, _ = a.AddClip(src, in, out, argStr(act, "label"))
		case assistant.VerbRemoveClip:
			if id := argStr(act, "id"); id != "" {
				_, _ = a.RemoveClip(id)
			}
		case assistant.VerbReorder:
			if id := argStr(act, "id"); id != "" {
				_, _ = a.Reorder(id, atoiSafe(argStr(act, "to")))
			}
		case assistant.VerbSetLabel:
			if id := argStr(act, "id"); id != "" {
				_, _ = a.SetLabel(id, argStr(act, "text"))
			}
		case assistant.VerbSetMarker:
			a.AddMarker(tcOrSeconds(argStr(act, "at")), argStr(act, "label"))
		case assistant.VerbSetOverlay:
			a.applyOverlayAction(argStr(act, "field"), argStr(act, "value"))
		}
	}
}

// applyOverlayAction translates a set_overlay action's string value into the
// boolean/position SetOverlay expects.
func (a *App) applyOverlayAction(field, val string) {
	field = strings.ToLower(strings.TrimSpace(field))
	if field == "position" {
		_, _ = a.SetOverlay("position", false, val)
		return
	}
	_, _ = a.SetOverlay(field, truthy(val), "")
}
