// ocr.go — the frame-gathering and I/O types for becky-ocr.
//
// It owns: (1) gathering frames-to-OCR from a becky-osint manifest or a frames
// dir, with provenance; (2) turning each internal/vision.RunOCR result into a
// FrameResult, splitting asserted (high-confidence) lines from flagged
// low-confidence ones and attaching the cheap candidate_* category; (3) the
// stdout JSON output contract.
//
// The actual OCR engine call (materializing the embedded PaddleOCR/RapidOCR
// Python helper, running it, parsing its JSON) lives in internal/vision
// (RunOCR) as of 2026-07-10 (P1 slice B, becky-AI-Agent-review-1.md) — moved
// out of this package so cmd/vision's escalation ladder can call the SAME
// engine for its mandatory OCR corroboration step instead of duplicating it.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/vision"
)

// FrameToOCR is one frame queued for OCR, with the provenance carried from the
// becky-osint manifest/sidecar (or synthesized for a bare frames-dir input).
type FrameToOCR struct {
	FramePath    string
	SourceFile   string
	SourceSHA256 string
	Timestamp    float64
	FrameIndex   int
}

// Line is one recognized line in the becky-ocr output: text, confidence, where on
// the frame, and a cheap candidate_* category for triage/search.
type Line struct {
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence"`
	BBox       []int   `json:"bbox,omitempty"`
	Category   string  `json:"category"`
}

// FrameResult is one frame's OCR output with full provenance. Lines are the
// ASSERTED (>= min-confidence) reads; LowConfidenceLines are flagged, not hidden.
type FrameResult struct {
	FramePath          string  `json:"frame_path"`
	SourceFile         string  `json:"source_file"`
	SourceSHA256       string  `json:"source_sha256,omitempty"`
	Timestamp          float64 `json:"timestamp"`
	FrameIndex         int     `json:"frame_index"`
	RotationApplied    int     `json:"rotation_applied"`
	Lines              []Line  `json:"lines"`
	LowConfidenceLines []Line  `json:"low_confidence_lines"`
	FullText           string  `json:"full_text"`
}

// SkipRecord notes a frame that produced no usable OCR result and why.
type SkipRecord struct {
	FramePath string `json:"frame_path"`
	Reason    string `json:"reason"`
}

// Output is the becky-ocr stdout/--output JSON document. OK is always true
// here — a hard usage/fatal error never reaches this struct; it short-circuits
// through failJSON's separate {"ok":false,"error":"..."} envelope instead (see
// main.go). Kept on this struct too (not just the error path) so a caller can
// check one field name across both outcomes.
type Output struct {
	OK             bool              `json:"ok"`
	Tool           string            `json:"tool"`
	Engine         string            `json:"engine"`
	SourceManifest string            `json:"source_manifest"`
	FramesOCRd     int               `json:"frames_ocrd"`
	RowsWritten    int               `json:"rows_written,omitempty"`
	Results        []FrameResult     `json:"results"`
	Skipped        []SkipRecord      `json:"skipped"`
	Notes          map[string]string `json:"notes"`
}

// buildFrameResult turns one helper frame result into a FrameResult: it attaches
// provenance, splits asserted vs low-confidence lines at minConf, assigns each line
// a candidate_* category, and builds the asserted-only full_text (so the index gets
// the text we stand behind, not the shaky reads).
func buildFrameResult(f FrameToOCR, hr vision.OCRFrame, minConf float64) FrameResult {
	res := FrameResult{
		FramePath:          f.FramePath,
		SourceFile:         f.SourceFile,
		SourceSHA256:       f.SourceSHA256,
		Timestamp:          f.Timestamp,
		FrameIndex:         f.FrameIndex,
		RotationApplied:    hr.RotationApplied,
		Lines:              []Line{},
		LowConfidenceLines: []Line{},
	}
	var asserted []string
	for _, hl := range hr.Lines {
		text := strings.TrimSpace(hl.Text)
		if text == "" {
			continue
		}
		ln := Line{
			Text:       text,
			Confidence: round2(hl.Confidence),
			BBox:       hl.BBox,
			Category:   categorize(text),
		}
		if hl.Confidence >= minConf {
			res.Lines = append(res.Lines, ln)
			asserted = append(asserted, text)
		} else {
			res.LowConfidenceLines = append(res.LowConfidenceLines, ln)
		}
	}
	res.FullText = strings.Join(asserted, "\n")
	return res
}

// gatherFrames collects the frames to OCR + their provenance, from a
// becky-osint manifest, a frames dir, or a single image. Exactly one of
// manifestPath/framesDir/imagePath is non-empty (enforced by main.go's usage
// check before this is called). Returns the frames, a label for the
// source_manifest field, and any fatal error.
func gatherFrames(manifestPath, framesDir, imagePath string, verbose bool) ([]FrameToOCR, string, error) {
	if manifestPath != "" {
		frames, err := framesFromManifest(manifestPath, verbose)
		return frames, filepath.ToSlash(manifestPath), err
	}
	if imagePath != "" {
		frames, err := framesFromImage(imagePath)
		return frames, filepath.ToSlash(imagePath), err
	}
	frames, err := framesFromDir(framesDir, verbose)
	return frames, filepath.ToSlash(framesDir), err
}

