//go:build llm

// smoke_test.go — the automated regression gate from becky-AI-Agent-review-1.md
// §5: builds the REAL becky-vision.exe and runs it NO-FLAGS (acceptance
// criterion 1 — image+prompt only, no --model/--gemma/--qwen) against every
// synthetic fixture in testdata\vision, asserting the JSON answer contains
// the substrings a correct read must contain. Exits nonzero (go test's
// normal behavior on a failed assertion) the moment any fixture's answer
// misses its expected evidence — a real regression gate, not a suggestion.
//
// Two speeds, because the full escalation ladder is too slow to run on every
// build (a Gemma-4 E4B llama-server spin-up alone is ~15-20s per image):
//
//   - FAST (default): BECKY_VISION_MAX_ESCALATIONS=0 caps the ladder at rung 0
//     (450M) + mandatory OCR corroboration (cmd/vision/ladder.go's
//     gatherOCRSignal, independent of the escalation cap). This proves OCR
//     corroboration ran and its literal on-screen text reached the final
//     answer — it does NOT prove the small model's own conclusion is right
//     (the review's whole finding is that the 450M alone gets that wrong).
//     ~5-10s total for all 4 fixtures.
//
//     go test -tags=llm -run TestVisionSmoke ./cmd/vision/...
//
//   - FULL (weekly): set BECKY_VISION_SMOKE_FULL=1 to run the real, uncapped
//     no-flags ladder — THE acceptance-criterion-2 gate (the ESCALATED
//     answer must be correct, not just OCR-augmented). ~15-25s per fixture
//     (spins up Gemma-4 E4B). Verified 2026-07-10 against every fixture here
//     before this file was written; see becky-AI-Agent-review-1.md's
//     RESOLUTION section for the transcripts.
//
//     BECKY_VISION_SMOKE_FULL=1 go test -tags=llm -timeout 20m -run TestVisionSmoke ./cmd/vision/...
//
// Skips (never fails red) when the model files or llama.cpp binary are
// missing on this machine — mirrors cmd/ask/intent_llm_test.go's convention
// for opt-in, model-needing tests so a clean checkout never goes red.
package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"becky-go/internal/vision"
)

// smokeFixture is one testdata\vision regression case: the image, the prompt
// becky-vision is asked (chosen to imply on-screen text/UI state, so the
// ladder's promptImpliesTextOrUI gate always fires and OCR corroboration
// always runs — every fixture here exercises that path deliberately), and
// the substrings ANY of which must appear (case-insensitively) in the final
// description for the answer to count as correct.
type smokeFixture struct {
	image       string
	prompt      string
	wantAny     []string // description must contain at least one (lowercased)
	wantOCRKind bool     // also require a "kind":"ocr","ok":true source (the canonical gate fixture only)
}

// smokeFixtures are becky-AI-Agent-review-1.md §5's four named mockups:
// terminal waiting on a prompt (THE regression case), terminal idle, an
// error dialog, an empty desktop. Substrings were chosen from REAL becky-vision
// output observed while building this gate (2026-07-10, both fast and full
// mode) — not guessed.
var smokeFixtures = []smokeFixture{
	{
		image:       "terminal_prompt_waiting.png",
		prompt:      "Is anything on this screen stuck or waiting for input?",
		wantAny:     []string{"proceed", "stuck", "waiting"},
		wantOCRKind: true,
	},
	{
		image:   "terminal_idle_prompt.png",
		prompt:  "What state is the terminal application in this screen? Is anything stuck or waiting?",
		wantAny: []string{"ready", "idle", "finished", "complete", "done"},
	},
	{
		image:   "error_dialog.png",
		prompt:  "What does this dialog box say? Is there an error?",
		wantAny: []string{"error", "stopped working", "problem"},
	},
	{
		image:   "empty_desktop.png",
		prompt:  "What is on this screen? Is anything stuck or waiting for input?",
		wantAny: []string{"empty", "blank", "nothing", "no visible"},
	},
}

