package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// makeFile is declared in coverage_test.go (same package); reused here.

// --- adaptCommand ---

func TestAdaptCommand_VideoTarget(t *testing.T) {
	dir := t.TempDir()
	f := makeFile(t, dir, "clip.mp4")
	tgt := resolveTarget([]string{f})

	example := `becky-transcribe "<video>" --format srt`
	got := adaptCommand(example, tgt)
	if strings.Contains(got, "<video>") {
		t.Errorf("video placeholder should be replaced; got %q", got)
	}
	if !strings.Contains(got, f) {
		t.Errorf("command should contain the actual path %q; got %q", f, got)
	}
}

func TestAdaptCommand_FolderTarget(t *testing.T) {
	dir := t.TempDir()
	tgt := resolveTarget([]string{dir})

	for _, ph := range []string{`"<folder>"`, `"<corpus-dir>"`, `"<wiki-dir>"`, `"<frames-dir>"`} {
		example := `becky example ` + ph
		got := adaptCommand(example, tgt)
		if strings.Contains(got, ph) {
			t.Errorf("placeholder %s should be replaced; got %q", ph, got)
		}
		if !strings.Contains(got, dir) {
			t.Errorf("adapted command should contain dir path; got %q", got)
		}
	}
}

func TestAdaptCommand_VideoOrFolderPlaceholder(t *testing.T) {
	dir := t.TempDir()
	f := makeFile(t, dir, "test.mp4")
	tgt := resolveTarget([]string{f})

	example := `becky-pipeline "<video-or-folder>"`
	got := adaptCommand(example, tgt)
	if strings.Contains(got, "<video-or-folder>") {
		t.Errorf("video-or-folder placeholder should be replaced; got %q", got)
	}
}

func TestAdaptCommand_NoTarget_KeepsPlaceholders(t *testing.T) {
	example := `becky-transcribe "<video>" --format srt`
	got := adaptCommand(example, Target{})
	if got != example {
		t.Errorf("with no target, command should be unchanged; got %q", got)
	}
}

func TestAdaptCommand_UserValuePlaceholders_Untouched(t *testing.T) {
	// <query>, <claim>, <name>, <url> are user-supplied values and must NOT be filled in.
	dir := t.TempDir()
	tgt := resolveTarget([]string{dir})

	for _, ph := range []string{`"<query>"`, `"<claim>"`, `"<name>"`, `"<url>"`} {
		got := adaptCommand(`becky find `+ph+` --db forensic.db`, tgt)
		if !strings.Contains(got, ph) {
			t.Errorf("user-value placeholder %s must not be replaced; got %q", ph, got)
		}
	}
}

// --- stepPos / stepOrderMap ---

func TestStepPos_KnownVerbs(t *testing.T) {
	// enroll-wiki must run before find (lower position).
	if stepPos("enroll-wiki") >= stepPos("find") {
		t.Errorf("enroll-wiki (%d) should run before find (%d)",
			stepPos("enroll-wiki"), stepPos("find"))
	}
	// transcribe before identify (matches pipeline order).
	if stepPos("becky-transcribe") >= stepPos("becky-identify") {
		t.Errorf("transcribe (%d) should run before identify (%d)",
			stepPos("becky-transcribe"), stepPos("becky-identify"))
	}
	// index before appearances (must have embedded corpus first).
	if stepPos("index") >= stepPos("appearances") {
		t.Errorf("index (%d) should run before appearances (%d)",
			stepPos("index"), stepPos("appearances"))
	}
}

func TestStepPos_UnknownVerbDefaultsLast(t *testing.T) {
	if stepPos("becky-not-a-real-tool") != stepOrderDefault {
		t.Errorf("unknown verb should return stepOrderDefault (%d)", stepOrderDefault)
	}
}

// --- buildWorkflowPlan ---

func TestBuildWorkflowPlan_OrdersInPipelineSequence(t *testing.T) {
	// Request: appearances + index — index (corpus ingest) must come FIRST.
	hits := []capability{
		{Verb: "appearances", Summary: "Find appearances", Example: `becky appearances "Shelby" --kb kb-final --corpus "<folder>"`},
		{Verb: "index", Summary: "Ingest corpus", Example: `becky index "<corpus-dir>" --db forensic.db --kb kb-final`},
	}
	steps := buildWorkflowPlan(hits, Target{})
	if len(steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(steps))
	}
	if steps[0].Verb != "index" {
		t.Errorf("step 1 should be 'index' (comes before appearances); got %q", steps[0].Verb)
	}
	if steps[1].Verb != "appearances" {
		t.Errorf("step 2 should be 'appearances'; got %q", steps[1].Verb)
	}
	if steps[0].Num != 1 || steps[1].Num != 2 {
		t.Errorf("step numbers wrong: %d, %d (want 1, 2)", steps[0].Num, steps[1].Num)
	}
}

