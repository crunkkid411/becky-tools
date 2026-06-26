package forensicrun

import (
	"context"
	"errors"
	"strings"
	"testing"

	"becky-go/internal/orchestrate"
)

// fakeExec is a stand-in validate ladder for the PURE Report tests: it returns one configured
// corroborating signal (forced to KindWatched for a presence claim, so rule 3 is satisfiable).
type fakeExec struct {
	sig   orchestrate.Signal
	err   error
	calls int
}

func (f *fakeExec) Validate(c orchestrate.Claim, level int) (orchestrate.Signal, error) {
	f.calls++
	if f.err != nil {
		return orchestrate.Signal{}, f.err
	}
	s := f.sig
	if c.IsPresence {
		s.Kind = orchestrate.KindWatched
	}
	return s, nil
}

func hasClaim(vs []orchestrate.Verdict, key string) bool {
	for _, v := range vs {
		if v.Claim == key {
			return true
		}
	}
	return false
}

func TestPlan_OneSpeaker_SkipsDiarize(t *testing.T) {
	got := Plan(1)
	if contains(got, "becky-diarize") {
		t.Errorf("one-speaker plan must SKIP diarize, got %v", got)
	}
	if contains(got, "verify-with-gemma4") {
		t.Errorf("one-speaker plan must skip the gemma4 verify, got %v", got)
	}
	if !contains(got, "becky-transcribe") {
		t.Errorf("plan must always transcribe, got %v", got)
	}
}

func TestPlan_MultiSpeaker_IncludesDiarize(t *testing.T) {
	got := Plan(3)
	if !contains(got, "becky-diarize") {
		t.Errorf("multi-speaker plan MUST diarize, got %v", got)
	}
	if !contains(got, "verify-with-gemma4") {
		t.Errorf("multi-speaker plan must include the gemma4 verify, got %v", got)
	}
}

// A corroborated identification (voice+face agree) concludes the name with NO model call.
func TestReport_CorroboratedName_Concluded(t *testing.T) {
	id := `{"identifications":[{"type":"corroborated","name":"Shelby","confidence":0.9,"corroborated_by":["voice","face"]}]}`
	ex := &fakeExec{}
	rep := Report("clip.mp4", "", 1, Inputs{Identify: []byte(id)}, ex, 2)

	if !hasClaim(rep.Names, "person=Shelby") {
		t.Fatalf("corroborated Shelby must be a stated name, got Names=%v Held=%v", rep.Names, rep.Held)
	}
	if rep.Names[0].Status != orchestrate.Concluded {
		t.Errorf("status = %q, want concluded", rep.Names[0].Status)
	}
	if ex.calls != 0 {
		t.Errorf("an already-corroborated name must NOT call the model ladder, calls=%d", ex.calls)
	}
}

// A single-modality match is held as a candidate when the ladder cannot corroborate it.
func TestReport_SingleSignalName_HeldWhenLadderFails(t *testing.T) {
	id := `{"identifications":[{"type":"voice","name":"Alex","confidence":0.8}]}`
	ex := &fakeExec{err: errors.New("model unavailable")}
	rep := Report("clip.mp4", "", 1, Inputs{Identify: []byte(id)}, ex, 2)

	if hasClaim(rep.Names, "person=Alex") {
		t.Fatalf("a single weak signal must NOT be stated as a name; Names=%v", rep.Names)
	}
	if !hasClaim(rep.Held, "person=Alex") {
		t.Fatalf("Alex must be HELD as a candidate, got Held=%v", rep.Held)
	}
}

// The forced ladder promotes a one-signal candidate to a stated name when the model corroborates.
func TestReport_LadderPromotesCandidate(t *testing.T) {
	id := `{"identifications":[{"type":"voice","name":"Alex","confidence":0.8}]}`
	ex := &fakeExec{sig: orchestrate.Signal{Source: "gemma4-e4b", Kind: orchestrate.KindPrint, Confidence: 0.8}}
	rep := Report("clip.mp4", "", 1, Inputs{Identify: []byte(id)}, ex, 2)

	if !hasClaim(rep.Names, "person=Alex") {
		t.Fatalf("ladder corroboration must promote Alex to a stated name; Names=%v Held=%v", rep.Names, rep.Held)
	}
	if ex.calls == 0 {
		t.Errorf("a candidate must invoke the ladder")
	}
}

