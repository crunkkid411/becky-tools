// Process execution for becky-pipeline. toolRunner builds the argv for each
// becky tool from the documented chaining contract, runs the binary, captures
// stderr for diagnostics, and (for becky-embed, which prints its summary to
// stdout rather than a --output file) captures stdout into embed.json. Every
// failure is returned as a StepResult, never a panic — the caller continues the
// rest of the chain.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/sidecar"
)

// findSubtitleSidecar returns the best subtitle sidecar next to video, or "".
// Thin wrapper so the run path depends on the shared sidecar package only here.
func findSubtitleSidecar(video string) string {
	return sidecar.FindSubtitle(video)
}

// toolRunner shells out to the becky-*.exe binaries in binDir, and runs the
// in-process sidecar-ingestion steps (sidecar transcript reuse + metadata).
type toolRunner struct {
	binDir          string
	verbose         bool
	cfg             config.Config // for ffprobe (sidecar transcript duration) + db ingestion
	forceTranscribe bool          // ignore subtitle sidecars; always run Parakeet
	lang            string        // language tag stamped into sidecar transcripts
}

// stderrTailBytes caps how much of a failed tool's stderr we store in the
// manifest, so one chatty failure cannot bloat the JSON.
const stderrTailBytes = 2000

// binName returns the platform-correct binary file name for a step.
func binName(step string) string {
	name := "becky-" + step
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// optionalBinary reports whether a step should degrade to a graceful skip (rather
// than a failure) when its becky-*.exe is missing from --bin. ocr, motion, report,
// and validate are optional-by-binary: additive enrichments that must never break the
// chain — mirroring each tool's own degrade-never-crash behaviour. validate also needs
// the Gemma-4 model + GPU, so its binary being absent is expected in a GPU-less
// environment. Every other step's missing binary is a real setup error the operator
// should see.
func optionalBinary(step string) bool {
	return step == stepOCR || step == stepMotion || step == stepReport || step == stepValidate
}

// fileExists reports whether path exists and is non-empty on disk. Used by
// validate's stepArgs to pass optional context files only when they are available.
func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Size() > 0
}

// stepArgs builds the positional+flag argv for a step (excluding the binary
// path itself) per the documented chain. The boolean reports whether the step
// is runnable at all given current inputs (e.g. identify with no --kb).
func stepArgs(step, video, kb string, p stepPaths) (args []string, runnable bool, why string) {
	switch step {
	case stepTranscribe:
		return []string{video, "--output", p.transcript}, true, ""
	case stepDiarize:
		return []string{video, "--output", p.diarized}, true, ""
	case stepEvents:
		// diarize -> events(--diarized); frames land in the shared osint dir.
		return []string{video, "--diarized", p.diarized,
			"--osint-dir", p.osintDir, "--output", p.events}, true, ""
	case stepOSINT:
		// events -> osint(--events); reuse the same frame export dir.
		return []string{video, "--events", p.events,
			"--output-dir", p.osintDir, "--output", p.osintManifest}, true, ""
	case stepOCR:
		// osint -> ocr(--manifest osint-manifest.json): OCR the scene-change frames
		// osint exported and write the recognized text into the SAME forensic DB the
		// rest of the chain uses, so `becky find` surfaces on-screen text (addresses,
		// plates, names, chat) alongside the transcript. becky-ocr writes ocr.json as
		// its run summary and exits 0 even when the OCR model/deps are missing
		// (degrade note inside the JSON), so the chain never breaks on a missing model.
		return []string{"--manifest", p.osintManifest,
			"--db", p.embedDB, "--output", p.ocrJSON}, true, ""
	case stepEmbed:
		// embed reads the transcript JSON, embeds segments into --db. It prints
		// its summary to stdout (no --output flag), so the runner captures it.
		return []string{p.transcript, "--source", video, "--db", p.embedDB}, true, ""
	case stepIdentify:
		// identify is optional and only runnable with a knowledge base.
		if kb == "" {
			return nil, false, "no --kb provided"
		}
		return []string{video, "--kb", kb, "--output", p.identify}, true, ""
	case stepMotion:
		// motion reads the video at true source fps (needs ffmpeg on PATH) and emits
		// a burst timeline. No dependency on other steps — runs on the raw video.
		return []string{video, "--output", p.motion}, true, ""
	case stepValidate:
		// validate is the LLM AV-description step.  It benefits from all prior
		// sidecars and -- crucially -- from motion.json to target the analysis
		// window at the highest-scored motion burst instead of blind 1-fps sampling
		// (SPEC-VIDEO-ANALYSIS.md §5).  Every context file is optional: we pass it
		// only when it exists so validate's own degrade path handles any gaps.
		args := []string{video, "--output", p.validateJSON}
		for _, pair := range [][]string{
			{"--transcript", p.transcript},
			{"--events", p.events},
			{"--identify", p.identify},
			{"--motion", p.motion},
		} {
			if fileExists(pair[1]) {
				args = append(args, pair[0], pair[1])
			}
		}
		return args, true, ""
	case stepReport:
		// report reads whatever sidecars are available (graceful when some are missing)
		// and emits a structured case report. Pass only files that already exist so
		// becky-report's own degrade path handles any that are absent.
		return reportStepArgs(p), true, ""
	default:
		return nil, false, "unknown step"
	}
}