// framesFromImage returns a single-frame slice for one image file — the
// --image convention every image-taking becky tool shares (becky-vision
// --image; becky-AI-Agent-review-1.md F6: "becky-vision takes --image;
// becky-ocr takes -frames-dir/-manifest and rejects --image"). Provenance is
// synthesized exactly like framesFromDir's single-file case: source_file is
// the image path itself, frame_index 0.
func framesFromImage(path string) ([]FrameToOCR, error) {
	if !isImage(path) {
		return nil, fmt.Errorf("--image must be a .jpg/.jpeg/.png file, got %s", path)
	}
	if st, err := os.Stat(path); err != nil || st.IsDir() {
		return nil, fmt.Errorf("image not found: %s", path)
	}
	return []FrameToOCR{{
		FramePath:  path,
		SourceFile: filepath.ToSlash(path),
		FrameIndex: 0,
	}}, nil
}

// framesFromManifest reads a becky-osint manifest and returns one FrameToOCR per
// export, carrying the manifest's source_file/source_sha256 + each export's
// frame_path/timestamp/frame_index. Frames whose image is missing are skipped (a
// stale manifest shouldn't abort the run).
func framesFromManifest(path string, verbose bool) ([]FrameToOCR, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m osintManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	base := filepath.Dir(path)
	out := make([]FrameToOCR, 0, len(m.Exports))
	for _, e := range m.Exports {
		fp := resolveFramePath(base, e.FramePath)
		if _, serr := os.Stat(fp); serr != nil {
			beckyio.Logf(verbose, "  skip missing frame: %s", fp)
			continue
		}
		sha := e.SHA256
		if sha == "" {
			sha = m.SourceSHA256
		}
		out = append(out, FrameToOCR{
			FramePath:    fp,
			SourceFile:   m.SourceFile,
			SourceSHA256: sha,
			Timestamp:    e.Timestamp,
			FrameIndex:   e.FrameIndex,
		})
	}
	return out, nil
}

// framesFromDir returns one FrameToOCR per image file in dir (non-recursive),
// synthesizing minimal provenance (source_file = the frame path itself, frame_index
// by sort order). This is the "any folder of frames" path from the spec.
func framesFromDir(dir string, verbose bool) ([]FrameToOCR, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read frames dir: %w", err)
	}
	var names []string
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		if isImage(ent.Name()) {
			names = append(names, ent.Name())
		}
	}
	sort.Strings(names)
	out := make([]FrameToOCR, 0, len(names))
	for i, name := range names {
		fp := filepath.Join(dir, name)
		out = append(out, FrameToOCR{
			FramePath:  fp,
			SourceFile: filepath.ToSlash(fp),
			FrameIndex: i,
		})
	}
	beckyio.Logf(verbose, "  found %d image(s) in %s", len(out), dir)
	return out, nil
}

// resolveFramePath joins a manifest-relative frame path against the manifest's dir
// when it isn't already absolute/existing, so a manifest written with relative
// paths still resolves regardless of the current working directory.
func resolveFramePath(manifestDir, framePath string) string {
	fp := filepath.FromSlash(framePath)
	if filepath.IsAbs(fp) {
		return fp
	}
	if _, err := os.Stat(fp); err == nil {
		return fp
	}
	return filepath.Join(manifestDir, fp)
}

// osintManifest mirrors the subset of the becky-osint manifest this tool consumes.
type osintManifest struct {
	SourceFile   string        `json:"source_file"`
	SourceSHA256 string        `json:"source_sha256"`
	Exports      []osintExport `json:"exports"`
}

// osintExport mirrors one export record in the becky-osint manifest.
type osintExport struct {
	Timestamp  float64 `json:"timestamp"`
	FrameIndex int     `json:"frame_index"`
	FramePath  string  `json:"frame_path"`
	SHA256     string  `json:"sha256"`
}

// ocrID is the deterministic primary key for an OCR line:
// sha12(source_file)+":"+frame_index+":"+line_ordinal. Mirrors the existing
// segment/identification key scheme so re-running becky-ocr is idempotent.
func ocrID(sourceFile string, frameIndex, ordinal int) string {
	return fmt.Sprintf("%s:%d:%d", sha12(sourceFile), frameIndex, ordinal)
}

// sha12 returns the first 12 hex chars of sha256(s), the short-hash convention the
// other becky tables use for deterministic ids.
func sha12(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}

// isImage reports whether name has a frame image extension becky-osint produces.
func isImage(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".jpg" || ext == ".jpeg" || ext == ".png"
}

// marshalIndent renders the output as indented JSON with a trailing newline,
// matching beckyio.PrintJSON's on-stdout shape for --output file writes.
func marshalIndent(o Output) ([]byte, error) {
	b, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal output: %w", err)
	}
	return append(b, '\n'), nil
}

func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }
