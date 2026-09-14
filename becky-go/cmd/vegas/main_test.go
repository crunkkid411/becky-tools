package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseArgsTypesValues(t *testing.T) {
	got, err := parseArgs([]string{"seconds=12.5", "view=true", `path=X:\clip 1.mp4`, "label=take 2", "in=10", "folder=E:/footage"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"seconds": 12.5, "view": true, "path": `X:\clip 1.mp4`, "label": "take 2", "in": 10.0, "folder": "E:/footage",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseArgs = %#v, want %#v", got, want)
	}
	if _, err := parseArgs([]string{"no-equals"}); err == nil {
		t.Fatal("a word without = must be rejected, not silently dropped")
	}
}

// Two VEGAS windows open: the one focused most recently must win, a dead pid's
// leftover file must be ignored, and --pid must select exactly one.
func TestLoadInstancesAndChoose(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("100.json", `{"pid":100,"pipe":"becky-vegas-100","updated":"2026-09-13T23:00:00Z","last_focused":"2026-09-13T22:00:00Z"}`)
	write("200.json", `{"pid":200,"pipe":"becky-vegas-200","updated":"2026-09-13T22:30:00Z","last_focused":"2026-09-13T23:10:00Z"}`)
	write("300.json", `{"pid":300,"pipe":"becky-vegas-300","updated":"2026-09-13T23:59:00Z","last_focused":"2026-09-13T23:59:00Z"}`)
	write("junk.json", `not json`)
	alive := func(pid int) bool { return pid != 300 }

	list := loadInstances(dir, alive)
	if len(list) != 2 || list[0].PID != 200 || list[1].PID != 100 {
		t.Fatalf("order = %+v, want pid 200 (focused last) then 100, dead 300 dropped", list)
	}
	one, err := choose(list, 100)
	if err != nil || len(one) != 1 || one[0].PID != 100 {
		t.Fatalf("choose pid 100 = %+v, %v", one, err)
	}
	if _, err := choose(list, 999); err == nil {
		t.Fatal("an unknown pid must be an error")
	}
	if _, err := choose(nil, 0); err == nil {
		t.Fatal("no running VEGAS must be an error")
	}
}
