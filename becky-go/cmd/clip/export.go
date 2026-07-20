package main

// export.go is the engine-facing half of the App: the wrappers over internal/reel
// (render the compilation MP4, grab a still, build a preview proxy) and
// internal/edl (write the CMX3600 EDL + the re-based SRT). The GUI's Export button
// and the assistant's export/grab_frame verbs route here. Every wrapper opens
// sources READ-ONLY and writes only the chosen output (or a becky work-dir file) —
// the originals are never modified (CLAUDE.md §2).

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"becky-go/internal/edl"
	"becky-go/internal/mediainfo"
	"becky-go/internal/reel"
	"becky-go/internal/subs"
)

// ExportResult is the payload the UI shows after an export: the MP4 + the sidecar
// EDL/SRT paths, the codec actually used (after any nvenc→libx264 fallback), and
// any degrade note.
type ExportResult struct {
	MP4         string  `json:"mp4"`
	EDL         string  `json:"edl"`
	SRT         string  `json:"srt"`
	Codec       string  `json:"codec"`
	Clips       int     `json:"clips"`
	DurationSec float64 `json:"duration_sec"`
	OutputMB    float64 `json:"output_mb"`
	Note        string  `json:"note,omitempty"`

	// AudioOK + Audio are the always-on post-render CORROBORATION: after the render,
	// becky re-opens the output (read-only) and confirms it actually has AUDIBLE
	// audio (ffprobe stream + ffmpeg volumedetect), so a silent render can never
	// ship unnoticed again. AudioOK is false when the output has no audio stream or
	// is effectively silent; Audio is the plain-language summary the GUI shows.
	AudioOK bool   `json:"audio_ok"`
	Audio   string `json:"audio,omitempty"`

	// Captions is the .srt actually BURNED INTO the MP4 ("" = none). The review
	// apps show this so "did my captions make it into the render?" is answered by
	// the render itself, not by opening the file and hoping. Preview-only captions
	// were a real, day-costing bug — this field is what makes the render honest.
	Captions string `json:"captions,omitempty"`
}

// ExportReel renders the current reel to a compilation MP4 and writes the EDL +
// re-based SRT beside it. outPath is the MP4 path (or "" → a slugged name in the
// work dir). Returns the produced paths. The render degrades nvenc→libx264 inside
// internal/reel; a missing ffmpeg yields a clear error, never a panic.
func (a *App) ExportReel(outPath string) (ExportResult, error) {
	a.mu.Lock()
	r := a.reel
	a.mu.Unlock()

	if len(r.Clips) == 0 {
		return ExportResult{}, fmt.Errorf("the timeline is empty — add a clip before exporting")
	}
	// The captions the reviewer is looking at go INTO the file. The .srt is timed
	// to the whole reel, which is exactly what a full-reel render outputs.
	srt, marginV := a.reelCaptions()
	return a.renderReel(r, outPath, "_reel", srt, marginV)
}

// reelCaptions returns the hand-edited caption .srt sitting beside the OPEN reel
// file, plus the height Jordan set by dragging a caption in the review app —
// i.e. exactly the captions the caption lane is previewing. ("", 0) when no reel
// file is open or it was never captioned, which simply means no captions burn in.
//
// Both "<name>.json" and "<name>.reel.json" are in circulation as reel files and
// becky-subtitle writes "<name>.srt" for either, so both stems are tried. A
// missing .srt is NOT an error — it is the un-captioned case.
func (a *App) reelCaptions() (srt string, marginV int) {
	a.mu.Lock()
	path := a.reelPath
	a.mu.Unlock()
	if strings.TrimSpace(path) == "" {
		return "", 0
	}
	stem := strings.TrimSuffix(path, filepath.Ext(path))
	stems := []string{stem}
	if base := strings.TrimSuffix(stem, ".reel"); base != stem {
		stems = append(stems, base)
	}
	for _, s := range stems {
		cand := s + ".srt"
		if fi, err := os.Stat(cand); err == nil && fi.Size() > 0 {
			return cand, subs.LoadMarginV(cand)
		}
	}
	return "", 0
}

