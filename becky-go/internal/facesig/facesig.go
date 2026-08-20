// Package facesig runs a coarse, whole-video face pass and answers, for any
// candidate window, "was a talking head actually ON SCREEN here?"
//
// This exists because of a bug HANDOFF-SHORTS-PIPELINE.md §7 names plainly: the
// top-ranked moment was chosen from words alone and had him bent out of shot —
// only the renderer caught it, after the pick was already made. Structure reads
// the transcript; audio (internal/audiosig) reads the soundtrack; neither one
// can see the frame. This is the THIRD independent signal, following the same
// shape as audiosig: read the whole file ONCE (Run), then score any window
// cheaply (Signals.In).
//
// Face coverage has a different CHARACTER than audio, and that is deliberate.
// Audio measures energy and nudges the order either way — loud is not
// necessarily better, quiet is not necessarily worse, so it is folded into the
// structural prior as a weighted average. Being ON screen does not make a
// moment GOOD either — but being OFF screen for most of a window DOES mean a
// "talking head" short has no talking head in it, which no amount of
// structural or audio evidence can excuse. So this signal only ever SINKS a
// window's score (see moment.facePenalty in internal/moment/judge.go); it
// never boosts one, and — like audio — it never on its own promotes a
// candidate to a conclusion.
//
// The detection itself reuses exactly what cmd/becky-speaking already proved
// out: ffmpeg decodes the file to individually numbered frames in ONE call,
// internal/faceembed's face_embed.py --all-faces detects every face per frame,
// and internal/facetrack turns those per-frame detections into persistent
// tracks. becky-speaking samples EVERY frame because a crop tracker needs
// frame-accurate motion; ranking only needs "is there a face here at all", so
// this samples much more coarsely — see DefaultSamplePeriod for the measured
// cost that number is based on. A false-positive single-frame detection (a
// lamp, a poster) is filtered the same way becky-speaking filters one: a
// facetrack.Track must accumulate MinDetections before it counts as a track at
// all, so a one-off hallucination never earns coverage.
package facesig

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"becky-go/internal/config"
	"becky-go/internal/faceembed"
	"becky-go/internal/facetrack"
	"becky-go/internal/mediainfo"
	"becky-go/internal/pathx"
)

// DefaultSamplePeriod is how often the coarse pass actually looks, in seconds.
//
// Measured on test-for-clips.mp4 (300.2s, 1920x1080, CPU face detector,
// 2026-08-19): sampling every 2.0s (150 frames) took 20.6s to extract with
// ffmpeg + 68.6s to embed with face_embed.py, 89.2s total — about 30% of the
// clip's own runtime. Sampling every 1.0s (300 frames) took 150.5s total,
// roughly double, for roughly double the resolution this signal actually
// needs: the shortest candidate window is 12s, so even 2.0s sampling still
// gives 6 looks at the smallest window and 30 at the largest (60s). Going
// coarser than this starts to matter for score.go's coverage math (see
// facetrack.Track.CoverageIn's samplePeriod argument); going finer buys
// resolution this signal has no use for. Embedding cost is ~0.45s/frame
// regardless of rate (CPU-bound, not I/O-bound), so this is the actual dial
// for how long a folder of long streams takes — that is why it is also a CLI
// flag (cmd/becky-moment's --face-sample-period) rather than hardcoded.
const DefaultSamplePeriod = 2.0

// frameJPEGQuality is ffmpeg's -q:v for extracted frames (lower = better),
// matching cmd/becky-speaking's frameJPEGQuality.
const frameJPEGQuality = 3

// Signals is the analysed file: every face track found across its whole
// duration, plus the rate they were sampled at (Track.CoverageIn needs it to
// tell "sampled sparsely but present throughout" apart from "glimpsed a few
// times" — the timestamps alone cannot).
type Signals struct {
	OK           bool
	Reason       string
	Tracks       []facetrack.Track
	SamplePeriod float64
}

