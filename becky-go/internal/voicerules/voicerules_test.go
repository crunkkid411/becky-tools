package voicerules

import (
	"testing"

	"becky-go/internal/catalog"
)

// A RED tool is refused proactively; a GREEN one is allowed.
func TestGateAction_TierGate(t *testing.T) {
	r := Default()

	// becky-export is RED in the catalog.
	if d := r.GateAction("becky-export", true); d.Allowed {
		t.Errorf("RED tool must be refused proactively; got %+v", d)
	}
	if d := r.GateAction("becky-export", false); !d.Allowed || !d.NeedConfirm {
		t.Errorf("RED tool asked directly must be allowed WITH confirm; got %+v", d)
	}

	// becky-transcribe is GREEN: allowed proactively, no confirm.
	if d := r.GateAction("becky-transcribe", true); !d.Allowed || d.NeedConfirm {
		t.Errorf("GREEN tool must be allowed proactively without confirm; got %+v", d)
	}

	// unknown tool defaults to RED => refused proactively.
	if d := r.GateAction("becky-unknown", true); d.Allowed {
		t.Errorf("unknown tool defaults RED, must be refused proactively; got %+v", d)
	}
	if got := r.TierFor("becky-unknown"); got != catalog.TierRed {
		t.Errorf("unknown tier = %q, want red", got)
	}
}

// YELLOW tool: confirm-once when asked directly; refused proactively by default.
func TestGateAction_Yellow(t *testing.T) {
	r := Default()
	// becky-cut is YELLOW.
	if d := r.GateAction("becky-cut", false); !d.Allowed || !d.NeedConfirm {
		t.Errorf("YELLOW asked directly must be allowed with confirm-once; got %+v", d)
	}
	if d := r.GateAction("becky-cut", true); d.Allowed {
		t.Errorf("YELLOW must not be proactive by default; got %+v", d)
	}
}

// A tier override re-tiers a tool (e.g. force a GREEN tool to RED).
func TestTierOverride(t *testing.T) {
	r := Default()
	r.TierOverrides = map[string]catalog.Tier{"becky-transcribe": catalog.TierRed}
	if got := r.TierFor("becky-transcribe"); got != catalog.TierRed {
		t.Errorf("override tier = %q, want red", got)
	}
	if d := r.GateAction("becky-transcribe", true); d.Allowed {
		t.Errorf("overridden-to-RED tool must be refused proactively; got %+v", d)
	}
}

// Sensitive context forces local even when cloud is allowed.
func TestRealtimeFor_SensitiveForcesLocal(t *testing.T) {
	r := Default() // CloudAllowed=true
	if got := r.RealtimeFor("editing the case footage"); got != RealtimeLocal {
		t.Errorf("sensitive context must force local; got %q", got)
	}
	if got := r.RealtimeFor("making a house track"); got != RealtimeCloud {
		t.Errorf("non-sensitive context with cloud allowed should be cloud; got %q", got)
	}
	// cloud globally disabled => always local.
	r.CloudAllowed = false
	if got := r.RealtimeFor("making a house track"); got != RealtimeLocal {
		t.Errorf("cloud disabled must force local; got %q", got)
	}
}

// Budget caps proactive proposals per minute.
func TestBudget_CapsProposals(t *testing.T) {
	r := Default()
	r.MaxProposalsPerMinute = 2
	b := r.NewBudget()
	if !b.AllowProposal() || !b.AllowProposal() {
		t.Fatal("first two proposals should fit a cap of 2")
	}
	if b.AllowProposal() {
		t.Error("third proposal must be refused (budget cap enforced)")
	}
	b.Reset()
	if !b.AllowProposal() {
		t.Error("after Reset the budget should allow again")
	}
}

// Quiet hours, including a window that wraps midnight.
func TestInQuietHours(t *testing.T) {
	r := Default()
	r.QuietHours = []QuietWindow{{Start: 22, End: 6}} // wraps midnight
	for _, h := range []int{22, 23, 0, 3, 5} {
		if !r.InQuietHours(h) {
			t.Errorf("hour %d should be quiet", h)
		}
	}
	for _, h := range []int{6, 9, 13, 21} {
		if r.InQuietHours(h) {
			t.Errorf("hour %d should NOT be quiet", h)
		}
	}
}

// THE Highlight-bug regression: a staged screenshot is NOT marked sent without an
// explicit, correct confirm token.
func TestStagedSet_NotSentWithoutConfirm(t *testing.T) {
	s, token := Stage("screenshot", "tab:case-notes")

	if s.Sent {
		t.Fatal("staged set must NOT be auto-sent")
	}
	// no token => not sent.
	if s.Send("") || s.Sent {
		t.Fatal("Send with empty token must not send")
	}
	// wrong token => not sent.
	if s.Send("confirm-deadbeefdeadbeef") || s.Sent {
		t.Fatal("Send with the wrong token must not send")
	}
	// the correct token => sent.
	if !s.Send(token) || !s.Sent {
		t.Fatalf("Send with the correct token should send; token=%s", token)
	}
}

// Stripping a staged item invalidates a previously-issued token (can't send a different
// set with a stale confirm).
func TestStagedSet_StripInvalidatesToken(t *testing.T) {
	s, token := Stage("screenshot", "tab:case-notes")
	s.Strip(0) // remove the screenshot
	if len(s.Items) != 1 || s.Items[0] != "tab:case-notes" {
		t.Fatalf("strip left %v", s.Items)
	}
	if s.Send(token) {
		t.Error("the pre-strip token must not send the now-different set")
	}
	if s.Sent {
		t.Error("set must remain un-sent after a stale-token send attempt")
	}
	if !s.Send(s.ConfirmToken()) || !s.Sent {
		t.Error("the refreshed token should send the stripped set")
	}
}

// Load fills safe defaults for an empty file and validates fields.
func TestLoad_DefaultsAndValidation(t *testing.T) {
	r, err := Load([]byte(``))
	if err != nil {
		t.Fatalf("empty file should load defaults: %v", err)
	}
	if r.MaxProposalsPerMinute != defaultMaxProposalsPerMinute {
		t.Errorf("default cap = %d, want %d", r.MaxProposalsPerMinute, defaultMaxProposalsPerMinute)
	}
	if len(r.AllowProactiveTiers) != 1 || r.AllowProactiveTiers[0] != catalog.TierGreen {
		t.Errorf("default proactive tiers = %v, want [green]", r.AllowProactiveTiers)
	}
	// invalid tier rejected.
	if _, err := Load([]byte(`{"allow_proactive_tiers":["purple"]}`)); err == nil {
		t.Error("an invalid proactive tier must be rejected")
	}
	// out-of-range quiet hour rejected.
	if _, err := Load([]byte(`{"quiet_hours":[{"start":30,"end":2}]}`)); err == nil {
		t.Error("an out-of-range quiet window must be rejected")
	}
	// a real rules file loads.
	r2, err := Load([]byte(`{"cloud_allowed":false,"max_proposals_per_minute":5,"sensitive_contexts":["secret"]}`))
	if err != nil {
		t.Fatalf("valid file: %v", err)
	}
	if r2.CloudAllowed || r2.MaxProposalsPerMinute != 5 || !r2.IsSensitive("a secret plan") {
		t.Errorf("loaded rules wrong: %+v", r2)
	}
}
