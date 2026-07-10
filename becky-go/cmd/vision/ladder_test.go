package main

import (
	"strings"
	"testing"

	"becky-go/internal/vision"
)

// --- promptImpliesTextOrUI ---------------------------------------------------

func TestPromptImpliesTextOrUI(t *testing.T) {
	cases := map[string]bool{
		"Describe this image concisely and factually.":           false,
		"What color is the cat?":                                 false,
		"What state is the terminal application in this photo?":  true,
		"Is anything on this screen stuck or waiting for input?": true,
		"What does the dialog box say?":                          true,
		"Read the error message on screen.":                      true,
		"Describe the sunset over the mountains.":                false,
	}
	for prompt, want := range cases {
		if got := promptImpliesTextOrUI(prompt); got != want {
			t.Errorf("promptImpliesTextOrUI(%q) = %v, want %v", prompt, got, want)
		}
	}
}

// --- looksUncertain -----------------------------------------------------------

func TestLooksUncertain(t *testing.T) {
	cases := map[string]bool{
		"A person in a green wig, smiling at the camera.":                   false,
		"The terminal is stuck waiting for a permission prompt.":            false,
		"I'm not sure, but it might be a cat.":                              true,
		"It is unclear whether the process is running.":                     true,
		"There are no visible icons that would suggest it is stuck.":        true,
		"The screen appears to show a completed task, though hard to tell.": true,
		// Regression (fixed 2026-07-10, slice B): "appears to" alone is
		// ordinary scene-setting VLM prose, not a hedge about the actual
		// conclusion — it must NOT trigger looksUncertain on its own (it did
		// above only because "hard to tell" is also present). This is the
		// exact false-positive from becky-AI-Agent-review-1.md's regression
		// image that wasted one escalation to Gemma-4 12B.
		"The image appears to show a laptop screen displaying a terminal window with a permission prompt.": false,
		"The terminal appears to be stuck on a Yes/No confirmation dialog.":                                false,
	}
	for text, want := range cases {
		if got := looksUncertain(text); got != want {
			t.Errorf("looksUncertain(%q) = %v, want %v", text, got, want)
		}
	}
}

// --- disagree ------------------------------------------------------------------

func TestDisagree(t *testing.T) {
	cases := []struct {
		name         string
		a, b         string
		wantDisagree bool
	}{
		{
			name:         "opposite: stuck vs ready",
			a:            "The terminal is stuck waiting for a permission prompt.",
			b:            "The terminal is ready and idle, nothing stuck.",
			wantDisagree: true,
		},
		{
			name:         "agree: both say stuck",
			a:            "The terminal appears frozen, waiting for input.",
			b:            "The application is stuck on a permission prompt.",
			wantDisagree: false,
		},
		{
			name:         "neither mentions stuck/ready axis",
			a:            "A cat sits on a windowsill.",
			b:            "A dog runs across a field.",
			wantDisagree: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := disagree(c.a, c.b); got != c.wantDisagree {
				t.Errorf("disagree(%q, %q) = %v, want %v", c.a, c.b, got, c.wantDisagree)
			}
		})
	}
}

// --- computeConfidence ---------------------------------------------------------

func TestComputeConfidence_higherRungHigherBaseline(t *testing.T) {
	confident := "The cat is orange."
	last := -1.0
	for rung := 0; rung < 4; rung++ {
		c := computeConfidence(rung, confident, false)
		if c <= last {
			t.Errorf("rung %d confidence %.2f should exceed previous rung's %.2f", rung, c, last)
		}
		last = c
	}
}

func TestComputeConfidence_hedgingLowersScore(t *testing.T) {
	confident := computeConfidence(0, "The cat is orange.", false)
	hedging := computeConfidence(0, "It might be an orange cat, hard to tell.", false)
	if hedging >= confident {
		t.Errorf("hedging confidence %.2f should be lower than confident %.2f", hedging, confident)
	}
}

