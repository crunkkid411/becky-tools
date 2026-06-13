// Package faceembed is the shared face detection + ArcFace embedding runner used
// by both becky-identify (face matching against an enrolled KB) and becky-events
// (multi_face detection). Keeping ONE runner here — rather than a copy in each
// tool — means detection + alignment + the 512-d embedding behave identically
// everywhere, and the env/python/model-name plumbing lives in a single place.
//
// The heavy compute (SCRFD detector + w600k_r50 ArcFace, InsightFace buffalo_l)
// runs in the embedded face_embed.py helper; this package only materializes that
// helper, runs it under the right PYTHONPATH, and parses its JSON. Cosine matching
// stays in the callers (deterministic, in Go), as with voice_embed.py.
package faceembed

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/pyhelpers"
)

// defaultModelName is the InsightFace model pack used when config leaves it blank.
const defaultModelName = "buffalo_l"

// Face is one image's result: the most prominent detected face (largest bbox area
// x det score), its L2-normalized 512-d embedding, and how many faces were found.
// One Face is returned per input image, in input order. Found is false (with an
// empty Vector) when no face cleared the helper's det_score>=0.5 filter.
type Face struct {
	Path     string    // the image path this record corresponds to
	Found    bool      // true if at least one face cleared the det-score filter
	NFaces   int       // number of faces detected at det_score >= 0.5
	Vector   []float64 // L2-normalized 512-d ArcFace embedding of the best face
	DetScore float64   // detection score of the best face
	BBox     []float64 // [x1,y1,x2,y2] of the best face
}

// helperRec mirrors one entry in face_embed.py's "faces" array.
type helperRec struct {
	Path     string    `json:"path"`
	Found    bool      `json:"found"`
	NFaces   int       `json:"n_faces"`
	Vector   []float64 `json:"vector"`
	DetScore float64   `json:"det_score"`
	BBox     []float64 `json:"bbox"`
}

// helperResult mirrors face_embed.py's stdout JSON contract.
type helperResult struct {
	Skipped bool        `json:"skipped"`
	Reason  string      `json:"reason"`
	Dim     int         `json:"dim"`
	Faces   []helperRec `json:"faces"`
}

// Embed runs the face helper over one or more images and returns one Face per
// input image, in input order. It materializes the embedded face_embed.py helper,
// runs it under PYTHONPATH=cfg.FacePyLib (the face deps live in a --target
// site-packages dir not on the default path), and parses the helper's JSON with a
// tolerant bottom-up line scan (InsightFace prints banners before the JSON line).
//
// A helper "skipped" result (missing deps/models, unreadable image, etc.) is
// surfaced as an error so the caller can degrade gracefully with a clear note.
// Returns (nil, nil) for an empty image list.
func Embed(cfg config.Config, images []string, device string, verbose bool) ([]Face, error) {
	if len(images) == 0 {
		return nil, nil
	}
	if cfg.FaceModelRoot == "" {
		return nil, fmt.Errorf("face model root not configured")
	}
	script, err := pyhelpers.Materialize("face_embed.py", pyhelpers.FaceEmbed)
	if err != nil {
		return nil, fmt.Errorf("materialize face helper: %w", err)
	}

	args := append([]string{script}, images...)
	args = append(args,
		"--model-root", cfg.FaceModelRoot,
		"--model-name", modelName(cfg),
		"--device", device)

	cmd := exec.Command(python(cfg), args...)
	cmd.Env = childEnv(cfg)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	if verbose {
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stderr = &stderr
	}
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("face helper failed: %v\n%s", err, tail(stderr.String()))
	}

	res, ok := parseJSON(stdout.String())
	if !ok {
		return nil, fmt.Errorf("could not parse face helper output:\n%s", tail(stdout.String()))
	}
	if res.Skipped {
		return nil, fmt.Errorf("face helper skipped: %s", res.Reason)
	}

	out := make([]Face, 0, len(res.Faces))
	for _, r := range res.Faces {
		out = append(out, Face{
			Path:     r.Path,
			Found:    r.Found,
			NFaces:   r.NFaces,
			Vector:   r.Vector,
			DetScore: r.DetScore,
			BBox:     r.BBox,
		})
	}
	beckyio.Logf(verbose, "faceembed: embedded %d image(s), dim=%d", len(out), res.Dim)
	return out, nil
}

// parseJSON tolerates leading library banner noise by scanning bottom-up for the
// first line that unmarshals into the expected shape.
func parseJSON(s string) (helperResult, bool) {
	if r, ok := tryUnmarshal(strings.TrimSpace(s)); ok {
		return r, true
	}
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if r, ok := tryUnmarshal(line); ok {
			return r, true
		}
	}
	return helperResult{}, false
}

func tryUnmarshal(s string) (helperResult, bool) {
	var r helperResult
	if json.Unmarshal([]byte(s), &r) == nil && (r.Skipped || r.Faces != nil || r.Dim > 0) {
		return r, true
	}
	return helperResult{}, false
}

func python(cfg config.Config) string {
	if cfg.FacePython != "" {
		return cfg.FacePython
	}
	return cfg.Python
}

func modelName(cfg config.Config) string {
	if cfg.FaceModelName != "" {
		return cfg.FaceModelName
	}
	return defaultModelName
}

// childEnv returns the child environment with the face dependency site-packages
// prepended to PYTHONPATH (the deps were pip-installed into a --target dir).
func childEnv(cfg config.Config) []string {
	env := os.Environ()
	if cfg.FacePyLib != "" {
		env = append(env, "PYTHONPATH="+cfg.FacePyLib+string(os.PathListSeparator)+os.Getenv("PYTHONPATH"))
	}
	return env
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 800 {
		return s[len(s)-800:]
	}
	return s
}
