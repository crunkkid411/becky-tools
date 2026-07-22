// ocrengine.go — the shared OCR engine runner, MOVED here (2026-07-10, P1 slice
// B of becky-AI-Agent-review-1.md) from cmd/ocr so cmd/vision's escalation
// ladder can call the SAME PaddleOCR/RapidOCR helper becky-ocr uses for its
// mandatory corroboration step, instead of shelling out to a second .exe or
// duplicating the embedded Python helper. becky-ocr (cmd/ocr) now calls
// RunOCR here too — one engine, two callers, no drift between them.
//
// Owns: materializing the embedded ocr_paddle.py helper, running it over a
// batch of image paths in ONE warm-model invocation, and parsing its JSON
// contract. Heavy compute stays in ONNX; this is glue + parsing, exactly like
// the rest of this package's relationship to llama.cpp.
package vision

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"becky-go/internal/config"
)

//go:embed ocr_paddle.py
var ocrPaddlePy []byte

// OCRLine is one recognized line as the Python helper reports it: raw text,
// recognition confidence, and its bounding box. Categorization (candidate_*)
// and confidence-threshold splitting are becky-ocr's concern, not this
// engine's — callers decide what to do with the raw lines.
type OCRLine struct {
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence"`
	BBox       []int   `json:"bbox"`
}

// OCRFrame is one image's OCR result.
type OCRFrame struct {
	Path            string    `json:"path"`
	Found           bool      `json:"found"`
	RotationApplied int       `json:"rotation_applied"`
	Lines           []OCRLine `json:"lines"`
	Error           string    `json:"error,omitempty"`
}

// OCRHelperResult mirrors ocr_paddle.py's stdout JSON contract.
type OCRHelperResult struct {
	Skipped bool       `json:"skipped"`
	Reason  string     `json:"reason"`
	Engine  string     `json:"engine"`
	Results []OCRFrame `json:"results"`
}

// RunOCR materializes the embedded helper, runs it over the batch of image
// paths under the face interpreter + PYTHONPATH (the OCR deps live in the
// same --target site-packages dir as the face deps), and parses its JSON. A
// helper "skipped" result (missing deps/models) is surfaced as an error so
// the caller can degrade gracefully (never a crash, never half a result) —
// mirrors Describe()'s degrade-never-panic contract.
func RunOCR(cfg config.Config, paths []string, engine string, tryRotations, verbose bool) (OCRHelperResult, error) {
	if len(paths) == 0 {
		return OCRHelperResult{}, nil
	}
	script, err := materializeOCRScript()
	if err != nil {
		return OCRHelperResult{}, fmt.Errorf("materialize ocr helper: %w", err)
	}

	args := append([]string{script}, paths...)
	args = append(args, "--engine", engine)
	if tryRotations {
		args = append(args, "--try-rotations")
	}

	cmd := exec.Command(ocrPython(cfg), args...)
	cmd.Env = ocrChildEnv(cfg)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	if verbose {
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stderr = &stderr
	}
	if err := cmd.Run(); err != nil {
		return OCRHelperResult{}, fmt.Errorf("ocr helper failed: %v\n%s", err, tail(stderr.String()))
	}

	res, ok := parseOCRHelperJSON(stdout.String())
	if !ok {
		return OCRHelperResult{}, fmt.Errorf("could not parse ocr helper output:\n%s", tail(stdout.String()))
	}
	if res.Skipped {
		return OCRHelperResult{}, fmt.Errorf("ocr helper skipped: %s", res.Reason)
	}
	return res, nil
}

// materializeOCRScript writes the embedded helper to a stable temp path and
// returns it, mirroring internal/pyhelpers.Materialize.
func materializeOCRScript() (string, error) {
	dir := filepath.Join(os.TempDir(), "becky-vision-ocr-pyhelpers")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "ocr_paddle.py")
	if err := os.WriteFile(path, ocrPaddlePy, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// ocrPython returns the interpreter to run the helper. The OCR deps
// (rapidocr, onnxruntime, cv2) live in the SAME --target site-packages dir as
// the face deps, reached via the face interpreter.
func ocrPython(cfg config.Config) string {
	if cfg.FacePython != "" {
		return cfg.FacePython
	}
	return cfg.Python
}

// ocrChildEnv prepends the dependency site-packages dir (cfg.FacePyLib) to
// PYTHONPATH, where rapidocr/onnxruntime/cv2 are installed.
func ocrChildEnv(cfg config.Config) []string {
	env := os.Environ()
	if cfg.FacePyLib != "" {
		env = append(env, "PYTHONPATH="+cfg.FacePyLib+string(os.PathListSeparator)+os.Getenv("PYTHONPATH"))
	}
	return env
}

// parseOCRHelperJSON tolerates leading library banner noise (RapidOCR logs to
// stderr, but be defensive) by scanning bottom-up for the first line that
// unmarshals into the expected shape — the same approach internal/faceembed
// uses.
func parseOCRHelperJSON(s string) (OCRHelperResult, bool) {
	if r, ok := tryUnmarshalOCR(strings.TrimSpace(s)); ok {
		return r, true
	}
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if r, ok := tryUnmarshalOCR(line); ok {
			return r, true
		}
	}
	return OCRHelperResult{}, false
}

func tryUnmarshalOCR(s string) (OCRHelperResult, bool) {
	var r OCRHelperResult
	if json.Unmarshal([]byte(s), &r) == nil && (r.Skipped || r.Results != nil || r.Engine != "") {
		return r, true
	}
	return OCRHelperResult{}, false
}
