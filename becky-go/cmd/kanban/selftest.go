// selftest.go - becky-kanban's one-command, OFFLINE proof of the real code path:
// the board round-trips through a fresh temp file, the four actions behave,
// selection by index AND by text match works, invalid input is rejected, a
// corrupt store is refused WITHOUT being clobbered, unknown card fields survive,
// and nothing is ever deleted. No network, no fixed machine state, no side
// effects outside the temp dir. This is becky's "provable handoff" gate
// (STANDARDS-WORKFLOW.md section 7).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func runSelftest() int {
	dir, err := os.MkdirTemp("", "becky-kanban-selftest-")
	if err != nil {
		fmt.Println("selftest: could not make temp dir:", err)
		return 1
	}
	defer os.RemoveAll(dir)
	store := filepath.Join(dir, "kanban.json")
	opt := func() options { return options{store: store} }

	type check struct {
		name string
		ok   bool
	}
	var checks []check
	add := func(name string, ok bool) { checks = append(checks, check{name, ok}) }

	// add two cards
	r := run("add", []string{"fix the orb throttling"}, opt())
	add("add creates card #0 (default agent claude, col 0)",
		r.OK && r.Card != nil && r.Card.Index == 0 && r.Card.Agent == "claude" && r.Card.Col == 0)

	o := opt()
	o.col = "2"
	o.agent = "whoretana"
	r = run("add", []string{"ship the weekly video"}, o)
	add("add creates card #1 with --col 2 --agent whoretana",
		r.OK && r.Card != nil && r.Card.Index == 1 && r.Card.Col == 2 && r.Card.Agent == "whoretana")

	// list
	r = run("list", nil, opt())
	add("list returns both cards", r.OK && r.Count == 2 && len(r.Cards) == 2)

	// list --col filter
	lo := opt()
	lo.col = "2"
	r = run("list", nil, lo)
	add("list --col 2 returns exactly card #1", r.OK && r.Count == 1 && r.Cards[0].Index == 1)

	// move by index
	r = run("move", []string{"0", "1"}, opt())
	add("move 0 1 sets card #0 to col 1", r.OK && r.Card != nil && r.Card.Index == 0 && r.Card.Col == 1)

	// move by text match
	r = run("move", []string{"weekly video", "0"}, opt())
	add("move by text match resolves the one card", r.OK && r.Card != nil && r.Card.Index == 1 && r.Card.Col == 0)

	// note appends to text
	r = run("note", []string{"0", "[WORKING]"}, opt())
	add("note appends to card #0 text",
		r.OK && r.Card != nil && r.Card.Text == "fix the orb throttling [WORKING]")

	// PERSISTENCE: a brand-new load from disk sees the mutations
	reloaded, lerr := load(store)
	add("board persists across a fresh load (col + text survive)",
		lerr == nil && len(reloaded) == 2 && reloaded[0].Col() == 1 &&
			reloaded[0].Text() == "fix the orb throttling [WORKING]")

	// empty text rejected
	add("add REJECTS empty text", !run("add", []string{"   "}, opt()).OK)

	// bad column rejected
	bo := opt()
	bo.col = "-1"
	add("add REJECTS a negative column", !run("add", []string{"x"}, bo).OK)

	// out-of-range index rejected
	add("move REJECTS an out-of-range index", !run("move", []string{"99", "1"}, opt()).OK)

	// no-match rejected
	add("move REJECTS a text that matches no card", !run("move", []string{"zzz-nope", "1"}, opt()).OK)

	// DELETE IS NOT SUPPORTED (Law 8b)
	add("delete action is REFUSED (never removes a card)", !run("delete", []string{"0"}, opt()).OK)

	// UNKNOWN FIELDS PRESERVED (Law 8b): seed a card carrying an extra field,
	// mutate it, and confirm the extra field survives.
	pfDir, _ := os.MkdirTemp("", "becky-kanban-preserve-")
	defer os.RemoveAll(pfDir)
	pf := filepath.Join(pfDir, "kanban.json")
	os.WriteFile(pf, []byte(`[{"agent":"claude","col":0,"text":"keep me","id":"card-xyz","order":7}]`), 0o644)
	po := opt()
	po.store = pf
	run("move", []string{"0", "2"}, po)
	pfReloaded, _ := load(pf)
	_, hasID := pfReloaded[0]["id"]
	_, hasOrder := pfReloaded[0]["order"]
	add("unknown card fields (id, order) survive a move", len(pfReloaded) == 1 && hasID && hasOrder && pfReloaded[0].Col() == 2)

	// CORRUPT STORE: refused AND left untouched (Law 8b)
	corruptDir, _ := os.MkdirTemp("", "becky-kanban-corrupt-")
	defer os.RemoveAll(corruptDir)
	corrupt := filepath.Join(corruptDir, "kanban.json")
	badBytes := []byte("[ this is not valid json }")
	os.WriteFile(corrupt, badBytes, 0o644)
	co := opt()
	co.store = corrupt
	r = run("add", []string{"should not be written"}, co)
	after, _ := os.ReadFile(corrupt)
	add("corrupt store is REFUSED and left byte-for-byte untouched",
		!r.OK && string(after) == string(badBytes))

	// NEVER DELETES: after everything, both original cards are still present
	final, _ := load(store)
	add("no action ever removed a card (still 2)", len(final) == 2)

	// on-disk store is a bare JSON array (the shape MissionControl reads)
	raw, _ := os.ReadFile(store)
	var arr []map[string]json.RawMessage
	add("on-disk store is a bare JSON array", json.Unmarshal(raw, &arr) == nil && len(arr) == 2)

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
		fmt.Printf("becky-kanban selftest: PASS (%d/%d checks)\n", len(checks), len(checks))
		return 0
	}
	fmt.Printf("becky-kanban selftest: FAIL (%d/%d checks failed)\n", failed, len(checks))
	return 1
}
