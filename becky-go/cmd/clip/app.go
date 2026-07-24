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
	"becky-go/internal/qmd"
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

	// undoStack/redoStack are the timeline history (Ctrl+Z / Ctrl+Shift+Z). Each
	// CLIP-changing edit (add/remove/reorder/trim/label/load) records the PRE-edit
	// clip state to undoStack and clears redoStack; Undo/Redo move snapshots between
	// the two. The snapshot is clips + name + nextID only — overlay + markers are
	// deliberately NOT included, so undo only ever changes the CLIPS (predictable).
	undoStack []reelSnapshot
	redoStack []reelSnapshot

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

	// questions are the human-review Q&A cards (questions.go), pre-loaded from a
	// <reel>.questions.json sidecar; questionsPath is that file (for the answers file
	// beside it). Empty unless Becky Review was opened via "Open Forensic Hits".
	questions     []ReviewQuestion
	questionsPath string

	// http is the lazily-started loopback media+shell server (server.go).
	http httpState

	cfg config.Config

	// peaksCache holds computed per-clip waveform amplitude buckets (peaks.go),
	// keyed by source+window+bucket-count, so re-rendering the same clip's
	// waveform lane (zoom, reorder, re-open) decodes ffmpeg PCM nothing twice.
	// Guarded by mu like every other App field; nil until the first Peaks call.
	peaksCache map[string]PeaksResult

	// extraFiles are absolute paths of videos the user EXPLICITLY dragged onto the
	// timeline from OUTSIDE the open case folder (item 21 external drag). Only these
	// exact files are accepted by resolveSource / served — a per-FILE allow-list, so
	// dropping one external clip never widens the scope to a whole other folder.
	extraFiles map[string]bool

	// lastSearchHits is the most recent Search() result, in the exact order the
	// GUI displayed it — the referent for a chat "add clip 3"/"add the last clip"
	// (see assistant.resolveHitActions) and for Tier-2's funnel candidates. Reset
	// to nil whenever OpenFolder/Reindex changes the corpus underneath it, so a
	// stale index can never resolve to the wrong clip.
	lastSearchHits []footage.Candidate

	// uiPlayhead / uiSelected / uiThresholdOn / uiThresholdDB are the H-1
	// shared state the editing UI reports through the seek / set_select /
	// set_threshold verbs: the playhead (compilation seconds), the selected
	// clip IDs, and the skip-quiet threshold. Telemetry only — storing them
	// never mutates the reel — but they flow into assistant.Context.Timeline
	// so "delete this clip" / "split here" resolve against where Jordan
	// actually is. Before these existed the C++ app fired all three verbs at
	// a dispatch table that did not know them, so the engine (and the AI)
	// never learned any of it.
	uiPlayhead    float64
	uiSelected    []string
	uiThresholdOn bool
	uiThresholdDB float64

	// emit is H-5's AI-activity sink: set by cmdBridge (main.go) to push
	// {"event":{...}} lines over the NDJSON stdio seam (GUI-RULES.md §2's
	// "event" message kind) so the right panel can show what becky is doing
	// without blocking Jordan's own editing. nil in every path with no
	// listener (unit tests, the WebView2 build, headless `call`) — emitEvent
	// no-ops then, so this is purely additive and never required.
	emit EventEmitter
}

// EventEmitter pushes one plain-language AI-activity line. kind is
// "started"|"progress"|"done" (GUI-RULES.md §2); source names the seam that
// produced it ("ask", "apply_edit_batch"); text is the one sentence a human
// reads. See App.emitEvent for the nil-safe call site.
type EventEmitter func(kind, source, text string)

