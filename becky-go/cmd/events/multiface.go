// multiface.go — multi_face detection for becky-events. Samples the clip coarsely
// (every ~3s, capped), runs the SHARED internal/faceembed runner (the same one
// becky-identify uses — no duplicated face pipeline), and emits a multi_face event
// for every frame that contains 2+ faces. The helper already filters detections at
// det_score>=0.5, so any frame it reports with n_faces>=2 is a genuine multi-face
// hit. Degrades gracefully: a runner/model/dep failure is returned as an error so
// main() can record a "skipped" note and keep speaker + location events.
package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/faceembed"
	"becky-go/internal/mediainfo"
	"becky-go/internal/osintexport"
)

const (
	// multiFaceSampleEverySec is the DENSE base cadence: one frame per second. The
	// old 3s grid stepped over brief two-person moments (and, on rotated phone
	// footage handed to the detector sideways, saw nothing at all). On long clips
	// the interval widens below so multiFaceMaxFrames still spans the whole clip.
	multiFaceSampleEverySec = 1.0
	multiFaceMaxFrames      = 60 // cap frames per video (keeps events reasonably fast)
	multiFaceJPEGQuality    = 3  // ffmpeg -q:v for sampled frames (lower = better)
	multiFaceMinFaces       = 2  // a frame needs this many faces to be a multi_face hit
)

// multiFaceEvents samples frames coarsely, embeds them via the shared faceembed
// runner, and returns a multi_face Event for each frame holding >= 2 faces.
func multiFaceEvents(cfg config.Config, info mediainfo.Info, input, dev string, verbose bool) ([]Event, error) {
	frames, times, err := sampleFaceFrames(cfg, info, input, verbose)
	if err != nil {
		return nil, err
	}
	defer func() {
		if len(frames) > 0 {
			os.RemoveAll(filepath.Dir(frames[0]))
		}
	}()
	if len(frames) == 0 {
		return nil, fmt.Errorf("no frames could be sampled from video")
	}

	recs, err := faceembed.Embed(cfg, frames, dev, verbose)
	if err != nil {
		return nil, err
	}

	fps := info.FPS
	if fps <= 0 {
		fps = 30
	}

	var events []Event
	for i, f := range recs {
		if f.NFaces < multiFaceMinFaces {
			continue
		}
		ts := 0.0
		if i < len(times) {
			ts = times[i]
		}
		frame := int(math.Round(ts * fps))
		events = append(events, Event{
			Type:        "multi_face",
			Start:       round3(ts),
			End:         round3(ts),
			Frame:       frame,
			Timestamp:   round3(ts),
			FaceCount:   f.NFaces,
			Confidence:  multiFaceConfidence(f.DetScore),
			Description: fmt.Sprintf("Multiple faces in frame (%d detected)", f.NFaces),
		})
		beckyio.Logf(verbose, "  multi_face @ %.1fs: %d faces (det_score=%.3f)", ts, f.NFaces, f.DetScore)
	}
	return events, nil
}

// sampleFaceFrames extracts frames densely into a temp dir and returns parallel
// slices of (path, timestampSec). It samples one frame per second
// (multiFaceSampleEverySec) so brief two-person moments are not stepped over; on
// long clips the interval widens so multiFaceMaxFrames still spans the whole clip.
// The clip's display rotation is probed ONCE and applied to every frame via
// osintexport.ExtractFrameRotated, so rotated phone footage is handed to the
// detector UPRIGHT (a sideways frame is silently undetected). Best-effort per
// frame; failures are skipped.
func sampleFaceFrames(cfg config.Config, info mediainfo.Info, input string, verbose bool) ([]string, []float64, error) {
	dur := info.Duration
	if dur <= 0 {
		dur = multiFaceSampleEverySec
	}
	step := multiFaceSampleInterval(dur)
	rot := osintexport.DisplayRotation(cfg.FFprobe, input)
	dir, err := os.MkdirTemp("", "becky_eventsfaces_")
	if err != nil {
		return nil, nil, fmt.Errorf("create face frame dir: %w", err)
	}
	var paths []string
	var times []float64
	idx := 0
	for t := 0.0; t < dur && len(paths) < multiFaceMaxFrames; t += step {
		p := filepath.Join(dir, fmt.Sprintf("f_%04d.jpg", idx))
		if err := osintexport.ExtractFrameRotated(cfg.FFmpeg, input, t, p, "jpg", multiFaceJPEGQuality, rot); err == nil {
			paths = append(paths, p)
			times = append(times, t)
		}
		idx++
	}
	beckyio.Logf(verbose, "multi_face: sampled %d frame(s) every %.1fs (rotation %s)",
		len(paths), step, osintexport.RotationLabel(rot))
	return paths, times, nil
}

// multiFaceSampleInterval returns the seconds-between-frames cadence: the dense
// base rate (1s) for short clips, widened just enough that multiFaceMaxFrames
// covers the whole clip on long ones.
func multiFaceSampleInterval(dur float64) float64 {
	step := multiFaceSampleEverySec
	if dur > multiFaceSampleEverySec*float64(multiFaceMaxFrames) {
		step = dur / float64(multiFaceMaxFrames)
	}
	return step
}

// multiFaceConfidence reports the best face's detection score (clamped to [0,1])
// as the event confidence. The helper already gated detections at >=0.5, so this
// is a calibrated signal of how confidently the faces were detected.
func multiFaceConfidence(detScore float64) float64 {
	if detScore <= 0 {
		return 0.6
	}
	if detScore > 1 {
		detScore = 1
	}
	return round3(detScore)
}