// ExportSelection renders ONLY the clips whose IDs are in ids (kept in their current
// timeline order) to a compilation MP4, with the EDL/SRT sidecars beside it. It uses
// the SAME render path as ExportReel on a filtered copy of the reel, so the selected
// clips export byte-identically to a full export of just those clips. Unknown ids are
// ignored; an empty / all-unknown selection is a clear error (never a silent no-op).
func (a *App) ExportSelection(ids []string, outPath string) (ExportResult, error) {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		if s := strings.TrimSpace(id); s != "" {
			want[s] = true
		}
	}
	a.mu.Lock()
	sub := a.reel   // copy the reel header (version/name/overlay)
	sub.Clips = nil // ...with only the selected clips, in timeline order
	for _, c := range a.reel.Clips {
		if want[c.ID] {
			sub.Clips = append(sub.Clips, c)
		}
	}
	a.mu.Unlock()
	if len(sub.Clips) == 0 {
		return ExportResult{}, fmt.Errorf("no selected clips to render — select one or more clips first")
	}
	// ponytail: no captions on a selection render. The .srt is timed to the WHOLE
	// reel, so dropping clips shifts every later cue off its words — silently wrong
	// captions are worse than none. renderReel says so in the note. Upgrade path:
	// re-base the cues onto the selection's timeline before burning.
	return a.renderReel(sub, outPath, "_selection", "", 0)
}

// renderReel renders r to outPath (or an auto-sequenced <slug><suffix>_NNNN.mp4 in
// the render dir) and writes the EDL + re-based SRT sidecars beside it, then
// corroborates the output actually has audible audio. Shared by ExportReel (whole
// timeline) and ExportSelection (a filtered sub-reel) so both behave identically.
// capSRT (when non-empty) is BURNED INTO the video in the render's own ffmpeg
// pass — the rendered file is the product, so preview-only captions are a bug.
func (a *App) renderReel(r edl.Reel, outPath, suffix, capSRT string, capMarginV int) (ExportResult, error) {
	if strings.TrimSpace(outPath) == "" {
		dir, err := a.renderDir()
		if err != nil {
			return ExportResult{}, err
		}
		// Name by the clips' SOURCE: clips_<sourcestem>_NNNN.mp4 when every clip is from
		// ONE video, else clips_compilation_NNNN.mp4. The _NNNN sequence (next free number)
		// means a re-export never overwrites a previous one.
		_ = suffix // superseded by source-based naming (kept in the signature for callers)
		outPath = nextSequencedPath(dir, exportBaseName(r.Clips), ".mp4")
	}
	outPath = absOut(outPath)

	res, err := reel.Render(r, reel.Options{
		Output:          outPath,
		SubtitleSRT:     capSRT,
		SubtitleMarginV: capMarginV,
	})
	if err != nil {
		return ExportResult{}, err
	}

	// Sidecar EDL + re-based SRT next to the MP4 (best-effort: a write failure is
	// noted but doesn't undo a successful render).
	stem := strings.TrimSuffix(outPath, filepath.Ext(outPath))
	edlPath := stem + ".edl"
	srtPath := stem + ".srt"
	var noteParts []string
	if res.Note != "" {
		noteParts = append(noteParts, res.Note)
	}
	if err := writeTextFile(edlPath, func(w *bufio.Writer) error { return edl.WriteEDL(w, r) }); err != nil {
		noteParts = append(noteParts, "EDL not written: "+err.Error())
		edlPath = ""
	}
	if err := writeTextFile(srtPath, func(w *bufio.Writer) error { return edl.WriteSRT(w, r) }); err != nil {
		noteParts = append(noteParts, "SRT not written: "+err.Error())
		srtPath = ""
	}

	// Always-on corroboration: re-open the render and confirm it actually has
	// AUDIBLE audio (a render whose whole point is the spoken quotes must never be
	// silent). Degrades quietly if ffprobe/ffmpeg is unavailable.
	audioOK, audioNote := a.verifyExportAudio(res.Output)
	if !audioOK && audioNote != "" {
		noteParts = append(noteParts, "AUDIO CHECK: "+audioNote)
	}
	if capSRT == "" && suffix == "_selection" {
		if s, _ := a.reelCaptions(); s != "" {
			noteParts = append(noteParts, "captions NOT burned in: the .srt is timed to the whole reel, so it would sit wrong on a selection — use Render for a captioned file")
		}
	}

	return ExportResult{
		Captions:    capSRT,
		MP4:         res.Output,
		EDL:         edlPath,
		SRT:         srtPath,
		Codec:       res.Codec,
		Clips:       res.Clips,
		DurationSec: res.DurationSec,
		OutputMB:    res.OutputMB,
		Note:        strings.Join(noteParts, "; "),
		AudioOK:     audioOK,
		Audio:       audioNote,
	}, nil
}