// reportStepArgs builds the becky-report argv, passing only sidecars that are
// present on disk. becky-report handles missing inputs via its Degraded flag and
// exits non-zero only when ALL sidecars are absent — so the pipeline treats that
// as a failed (partial) step, not a panic.
func reportStepArgs(p stepPaths) []string {
	var args []string
	for _, pair := range [][2]string{
		{"--transcript", p.transcript},
		{"--events", p.events},
		{"--identify", p.identify},
		{"--motion", p.motion},
	} {
		if fi, err := os.Stat(pair[1]); err == nil && fi.Size() > 0 {
			args = append(args, pair[0], pair[1])
		}
	}
	return append(args, "--output", p.reportJSON, "--md-output", p.reportMD)
}

// runStep executes one planned step and returns its StepResult. A planned skip
// (already-done / missing-dependency) short-circuits without launching a process.
func (r *toolRunner) runStep(ps plannedStep, video, kb string, p stepPaths, force bool) StepResult {
	marker := outputMarker(ps.Name, p)
	sr := StepResult{Name: ps.Name, Output: marker}

	if !ps.WillRun {
		sr.Status = "skipped"
		sr.SkipReason = ps.SkipReason
		beckyio.Logf(r.verbose, "  [skip] %s (%s)", ps.Name, ps.SkipReason)
		return sr
	}

	// In-process steps (no becky-*.exe to shell): metadata ingestion, and the
	// sidecar-first transcribe path. These run in Go and short-circuit the
	// binary-exec path below.
	switch ps.Name {
	case stepMetadata:
		return r.runMetadata(video, p, sr)
	case stepTranscribe:
		if done, res := r.runTranscribe(video, p, sr); done {
			return res
		}
		// Fall through to the becky-transcribe (Parakeet) binary path below.
	}

	args, runnable, why := stepArgs(ps.Name, video, kb, p)
	if !runnable {
		// Optional step not applicable (e.g. identify without --kb): record as a
		// graceful skip, not a failure, and keep the chain going.
		sr.Status = "skipped"
		sr.SkipReason = why
		beckyio.Logf(r.verbose, "  [skip] %s (%s)", ps.Name, why)
		return sr
	}

	bin := filepath.Join(r.binDir, binName(ps.Name))
	if _, err := os.Stat(bin); err != nil {
		// An optional-binary step (ocr) whose binary isn't present degrades to a
		// graceful skip — its absence must never turn a video's status to "partial".
		// For all other steps a missing binary is a genuine setup error the operator
		// needs to see.
		if optionalBinary(ps.Name) {
			sr.Status = "skipped"
			sr.SkipReason = "becky-ocr not available: " + bin
			beckyio.Logf(r.verbose, "  [skip] %s (%s)", ps.Name, sr.SkipReason)
			return sr
		}
		sr.Status = "failed"
		sr.Error = fmt.Sprintf("binary not found: %s", bin)
		beckyio.Logf(true, "  [fail] %s: %s", ps.Name, sr.Error)
		return sr
	}

	beckyio.Logf(r.verbose, "  [run ] %s %s", binName(ps.Name), strings.Join(args, " "))
	start := time.Now()
	exitCode, stderrTail, runErr := r.exec(ps.Name, bin, args, p)
	sr.DurationMS = time.Since(start).Milliseconds()

	if runErr != nil {
		sr.Status = "failed"
		if exitCode != 0 {
			ec := exitCode
			sr.ExitCode = &ec
		}
		sr.Error = failureDetail(runErr, stderrTail)
		beckyio.Logf(true, "  [fail] %s (%dms): %s", ps.Name, sr.DurationMS, sr.Error)
		return sr
	}

	sr.Status = "ok"
	// becky-transcribe (Parakeet) wrote transcript.json with no provenance field;
	// stamp it so every transcript carries transcript_source for the manifest.
	if ps.Name == stepTranscribe {
		stampTranscriptSource(p.transcript, "parakeet")
		sr.TranscriptSource = "parakeet"
	}
	// becky-ocr exits 0 even when the OCR model/deps are missing (it writes a degrade
	// note into ocr.json instead of failing). Surface that summary in the manifest so
	// "ran but degraded gracefully" is visible without opening ocr.json.
	if ps.Name == stepOCR {
		sr.Note = ocrRunNote(p.ocrJSON)
	}
	// becky-report surfaces the conclusion count so the manifest shows the case summary
	// without requiring Jordan to open report.json.
	if ps.Name == stepReport {
		sr.Note = reportRunNote(p.reportJSON)
	}
	// becky-validate exits 0 even when Gemma-4 is unavailable (graceful degrade).
	// Surface the observation count + any motion-targeting note in the manifest.
	if ps.Name == stepValidate {
		sr.Note = validateRunNote(p.validateJSON)
	}
	beckyio.Logf(r.verbose, "  [ ok ] %s (%dms) -> %s", ps.Name, sr.DurationMS, marker)
	return sr
}

