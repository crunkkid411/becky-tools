// becky-perceive — one dumb call to Falcon-Perception (open-vocabulary
// detection/segmentation, ~0.6B params, CPU/ONNX, zero VRAM): give it an
// image + a plain phrase, get back bounding boxes for every match.
//
//	becky-perceive <image-path> "<phrase>" [--limit N] [--pretty]
//	becky-perceive --image <image-path> "<phrase>" [--limit N] [--pretty]
//
// Backed by the verified driver at X:\AI-2\becky-tools\models\falcon-perception\ (an
// isolated CPU-only onnxruntime venv + falcon_perception_onnx.py). This Go
// binary shells to that venv's python.exe directly (absolute path, no
// activation) and passes it --json (an additive flag added to the driver so
// it emits ONE clean JSON line to stdout instead of its normal human-readable
// debug prints).
//
// Default stdout: {"ok":true,"query":"...","count":N,"boxes":[{x,y,w,h,confidence}],...}
// box x/y/w/h are pixel coordinates in the ORIGINAL image (top-left origin),
// not the 256x256 tensor Falcon-Perception runs inference at.
// --pretty prints the same result as high-contrast colored text instead.
// Exit 0 on success; nonzero with {"ok":false,"error":"..."} on failure
// (missing image, missing model files, or a python/inference error).
//
// First call per process pays a ~9s model-load cost (2.5GB of ONNX weights);
// each becky-perceive invocation is a fresh process, so every call pays it.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Falcon-Perception lives at a fixed, already-verified location (single
// consumer today) — hardcoded absolute paths per the build spec, not routed
// through internal/config (that package exists for paths shared across many
// tools; adding a speculative entry there for one caller is unneeded).
const (
	falconDir    = `X:\AI-2\becky-tools\models\falcon-perception`
	falconPython = falconDir + `\venv\Scripts\python.exe`
	falconDriver = falconDir + `\falcon_perception_onnx.py`
	falconOnnx   = falconDir + `\onnx`
)

// driverTimeout bounds one python subprocess call. Verified runs take
// ~5-12s (cold model load ~5-9s + inference); 120s leaves generous headroom
// without letting a stuck subprocess hang becky-perceive forever.
const driverTimeout = 120 * time.Second

// Box is one detection in the original image's pixel coordinate space.
type Box struct {
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	W          float64 `json:"w"`
	H          float64 `json:"h"`
	Confidence float64 `json:"confidence"`
}

// successEnvelope is becky-perceive's stdout contract on success.
type successEnvelope struct {
	OK          bool    `json:"ok"`
	Query       string  `json:"query"`
	Count       int     `json:"count"`
	Boxes       []Box   `json:"boxes"`
	ImageWidth  int     `json:"image_width,omitempty"`
	ImageHeight int     `json:"image_height,omitempty"`
	LoadSeconds float64 `json:"load_seconds,omitempty"`
}

// errorEnvelope is becky-perceive's stdout contract on failure.
type errorEnvelope struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

// driverOutput mirrors falcon_perception_onnx.py's --json stdout line.
type driverOutput struct {
	OK          bool    `json:"ok"`
	Error       string  `json:"error"`
	Query       string  `json:"query"`
	Count       int     `json:"count"`
	Boxes       []Box   `json:"boxes"`
	ImageWidth  int     `json:"image_width"`
	ImageHeight int     `json:"image_height"`
	LoadSeconds float64 `json:"load_seconds"`
}

func main() {
	enableANSI()
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	imagePath, phrase, limit, pretty, err := parseArgs(args)
	if err != nil {
		return fail(err.Error(), pretty)
	}

	if err := checkModelInstalled(); err != nil {
		return fail(err.Error(), pretty)
	}

	abs, err := filepath.Abs(imagePath)
	if err != nil {
		abs = imagePath
	}
	if st, err := os.Stat(abs); err != nil || st.IsDir() {
		return fail(fmt.Sprintf("image not found: %s", imagePath), pretty)
	}

	out, err := runDriver(abs, phrase, limit)
	if err != nil {
		return fail(err.Error(), pretty)
	}
	if !out.OK {
		msg := out.Error
		if msg == "" {
			msg = "Falcon-Perception returned an error with no message"
		}
		return fail(msg, pretty)
	}

	env := successEnvelope{
		OK: true, Query: phrase, Count: out.Count, Boxes: out.Boxes,
		ImageWidth: out.ImageWidth, ImageHeight: out.ImageHeight, LoadSeconds: out.LoadSeconds,
	}
	if env.Boxes == nil {
		env.Boxes = []Box{}
	}
	if pretty {
		printPretty(env)
		return 0
	}
	return printSuccess(env)
}