func TestBuildWorkflowPlan_TranscribeBeforeIdentify(t *testing.T) {
	hits := []capability{
		{Verb: "becky-identify", Summary: "Identify people", Example: `becky-identify "<video>" --kb kb-final`},
		{Verb: "becky-transcribe", Summary: "Transcribe speech", Example: `becky-transcribe "<video>" --format srt`},
	}
	steps := buildWorkflowPlan(hits, Target{})
	if len(steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(steps))
	}
	if steps[0].Verb != "becky-transcribe" {
		t.Errorf("step 1 should be transcribe; got %q", steps[0].Verb)
	}
}

func TestBuildWorkflowPlan_FillsTargetInCommands(t *testing.T) {
	dir := t.TempDir()
	f := makeFile(t, dir, "evidence.mp4")
	tgt := resolveTarget([]string{f})

	hits := []capability{
		{Verb: "becky-transcribe", Summary: "Transcribe", Example: `becky-transcribe "<video>" --format srt`},
		{Verb: "becky-diarize", Summary: "Diarize", Example: `becky-diarize "<video>"`},
	}
	steps := buildWorkflowPlan(hits, tgt)
	for _, s := range steps {
		if !strings.Contains(s.Command, f) {
			t.Errorf("step %d command should contain the actual path; got %q", s.Num, s.Command)
		}
		if strings.Contains(s.Command, "<video>") {
			t.Errorf("step %d command still has raw placeholder; got %q", s.Num, s.Command)
		}
	}
}

func TestBuildWorkflowPlan_SingleHit(t *testing.T) {
	hits := []capability{
		{Verb: "becky-transcribe", Summary: "Transcribe", Example: `becky-transcribe "<video>"`},
	}
	steps := buildWorkflowPlan(hits, Target{})
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if steps[0].Num != 1 {
		t.Errorf("single step should have Num=1, got %d", steps[0].Num)
	}
}

// --- workflowReply ---

func TestWorkflowReply_NumberedSteps(t *testing.T) {
	hits := []capability{
		{Verb: "appearances", Summary: "Find appearances", Example: `becky appearances "Shelby" --kb kb-final --corpus "<folder>"`},
		{Verb: "find", Summary: "Search corpus", Example: `becky find "affair" --db forensic.db`},
	}
	out := workflowReply(hits, Target{})
	if !strings.Contains(out, "1.") || !strings.Contains(out, "2.") {
		t.Errorf("workflow reply should have numbered steps; got:\n%s", out)
	}
}

func TestWorkflowReply_ContainsBothTools(t *testing.T) {
	hits := []capability{
		{Verb: "becky-transcribe", Summary: "Transcribe", Example: `becky-transcribe "<video>"`},
		{Verb: "becky-identify", Summary: "Identify", Example: `becky-identify "<video>" --kb kb-final`},
	}
	out := workflowReply(hits, Target{})
	if !strings.Contains(out, "becky-transcribe") {
		t.Errorf("workflow reply should contain transcribe command; got:\n%s", out)
	}
	if !strings.Contains(out, "becky-identify") {
		t.Errorf("workflow reply should contain identify command; got:\n%s", out)
	}
}

func TestWorkflowReply_FillsTargetInOutput(t *testing.T) {
	dir := t.TempDir()
	f := makeFile(t, dir, "clip.mp4")
	tgt := resolveTarget([]string{f})

	hits := []capability{
		{Verb: "becky-transcribe", Summary: "Transcribe", Example: `becky-transcribe "<video>"`},
		{Verb: "becky-identify", Summary: "Identify", Example: `becky-identify "<video>" --kb kb-final`},
	}
	out := workflowReply(hits, tgt)
	if !strings.Contains(out, filepath.Base(f)) {
		t.Errorf("workflow reply should contain the target filename; got:\n%s", out)
	}
}

