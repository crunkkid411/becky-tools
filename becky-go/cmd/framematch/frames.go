// frames.go — sample + perceptual-hash frames from ONE source (a video or a
// folder of reference images) into the output dir. Every primitive (frame
// extraction, SHA-256, aHash, provenance sidecar) is reused verbatim from
// internal/osintexport so the provenance format is identical to becky-osint and
// becky-events. The source is only ever READ; all frames are written as COPIES.
package main

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	// Register decoders so we can re-read an extracted frame (or a reference
	// image) and hash it in-process, stdlib only.
	_ "image/jpeg"
	_ "image/png"

	"becky-go/internal/config"
	"becky-go/internal/mediainfo"
	"becky-go/internal/osintexport"
)

// imageExts are the still-image extensions recognized when a source is a folder.
var imageExts = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".bmp": true, ".webp": true}

// sampleSource extracts frames from a video (at the chosen interval) or copies
// + hashes the images in a folder, writing each frame and a provenance sidecar
// into outDir/frames-<label>/. It returns the source facts and the per-frame
// records.
func sampleSource(cfg config.Config, srcPath, label, outDir string,
	interval, fps float64, roiCfg roiConfig, verbose bool) (SourceInfo, []Frame, error) {

	fi, err := os.Stat(srcPath)
	if err != nil {
		return SourceInfo{}, nil, fmt.Errorf("source %s: %w", label, err)
	}
	frameDir := filepath.Join(outDir, "frames-"+label)
	if err := os.MkdirAll(frameDir, 0o755); err != nil {
		return SourceInfo{}, nil, fmt.Errorf("create frame dir %s: %w", frameDir, err)
	}

	if fi.IsDir() {
		return sampleImageFolder(srcPath, label, frameDir, roiCfg, verbose)
	}
	return sampleVideo(cfg, srcPath, label, frameDir, interval, fps, roiCfg, verbose)
}

// sampleVideo extracts one full-resolution frame every `step` seconds across the
// clip, hashing each. interval (seconds) wins if set; otherwise fps (samples/s).
func sampleVideo(cfg config.Config, video, label, frameDir string,
	interval, fps float64, roiCfg roiConfig, verbose bool) (SourceInfo, []Frame, error) {

	info, err := mediainfo.Probe(cfg.FFprobe, video)
	if err != nil {
		return SourceInfo{}, nil, fmt.Errorf("probe %s: %w", label, err)
	}
	if !info.HasVideo {
		return SourceInfo{}, nil, fmt.Errorf("source %s has no video stream", label)
	}
	srcSHA, err := osintexport.SHA256File(video)
	if err != nil {
		return SourceInfo{}, nil, fmt.Errorf("sha256 %s: %w", label, err)
	}

	step := interval
	if step <= 0 {
		if fps <= 0 {
			fps = 1.0
		}
		step = 1.0 / fps
	}
	dur := info.Duration
	if dur <= 0 {
		dur = 1
	}

	src := SourceInfo{
		Label:      label,
		Path:       filepath.ToSlash(video),
		Kind:       "video",
		SHA256:     srcSHA,
		Duration:   round3(info.Duration),
		FPS:        round3(info.FPS),
		Resolution: info.Resolution(),
	}

	var frames []Frame
	idx := 0
	for ts := 0.0; ts < dur; ts += step {
		stem := fmt.Sprintf("%s_%04d_%dms", label, idx, int(ts*1000))
		framePath := filepath.Join(frameDir, stem+".jpg")
		sidecarPath := filepath.Join(frameDir, stem+".json")

		if err := osintexport.ExtractFrame(cfg.FFmpeg, video, ts, framePath, "jpg", 2); err != nil {
			// A single bad seek (e.g. the last partial second) is non-fatal: skip it.
			logf(verbose, "  [%s] frame %d @ %.3fs skipped: %v", label, idx, ts, err)
			idx++
			continue
		}
		hash, roiHash, kp, herr := hashFrameROI(framePath, roiCfg)
		if herr != nil {
			logf(verbose, "  [%s] hash %d @ %.3fs skipped: %v", label, idx, ts, herr)
			idx++
			continue
		}
		side := osintexport.Sidecar{
			SourceFile:     video,
			SourceSHA256:   srcSHA,
			EventType:      "framematch_sample",
			Timestamp:      round3(ts),
			FrameIndex:     idx,
			FPS:            round3(info.FPS),
			Resolution:     info.Resolution(),
			PerceptualHash: hash,
			Notes:          osintexport.ProvenanceNote,
			ExtractedAt:    time.Now().UTC().Format(time.RFC3339),
			Tool:           toolVersion,
		}
		if err := osintexport.WriteProvenance(sidecarPath, side); err != nil {
			return src, nil, fmt.Errorf("write provenance %s: %w", label, err)
		}
		frames = append(frames, Frame{
			SourceLabel: label,
			Index:       idx,
			Timestamp:   round3(ts),
			TimeLabel:   timeLabel(ts),
			Path:        filepath.ToSlash(framePath),
			Sidecar:     filepath.ToSlash(sidecarPath),
			Hash:        hash,
			ROIHash:     roiHash,
			ROIUsed:     roiCfg.spec(),
			Keypoints:   kp,
		})
		logf(verbose, "  [%s] sampled frame %d @ %.3fs (phash %s roi %s)", label, idx, ts, hash, roiHash)
		idx++
	}
	src.FrameCount = len(frames)
	return src, frames, nil
}

