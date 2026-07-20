package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// tempStore returns options pointing at a fresh per-test store file.
func tempStore(t *testing.T) options {
	t.Helper()
	return options{store: filepath.Join(t.TempDir(), "kanban.json")}
}

func TestAddDefaultsThenList(t *testing.T) {
	o := tempStore(t)

	r := run("add", []string{"fix", "the", "orb"}, o)
	if !r.OK || r.Card == nil {
		t.Fatalf("add failed: %+v", r)
	}
	if r.Card.Index != 0 || r.Card.Text != "fix the orb" || r.Card.Col != 0 || r.Card.Agent != "claude" {
		t.Fatalf("unexpected card: %+v", r.Card)
	}

	r = run("list", nil, o)
	if !r.OK || r.Count != 1 || len(r.Cards) != 1 {
		t.Fatalf("list wrong: %+v", r)
	}
}

func TestAddWithColAndAgent(t *testing.T) {
	o := tempStore(t)
	o.col = "2"
	o.agent = "whoretana"
	r := run("add", []string{"ship video"}, o)
	if !r.OK || r.Card.Col != 2 || r.Card.Agent != "whoretana" {
		t.Fatalf("add with col/agent failed: %+v", r.Card)
	}
	// bad column is rejected
	bo := tempStore(t)
	bo.col = "banana"
	if r := run("add", []string{"x"}, bo); r.OK {
		t.Fatalf("expected non-numeric column to fail")
	}
	bo2 := tempStore(t)
	bo2.col = "-3"
	if r := run("add", []string{"x"}, bo2); r.OK {
		t.Fatalf("expected negative column to fail")
	}
}

func TestMoveByIndexAndMatch(t *testing.T) {
	o := tempStore(t)
	run("add", []string{"alpha card"}, o)
	run("add", []string{"beta card"}, o)

	// by index (positional col)
	if r := run("move", []string{"0", "1"}, o); !r.OK || r.Card.Index != 0 || r.Card.Col != 1 {
		t.Fatalf("move by index failed: %+v", r)
	}
	// by index (col via flag)
	fo := o
	fo.col = "2"
	if r := run("move", []string{"0"}, fo); !r.OK || r.Card.Col != 2 {
		t.Fatalf("move by index with --col failed: %+v", r)
	}
	// by unique text match
	if r := run("move", []string{"beta", "2"}, o); !r.OK || r.Card.Index != 1 || r.Card.Col != 2 {
		t.Fatalf("move by match failed: %+v", r)
	}
	// missing destination column rejected
	if r := run("move", []string{"0"}, o); r.OK {
		t.Fatalf("expected move with no column to fail")
	}
	// out-of-range index rejected
	if r := run("move", []string{"99", "1"}, o); r.OK {
		t.Fatalf("expected out-of-range index to fail")
	}
}

func TestMoveAmbiguousMatchRejected(t *testing.T) {
	o := tempStore(t)
	run("add", []string{"build the widget"}, o)
	run("add", []string{"build the gadget"}, o)
	// "build the" matches both -> must refuse, never guess
	if r := run("move", []string{"build the", "2"}, o); r.OK {
		t.Fatalf("expected ambiguous match to be refused: %+v", r)
	}
	// no-match refused
	if r := run("move", []string{"nonexistent", "2"}, o); r.OK {
		t.Fatalf("expected no-match to be refused")
	}
	// both cards still at col 0 (ambiguous move touched nothing)
	r := run("list", nil, o)
	for _, c := range r.Cards {
		if c.Col != 0 {
			t.Fatalf("ambiguous move should have changed nothing, got %+v", c)
		}
	}
}

func TestNoteAppends(t *testing.T) {
	o := tempStore(t)
	run("add", []string{"launch"}, o)

	r := run("note", []string{"0", "[WORKING]"}, o)
	if !r.OK || r.Card.Text != "launch [WORKING]" {
		t.Fatalf("first note failed: %+v", r.Card)
	}
	r = run("note", []string{"0", "[DONE]"}, o)
	if !r.OK || r.Card.Text != "launch [WORKING] [DONE]" {
		t.Fatalf("second note should append: %+v", r.Card)
	}
	// empty note rejected
	if r := run("note", []string{"0"}, o); r.OK {
		t.Fatalf("expected empty note to fail")
	}
	// note by text match
	if r := run("note", []string{"launch", "extra"}, o); !r.OK {
		t.Fatalf("note by match failed: %+v", r)
	}
}

func TestListColFilter(t *testing.T) {
	o := tempStore(t)
	run("add", []string{"a"}, o)
	co := o
	co.col = "2"
	run("add", []string{"b"}, co)

	fo := o
	fo.col = "2"
	if r := run("list", nil, fo); !r.OK || r.Count != 1 || r.Cards[0].Text != "b" {
		t.Fatalf("filter col 2 wrong: %+v", r)
	}
	fo0 := o
	fo0.col = "0"
	if r := run("list", nil, fo0); !r.OK || r.Count != 1 || r.Cards[0].Text != "a" {
		t.Fatalf("filter col 0 wrong: %+v", r)
	}
}

