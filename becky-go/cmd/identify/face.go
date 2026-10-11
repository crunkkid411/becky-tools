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
	"strings"

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
	name    string
	vector  []float64
	vector2 []float64 // LVFace (empty when that model is not installed)
}

// identifyFaces returns named face identifications + unidentified faces for input,
// plus the routing trail of every face it had to escalate (escalate.go).
// voiceNames are the people the voice step already heard (state for routing).
func identifyFaces(cfg config.Config, info mediainfo.Info, input string, kb Knowledge, threshold float64, device string, voiceNames map[string]bool, verbose bool) ([]Identification, []Unidentified, []FaceRoute, error) {
	if len(kb.Faces) == 0 {
		return nil, nil, nil, nil
	}
	if cfg.FaceModelRoot == "" {
		return nil, nil, nil, fmt.Errorf("face model root not configured")
	}

	// 1. Sample frames from the video (rotation-corrected, dense cadence).
	frames, times, err := sampleFaceFrames(cfg, info, input, verbose)
	if err != nil {
		return nil, nil, nil, err
	}
	defer func() {
		if len(frames) > 0 {
			os.RemoveAll(filepath.Dir(frames[0]))
		}
	}()
	if len(frames) == 0 {
		return nil, nil, nil, fmt.Errorf("no frames could be sampled from video")
	}

	// 2. Embed enrolled face-prints AND the sampled video frames in ONE helper
	// call so the InsightFace model loads exactly once per run (it was previously
	// loaded twice — KB then video — doubling startup cost). enrolledImages are
	// passed first; their results are split off, fused per name, and the remaining
	// records line up 1:1 with the sampled frames. EmbedBoth adds the second face
	// model (LVFace) on the same aligned crops.
	enrolledImages, owner := enrolledFaceImages(kb.Faces)
	if len(enrolledImages) == 0 {
		return nil, nil, nil, fmt.Errorf("no enrolled face-print images to embed")
	}
	all := append(append([]string{}, enrolledImages...), frames...)
	allRecs, err := faceembed.EmbedBoth(cfg, all, device, verbose)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(allRecs) != len(all) {
		return nil, nil, nil, fmt.Errorf("face helper returned %d records for %d images", len(allRecs), len(all))
	}
	enrolled := fuseEnrolled(allRecs[:len(enrolledImages)], owner)
	if len(enrolled) == 0 {
		return nil, nil, nil, fmt.Errorf("no faces detected in enrolled face-prints")
	}
	recs := allRecs[len(enrolledImages):]

	// 3. Read each frame-face with both models. Code settles the clear ones; the
	// strongest gray view per candidate goes to escalation (step 4).
	type best struct {
		sim float64
		ts  float64
	}
	bestByName := map[string]best{}
	gray := map[string]grayFace{}
	var unidentified []Unidentified
	for i, f := range recs {
		if !f.Found || len(f.Vector) == 0 {
			continue
		}
		ts := 0.0
		if i < len(times) {
			ts = times[i]
		}
		r := read(f, enrolled)
		switch classify(r, threshold) {
		case verdictYes:
			if cur, ok := bestByName[r.arcName]; !ok || r.arc > cur.sim {
				bestByName[r.arcName] = best{sim: r.arc, ts: ts}
			}
		case verdictNo:
			unidentified = append(unidentified, Unidentified{
				Type:        "face",
				Description: "unidentified face",
				Confidence:  math.Round(r.arc*10000) / 10000,
			})
		default:
			if cur, ok := gray[r.arcName]; !ok || r.arc > cur.read.arc {
				gray[r.arcName] = grayFace{name: r.arcName, read: r, rec: f, frame: frames[i], ts: ts}
			}
		}
	}

	fps := info.FPS
	if fps <= 0 {
		fps = 30
	}
	match := "cosine"
	if len(enrolled[0].vector2) > 0 {
		match = "cosine+lvface" // both face models agreed
	}
	var ids []Identification
	for name, b := range bestByName {
		ids = append(ids, Identification{
			Type:       "face",
			Name:       name,
			Confidence: math.Round(b.sim*10000) / 10000,
			Match:      match,
			Frames:     []FrameRef{{Frame: int(b.ts*fps + 0.5), Timestamp: math.Round(b.ts*1000) / 1000}},
		})
	}

	// 4. Escalate each gray candidate that no clear frame already settled.
	var trails []FaceRoute
	esc := &escalator{cfg: cfg, input: input, rotation: osintexport.DisplayRotation(cfg.FFprobe, input), device: device,
		verbose: verbose, threshold: threshold, enrolled: enrolled, refs: enrolledRecs(allRecs[:len(enrolledImages)], owner),
		voiceNames: voiceNames, people: peopleList(kb), hasAudio: info.HasAudio, tmp: filepath.Dir(frames[0])}
	defer esc.close()
	for _, g := range sortedGray(gray) {
		if _, settled := bestByName[g.name]; settled {
			continue
		}
		id, unid, trail := esc.escalate(g, fps)
		if id != nil {
			ids = append(ids, *id)
		}
		if unid != nil {
			unidentified = append(unidentified, *unid)
		}
		trails = append(trails, trail)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].Name < ids[j].Name })
	return ids, unidentified, trails, nil
}

// peopleList is every enrolled face's name with its aliases, "Name|a,b;Name2|c",
// for reverse image search to look for in the pages it finds.
func peopleList(kb Knowledge) string {
	var parts []string
	for _, f := range kb.Faces {
		p := f.Name + "|"
		if e, ok := kb.Entities[f.Key]; ok {
			p += strings.Join(e.Aliases, ",")
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, ";")
}

// enrolledRecs keeps each person's enrolled face records (Gemma's reference photos).
func enrolledRecs(recs []faceembed.Face, owner map[string]string) map[string][]faceembed.Face {
	out := map[string][]faceembed.Face{}
	for _, r := range recs {
		if r.Found && len(r.Vector) > 0 {
			out[owner[r.Path]] = append(out[owner[r.Path]], r)
		}
	}
	return out
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
	byName2 := map[string][][]float64{}
	for _, r := range recs {
		if r.Found && len(r.Vector) > 0 {
			byName[owner[r.Path]] = append(byName[owner[r.Path]], normalize(r.Vector))
			if len(r.Vector2) > 0 {
				byName2[owner[r.Path]] = append(byName2[owner[r.Path]], normalize(r.Vector2))
			}
		}
	}
	var out []enrolledFace
	for name, vs := range byName {
		e := enrolledFace{name: name, vector: averageNormalized(vs)}
		if len(byName2[name]) > 0 {
			e.vector2 = averageNormalized(byName2[name])
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}