// Run samples video once every samplePeriod seconds across its WHOLE
// duration, detects every face in every sampled frame, and tracks them into
// persistent identities. A failure degrades to (Signals{}, err) — same
// contract as internal/audiosig.Run — so the caller ranks without this signal
// and says so, rather than crashing or silently ranking on half the evidence.
//
// samplePeriod <= 0 uses DefaultSamplePeriod.
func Run(cfg config.Config, video string, samplePeriod float64, device string) (Signals, error) {
	if samplePeriod <= 0 {
		samplePeriod = DefaultSamplePeriod
	}
	if cfg.FaceModelRoot == "" {
		return Signals{}, fmt.Errorf("face model not configured — needs the same face model becky-identify uses")
	}
	if device == "" {
		device = cfg.Device
	}

	info, err := mediainfo.Probe(cfg.FFprobe, video)
	if err != nil {
		return Signals{}, fmt.Errorf("could not read %s's duration: %w", pathx.Base(video), err)
	}
	if info.Duration <= 0 {
		return Signals{}, fmt.Errorf("%s reports no readable duration — refusing to guess one", pathx.Base(video))
	}

	fps := 1.0 / samplePeriod
	paths, times, dir, err := extractFrames(cfg, video, info.Duration, fps)
	if err != nil {
		return Signals{}, err
	}
	defer os.RemoveAll(dir)

	faces, err := faceembed.EmbedAll(cfg, paths, device, false)
	if err != nil {
		return Signals{}, fmt.Errorf("face detection failed: %w", err)
	}

	dets := detectionsFromFaces(faces, times)
	tracks := facetrack.Build(dets, facetrack.DefaultOptions())
	return Signals{OK: true, Tracks: tracks, SamplePeriod: samplePeriod}, nil
}

// Window is how a candidate window scored on face coverage.
type Window struct {
	// Coverage is 0..1: the DENSITY of sampled instants in [t0,t1] where the
	// best-covered track was actually detected — not the span between its
	// outermost sightings in the window (facetrack.Track.CoverageIn's own
	// doc explains why that distinction matters; it was a real bug once).
	Coverage float64
	Samples  int
	Basis    string
}

// In scores the window [t0,t1]. With more than one track visible in the
// window, the BEST-covered one wins — this signal only asks "is there a
// talking head here at all", not who it is, so one well-covered face is
// enough regardless of who else passes through the background.
func (s Signals) In(t0, t1 float64) Window {
	if !s.OK || len(s.Tracks) == 0 {
		return Window{Basis: "no face track data for this window"}
	}
	var w Window
	for _, tr := range s.Tracks {
		frac, n := tr.CoverageIn(t0, t1, s.SamplePeriod)
		if frac > w.Coverage {
			w.Coverage = frac
			w.Samples = n
		}
	}
	if w.Samples == 0 {
		w.Basis = "no face detected anywhere in this window"
	} else {
		w.Basis = fmt.Sprintf("face coverage %.2f (%d sampled sighting(s))", w.Coverage, w.Samples)
	}
	return w
}