// ocrRunNote reads becky-ocr's ocr.json summary and returns a compact human note
// for the manifest: how many frames were OCR'd, how many ocr_text rows were written,
// and any top-level degrade note (e.g. "OCR engine unavailable: ..."). Best-effort:
// an unreadable/absent summary yields "" (the step still counts as ok). This is the
// graceful-degrade surface the spec asks for — a note, with exit 0 preserved.
func ocrRunNote(ocrJSONPath string) string {
	data, err := os.ReadFile(ocrJSONPath)
	if err != nil {
		return ""
	}
	var doc struct {
		FramesOCRd  int               `json:"frames_ocrd"`
		RowsWritten int               `json:"rows_written"`
		Engine      string            `json:"engine"`
		Notes       map[string]string `json:"notes"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return ""
	}
	note := fmt.Sprintf("OCR'd %d frame(s), wrote %d ocr_text row(s) [engine %s]",
		doc.FramesOCRd, doc.RowsWritten, doc.Engine)
	// Surface a degrade note (model/deps unavailable) so it's obvious in the manifest.
	if degrade, ok := doc.Notes["ocr"]; ok && degrade != "" {
		note += "; degraded: " + degrade
	}
	return note
}

// reportRunNote reads becky-report's report.json and returns a compact human note
// for the manifest: how many conclusions and review items were found, and whether
// the report is degraded (e.g. all sidecars missing). Best-effort: an unreadable
// or absent report.json yields "" — the step still counts as ok.
func reportRunNote(reportJSONPath string) string {
	data, err := os.ReadFile(reportJSONPath)
	if err != nil {
		return ""
	}
	var doc struct {
		Conclusions []json.RawMessage `json:"conclusions"`
		ReviewItems []json.RawMessage `json:"review_required"`
		Degraded    bool              `json:"degraded"`
		Notes       []string          `json:"notes,omitempty"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return ""
	}
	if doc.Degraded {
		note := "report degraded (no forensic data found)"
		if len(doc.Notes) > 0 {
			note += ": " + doc.Notes[0]
		}
		return note
	}
	return fmt.Sprintf("%d DOCUMENTED conclusion(s), %d review item(s) → report.json + report.md",
		len(doc.Conclusions), len(doc.ReviewItems))
}