// TestVisionSmoke is the gate. It builds becky-vision.exe once, then runs it
// no-flags against every fixture in smokeFixtures.
func TestVisionSmoke(t *testing.T) {
	skipIfModelsMissing(t)
	exePath := buildVisionExe(t)
	full := strings.TrimSpace(os.Getenv("BECKY_VISION_SMOKE_FULL")) != ""

	for _, fx := range smokeFixtures {
		fx := fx
		t.Run(fx.image, func(t *testing.T) {
			res := runVisionNoFlags(t, exePath, fx.image, fx.prompt, full)

			if res.Degraded {
				t.Fatalf("becky-vision degraded on %s (should have produced a real answer): %s", fx.image, res.Error)
			}
			if strings.TrimSpace(res.Description) == "" {
				t.Fatalf("becky-vision returned an empty description for %s", fx.image)
			}

			low := strings.ToLower(res.Description)
			if !containsAnySmoke(low, fx.wantAny) {
				t.Errorf("%s: description matched none of %v\ngot: %s", fx.image, fx.wantAny, res.Description)
			}

			if fx.wantOCRKind && !hasOKSource(res.Sources, "ocr") {
				t.Errorf("%s: expected a \"kind\":\"ocr\",\"ok\":true source in the envelope, got sources=%+v", fx.image, res.Sources)
			}
		})
	}
}

// skipIfModelsMissing mirrors intent_llm_test.go's Ready()-then-skip
// convention: a clean checkout / CI box without the GGUFs or llama.cpp must
// SKIP, never fail red.
func skipIfModelsMissing(t *testing.T) {
	t.Helper()
	if _, _, err := vision.DiscoverModels(vision.DefaultModelDir); err != nil {
		t.Skipf("450M model/mmproj not found in %s, skipping: %v", vision.DefaultModelDir, err)
	}
	if _, err := os.Stat(vision.DefaultBin); err != nil {
		t.Skipf("llama-mtmd-cli not found at %s, skipping: %v", vision.DefaultBin, err)
	}
}

// buildVisionExe compiles the CURRENT source of this package (becky-vision)
// to a temp file, so the gate always exercises today's code — never a stale
// PATH install.
func buildVisionExe(t *testing.T) string {
	t.Helper()
	exePath := filepath.Join(t.TempDir(), "becky-vision-smoke.exe")
	cmd := exec.Command("go", "build", "-o", exePath, ".")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build becky-vision for the smoke gate: %v\n%s", err, out)
	}
	return exePath
}

// runVisionNoFlags shells out to exePath exactly as becky-AI-Agent-review-1.md
// acceptance criterion 1 requires: --image/--prompt/--json only, never a
// model-selecting flag. "Fast mode" is still a no-flags call — the ladder
// cap travels as an env var (BECKY_VISION_MAX_ESCALATIONS), the same
// convention as this codebase's other runtime knobs (BECKY_AVLM_VARIANT
// etc.), never a CLI flag.
func runVisionNoFlags(t *testing.T, exePath, imageName, prompt string, full bool) vision.Result {
	t.Helper()
	// Fixtures live at the MODULE root (becky-go\testdata\vision), not a
	// package-local testdata dir — becky-AI-Agent-review-1.md's own wording
	// and the AUTOPILOT work order both name that exact path, shared across
	// every cmd\* tool that might one day want the same regression images.
	// go test's cwd is this package dir (cmd\vision), hence the "..\..".
	imgPath, err := filepath.Abs(filepath.Join("..", "..", "testdata", "vision", imageName))
	if err != nil {
		t.Fatalf("resolve fixture path %s: %v", imageName, err)
	}
	if _, err := os.Stat(imgPath); err != nil {
		t.Fatalf("fixture missing: %v", err)
	}

	timeout := 60 * time.Second
	env := envWithout(os.Environ(), "BECKY_VISION_MAX_ESCALATIONS") // never inherit an ambient override
	if full {
		timeout = 5 * time.Minute
	} else {
		env = append(env, "BECKY_VISION_MAX_ESCALATIONS=0")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, exePath, "--image", imgPath, "--prompt", prompt, "--json")
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		t.Fatalf("becky-vision --image %s --prompt %q --json failed: %v\nstderr: %s", imgPath, prompt, err, stderr)
	}

	var res vision.Result
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("parse becky-vision JSON output for %s: %v\nraw: %s", imageName, err, out)
	}
	return res
}

// envWithout returns a copy of env with every "key=..." entry for key removed
// (case-sensitive, matching Windows/Go env slice conventions here) so a
// caller can append a single deterministic override without depending on
// duplicate-key resolution order, which os/exec does not document.
func envWithout(env []string, key string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func containsAnySmoke(haystack string, subs []string) bool {
	for _, s := range subs {
		if strings.Contains(haystack, s) {
			return true
		}
	}
	return false
}

func hasOKSource(sources []vision.Source, kind string) bool {
	for _, s := range sources {
		if s.Kind == kind && s.OK {
			return true
		}
	}
	return false
}
