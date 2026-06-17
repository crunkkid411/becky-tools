// motion_window.go — derive a targeted analysis window from becky-motion output.
//
// When --motion is passed to becky-validate, we read the motion.json produced by
// becky-motion, find the burst with the highest motion_score, and aim the Gemma-4
// window at exactly that short interval (padded by burstPad seconds on each side)
// at a higher fps than the default 1 fps.  A short targeted window affords 4-8 fps
// cheaply — far better than 1 fps across the whole clip.
//
// All failures (missing file, bad JSON, no bursts) degrade gracefully: the caller
// gets zeros back and a prose note explaining why; it continues with its default
// window.  The source file is only read, never written.
package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
)

// burstPad is the context padding (seconds) added before and after the highest-
// scored burst so Gemma-4 sees the approach and recovery, not just the peak frame.
const burstPad = 1.0

// burstFPS is the frame sample rate used for a targeted burst window.  The spec
// notes "a short window affords 4-8 fps cheaply, far better than 1 fps clip-wide."
const burstFPS = 4.0

// motionWindow reads path (a becky-motion JSON file), finds the burst with the
// highest motion_score, and returns:
//
//	start — seconds into the clip where analysis should begin (>= 0)
//	dur   — window duration in seconds (0 means "use the default --window flag")
//	fps   — recommended frame sample rate (0 means "keep the caller's default")
//	note  — human-readable summary or error message (empty = no motion file given)
//
// The returned start/dur/fps are always safe to pass to avlm: start>=0, dur>0 when
// a burst was found, fps==burstFPS when targeted.
func motionWindow(path string) (start, dur, fps float64, note string) {
	if path == "" {
		return 0, 0, 0, ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, 0, fmt.Sprintf("--motion: cannot read %q: %v; using default window", path, err)
	}
	var doc motionDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return 0, 0, 0, fmt.Sprintf("--motion: cannot parse %q: %v; using default window", path, err)
	}
	if len(doc.MotionBursts) == 0 {
		return 0, 0, 0, fmt.Sprintf("--motion: %q has no bursts (clip may be static); using default window", path)
	}

	// Highest motion_score burst is the most forensically significant window.
	best := doc.MotionBursts[0]
	for _, b := range doc.MotionBursts[1:] {
		if b.MotionScore > best.MotionScore {
			best = b
		}
	}

	// Pad outward from the burst, clamped at 0 so start is never negative.
	paddedStart := math.Max(0, best.WindowStart-burstPad)
	paddedEnd := best.WindowEnd + burstPad
	paddedDur := paddedEnd - paddedStart
	if paddedDur <= 0 {
		return 0, 0, 0, fmt.Sprintf("--motion: burst at %.3fs–%.3fs collapsed to zero duration after clamping; using default window",
			best.WindowStart, best.WindowEnd)
	}

	note = fmt.Sprintf("--motion: targeting burst at %.3fs–%.3fs (score %.3f) with %.0fs padding → window [%.1f, %.1f]s at %.0f fps",
		best.WindowStart, best.WindowEnd, best.MotionScore, burstPad, paddedStart, paddedEnd, burstFPS)
	return paddedStart, paddedDur, burstFPS, note
}

// motionDoc is the minimal subset of becky-motion output that motion_window reads.
// It mirrors cmd/motion's Output and Burst structs (only the fields needed here).
type motionDoc struct {
	MotionBursts []motionBurst `json:"motion_bursts"`
}

type motionBurst struct {
	WindowStart float64 `json:"window_start"`
	WindowEnd   float64 `json:"window_end"`
	MotionScore float64 `json:"motion_score"`
}
