// Step planning and resume logic for becky-pipeline. Kept separate from main so
// the planner is unit-testable without spawning real becky tools.
//
// A "step" is one becky tool in the forensic chain. Each step knows the becky
// binary it runs, the on-disk output it produces (the marker used for
// idempotent skipping), and how to build its argv. The driver in main.go owns
// process execution; this file owns *what* to run and *whether* to run it.
package main

import (
	"path/filepath"
	"sort"
	"strings"
)

// Known step names, in canonical chain order. The default --steps set is the
// deterministic sweep; embed/identify/validate are optional (server/KB/model dependent).
const (
	stepTranscribe = "transcribe"
	stepMetadata   = "metadata"
	stepDiarize    = "diarize"
	stepEvents     = "events"
	stepOSINT      = "osint"
	stepOCR        = "ocr"
	stepEmbed      = "embed"
	stepIdentify   = "identify"
	stepValidate   = "validate" // LLM AV description (Gemma-4); opt-in, needs GPU model
)

// canonicalOrder is the dependency-respecting order steps must run in:
// diarize -> events(--diarized) -> osint(--events) -> ocr(--manifest osint);
// transcribe -> embed; metadata + identify are independent. metadata runs right
// after transcribe so the sidecar artifacts (transcript + info/chat) are produced
// together. ocr runs right after osint, OCR'ing the scene-change frames osint
// exported and writing the recognized text into the forensic DB so it is searchable.
// validate is LAST: it benefits from all other sidecars and uses the motion.json
// burst window for targeted analysis (when available).
var canonicalOrder = []string{
	stepTranscribe,
	stepMetadata,
	stepDiarize,
	stepEvents,
	stepOSINT,
	stepOCR,
	stepEmbed,
	stepIdentify,
	stepValidate,
}

// defaultSteps is the deterministic sweep run when --steps is omitted. metadata
// is included (it is a cheap no-op when no yt-dlp sidecars are present). ocr is
// included right after osint: it OCRs the frames osint exports and degrades
// gracefully (note, exit 0) when the OCR model/deps are unavailable, so adding it
// to the default sweep never breaks a run that lacks the OCR stack. embed and
// identify are excluded by default: embed needs the resident embedding server
// (qwen3-4b) and identify needs a --kb, so they are opt-in.
var defaultSteps = []string{stepTranscribe, stepMetadata, stepDiarize, stepEvents, stepOSINT, stepOCR}

// knownSteps is the validation set for --steps parsing.
var knownSteps = map[string]bool{
	stepTranscribe: true,
	stepMetadata:   true,
	stepDiarize:    true,
	stepEvents:     true,
	stepOSINT:      true,
	stepOCR:        true,
	stepEmbed:      true,
	stepIdentify:   true,
	stepValidate:   true,
}

// stepDeps maps a step to the steps that must have produced output before it can
// run. Used to skip dependents gracefully when a prerequisite is missing. ocr
// depends on osint (it reads the osint manifest + the frames osint exported).
var stepDeps = map[string][]string{
	stepEvents: {stepDiarize},
	stepOSINT:  {stepEvents},
	stepOCR:    {stepOSINT},
	stepEmbed:  {stepTranscribe},
}

// parseSteps turns a comma-separated --steps value into a validated, canonically
// ordered, de-duplicated slice. An empty value yields the default sweep.
func parseSteps(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return append([]string(nil), defaultSteps...), nil
	}
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		name := strings.ToLower(strings.TrimSpace(part))
		if name == "" {
			continue
		}
		if !knownSteps[name] {
			return nil, &unknownStepError{name: name}
		}
		seen[name] = true
	}
	if len(seen) == 0 {
		return nil, &unknownStepError{name: "(none)"}
	}
	ordered := make([]string, 0, len(seen))
	for _, name := range canonicalOrder {
		if seen[name] {
			ordered = append(ordered, name)
		}
	}
	return ordered, nil
}

type unknownStepError struct{ name string }

func (e *unknownStepError) Error() string { return "unknown step: " + e.name }

// stepPaths holds the on-disk paths a step reads and writes, derived from the
// per-video output directory. Centralising this keeps main.go's argv builder and
// the resume check in agreement about file names.
type stepPaths struct {
	transcript    string // transcript.json
	metadata      string // metadata.json (yt-dlp .info.json + .live_chat.json)
	diarized      string // diarized.json
	events        string // events.json
	osintDir      string // osint/ (frame export dir, shared by events + osint)
	osintManifest string // osint-manifest.json
	ocrJSON       string // ocr.json (becky-ocr manifest summary)
	embedJSON     string // embed.json (captured stdout summary)
	embedDB       string // forensic.db (or overridden by --db)
	identify      string // identify.json
	motion        string // motion.json (becky-motion burst timeline; consumed by validate)
	validateJSON  string // validate.json (becky-validate AV observations)
}

