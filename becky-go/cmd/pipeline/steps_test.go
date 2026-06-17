package main

import (
	"reflect"
	"testing"
)

// fakeStat builds a statFn over a set of "existing & non-empty" marker paths.
func fakeStat(existing map[string]bool) statFn {
	return func(path string) (bool, bool) {
		ok := existing[path]
		return ok, ok
	}
}

func TestParseStepsDefault(t *testing.T) {
	got, err := parseSteps("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// ocr is in the default sweep, right after osint (it degrades gracefully when
	// the OCR model/deps are missing, so it's safe to default on).
	want := []string{stepTranscribe, stepMetadata, stepDiarize, stepEvents, stepOSINT, stepOCR}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("default steps = %v, want %v", got, want)
	}
}

// TestParseStepsOCRAfterOSINT pins the canonical ordering: ocr always follows osint
// regardless of the order the user lists them, because ocr consumes osint's manifest.
func TestParseStepsOCRAfterOSINT(t *testing.T) {
	got, err := parseSteps("ocr,osint")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{stepOSINT, stepOCR}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v (ocr must follow osint)", got, want)
	}
}

func TestParseStepsReordersAndDedups(t *testing.T) {
	// Out-of-order, duplicated, mixed case -> canonical order, unique.
	got, err := parseSteps("OSINT, diarize, diarize, transcribe, events")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{stepTranscribe, stepDiarize, stepEvents, stepOSINT}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParseStepsUnknown(t *testing.T) {
	if _, err := parseSteps("transcribe,bogus"); err == nil {
		t.Fatal("expected error for unknown step, got nil")
	}
}

func TestPlanStepsFreshRunAllRun(t *testing.T) {
	steps := []string{stepTranscribe, stepDiarize, stepEvents, stepOSINT}
	p := newStepPaths("out/test", "")
	plan := planSteps(steps, p, false, fakeStat(nil))

	if len(plan) != 4 {
		t.Fatalf("expected 4 planned steps, got %d", len(plan))
	}
	for _, ps := range plan {
		if !ps.WillRun {
			t.Errorf("step %s should run on a fresh run; skip reason=%q", ps.Name, ps.SkipReason)
		}
	}
}

func TestPlanStepsResumeSkipsCompleted(t *testing.T) {
	steps := []string{stepTranscribe, stepDiarize, stepEvents, stepOSINT}
	p := newStepPaths("out/test", "")
	// transcribe + diarize already produced output; events + osint not yet.
	existing := map[string]bool{
		p.transcript: true,
		p.diarized:   true,
	}
	plan := planSteps(steps, p, false, fakeStat(existing))

	byName := indexPlan(plan)
	if byName[stepTranscribe].WillRun {
		t.Error("transcribe should be skipped (already-done)")
	}
	if byName[stepTranscribe].SkipReason != "already-done" {
		t.Errorf("transcribe skip reason = %q, want already-done", byName[stepTranscribe].SkipReason)
	}
	if byName[stepDiarize].WillRun {
		t.Error("diarize should be skipped (already-done)")
	}
	if !byName[stepEvents].WillRun {
		t.Error("events should run (output missing)")
	}
	if !byName[stepOSINT].WillRun {
		t.Error("osint should run (output missing)")
	}
}

func TestPlanStepsForceRerunsEverything(t *testing.T) {
	steps := []string{stepTranscribe, stepDiarize, stepEvents, stepOSINT}
	p := newStepPaths("out/test", "")
	existing := map[string]bool{
		p.transcript:    true,
		p.diarized:      true,
		p.events:        true,
		p.osintManifest: true,
	}
	plan := planSteps(steps, p, true, fakeStat(existing))
	for _, ps := range plan {
		if !ps.WillRun {
			t.Errorf("force should re-run %s, but it was skipped (%s)", ps.Name, ps.SkipReason)
		}
	}
}

func TestPlanStepsMissingDependencySkipsDependent(t *testing.T) {
	// Request only osint, with no events output on disk and events not requested.
	// osint depends on events -> it must be skipped with a missing-dependency
	// reason rather than launched against a nonexistent events.json.
	steps := []string{stepOSINT}
	p := newStepPaths("out/test", "")
	plan := planSteps(steps, p, false, fakeStat(nil))

	if len(plan) != 1 {
		t.Fatalf("expected 1 planned step, got %d", len(plan))
	}
	if plan[0].WillRun {
		t.Error("osint should be skipped when events output is missing")
	}
	if plan[0].SkipReason != "missing-dependency: events" {
		t.Errorf("skip reason = %q, want missing-dependency: events", plan[0].SkipReason)
	}
}

func TestPlanStepsDependencySatisfiedOnDisk(t *testing.T) {
	// Request only osint, but events.json already exists from a prior run.
	steps := []string{stepOSINT}
	p := newStepPaths("out/test", "")
	plan := planSteps(steps, p, false, fakeStat(map[string]bool{p.events: true}))

	if !plan[0].WillRun {
		t.Errorf("osint should run when events output exists on disk; skip=%q", plan[0].SkipReason)
	}
}

func TestPlanStepsDependencySatisfiedByEarlierScheduledStep(t *testing.T) {
	// Full chain requested, nothing on disk. diarize runs -> events runs ->
	// osint runs: each dependency is satisfied by an earlier scheduled step.
	steps := []string{stepDiarize, stepEvents, stepOSINT}
	p := newStepPaths("out/test", "")
	plan := planSteps(steps, p, false, fakeStat(nil))

	byName := indexPlan(plan)
	for _, s := range steps {
		if !byName[s].WillRun {
			t.Errorf("%s should run in a full fresh chain; skip=%q", s, byName[s].SkipReason)
		}
	}
}

func TestPlanStepsCascadesWhenChainBrokenUpstream(t *testing.T) {
	// events + osint requested, but diarize neither requested nor on disk.
	// events can't run (missing diarize) -> osint can't run (missing events).
	// Both cascade to a graceful skip instead of launching against missing input.
	steps := []string{stepEvents, stepOSINT}
	p := newStepPaths("out/test", "")
	plan := planSteps(steps, p, false, fakeStat(nil))

	byName := indexPlan(plan)
	if byName[stepEvents].WillRun {
		t.Error("events should skip when diarize output is missing")
	}
	if byName[stepEvents].SkipReason != "missing-dependency: diarize" {
		t.Errorf("events skip reason = %q, want missing-dependency: diarize", byName[stepEvents].SkipReason)
	}
	if byName[stepOSINT].WillRun {
		t.Error("osint should cascade-skip when events is skipped")
	}
}

func TestNewStepPathsDBOverride(t *testing.T) {
	p := newStepPaths("out/test", "X:/custom/forensic.db")
	if p.embedDB != "X:/custom/forensic.db" {
		t.Errorf("embedDB = %q, want the override", p.embedDB)
	}
	def := newStepPaths("out/test", "")
	if def.embedDB == "" {
		t.Error("default embedDB should not be empty")
	}
}

func TestOutputMarkers(t *testing.T) {
	p := newStepPaths("out/test", "")
	cases := map[string]string{
		stepTranscribe: p.transcript,
		stepMetadata:   p.metadata,
		stepDiarize:    p.diarized,
		stepEvents:     p.events,
		stepOSINT:      p.osintManifest,
		stepOCR:        p.ocrJSON,
		stepEmbed:      p.embedJSON,
		stepIdentify:   p.identify,
		stepMotion:     p.motion,
		stepReport:     p.reportJSON,
	}
	for step, want := range cases {
		if got := outputMarker(step, p); got != want {
			t.Errorf("outputMarker(%s) = %q, want %q", step, got, want)
		}
	}
}

// TestPlanStepsOCRDependsOnOSINT proves ocr is gracefully skipped when osint has not
// run and is not scheduled (ocr reads the osint manifest), and runs when osint is
// scheduled earlier in the same chain.
func TestPlanStepsOCRDependsOnOSINT(t *testing.T) {
	p := newStepPaths("out/test", "")

	// ocr alone, no osint on disk, osint not requested -> skip with missing-dependency.
	plan := planSteps([]string{stepOCR}, p, false, fakeStat(nil))
	if len(plan) != 1 || plan[0].WillRun {
		t.Fatalf("ocr should skip without osint; plan=%+v", plan)
	}
	if plan[0].SkipReason != "missing-dependency: osint" {
		t.Errorf("skip reason = %q, want missing-dependency: osint", plan[0].SkipReason)
	}

	// Full fresh chain incl. ocr: diarize -> events -> osint -> ocr all run, each
	// dependency satisfied by an earlier scheduled step.
	plan2 := planSteps([]string{stepDiarize, stepEvents, stepOSINT, stepOCR}, p, false, fakeStat(nil))
	byName := indexPlan(plan2)
	for _, s := range []string{stepDiarize, stepEvents, stepOSINT, stepOCR} {
		if !byName[s].WillRun {
			t.Errorf("%s should run in a full fresh chain; skip=%q", s, byName[s].SkipReason)
		}
	}

	// ocr alone but osint-manifest.json already on disk from a prior run -> ocr runs.
	plan3 := planSteps([]string{stepOCR}, p, false, fakeStat(map[string]bool{p.osintManifest: true}))
	if !plan3[0].WillRun {
		t.Errorf("ocr should run when osint manifest exists on disk; skip=%q", plan3[0].SkipReason)
	}
}

// indexPlan maps planned steps by name for assertions.
func indexPlan(plan []plannedStep) map[string]plannedStep {
	m := make(map[string]plannedStep, len(plan))
	for _, ps := range plan {
		m[ps.Name] = ps
	}
	return m
}

func TestParseStepsMotionAndReport(t *testing.T) {
	got, err := parseSteps("motion,report")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// motion before report in canonical order.
	if len(got) != 2 || got[0] != stepMotion || got[1] != stepReport {
		t.Fatalf("got %v, want [motion report]", got)
	}
}

func TestParseStepsReportBeforeMotionInInputGetsReordered(t *testing.T) {
	// User passes report,motion (wrong order) — canonical order should fix it.
	got, err := parseSteps("report,motion")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0] != stepMotion || got[1] != stepReport {
		t.Fatalf("got %v, want [motion report]", got)
	}
}

