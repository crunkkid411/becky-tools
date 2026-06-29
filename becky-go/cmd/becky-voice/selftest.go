package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
)

// runSelfTest runs 5 scripted assertions over the Router with stub exec.
// No real becky-*.exe, no mic, no model required.
// Returns 0 on PASS, 1 on any FAIL.
func runSelfTest() int {
	out := bufio.NewWriter(&bytes.Buffer{})
	audit := bufio.NewWriter(&bytes.Buffer{})
	r := NewRouter(out, audit)
	r.UseStubExec()
	ctx := context.Background()

	pass := true
	total := 0
	ok := 0

	assert := func(label string, got EventMsg, checks ...func(EventMsg) error) {
		total++
		var errs []error
		for _, fn := range checks {
			if err := fn(got); err != nil {
				errs = append(errs, err)
			}
		}
		if len(errs) == 0 {
			fmt.Printf("PASS %s\n", label)
			ok++
		} else {
			for _, e := range errs {
				fmt.Printf("FAIL %s: %v (got type=%q action=%q tier=%q tool=%q need_confirm=%v)\n",
					label, e, got.Type, got.Action, got.Tier, got.Tool, got.NeedConfirm)
			}
			pass = false
		}
	}

	wantType := func(t string) func(EventMsg) error {
		return func(e EventMsg) error {
			if e.Type != t {
				return fmt.Errorf("type=%q want %q", e.Type, t)
			}
			return nil
		}
	}
	wantAction := func(a string) func(EventMsg) error {
		return func(e EventMsg) error {
			if e.Action != a {
				return fmt.Errorf("action=%q want %q", e.Action, a)
			}
			return nil
		}
	}
	wantTier := func(t string) func(EventMsg) error {
		return func(e EventMsg) error {
			if e.Tier != t {
				return fmt.Errorf("tier=%q want %q", e.Tier, t)
			}
			return nil
		}
	}

	// A1: GREEN tool ("transcribe") → type=result, action=run, tier=green.
	// The catalog has becky-transcribe as TierGreen in pack "default". The router
	// must auto-run it without asking for confirmation.
	e1 := r.Route(ctx, IntentMsg{Type: "intent", Text: "transcribe this video", ID: "t1"})
	assert("A1 green auto-run",
		e1,
		wantType("result"),
		wantAction("run"),
		wantTier("green"),
	)

	// A2: RED tool ("export") in the default pack → type=need_confirm, action=await_confirm.
	// becky-export is TierRed in the catalog. Without a confirm token the router
	// must NOT run it and must set NeedConfirm=true.
	// NOTE: "export my findings" avoided — "find" is a substring of "findings" and would
	// match the TierGreen "find" op first. "export this" unambiguously hits becky-export.
	e2 := r.Route(ctx, IntentMsg{Type: "intent", Text: "export this", ID: "t2"})
	assert("A2 red gates on confirm",
		e2,
		wantType("need_confirm"),
		wantAction("await_confirm"),
		func(e EventMsg) error {
			if !e.NeedConfirm {
				return fmt.Errorf("need_confirm=false, want true")
			}
			return nil
		},
	)

	// A3: confirm the pending RED action → type=result, action=run.
	// A2 left a pending confirm; echoing it must execute the export.
	e3 := r.Route(ctx, IntentMsg{Type: "confirm", ID: "t2"})
	assert("A3 confirm executes pending",
		e3,
		wantType("result"),
		wantAction("run"),
	)

	// A4: "fix it" deploys the fix-verb for the last tool → action=run, Tool non-empty.
	// After A3, lastTool="becky-export". The response map maps any tool to
	// defaultFixVerb="becky-new-tool", so "fix it" must emit Tool="becky-new-tool".
	e4 := r.Route(ctx, IntentMsg{Type: "intent", Text: "fix it", ID: "t4"})
	assert("A4 fix-it deploys fix verb",
		e4,
		wantAction("run"),
		func(e EventMsg) error {
			if e.Tool == "" {
				return fmt.Errorf("Tool empty, want fix-verb name")
			}
			return nil
		},
	)

	// A5: switch to "reaper" pack → becky-transcribe is NOT in that pack → type=error.
	// Pack scoping must prevent offering a tool that is not in the active pack,
	// even if the catalog knows the tool.
	r.Route(ctx, IntentMsg{Type: "set_pack", Pack: "reaper", ID: "t5a"})
	e5 := r.Route(ctx, IntentMsg{Type: "intent", Text: "transcribe this video", ID: "t5b"})
	assert("A5 reaper pack blocks transcribe",
		e5,
		wantType("error"),
	)

	fmt.Printf("\nselftest %d/%d PASS\n", ok, total)
	if !pass {
		return 1
	}
	return 0
}
