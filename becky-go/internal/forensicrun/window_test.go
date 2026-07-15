package forensicrun

import (
	"context"
	"testing"

	"becky-go/internal/orchestrate"
)

func argValue(args []string, flag string) (string, bool) {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag {
			return args[i+1], true
		}
	}
	return "", false
}

// A presence claim's watch must aim becky-validate at THAT claim's moment, so a long
// video is swept window-by-window instead of every watch hitting time 0.
func TestGemmaLadder_AimsPresenceWatchAtClaimWindow(t *testing.T) {
	var got []string
	run := func(_ context.Context, tool string, args, _ []string) ([]byte, error) {
		if tool == "becky-validate" {
			got = args
		}
		return []byte(`{"observations":[]}`), nil
	}
	g := gemmaLadder{file: "clip.mp4", run: run}
	c := orchestrate.Claim{Key: "onscreen=John@[120.0-135.0]", IsPresence: true}
	if _, err := g.Validate(c, 1); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if v, ok := argValue(got, "--window-start"); !ok || v != "120" {
		t.Errorf("--window-start = %q (ok=%v), want 120; args=%v", v, ok, got)
	}
	if w, ok := argValue(got, "--window"); !ok || w != "15" {
		t.Errorf("--window = %q (ok=%v), want 15; args=%v", w, ok, got)
	}
}

// An identity (non-presence) claim is a whole-file re-check: it must NOT aim a window.
func TestGemmaLadder_IdentityWatchIsWholeFile(t *testing.T) {
	var got []string
	run := func(_ context.Context, _ string, args, _ []string) ([]byte, error) {
		got = args
		return []byte(`{"observations":[]}`), nil
	}
	g := gemmaLadder{file: "clip.mp4", run: run}
	if _, err := g.Validate(orchestrate.Claim{Key: "name=John", IsPresence: false}, 1); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if _, ok := argValue(got, "--window-start"); ok {
		t.Errorf("identity watch should not aim a window; args=%v", got)
	}
}

func TestPresenceWindowFromKey(t *testing.T) {
	if start, dur, ok := presenceWindowFromKey("onscreen=John@[120.0-135.0]"); !ok || start != 120 || dur != 15 {
		t.Fatalf("got start=%v dur=%v ok=%v, want 120/15/true", start, dur, ok)
	}
	if _, dur, _ := presenceWindowFromKey("onscreen=X@[0.0-999.0]"); dur != 60 {
		t.Errorf("dur cap = %v, want 60 (model limit)", dur)
	}
	if _, _, ok := presenceWindowFromKey("name=John"); ok {
		t.Error("a name= key has no window span")
	}
}