// verifyExportAudio re-opens a just-rendered MP4 (READ-ONLY) and corroborates that
// it carries AUDIBLE audio, with two independent signals: ffprobe (an audio stream
// exists) and ffmpeg volumedetect (the mean level is above the silence floor — a
// silent track reads about -91 dB / -inf). It returns (ok, summary): ok=false for
// "no audio stream" or "silent", with a plain-language summary the GUI surfaces.
// Degrade-never-crash: if ffprobe/ffmpeg is absent it returns (false, "") so the UI
// simply makes no audio claim rather than a false one.
func (a *App) verifyExportAudio(mp4 string) (bool, string) {
	cfg := a.cfg
	info, err := mediainfo.Probe(cfg.FFprobe, mp4)
	if err != nil {
		return false, "" // can't probe -> make no claim (honest)
	}
	if !info.HasAudio {
		return false, "no audio stream in the render"
	}
	if vol, ok := mediainfo.MeanVolume(cfg.FFmpeg, mp4); ok {
		return vol.Audible, vol.Describe()
	}
	return true, "audio stream present"
}

// WriteEDLOnly writes just the CMX3600 EDL for the current reel (no render).
func (a *App) WriteEDLOnly(outPath string) (string, error) {
	a.mu.Lock()
	r := a.reel
	a.mu.Unlock()
	if len(r.Clips) == 0 {
		return "", fmt.Errorf("the timeline is empty")
	}
	if strings.TrimSpace(outPath) == "" {
		dir, err := a.renderDir()
		if err != nil {
			return "", err
		}
		outPath = filepath.Join(dir, slugName(r.Name)+".edl")
	}
	outPath = absOut(outPath)
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return "", err
	}
	if err := writeTextFile(outPath, func(w *bufio.Writer) error { return edl.WriteEDL(w, r) }); err != nil {
		return "", err
	}
	return outPath, nil
}

// WriteSRTOnly writes just the re-based SRT for the current reel (no render).
func (a *App) WriteSRTOnly(outPath string) (string, error) {
	a.mu.Lock()
	r := a.reel
	a.mu.Unlock()
	if len(r.Clips) == 0 {
		return "", fmt.Errorf("the timeline is empty")
	}
	if strings.TrimSpace(outPath) == "" {
		dir, err := a.renderDir()
		if err != nil {
			return "", err
		}
		outPath = filepath.Join(dir, slugName(r.Name)+".srt")
	}
	outPath = absOut(outPath)
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return "", err
	}
	if err := writeTextFile(outPath, func(w *bufio.Writer) error { return edl.WriteSRT(w, r) }); err != nil {
		return "", err
	}
	return outPath, nil
}

// GrabFrame extracts one frame-accurate still from a source (must be in the open
// folder) at time t seconds, into the work dir. Returns the PNG path.
func (a *App) GrabFrame(source string, t float64) (string, error) {
	v, ok := a.resolveSource(source)
	if !ok {
		return "", fmt.Errorf("frame source is not in the open folder: %s", source)
	}
	dir, err := a.renderDir()
	if err != nil {
		return "", err
	}
	full := baseName(v.Path)
	stem := strings.TrimSuffix(full, filepath.Ext(full))
	out := filepath.Join(dir, fmt.Sprintf("%s_%.3fs.png", slugName(stem), t))
	if err := reel.GrabFrame(v.Path, t, out); err != nil {
		return "", err
	}
	return out, nil
}

