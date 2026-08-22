// becky-speaking — decide WHICH visible face is actually talking.
//
// One dumb call: give it a video (and optionally a --start/--end window) and it
// answers "who is speaking" as a ranked list of tracked faces with a speaking
// score each, corroborated by LR-ASD (lip motion against the real soundtrack).
//
//	becky-speaking --video clip.mp4 --start 0 --end 12
//
// This wires up the last piece of HANDOFF-SHORTS-PIPELINE.md's face story, which
// existed as three separate, working, unconnected pieces:
//
//   - face_embed.py --all-faces already emits EVERY face per frame (not just the
//     most prominent), deterministically ordered.
//   - internal/facetrack already turns per-frame detections into persistent
//     tracks (IoU + ArcFace association), but nothing fed it real detections.
//   - asd.py (LR-ASD) already scores a track's speaking likelihood, but nothing
//     called it.
//
// The chain: read the video's TRUE frame rate (ffprobe — never assume 30, a
// human watches at the real fps and so must this), decode the window at exactly
// that rate to individual frames, run every frame through the shared face
// detector, track the per-frame faces into persistent identities, then hand
// those tracks to LR-ASD for a speaking score each. Every step degrades to a
// typed note instead of a crash; a track LR-ASD could not score keeps its
// speaking_frac as null rather than a guessed number.
package main

import (
	"encoding/json"
	"flag"
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
)

// speakerMargin is how much higher the best track's speaking_frac must be over
// the runner-up before becky calls it a CONCLUSION rather than a candidate. This
// is CLAUDE.md's "corroborate, then conclude — don't hedge" applied to a single
// (already internally corroborated — LR-ASD itself fuses lip motion and audio)
// signal: a clear lead is treated as decisive, a close call is reported honestly
// as ambiguous rather than resolved by a coin flip.
const speakerMargin = 0.20

// minSpeakingFrac is the floor a lone track's speaking_frac must clear before it
// is called "speaking" at all — below this it read as mostly silent, whatever the
// other tracks did.
const minSpeakingFrac = 0.50

// frameJPEGQuality is ffmpeg's -q:v for extracted frames (lower = better).
const frameJPEGQuality = 3

// boxAt is one tracked face box at one instant, as FRACTIONS of the frame
// ([x1,y1,x2,y2]) — facetrack's own convention.
type boxAt struct {
	T    float64    `json:"t"`
	BBox [4]float64 `json:"bbox"`
}

type trackOut struct {
	ID           int      `json:"id"`
	Start        float64  `json:"start"`
	End          float64  `json:"end"`
	Detections   int      `json:"detections"`
	Scored       int      `json:"scored"`
	ScoreMean    *float64 `json:"score_mean"`
	SpeakingFrac *float64 `json:"speaking_frac"`
	Speaking     bool     `json:"speaking"`
	Note         string   `json:"note,omitempty"`
	// Boxes is the track's per-frame geometry, emitted only under --boxes.
	// OFF by default because a 60s window at 30fps is ~1800 boxes per track and
	// the summary is what a human reads; ON for callers that need to STEER
	// something with it (cmd/becky-short's speaker rung builds its camera path
	// from exactly these). Emitting them here rather than exposing a second
	// query is deliberate: the geometry is a free by-product of a run that costs
	// face detection plus LR-ASD, and asking twice would pay that twice for
	// nothing.
	Boxes []boxAt `json:"boxes,omitempty"`
}

type speakingReport struct {
	OK             bool       `json:"ok"`
	Video          string     `json:"video"`
	Start          float64    `json:"start"`
	End            float64    `json:"end"`
	FPS            float64    `json:"fps"`
	FramesUsed     int        `json:"frames_used"`
	Tracks         []trackOut `json:"tracks"`
	SpeakerTrackID *int       `json:"speaker_track_id,omitempty"`
	Confidence     string     `json:"confidence"` // conclusion | candidate | none
	Note           string     `json:"note,omitempty"`
}

func main() {
	var (
		video    = flag.String("video", "", "source video")
		start    = flag.Float64("start", 0, "window start (seconds)")
		end      = flag.Float64("end", 0, "window end (seconds); 0 = to end of file")
		out      = flag.String("out", "", "write JSON here instead of stdout")
		device   = flag.String("device", "", "face detector device: cpu|cuda (default: config)")
		boxes    = flag.Bool("boxes", false, "include each track's per-frame face boxes in the JSON "+
			"(large; for callers that steer a crop with them, not for reading)")
		selftest = flag.Bool("selftest", false, "run the offline proof and exit")
		verbose  = flag.Bool("verbose", false, "progress to stderr")
	)
	flag.Parse()

	if *selftest {
		os.Exit(runSelftest())
	}

	cfg := config.Load()
	if *video == "" {
		fail(fmt.Errorf("need --video"))
	}
	if _, err := os.Stat(*video); err != nil {
		fail(fmt.Errorf("cannot read %s: %w", *video, err))
	}
	dev := *device
	if dev == "" {
		dev = cfg.Device
	}

	rep, err := run(cfg, *video, *start, *end, dev, *boxes, *verbose)
	if err != nil {
		rep = speakingReport{OK: false, Video: *video, Start: *start, End: *end,
			Confidence: "none", Note: err.Error()}
	}

	data, _ := json.MarshalIndent(rep, "", "  ")
	if *out != "" {
		if err := os.WriteFile(*out, append(data, '\n'), 0o644); err != nil {
			fail(err)
		}
	} else {
		fmt.Println(string(data))
	}
	if !rep.OK {
		os.Exit(1)
	}
}