func TestWorkflowReply_ShowsPlaceholderHintWhenNeeded(t *testing.T) {
	// A command with remaining angle-bracket placeholders should trigger the hint.
	hits := []capability{
		{Verb: "appearances", Summary: "Find appearances", Example: `becky appearances "Shelby" --kb kb-final --corpus "<folder>"`},
		{Verb: "find", Summary: "Search corpus", Example: `becky find "<query>" --db forensic.db`},
	}
	out := workflowReply(hits, Target{}) // no target → placeholders remain
	if !strings.Contains(out, "angle brackets") {
		t.Errorf("should note angle-bracket placeholders when any remain; got:\n%s", out)
	}
}

func TestWorkflowReply_NoPlaceholderHintWhenAllFilled(t *testing.T) {
	// When all placeholders are filled, the hint should be absent.
	dir := t.TempDir()
	f := makeFile(t, dir, "clip.mp4")
	tgt := resolveTarget([]string{f})

	hits := []capability{
		{Verb: "becky-transcribe", Summary: "Transcribe", Example: `becky-transcribe "<video>"`},
		{Verb: "becky-diarize", Summary: "Diarize", Example: `becky-diarize "<video>"`},
	}
	out := workflowReply(hits, tgt)
	if strings.Contains(out, "angle brackets") {
		t.Errorf("should NOT show placeholder hint when all paths are filled; got:\n%s", out)
	}
}

func TestWorkflowReply_MentionsTarget(t *testing.T) {
	dir := t.TempDir()
	f := makeFile(t, dir, "evidence.mp4")
	tgt := resolveTarget([]string{f})

	hits := []capability{
		{Verb: "becky-transcribe", Summary: "Transcribe", Example: `becky-transcribe "<video>"`},
		{Verb: "becky-events", Summary: "Events", Example: `becky-events "<video>"`},
	}
	out := workflowReply(hits, tgt)
	// The intro should name the target.
	if !strings.Contains(out, "evidence.mp4") {
		t.Errorf("workflow reply intro should mention the target; got:\n%s", out)
	}
}

// --- route() integration ---

func TestRoute_TwoMatchesShowsWorkflowPlan(t *testing.T) {
	// "transcribe and diarize this" → 2+ catalog hits → workflow plan, not a flat list.
	tgt := videoTarget(t)
	r := route(context.Background(), nil, "how do I transcribe and diarize this video?", tgt)

	if len(r.Pending) != 0 {
		t.Errorf("a workflow reply must stage no Pending command; got %v", r.Pending)
	}
	// Numbered plan has "1." and "2."
	if !strings.Contains(r.Reply, "1.") {
		t.Errorf("workflow reply should be a numbered plan; got:\n%s", r.Reply)
	}
}

func TestRoute_SingleMatchShowsCatalogAnswer(t *testing.T) {
	// A single-tool question keeps the existing capability answer (not a plan).
	r := route(context.Background(), nil, "can becky transcribe a video?", Target{})
	if len(r.Pending) != 0 {
		t.Errorf("capability answer should not stage commands; got %v", r.Pending)
	}
	// Single-match path shows "Closest match(es):" not a numbered plan.
	if strings.Contains(r.Reply, "1.") && strings.Contains(r.Reply, "2.") {
		t.Errorf("single-tool question should not show a numbered plan; got:\n%s", r.Reply)
	}
}

func TestRoute_MultipleMatchesNoTarget_ShowsPlan(t *testing.T) {
	// Even without a target, multi-match questions get a workflow plan.
	r := route(context.Background(), nil, "how do I find where someone appears and search what they said?", Target{})
	if len(r.Pending) != 0 {
		t.Errorf("workflow reply must not auto-stage commands")
	}
	// The reply should contain a numbered plan, not just a bulleted list.
	if !strings.Contains(r.Reply, "step") && !strings.Contains(r.Reply, "1.") {
		t.Errorf("multi-match reply should be a workflow plan; got:\n%s", r.Reply)
	}
}

func TestHasOpenPlaceholder(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{`becky-transcribe "clip.mp4"`, false},
		{`becky-transcribe "<video>"`, true},
		{`becky appearances "Shelby" --corpus "<folder>"`, true},
		{`becky find "affair" --db forensic.db`, false},
	}
	for _, c := range cases {
		if got := hasOpenPlaceholder(c.in); got != c.want {
			t.Errorf("hasOpenPlaceholder(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