// thumbWidth is the scaled width (px) of a timeline clip thumbnail — small enough
// to inline as a data: URI for many clips, big enough to recognise the scene.
const thumbWidth = 160

// ThumbResult is the reply for the "thumb" verb: a tiny first-frame thumbnail for
// a timeline clip, inlined as a base64 data: URI so the WebView can show it with
// NO media server. Data is "" when ffmpeg is unavailable or the source isn't in
// the open folder — a degrade (the clip just shows no thumbnail), never an error.
type ThumbResult struct {
	Data string `json:"data"`
}

// Thumb returns a small, CACHED first-frame thumbnail for source at time t as a
// base64 image/jpeg data: URI. The source must be an indexed video in the open
// folder (path security: thumbnails only come from originals the case folder
// knows). The JPEG is cached in the work dir keyed by source + time + width, so a
// timeline that re-renders (zoom, reorder, trim) re-extracts nothing. The source
// is opened READ-ONLY. Degrade-never-crash: any failure (no ffmpeg, unresolved
// source, read error) yields {data:""} so the timeline keeps working.
func (a *App) Thumb(source string, t float64) ThumbResult {
	v, ok := a.resolveSource(source)
	if !ok {
		return ThumbResult{}
	}
	dir, err := a.thumbDir()
	if err != nil {
		return ThumbResult{}
	}
	full := baseName(v.Path)
	stem := strings.TrimSuffix(full, filepath.Ext(full))
	out := filepath.Join(dir, fmt.Sprintf("%s_%.3fs_thumb%d.jpg", slugName(stem), t, thumbWidth))
	if _, statErr := os.Stat(out); statErr != nil {
		// Not cached yet — extract it once. If the in-point grab fails (e.g. a
		// truncated download whose transcript in-point is past the last decodable
		// frame), fall back to the last frame near EOF so the clip still gets a
		// thumbnail. Both failing leaves no file and degrades to no thumbnail.
		if gerr := reel.GrabThumb(v.Path, t, out, thumbWidth); gerr != nil {
			if terr := reel.GrabThumbTail(v.Path, out, thumbWidth); terr != nil {
				return ThumbResult{}
			}
		}
	}
	b, err := os.ReadFile(out)
	if err != nil || len(b) == 0 {
		return ThumbResult{}
	}
	return ThumbResult{Data: "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(b)}
}

// EDLResult is the reply for the timeline_edl verb: the path to an mpv EDL file
// that concatenates the current timeline clips into ONE seamless virtual stream,
// plus the total compilation duration (seconds). mpv loads the path and plays the
// trimmed clips GAPLESSLY — no per-clip reload, no blink. Path is "" for an empty
// timeline.
type EDLResult struct {
	Path     string  `json:"path"`
	Duration float64 `json:"duration"`
}

// TimelineEDL writes an mpv EDL ("# mpv EDL v0") describing the current reel — one
// "source,in,length" line per clip, in order — into the work dir, and returns its
// path so the UI can load the whole timeline as a single seamless source. The
// sources are the reel's own (already folder-validated) absolute paths, opened
// READ-ONLY by mpv. The simple EDL line format is comma-delimited, so a clip whose
// source path contains a comma is skipped (extremely rare for these files) rather
// than corrupting the stream. Degrade-never-crash: an empty timeline or a write
// failure yields an empty path, never a panic.
func (a *App) TimelineEDL() (EDLResult, error) {
	a.mu.Lock()
	clips := make([]edl.Clip, len(a.reel.Clips))
	copy(clips, a.reel.Clips)
	work := a.workDir
	a.mu.Unlock()
	if len(clips) == 0 {
		return EDLResult{}, nil
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		return EDLResult{}, fmt.Errorf("create work dir: %w", err)
	}
	var b strings.Builder
	b.WriteString("# mpv EDL v0\n")
	var total float64
	for _, c := range clips {
		length := c.Out - c.In
		if length <= 0 || strings.ContainsRune(c.Source, ',') {
			continue
		}
		// Prefer a FRESH cached windowed scrub proxy (built lazily by the UI) — an
		// intra-frame proxy of just this clip's [in,out) window scrubs far faster than
		// the raw long-GOP source. The proxy may be padded wider than the exact clip
		// (ScrubProxyPadSec), so inOff is whatever offset CachedScrubProxySegment says
		// the window actually starts at inside it (0 for an exact match). When no
		// proxy exists yet, fall back to the raw source at its in-point — today's
		// exact behavior, so a missing/absent proxy can never regress seamless playback.
		src, inOff := c.Source, c.In
		if p, off, ok := reel.CachedScrubProxySegment(c.Source, c.In, c.Out, work); ok && !strings.ContainsRune(p, ',') {
			src, inOff = p, off
		}
		fmt.Fprintf(&b, "%s,%.3f,%.3f\n", src, inOff, length)
		total += length
	}
	out := filepath.Join(work, "timeline.edl")
	if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
		return EDLResult{}, fmt.Errorf("write edl: %w", err)
	}
	return EDLResult{Path: out, Duration: total}, nil
}