func TestPersistenceAcrossLoads(t *testing.T) {
	o := tempStore(t)
	run("add", []string{"first"}, o)
	run("add", []string{"second"}, o)
	run("move", []string{"0", "2"}, o)
	run("note", []string{"1", "progress"}, o)

	board, err := load(o.store)
	if err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	if len(board) != 2 {
		t.Fatalf("want 2 cards, got %d", len(board))
	}
	if board[0].Col() != 2 {
		t.Fatalf("card 0 col not persisted: %d", board[0].Col())
	}
	if board[1].Text() != "second progress" {
		t.Fatalf("card 1 text not persisted: %q", board[1].Text())
	}
}

// TestPreservesUnknownFields is the Law 8b guarantee: a field this tool does not
// know about (an id the GUI might add) must survive a round-trip untouched.
func TestPreservesUnknownFields(t *testing.T) {
	o := tempStore(t)
	seed := `[{"agent":"claude","col":0,"text":"keep me","id":"abc-123","order":5}]`
	if err := os.WriteFile(o.store, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := run("move", []string{"0", "1"}, o); !r.OK {
		t.Fatalf("move failed: %+v", r)
	}
	if r := run("note", []string{"0", "hi"}, o); !r.OK {
		t.Fatalf("note failed: %+v", r)
	}
	board, _ := load(o.store)
	if len(board) != 1 {
		t.Fatalf("want 1 card, got %d", len(board))
	}
	var id string
	if err := json.Unmarshal(board[0]["id"], &id); err != nil || id != "abc-123" {
		t.Fatalf("unknown field 'id' not preserved: %q err=%v", id, err)
	}
	if _, ok := board[0]["order"]; !ok {
		t.Fatalf("unknown field 'order' not preserved")
	}
	if board[0].Col() != 1 || board[0].Text() != "keep me hi" {
		t.Fatalf("known fields not updated: col=%d text=%q", board[0].Col(), board[0].Text())
	}
}

// TestPreservesExistingKeyOrder confirms the on-disk field order matches the
// existing kanban.json convention (agent, col, text - which is alphabetical, and
// how encoding/json emits map keys).
func TestPreservesExistingKeyOrder(t *testing.T) {
	o := tempStore(t)
	run("add", []string{"x"}, o)
	raw, _ := os.ReadFile(o.store)
	s := string(raw)
	ai, ci, ti := indexOfKey(s, `"agent"`), indexOfKey(s, `"col"`), indexOfKey(s, `"text"`)
	if !(ai >= 0 && ci > ai && ti > ci) {
		t.Fatalf("expected agent<col<text ordering, got positions %d,%d,%d in %s", ai, ci, ti, s)
	}
}

func indexOfKey(s, key string) int {
	for i := 0; i+len(key) <= len(s); i++ {
		if s[i:i+len(key)] == key {
			return i
		}
	}
	return -1
}

func TestCorruptStoreRefusedAndUntouched(t *testing.T) {
	o := tempStore(t)
	bad := []byte("not json at all {[")
	if err := os.WriteFile(o.store, bad, 0o644); err != nil {
		t.Fatal(err)
	}
	if r := run("add", []string{"x"}, o); r.OK {
		t.Fatalf("expected corrupt store to fail the add")
	}
	after, _ := os.ReadFile(o.store)
	if string(after) != string(bad) {
		t.Fatalf("corrupt store was modified! before=%q after=%q", bad, after)
	}
}

// TestNoDeleteEverRemovesACard is the Law 8b assertion the task requires: no
// action removes a card, and the `delete` verb is explicitly unreachable.
func TestNoDeleteEverRemovesACard(t *testing.T) {
	o := tempStore(t)
	run("add", []string{"keep me"}, o)
	run("add", []string{"keep me too"}, o)
	// churn through every action, including the refused delete verbs
	run("move", []string{"0", "1"}, o)
	run("move", []string{"1", "2"}, o)
	run("note", []string{"0", "note"}, o)
	for _, verb := range []string{"delete", "remove", "rm", "del"} {
		if r := run(verb, []string{"0"}, o); r.OK {
			t.Fatalf("verb %q must be refused (Law 8b), got OK", verb)
		}
	}
	if r := run("list", nil, o); r.Count != 2 {
		t.Fatalf("a card disappeared: %+v", r)
	}
}

func TestUnknownAction(t *testing.T) {
	o := tempStore(t)
	if r := run("frobnicate", nil, o); r.OK {
		t.Fatalf("expected unknown action to fail")
	}
}

func TestEmptyBoardOps(t *testing.T) {
	o := tempStore(t)
	// list on a missing store is a fresh empty board, not an error
	if r := run("list", nil, o); !r.OK || r.Count != 0 {
		t.Fatalf("list on empty board should succeed with 0: %+v", r)
	}
	// move on an empty board is a clean error, not a crash
	if r := run("move", []string{"0", "1"}, o); r.OK {
		t.Fatalf("move on empty board should fail")
	}
}
