// falconaim.go — the SECOND opinion on where the subject is.
//
// Reka Edge (internal/ground) is a 7B language model doing grounded detection.
// Falcon-Perception is a 0.6B open-vocabulary DETECTOR running on ONNX Runtime
// with no torch at all. Different size, different architecture, different
// failure modes — which is the whole point. becky's rule
// (FORENSIC-OUTPUT-PHILOSOPHY.md) is that one signal is a candidate and two
// agreeing is a conclusion, and until now the framing path had exactly one.
//
// Jordan asked why it was not wired in, and the honest answer is that nobody
// had done it: the model, its ONNX weights and its venv have been sitting in
// models/falcon-perception since 2026-07-09. Verified working on this machine
// 2026-08-20 — four people found in the reference image, boxes and masks, on CPU.
//
// It is rung 4 of the ladder in framing.go: consulted when Reka could not locate
// anything, because at ~6s per frame after a one-time ~11s load it is too slow
// to run on every span, and there is no reason to when the first detector
// already answered.
package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"becky-go/internal/config"
	"becky-go/internal/pyhelpers"
)

// falconFrames is how many frames of a span to spend on the second opinion.
// Small on purpose: this rung answers "is there a person here at all, and
// roughly where", not "trace the camera path".
const falconFrames = 4

// falconDir is where the model, its venv and its script live. Kept beside the
// weights rather than copied into pyhelpers because it carries its OWN venv —
// this machine's global PIP_TARGET makes shared interpreters unreliable, and
// that is exactly the breakage this rung exists to survive.
func falconDir() string { return `X:\AI-2\becky-tools\models\falcon-perception` }

type falconLine struct {
	OK    bool   `json:"ok"`
	Frame string `json:"frame"`
	Boxes []struct {
		X, Y, W, H float64
		Confidence float64
	} `json:"boxes"`
}

// falconAimX returns where Falcon-Perception says the person is across a span,
// as a fraction of frame width, and how many frames actually contained one.
//
// ok=false means "this rung has no answer" — a missing venv, a failed load, or
// simply no person found. Never an error: the ladder moves to the next rung.
func falconAimX(cfg config.Config, src string, start, end float64) (x float64, frames int, ok bool) {
	dir := falconDir()
	// The model's OWN venv: this machine's global PIP_TARGET makes shared
	// interpreters unreliable, and surviving that breakage is half of why this
	// rung exists at all.
	py := filepath.Join(dir, "venv", "Scripts", "python.exe")
	if !fileExistsAt(py) || !fileExistsAt(filepath.Join(dir, "falcon_perception_onnx.py")) {
		return 0, 0, false
	}
	// The batch runner is REPO code (pyhelpers/falcon_detect.py); only the
	// detector class and its weights live in the gitignored model directory.
	script, serr := pyhelpers.Materialize("falcon_detect.py", pyhelpers.FalconDetect)
	if serr != nil {
		return 0, 0, false
	}
	if end <= start {
		return 0, 0, false
	}

	tmp, err := os.MkdirTemp("", "becky-falcon-")
	if err != nil {
		return 0, 0, false
	}
	defer os.RemoveAll(tmp)

	fps := falconFrames / (end - start)
	cmd := exec.Command(cfg.FFmpeg, "-y", "-v", "error",
		"-ss", strconv.FormatFloat(start, 'f', 6, 64),
		"-t", strconv.FormatFloat(end-start, 'f', 6, 64),
		"-i", src,
		"-vf", "fps="+strconv.FormatFloat(fps, 'f', -1, 64)+",scale=640:-2",
		filepath.Join(tmp, "f_%05d.jpg"))
	if err := cmd.Run(); err != nil {
		return 0, 0, false
	}

	out, err := exec.Command(py, script, "--model-dir", dir, "--frames", tmp, "--query", "person").Output()
	if err != nil {
		return 0, 0, false
	}

	var sum float64
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var fl falconLine
		if json.Unmarshal([]byte(line), &fl) != nil || !fl.OK || len(fl.Boxes) == 0 {
			continue
		}
		// The LARGEST box, same rule as the grounding path: on a two-shot the
		// bigger figure is the one the shot is built around.
		best := fl.Boxes[0]
		for _, b := range fl.Boxes[1:] {
			if b.W*b.H > best.W*best.H {
				best = b
			}
		}
		if best.W <= 0 || best.W*best.H >= occlusionArea {
			continue
		}
		sum += best.X + best.W/2
		frames++
	}
	if frames == 0 {
		return 0, 0, false
	}
	return sum / float64(frames), frames, true
}