// emitEvent is the nil-safe call site every long-running/AI-driven verb uses
// to announce activity (H-5). A no-op when nothing is listening — this can
// never block, error, or affect the verb's own return value, so it is safe to
// call from inside a locked section (it never touches App state).
func (a *App) emitEvent(kind, source, text string) {
	if a.emit == nil || text == "" {
		return
	}
	a.emit(kind, source, text)
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
// lower-third is ON by default for a new project (Jordan's preference — a forensic
// review almost always wants the provenance lower-third) showing filename + original
// timecode + date + person + location; he can toggle it off per reel.
func newReel(name string) edl.Reel {
	return edl.Reel{
		Version: "1",
		Name:    name,
		Clips:   []edl.Clip{},
		Overlay: edl.Overlay{
			Enabled:      true,
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

// warmQmd is the seam over qmd.Warm — production never reassigns it; the test
// package's TestMain (clipcolor_test.go) stubs it to a no-op for the whole
// binary so no test's OpenFolder shells the real qmd binary.
var warmQmd = qmd.Warm

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
	a.lastSearchHits = nil // a new corpus invalidates any prior "add clip N" referent
	a.mu.Unlock()

	// Per-source clip colours are frozen PER PROJECT (= this case folder) on
	// disk. Loaded here so a restarted engine wears yesterday's colours, not a
	// fresh first-appearance shuffle. See clipcolor.go.
	LoadClipColors(abs)

	// I-4 (M: becky-review-3-review cycle 18): the first keyword search after a
	// fresh engine boot pays ~7.8-8.0s to parse+cache every transcript (measured
	// on the real 1,136-transcript corpus); every later search is 226-270ms.
	// Pay that cost here, in the background, right after indexing - not on
	// Jordan's first search keystroke.
	go footage.WarmTranscriptCache(idx)

	// Same "pay it before Jordan types" pattern for the smart-search model
	// (qmd.Warm's doc comment: no daemon to keep hot, but this pulls the
	// embedding weights into the OS page cache before his first real query).
	go warmQmd()

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
	// Item 5: open the picker IN the currently-open case folder (Jordan: "open
	// folder needs to open to the last folder, not the default windows navigator").
	a.mu.Lock()
	startDir := a.folder
	a.mu.Unlock()
	dir, err := pickFolderFn(startDir)
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
// seconds (float) and its video frame rate. The frontend uses Duration to clamp
// timeline trim/extend so a clip can't be dragged past the end of its source,
// and Fps to step frame-exact (BUILD_1.md D-2) instead of assuming 30fps on
// every source. Both are 0 when the source isn't probe-able (no ffprobe,
// unreadable, not in the folder) — a degrade, not an error, so the UI just
// falls back to its own bounds/default.
type ProbeResult struct {
	Duration float64 `json:"duration"`
	Fps      float64 `json:"fps"`
}

// Probe returns the duration (seconds) and frame rate of a source video via
// ffprobe. The source must be an indexed video in the open folder (path
// security — probe can only touch originals the case folder already knows).
// Degrade-never-crash: an unresolved source or an ffprobe failure returns
// {duration: 0, fps: 0}, never an error, so the timeline UI keeps working
// without ffprobe. Read-only: the video bytes are only inspected, never written.
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
	return ProbeResult{Duration: info.Duration, Fps: info.FPS}
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
	Date           string  `json:"date,omitempty"` // ISO YYYY-MM-DD from the file name; drives the newest-first sort
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

	// maxPlayable + maxTranscriptOnly bound each group so a pathological common-word
	// search can't return a runaway payload — but they are set HIGH (was 200 each,
	// which silently hid real hits: a known quote past the top 200-by-score never
	// showed, even when its date was known). At 5000 each, any realistic forensic
	// search returns ALL its hits; the UI renders the newest first and tells the user
	// when a search is still too broad to show in full ("Showing X of N — narrow it").
	const maxPlayable = 5000
	const maxTranscriptOnly = 5000

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
			Date:     c.Date,
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
			Date:           c.Date,
			Start:          c.Timestamp,
			End:            c.End,
			Text:           c.Text,
			Timecode:       mmss(c.Timestamp),
			Score:          c.Score,
			TranscriptOnly: true,
		})
	}
	// Forensic scrub-by-time: order hits by the file-name date NEWEST first (today at
	// the top), so scrolling jumps through time fast. Files with no date-coded name
	// fall to the bottom. (The folder LIST stays newest-file-by-mtime — unchanged.)
	sortSearchByDate(out)

	// Remember this exact, as-displayed order so a later "add clip 3" in the chat
	// resolves against what the user actually saw, not the pre-sort grep order.
	a.mu.Lock()
	a.lastSearchHits = searchResultsToCandidates(out)
	a.mu.Unlock()

	return out
}