// validateRunNote reads validate.json and returns a compact note for the manifest:
// how many observations were produced, whether motion-targeting was used, and any
// graceful-degradation note (e.g. "Gemma-4 unavailable"). Best-effort: an
// unreadable/absent validate.json yields "" (the step still counts as ok).
func validateRunNote(validateJSONPath string) string {
	data, err := os.ReadFile(validateJSONPath)
	if err != nil {
		return ""
	}
	var doc struct {
		Observations   []json.RawMessage `json:"observations"`
		MotionTargeted bool              `json:"motion_targeted"`
		Note           string            `json:"note,omitempty"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return ""
	}
	note := fmt.Sprintf("%d observation(s)", len(doc.Observations))
	if doc.MotionTargeted {
		note += " (motion-targeted)"
	}
	if doc.Note != "" {
		note += "; " + doc.Note
	}
	return note
}

// runMetadata is the in-process metadata step: parse the .info.json +
// .live_chat.json next to the video into metadata.json and (best-effort) ingest
// them into the forensic DB so they are queryable. A video with no metadata
// sidecars is a graceful skip (the common case for non-yt-dlp footage).
func (r *toolRunner) runMetadata(video string, p stepPaths, sr StepResult) StepResult {
	start := time.Now()
	doc, found, err := ingestMetadata(video, p.metadata)
	sr.DurationMS = time.Since(start).Milliseconds()
	if err != nil {
		sr.Status = "failed"
		sr.Error = err.Error()
		beckyio.Logf(true, "  [fail] metadata: %s", sr.Error)
		return sr
	}
	if !found {
		sr.Status = "skipped"
		sr.SkipReason = "no .info.json/.live_chat.json sidecar"
		beckyio.Logf(r.verbose, "  [skip] metadata (no sidecar)")
		return sr
	}
	// Store the metadata in the forensic DB (additive tables) so it is searchable
	// / available for cross-referencing. Best-effort: a DB failure does not fail
	// the step (metadata.json is still written), it is just noted.
	if dberr := ingestMetadataToDB(r.cfg, p.embedDB, video, doc); dberr != nil {
		beckyio.Logf(r.verbose, "  [warn] metadata DB ingest: %v", dberr)
		sr.Note = "metadata.json written; DB ingest skipped: " + dberr.Error()
	} else {
		sr.Note = "DB ingested"
	}
	sr.Status = "ok"
	beckyio.Logf(r.verbose, "  [ ok ] metadata (%dms) -> %s (info=%v chat=%d)",
		sr.DurationMS, p.metadata, doc.Info != nil, doc.ChatCount)
	return sr
}

// runTranscribe is the sidecar-first transcribe path. It returns done=true when
// it handled the step (a subtitle sidecar was reused), or done=false to let the
// caller run becky-transcribe (Parakeet). --force-transcribe always returns
// done=false so Parakeet runs even when a sidecar exists.
func (r *toolRunner) runTranscribe(video string, p stepPaths, sr StepResult) (bool, StepResult) {
	if r.forceTranscribe {
		beckyio.Logf(r.verbose, "  [note] --force-transcribe: ignoring any subtitle sidecar")
		return false, sr
	}
	subPath := findSubtitleSidecar(video)
	if subPath == "" {
		return false, sr // no sidecar: fall back to Parakeet
	}
	start := time.Now()
	sub, err := writeSidecarTranscript(subPath, video, p.transcript, r.lang, r.cfg.FFprobe)
	sr.DurationMS = time.Since(start).Milliseconds()
	if err != nil {
		// Sidecar parse failed: don't fail the chain — fall back to Parakeet.
		beckyio.Logf(true, "  [warn] sidecar transcript failed (%v); falling back to Parakeet", err)
		return false, sr
	}
	sr.Status = "ok"
	sr.TranscriptSource = "youtube-srt"
	sr.Note = fmt.Sprintf("reused %s (%s, %d segments) — Parakeet skipped",
		filepath.Base(subPath), sub.Format, len(sub.Segments))
	beckyio.Logf(r.verbose, "  [ ok ] transcribe (%dms) reused sidecar %s (%d segments) -> %s",
		sr.DurationMS, filepath.Base(subPath), len(sub.Segments), p.transcript)
	return true, sr
}

// exec runs the tool. For embed we capture stdout into embed.json (it has no
// --output flag); for all others stdout is discarded (they write their own
// --output file) and only stderr is captured for diagnostics.
func (r *toolRunner) exec(step, bin string, args []string, p stepPaths) (exitCode int, stderrTail string, err error) {
	cmd := exec.Command(bin, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr

	if step == stepEmbed {
		f, ferr := os.Create(p.embedJSON)
		if ferr != nil {
			return 0, "", fmt.Errorf("create embed.json: %w", ferr)
		}
		defer f.Close()
		cmd.Stdout = f
	} else {
		cmd.Stdout = nil // tool writes its own --output file; ignore stdout
	}

	runErr := cmd.Run()
	exitCode = exitCodeOf(runErr)
	stderrTail = tail(stderr.String(), stderrTailBytes)
	return exitCode, stderrTail, runErr
}

// exitCodeOf extracts the process exit code from a Cmd.Run error (0 if nil).
func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// failureDetail combines the run error with the captured stderr tail into a
// single human-readable string for the manifest.
func failureDetail(runErr error, stderrTail string) string {
	detail := runErr.Error()
	if stderrTail != "" {
		detail += ": " + stderrTail
	}
	return detail
}

// tail returns at most n trailing bytes of s, trimmed, collapsing whitespace so
// the manifest stays compact.
func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = "..." + s[len(s)-n:]
	}
	return strings.Join(strings.Fields(s), " ")
}

// writeJSONFile writes v to path as indented JSON with a trailing newline,
// matching the stdout shape produced by beckyio.PrintJSON.
func writeJSONFile(path string, v any) error {
	b, err := marshalIndent(v)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// marshalIndent renders v as indented JSON with a trailing newline.
func marshalIndent(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}
	return append(b, '\n'), nil
}
