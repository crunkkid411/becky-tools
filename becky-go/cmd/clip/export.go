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

	if strings.TrimSpace(outPath) == "" {
		dir, err := a.renderDir()
		if err != nil {
			return ExportResult{}, err
		}
		outPath = filepath.Join(dir, slugName(r.Name)+"_reel.mp4")
	}
	outPath = absOut(outPath)

	res, err := reel.Render(r, reel.Options{Output: outPath})
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

	return ExportResult{
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
	dir, err := a.renderDir()
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

// absOut returns the cleaned absolute form of an output path (best-effort).
func absOut(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}
