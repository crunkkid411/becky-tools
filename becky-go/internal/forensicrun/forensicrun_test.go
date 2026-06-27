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

// The ladder is CROSS-FAMILY: L1 Gemma-4 E4B (gemma4-local) -> L2 Qwen3.5-4B (qwen35-local, a
// DIFFERENT family) -> L3 Gemma-4 12B (BECKY_AVLM_VARIANT=12b — env, not a flag). Here only the 12B
// watch corroborates, so all three levels run IN ORDER and the 12B concludes Alex.
func TestLadder_CrossFamilyEscalation(t *testing.T) {
	id := `{"identifications":[{"type":"voice","name":"Alex","confidence":0.8}]}`
	type call struct {
		args []string
		env  []string
	}
	var validateCalls []call
	var identifyArgs []string
	runner := func(ctx context.Context, tool string, args, env []string) ([]byte, error) {
		switch tool {
		case "becky-identify":
			identifyArgs = args
			return []byte(id), nil
		case "becky-validate":
			validateCalls = append(validateCalls, call{args: args, env: env})
			// Only the 12B (variant env) corroborates; the E4B and the Qwen watch find
			// nothing, so the ladder runs all three levels in order.
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
	if len(validateCalls) != 3 {
		t.Fatalf("cross-family ladder must try ALL three levels (E4B -> Qwen3.5 -> 12B), got %d validate calls", len(validateCalls))
	}
	// L1: Gemma-4 E4B via gemma4-local, no variant env.
	if backendOf(validateCalls[0].args) != "gemma4-local" || envHas(validateCalls[0].env, "BECKY_AVLM_VARIANT=12b") {
		t.Errorf("L1 must be gemma4-local with no variant env, got backend=%q env=%v", backendOf(validateCalls[0].args), validateCalls[0].env)
	}
	// L2: Qwen3.5-4B via qwen35-local (the DIFFERENT family), no variant env.
	if backendOf(validateCalls[1].args) != "qwen35-local" {
		t.Errorf("L2 must be the cross-family qwen35-local backend, got backend=%q", backendOf(validateCalls[1].args))
	}
	if envHas(validateCalls[1].env, "BECKY_AVLM_VARIANT=12b") {
		t.Errorf("L2 (Qwen) must NOT carry the 12B variant env, got %v", validateCalls[1].env)
	}
	// L3: Gemma-4 12B via gemma4-local + the variant env.
	if backendOf(validateCalls[2].args) != "gemma4-local" || !envHas(validateCalls[2].env, "BECKY_AVLM_VARIANT=12b") {
		t.Errorf("L3 must escalate to gemma4-local with BECKY_AVLM_VARIANT=12b, got backend=%q env=%v", backendOf(validateCalls[2].args), validateCalls[2].env)
	}
	if !hasClaim(rep.Names, "person=Alex") {
		t.Errorf("the 12B watch corroborated Alex, so it must be a stated name; Names=%v", rep.Names)
	}
}

// The headline win the user asked for: cross-family corroboration. When Gemma-4 (L1) finds nothing
// but Qwen3.5 (L2) corroborates, the one-signal name is promoted at L2 — a DIFFERENT family — and the
// ladder STOPS before the 12B. This is real corroboration (two independent families), not Gemma echo.
func TestLadder_QwenCorroboratesAtLevel2(t *testing.T) {
	id := `{"identifications":[{"type":"voice","name":"Alex","confidence":0.8}]}`
	var backends []string
	runner := func(ctx context.Context, tool string, args, env []string) ([]byte, error) {
		switch tool {
		case "becky-identify":
			return []byte(id), nil
		case "becky-validate":
			b := backendOf(args)
			backends = append(backends, b)
			if b == "qwen35-local" {
				return []byte(`{"observations":[{"confidence":0.8}]}`), nil // the cross-family watch agrees
			}
			return []byte(`{"observations":[]}`), nil // Gemma-4 E4B found nothing
		default:
			return nil, errors.New("unused")
		}
	}

	rep := runAndReport(context.Background(), "clip.mp4", "", "kb-final", 1, nil, runner)

	if !hasClaim(rep.Names, "person=Alex") {
		t.Fatalf("Qwen3.5 corroborated Alex (cross-family) — it must be a stated name; Names=%v Held=%v", rep.Names, rep.Held)
	}
	if len(backends) != 2 || backends[0] != "gemma4-local" || backends[1] != "qwen35-local" {
		t.Fatalf("ladder must stop after Qwen corroborates at L2 (no 12B), got backends=%v", backends)
	}
}

// A presence watch corroborates ONLY when the model saw the SUBJECT, not just "something".
func TestPresenceWatch_SubjectAware(t *testing.T) {
	tr := `{"segments":[{"start":10,"end":12,"text":"the cat is here"}]}`
	mo := `{"motion_bursts":[{"window_start":10,"window_end":14}]}`
	base := func(validateJSON string) ToolRunner {
		return func(ctx context.Context, tool string, args, env []string) ([]byte, error) {
			switch tool {
			case "becky-identify":
				return []byte(`{}`), nil
			case "becky-transcribe":
				return []byte(tr), nil
			case "becky-motion":
				return []byte(mo), nil
			case "becky-validate":
				return []byte(validateJSON), nil
			}
			return nil, errors.New("unused")
		}
	}

	sawCat := runAndReport(context.Background(), "clip.mp4", "cat", "kb", 1, nil,
		base(`{"observations":[{"visual":"a cat on the floor","confidence":0.8}]}`))
	if len(sawCat.OnScreen) == 0 {
		t.Fatalf("a watch that SAW the cat must conclude presence; held=%v audit=%v", sawCat.Held, sawCat.Audit)
	}

	sawDog := runAndReport(context.Background(), "clip.mp4", "cat", "kb", 1, nil,
		base(`{"observations":[{"visual":"a dog by the door","confidence":0.95}]}`))
	if len(sawDog.OnScreen) != 0 {
		t.Fatalf("a watch that did NOT see the cat must NOT conclude cat presence, got %v", sawDog.OnScreen)
	}
}

// NewGemmaLadder degrades (errors) on a missing becky-validate binary, so a claim stays held.
func TestNewGemmaLadder_DegradesOnMissingBinary(t *testing.T) {
	ex := NewGemmaLadder("/no/such/file.mp4")
	if _, err := ex.Validate(orchestrate.Claim{Key: "person=X"}, 1); err == nil {
		t.Error("a missing becky-validate must error so the claim stays held, got nil")
	}
}

// ResolveKB prefers an explicit value, then BECKY_KB, then the kb-final convention.
func TestResolveKB(t *testing.T) {
	t.Setenv("BECKY_KB", "")
	if got := ResolveKB("/cases/jordan/kb"); got != "/cases/jordan/kb" {
		t.Errorf("explicit kb must win, got %q", got)
	}
	if got := ResolveKB(""); got != defaultKB {
		t.Errorf("no explicit, no env -> default %q, got %q", defaultKB, got)
	}
	t.Setenv("BECKY_KB", "/env/kb")
	if got := ResolveKB(""); got != "/env/kb" {
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

// backendOf returns the value following "--backend" in a becky-validate argv (or "").
func backendOf(args []string) string {
	for i, a := range args {
		if a == "--backend" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
