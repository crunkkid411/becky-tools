package main

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"becky-go/internal/forensicrun"
)

// forensicTimeout bounds the whole self-regulating resolution (identify + the Gemma-4 validate
// ladder can each be slow on a real clip); it never blocks the transcript, which is already done.
const forensicTimeout = 30 * time.Minute

// forensicResolve is the seam the forensic path calls — the real runtime by default, swappable in
// tests so the wiring (flag -> resolve -> embed, reusing our transcript) is verified without models.
var forensicResolve = forensicrun.RunAndReport

// buildForensic runs becky's self-regulating forensic resolution over THIS clip and returns the
// corroborated report to embed under "forensic". It hands our just-produced transcript to
// forensicrun so becky-transcribe is NOT run a second time for the mention signals. kb is the
// knowledge base for naming ("" => BECKY_KB env or the kb-final convention).
func buildForensic(file, subject, kb string, speakers int, transcript Output) *forensicrun.ForensicReport {
	ctx, cancel := context.WithTimeout(context.Background(), forensicTimeout)
	defer cancel()
	trJSON, _ := json.Marshal(transcript)
	rep := forensicResolve(ctx, file, subject, kb, speakers, trJSON)
	return &rep
}

// forensicSubjectNote renders the optional subject for the verbose log.
func forensicSubjectNote(subject string) string {
	if s := strings.TrimSpace(subject); s != "" {
		return " + locate " + s + " on screen"
	}
	return ""
}