func TestComputeConfidence_clampedToRange(t *testing.T) {
	c := computeConfidence(0, "not sure, unclear, hard to tell, ambiguous", true)
	if c < 0.05 || c > 0.99 {
		t.Errorf("confidence %.2f out of [0.05, 0.99] range", c)
	}
}

// --- runEscalationPolicy: the decision flow, no real models ------------------

// fakeRungs builds a []rungCall from canned outcomes so the ESCALATION POLICY
// is verified without spawning a single model. calls[i] increments every time
// rung i actually runs, so tests can assert a rung was (or was not) reached.
func fakeRungs(outcomes []rungResult, calls []int) []rungCall {
	rungs := make([]rungCall, len(outcomes))
	for i := range outcomes {
		i := i
		rungs[i] = rungCall{
			label:  labelFor(i),
			engine: "fake",
			run: func() rungResult {
				calls[i]++
				return outcomes[i]
			},
		}
	}
	return rungs
}

func labelFor(i int) string {
	names := []string{"rung0", "rung1", "rung2", "rung3", "rung4"}
	if i < len(names) {
		return names[i]
	}
	return "rungN"
}

func TestEscalationPolicy_confidentRung0StopsImmediately(t *testing.T) {
	calls := make([]int, 4)
	rungs := fakeRungs([]rungResult{
		{text: "A cat sits on a windowsill.", ok: true},
		{text: "should never run", ok: true},
		{text: "should never run", ok: true},
		{text: "should never run", ok: true},
	}, calls)

	res := runEscalationPolicy("cat.jpg", "Describe this image.", rungs, ocrSignal{}, false)

	if res.Degraded {
		t.Fatalf("expected a usable result, got degraded: %s", res.Error)
	}
	if res.Escalations != 0 {
		t.Errorf("escalations = %d, want 0 (rung 0 was confident)", res.Escalations)
	}
	if len(res.Sources) != 1 {
		t.Errorf("sources = %d, want 1 (only rung 0 should have run)", len(res.Sources))
	}
	if calls[1] != 0 || calls[2] != 0 || calls[3] != 0 {
		t.Errorf("later rungs must not run when rung 0 is confident: calls=%v", calls)
	}
}

func TestEscalationPolicy_textPromptForcesEscalationEvenWhenConfident(t *testing.T) {
	calls := make([]int, 4)
	rungs := fakeRungs([]rungResult{
		// The review's exact incident shape: a CONFIDENT-sounding, non-hedging,
		// WRONG answer to a prompt about on-screen text/UI state.
		{text: "The terminal is in the Finish state, ready to accept input.", ok: true},
		{text: "The terminal is stuck on a permission prompt.", ok: true},
		// rung 2 (a bigger model) resolves the rung0/rung1 disagreement by
		// confirming the "stuck" read clearly — the ladder should stop here.
		{text: "The screen clearly shows a terminal stuck on a permission prompt.", ok: true},
		{text: "should never run (rung2 already agreed with rung1)", ok: true},
	}, calls)

	res := runEscalationPolicy("term.jpg", "Is anything on this screen stuck or waiting for input?", rungs, ocrSignal{}, false)

	if calls[0] != 1 || calls[1] != 1 {
		t.Fatalf("expected rungs 0 and 1 to run, got calls=%v", calls)
	}
	// Rung 1's answer disagrees with rung 0's (stuck vs ready) AND doesn't
	// hedge, so the ladder should climb once more looking for agreement.
	if calls[2] != 1 {
		t.Errorf("expected rung 2 to run after rung0/rung1 disagreed, calls=%v", calls)
	}
	// Rung 2 agrees with rung 1 (both say "stuck") and doesn't hedge, so the
	// ladder should stop there — rung 3 must never run.
	if calls[3] != 0 {
		t.Errorf("rung 2 resolved the disagreement; rung 3 must not run, calls=%v", calls)
	}
	if res.Escalations < 1 {
		t.Errorf("escalations = %d, want >= 1 (prompt implied UI/text state)", res.Escalations)
	}
	if res.Model != "rung2" {
		t.Errorf("final model = %q, want rung2 (the rung that resolved the disagreement)", res.Model)
	}
}