// newStepPaths derives all step output paths from the per-video output dir.
// embedDB defaults to <dir>/forensic.db unless dbOverride is set.
func newStepPaths(videoDir, dbOverride string) stepPaths {
	db := dbOverride
	if db == "" {
		db = filepath.Join(videoDir, "forensic.db")
	}
	return stepPaths{
		transcript:    filepath.Join(videoDir, "transcript.json"),
		metadata:      filepath.Join(videoDir, "metadata.json"),
		diarized:      filepath.Join(videoDir, "diarized.json"),
		events:        filepath.Join(videoDir, "events.json"),
		osintDir:      filepath.Join(videoDir, "osint"),
		osintManifest: filepath.Join(videoDir, "osint-manifest.json"),
		ocrJSON:       filepath.Join(videoDir, "ocr.json"),
		embedJSON:     filepath.Join(videoDir, "embed.json"),
		embedDB:       db,
		identify:      filepath.Join(videoDir, "identify.json"),
		motion:        filepath.Join(videoDir, "motion.json"),
		validateJSON:  filepath.Join(videoDir, "validate.json"),
	}
}

// outputMarker returns the path whose existence means a step is already done.
// This is the file the resume check stats. For embed we use the captured JSON
// summary (not the DB, which may exist from another step).
func outputMarker(step string, p stepPaths) string {
	switch step {
	case stepTranscribe:
		return p.transcript
	case stepMetadata:
		return p.metadata
	case stepDiarize:
		return p.diarized
	case stepEvents:
		return p.events
	case stepOSINT:
		return p.osintManifest
	case stepOCR:
		return p.ocrJSON
	case stepEmbed:
		return p.embedJSON
	case stepIdentify:
		return p.identify
	case stepValidate:
		return p.validateJSON
	default:
		return ""
	}
}

// statFn abstracts os.Stat so the planner is testable with an in-memory file set.
// It reports whether the path exists and whether it is non-empty.
type statFn func(path string) (exists bool, nonEmpty bool)

// plannedStep is one decided step: whether to run it, and why it was skipped.
type plannedStep struct {
	Name       string
	WillRun    bool
	SkipReason string // "" when WillRun; otherwise "already-done" or "missing-dependency: X"
}

// planSteps decides, for the requested steps, which to run vs skip. Rules:
//   - force: always run (never skip on existing output).
//   - otherwise: skip a step whose output marker already exists and is non-empty.
//   - a step whose dependency was not satisfied (its dep neither pre-existing nor
//     scheduled-to-run) is skipped with a missing-dependency reason, so the chain
//     degrades gracefully instead of invoking a tool with a missing input.
//
// stat reports existence/non-emptiness of a marker; this lets tests inject state.
func planSteps(steps []string, p stepPaths, force bool, stat statFn) []plannedStep {
	// satisfied[step] = its output will exist after the run (pre-existing or run).
	satisfied := map[string]bool{}
	plan := make([]plannedStep, 0, len(steps))
	requested := map[string]bool{}
	for _, s := range steps {
		requested[s] = true
	}

	for _, step := range steps {
		ps := plannedStep{Name: step}

		// Dependency gate: every dep must already be on disk OR be a requested
		// step we've decided will be satisfied earlier in this loop.
		missing := unmetDeps(step, p, satisfied, stat)
		if len(missing) > 0 {
			ps.WillRun = false
			ps.SkipReason = "missing-dependency: " + strings.Join(missing, ",")
			plan = append(plan, ps)
			continue
		}

		marker := outputMarker(step, p)
		exists, nonEmpty := false, false
		if marker != "" {
			exists, nonEmpty = stat(marker)
		}
		done := exists && nonEmpty

		if done && !force {
			ps.WillRun = false
			ps.SkipReason = "already-done"
			satisfied[step] = true // its output is on disk for dependents
		} else {
			ps.WillRun = true
			satisfied[step] = true // will be produced by this run
		}
		plan = append(plan, ps)
	}
	return plan
}

// unmetDeps returns the dependency steps for `step` that are neither already on
// disk nor satisfied (pre-existing/scheduled) within this plan.
func unmetDeps(step string, p stepPaths, satisfied map[string]bool, stat statFn) []string {
	deps := stepDeps[step]
	if len(deps) == 0 {
		return nil
	}
	var missing []string
	for _, dep := range deps {
		if satisfied[dep] {
			continue
		}
		// Dep output already on disk from a prior run?
		marker := outputMarker(dep, p)
		if marker != "" {
			if exists, nonEmpty := stat(marker); exists && nonEmpty {
				satisfied[dep] = true
				continue
			}
		}
		missing = append(missing, dep)
	}
	sort.Strings(missing)
	return missing
}