// sampleImageFolder hashes every image in a folder (sorted by name) as a source.
// The images are not re-extracted (they ARE the frames); we copy them into the
// frame dir as byte-for-byte passthrough so the exhibit references local copies
// and the originals stay untouched, plus a provenance sidecar per image.
func sampleImageFolder(dir, label, frameDir string, roiCfg roiConfig, verbose bool) (SourceInfo, []Frame, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return SourceInfo{}, nil, fmt.Errorf("read image folder %s: %w", label, err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if imageExts[strings.ToLower(filepath.Ext(e.Name()))] {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return SourceInfo{}, nil, fmt.Errorf("source %s folder has no images", label)
	}

	src := SourceInfo{
		Label: label,
		Path:  filepath.ToSlash(dir),
		Kind:  "images",
	}
	var frames []Frame
	for i, name := range names {
		origPath := filepath.Join(dir, name)
		hash, roiHash, kp, w, h, herr := hashImageFileROI(origPath, roiCfg)
		if herr != nil {
			logf(verbose, "  [%s] image %s skipped: %v", label, name, herr)
			continue
		}
		if src.Resolution == "" {
			src.Resolution = fmt.Sprintf("%dx%d", w, h)
		}
		// Copy the image into the frame dir so the exhibit references a local copy.
		stem := fmt.Sprintf("%s_%04d", label, i)
		copyExt := strings.ToLower(filepath.Ext(name))
		framePath := filepath.Join(frameDir, stem+copyExt)
		if cerr := copyFile(origPath, framePath); cerr != nil {
			return src, nil, fmt.Errorf("copy image %s: %w", name, cerr)
		}
		sha, _ := osintexport.SHA256File(origPath)
		sidecarPath := filepath.Join(frameDir, stem+".json")
		side := osintexport.Sidecar{
			SourceFile:     origPath,
			SourceSHA256:   sha,
			EventType:      "framematch_reference_image",
			Timestamp:      float64(i),
			FrameIndex:     i,
			Resolution:     fmt.Sprintf("%dx%d", w, h),
			PerceptualHash: hash,
			Notes:          osintexport.ProvenanceNote,
			ExtractedAt:    time.Now().UTC().Format(time.RFC3339),
			Tool:           toolVersion,
		}
		if err := osintexport.WriteProvenance(sidecarPath, side); err != nil {
			return src, nil, fmt.Errorf("write provenance %s: %w", label, err)
		}
		frames = append(frames, Frame{
			SourceLabel: label,
			Index:       i,
			Timestamp:   float64(i),
			TimeLabel:   name,
			Path:        filepath.ToSlash(framePath),
			Sidecar:     filepath.ToSlash(sidecarPath),
			Hash:        hash,
			ROIHash:     roiHash,
			ROIUsed:     roiCfg.spec(),
			Keypoints:   kp,
		})
		logf(verbose, "  [%s] reference image %s (phash %s roi %s)", label, name, hash, roiHash)
	}
	src.FrameCount = len(frames)
	return src, frames, nil
}

// hashFrameROI decodes a frame once and returns (whole-frame aHash hex, ROI
// hash hex, keypoint count). The ROI hash is the primary same-room signal; the
// whole-frame hash is kept for provenance and as a weak signal.
func hashFrameROI(path string, roiCfg roiConfig) (whole, roi string, keypoints int, err error) {
	whole, roi, keypoints, _, _, err = hashImageFileROI(path, roiCfg)
	return whole, roi, keypoints, err
}

// hashImageFileROI decodes an image and returns its whole-frame aHash, its ROI
// hash, the static-decor keypoint count (0 if disabled), and pixel dimensions.
func hashImageFileROI(path string, roiCfg roiConfig) (whole, roi string, keypoints, w, h int, err error) {
	f, oerr := os.Open(path)
	if oerr != nil {
		return "", "", 0, 0, 0, fmt.Errorf("open image: %w", oerr)
	}
	defer f.Close()
	img, _, derr := image.Decode(f)
	if derr != nil {
		return "", "", 0, 0, 0, fmt.Errorf("decode image: %w", derr)
	}
	b := img.Bounds()
	whole = osintexport.HashHex(osintexport.AHashFromImage(img))
	roi = roiCfg.roiHashHex(img)
	keypoints = roiCfg.keypointCount(img)
	return whole, roi, keypoints, b.Dx(), b.Dy(), nil
}

// copyFile copies src to dst byte-for-byte (no transform). Used to localize a
// reference image into the exhibit's frame dir without altering the original.
func copyFile(src, dst string) error {
	in, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, in, 0o644)
}

// timeLabel renders seconds as "M:SS.s" for the exhibit (e.g. 73.4 -> "1:13.4").
func timeLabel(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	m := int(sec) / 60
	s := sec - float64(m*60)
	return fmt.Sprintf("%d:%04.1f", m, s)
}