func TestEscalationPolicy_degradedRungFallsThroughToNext(t *testing.T) {
	calls := make([]int, 4)
	rungs := fakeRungs([]rungResult{
		{ok: false}, // model missing
		{text: "A confident, plain answer.", ok: true},
		{text: "should never run", ok: true},
		{text: "should never run", ok: true},
	}, calls)

	res := runEscalationPolicy("x.jpg", "Describe this image.", rungs, ocrSignal{}, false)

	if res.Degraded {
		t.Fatalf("rung 1 succeeded, result must not be degraded: %s", res.Error)
	}
	if res.Model != "rung1" {
		t.Errorf("model = %q, want rung1 (the first rung that actually answered)", res.Model)
	}
	if calls[2] != 0 {
		t.Errorf("rung 1 was confident; rung 2 should not have run, calls=%v", calls)
	}
}

func TestEscalationPolicy_allRungsFailIsHonestlyDegraded(t *testing.T) {
	calls := make([]int, 4)
	rungs := fakeRungs([]rungResult{
		{ok: false}, {ok: false}, {ok: false}, {ok: false},
	}, calls)

	res := runEscalationPolicy("x.jpg", "Describe this image.", rungs, ocrSignal{}, false)

	if !res.Degraded {
		t.Fatalf("every rung failed, result must be degraded")
	}
	if res.Error == "" {
		t.Errorf("degraded result must carry a plain-language error")
	}
	if len(res.Sources) != 4 {
		t.Errorf("sources should record all 4 attempted rungs, got %d", len(res.Sources))
	}
	for i, s := range res.Sources {
		if s.OK {
			t.Errorf("source[%d] should be marked not-ok (that rung failed)", i)
		}
	}
}

func TestEscalationPolicy_budgetCapNeverExceedsMaxEscalations(t *testing.T) {
	calls := make([]int, 5)
	// 5 rungs available, all uncertain forever — the ladder must stop at
	// MaxEscalations (3) escalations = 4 total rungs, never reaching rung 4.
	outcomes := make([]rungResult, 5)
	for i := range outcomes {
		outcomes[i] = rungResult{text: "not sure, hard to tell", ok: true}
	}
	rungs := fakeRungs(outcomes, calls)

	res := runEscalationPolicy("x.jpg", "Describe this image.", rungs, ocrSignal{}, false)

	if res.Escalations > MaxEscalations {
		t.Errorf("escalations = %d, must not exceed MaxEscalations (%d)", res.Escalations, MaxEscalations)
	}
	if calls[4] != 0 {
		t.Errorf("rung 4 (beyond the 4-rung budget) must never run, calls=%v", calls)
	}
}

// TestEscalationPolicy_smallTiersAgreeingWronglyStillReachesGemma pins down a
// real finding from testing against the review's actual regression image
// (IMG_7725.JPEG, 2026-07-09): the 450M AND the 1.6B both confidently agreed
// on the SAME wrong "nothing stuck" read, so disagree() never fired between
// them — but Gemma-4 E4B (rung 2) read the on-screen text correctly on the
// first try. Two small tiers agreeing with each other is not evidence they're
// right; a text/UI-implying prompt must always reach rung 2 regardless.
func TestEscalationPolicy_smallTiersAgreeingWronglyStillReachesGemma(t *testing.T) {
	calls := make([]int, 4)
	rungs := fakeRungs([]rungResult{
		{text: "The terminal shows AI Finish running. Nothing is stuck or waiting.", ok: true},
		{text: "The terminal is in the AI Finish state. Nothing stuck or waiting here.", ok: true},
		{text: "The terminal is waiting for user input at a Yes/No prompt.", ok: true},
		{text: "should never run (rung2 was confident and non-hedging)", ok: true},
	}, calls)

	res := runEscalationPolicy("term.jpg", "Is anything on this screen stuck or waiting for input?", rungs, ocrSignal{}, false)

	if calls[0] != 1 || calls[1] != 1 || calls[2] != 1 {
		t.Fatalf("expected rungs 0, 1, and 2 to all run despite rung0/rung1 agreeing, calls=%v", calls)
	}
	if calls[3] != 0 {
		t.Errorf("rung 2 was confident; rung 3 must not run, calls=%v", calls)
	}
	if res.Model != "rung2" {
		t.Errorf("final model = %q, want rung2 (the tier that actually got it right)", res.Model)
	}
}

