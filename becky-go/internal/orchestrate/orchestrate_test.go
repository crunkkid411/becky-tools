package orchestrate

import "testing"

// --- Corroborate: the gate that makes the protocol impossible to ignore ---

// TestNamingNeedsCorroboration: recall is for DETECTION, not NAMING. One print signal must
// NOT conclude a name; two independent signals must.
func TestNamingNeedsCorroboration(t *testing.T) {
	r := DefaultRules()

	one := Claim{Key: "speaker=Shelby", Signals: []Signal{
		{Source: "becky-identify", Kind: KindPrint, Confidence: 0.9},
	}}
	if v := Corroborate(one, r); v.Status != Candidate {
		t.Errorf("one signal => %s, want candidate (a name needs corroboration). reason=%q", v.Status, v.Reason)
	}

	two := Claim{Key: "speaker=Shelby", Signals: []Signal{
		{Source: "becky-identify", Kind: KindPrint, Confidence: 0.9}, // voice print
		{Source: "becky-faceid", Kind: KindPrint, Confidence: 0.8},   // face print, different source
	}}
	if v := Corroborate(two, r); v.Status != Concluded {
		t.Errorf("two independent signals => %s, want concluded. reason=%q", v.Status, v.Reason)
	}
}

// TestSameSourceTwiceIsNotCorroboration: two signals from the SAME source are one source.
func TestSameSourceTwiceIsNotCorroboration(t *testing.T) {
	c := Claim{Key: "speaker=Shelby", Signals: []Signal{
		{Source: "becky-identify", Kind: KindPrint, Confidence: 0.9},
		{Source: "becky-identify", Kind: KindPrint, Confidence: 0.95},
	}}
	if v := Corroborate(c, DefaultRules()); v.Status == Concluded {
		t.Errorf("one source twice must NOT conclude, got concluded (%v)", v.Sources)
	}
}

// TestLowConfidenceSignalDoesNotCount: a signal below the floor is ignored entirely.
func TestLowConfidenceSignalDoesNotCount(t *testing.T) {
	c := Claim{Key: "speaker=Shelby", Signals: []Signal{
		{Source: "a", Kind: KindPrint, Confidence: 0.9},
		{Source: "b", Kind: KindPrint, Confidence: 0.2}, // below 0.5 floor
	}}
	v := Corroborate(c, DefaultRules())
	if v.Status != Candidate {
		t.Errorf("a sub-floor 2nd signal must not corroborate => want candidate, got %s", v.Status)
	}
	if len(v.Sources) != 1 || v.Sources[0] != "a" {
		t.Errorf("only the qualifying source should count, got %v", v.Sources)
	}
}

// TestPresenceRequiresAWatch: a mention + a motion burst NEVER prove on-screen presence,
// no matter how many; only a model that WATCHED it can.
func TestPresenceRequiresAWatch(t *testing.T) {
	r := DefaultRules()

	noWatch := Claim{Key: "onscreen=cat@[10-14]", IsPresence: true, Signals: []Signal{
		{Source: "becky-transcribe", Kind: KindMention, Confidence: 0.99},
		{Source: "becky-motion", Kind: KindMotion, Confidence: 0.99},
	}}
	v := Corroborate(noWatch, r)
	if v.Status == Concluded {
		t.Errorf("presence with NO watch must never conclude, got concluded. reason=%q", v.Reason)
	}

	watched := Claim{Key: "onscreen=cat@[10-14]", IsPresence: true, Signals: []Signal{
		{Source: "becky-validate", Kind: KindWatched, Confidence: 0.9}, // a model watched it
		{Source: "becky-motion", Kind: KindMotion, Confidence: 0.8},    // + a second source
	}}
	if v := Corroborate(watched, r); v.Status != Concluded {
		t.Errorf("watched + a 2nd source => want concluded, got %s. reason=%q", v.Status, v.Reason)
	}
}

// --- ResolveClaim: the confidence ladder is FORCED ---

type fakeExec struct {
	calls  []int  // levels Validate was called at, in order
	source string // source name for the signal it returns
	kind   SignalKind
	conf   float64
}