// AnyIn scores the window on whether ANY face is in frame at each sampled
// instant, taking the UNION of all tracks rather than the best single one.
//
// In() asks a different question and asks it correctly for ranking a candidate
// window of a SOURCE: is one person consistently present, i.e. is this a
// talking-head moment. Point that at a RENDERED short and it is the wrong
// question by construction — a short built from twenty jumpcuts between two
// people has no single track spanning it, so the best track covers a fraction
// of the running time no matter how well every shot is framed.
//
// MEASURED, on a 29.1s render of Jordan's reference window: In() reported
// coverage 0.13 from 15 sightings and the review pass called that a 54-point
// gap against the render's own claim, blaming the render. An independent
// InsightFace pass over the same file found a face in 83% of frames. In() was
// right about what it measures and wrong about what was being asked.
func (s Signals) AnyIn(t0, t1 float64) Window {
	if !s.OK || len(s.Tracks) == 0 {
		return Window{Basis: "no face track data for this window"}
	}
	if t1 <= t0 || s.SamplePeriod <= 0 {
		return Window{Basis: "empty window"}
	}
	// Sampled instants are shared across tracks, so count each instant once.
	seen := map[int64]struct{}{}
	for _, tr := range s.Tracks {
		for _, d := range tr.Detections {
			if d.Time < t0 || d.Time > t1 {
				continue
			}
			seen[int64(d.Time/s.SamplePeriod+0.5)] = struct{}{}
		}
	}
	var w Window
	w.Samples = len(seen)
	if w.Samples == 0 {
		w.Basis = "no face detected anywhere in this window"
		return w
	}
	w.Coverage = float64(w.Samples) * s.SamplePeriod / (t1 - t0)
	if w.Coverage > 1 {
		w.Coverage = 1
	}
	w.Basis = fmt.Sprintf("a face was in frame at %.0f%% of sampled instants (%d), any identity",
		w.Coverage*100, w.Samples)
	return w
}

// extractFrames decodes the whole file at exactly fps into individually
// numbered JPEGs in ONE ffmpeg call — the same approach cmd/becky-speaking
// uses for the same reason: per-frame seeks on a real file are slow and not
// reliably exact. Returns frame paths and their absolute timestamps (seconds
// from the start of the file), in extraction order, plus the temp dir to
// clean up.
func extractFrames(cfg config.Config, video string, duration, fps float64) ([]string, []float64, string, error) {
	dir, err := os.MkdirTemp("", "becky-facesig-frames-")
	if err != nil {
		return nil, nil, "", err
	}
	pattern := filepath.Join(dir, "f_%06d.jpg")
	args := []string{"-y",
		"-i", video,
		"-vf", fmt.Sprintf("fps=%s", trimF(fps)),
		"-q:v", fmt.Sprintf("%d", frameJPEGQuality),
		"-loglevel", "error",
		pattern,
	}
	cmd := exec.Command(ffmpegBin(cfg), args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		os.RemoveAll(dir)
		return nil, nil, "", fmt.Errorf("ffmpeg frame extraction failed: %v\n%s", err, tail(stderr.String()))
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		os.RemoveAll(dir)
		return nil, nil, "", err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		os.RemoveAll(dir)
		return nil, nil, "", fmt.Errorf("ffmpeg produced no frames for %.3fs of %s", duration, pathx.Base(video))
	}

	paths := make([]string, len(names))
	period := 1.0 / fps
	times := make([]float64, len(names))
	for i, n := range names {
		paths[i] = filepath.Join(dir, n)
		times[i] = float64(i) * period
	}
	return paths, times, dir, nil
}

// detectionsFromFaces flattens faceembed's per-image "all faces" results into
// facetrack.Detection input, matching cmd/becky-speaking's approach: i is used
// as the frame index directly, since extractFrames samples at exactly fps, so
// image i really is frame i of the sampled sequence.
func detectionsFromFaces(faces []faceembed.Face, times []float64) []facetrack.Detection {
	var dets []facetrack.Detection
	for i, f := range faces {
		if i >= len(times) {
			break
		}
		for _, rec := range f.All {
			if len(rec.BBox) != 4 {
				continue
			}
			dets = append(dets, facetrack.Detection{
				Frame:    i,
				Time:     times[i],
				BBox:     [4]float64{rec.BBox[0], rec.BBox[1], rec.BBox[2], rec.BBox[3]},
				Vector:   rec.Vector,
				DetScore: rec.DetScore,
			})
		}
	}
	return dets
}

func ffmpegBin(cfg config.Config) string {
	if cfg.FFmpeg != "" {
		return cfg.FFmpeg
	}
	return "ffmpeg"
}

func trimF(v float64) string {
	s := fmt.Sprintf("%.6f", v)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 900 {
		return "…" + s[len(s)-900:]
	}
	return s
}
