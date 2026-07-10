// selftest.go - becky-goal's one-command, OFFLINE proof of the real code path:
// the durable store round-trips through a fresh temp file, the four actions
// behave, invalid input is rejected, a corrupt store is refused WITHOUT being
// clobbered, and nothing is ever deleted. No network, no fixed machine state,
// no side effects outside the temp dir. This is becky's "provable handoff"
// gate (STANDARDS-WORKFLOW.md section 7).
package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func runSelftest() int {
	dir, err := os.MkdirTemp("", "becky-goal-selftest-")
	if err != nil {
		fmt.Println("selftest: could not make temp dir:", err)
		return 1
	}
	defer os.RemoveAll(dir)
	store := filepath.Join(dir, "goals.json")
	opt := func() options { return options{store: store} }

	type check struct {
		name string
		ok   bool
	}
	var checks []check
	add := func(name string, ok bool) { checks = append(checks, check{name, ok}) }

	// add two goals
	r := run("add", []string{"ship the weekly video"}, opt())
	add("add creates goal g1", r.OK && r.Goal != nil && r.Goal.ID == "g1" && r.Goal.Status == StatusTodo)

	o := opt()
	o.due = "2026-07-20"
	o.notes = "waiting on transcript"
	r = run("add", []string{"restore the childcare email"}, o)
	add("add creates goal g2 with due + notes", r.OK && r.Goal != nil && r.Goal.ID == "g2" && r.Goal.Due == "2026-07-20" && r.Goal.Notes == "waiting on transcript")

	// list
	r = run("list", nil, opt())
	add("list returns both goals", r.OK && r.Count == 2 && len(r.Goals) == 2)

	// update-status (positional form)
	r = run("update-status", []string{"g1", "active"}, opt())
	add("update-status g1 -> active", r.OK && r.Goal != nil && r.Goal.Status == StatusActive)

	// filtered list
	r = run("list", []string{"active"}, opt())
	add("list --status active returns exactly g1", r.OK && r.Count == 1 && len(r.Goals) == 1 && r.Goals[0].ID == "g1")

	// note (positional form)
	r = run("note", []string{"g1", "cut the first draft"}, opt())
	add("note appends progress to g1", r.OK && r.Goal != nil && len(r.Goal.Progress) == 1 && r.Goal.Progress[0].Text == "cut the first draft")

	// PERSISTENCE: a brand-new load from disk sees the mutations
	reloaded, lerr := load(store)
	var g1 *Goal
	for i := range reloaded {
		if reloaded[i].ID == "g1" {
			g1 = &reloaded[i]
		}
	}
	add("store persists across a fresh load (status + progress survive)",
		lerr == nil && len(reloaded) == 2 && g1 != nil && g1.Status == StatusActive && len(g1.Progress) == 1)

	// invalid status is rejected
	r = run("update-status", []string{"g1", "banana"}, opt())
	add("update-status REJECTS an invalid status", !r.OK)

	// unknown id is rejected
	r = run("update-status", []string{"g999", "done"}, opt())
	add("update-status REJECTS an unknown id", !r.OK)

	// note on unknown id is rejected
	r = run("note", []string{"g999", "hello"}, opt())
	add("note REJECTS an unknown id", !r.OK)

	// empty outcome is rejected
	r = run("add", []string{"   "}, opt())
	add("add REJECTS an empty outcome", !r.OK)

	// CORRUPT STORE: refused AND left untouched (Law 8b)
	corruptDir, _ := os.MkdirTemp("", "becky-goal-corrupt-")
	defer os.RemoveAll(corruptDir)
	corrupt := filepath.Join(corruptDir, "goals.json")
	badBytes := []byte("{ this is not valid json ]")
	os.WriteFile(corrupt, badBytes, 0o644)
	co := opt()
	co.store = corrupt
	r = run("add", []string{"should not be written"}, co)
	after, _ := os.ReadFile(corrupt)
	add("corrupt store is REFUSED and left byte-for-byte untouched",
		!r.OK && string(after) == string(badBytes))

	// NEVER DELETES: after everything, both original goals are still present
	final, _ := load(store)
	add("no action ever removed a goal (still 2)", len(final) == 2)

	failed := 0
	for _, c := range checks {
		status := "PASS"
		if !c.ok {
			status = "FAIL"
			failed++
		}
		fmt.Printf("[%s] %s\n", status, c.name)
	}
	fmt.Println()
	if failed == 0 {
		fmt.Printf("becky-goal selftest: PASS (%d/%d checks)\n", len(checks), len(checks))
		return 0
	}
	fmt.Printf("becky-goal selftest: FAIL (%d/%d checks failed)\n", failed, len(checks))
	return 1
}