func (f *fakeExec) Validate(c Claim, level int) (Signal, error) {
	f.calls = append(f.calls, level)
	return Signal{Source: f.source, Kind: f.kind, Confidence: f.conf}, nil
}

// TestLadderValidatesThenConcludes: a one-signal claim escalates to Gemma-4 (level 1), which
// adds a second independent source, and THEN it concludes — and Validate was actually called.
func TestLadderValidatesThenConcludes(t *testing.T) {
	c := Claim{Key: "speaker=Shelby", Signals: []Signal{
		{Source: "becky-identify", Kind: KindPrint, Confidence: 0.9},
	}}
	ex := &fakeExec{source: "gemma4-e4b", kind: KindPrint, conf: 0.85}
	v, audit := ResolveClaim(c, DefaultRules(), ex, 2)

	if v.Status != Concluded {
		t.Errorf("after validation => want concluded, got %s (audit: %v)", v.Status, audit)
	}
	if len(ex.calls) != 1 || ex.calls[0] != 1 {
		t.Errorf("expected exactly one level-1 validation, got calls=%v", ex.calls)
	}
}

// TestLadderEscalatesTo12B: if level-1 validation still leaves it short, it escalates to
// level 2 (12B). Here each model signal is from the same source, so it never corroborates and
// the ladder must run BOTH levels.
func TestLadderEscalatesTo12B(t *testing.T) {
	c := Claim{Key: "speaker=Shelby", Signals: []Signal{
		{Source: "becky-identify", Kind: KindPrint, Confidence: 0.9},
	}}
	ex := &fakeExec{source: "becky-identify", kind: KindPrint, conf: 0.9} // same source: no new corroboration
	v, _ := ResolveClaim(c, DefaultRules(), ex, 2)

	if want := []int{1, 2}; len(ex.calls) != 2 || ex.calls[0] != want[0] || ex.calls[1] != want[1] {
		t.Errorf("expected escalation through levels 1 then 2, got %v", ex.calls)
	}
	if v.Status == Concluded {
		t.Errorf("same-source validations must not conclude, got concluded")
	}
}

// TestLadderWatchesForPresence: a presence claim with no watch escalates to a model that
// WATCHES it, which then lets it conclude — the ladder produces the missing watch.
func TestLadderWatchesForPresence(t *testing.T) {
	c := Claim{Key: "onscreen=cat@[10-14]", IsPresence: true, Signals: []Signal{
		{Source: "becky-motion", Kind: KindMotion, Confidence: 0.8}, // a candidate moment only
	}}
	ex := &fakeExec{source: "gemma4-12b", kind: KindWatched, conf: 0.9}
	v, audit := ResolveClaim(c, DefaultRules(), ex, 2)
	if v.Status != Concluded {
		t.Errorf("a watch from the model + the motion source => want concluded, got %s (audit %v)", v.Status, audit)
	}
}

// TestResolvePartitions: Resolve keeps concluded facts separate from candidates/unknowns —
// no flood of maybes in the stated output.
func TestResolvePartitions(t *testing.T) {
	claims := []Claim{
		{Key: "concluded-one", Signals: []Signal{
			{Source: "a", Kind: KindPrint, Confidence: 0.9},
			{Source: "b", Kind: KindPrint, Confidence: 0.9},
		}},
		{Key: "candidate-one", Signals: []Signal{
			{Source: "a", Kind: KindPrint, Confidence: 0.9},
		}},
		{Key: "unknown-one", Signals: []Signal{
			{Source: "a", Kind: KindPrint, Confidence: 0.1},
		}},
	}
	res := Resolve(claims, DefaultRules(), nil, 0) // nil exec: no ladder, pure corroboration
	if len(res.Concluded) != 1 || res.Concluded[0].Claim != "concluded-one" {
		t.Errorf("concluded = %+v, want exactly concluded-one", res.Concluded)
	}
	if len(res.Candidates) != 1 || res.Candidates[0].Claim != "candidate-one" {
		t.Errorf("candidates = %+v, want exactly candidate-one", res.Candidates)
	}
	if len(res.Unknown) != 1 {
		t.Errorf("unknown = %+v, want exactly one", res.Unknown)
	}
}
