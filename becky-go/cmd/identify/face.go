// face.go — face identification: sample frames from the video, detect + ArcFace-
// embed the most prominent face per frame, and cosine-match against enrolled
// face-prints. Mirrors voice.go for the visual modality. Detection + alignment +
// 512-d embedding run via the SHARED internal/faceembed runner (InsightFace
// buffalo_l: SCRFD + w600k_r50), which becky-events also uses, so the face
// pipeline is defined once. The cosine match is done here in Go (shared vec.go)
// so matching stays deterministic and testable.
package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/faceembed"
	"becky-go/internal/mediainfo"
	"becky-go/internal/osintexport"
)

const (
	// faceSampleEverySec is the DENSE base cadence: one frame per second. A frontal
	// face can flash by in ~1-2s on phone footage, so the old 3s grid routinely
	// stepped right over it (it sampled t=0,3,6 on a clip whose only faces were at
	// ~2s and ~7s -> zero detections). On long clips the interval is widened below
	// so faceMaxFrames still spans the whole clip.
	faceSampleEverySec = 1.0
	faceMaxFrames      = 60 // cap frames per video (keeps runtime bounded)
	faceJPEGQuality    = 3  // ffmpeg -q:v for sampled frames (lower = better)
)

// enrolledFace is one KB identity's fused (averaged, normalized) face embedding.
type enrolledFace struct {
	name   string
	vector []float64
}

// identifyFaces returns named face identifications + unidentified faces for input.
func identifyFaces(cfg config.Config, info mediainfo.Info, input string, kb Knowledge, threshold float64, device string, verbose bool) ([]Identification, []Unidentified, error) {
	if len(kb.Faces) == 0 {
		return nil, nil, nil
	}
	if cfg.FaceModelRoot == "" {
		return nil, nil, fmt.Errorf("face model root not configured")
	}

	// 1. Sample frames from the video (rotation-corrected, dense cadence).
	frames, times, err := sampleFaceFrames(cfg, info, input, verbose)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if len(frames) > 0 {
			os.RemoveAll(filepath.Dir(frames[0]))
		}
	}()
	if len(frames) == 0 {
		return nil, nil, fmt.Errorf("no frames could be sampled from video")
	}

	// 2. Embed enrolled face-prints AND the sampled video frames in ONE helper
	// call so the InsightFace model loads exactly once per run (it was previously
	// loaded twice — KB then video — doubling startup cost). enrolledImages are
	// passed first; their results are split off, fused per name, and the remaining
	// records line up 1:1 with the sampled frames.
	enrolledImages, owner := enrolledFaceImages(kb.Faces)
	if len(enrolledImages) == 0 {
		return nil, nil, fmt.Errorf("no enrolled face-print images to embed")
	}
	all := append(append([]string{}, enrolledImages...), frames...)
	allRecs, err := faceembed.Embed(cfg, all, device, verbose)
	if err != nil {
		return nil, nil, err
	}
	if len(allRecs) != len(all) {
		return nil, nil, fmt.Errorf("face helper returned %d records for %d images", len(allRecs), len(all))
	}
	enrolled := fuseEnrolled(allRecs[:len(enrolledImages)], owner)
	if len(enrolled) == 0 {
		return nil, nil, fmt.Errorf("no faces detected in enrolled face-prints")
	}
	recs := allRecs[len(enrolledImages):]

	// 3. Match each detected frame-face against enrolled names; keep best per name.
	type best struct {
		sim float64
		ts  float64
	}
	bestByName := map[string]best{}
	var unidentified []Unidentified
	for i, f := range recs {
		if !f.Found || len(f.Vector) == 0 {
			continue
		}
		ts := 0.0
		if i < len(times) {
			ts = times[i]
		}
		vec := normalize(f.Vector)
		bestName, bestSim := "", 0.0
		for _, e := range enrolled {
			if s := cosine(vec, e.vector); s > bestSim {
				bestSim, bestName = s, e.name
			}
		}
		if bestSim >= threshold {
			if cur, ok := bestByName[bestName]; !ok || bestSim > cur.sim {
				bestByName[bestName] = best{sim: bestSim, ts: ts}
			}
		} else {
			unidentified = append(unidentified, Unidentified{
				Type:        "face",
				Description: "unidentified face",
				Confidence:  math.Round(bestSim*10000) / 10000,
			})
		}
	}

	fps := info.FPS
	if fps <= 0 {
		fps = 30
	}
	var ids []Identification
	for name, b := range bestByName {
		ids = append(ids, Identification{
			Type:       "face",
			Name:       name,
			Confidence: math.Round(b.sim*10000) / 10000,
			Match:      "cosine",
			Frames:     []FrameRef{{Frame: int(b.ts*fps + 0.5), Timestamp: math.Round(b.ts*1000) / 1000}},
		})
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].Name < ids[j].Name })
	return ids, unidentified, nil
}