// searchResultsToCandidates converts a displayed SearchResult list back into
// footage.Candidate — the type assistant.Router.Assist expects for its
// searchHits param — preserving order (the referent for "add clip N").
func searchResultsToCandidates(out []SearchResult) []footage.Candidate {
	cands := make([]footage.Candidate, 0, len(out))
	for _, r := range out {
		cands = append(cands, footage.Candidate{
			Source:    r.Source,
			Name:      r.Name,
			Date:      r.Date,
			Timestamp: r.Start,
			End:       r.End,
			Text:      r.Text,
			Score:     r.Score,
		})
	}
	return cands
}

// sortSearchByDate orders search hits by their file-name date, newest first, with
// undated files last. Within one date, playable hits precede transcript-only ones,
// then by name (groups a file's quotes together) and timestamp (chronological).
func sortSearchByDate(out []SearchResult) {
	sort.SliceStable(out, func(i, j int) bool {
		di, dj := out[i].Date, out[j].Date
		if di != dj {
			if di == "" {
				return false // undated sinks below anything dated
			}
			if dj == "" {
				return true
			}
			return di > dj // ISO dates sort lexically; ">" = newer first
		}
		if out[i].TranscriptOnly != out[j].TranscriptOnly {
			return !out[i].TranscriptOnly // playable above transcript-only
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Start < out[j].Start
	})
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
	// Color is this clip's per-SOURCE colour, "#RRGGBB". The review app already
	// parsed a "color" field and filled the whole clip body with it — but this
	// field did not exist, so that parse always missed and EVERY clip rendered in
	// the one default blue. Jordan's clip colours are an ACCESSIBILITY AID (he is
	// vision-impaired and identifies a clip by its colour at a glance), so five
	// separate requirements were all failing on this one absent field.
	Color string `json:"color,omitempty"`
}

// MarkerView is a timeline marker (compilation-timeline position).
type MarkerView struct {
	At    float64 `json:"at"`
	Label string  `json:"label,omitempty"`
}

// AddClip appends a clip {source,in,out,label} to the reel. See AddClipAt.
func (a *App) AddClip(source string, in, out float64, label string) (TimelineView, error) {
	return a.AddClipAt(source, in, out, label, -1)
}

// AddClipAt inserts a clip {source,in,out,label} at position `at` (a zero-based index
// among the current clips), pulling per-video meta from the read-only sidecar. at<0 or
// past the end APPENDS; otherwise the clip lands at `at` and everything from `at` on is
// pushed back (used to add a clip right after the one under the playhead). The source
// must be a video in the open folder (path security: an add can only reference indexed
// originals — external files come through AddExternalClip). Returns the updated timeline.
func (a *App) AddClipAt(source string, in, out float64, label string, at int) (TimelineView, error) {
	if out < in {
		in, out = out, in
	}
	v, ok := a.resolveSource(source)
	if !ok {
		return TimelineView{}, fmt.Errorf("clip source is not in the open folder: %s", source)
	}

	a.mu.Lock()
	a.pushUndoLocked()
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
	if at < 0 || at >= len(a.reel.Clips) {
		a.reel.Clips = append(a.reel.Clips, clip)
	} else {
		next := make([]edl.Clip, 0, len(a.reel.Clips)+1)
		next = append(next, a.reel.Clips[:at]...)
		next = append(next, clip)
		next = append(next, a.reel.Clips[at:]...)
		a.reel.Clips = next
	}
	a.mu.Unlock()

	return a.Timeline(), nil
}

// AddExternalClip adds a video from ANYWHERE on disk (dragged onto the timeline from
// outside the open case folder — item 21) as one whole clip at index `at` (see
// AddClipAt; at<0 appends): it authorizes the EXACT file (extraFiles, a per-file
// allow-list), probes its duration, and inserts it. A path that isn't a real file is an
// error. The file is only ever opened READ-ONLY (probe/thumbnail/mpv), like every source.
func (a *App) AddExternalClip(path string, at int) (TimelineView, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	abs = filepath.Clean(abs)
	fi, err := os.Stat(abs)
	if err != nil || fi.IsDir() {
		return a.Timeline(), fmt.Errorf("not a file: %s", path)
	}
	a.mu.Lock()
	if a.extraFiles == nil {
		a.extraFiles = map[string]bool{}
	}
	a.extraFiles[abs] = true
	a.mu.Unlock()
	dur := a.Probe(abs).Duration
	if dur <= 0 {
		dur = 3600 // unknown duration -> a generous window the user can trim back
	}
	return a.AddClipAt(abs, 0, dur, "", at)
}

// ClipSpec is one clip to (re)build in SetClips: a source + [in,out] window + label.
type ClipSpec struct {
	Source string  `json:"source"`
	In     float64 `json:"in"`
	Out    float64 `json:"out"`
	Label  string  `json:"label"`
}

// SetClips REPLACES the whole clip list with `specs` as ONE undoable edit — used by the
// "trim to the loud parts" action, which recomputes the timeline as the above-threshold
// segments of the current clips. Each spec's source is re-validated (skipped if unknown /
// out-of-folder) and its meta re-pulled; new stable ids are assigned. Reversible with one
// Ctrl+Z.
func (a *App) SetClips(specs []ClipSpec) (TimelineView, error) {
	built := make([]edl.Clip, 0, len(specs))
	for _, s := range specs {
		in, out := s.In, s.Out
		if out < in {
			in, out = out, in
		}
		if out-in <= 0 {
			continue
		}
		v, ok := a.resolveSource(s.Source) // locks internally — must not hold a.mu here
		if !ok {
			continue
		}
		built = append(built, edl.Clip{
			Source: v.Path,
			In:     clampNonNeg(in),
			Out:    clampNonNeg(out),
			Label:  strings.TrimSpace(s.Label),
			Meta: edl.ClipMeta{
				Date:      v.Meta.Date,
				Link:      v.Meta.Link,
				Person:    v.Meta.Person,
				Location:  v.Meta.Location,
				SourceFPS: v.Meta.SourceFPS,
			},
		})
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pushUndoLocked()
	for i := range built {
		a.nextID++
		built[i].ID = fmt.Sprintf("c%d", a.nextID)
	}
	a.reel.Clips = built
	return a.timelineLocked(), nil
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
	a.pushUndoLocked()
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
	a.pushUndoLocked()
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

// ReorderMany moves a SET of clips (by id) as one contiguous block to position `to`
// (an index among the clips that are NOT being moved), preserving the moved clips'
// relative order. This is ONE undoable edit — a single pushUndoLocked — so dragging a
// multi-selection (item 10) undoes in one Ctrl+Z, unlike calling Reorder per clip. An
// empty/unknown id set is a no-op error.
func (a *App) ReorderMany(ids []string, to int) (TimelineView, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	idset := make(map[string]bool, len(ids))
	for _, id := range ids {
		idset[id] = true
	}
	moved := make([]edl.Clip, 0, len(ids))
	rest := make([]edl.Clip, 0, len(a.reel.Clips))
	for _, c := range a.reel.Clips {
		if idset[c.ID] {
			moved = append(moved, c) // keep timeline order among the moved clips
		} else {
			rest = append(rest, c)
		}
	}
	if len(moved) == 0 {
		return a.timelineLocked(), fmt.Errorf("no clips to move")
	}
	if to < 0 {
		to = 0
	}
	if to > len(rest) {
		to = len(rest)
	}
	a.pushUndoLocked()
	out := make([]edl.Clip, 0, len(a.reel.Clips))
	out = append(out, rest[:to]...)
	out = append(out, moved...)
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
			a.pushUndoLocked()
			// Copy-on-write: pushUndoLocked's snapshot is now an O(1) shallow copy of
			// this slice header (see snapshotLocked) rather than a deep per-clip copy,
			// so it shares this backing array. Writing the trim into a.reel.Clips[i] IN
			// PLACE would silently mutate that snapshot's view of the same index too,
			// corrupting undo. Build a fresh backing array before touching the element.
			next := make([]edl.Clip, len(a.reel.Clips))
			copy(next, a.reel.Clips)
			next[i].In = clampNonNeg(in)
			next[i].Out = clampNonNeg(out)
			a.reel.Clips = next
			return a.timelineLocked(), nil
		}
	}
	return a.timelineLocked(), fmt.Errorf("no clip %q", id)
}

// Split cuts the clip id into two at source time atSource (seconds into the
// source, strictly inside [In,Out]). The left half keeps the id and becomes
// [In, atSource]; a new clip [atSource, Out] (same source/label/meta) is inserted
// directly after it. This is ONE undoable edit — a single pushUndoLocked — so
// Ctrl+Z reverses the whole split at once. Doing the same thing from the client as
// set_trim + add_clip + reorder recorded THREE undo steps, which is why undoing a
// split used to walk backwards through three weird intermediate states. Returns the
// updated timeline and the new right-half clip id. A split too close to either edge
// (< splitEdgeMargin) is a no-op error.
func (a *App) Split(id string, atSource float64) (TimelineView, string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	idx := -1
	for i := range a.reel.Clips {
		if a.reel.Clips[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return a.timelineLocked(), "", fmt.Errorf("no clip %q", id)
	}
	c := a.reel.Clips[idx]
	const splitEdgeMargin = 0.1
	if atSource <= c.In+splitEdgeMargin || atSource >= c.Out-splitEdgeMargin {
		return a.timelineLocked(), "", fmt.Errorf("split point too close to a clip edge")
	}
	a.pushUndoLocked()
	a.nextID++
	right := edl.Clip{
		ID:     fmt.Sprintf("c%d", a.nextID),
		Source: c.Source,
		In:     atSource,
		Out:    c.Out,
		Label:  c.Label,
		Meta:   c.Meta,
	}
	// Copy-on-write: build the new slice from `left` (a modified COPY of c) instead
	// of mutating a.reel.Clips[idx] in place - same undo-snapshot-aliasing hazard as
	// SetTrim above, now that pushUndoLocked's snapshot is a shallow slice-header
	// copy rather than a deep one.
	left := c
	left.Out = atSource // left half keeps the id
	out := make([]edl.Clip, 0, len(a.reel.Clips)+1)
	out = append(out, a.reel.Clips[:idx]...)
	out = append(out, left)
	out = append(out, right)
	out = append(out, a.reel.Clips[idx+1:]...)
	a.reel.Clips = out
	return a.timelineLocked(), right.ID, nil
}

// SetLabel renames a clip's Label.
func (a *App) SetLabel(id, text string) (TimelineView, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.reel.Clips {
		if a.reel.Clips[i].ID == id {
			a.pushUndoLocked()
			// Copy-on-write - see SetTrim's comment above.
			next := make([]edl.Clip, len(a.reel.Clips))
			copy(next, a.reel.Clips)
			next[i].Label = strings.TrimSpace(text)
			a.reel.Clips = next
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
			Color:     clipColor(c.Source),
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

// ---- undo / redo (timeline history) ---------------------------------------

// reelSnapshot is one undo/redo checkpoint: the clip list (see snapshotLocked
// for why this is a shared slice, not a deep copy) plus the reel name and the
// id counter — enough to fully restore the timeline's CLIPS. Overlay + markers
// are intentionally excluded so undo only ever changes clips.
type reelSnapshot struct {
	clips  []edl.Clip
	name   string
	nextID int
}

// maxUndoDepth caps the history so a long session can't grow it without bound.
const maxUndoDepth = 200

// snapshotLocked captures the current clip state. Caller holds a.mu.
//
// This is a SHALLOW copy of the slice header (O(1), no per-clip copy) — safe
// ONLY because every clip mutator now either appends new clips at the tail
// (AddClipAt's fast path — never touches an index an older snapshot can see)
// or builds a brand-new backing array before installing it into a.reel.Clips
// (RemoveClip/Reorder/ReorderMany/SetClips/Split/SetTrim/SetLabel all do
// copy-on-write — see their comments). No mutator may EVER again write
// a.reel.Clips[i].Field in place, or it will silently corrupt every older
// snapshot sharing that backing array (restoreLocked always deep-copies ON
// restore, so a restored timeline is independent either way).
//
// Before this fix, this was a full make+copy of the WHOLE clip list on EVERY
// single edit — O(n) per edit, the root cause of add_clip latency growing to
// 130-172ms past ~5,000 clips (2026-07-23 cycle-25 review). See
// perf_add_clip_test.go for the regression guard (both the latency bound and
// an explicit undo-non-aliasing check across SetLabel/SetTrim/Split).
func (a *App) snapshotLocked() reelSnapshot {
	return reelSnapshot{clips: a.reel.Clips, name: a.reel.Name, nextID: a.nextID}
}

// pushUndoLocked records the CURRENT clip state before a mutation and drops any
// redo branch (a new edit forks history). Caller holds a.mu and is about to mutate
// the reel's clips. This is the ONE place edits become undoable, so every clip
// mutator calls it right before it changes a.reel.Clips.
func (a *App) pushUndoLocked() {
	a.undoStack = append(a.undoStack, a.snapshotLocked())
	if len(a.undoStack) > maxUndoDepth {
		a.undoStack = a.undoStack[len(a.undoStack)-maxUndoDepth:]
	}
	a.redoStack = nil
}

// restoreLocked replaces the live clip state with a snapshot. Caller holds a.mu.
func (a *App) restoreLocked(s reelSnapshot) {
	cl := make([]edl.Clip, len(s.clips))
	copy(cl, s.clips)
	a.reel.Clips = cl
	a.reel.Name = s.name
	a.nextID = s.nextID
}

// Undo reverts the last clip mutation. The second return is false (with the
// timeline unchanged) when there is nothing to undo, so the UI can no-op quietly.
func (a *App) Undo() (TimelineView, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.undoStack) == 0 {
		return a.timelineLocked(), false
	}
	a.redoStack = append(a.redoStack, a.snapshotLocked())
	s := a.undoStack[len(a.undoStack)-1]
	a.undoStack = a.undoStack[:len(a.undoStack)-1]
	a.restoreLocked(s)
	return a.timelineLocked(), true
}

// Redo re-applies the last undone mutation. false when there is nothing to redo.
func (a *App) Redo() (TimelineView, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.redoStack) == 0 {
		return a.timelineLocked(), false
	}
	a.undoStack = append(a.undoStack, a.snapshotLocked())
	s := a.redoStack[len(a.redoStack)-1]
	a.redoStack = a.redoStack[:len(a.redoStack)-1]
	a.restoreLocked(s)
	return a.timelineLocked(), true
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
	// Per-source clip colours are frozen PER PROJECT, and the project is the
	// open CASE FOLDER (open_folder's a.folder) - not wherever this particular
	// reel file happens to sit. A reel commonly lives in a "Rendered" OUTPUT
	// subfolder (excluded from browse/search - see footage/discover.go), one
	// level below the actual footage, so filepath.Dir(path) alone is a
	// DIFFERENT directory than the case folder. That keyed two different
	// on-disk colour files for the same project depending on which loaded
	// last (reel vs. folder) - the "colours going wild" regression (item 11,
	// 2026-07-23). Prefer the already-open case folder; fall back to the
	// reel's own folder only when no folder has been opened yet this session
	// (the forensic launcher's reel-before-folder boot order). See clipcolor.go.
	a.mu.Lock()
	colorDir := a.folder
	a.mu.Unlock()
	if colorDir == "" {
		colorDir = filepath.Dir(path)
	}
	LoadClipColors(colorDir)
	// Item 26: colour the reel's sources STRICTLY in first-appearance order so the timeline
	// always shows the palette in reel order (#14FF39, #00AEEF, ...), overriding any stale or
	// foreign-polluted per-folder colour history that used to scramble it into "random".
	{
		srcs := make([]string, 0, len(r.Clips))
		for _, c := range r.Clips {
			srcs = append(srcs, c.Source)
		}
		ReseedClipColorsInOrder(srcs)
	}
	a.mu.Lock()
	a.pushUndoLocked()
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
	extra := a.extraFiles[abs]
	a.mu.Unlock()

	// Serve paths under the case folder, the work dir, OR an explicitly dragged-in
	// external file (item 21) — the last is a single authorized path, not a folder.
	if !underRoot(abs, folder) && !underRoot(abs, work) && !extra {
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
	// A file the user explicitly dragged in from outside the folder (item 21). No
	// sidecar meta — external footage has no forensic sidecar; the path is enough.
	if a.extraFiles[abs] {
		return footage.Video{Path: abs, Name: baseName(abs)}, true
	}
	base := baseName(source)
	for _, v := range a.index.Videos {
		if v.Name == base {
			return v, true
		}
	}
	return footage.Video{}, false
}

// resolveSourceForRead is resolveSource plus "and if the file is simply THERE on
// disk, that counts too" — for READ-ONLY operations like extracting a thumbnail.
//
// resolveSource itself must stay strict: AddClip deliberately refuses a source
// outside the open folder unless it was explicitly dragged in (a.extraFiles),
// and two tests enforce that boundary. Loosening it there was tried and broke
// them, correctly.
//
// But a REEL CAN REFERENCE FOOTAGE FROM ANYWHERE — that is the whole point of
// loading a Vegas EDL. Loading Jordan's post_constantly edit (footage on
// X:\Videos\...) while the library browsed E:\TakingBack2007 made all 88 clips
// unresolvable, so Thumb() bailed before it ever reached the extractor: every
// clip on the timeline drew the black "no thumbnail" placeholder, permanently,
// with nothing cached and nothing retried.
//
// Reading a frame out of a file the user already has open in their own edit is
// not a boundary worth defending; adding it to the reel is.
func (a *App) resolveSourceForRead(source string) (footage.Video, bool) {
	if v, ok := a.resolveSource(source); ok {
		return v, true
	}
	abs, err := filepath.Abs(source)
	if err != nil {
		abs = source
	}
	abs = filepath.Clean(abs)
	if fi, statErr := os.Stat(abs); statErr == nil && !fi.IsDir() {
		return footage.Video{Path: abs, Name: baseName(abs)}, true
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
	hits := a.lastSearchHits
	cx := assistant.Context{
		FolderRoot: a.folder,
		Index:      &idx,
		DB:         "", // no forensic.db wired in the GUI yet (Tier-0 grep covers search)
		Timeline:   a.timelineStateLocked(),
		Online:     online,
		Budget:     a.budget(),
	}
	a.mu.Unlock()

	// H-5: bracket the turn with started/done events so the right panel shows
	// AI activity for a turn that can run up to askReply's 300s deadline (an
	// online Tier-2 call) — WITHOUT touching the timeline lock, so Jordan's own
	// editing (which runs through the same App under a.mu) is never blocked by
	// this being slow.
	a.emitEvent("started", "ask", "Thinking: "+truncateText(strings.TrimSpace(utterance), 80))

	// Assist is the CHAT brain (not the action-only Handle): a Tier-0 command runs
	// instantly, a "find every time X" ask runs the retrieval funnel, and anything
	// else (a question, a fuzzy request) is ANSWERED by Claude (CLI/OAuth or API
	// key) when available — so becky is a real assistant, not a keyword grep.
	// hits is the last Search()/QmdSearch() result — lets Tier-0's "add clip 3"
	// resolve a real source/in/out (assistant.resolveHitActions) and gives Tier-2's
	// funnel real candidates instead of nothing.
	p, err := r.Assist(ctx, utterance, cx, hits)
	if err != nil {
		a.emitEvent("done", "ask", "Could not answer: "+truncateText(err.Error(), 80))
		return p, err
	}
	a.emitEvent("done", "ask", p.PreviewText)
	return p, nil
}

// ---- H-1 shared state (UI → engine telemetry) -----------------------------

// SetPlayhead records the editor's playhead in compilation seconds. Fired by
// the UI's coalescing seek worker on every scrub/settle; always succeeds.
func (a *App) SetPlayhead(t float64) map[string]any {
	if t < 0 {
		t = 0
	}
	a.mu.Lock()
	a.uiPlayhead = t
	a.mu.Unlock()
	return map[string]any{"t": t}
}

// SetSelection records which clip IDs are selected in the UI. An empty list is
// a real state (nothing selected), not an error.
func (a *App) SetSelection(ids []string) map[string]any {
	a.mu.Lock()
	a.uiSelected = append([]string(nil), ids...)
	a.mu.Unlock()
	return map[string]any{"selected": len(ids)}
}

// SetThreshold records the UI's skip-quiet toggle and its dB level.
func (a *App) SetThreshold(on bool, levelDB float64) map[string]any {
	a.mu.Lock()
	a.uiThresholdOn = on
	a.uiThresholdDB = levelDB
	a.mu.Unlock()
	return map[string]any{"on": on, "level": levelDB}
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
		Playhead:    a.uiPlayhead,
		Selected:    append([]string(nil), a.uiSelected...),
		SkipQuietOn: a.uiThresholdOn,
		SkipQuietDB: a.uiThresholdDB,
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
//
// H-4/H-6: every CLIP-mutating action (add_clip/remove_clip/reorder/set_label)
// in the proposal is queued into ONE apply_edit_batch call instead of being
// applied one-by-one — each of AddClip/RemoveClip/Reorder/SetLabel pushes its
// OWN undo snapshot, so a 5-action AI pass used to cost 5 separate Ctrl+Z
// presses to fully revert. Routing them through ApplyEditBatch makes the whole
// approved pass ONE undo span, which is the entire point of H-4 ("Jordan's
// 90-100% + flare-pass model requires cheap wholesale rejection"). set_marker
// and set_overlay stay outside the batch (they were already excluded from clip
// undo — see reelSnapshot's doc comment — and act on different state, so
// interleaving order with the clip ops doesn't matter).
func (a *App) applyActions(actions []assistant.Action) {
	var ops []EditOp
	for _, act := range actions {
		switch act.Verb {
		case assistant.VerbAddClip:
			ops = append(ops, EditOp{Verb: "add_clip", Args: map[string]any{
				"source": argStr(act, "source"),
				"in":     tcOrSeconds(argStr(act, "in")),
				"out":    tcOrSeconds(argStr(act, "out")),
				"label":  argStr(act, "label"),
			}})
		case assistant.VerbRemoveClip:
			if id := argStr(act, "id"); id != "" {
				ops = append(ops, EditOp{Verb: "remove_clip", Args: map[string]any{"id": id}})
			}
		case assistant.VerbReorder:
			if id := argStr(act, "id"); id != "" {
				ops = append(ops, EditOp{Verb: "reorder", Args: map[string]any{
					"id": id, "to": atoiSafe(argStr(act, "to")),
				}})
			}
		case assistant.VerbSetLabel:
			if id := argStr(act, "id"); id != "" {
				ops = append(ops, EditOp{Verb: "set_label", Args: map[string]any{
					"id": id, "text": argStr(act, "text"),
				}})
			}
		case assistant.VerbSetMarker:
			a.AddMarker(tcOrSeconds(argStr(act, "at")), argStr(act, "label"))
		case assistant.VerbSetOverlay:
			a.applyOverlayAction(argStr(act, "field"), argStr(act, "value"))
		}
	}
	if len(ops) > 0 {
		_, _, _ = a.ApplyEditBatch(ops)
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