// ProxyFor returns a web-playable, SCRUB-FRIENDLY path for a source: a low-res,
// intra-frame, constant-frame-rate proxy built in the work dir (the all-intra
// H.264 scrub proxy is yuv420p+faststart, so the WebView2 <video> plays it and
// frame-stepping is snappy). Unlike a plain web proxy this does NOT pass through
// long-GOP H.264 — that codec is exactly what scrubs slowly, since every seek
// must decode a whole group of pictures (HANDOFF-PROXY-SNAPPINESS.md). The
// source must be in the open folder and is opened READ-ONLY; final export still
// uses the ORIGINAL, never this proxy. ffmpeg absence is a clear error (the
// caller falls back to the original URL so preview still attempts to play).
func (a *App) ProxyFor(source string) (string, error) {
	v, ok := a.resolveSource(source)
	if !ok {
		return "", fmt.Errorf("proxy source is not in the open folder: %s", source)
	}
	a.mu.Lock()
	work := a.workDir
	a.mu.Unlock()
	if err := os.MkdirAll(work, 0o755); err != nil {
		return "", fmt.Errorf("create work dir: %w", err)
	}
	return reel.ScrubProxy(v.Path, work)
}

// ScrubSegmentResult is the reply for the scrub_segment verb: a windowed scrub
// proxy path for ONLY a timeline clip's actual [in,out) span. Path is "" when
// ffmpeg is unavailable or the source isn't in the open folder — a degrade,
// never an error, mirroring Thumb's bridge contract.
type ScrubSegmentResult struct {
	Path string `json:"path"`
}