// TestEscalationPolicy_sourcesUseShortLabelsNotPaths guards against the
// review's F3 complaint ("model field leaks the machinery" — a raw GGUF path)
// regressing: every Source.Model must be a short label, never contain a path
// separator or a .gguf extension.
func TestEscalationPolicy_sourcesUseShortLabelsNotPaths(t *testing.T) {
	calls := make([]int, 1)
	rungs := fakeRungs([]rungResult{{text: "fine.", ok: true}}, calls)
	res := runEscalationPolicy("x.jpg", "Describe this image.", rungs, ocrSignal{}, false)
	for _, s := range res.Sources {
		if containsAny(s.Model, []string{"\\", "/", ".gguf"}) {
			t.Errorf("Source.Model %q leaks a filesystem path", s.Model)
		}
	}
	if containsAny(res.Model, []string{"\\", "/", ".gguf"}) {
		t.Errorf("Result.Model %q leaks a filesystem path", res.Model)
	}
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && len(s) >= len(sub) {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}

// sanity: vision.Source / vision.Result fields used above compile with the
// shape ladder.go expects (kind/model/ok, confidence/escalations/sources).
var _ = vision.Source{Kind: "vlm", Model: "x", OK: true}

// --- OCR corroboration (slice B) ---------------------------------------------
// ocrSignal is injected directly (never spawns PaddleOCR), mirroring how
// fakeRungs keeps the decision logic testable without real models.

func TestOCRLooksLikePrompt(t *testing.T) {
	cases := map[string]bool{
		"":                                     false,
		"A cat sits on a windowsill.":           false,
		"Do you want to proceed?":               true,
		"1. Yes":                                false, // needs a companion prompt line to read as a dialog
		"Esc to cancel - Tab to amend":           true,
		"Are you sure you want to delete this?": true,
		"MENU - File Edit View":                 false,
	}
	for text, want := range cases {
		if got := ocrLooksLikePrompt([]string{text}); got != want {
			t.Errorf("ocrLooksLikePrompt(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestOCRSignal_disagreesWithReady_liveProptOverridesConfidentReadyAnswer(t *testing.T) {
	ocr := ocrSignal{ran: true, ok: true, keyLines: []string{"Use skill \"claude-in-chrome\"?", "Do you want to proceed?", "1. Yes"}}
	text := "The terminal is in the Finish state, ready to accept input."
	if !ocr.disagreesWithReady(text) {
		t.Errorf("OCR found a live prompt while text claims ready; disagreesWithReady should be true")
	}
	if ocr.agrees(text) {
		t.Errorf("agrees should be false when disagreesWithReady is true")
	}
}

func TestOCRSignal_agrees_whenBothIndicateStuck(t *testing.T) {
	ocr := ocrSignal{ran: true, ok: true, keyLines: []string{"Do you want to proceed?", "1. Yes", "2. No"}}
	text := "The terminal is stuck on a permission prompt, waiting for input."
	if ocr.disagreesWithReady(text) {
		t.Errorf("both signals point to stuck; disagreesWithReady should be false")
	}
	if !ocr.agrees(text) {
		t.Errorf("agrees should be true when OCR and text both indicate a live prompt")
	}
}

func TestOCRSignal_neverRan_neverOverridesAnything(t *testing.T) {
	var ocr ocrSignal // zero value: ran=false
	if ocr.disagreesWithReady("The terminal is ready, nothing stuck.") {
		t.Errorf("an OCR signal that never ran must never force disagreement")
	}
	if !ocr.agrees("anything") {
		t.Errorf("an OCR signal that never ran must default to agrees=true (no evidence against)")
	}
}

func TestOCRSignal_ranButFailed_neverOverrides(t *testing.T) {
	ocr := ocrSignal{ran: true, ok: false}
	if ocr.disagreesWithReady("The terminal is ready, nothing stuck.") {
		t.Errorf("a failed OCR attempt must never force disagreement")
	}
}

func TestHighConfidenceLines_filtersSortsAndCaps(t *testing.T) {
	lines := []vision.OCRLine{
		{Text: "low conf", Confidence: 0.2},
		{Text: "  ", Confidence: 0.9}, // blank after trim -> dropped
		{Text: "second", Confidence: 0.8},
		{Text: "first", Confidence: 0.99},
		{Text: "third", Confidence: 0.6},
	}
	got := highConfidenceLines(lines, 0.5, 2)
	want := []string{"first", "second"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("highConfidenceLines() = %v, want %v", got, want)
	}
}

// Regression (found on the real IMG_7725 e2e run, 2026-07-10): a screenshot's
// UI-chrome words OCR at a full 1.00 confidence and, under pure confidence
// ordering, crowd the actual 0.97 "Do you want to proceed?" prompt line out
// of the capped key-lines list — starving the agreement check of the exact
// evidence it exists to carry. Prompt-shaped lines must sort first.
func TestHighConfidenceLines_promptLinesBeatHigherConfidenceChrome(t *testing.T) {
	lines := []vision.OCRLine{
		{Text: "CONSOLE", Confidence: 1.0},
		{Text: "expand", Confidence: 1.0},
		{Text: "popout", Confidence: 1.0},
		{Text: "Do you want to proceed?", Confidence: 0.97},
		{Text: "Esc to cancel - Tab to amend", Confidence: 0.97},
	}
	got := highConfidenceLines(lines, 0.5, 3)
	if len(got) != 3 || got[0] != "Do you want to proceed?" || got[1] != "Esc to cancel - Tab to amend" {
		t.Errorf("prompt-shaped lines must sort before higher-confidence chrome words, got %v", got)
	}
	if !ocrLooksLikePrompt(got) {
		t.Errorf("the capped key lines must still look like a prompt, got %v", got)
	}
}

func TestAppendOCRText_incorporatesKeyLinesIntoDescription(t *testing.T) {
	got := appendOCRText("The terminal is stuck.", []string{"Do you want to proceed?", "1. Yes"})
	if !strings.Contains(got, "Do you want to proceed?") {
		t.Errorf("appendOCRText result missing a key line: %q", got)
	}
	if !strings.HasPrefix(got, "The terminal is stuck.") {
		t.Errorf("appendOCRText should preserve the model's own words: %q", got)
	}
}

// TestEscalationPolicy_ocrDisagreementForcesEscalation pins down the review's
// mandatory-corroboration requirement (§4 #5): even when two small tiers
// AGREE with each other (no looksUncertain, no disagree() between them), a
// live on-screen prompt OCR actually read must still force the ladder past
// them — without OCR injected, this exact rung0/rung1 pair (non-hedging,
// prompt not text-implying) would stop at rung 0 immediately.
func TestEscalationPolicy_ocrDisagreementForcesEscalation(t *testing.T) {
	calls := make([]int, 3)
	rungs := fakeRungs([]rungResult{
		{text: "The terminal is idle and ready for the next command.", ok: true},
		{text: "The terminal is idle and ready for the next command.", ok: true},
		{text: "The terminal is waiting on a Yes/No permission prompt.", ok: true},
	}, calls)
	ocr := ocrSignal{ran: true, ok: true, keyLines: []string{"Do you want to proceed?", "1. Yes", "2. No"}}

	res := runEscalationPolicy("term.jpg", "Describe this photo.", rungs, ocr, false)

	if calls[2] != 1 {
		t.Fatalf("OCR found a live prompt while rung0/1 said ready; must escalate to rung2, calls=%v", calls)
	}
	if res.Model != "rung2" {
		t.Errorf("final model = %q, want rung2 (the rung OCR corroboration forced)", res.Model)
	}
}

// TestEscalationPolicy_noOCRSignal_matchesPreSliceBBehavior guards the
// contrast case for the test above: the SAME rungs/prompt WITHOUT an OCR
// signal must still stop at rung 0 (the pre-slice-B behavior) — proving the
// escalation above is caused by OCR, not a change to the base policy.
func TestEscalationPolicy_noOCRSignal_matchesPreSliceBBehavior(t *testing.T) {
	calls := make([]int, 3)
	rungs := fakeRungs([]rungResult{
		{text: "The terminal is idle and ready for the next command.", ok: true},
		{text: "should never run", ok: true},
		{text: "should never run", ok: true},
	}, calls)

	res := runEscalationPolicy("term.jpg", "Describe this photo.", rungs, ocrSignal{}, false)

	if calls[1] != 0 || calls[2] != 0 {
		t.Errorf("without an OCR signal, a confident non-hedging rung 0 must stop immediately, calls=%v", calls)
	}
	if res.Validated {
		t.Errorf("a single unconfirmed rung must never be Validated")
	}
}

// TestEscalationPolicy_ocrSourceInEnvelope checks the full envelope shape the
// review's acceptance criterion 3/5 asks for: a "kind":"ocr" Source carrying
// Agrees + KeyLines, the OCR text folded into Description, and Validated set.
func TestEscalationPolicy_ocrSourceInEnvelope(t *testing.T) {
	calls := make([]int, 1)
	rungs := fakeRungs([]rungResult{
		{text: "The terminal is stuck on a permission prompt, waiting for input.", ok: true},
	}, calls)
	ocr := ocrSignal{ran: true, ok: true, keyLines: []string{"Do you want to proceed?", "1. Yes", "2. No", "Esc to cancel"}}

	res := runEscalationPolicy("term.jpg", "Is the terminal stuck?", rungs, ocr, false)

	var ocrSrc *vision.Source
	for i := range res.Sources {
		if res.Sources[i].Kind == "ocr" {
			ocrSrc = &res.Sources[i]
		}
	}
	if ocrSrc == nil {
		t.Fatalf("expected a kind=ocr source in the envelope, got %+v", res.Sources)
	}
	if !ocrSrc.OK {
		t.Errorf("ocr source should be marked ok")
	}
	if !ocrSrc.Agrees {
		t.Errorf("ocr source should agree with a matching 'stuck' answer")
	}
	if len(ocrSrc.KeyLines) == 0 {
		t.Errorf("ocr source should carry the key lines that were fed in")
	}
	if !strings.Contains(res.Description, "proceed") {
		t.Errorf("final description should incorporate the OCR key lines, got: %s", res.Description)
	}
	if !res.Validated {
		t.Errorf("an OCR-corroborated answer should be marked Validated")
	}
	// The OCR source must never be counted as an "escalation" (a rung) —
	// only 1 VLM rung ran here, so Escalations must stay 0.
	if res.Escalations != 0 {
		t.Errorf("Escalations = %d, want 0 (OCR corroboration is not a rung)", res.Escalations)
	}
}

// TestEscalationPolicy_ocrFailedStillRecordsHonestSource covers the degrade
// path: OCR was attempted (prompt implied text) but the engine failed — the
// call must still succeed (VLM-only), and the envelope must say so honestly
// rather than silently omitting the attempt.
func TestEscalationPolicy_ocrFailedStillRecordsHonestSource(t *testing.T) {
	calls := make([]int, 1)
	rungs := fakeRungs([]rungResult{
		{text: "A quiet desktop, nothing happening.", ok: true},
	}, calls)
	ocr := ocrSignal{ran: true, ok: false}

	res := runEscalationPolicy("x.jpg", "What does the screen say?", rungs, ocr, false)

	if res.Degraded {
		t.Fatalf("a failed OCR corroboration must not degrade the whole call")
	}
	found := false
	for _, s := range res.Sources {
		if s.Kind == "ocr" {
			found = true
			if s.OK {
				t.Errorf("failed OCR must be recorded as not-ok")
			}
		}
	}
	if !found {
		t.Errorf("an attempted-but-failed OCR must still appear in sources (honest envelope, review's F3)")
	}
}
