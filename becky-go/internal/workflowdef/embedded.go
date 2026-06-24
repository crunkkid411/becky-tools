package workflowdef

import (
	_ "embed"
)

// processVideoJSON is the shipped process-video recipe, embedded so the engine has a
// known-good default even when no external workflows/ dir is present. It reproduces
// today's transcribe chain but makes diarize + the gemma4 check conditional on more
// than one speaker (SPEC-BECKY-VOICE.md §3.3).
//
//go:embed process_video.json
var processVideoJSON []byte

// ProcessVideo returns the embedded, validated process-video recipe.
func ProcessVideo() (Recipe, error) { return Parse(processVideoJSON) }