func TestCanonicalOrderEndsWithMotionThenReport(t *testing.T) {
	n := len(canonicalOrder)
	if n < 2 {
		t.Fatal("canonicalOrder has fewer than 2 entries")
	}
	if canonicalOrder[n-2] != stepMotion {
		t.Errorf("second-to-last step = %q, want %q", canonicalOrder[n-2], stepMotion)
	}
	if canonicalOrder[n-1] != stepReport {
		t.Errorf("last step = %q, want %q", canonicalOrder[n-1], stepReport)
	}
}

func TestPlanStepsMotionHasNoDeps(t *testing.T) {
	// motion can run with nothing else on disk — it reads the video directly.
	steps := []string{stepMotion}
	p := newStepPaths("out/test", "")
	plan := planSteps(steps, p, false, fakeStat(nil))

	if len(plan) != 1 {
		t.Fatalf("expected 1 planned step, got %d", len(plan))
	}
	if !plan[0].WillRun {
		t.Errorf("motion should always run (no deps); skip=%q", plan[0].SkipReason)
	}
}

func TestPlanStepsReportHasNoDeps(t *testing.T) {
	// report can run with nothing else on disk — it degrades gracefully when sidecars
	// are absent (Degraded=true in its output). No hard planner dependency.
	steps := []string{stepReport}
	p := newStepPaths("out/test", "")
	plan := planSteps(steps, p, false, fakeStat(nil))

	if len(plan) != 1 {
		t.Fatalf("expected 1 planned step, got %d", len(plan))
	}
	if !plan[0].WillRun {
		t.Errorf("report should always run (no hard deps); skip=%q", plan[0].SkipReason)
	}
}

func TestPlanStepsFullChainWithMotionAndReport(t *testing.T) {
	// The complete end-to-end chain, nothing on disk — everything should run.
	steps := []string{
		stepTranscribe, stepDiarize, stepEvents, stepOSINT, stepOCR,
		stepIdentify, stepMotion, stepReport,
	}
	p := newStepPaths("out/test", "")
	plan := planSteps(steps, p, false, fakeStat(nil))

	byName := indexPlan(plan)
	for _, s := range steps {
		if s == stepIdentify {
			continue // identify gracefully skips without --kb; not a planner concern
		}
		if !byName[s].WillRun {
			t.Errorf("%s should run in a full fresh chain; skip=%q", s, byName[s].SkipReason)
		}
	}
}

func TestNewStepPathsMotionAndReportPaths(t *testing.T) {
	p := newStepPaths("out/test", "")
	if p.motion == "" {
		t.Error("motion path should not be empty")
	}
	if p.reportJSON == "" {
		t.Error("reportJSON path should not be empty")
	}
	if p.reportMD == "" {
		t.Error("reportMD path should not be empty")
	}
}
