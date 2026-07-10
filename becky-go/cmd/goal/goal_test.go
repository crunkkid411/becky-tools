package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// fixedClock pins nowFunc so timestamps are deterministic in tests (becky
// invariant: same input -> same output). Restores the real clock on cleanup.
func fixedClock(t *testing.T) {
	t.Helper()
	prev := nowFunc
	nowFunc = func() string { return "2026-07-10T00:00:00Z" }
	t.Cleanup(func() { nowFunc = prev })
}

// tempStore returns options pointing at a fresh per-test store file.
func tempStore(t *testing.T) options {
	t.Helper()
	return options{store: filepath.Join(t.TempDir(), "goals.json")}
}

func TestAddThenList(t *testing.T) {
	fixedClock(t)
	o := tempStore(t)

	r := run("add", []string{"ship", "the", "video"}, o)
	if !r.OK || r.Goal == nil {
		t.Fatalf("add failed: %+v", r)
	}
	if r.Goal.ID != "g1" || r.Goal.Outcome != "ship the video" || r.Goal.Status != StatusTodo {
		t.Fatalf("unexpected goal: %+v", r.Goal)
	}
	if r.Goal.Created != "2026-07-10T00:00:00Z" {
		t.Fatalf("clock not applied: %q", r.Goal.Created)
	}

	r = run("list", nil, o)
	if !r.OK || r.Count != 1 || len(r.Goals) != 1 {
		t.Fatalf("list wrong: %+v", r)
	}
}

func TestNextIDMonotonic(t *testing.T) {
	fixedClock(t)
	o := tempStore(t)
	for i, want := range []string{"g1", "g2", "g3"} {
		r := run("add", []string{"goal"}, o)
		if !r.OK || r.Goal.ID != want {
			t.Fatalf("add #%d got id %q want %q", i, r.Goal.ID, want)
		}
	}
}

func TestUpdateStatusValidationAndFlags(t *testing.T) {
	fixedClock(t)
	o := tempStore(t)
	run("add", []string{"do a thing"}, o)

	// positional form
	if r := run("update-status", []string{"g1", "active"}, o); !r.OK || r.Goal.Status != StatusActive {
		t.Fatalf("positional update-status failed: %+v", r)
	}
	// flag form
	fo := o
	fo.id = "g1"
	fo.status = "blocked"
	if r := run("update-status", nil, fo); !r.OK || r.Goal.Status != StatusBlocked {
		t.Fatalf("flag update-status failed: %+v", r)
	}
	// invalid status rejected
	if r := run("update-status", []string{"g1", "sideways"}, o); r.OK {
		t.Fatalf("expected invalid status to fail")
	}
	// unknown id rejected
	if r := run("update-status", []string{"g42", "done"}, o); r.OK {
		t.Fatalf("expected unknown id to fail")
	}
}

func TestNoteAppends(t *testing.T) {
	fixedClock(t)
	o := tempStore(t)
	run("add", []string{"launch"}, o)

	r := run("note", []string{"g1", "step one done"}, o)
	if !r.OK || len(r.Goal.Progress) != 1 || r.Goal.Progress[0].Text != "step one done" {
		t.Fatalf("first note failed: %+v", r)
	}
	r = run("note", []string{"g1", "step two done"}, o)
	if !r.OK || len(r.Goal.Progress) != 2 {
		t.Fatalf("second note should append: %+v", r)
	}
	// empty note rejected
	if r := run("note", []string{"g1"}, o); r.OK {
		t.Fatalf("expected empty note to fail")
	}
}

func TestPersistenceAcrossLoads(t *testing.T) {
	fixedClock(t)
	o := tempStore(t)
	run("add", []string{"first"}, o)
	run("add", []string{"second"}, o)
	run("update-status", []string{"g1", "done"}, o)
	run("note", []string{"g2", "in progress"}, o)

	goals, err := load(o.store)
	if err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	if len(goals) != 2 {
		t.Fatalf("want 2 goals, got %d", len(goals))
	}
	if goals[0].Status != StatusDone {
		t.Fatalf("g1 status not persisted: %q", goals[0].Status)
	}
	if len(goals[1].Progress) != 1 {
		t.Fatalf("g2 progress not persisted: %+v", goals[1])
	}
}

func TestListStatusFilter(t *testing.T) {
	fixedClock(t)
	o := tempStore(t)
	run("add", []string{"a"}, o)
	run("add", []string{"b"}, o)
	run("update-status", []string{"g2", "done"}, o)

	if r := run("list", []string{"done"}, o); !r.OK || r.Count != 1 || r.Goals[0].ID != "g2" {
		t.Fatalf("filter done wrong: %+v", r)
	}
	if r := run("list", []string{"todo"}, o); !r.OK || r.Count != 1 || r.Goals[0].ID != "g1" {
		t.Fatalf("filter todo wrong: %+v", r)
	}
	// invalid filter rejected
	if r := run("list", []string{"bogus"}, o); r.OK {
		t.Fatalf("expected bogus filter to fail")
	}
}

func TestCorruptStoreRefusedAndUntouched(t *testing.T) {
	fixedClock(t)
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

func TestNeverDeletes(t *testing.T) {
	fixedClock(t)
	o := tempStore(t)
	run("add", []string{"keep me"}, o)
	run("add", []string{"keep me too"}, o)
	// churn through every mutating action
	run("update-status", []string{"g1", "active"}, o)
	run("update-status", []string{"g1", "done"}, o)
	run("note", []string{"g2", "still here"}, o)
	if r := run("list", nil, o); r.Count != 2 {
		t.Fatalf("a goal disappeared: %+v", r)
	}
}

func TestUnknownAction(t *testing.T) {
	o := tempStore(t)
	if r := run("frobnicate", nil, o); r.OK {
		t.Fatalf("expected unknown action to fail")
	}
}

func TestStoreIsBareArrayOnDisk(t *testing.T) {
	// MissionControl reads kanban.json as a bare JSON array; goals.json matches
	// that convention so the future GUI parses it the same way.
	fixedClock(t)
	o := tempStore(t)
	run("add", []string{"x"}, o)
	raw, err := os.ReadFile(o.store)
	if err != nil {
		t.Fatal(err)
	}
	var arr []Goal
	if err := json.Unmarshal(raw, &arr); err != nil {
		t.Fatalf("on-disk store is not a bare array: %v", err)
	}
	if len(arr) != 1 || arr[0].ID != "g1" {
		t.Fatalf("unexpected on-disk content: %s", raw)
	}
}