// run is the whole chain, factored out of main so selftest-adjacent logic (the
// pure decision functions below) can be exercised without it.
func run(cfg config.Config, video string, start, end float64, device string, withBoxes, verbose bool) (speakingReport, error) {
	if cfg.FaceModelRoot == "" {
		return speakingReport{}, fmt.Errorf("face model not configured — becky-speaking needs the same face model becky-identify uses")
	}
	if cfg.LRASDRepo == "" {
		return speakingReport{}, fmt.Errorf("LR-ASD checkout not configured (config.LRASDRepo) — who-is-speaking cannot run without it")
	}

	// TRUE frame rate, never assumed. Analyzing below the video's real fps is
	// exactly the mistake CLAUDE.md rule 2 exists to prevent, and it is what made
	// the first becky-short crop tracker lag: it sampled 8x/sec on 30fps footage.
	info, err := mediainfo.Probe(cfg.FFprobe, video)
	if err != nil {
		return speakingReport{}, fmt.Errorf("could not read %s's real frame rate: %w", filepath.Base(video), err)
	}
	if info.FPS <= 0 {
		return speakingReport{}, fmt.Errorf("%s reports no readable frame rate — refusing to guess one", filepath.Base(video))
	}
	fps := info.FPS

	if end <= 0 {
		end = info.Duration
	}
	if end <= start {
		return speakingReport{}, fmt.Errorf("--end (%.3f) must be greater than --start (%.3f)", end, start)
	}

	paths, times, frameDir, err := extractFrames(cfg, video, start, end, fps, verbose)
	if err != nil {
		return speakingReport{}, err
	}
	defer os.RemoveAll(frameDir)

	faces, err := faceembed.EmbedAll(cfg, paths, device, verbose)
	if err != nil {
		return speakingReport{}, fmt.Errorf("face detection failed: %w", err)
	}

	dets := detectionsFromFaces(faces, times)
	tracks := facetrack.Build(dets, facetrack.DefaultOptions())
	if len(tracks) == 0 {
		return speakingReport{OK: false, Video: video, Start: start, End: end, FPS: fps,
				FramesUsed: len(paths), Confidence: "none",
				Note: fmt.Sprintf("no persistent face found across %d frame(s) in that window", len(paths))},
			nil
	}

	asdIn := asdTracksFromFaceTracks(tracks)
	tracksPath, err := writeTracksJSON(asdIn)
	if err != nil {
		return speakingReport{}, err
	}
	defer os.Remove(tracksPath)

	asdRes, err := runASD(cfg, video, tracksPath, start, end, verbose)
	if err != nil {
		return speakingReport{}, fmt.Errorf("LR-ASD failed: %w", err)
	}
	if !asdRes.OK {
		return speakingReport{OK: false, Video: video, Start: start, End: end, FPS: fps,
			FramesUsed: len(paths), Confidence: "none",
			Note: "LR-ASD declined: " + asdRes.Reason}, nil
	}

	outTracks := joinTracks(tracks, asdRes.Tracks)
	if withBoxes {
		attachBoxes(outTracks, tracks)
	}
	speaker, confidence := decideSpeaker(outTracks)

	return speakingReport{
		OK:             true,
		Video:          video,
		Start:          start,
		End:            end,
		FPS:            fps,
		FramesUsed:     len(paths),
		Tracks:         outTracks,
		SpeakerTrackID: speaker,
		Confidence:     confidence,
	}, nil
}

// extractFrames decodes [start,end) at EXACTLY fps into individually numbered
// JPEGs, in ONE ffmpeg call rather than one process per frame — the same lesson
// crop_path.py and asd.py both learned the hard way: per-frame seeks on a real
// file are slow and, worse, not reliably exact. Returns frame paths and their
// absolute timestamps (seconds from the start of the FILE, matching what asd.py
// expects), in extraction order, plus the temp dir to clean up.
func extractFrames(cfg config.Config, video string, start, end, fps float64, verbose bool) ([]string, []float64, string, error) {
	dir, err := os.MkdirTemp("", "becky-speaking-frames-")
	if err != nil {
		return nil, nil, "", err
	}
	pattern := filepath.Join(dir, "f_%06d.jpg")
	args := []string{"-y",
		"-ss", trimF(start), "-t", trimF(end - start),
		"-i", video,
		"-vf", fmt.Sprintf("fps=%s", trimF(fps)),
		"-q:v", fmt.Sprintf("%d", frameJPEGQuality),
		"-loglevel", "error",
		pattern,
	}
	cmd := exec.Command(ffmpegBin(cfg), args...)
	var stderr strings.Builder
	if verbose {
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stderr = &stderr
	}
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
		return nil, nil, "", fmt.Errorf("ffmpeg produced no frames for %.3f-%.3fs", start, end)
	}

	paths := make([]string, len(names))
	times := frameTimes(start, fps, len(names))
	for i, n := range names {
		paths[i] = filepath.Join(dir, n)
	}
	return paths, times, dir, nil
}

// frameTimes is the pure timestamp math behind extractFrames, split out so the
// selftest can assert it without touching ffmpeg or the filesystem.
func frameTimes(start, fps float64, n int) []float64 {
	times := make([]float64, n)
	for i := 0; i < n; i++ {
		times[i] = start + float64(i)/fps
	}
	return times
}

// detectionsFromFaces flattens faceembed's per-image "all faces" results into
// facetrack.Detection input. i is used as the frame index directly: extractFrames
// samples at exactly fps, so image i really is frame i of the window at that
// rate, which is what MaxGapFrames counts against.
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

func fail(err error) {
	fmt.Fprintln(os.Stderr, "becky-speaking:", err)
	os.Exit(2)
}
