// vision.go — the image-answer path for single-shot mode. becky-ask does not host
// the VLM; it shells the sibling becky-vision binary (the single-tool principle:
// the LFM2.5-VL model lives in its one tool — SPEC-ASK-SINGLESHOT.md §3.3). This
// reuses becky-ask's existing resolve-a-sibling pattern (binPathFor + exec) and
// inherits becky-vision's degrade-never-crash contract for free.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"strings"
)

// siblingVisionAsker is the production visionAsker: it locates becky-vision next
// to becky-ask and runs `becky-vision --image <f> --prompt "<q>" --json`, parsing
// the vision.Result JSON. Any failure (binary absent, bad output, model missing)
// degrades to (description="", source, degraded=true, errMsg) — exit-0 territory.
type siblingVisionAsker struct{}

// visionResult mirrors internal/vision.Result's JSON shape. We unmarshal it here
// rather than importing internal/vision so cmd/ask stays decoupled from the VLM
// package (the binary is the contract).
type visionResult struct {
	Description string `json:"description"`
	Model       string `json:"model"`
	Degraded    bool   `json:"degraded"`
	Error       string `json:"error"`
}

func (siblingVisionAsker) ask(ctx context.Context, image, question string) (string, string, bool, string) {
	bin, err := binPathFor("vision")
	if err != nil {
		return "", "lfm2.5-vl", true, "becky-vision not found next to becky-ask: " + err.Error()
	}
	cmd := exec.CommandContext(ctx, bin, "--image", image, "--prompt", question, "--json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if runErr := cmd.Run(); runErr != nil {
		// becky-vision exits 0 on a clean degrade; a non-zero exit is a usage/real
		// error — surface it as a degrade note so single-shot still exits cleanly.
		return "", "lfm2.5-vl", true, "becky-vision failed: " + tailRun(stderr.String(), 200)
	}
	var vr visionResult
	if jerr := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &vr); jerr != nil {
		return "", "lfm2.5-vl", true, "could not parse becky-vision output"
	}
	if vr.Degraded || strings.TrimSpace(vr.Description) == "" {
		msg := strings.TrimSpace(vr.Error)
		if msg == "" {
			msg = "vision model unavailable or returned no description"
		}
		return "", "lfm2.5-vl", true, msg
	}
	return vr.Description, "lfm2.5-vl", false, ""
}