// ScrubSegment returns a web-playable, scrub-friendly proxy for ONLY the
// [inSec,outSec) window of source — the windowed sibling of ProxyFor (which
// proxies the WHOLE source). Transcoding just the span a timeline clip
// actually uses is far cheaper than a whole-file proxy for a long source with
// one short clip on it ("windowed for only what is on the timeline"). Same
// intra-frame/constant-fps recipe as ProxyFor (reel.ScrubProxySegment), so the
// scrub feel matches. The source must be in the open folder and is opened
// READ-ONLY; final export still uses the ORIGINAL, never this proxy.
// Degrade-never-crash: an unresolved source, an un-orderable/empty window, or
// ffmpeg being unavailable all yield {path:""}, never an error — same
// contract as Thumb, so a missing ffmpeg just leaves the waveform lane's
// preview unavailable rather than failing the bridge call.
func (a *App) ScrubSegment(source string, inSec, outSec float64) ScrubSegmentResult {
	v, ok := a.resolveSource(source)
	if !ok {
		return ScrubSegmentResult{}
	}
	if outSec < inSec {
		inSec, outSec = outSec, inSec
	}
	inSec = clampNonNeg(inSec)
	a.mu.Lock()
	work := a.workDir
	a.mu.Unlock()
	if err := os.MkdirAll(work, 0o755); err != nil {
		return ScrubSegmentResult{}
	}
	// Pad the built window on each side (reel.ScrubProxyPadSec): SRT-derived cut
	// points aren't frame-exact and dragging a trim handle to fine-tune them is
	// normal workflow — a minor adjustment should land inside the ALREADY-BUILT
	// padded proxy (served instantly, see findContainingProxy in internal/reel)
	// instead of invalidating the exact-window cache and paying a fresh
	// raw-source encode on every small drag.
	padIn := clampNonNeg(inSec - reel.ScrubProxyPadSec)
	padOut := outSec + reel.ScrubProxyPadSec
	path, err := reel.ScrubProxySegment(v.Path, padIn, padOut, work)
	if err != nil {
		return ScrubSegmentResult{}
	}
	return ScrubSegmentResult{Path: path}
}

// writeTextFile creates path and runs fn against a buffered writer, flushing on
// success. Never touches any source video — only this output path.
func writeTextFile(path string, fn func(*bufio.Writer) error) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	if err := fn(w); err != nil {
		return err
	}
	return w.Flush()
}

// renderDir is where HUMAN-FACING outputs go: a "render" subfolder of the OPEN
// case folder. This is the Becky Tools protocol — save next to the originals, where
// a human can find them, NEVER in a hidden AppData/temp dir. It is created on
// demand. Writing a NEW file into a NEW subfolder never modifies an original (the
// forensic invariant holds). Only when no folder is open (e.g. a headless call with
// an explicit timeline) does it fall back to the becky work dir so a render still
// has somewhere to land.
// thumbDir is a dedicated subfolder of the render dir for the timeline's clip
// thumbnails, so those many tiny jpegs don't clutter render/ beside the actual
// rendered compilations (Jordan's ask). Created on demand.
func (a *App) thumbDir() (string, error) {
	base, err := a.renderDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "timeline_thumbnails")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create thumbnail dir %q: %w", dir, err)
	}
	return dir, nil
}

func (a *App) renderDir() (string, error) {
	a.mu.Lock()
	folder := a.folder
	work := a.workDir
	a.mu.Unlock()
	dir := work
	if strings.TrimSpace(folder) != "" {
		dir = filepath.Join(folder, "render")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create render dir %q: %w", dir, err)
	}
	return dir, nil
}

// nextSequencedPath returns dir/<base>_NNNN<ext> for the lowest 4-digit NNNN (>=1)
// whose file does not yet exist, so a re-export lands beside the previous one
// (..._0001, _0002, _0003) instead of overwriting it. The .edl/.srt sidecars share
// the chosen stem. The 9999 cap is a runaway guard (no real case hits it).
func nextSequencedPath(dir, base, ext string) string {
	for n := 1; n <= 9999; n++ {
		p := filepath.Join(dir, fmt.Sprintf("%s_%04d%s", base, n, ext))
		if _, err := os.Stat(p); os.IsNotExist(err) {
			return p
		}
	}
	return filepath.Join(dir, base+"_9999"+ext)
}

// exportBaseName builds the export file base from the clips' SOURCE videos:
// "clips_<sourcestem>" when every clip shares ONE source, else "clips_compilation".
// nextSequencedPath appends the _NNNN sequence + extension.
func exportBaseName(clips []edl.Clip) string {
	stem := ""
	for _, c := range clips {
		s := baseName(c.Source)
		s = strings.TrimSuffix(s, filepath.Ext(s))
		if stem == "" {
			stem = s
		} else if s != stem {
			return "clips_compilation"
		}
	}
	if stem == "" {
		return "clips_compilation"
	}
	return "clips_" + slugName(stem)
}

// absOut returns the cleaned absolute form of an output path (best-effort).
func absOut(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}
