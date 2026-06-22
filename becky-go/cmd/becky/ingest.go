// ingest.go — `becky ingest <folder>`: the corpus front door. It runs the whole
// forensic pipeline over a folder of clips with ONE command, then rolls every
// clip's corroborated report up into ONE skimmable DIGEST.md (+ digest.json).
//
// It invents no analysis: it shells becky-pipeline (via runTool, like the other
// ops) and FORMATS the existing per-clip report.json / osint-manifest.json
// through internal/digest. The corroboration math is internal/report's, never
// re-coded here. --no-pipeline rebuilds the digest from existing sidecars with
// zero models/ffmpeg — the offline, cloud-runnable path.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"becky-go/internal/digest"
	"becky-go/internal/report"
)

// runIngest is the `becky ingest` op. See the SPEC-BECKY-INGEST.md §4 flag table.
func runIngest(args []string) error {
	cf, rest := extractCommon(args)
	kb, rest := flagValue(rest, "kb", "")
	out, rest := flagValue(rest, "out", "pipeline-out")
	steps, rest := flagValue(rest, "steps", "")
	digestPath, rest := flagValue(rest, "digest", "")
	resume := hasFlag(rest, "resume")
	rest = dropFlag(rest, "resume")
	force := hasFlag(rest, "force")
	rest = dropFlag(rest, "force")
	noPipeline := hasFlag(rest, "no-pipeline")
	rest = dropFlag(rest, "no-pipeline")

	folder := firstPositional(rest)
	if folder == "" {
		return fmt.Errorf("usage: becky ingest <folder> [--kb <dir>] [--out <dir>] [--steps <set>] [--no-pipeline] [--digest <path>]")
	}

	// --no-pipeline only reads <out>/; the source folder need not exist there.
	if !noPipeline {
		if !dirExists(folder) {
			return fmt.Errorf("folder not found (or not a directory): %s", folder)
		}
	}

	// Build the forwarded step set, force-appending `report` so each clip has a
	// report.json (the digest's preferred input). --kb implies the identify step.
	forwardSteps := ingestSteps(steps, kb)

	// (1) Run the pipeline unless --no-pipeline.
	if !noPipeline {
		pargs := []string{folder, "--out", out, "--steps", forwardSteps}
		if kb != "" {
			pargs = append(pargs, "--kb", kb)
		}
		if cf.bin != "" {
			pargs = append(pargs, "--bin", cf.bin)
		}
		if resume {
			pargs = append(pargs, "--resume")
		}
		if force {
			pargs = append(pargs, "--force")
		}
		if cf.verbose {
			pargs = append(pargs, "--verbose")
		}
		headline(cf, "Running the forensic pipeline over %s ...", folder)
		if _, err := runTool(cf, "pipeline", pargs); err != nil {
			// becky-pipeline exits 0 even with partial clips; a real failure here
			// is fatal (no manifest to digest).
			return err
		}
	}

	// (2) Read the pipeline manifest — the authoritative clip list.
	clips, manifestNotes := loadIngestClips(out)

	// (3+4) Build + write the digest.
	info := digest.CorpusInfo{
		Folder:  absOr(folder),
		OutRoot: absOr(out),
		KB:      kb,
		Steps:   strings.Split(forwardSteps, ","),
		Notes:   manifestNotes,
	}
	d := digest.Build(clips, info, nil)

	md := digest.Markdown(d)
	if digestPath == "" {
		digestPath = filepath.Join(out, "DIGEST.md")
	}
	if err := os.MkdirAll(filepath.Dir(digestPath), 0o755); err != nil {
		return fmt.Errorf("create digest dir: %w", err)
	}
	if err := os.WriteFile(digestPath, []byte(md), 0o644); err != nil {
		return fmt.Errorf("write DIGEST.md: %w", err)
	}
	jsonPath := filepath.Join(out, "digest.json")
	jb, err := digest.JSON(d)
	if err != nil {
		return fmt.Errorf("encode digest.json: %w", err)
	}
	if err := os.WriteFile(jsonPath, jb, 0o644); err != nil {
		return fmt.Errorf("write digest.json: %w", err)
	}

	headline(cf, "Digest: %d clips, %d ok, %d partial -> %s",
		d.Corpus.ClipsTotal, d.Corpus.ClipsOK, d.Corpus.ClipsPartial, absOr(digestPath))
	os.Stdout.Write(jb)
	return nil
}

// ingestSteps returns the comma-separated step set forwarded to becky-pipeline:
// the user's set (or the ingest default), plus `identify` when --kb is given,
// plus `report` always. De-duplicated; the pipeline canonically re-orders it.
func ingestSteps(userSteps, kb string) string {
	base := strings.TrimSpace(userSteps)
	if base == "" {
		// The ingest default: the pipeline's deterministic sweep.
		base = "transcribe,metadata,diarize,events,osint,ocr"
	}
	set := []string{}
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		set = append(set, s)
	}
	for _, s := range strings.Split(base, ",") {
		add(s)
	}
	if kb != "" {
		add("identify")
	}
	add("report")
	return strings.Join(set, ",")
}

