// Package falcon runs Falcon-Perception (0.6B open-vocabulary detector, ONNX,
// pyhelpers/falcon_detect.py) over a folder of frames: WHERE a named thing is, as
// boxes. Its strength is finding and boxing people and objects, small or far away,
// that a face detector or a describe-the-picture model handles poorly. Model, venv
// and weights live in models/falcon-perception (gitignored).
package falcon

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"becky-go/internal/pyhelpers"
)

// Dir is where the model and its venv live (cmd/livestream has its own copy of this path).
const Dir = `X:\AI-2\becky-tools\models\falcon-perception`

// Box is one detection, as FRACTIONS of the frame.
type Box struct {
	X, Y, W, H float64
	Confidence float64
}

// Ready says whether Falcon can run here.
func Ready() bool {
	_, err := os.Stat(python())
	return err == nil
}

func python() string { return filepath.Join(Dir, "venv", "Scripts", "python.exe") }

// Find returns the boxes for query in every .jpg in dir, keyed by file name.
func Find(dir, query string) (map[string][]Box, error) {
	if !Ready() {
		return nil, fmt.Errorf("Falcon-Perception is not installed (%s)", python())
	}
	script, err := pyhelpers.Materialize("falcon_detect.py", pyhelpers.FalconDetect)
	if err != nil {
		return nil, err
	}
	raw, err := exec.Command(python(), script, "--model-dir", Dir, "--frames", dir, "--query", query).Output()
	if err != nil && len(raw) == 0 {
		return nil, fmt.Errorf("Falcon-Perception: %w", err)
	}
	return parse(string(raw)), nil
}

func parse(raw string) map[string][]Box {
	out := map[string][]Box{}
	for _, line := range strings.Split(raw, "\n") {
		var fl struct {
			OK    bool   `json:"ok"`
			Frame string `json:"frame"`
			Boxes []Box  `json:"boxes"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(line)), &fl) == nil && fl.OK {
			out[fl.Frame] = fl.Boxes
		}
	}
	return out
}
