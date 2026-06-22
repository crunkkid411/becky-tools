package main

import (
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"

	"becky-go/internal/exifmeta"
	"becky-go/internal/location"
	"becky-go/internal/mediainfo"
	"becky-go/internal/osintexport"
)

// sampleClip extracts representative keyframes from one video, fingerprints them
// over the decor band, and reduces the clip to its PRIMARY (medoid) fingerprint.
//
// This is the MEDIA-DEPENDENT half (the LOCAL boundary, SPEC §5): it needs
// ffmpeg/ffprobe on real footage. It is written here so the CLI is complete, but
// it degrades-never-crashes: missing binaries / unreadable clip / no upright
// frames → a Clip with Degraded set (reported, the rest of the corpus continues).
func sampleClip(idx int, path string, cfg sampleConfig, fp location.Fingerprinter) location.Clip {
	c := location.Clip{Index: idx, Path: path}

	// SHA-256 provenance (read-only). A hashing failure is non-fatal.
	if h, err := osintexport.SHA256File(path); err == nil {
		c.SHA256 = h
	}

	// Metadata as a dwelling signal (best-effort).
	if cfg.metadata {
		ex := exifmeta.NewExtractor(cfg.exiftool, cfg.ffprobe)
		if md, err := ex.Extract(path); err == nil {
			c.CaptureTime = preferUTC(md)
			if md.GPS != nil && md.GPS.Present {
				c.GPS = fmt.Sprintf("%.6f,%.6f", md.GPS.Latitude, md.GPS.Longitude)
			}
		}
	}

	info, err := mediainfo.Probe(cfg.ffprobe, path)
	if err != nil {
		c.Degraded = "ffprobe unavailable or clip unreadable: " + err.Error()
		return c
	}
	c.Duration = info.Duration
	if !info.HasVideo || info.Duration <= 0 {
		c.Degraded = "no usable video stream"
		return c
	}

	rotation := osintexport.DisplayRotation(cfg.ffprobe, path)

	// Sample timestamps at the coarse interval.
	stamps := sampleTimestamps(info.Duration, cfg.interval)
	mask := cfg.mask

	var kfs []kf
	tmpDir := cfg.framesDir
	if tmpDir == "" {
		tmpDir = "location-out"
	}
	_ = os.MkdirAll(tmpDir, 0o755)

	for i, ts := range stamps {
		outPath := filepath.Join(tmpDir, fmt.Sprintf("clip%d_kf%d.jpg", idx, i))
		if err := osintexport.ExtractFrameRotated(cfg.ffmpeg, path, ts, outPath, "jpg", 3, rotation); err != nil {
			continue // skip this keyframe; degrade-never-crash
		}
		img, derr := decodeImage(outPath)
		if derr != nil {
			continue
		}
		pr, perr := fp.Print(img, mask)
		if perr != nil {
			continue
		}
		// Dedup near-identical keyframes by decor-hash Hamming.
		if isDuplicate(pr, kfs, cfg.dedupBits) {
			continue
		}
		kfs = append(kfs, kf{print: pr})
	}

	if len(kfs) == 0 {
		c.Degraded = "no upright keyframes could be extracted"
		return c
	}
	c.KeyframeN = len(kfs)
	// Primary fingerprint = medoid (the keyframe nearest all others). For a static
	// talking-head clip this collapses to the single representative print.
	prints := make([]location.Fingerprint, len(kfs))
	for i := range kfs {
		prints[i] = kfs[i].print
	}
	c.Print = medoid(prints)
	return c
}

// kf is one retained keyframe's fingerprint.
type kf struct {
	print location.Fingerprint
}

func isDuplicate(pr location.Fingerprint, kfs []kf, bits int) bool {
	for _, k := range kfs {
		if hammingU64(pr.DecorHash, k.print.DecorHash) <= bits {
			return true
		}
	}
	return false
}

func decodeImage(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}

// sampleTimestamps returns evenly-spaced sample times across the clip, always
// including at least one (the midpoint) for very short clips.
func sampleTimestamps(duration, interval float64) []float64 {
	if duration <= 0 {
		return nil
	}
	if interval <= 0 {
		interval = 2.0
	}
	if duration < interval {
		return []float64{duration / 2}
	}
	var out []float64
	for t := interval / 2; t < duration; t += interval {
		out = append(out, t)
	}
	if len(out) == 0 {
		out = []float64{duration / 2}
	}
	return out
}

// medoid returns the fingerprint with the smallest total decor-hash distance to
// all others (deterministic; ties break by first index).
func medoid(prints []location.Fingerprint) location.Fingerprint {
	if len(prints) == 1 {
		return prints[0]
	}
	best := 0
	bestSum := 1 << 30
	for i := range prints {
		sum := 0
		for j := range prints {
			sum += hammingU64(prints[i].DecorHash, prints[j].DecorHash)
		}
		if sum < bestSum {
			bestSum = sum
			best = i
		}
	}
	return prints[best]
}

func hammingU64(a, b uint64) int {
	x := a ^ b
	n := 0
	for x != 0 {
		x &= x - 1
		n++
	}
	return n
}

func preferUTC(md exifmeta.Metadata) string {
	if md.CaptureTimeUTC != "" {
		return md.CaptureTimeUTC
	}
	return md.CaptureTimeLocal
}
