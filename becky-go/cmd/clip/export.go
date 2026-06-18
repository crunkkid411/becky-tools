package main

// export.go is the engine-facing half of the App: the wrappers over internal/reel
// (render the compilation MP4, grab a still, build a preview proxy) and
// internal/edl (write the CMX3600 EDL + the re-based SRT). The GUI's Export button
// and the assistant's export/grab_frame verbs route here. Every wrapper opens
// sources READ-ONLY and writes only the chosen output (or a becky work-dir file) —
// the originals are never modified (CLAUDE.md §2).

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"becky-go/internal/edl"
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
}

// ExportReel renders the current reel to a compilation MP4 and writes the EDL +
// re-based SRT beside it. outPath is the MP4 path (or "" → a slugged name in the
// work dir). Returns the produced paths. The render degrades nvenc→libx264 inside
// internal/reel; a missing ffmpeg yields a clear error, never a panic.
func (a *App) ExportReel(outPath string) (ExportResult, error) {
	a.mu.Lock()
	r := a.reel
	work := a.workDir
	a.mu.Unlock()

	if len(r.Clips) == 0 {
		return ExportResult{}, fmt.Errorf("the timeline is empty — add a clip before exporting")
	}

	if strings.TrimSpace(outPath) == "" {
		if err := os.MkdirAll(work, 0o755); err != nil {
			return ExportResult{}, fmt.Errorf("create work dir: %w", err)
		}
		outPath = filepath.Join(work, slugName(r.Name)+"_reel.mp4")
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

	return ExportResult{
		MP4:         res.Output,
		EDL:         edlPath,
		SRT:         srtPath,
		Codec:       res.Codec,
		Clips:       res.Clips,
		DurationSec: res.DurationSec,
		OutputMB:    res.OutputMB,
		Note:        strings.Join(noteParts, "; "),
	}, nil
}

// WriteEDLOnly writes just the CMX3600 EDL for the current reel (no render).
func (a *App) WriteEDLOnly(outPath string) (string, error) {
	a.mu.Lock()
	r := a.reel
	work := a.workDir
	a.mu.Unlock()
	if len(r.Clips) == 0 {
		return "", fmt.Errorf("the timeline is empty")
	}
	if strings.TrimSpace(outPath) == "" {
		outPath = filepath.Join(work, slugName(r.Name)+".edl")
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
	work := a.workDir
	a.mu.Unlock()
	if len(r.Clips) == 0 {
		return "", fmt.Errorf("the timeline is empty")
	}
	if strings.TrimSpace(outPath) == "" {
		outPath = filepath.Join(work, slugName(r.Name)+".srt")
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
	a.mu.Lock()
	work := a.workDir
	a.mu.Unlock()
	if err := os.MkdirAll(work, 0o755); err != nil {
		return "", fmt.Errorf("create work dir: %w", err)
	}
	full := baseName(v.Path)
	stem := strings.TrimSuffix(full, filepath.Ext(full))
	out := filepath.Join(work, fmt.Sprintf("%s_%.3fs.png", slugName(stem), t))
	if err := reel.GrabFrame(v.Path, t, out); err != nil {
		return "", err
	}
	return out, nil
}

// ProxyFor returns a web-playable path for a source: the original if its codec is
// already browser-safe (h264/vp8/vp9/av1), else a freshly-built H.264 proxy in
// the work dir. Used so the <video> preview can decode exotic forensic codecs.
// The source must be in the open folder. ffprobe/ffmpeg absence is a clear error.
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
	return reel.Proxy(v.Path, work)
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

// absOut returns the cleaned absolute form of an output path (best-effort).
func absOut(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}
