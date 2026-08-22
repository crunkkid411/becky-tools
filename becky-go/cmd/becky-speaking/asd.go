package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"becky-go/internal/config"
	"becky-go/internal/facetrack"
	"becky-go/internal/pyhelpers"
)

// --- Go -> asd.py track contract -------------------------------------------
//
// Mirrors asd.py's documented --tracks JSON exactly (module docstring):
//   {"tracks": [{"id": 1, "detections": [{"t": 0.04, "bbox": [x1,y1,x2,y2]}]}]}
// bbox is SOURCE pixels, t is seconds from the start of the FILE — both of which
// facetrack.Detection already carries, so this is a straight field copy.

type asdInDetection struct {
	T    float64    `json:"t"`
	BBox [4]float64 `json:"bbox"`
}

type asdInTrack struct {
	ID         int              `json:"id"`
	Detections []asdInDetection `json:"detections"`
}

type asdInFile struct {
	Tracks []asdInTrack `json:"tracks"`
}

func asdTracksFromFaceTracks(tracks []facetrack.Track) asdInFile {
	out := asdInFile{Tracks: make([]asdInTrack, 0, len(tracks))}
	for _, t := range tracks {
		dets := make([]asdInDetection, 0, len(t.Detections))
		for _, d := range t.Detections {
			dets = append(dets, asdInDetection{T: d.Time, BBox: d.BBox})
		}
		out.Tracks = append(out.Tracks, asdInTrack{ID: t.ID, Detections: dets})
	}
	return out
}

func writeTracksJSON(in asdInFile) (string, error) {
	f, err := os.CreateTemp("", "becky-speaking-tracks-*.json")
	if err != nil {
		return "", err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	if err := enc.Encode(in); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// --- asd.py -> Go result contract -------------------------------------------
//
// Mirrors asd.py's documented stdout JSON (module docstring). The per-frame
// "scores" array is intentionally not decoded here — Go only needs the summary.

type asdOutTrack struct {
	ID           int      `json:"id"`
	Scored       int      `json:"scored"`
	ScoreMean    *float64 `json:"score_mean"`
	SpeakingFrac *float64 `json:"speaking_frac"`
	Note         string   `json:"note,omitempty"`
}

type asdOut struct {
	OK     bool          `json:"ok"`
	Reason string        `json:"reason,omitempty"`
	FPS    float64       `json:"fps,omitempty"`
	Device string        `json:"device,omitempty"`
	Tracks []asdOutTrack `json:"tracks"`
}

// runASD runs asd.py (LR-ASD) over the given tracks JSON file and returns its
// parsed result. It uses cfg.FacePython + cfg.FacePyLib on PYTHONPATH — the SAME
// interpreter crop_path.py/face_embed.py already use — because that is the one
// environment on this machine that has cv2, python_speech_features, scipy AND a
// CUDA build of torch all together (verified: anaconda3 base + the FacePyLib
// site-packages dir). --device is always "auto": asd.py picks CUDA when it is
// there and falls back to CPU otherwise, which is the right default regardless
// of the face detector's own --device (onnxruntime here is CPU-only; torch is
// not, and LR-ASD is many times faster on the GPU).
func runASD(cfg config.Config, video, tracksPath string, start, end float64, verbose bool) (asdOut, error) {
	script, err := pyhelpers.Materialize("asd.py", pyhelpers.ASD)
	if err != nil {
		return asdOut{}, fmt.Errorf("materialize asd helper: %w", err)
	}

	args := []string{script,
		"--video", video,
		"--tracks", tracksPath,
		"--repo", cfg.LRASDRepo,
		"--start", trimF(start),
		"--end", trimF(end),
		"--ffmpeg", ffmpegBin(cfg),
		"--device", "auto",
	}

	cmd := exec.Command(asdPython(cfg), args...)
	cmd.Env = asdChildEnv(cfg)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	if verbose {
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stderr = &stderr
	}
	if err := cmd.Run(); err != nil {
		return asdOut{}, fmt.Errorf("asd helper failed: %v\n%s", err, tail(stderr.String()))
	}

	var res asdOut
	if err := json.Unmarshal([]byte(lastJSONLine(stdout.String())), &res); err != nil {
		return asdOut{}, fmt.Errorf("could not parse asd helper output: %w\n%s", err, tail(stdout.String()))
	}
	return res, nil
}

func asdPython(cfg config.Config) string {
	if cfg.FacePython != "" {
		return cfg.FacePython
	}
	return cfg.Python
}

func asdChildEnv(cfg config.Config) []string {
	env := os.Environ()
	if cfg.FacePyLib != "" {
		env = append(env, "PYTHONPATH="+cfg.FacePyLib+string(os.PathListSeparator)+os.Getenv("PYTHONPATH"))
	}
	return env
}

func lastJSONLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if strings.HasPrefix(l, "{") {
			return l
		}
	}
	return strings.TrimSpace(s)
}

