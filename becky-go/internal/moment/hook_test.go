package moment

import (
	"strings"
	"testing"
)

// Until now the in-point had exactly ONE signal behind it: the structural pass.
// Asking the content model to QUOTE the line the clip should open on gives a
// second, independent opinion on the same decision — which is what becky's own
// corroborate-then-conclude rule requires everywhere else.

func TestHookIsLate_FlagsAQuoteFromPartWayIn(t *testing.T) {
	text := "So anyway that's the setup. The thing nobody tells you about running a studio is the cashflow. " +
		"You can be fully booked and still not make rent."
	if !hookIsLate(text, "The thing nobody tells you about running a studio is the cashflow") {
		t.Error("a verbatim quote from the second sentence should read as 'this clip opens too early'")
	}
}

func TestHookIsLate_AgreesWhenTheClipAlreadyOpensThere(t *testing.T) {
	text := "The thing nobody tells you about running a studio is the cashflow. I nearly went under twice."
	if hookIsLate(text, "The thing nobody tells you about running a studio is the cashflow") {
		t.Error("the quoted hook IS the opening; that is agreement, not disagreement")
	}
}

// Silence from a model is not evidence. A paraphrase is not a disagreement about
// the in-point — it is the model doing what models do.
func TestHookIsLate_TreatsAParaphraseAsNoSignal(t *testing.T) {
	text := "The thing nobody tells you about running a studio is the cashflow. I nearly went under twice."
	if hookIsLate(text, "he talks about how cashflow is the hidden problem with studios") {
		t.Error("a paraphrase must not downgrade a clip; only a verbatim quote counts")
	}
	if hookIsLate(text, "") {
		t.Error("an empty hook is no signal, not a disagreement")
	}
}

// A two-word "quote" matches by accident. Require enough text to be distinctive.
func TestHookIsLate_IgnoresQuotesTooShortToBeDistinctive(t *testing.T) {
	text := "Right. So the thing is, you have to plan for it."
	if hookIsLate(text, "the thing") {
		t.Error("an 11-character fragment is not evidence of anything")
	}
}

// Punctuation and casing get reformatted by every model. That must not defeat
// the match, but it must not turn into a fuzzy match either.
func TestNormalizeQuote_SurvivesReformattingOnly(t *testing.T) {
	a := normalizeQuote(`  "So, he SAID -- it's over!"  `)
	if a != "so he said it s over" {
		t.Fatalf("normalised to %q", a)
	}
	if strings.Contains(a, ",") || strings.Contains(a, "\"") {
		t.Error("punctuation survived normalisation")
	}
}

// The whole point is that it CHANGES the verdict — a signal that is recorded and
// never acted on is decoration.
func TestRank_HoldsAClipWhoseOpeningTheModelDisputes(t *testing.T) {
	text := "So anyway that's the setup. The thing nobody tells you about running a studio is the cashflow. " +
		"You can be fully booked and still not make rent."
	cands := []Candidate{{Source: "a", Start: 0, End: 30, Text: text, Score: 0.90}}
	js := []Judgement{{
		Index: 0, Score: 89, Complete: true,
		Hook:   "The thing nobody tells you about running a studio is the cashflow",
		Reason: "strong specific claim",
	}}

	got := Rank(cands, js)
	if len(got) != 1 {
		t.Fatalf("got %d ranked", len(got))
	}
	if got[0].Confidence != Disputed {
		t.Errorf("confidence = %q, want %q: structure and content disagree about where this clip starts",
			got[0].Confidence, Disputed)
	}
	if !strings.Contains(got[0].Basis, "OPENING") {
		t.Errorf("the basis does not say WHAT was disputed: %q", got[0].Basis)
	}
	// Held at the lower of the two, never averaged upward.
	if got[0].Final > 0.90 {
		t.Errorf("final = %.3f, want <= the lower signal (0.900)", got[0].Final)
	}
}

// A clip the model agrees with must still reach a conclusion — the new check
// must not quietly demote everything.
func TestRank_StillConcludesWhenTheOpeningAgrees(t *testing.T) {
	text := "The thing nobody tells you about running a studio is the cashflow. I nearly went under twice."
	cands := []Candidate{{Source: "a", Start: 0, End: 30, Text: text, Score: 0.90}}
	js := []Judgement{{
		Index: 0, Score: 89, Complete: true,
		Hook:   "The thing nobody tells you about running a studio is the cashflow",
		Reason: "strong specific claim",
	}}
	got := Rank(cands, js)
	if got[0].Confidence != Conclusion {
		t.Errorf("confidence = %q, want %q", got[0].Confidence, Conclusion)
	}
}

// The veto outranks the opening objection: a clip that does not complete is
// vetoed whatever it opens on.
func TestRank_IncompleteArcStillOutranksTheOpeningObjection(t *testing.T) {
	text := "So anyway that's the setup. The thing nobody tells you about running a studio is the cashflow."
	cands := []Candidate{{Source: "a", Start: 0, End: 30, Text: text, Score: 0.90}}
	js := []Judgement{{
		Index: 0, Score: 89, Complete: false,
		Hook:   "The thing nobody tells you about running a studio is the cashflow",
		Reason: "trails off",
	}}
	if got := Rank(cands, js); got[0].Confidence != Vetoed {
		t.Errorf("confidence = %q, want %q — an incomplete arc is a veto regardless of the opening",
			got[0].Confidence, Vetoed)
	}
}