// loadIngestClips reads <out>/manifest.json and assembles a digest.ClipInput per
// clip, loading each clip's report.json (or rebuilding via report.Build from
// whatever sidecars exist) plus its osint-manifest.json capture block. When the
// manifest is absent it falls back to scanning <out>/*/ subdirs so a stray
// sidecar dir still digests. Degrade-never-crash: errors become per-clip notes,
// never panics.
func loadIngestClips(out string) ([]digest.ClipInput, []string) {
	var notes []string

	manPath := filepath.Join(out, "manifest.json")
	data, err := os.ReadFile(manPath)
	if err != nil {
		notes = append(notes, fmt.Sprintf("no manifest.json at %s; scanned %s/*/ for sidecars instead", manPath, out))
		return scanForClips(out), notes
	}

	var man pipelineManifest
	if jerr := json.Unmarshal(data, &man); jerr != nil {
		notes = append(notes, fmt.Sprintf("manifest.json unreadable (%v); scanned %s/*/ instead", jerr, out))
		return scanForClips(out), notes
	}

	clips := make([]digest.ClipInput, 0, len(man.Videos))
	for _, v := range man.Videos {
		dir := v.OutDir
		if dir == "" {
			dir = filepath.Join(out, v.Stem)
		}
		ci := digest.ClipInput{
			Stem:       v.Stem,
			Input:      v.Input,
			Status:     v.Status,
			SidecarDir: dir,
			Notes:      stepNotes(v),
		}
		ci.Report, ci.HasReport = loadOrBuildReport(dir, v.Stem)
		ci.Capture = loadCapture(dir)
		clips = append(clips, ci)
	}
	return clips, notes
}

// scanForClips is the manifest-less fallback: every <out>/<sub>/ directory that
// holds at least one known sidecar becomes a clip.
func scanForClips(out string) []digest.ClipInput {
	entries, err := os.ReadDir(out)
	if err != nil {
		return []digest.ClipInput{}
	}
	var clips []digest.ClipInput
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(out, e.Name())
		if !hasAnySidecar(dir) {
			continue
		}
		ci := digest.ClipInput{
			Stem:       e.Name(),
			Status:     "unknown",
			SidecarDir: dir,
		}
		ci.Report, ci.HasReport = loadOrBuildReport(dir, e.Name())
		ci.Capture = loadCapture(dir)
		clips = append(clips, ci)
	}
	return clips
}

// loadOrBuildReport loads <dir>/report.json if present; otherwise rebuilds the
// report in-process from whatever sidecars exist (report.Build over
// report.LoadSidecars). The bool reports whether any report data was available.
func loadOrBuildReport(dir, stem string) (report.Report, bool) {
	if b, err := os.ReadFile(filepath.Join(dir, "report.json")); err == nil {
		var r report.Report
		if json.Unmarshal(b, &r) == nil {
			return r, true
		}
	}

	// Fallback: build from the four sidecars report understands, passing only the
	// ones that exist on disk so report.Build's own degrade path handles the rest.
	transcript := existing(dir, "transcript.json")
	events := existing(dir, "events.json")
	identify := existing(dir, "identify.json")
	motion := existing(dir, "motion.json")
	if transcript == "" && events == "" && identify == "" && motion == "" {
		return report.Report{Source: stem, Degraded: true}, false
	}
	s, _, err := report.LoadSidecars(transcript, events, identify, motion)
	if err != nil {
		return report.Report{Source: stem, Degraded: true}, false
	}
	return report.Build(s, stem), true
}

// loadCapture reads <dir>/osint-manifest.json and returns its `metadata` block as
// a digest.CaptureMeta. Missing/unreadable → an empty CaptureMeta (the digest
// then renders "unknown - no capture tag").
func loadCapture(dir string) digest.CaptureMeta {
	b, err := os.ReadFile(filepath.Join(dir, "osint-manifest.json"))
	if err != nil {
		return digest.CaptureMeta{}
	}
	var wrap struct {
		Metadata *digest.CaptureMeta `json:"metadata"`
	}
	if json.Unmarshal(b, &wrap) != nil || wrap.Metadata == nil {
		return digest.CaptureMeta{}
	}
	return *wrap.Metadata
}

// existing returns the joined path if the file exists and is non-empty, else "".
func existing(dir, name string) string {
	p := filepath.Join(dir, name)
	if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Size() > 0 {
		return p
	}
	return ""
}

// hasAnySidecar reports whether dir holds at least one known sidecar file.
func hasAnySidecar(dir string) bool {
	for _, name := range []string{"report.json", "transcript.json", "events.json", "identify.json", "motion.json", "osint-manifest.json"} {
		if existing(dir, name) != "" {
			return true
		}
	}
	return false
}

// stepNotes turns failed/skipped pipeline steps into per-clip digest notes.
func stepNotes(v videoResult) []string {
	var notes []string
	for _, s := range v.Steps {
		switch s.Status {
		case "failed":
			notes = append(notes, fmt.Sprintf("step %s failed", s.Name))
		case "skipped":
			if s.SkipReason != "" && !strings.HasPrefix(s.SkipReason, "already-done") {
				notes = append(notes, fmt.Sprintf("step %s skipped (%s)", s.Name, s.SkipReason))
			}
		}
	}
	return notes
}

// --- minimal pipeline-manifest shapes (read-only; mirrors cmd/pipeline/manifest.go) ---

type pipelineManifest struct {
	Videos []videoResult `json:"videos"`
}

type videoResult struct {
	Input  string           `json:"input"`
	Stem   string           `json:"stem"`
	OutDir string           `json:"out_dir"`
	Status string           `json:"status"`
	Steps  []stepResultView `json:"steps"`
}

type stepResultView struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	SkipReason string `json:"skip_reason,omitempty"`
}