// --- Joining tracks with their ASD scores, and the speaker decision ---------

// joinTracks pairs each facetrack.Track with its asd.py score by ID and reports
// the honest per-track shape: score_mean/speaking_frac stay nil when LR-ASD could
// not score that track (too few frames on screen) rather than defaulting to 0,
// which would silently read as "confirmed not speaking".
// attachBoxes copies each track's per-frame geometry onto the matching report
// row. Separate from joinTracks so the default (summary-only) output is
// byte-identical to what it has always been.
func attachBoxes(out []trackOut, tracks []facetrack.Track) {
	byID := make(map[int]facetrack.Track, len(tracks))
	for _, t := range tracks {
		byID[t.ID] = t
	}
	for i := range out {
		t, ok := byID[out[i].ID]
		if !ok {
			continue
		}
		bs := make([]boxAt, 0, len(t.Detections))
		for _, d := range t.Detections {
			bs = append(bs, boxAt{T: d.Time, BBox: d.BBox})
		}
		out[i].Boxes = bs
	}
}

func joinTracks(tracks []facetrack.Track, scored []asdOutTrack) []trackOut {
	byID := make(map[int]asdOutTrack, len(scored))
	for _, s := range scored {
		byID[s.ID] = s
	}
	out := make([]trackOut, 0, len(tracks))
	for _, t := range tracks {
		to := trackOut{
			ID:         t.ID,
			Start:      t.Start(),
			End:        t.End(),
			Detections: len(t.Detections),
		}
		if s, ok := byID[t.ID]; ok {
			to.Scored = s.Scored
			to.ScoreMean = s.ScoreMean
			to.SpeakingFrac = s.SpeakingFrac
			to.Note = s.Note
			to.Speaking = s.SpeakingFrac != nil && *s.SpeakingFrac >= minSpeakingFrac
		} else {
			to.Note = "LR-ASD returned no score for this track"
		}
		out = append(out, to)
	}
	// Stable, meaningful order: highest speaking_frac first (nil last), so the
	// report reads like a ranking without the caller having to sort it again.
	sort.SliceStable(out, func(i, j int) bool {
		fi, fj := out[i].SpeakingFrac, out[j].SpeakingFrac
		switch {
		case fi != nil && fj != nil:
			return *fi > *fj
		case fi != nil:
			return true
		case fj != nil:
			return false
		default:
			return out[i].ID < out[j].ID
		}
	})
	return out
}

// decideSpeaker corroborates the ranked tracks into a single verdict, following
// CLAUDE.md's "corroborate, then conclude — don't hedge" rule: a track that leads
// the field by a material margin (speakerMargin) and clears minSpeakingFrac is a
// CONCLUSION; anything closer, or a single track that never clears the floor, is
// reported honestly as a CANDIDATE rather than forced to a pick. No scored track
// at all is NONE — the pipeline degraded, and says so.
func decideSpeaker(tracks []trackOut) (speakerID *int, confidence string) {
	var scored []trackOut
	for _, t := range tracks {
		if t.SpeakingFrac != nil {
			scored = append(scored, t)
		}
	}
	if len(scored) == 0 {
		return nil, "none"
	}
	// tracks is already sorted best-first by joinTracks.
	best := scored[0]
	if *best.SpeakingFrac < minSpeakingFrac {
		return nil, "candidate"
	}
	if len(scored) == 1 {
		id := best.ID
		return &id, "conclusion"
	}
	second := scored[1]
	margin := *best.SpeakingFrac - *second.SpeakingFrac
	// epsilon guards a margin that is EXACTLY the threshold in decimal but not
	// in float64 (e.g. 0.70-0.50 == 0.19999999999999996), so "materially higher
	// by speakerMargin" does not silently flip to "candidate" on rounding.
	if margin >= speakerMargin-1e-9 {
		id := best.ID
		return &id, "conclusion"
	}
	return nil, "candidate"
}
