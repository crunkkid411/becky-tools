package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSaveScriptEscapesAndUsesCRLF(t *testing.T) {
	s := saveScript(`X:\Videos\a "b".veg`)
	if !strings.Contains(s, `vegas.SaveProject(@"X:\Videos\a ""b"".veg");`) {
		t.Fatalf("path not escaped as a C# verbatim string:\n%s", s)
	}
	if strings.Contains(strings.ReplaceAll(s, "\r\n", ""), "\n") {
		t.Fatal("job scripts must use CRLF line endings only")
	}
	if !strings.Contains(saveScript(""), "vegas.SaveProject();") {
		t.Fatal("save without a path must save to the project's own file")
	}
	if !strings.Contains(newProjectScript(), "vegas.NewProject(false, false);") {
		t.Fatal("new_project must not prompt (the client already refused unsaved changes)")
	}
}

func TestCheckSavePathNeverOverwritesUnasked(t *testing.T) {
	have := map[string]bool{`X:\f`: true, `X:\f\old.veg`: true}
	exists := func(p string) bool { return have[p] }
	cases := []struct {
		path      string
		overwrite bool
		ok        bool
	}{
		{`X:\f\new.veg`, false, true},
		{`X:\f\old.veg`, false, false},
		{`X:\f\old.veg`, true, true},
		{`X:\f\new.txt`, false, false},
		{`X:\missing\new.veg`, false, false},
	}
	for _, c := range cases {
		err := checkSavePath(c.path, c.overwrite, exists)
		if (err == nil) != c.ok {
			t.Errorf("%s overwrite=%v: got err %v", c.path, c.overwrite, err)
		}
	}
}

func TestPrepareRunScriptWritesFreshArgs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	got, err := prepareRunScript(map[string]any{"path": `C:\job.cs`, "keep": `X:\k.json`, "n": 3.0})
	if err != nil || got["path"] != `C:\job.cs` || len(got) != 1 {
		t.Fatalf("got %v, %v", got, err)
	}
	var a map[string]any
	b, _ := os.ReadFile(filepath.Join(dir, "BeckyVegas", "script-args.json"))
	if json.Unmarshal(b, &a) != nil || a["keep"] != `X:\k.json` || a["n"] != 3.0 || a["path"] != nil {
		t.Fatalf("args file wrong: %s", b)
	}
	// A run without arguments must leave {} so no stale argument is read.
	if _, err := prepareRunScript(map[string]any{"path": `C:\other.cs`}); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(dir, "BeckyVegas", "script-args.json"))
	if strings.TrimSpace(string(b)) != "{}" {
		t.Fatalf("stale args left behind: %s", b)
	}
	// args_file passes a JSON object through as-is.
	af := filepath.Join(dir, "a.json")
	os.WriteFile(af, []byte(`{"keeps":[[1,2]]}`), 0o644)
	if _, err := prepareRunScript(map[string]any{"path": `C:\j.cs`, "args_file": af}); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(dir, "BeckyVegas", "script-args.json"))
	if !strings.Contains(string(b), `"keeps"`) {
		t.Fatalf("args_file not passed through: %s", b)
	}
	if _, err := prepareRunScript(map[string]any{}); err == nil {
		t.Fatal("run_script without a path must be refused")
	}
}

// A script that throws shows a VEGAS dialog and stalls the run, so both new
// job scripts must catch and answer through their result file.
func TestOpenAndDeleteScriptsAnswerThroughAFile(t *testing.T) {
	open := openProjectScript(`X:\f\a "b".veg`, `C:\t\open.result.txt`)
	for _, want := range []string{`vegas.OpenProject(@"X:\f\a ""b"".veg")`, `catch (Exception e)`,
		`System.IO.File.WriteAllText(@"C:\t\open.result.txt", r);`} {
		if !strings.Contains(open, want) {
			t.Errorf("open_project script is missing %q:\n%s", want, open)
		}
	}
	del := deleteMarksScript([]string{"Breath example", `Say "hi"`}, `C:\t\del.result.txt`)
	for _, want := range []string{`string[] prefixes = { @"Breath example", @"Say ""hi""" };`,
		`StartsWith(p, StringComparison.Ordinal)`, `new UndoBlock("Becky: remove marks")`,
		`vegas.Project.Markers.Remove(m);`, `vegas.Project.Regions.Remove(g);`, `r = "ok " + ms.Count + " " + rs.Count;`,
		`catch (Exception e)`} {
		if !strings.Contains(del, want) {
			t.Errorf("delete_marks script is missing %q:\n%s", want, del)
		}
	}
	for _, s := range []string{open, del} {
		if strings.Contains(strings.ReplaceAll(s, "\r\n", ""), "\n") {
			t.Error("job scripts must use CRLF line endings only")
		}
	}
}

// An empty prefix matches every label, so it must never reach VEGAS.
func TestSplitPrefixesNeverEmpty(t *testing.T) {
	cases := map[any][]string{"": nil, " | ": nil, 3.0: nil, "Breath check": {"Breath check"},
		"Breath example| Breath check |": {"Breath example", "Breath check"}}
	for in, want := range cases {
		if got := splitPrefixes(in); !slices.Equal(got, want) {
			t.Errorf("splitPrefixes(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestCountPrefixedReadsTheMarkersVerb(t *testing.T) {
	var marks any
	_ = json.Unmarshal([]byte(`[{"kind":"marker","position":11.7,"label":"Breath example 1 of 4: 0.5 s"},
		{"kind":"marker","position":13,"label":"Cut inside speech, still loud"},
		{"kind":"region","position":20,"length":0.8,"label":"Breath check 1 of 5: 0.8 s"}]`), &marks)
	if n := countPrefixed(marks, []string{"Breath example", "Breath check"}); n != 2 {
		t.Errorf("count = %d, want 2", n)
	}
	if n := countPrefixed(marks, []string{"Unsure"}); n != 0 {
		t.Errorf("count = %d, want 0", n)
	}
}

func TestIsAutosaveQuestion(t *testing.T) {
	for text, want := range map[string]bool{
		"VEGAS Pro: An autosaved project was found. Would you like to restore it?": true,
		"Auto-Save Recovery: open the auto-saved file?":                            true,
		"VEGAS Pro: Restore the last project?":                                     true,
		"VEGAS Pro: Save changes to Untitled.veg?":                                 false,
		"VEGAS Pro: The file could not be found.":                                  false,
	} {
		if got := isAutosaveQuestion(text); got != want {
			t.Errorf("isAutosaveQuestion(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestBackupAutosavesSkipsEarlierBackupFolders(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	dir := filepath.Join(root, "VEGAS Pro", "18.0")
	if err := os.MkdirAll(filepath.Join(dir, "autosave-backup-2026-10-05"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "00008754.autosave.veg"), []byte("veg"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := backupAutosaves(); err != nil {
		t.Fatalf("an earlier backup folder must not stop the backup: %v", err)
	}
	got, _ := filepath.Glob(filepath.Join(dir, "autosave-backup-*-*", "00008754.autosave.veg"))
	if len(got) != 1 {
		t.Fatalf("autosave not copied into a new backup folder: %v", got)
	}
}