// Presence rule (rule 3): a mention + a motion burst, with NO watch, can never conclude presence.
func TestReport_Presence_NeedsWatch(t *testing.T) {
	tr := `{"segments":[{"start":10,"end":12,"text":"the cat jumped on the table"}]}`
	mo := `{"motion_bursts":[{"window_start":10,"window_end":14}]}`
	in := Inputs{Transcribe: []byte(tr), Motion: []byte(mo)}

	// No executor: nothing can watch -> the window is a held candidate, never stated.
	noWatch := Report("clip.mp4", "cat", 1, in, nil, 0)
	if len(noWatch.OnScreen) != 0 {
		t.Fatalf("a mention+motion with no watch must NOT be stated on-screen, got %v", noWatch.OnScreen)
	}
	if len(noWatch.Held) == 0 {
		t.Fatalf("the unwatched window must be HELD as a candidate")
	}

	// With a watching ladder: the model watch promotes the window to a stated on-screen interval.
	ex := &fakeExec{sig: orchestrate.Signal{Source: "gemma4-e4b", Confidence: 0.8}}
	watched := Report("clip.mp4", "cat", 1, in, ex, 2)
	if len(watched.OnScreen) == 0 {
		t.Fatalf("a watched window must be stated on-screen; Held=%v audit=%v", watched.Held, watched.Audit)
	}
	if watched.OnScreen[0].Status != orchestrate.Concluded {
		t.Errorf("watched on-screen status = %q, want concluded", watched.OnScreen[0].Status)
	}
}

// RunAndReport degrades, never crashes, when the sibling tools are absent.
func TestRunAndReport_DegradesWhenToolsMissing(t *testing.T) {
	failRunner := func(ctx context.Context, tool string, args, env []string) ([]byte, error) {
		return nil, errors.New("not installed")
	}
	rep := runAndReport(context.Background(), "clip.mp4", "cat", "kb-final", 2, nil, failRunner)

	if len(rep.Names) != 0 || len(rep.OnScreen) != 0 {
		t.Fatalf("missing tools must yield no stated facts, got Names=%v OnScreen=%v", rep.Names, rep.OnScreen)
	}
	if len(rep.Degraded) == 0 {
		t.Fatalf("missing tools must be recorded in Degraded")
	}
	if !containsSubstr(rep.Degraded, "becky-identify") {
		t.Errorf("a missing becky-identify must be reported, got %v", rep.Degraded)
	}
	// Multi-speaker plan still computed deterministically even with no tools.
	if !contains(rep.Plan, "becky-diarize") {
		t.Errorf("plan must still reflect speakers=2 (diarize), got %v", rep.Plan)
	}
}

// The escalation env is the REAL mechanism (the becky-resolve --variant bug fixed): level 2
// selects the 12B model via BECKY_AVLM_VARIANT=12b, not a flag.
func TestLadder_EscalatesViaVariantEnv(t *testing.T) {
	id := `{"identifications":[{"type":"voice","name":"Alex","confidence":0.8}]}`
	var validateEnvs [][]string
	var identifyArgs []string
	runner := func(ctx context.Context, tool string, args, env []string) ([]byte, error) {
		switch tool {
		case "becky-identify":
			identifyArgs = args
			return []byte(id), nil
		case "becky-validate":
			validateEnvs = append(validateEnvs, env)
			// L1 (no env) does NOT corroborate; L2 (12b env) does.
			if envHas(env, "BECKY_AVLM_VARIANT=12b") {
				return []byte(`{"observations":[{"confidence":0.8}]}`), nil
			}
			return []byte(`{"observations":[]}`), nil
		default:
			return nil, errors.New("unused")
		}
	}

	rep := runAndReport(context.Background(), "clip.mp4", "", "kb-final", 1, nil, runner)

	// becky-identify MUST be called with the knowledge base (it is required, else naming degrades).
	if !envHas(identifyArgs, "--kb") || !envHas(identifyArgs, "kb-final") {
		t.Errorf("becky-identify must be called with --kb kb-final, got args %v", identifyArgs)
	}
	if len(validateEnvs) != 2 {
		t.Fatalf("ladder must try BOTH levels (E4B then 12B), got %d validate calls", len(validateEnvs))
	}
	if envHas(validateEnvs[0], "BECKY_AVLM_VARIANT=12b") {
		t.Errorf("level 1 must run the default E4B (no variant env), got %v", validateEnvs[0])
	}
	if !envHas(validateEnvs[1], "BECKY_AVLM_VARIANT=12b") {
		t.Errorf("level 2 must escalate via BECKY_AVLM_VARIANT=12b, got %v", validateEnvs[1])
	}
	if !hasClaim(rep.Names, "person=Alex") {
		t.Errorf("the 12B watch corroborated Alex, so it must be a stated name; Names=%v", rep.Names)
	}
}

// resolveKB prefers an explicit value, then BECKY_KB, then the kb-final convention.
func TestResolveKB(t *testing.T) {
	t.Setenv("BECKY_KB", "")
	if got := resolveKB("/cases/jordan/kb"); got != "/cases/jordan/kb" {
		t.Errorf("explicit kb must win, got %q", got)
	}
	if got := resolveKB(""); got != defaultKB {
		t.Errorf("no explicit, no env -> default %q, got %q", defaultKB, got)
	}
	t.Setenv("BECKY_KB", "/env/kb")
	if got := resolveKB(""); got != "/env/kb" {
		t.Errorf("BECKY_KB must be used when no explicit, got %q", got)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func containsSubstr(ss []string, want string) bool {
	for _, s := range ss {
		if strings.Contains(s, want) {
			return true
		}
	}
	return false
}

func envHas(env []string, want string) bool {
	for _, e := range env {
		if e == want {
			return true
		}
	}
	return false
}
