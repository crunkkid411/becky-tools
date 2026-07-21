package main

// forensic.go — H-7: the forensic path as ONE in-app verb.
//
// Before this, the query → qmd recall → becky-judge → becky-hits → timeline
// pipeline was reachable only by an OUTSIDE agent building the reel first and
// launching the app with BECKY_REVIEW_REEL (or calling load_reel). The
// forensic_query verb runs the same three proven stages against the OPEN case
// folder, from inside a running session:
//
//	Stage 1+2  becky-judge : qmd RECALL over the transcripts, then the LLM
//	           judge keeps only genuine hits → _forensic_hits.json
//	Stage 3    becky-hits  : hit-list → a real reel resolved against the folder
//	           (+ the <reel>.questions.json sidecar for the Q&A panel)
//	Finish     a.LoadReel  : the reel lands on the timeline as ONE undo span,
//	           and the questions sidecar loads into the Q&A cards.
//
// This is an additive bridge verb on the SAME seam the human UI uses — no MCP
// server, no separate AI tool surface (BUILD-INPUTS.md:18-22), and the engine
// is not forked: new capability = new verb (BUILD_1.md:27-28).
//
// The real execs sit behind the runJudge/runHits seams (same pattern as
// transcribe.go's runTranscribe) so `go test` exercises the whole orchestration
// with fakes — no test ever shells the real judge (which costs an LLM call).
// Degrade-never-crash: a missing binary or a failed stage is a typed,
// plain-language error the chat/status line can show — never a panic, and
// never a half-loaded timeline (the reel only loads after every stage
// succeeded). becky-judge itself degrades internally (no Claude → it emits
// all Stage-1 candidates with a note), so recall is never lost to a judge
// failure.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"becky-go/internal/proc"
)

// judgeTimeout bounds the becky-judge exec. The judge is an LLM pass over up
// to --limit candidate windows, so the default is generous;
// BECKY_JUDGE_TIMEOUT (a Go duration like "30m") overrides it.
const judgeTimeout = 20 * time.Minute

// hitsTimeout bounds the becky-hits exec — pure Go over small JSON + .srt
// sidecars, so quick.
const hitsTimeout = 2 * time.Minute

// forensicHitsName / forensicReelName are the artifact names the pipeline has
// always used (becky-judge's documented output, becky-hits' default), written
// into the case folder like the external-agent flow always has. Re-running a
// query overwrites them — they are derived artifacts, not evidence.
const (
	forensicHitsName = "_forensic_hits.json"
	forensicReelName = "becky-hits.reel.json"
)

// ForensicResult is forensic_query's reply payload.
type ForensicResult struct {
	Timeline TimelineView `json:"timeline"`
	// Clips is how many hits landed on the timeline (== len(Timeline.Clips)).
	Clips int `json:"clips"`
	// Reel is the reel JSON becky-hits wrote (also now the app's save path).
	Reel string `json:"reel"`
	// Note carries plain-language degrade info ("judge fell back to …"), "" when clean.
	Note string `json:"note,omitempty"`
}

// runJudge is the seam over the real becky-judge exec:
//
//	becky-judge --folder <folder> --query <query> --out <outPath>
//
// Production never reassigns it; tests fake it with a function that writes a
// canned hit-list.
var runJudge = func(ctx context.Context, bin, folder, query, outPath string) error {
	cmd := exec.CommandContext(ctx, bin, "--folder", folder, "--query", query, "--out", outPath)
	proc.NoWindow(cmd)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("becky-judge failed: %w%s", err, transcribeErrTail(errBuf.String()))
	}
	return nil
}

// runHits is the seam over the real becky-hits exec:
//
//	becky-hits --hits <hitsPath> --folder <folder> --out <outPath>
var runHits = func(ctx context.Context, bin, folder, hitsPath, outPath string) error {
	cmd := exec.CommandContext(ctx, bin, "--hits", hitsPath, "--folder", folder, "--out", outPath)
	proc.NoWindow(cmd)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("becky-hits failed: %w%s", err, transcribeErrTail(errBuf.String()))
	}
	return nil
}