// sampleFaceFrames extracts frames densely into a temp dir and returns parallel
// slices of (path, timestampSec). It samples one frame per second (faceSampleEverySec)
// so a brief frontal face is not stepped over; on long clips the interval widens so
// faceMaxFrames still spans the whole clip. The clip's display rotation is probed
// ONCE and applied to every frame via osintexport.ExtractFrameRotated, so phone
// footage stored sideways is handed to the detector UPRIGHT (a sideways face is
// silently undetected by SCRFD — the corpus-wide miss this fixes).
func sampleFaceFrames(cfg config.Config, info mediainfo.Info, input string, verbose bool) ([]string, []float64, error) {
	dur := info.Duration
	if dur <= 0 {
		dur = faceSampleEverySec
	}
	step := faceSampleInterval(dur)
	rot := osintexport.DisplayRotation(cfg.FFprobe, input)
	dir, err := os.MkdirTemp("", "becky_faceframes_")
	if err != nil {
		return nil, nil, err
	}
	var paths []string
	var times []float64
	idx := 0
	for t := 0.0; t < dur && len(paths) < faceMaxFrames; t += step {
		p := filepath.Join(dir, fmt.Sprintf("f_%04d.jpg", idx))
		if err := osintexport.ExtractFrameRotated(cfg.FFmpeg, input, t, p, "jpg", faceJPEGQuality, rot); err == nil {
			paths = append(paths, p)
			times = append(times, t)
		}
		idx++
	}
	beckyio.Logf(verbose, "face: sampled %d frame(s) every %.1fs (rotation %s)",
		len(paths), step, osintexport.RotationLabel(rot))
	return paths, times, nil
}

// faceSampleInterval returns the seconds-between-frames cadence: the dense base
// rate (1s) for short clips, widened just enough that faceMaxFrames covers the
// whole clip on long ones. This keeps short clips dense (catching brief faces)
// without letting frame count explode on hour-long footage.
func faceSampleInterval(dur float64) float64 {
	step := faceSampleEverySec
	if dur > faceSampleEverySec*float64(faceMaxFrames) {
		step = dur / float64(faceMaxFrames)
	}
	return step
}

// enrolledFaceImages flattens every enrolled identity's face images into a single
// ordered list plus an image-path -> identity-name owner map. The caller embeds
// these together with the video frames in ONE helper run, then hands the enrolled
// slice of results to fuseEnrolled. (Split out from the old single-call helper so
// the model loads once for KB + video.)
func enrolledFaceImages(faces []FacePrint) ([]string, map[string]string) {
	var imgs []string
	owner := map[string]string{}
	for _, fp := range faces {
		for _, img := range fp.Faces {
			imgs = append(imgs, img)
			owner[img] = fp.Name
		}
	}
	return imgs, owner
}

// fuseEnrolled groups already-embedded enrolled face records by identity (via the
// owner map) and returns one averaged, normalized embedding per name.
func fuseEnrolled(recs []faceembed.Face, owner map[string]string) []enrolledFace {
	byName := map[string][][]float64{}
	for _, r := range recs {
		if r.Found && len(r.Vector) > 0 {
			byName[owner[r.Path]] = append(byName[owner[r.Path]], normalize(r.Vector))
		}
	}
	var out []enrolledFace
	for name, vs := range byName {
		out = append(out, enrolledFace{name: name, vector: averageNormalized(vs)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}
