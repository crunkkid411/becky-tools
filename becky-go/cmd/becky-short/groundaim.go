// groundaim.go — the grounding SERVER's lifetime, and the thresholds the
// framing ladder shares.
//
// The aiming logic that used to live here is now framing.go, which turned a
// two-outcome decision (aim, or REFUSE) into a seven-rung ladder that always
// returns something. What remains is the part that is genuinely about the
// grounding model rather than about framing: starting it once, shutting it
// down, and the two measured constants the ladder reads.
//
// internal/pyhelpers/ground.py — Reka Edge grounded detection, "Detect: <thing>"
// to a box — was written, verified on this machine, documented, and then never
// embedded or called by anything. focalaim.go's own comment had already written
// down what was missing: "What would make this shippable is the second signal: a
// VL naming what the viewer should be on, agreeing with where the motion is."
// This is the half that joins it up; framing.go is the half that uses it.
package main

import (
	"sync"

	"becky-go/internal/config"
	"becky-go/internal/ground"
)

// The grounding server is a process-wide singleton, started LAZILY on the first
// span that actually needs it and shared by every span after.
//
// Lazy because the start costs ~45 seconds and most spans never reach the
// fallback — the pose tracker handles them. Paying 45s up front on every job,
// including the ones that never ask a question, is exactly the kind of cost
// that gets a feature turned off again.
//
// Package-level rather than threaded through render -> renderJumpcutShort ->
// resolveCrop because those already carry a dozen parameters and one process
// only ever wants one server. groundOnce makes the start race-free.
// RESTARTABLE, not a sync.Once. The critic pass (critic.go) watches the
// rendered file with Gemma-4, and Gemma (4.2GB) plus Reka (5.5GB) do not both
// fit on an 8GB card - so the grounding server must SHUT DOWN before the critic
// runs and START AGAIN for the re-frame that follows. A sync.Once cannot do
// that: once tripped it stays tripped, so after the first closeGrounder() every
// later span silently got a nil runner and the whole ladder skipped its
// grounding rungs. Same laziness, same one-server-per-process guarantee, but the
// flag can be cleared.
var (
	groundMu     sync.Mutex
	groundOpened bool
	groundRunner *ground.Runner
	groundErr    error
)

// grounder returns the shared grounding runner, starting it on first use.
// A failure is remembered, so a machine without the model pays the failed start
// once rather than once per span.
func grounder(cfg config.Config, logf func(string, ...any)) (*ground.Runner, error) {
	groundMu.Lock()
	defer groundMu.Unlock()
	if !groundOpened {
		groundOpened = true
		groundRunner, groundErr = ground.New(cfg, logf)
	}
	return groundRunner, groundErr
}

// closeGrounder shuts the shared server down. main defers it.
// closeGrounder shuts the shared server down and frees the card. main defers
// it, and the critic loop calls it before starting Gemma-4. The next grounder()
// starts a fresh server - a failed start is NOT remembered across a close,
// because "the model would not load while Gemma had the card" is a different
// fact from "this machine has no model".
func closeGrounder() {
	groundMu.Lock()
	defer groundMu.Unlock()
	if groundRunner != nil {
		groundRunner.Close()
		groundRunner = nil
	}
	groundOpened = false
	groundErr = nil
}

// centredEnough is how close a grounded subject's centre must sit to the
// frame's own centre before framing it dead centre is the SAME decision as
// framing the subject. Inside this, aiming and centring produce the same rect
// anyway, so the note says "already framed" rather than implying a choice.
const centredEnough = 0.06

// occlusionArea is the share of the frame a detected box may cover before it
// stops being a SUBJECT and starts being an OCCLUSION.
//
// Measured on the BLINDFOLD master at 552-557s: someone's shirt swings into the
// lens and black-and-white fabric fills the whole frame for five seconds. Reka
// dutifully grounds it — it IS a shirt, and it IS right there — and a crop
// "aimed" at a box covering the entire frame is a centre crop with a confident
// note attached, which is the precise thing the ladder exists to prevent.
//
// Nothing a viewer is meant to look at fills 92% of a 16:9 frame. Frames over
// this contribute NO POSITION (ground.Samples drops them) rather than voting for
// the middle, and deadtail.go trims a tail made of them.
const occlusionArea = 0.92

// firstLineStr trims a message to its first line for a report note.
func firstLineStr(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}
