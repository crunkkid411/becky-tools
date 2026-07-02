package main

// autocut.go wires becky-cut's EXISTING silence/VAD auto-editor detection
// (cmd/cut) into becky-clip as a "propose keep segments" verb — it NEVER
// reimplements silence detection here, it only shells out to becky-cut in
// --dry-run mode (decide only, never render) and converts its decision list
// into timeline-ready {in,out} spans. The original video is never modified:
// --dry-run makes becky-cut skip its own render step entirely.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"becky-go/internal/proc"
)

// autoCutTimeout bounds one becky-cut --dry-run exec. auto-editor detection
// (and an optional VAD pass) on a long video can take a while even with no
// render; BECKY_CUT_TIMEOUT (a Go duration like "20m") overrides it.
const autoCutTimeout = 15 * time.Minute

// AutoCutSegment is one KEPT span from becky-cut's cut/VAD decision list, in
// the source video's own seconds — ready to feed straight into AddClip.
type AutoCutSegment struct {
	In  float64 `json:"in"`
	Out float64 `json:"out"`
}

// AutoCutResult is the reply for the autocut_silence verb: the KEEP segments
// becky-cut's silence/VAD detection found. Segments is always a (possibly
// empty) array — never null. Note carries a plain-language reason when the
// result is empty because of a degrade (becky-cut missing, a shell failure,
// unparseable output) rather than "no speech found".
type AutoCutResult struct {
	Segments []AutoCutSegment `json:"segments"`
	Note     string           `json:"note,omitempty"`
}

// emptyAutoCut is the shared degrade reply: an empty (never null) segment list
// plus a plain-language note.
func emptyAutoCut(note string) AutoCutResult {
	return AutoCutResult{Segments: []AutoCutSegment{}, Note: note}
}

// runAutoCut is the seam over the real becky-cut exec. It runs
//
//	becky-cut <videoPath> --dry-run
//
// and returns its raw stdout JSON report (see cmd/cut/main.go) for
// parseAutoCutKeepSegments. Defaults to the real exec; tests override it with
// a fake that returns canned JSON so the whole shell-and-parse flow is
// exercised offline. Production never reassigns it.
var runAutoCut = func(ctx context.Context, cutBin, videoPath string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, cutBin, videoPath, "--dry-run")
	proc.NoWindow(cmd)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("becky-cut failed: %w%s", err, transcribeErrTail(errBuf.String()))
	}
	return out, nil
}

// resolveCutBin finds the becky-cut executable, in the same order as
// resolveTranscribeBin: BECKY_CUT env -> next to the running exe -> PATH.
// becky-cut is OPTIONAL for this verb (unlike becky-transcribe, which is
// required for the core transcribe flow): if it can't be located we report
// ("", false) so AutoCutSilence can degrade instead of failing outright.
func resolveCutBin() (string, bool) {
	if p := strings.TrimSpace(os.Getenv("BECKY_CUT")); p != "" {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if fileExists(p) {
			return p, true
		}
		return "", false
	}
	if exe, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(exe), cutExeName())
		if fileExists(cand) {
			return cand, true
		}
	}
	if p, err := exec.LookPath("becky-cut"); err == nil {
		return p, true
	}
	return "", false
}

// cutExeName is the becky-cut sibling binary's filename for the host OS.
func cutExeName() string {
	if isWindows() {
		return "becky-cut.exe"
	}
	return "becky-cut"
}

// autoCutContext builds a per-exec context with the (overridable) timeout.
// The caller must defer the returned cancel.
func autoCutContext(parent context.Context) (context.Context, context.CancelFunc) {
	to := autoCutTimeout
	if d := strings.TrimSpace(os.Getenv("BECKY_CUT_TIMEOUT")); d != "" {
		if parsed, err := time.ParseDuration(d); err == nil && parsed > 0 {
			to = parsed
		}
	}
	return context.WithTimeout(parent, to)
}

// autoCutReport is the subset of becky-cut's --dry-run JSON report this parser
// needs: the per-chunk keep/cut decisions (see cmd/cut/main.go's decisions()).
// Every other report field (codec, fps, vad_applied, ...) is intentionally
// ignored here — becky-clip only wants the segments.
type autoCutReport struct {
	Decisions []struct {
		Status string  `json:"status"`
		Start  float64 `json:"start"`
		End    float64 `json:"end"`
	} `json:"decisions"`
}

// parseAutoCutKeepSegments parses becky-cut's --dry-run JSON stdout (its
// "decisions" list of {status,start,end} chunks) and returns only the "keep"
// spans as {in,out} seconds, in order. PURE (unit-tested against a synthetic
// sample of becky-cut's real output shape). An unparseable payload is an
// error; the caller degrades to an empty result with a note.
func parseAutoCutKeepSegments(stdout []byte) ([]AutoCutSegment, error) {
	var rep autoCutReport
	if err := json.Unmarshal(stdout, &rep); err != nil {
		return nil, fmt.Errorf("unexpected becky-cut output: %w", err)
	}
	segs := make([]AutoCutSegment, 0, len(rep.Decisions))
	for _, d := range rep.Decisions {
		if d.Status != "keep" {
			continue
		}
		segs = append(segs, AutoCutSegment{In: d.Start, Out: d.End})
	}
	return segs, nil
}

// AutoCutSilence runs becky-cut's existing silence/VAD auto-editor detection
// (NEVER reimplemented here) against the video named name (basename) in the
// open folder, in --dry-run mode so it only DECIDES and never renders/writes
// anything, and returns the KEEP segments in seconds — ready for the UI to
// feed straight into add_clip for each span ("auto-cut" the timeline).
// Read-only: becky-cut's dry-run touches no source bytes.
// Degrade-never-crash: an unresolved video, a missing becky-cut binary, or a
// becky-cut failure/unparseable-output all yield {segments:[],note:"..."} —
// never an error, so a folder without becky-cut installed still opens and
// browses fine.
func (a *App) AutoCutSilence(name string) AutoCutResult {
	v, ok := a.lookupVideo(name)
	if !ok {
		return emptyAutoCut("no such video in folder: " + name)
	}
	bin, ok := resolveCutBin()
	if !ok {
		return emptyAutoCut("becky-cut not found — build it with build-all-tools.bat (or set BECKY_CUT to its path)")
	}
	ctx, cancel := autoCutContext(context.Background())
	defer cancel()
	out, err := runAutoCut(ctx, bin, v.Path)
	if err != nil {
		return emptyAutoCut("becky-cut failed: " + firstLine(err))
	}
	segs, err := parseAutoCutKeepSegments(out)
	if err != nil {
		return emptyAutoCut("could not parse becky-cut output: " + firstLine(err))
	}
	return AutoCutResult{Segments: segs}
}