// ForensicQuery runs the forensic pipeline for one plain-language query and
// loads the resulting reel onto the timeline. Long-running (the judge is an
// LLM pass) — the async bridge keeps the UI alive and the H-5 events show
// progress in the activity panel.
func (a *App) ForensicQuery(query string) (ForensicResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return ForensicResult{}, fmt.Errorf("empty forensic query — say what to look for, e.g. \"asking people to harass Shelby\"")
	}

	a.mu.Lock()
	folder := a.folder
	a.mu.Unlock()
	if folder == "" {
		return ForensicResult{}, fmt.Errorf("open a case folder first — the forensic search runs over its transcripts")
	}

	judgeBin, err := resolveForensicBin("BECKY_JUDGE", "becky-judge")
	if err != nil {
		return ForensicResult{}, err
	}
	hitsBin, err := resolveForensicBin("BECKY_HITS", "becky-hits")
	if err != nil {
		return ForensicResult{}, err
	}

	a.emitEvent("started", "forensic_query", "Forensic search: "+truncateText(query, 80))

	// Stage 1+2: recall + judge → the hit-list.
	hitsPath := filepath.Join(folder, forensicHitsName)
	jctx, jcancel := context.WithTimeout(context.Background(), envDuration("BECKY_JUDGE_TIMEOUT", judgeTimeout))
	defer jcancel()
	if err := runJudge(jctx, judgeBin, folder, query, hitsPath); err != nil {
		a.emitEvent("done", "forensic_query", "Forensic search failed: "+truncateText(firstLine(err), 80))
		return ForensicResult{}, err
	}
	a.emitEvent("progress", "forensic_query", "Judged the candidates — building the reel…")

	// Stage 3: hit-list → reel (+ questions sidecar) resolved against the folder.
	reelPath := filepath.Join(folder, forensicReelName)
	hctx, hcancel := context.WithTimeout(context.Background(), hitsTimeout)
	defer hcancel()
	if err := runHits(hctx, hitsBin, folder, hitsPath, reelPath); err != nil {
		a.emitEvent("done", "forensic_query", "Forensic search failed: "+truncateText(firstLine(err), 80))
		return ForensicResult{}, err
	}

	// Finish: the reel lands on the timeline (one undo span — LoadReel pushes
	// exactly one snapshot), and the Q&A cards load if becky-hits wrote any.
	tl, err := a.LoadReel(reelPath)
	if err != nil {
		a.emitEvent("done", "forensic_query", "Forensic search failed: "+truncateText(firstLine(err), 80))
		return ForensicResult{}, fmt.Errorf("the pipeline ran but its reel could not be loaded: %w", err)
	}
	note := ""
	qPath := strings.TrimSuffix(reelPath, filepath.Ext(reelPath)) + ".questions.json"
	if fileExists(qPath) {
		if err := a.LoadQuestions(qPath); err != nil {
			note = "review questions could not be loaded: " + firstLine(err)
		}
	}

	a.emitEvent("done", "forensic_query", fmt.Sprintf("Forensic search done: %d hit(s) on the timeline.", len(tl.Clips)))
	return ForensicResult{Timeline: tl, Clips: len(tl.Clips), Reel: reelPath, Note: note}, nil
}

// resolveForensicBin finds a pipeline executable, in the same order as
// resolveTranscribeBin: the env override → next to the running exe → PATH.
func resolveForensicBin(envVar, name string) (string, error) {
	if p := strings.TrimSpace(os.Getenv(envVar)); p != "" {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if fileExists(p) {
			return p, nil
		}
		return "", fmt.Errorf("%s not found at %s=%q", name, envVar, p)
	}
	if exe, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(exe), exeName(name))
		if fileExists(cand) {
			return cand, nil
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("%s not found — build it with build-all-tools.bat (or set %s to its path)", name, envVar)
}

// exeName appends .exe on Windows.
func exeName(name string) string {
	if isWindows() {
		return name + ".exe"
	}
	return name
}

// envDuration reads a Go duration from the environment, falling back to def on
// unset/unparsable.
func envDuration(key string, def time.Duration) time.Duration {
	if s := strings.TrimSpace(os.Getenv(key)); s != "" {
		if d, err := time.ParseDuration(s); err == nil && d > 0 {
			return d
		}
	}
	return def
}