// parseArgs does simple manual parsing (not the flag package) because the
// contract puts the two positional args (image path, phrase) BEFORE the
// flags, which flag.Parse stops scanning at. Mirrors search_library's style.
//
// --image <path> is an explicit alternative to the positional image path —
// the --image convention every image-taking becky tool shares
// (becky-AI-Agent-review-1.md acceptance criterion 8). When given, ALL
// positionals become the phrase instead of the first one being consumed as
// the image; the original two-positional form keeps working unchanged.
func parseArgs(args []string) (imagePath, phrase string, limit int, pretty bool, err error) {
	usage := `usage: becky-perceive <image-path> "<phrase>" [--limit N] [--pretty]
   or: becky-perceive --image <image-path> "<phrase>" [--limit N] [--pretty]`
	var positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--pretty":
			pretty = true
		case a == "--json":
			// No-op: becky-perceive's default output (no --pretty) is already
			// the {"ok":...} JSON envelope. Recognized explicitly so --json is
			// always a safe flag to pass, same fix as search_library.
		case a == "--image":
			i++
			if i >= len(args) {
				return "", "", 0, pretty, fmt.Errorf("--image needs a path")
			}
			imagePath = args[i]
		case strings.HasPrefix(a, "--image="):
			imagePath = strings.TrimPrefix(a, "--image=")
		case a == "--limit":
			i++
			if i >= len(args) {
				return "", "", 0, pretty, fmt.Errorf("--limit needs a number")
			}
			n, e := strconv.Atoi(args[i])
			if e != nil || n <= 0 {
				return "", "", 0, pretty, fmt.Errorf("--limit needs a positive number, got %q", args[i])
			}
			limit = n
		case strings.HasPrefix(a, "--limit="):
			n, e := strconv.Atoi(strings.TrimPrefix(a, "--limit="))
			if e != nil || n <= 0 {
				return "", "", 0, pretty, fmt.Errorf("--limit needs a positive number, got %q", a)
			}
			limit = n
		case a == "-h" || a == "--help":
			return "", "", 0, pretty, fmt.Errorf("%s", usage)
		default:
			positional = append(positional, a)
		}
	}
	if imagePath != "" {
		if len(positional) < 1 {
			return "", "", 0, pretty, fmt.Errorf("%s", usage)
		}
		return imagePath, strings.Join(positional, " "), limit, pretty, nil
	}
	if len(positional) < 2 {
		return "", "", 0, pretty, fmt.Errorf("%s", usage)
	}
	return positional[0], strings.Join(positional[1:], " "), limit, pretty, nil
}

// checkModelInstalled reports a plain-language error if any piece of the
// Falcon-Perception install (venv interpreter, driver script, onnx weights
// dir) is missing, instead of letting a raw ENOENT surface from exec.Command.
func checkModelInstalled() error {
	if _, err := os.Stat(falconPython); err != nil {
		return fmt.Errorf("Falcon-Perception's python environment is missing (expected %s)", falconPython)
	}
	if _, err := os.Stat(falconDriver); err != nil {
		return fmt.Errorf("Falcon-Perception's driver script is missing (expected %s)", falconDriver)
	}
	if st, err := os.Stat(falconOnnx); err != nil || !st.IsDir() {
		return fmt.Errorf("Falcon-Perception's model files are missing (expected %s)", falconOnnx)
	}
	return nil
}

// runDriver shells to the venv python directly (absolute path, no
// activation) and parses its single-line --json stdout.
func runDriver(imagePath, phrase string, limit int) (driverOutput, error) {
	cmdArgs := []string{falconDriver, imagePath, phrase, "--json"}
	if limit > 0 {
		cmdArgs = append(cmdArgs, "--limit", strconv.Itoa(limit))
	}

	ctx, cancel := context.WithTimeout(context.Background(), driverTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, falconPython, cmdArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	if ctx.Err() == context.DeadlineExceeded {
		return driverOutput{}, fmt.Errorf("Falcon-Perception timed out after %s", driverTimeout)
	}

	out, ok := parseDriverJSON(stdout.String())
	if ok {
		return out, nil
	}
	if runErr != nil {
		return driverOutput{}, fmt.Errorf("Falcon-Perception failed: %s", tail(stderr.String(), runErr))
	}
	return driverOutput{}, fmt.Errorf("Falcon-Perception returned output becky-perceive couldn't parse: %s", tail(stdout.String(), nil))
}

// parseDriverJSON scans bottom-up for the first line that unmarshals into the
// expected shape, tolerating any stray warning/banner noise a python lib
// might print to stdout ahead of the one JSON line (same defensive approach
// becky-ocr's parseHelperJSON uses).
func parseDriverJSON(s string) (driverOutput, bool) {
	if out, ok := tryUnmarshalDriver(strings.TrimSpace(s)); ok {
		return out, true
	}
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if out, ok := tryUnmarshalDriver(line); ok {
			return out, true
		}
	}
	return driverOutput{}, false
}

func tryUnmarshalDriver(s string) (driverOutput, bool) {
	var out driverOutput
	if json.Unmarshal([]byte(s), &out) == nil && (out.OK || out.Error != "") {
		return out, true
	}
	return driverOutput{}, false
}

func tail(s string, runErr error) string {
	s = strings.TrimSpace(s)
	if s == "" && runErr != nil {
		return runErr.Error()
	}
	const maxLen = 800
	if len(s) > maxLen {
		s = s[len(s)-maxLen:]
	}
	if runErr != nil {
		return fmt.Sprintf("%s (%v)", s, runErr)
	}
	return s
}

func printSuccess(env successEnvelope) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(env); err != nil {
		fmt.Fprintln(os.Stderr, "becky-perceive: encode:", err)
		return 1
	}
	return 0
}

// fail prints the {"ok":false,"error":"..."} envelope (or a plain-language
// line in --pretty mode) and returns the process exit code.
func fail(msg string, pretty bool) int {
	if pretty {
		fmt.Printf("%sbecky-perceive error:%s %s\n", clrErr, clrReset, msg)
		return 1
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(errorEnvelope{OK: false, Error: msg}); err != nil {
		fmt.Fprintln(os.Stderr, "becky-perceive: encode:", err)
	}
	return 1
}

// ANSI, high-contrast (bold + bright) — never dim this for "accessibility";
// bright color on a dark terminal is the accessibility aid, not a decoration.
const (
	clrReset = "\x1b[0m"
	clrTitle = "\x1b[1;95m" // bold bright magenta
	clrLabel = "\x1b[1;93m" // bold bright yellow
	clrBox   = "\x1b[92m"   // bright green
	clrDim   = "\x1b[96m"   // bright cyan (footnotes)
	clrErr   = "\x1b[1;91m" // bold bright red
)

func printPretty(env successEnvelope) {
	fmt.Printf("%s%d box(es) for \"%s\"%s\n\n", clrLabel, env.Count, env.Query, clrReset)
	if env.Count == 0 {
		fmt.Printf("%sno matches — try a different phrase.%s\n", clrDim, clrReset)
	}
	for i, b := range env.Boxes {
		fmt.Printf("%s#%d%s %sx=%.1f y=%.1f w=%.1f h=%.1f%s  confidence=%.3f\n",
			clrTitle, i+1, clrReset, clrBox, b.X, b.Y, b.W, b.H, clrReset, b.Confidence)
	}
	fmt.Printf("\n%simage %dx%d px, model load %.1fs (first call per process loads ~9s cold —\n"+
		"each becky-perceive call starts a fresh process, so every call pays this).%s\n",
		clrDim, env.ImageWidth, env.ImageHeight, env.LoadSeconds, clrReset)
}
