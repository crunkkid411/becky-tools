package main

// Client-side verbs (2026-10-05; Jordan approved new_project, save,
// dialog_click and run_script with arguments - the ask SKILL.md said a save
// verb needed). They are built from the extension's proven run_script verb
// plus plain Win32, so they need no extension rebuild and no VEGAS restart:
//
//	becky-vegas launch                       start VEGAS if needed; wait until it answers
//	becky-vegas new_project                  empty project (refuses if the open one has unsaved changes)
//	becky-vegas save path="X:\f\name.veg"     save as (refuses to overwrite unless overwrite=true)
//	becky-vegas save                         save the open project to its own file
//	becky-vegas run_script path=job.cs args_file=a.json   (or key=value ... as the arguments)
//	becky-vegas dialog_click button="No" [title="..."]
//	becky-vegas open_project path="X:\f\name.veg"   (refuses if the open one has unsaved changes)
//	becky-vegas delete_marks prefix="Breath check|Breath example"   markers + regions whose label starts so; one undo step
//
// (open_project and delete_marks: 2026-10-06, for becky-livestream's breath check.)
//
// run_script arguments travel through %LOCALAPPDATA%\BeckyVegas\script-args.json
// because VEGAS 18 gives a script no way to read arguments. The file is
// rewritten before EVERY run_script ({} when there are none), so a script
// never reads stale arguments.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// clientVerbs are handled here, not by the extension.
var clientVerbs = map[string]bool{"launch": true, "new_project": true, "save": true, "dialog_click": true,
	"open_project": true, "delete_marks": true}

func scriptArgsPath() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "BeckyVegas", "script-args.json")
}

// prepareRunScript writes the script's arguments file and returns the args
// the extension gets (just the path).
func prepareRunScript(args map[string]any) (map[string]any, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return nil, errors.New("run_script needs path=<script.cs>")
	}
	payload := map[string]any{}
	if f, ok := args["args_file"].(string); ok && f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("args_file: %w", err)
		}
		if err := json.Unmarshal(b, &payload); err != nil {
			return nil, fmt.Errorf("args_file must hold one JSON object: %w", err)
		}
	} else {
		for k, v := range args {
			if k != "path" {
				payload[k] = v
			}
		}
	}
	// No HTML escaping: a script reading "job" with a plain regex must see a
	// path with & in it as &, not &.
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(payload); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(scriptArgsPath()), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(scriptArgsPath(), []byte(buf.String()), 0o644); err != nil {
		return nil, err
	}
	return map[string]any{"path": path}, nil
}

// csString is a C# verbatim string literal.
func csString(s string) string {
	return `@"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// jobScript wraps a FromVegas body into a complete VEGAS script (ASCII, CRLF).
func jobScript(body string) string {
	src := "using System;\nusing ScriptPortal.Vegas;\n\npublic class EntryPoint\n{\n    public void FromVegas(Vegas vegas)\n    {\n" +
		body + "\n    }\n}\n"
	return strings.ReplaceAll(src, "\n", "\r\n")
}

func newProjectScript() string {
	return jobScript("        vegas.NewProject(false, false);")
}

func saveScript(path string) string {
	if path == "" {
		return jobScript("        vegas.SaveProject();")
	}
	return jobScript("        vegas.SaveProject(" + csString(path) + ");")
}

// openProjectScript and deleteMarksScript answer through a result file instead
// of throwing: a script error shows a VEGAS dialog, which stalls an unattended run.
func openProjectScript(path, result string) string {
	return jobScript(`        string r;
        try { r = vegas.OpenProject(` + csString(path) + `) ? "ok" : "error: VEGAS did not open the project"; }
        catch (Exception e) { r = "error: " + e.Message; }
        System.IO.File.WriteAllText(` + csString(result) + `, r);`)
}

func deleteMarksScript(prefixes []string, result string) string {
	lits := make([]string, len(prefixes))
	for i, p := range prefixes {
		lits[i] = csString(p)
	}
	return jobScript(`        string r;
        try
        {
            string[] prefixes = { ` + strings.Join(lits, ", ") + ` };
            System.Collections.Generic.List<Marker> ms = new System.Collections.Generic.List<Marker>();
            foreach (Marker m in vegas.Project.Markers)
                foreach (string p in prefixes)
                    if ((m.Label ?? "").StartsWith(p, StringComparison.Ordinal)) { ms.Add(m); break; }
            System.Collections.Generic.List<Region> rs = new System.Collections.Generic.List<Region>();
            foreach (Region g in vegas.Project.Regions)
                foreach (string p in prefixes)
                    if ((g.Label ?? "").StartsWith(p, StringComparison.Ordinal)) { rs.Add(g); break; }
            using (UndoBlock u = new UndoBlock("Becky: remove marks"))
            {
                foreach (Marker m in ms) vegas.Project.Markers.Remove(m);
                foreach (Region g in rs) vegas.Project.Regions.Remove(g);
            }
            r = "ok " + ms.Count + " " + rs.Count;
        }
        catch (Exception e) { r = "error: " + e.Message; }
        System.IO.File.WriteAllText(` + csString(result) + `, r);`)
}

// splitPrefixes reads prefix="a|b". Empty parts are dropped: an empty prefix
// would match, and remove, every marker.
func splitPrefixes(v any) []string {
	s, _ := v.(string)
	var out []string
	for p := range strings.SplitSeq(s, "|") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// countPrefixed counts the markers verb's entries whose label starts with a prefix.
func countPrefixed(marks any, prefixes []string) int {
	n := 0
	for _, m := range asList(marks) {
		label, _ := m["label"].(string)
		for _, p := range prefixes {
			if strings.HasPrefix(label, p) {
				n++
				break
			}
		}
	}
	return n
}

// checkSavePath refuses anything but a .veg in an existing folder, and never
// overwrites an existing file unless asked (originals are never rewritten).
func checkSavePath(path string, overwrite bool, exists func(string) bool) error {
	if !strings.EqualFold(filepath.Ext(path), ".veg") {
		return fmt.Errorf("save path must end in .veg: %s", path)
	}
	if !exists(filepath.Dir(path)) {
		return fmt.Errorf("the folder does not exist: %s", filepath.Dir(path))
	}
	if exists(path) && !overwrite {
		return fmt.Errorf("%s already exists - pick a new name, or pass overwrite=true", path)
	}
	return nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// writeJob saves a generated script under %TEMP%\becky-vegas and returns its path.
func writeJob(name, src string) (string, error) {
	dir := filepath.Join(os.TempDir(), "becky-vegas")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(dir, name)
	return p, os.WriteFile(p, []byte(src), 0o644)
}

// send asks one VEGAS one command and returns its result (or its refusal).
func send(inst instance, cmd string, args map[string]any, timeout time.Duration) (any, error) {
	if args == nil {
		args = map[string]any{}
	}
	req, _ := json.Marshal(map[string]any{"id": 1, "cmd": cmd, "args": args})
	reply, err := call(inst.Pipe, req, timeout)
	if err != nil {
		return nil, err
	}
	var r struct {
		OK     bool   `json:"ok"`
		Result any    `json:"result"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(reply, &r); err != nil {
		return nil, fmt.Errorf("unreadable answer from VEGAS: %w", err)
	}
	if !r.OK {
		return nil, errors.New(r.Error)
	}
	return r.Result, nil
}

func runJob(inst instance, name, src string, timeout time.Duration) error {
	p, err := writeJob(name, src)
	if err != nil {
		return err
	}
	if _, err := prepareRunScript(map[string]any{"path": p}); err != nil {
		return err
	}
	_, err = send(inst, "run_script", map[string]any{"path": p}, timeout)
	return err
}

// runJobChecked runs a job script that writes "ok ..." or "error: ..." to a
// result file, and returns that answer.
func runJobChecked(inst instance, name string, script func(result string) string, timeout time.Duration) (string, error) {
	result := filepath.Join(os.TempDir(), "becky-vegas", name+".result.txt")
	_ = os.Remove(result)
	if err := runJob(inst, name+".cs", script(result), timeout); err != nil {
		return "", err
	}
	b, err := os.ReadFile(result)
	if err != nil {
		return "", fmt.Errorf("VEGAS ran %s but it did not report back", name)
	}
	s := strings.TrimSpace(string(b))
	if !strings.HasPrefix(s, "ok") {
		return "", errors.New(strings.TrimPrefix(s, "error: "))
	}
	return s, nil
}

// refuseUnsaved stops a verb that would replace the open project while it has
// unsaved changes (VEGAS would ask "save changes?" and stall).
func refuseUnsaved(inst instance, timeout time.Duration) error {
	st, err := send(inst, "status", nil, timeout)
	if err != nil {
		return err
	}
	if m, _ := st.(map[string]any); m != nil && m["project_modified"] == true {
		return errors.New("the open project has unsaved changes - save it first (becky-vegas save)")
	}
	return nil
}

// runClientVerb carries out one client-side verb against the chosen VEGAS.
func runClientVerb(cmd string, args map[string]any, inst instance, timeout time.Duration) (any, error) {
	switch cmd {
	case "new_project":
		if err := refuseUnsaved(inst, timeout); err != nil {
			return nil, err
		}
		if err := runJob(inst, "new_project.cs", newProjectScript(), timeout); err != nil {
			return nil, err
		}
		return send(inst, "status", nil, timeout)
	case "open_project":
		path, _ := args["path"].(string)
		if !strings.EqualFold(filepath.Ext(path), ".veg") || !fileExists(path) {
			return nil, fmt.Errorf("open_project needs path=<an existing .veg file>, got %q", path)
		}
		if err := refuseUnsaved(inst, timeout); err != nil {
			return nil, err
		}
		if _, err := runJobChecked(inst, "open_project", func(res string) string { return openProjectScript(path, res) }, timeout); err != nil {
			return nil, err
		}
		st, err := send(inst, "status", nil, timeout)
		if err != nil {
			return nil, err
		}
		if m, _ := st.(map[string]any); m == nil || !strings.EqualFold(filepath.Clean(fmt.Sprint(m["project_path"])), filepath.Clean(path)) {
			return nil, fmt.Errorf("VEGAS answered, but %s is not the open project", path)
		}
		return st, nil
	case "delete_marks":
		prefixes := splitPrefixes(args["prefix"])
		if len(prefixes) == 0 {
			return nil, errors.New(`delete_marks needs prefix="how the labels start" (several: "a|b")`)
		}
		out, err := runJobChecked(inst, "delete_marks", func(res string) string { return deleteMarksScript(prefixes, res) }, timeout)
		if err != nil {
			return nil, err
		}
		var nm, nr int
		_, _ = fmt.Sscanf(out, "ok %d %d", &nm, &nr)
		left, err := send(inst, "markers", nil, timeout)
		if err != nil {
			return nil, err
		}
		if n := countPrefixed(left, prefixes); n > 0 {
			return nil, fmt.Errorf("%d marker(s) starting with %q are still on the timeline", n, prefixes)
		}
		return map[string]any{"markers_removed": nm, "regions_removed": nr}, nil
	case "save":
		path, _ := args["path"].(string)
		if path != "" {
			if err := checkSavePath(path, args["overwrite"] == true, fileExists); err != nil {
				return nil, err
			}
		}
		if err := runJob(inst, "save.cs", saveScript(path), timeout); err != nil {
			return nil, err
		}
		if path != "" && !fileExists(path) {
			return nil, fmt.Errorf("VEGAS finished but %s was not written", path)
		}
		return send(inst, "status", nil, timeout)
	case "dialog_click":
		button, _ := args["button"].(string)
		if button == "" {
			return nil, errors.New("dialog_click needs button=<label>")
		}
		title, _ := args["title"].(string)
		dlg, err := clickDialogButton(inst.PID, button, title)
		if err != nil {
			return nil, err
		}
		return map[string]any{"clicked": button, "dialog": dlg}, nil
	}
	return nil, fmt.Errorf("unknown client verb %q", cmd)
}

// vegasExe is VEGAS Pro 18's program (BECKY_VEGAS_EXE overrides it).
func vegasExe() string {
	if v := os.Getenv("BECKY_VEGAS_EXE"); v != "" {
		return v
	}
	return `C:\Program Files\VEGAS\VEGAS Pro 18.0\vegas180.exe`
}

// launch starts VEGAS when no VEGAS with the extension answers, then waits
// until one does. The one known startup dialog - "restore the autosaved
// project?" - is answered No AFTER the autosave files are copied to a backup
// folder (nothing is deleted); any other dialog stops the launch with its text.
func launch(timeout time.Duration) (any, error) {
	if inst, ok := firstAnswering(); ok {
		return answerStartup(inst) // it may still be showing the restore question
	}
	cmd := exec.Command(vegasExe())
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("could not start VEGAS: %w", err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)
		inst, ok := answering(pid)
		if !ok {
			continue
		}
		time.Sleep(3 * time.Second) // let startup dialogs appear
		return answerStartup(inst)
	}
	return nil, fmt.Errorf("VEGAS (pid %d) did not answer within %s", pid, timeout)
}

// answerStartup answers the restore-autosave question if it is showing, then
// returns VEGAS's status. Any other open dialog stops with its text.
func answerStartup(inst instance) (any, error) {
	ds, err := send(inst, "dialogs", nil, 30*time.Second)
	if err != nil {
		return nil, err
	}
	for _, d := range asList(ds) {
		if !isAutosaveQuestion(fmt.Sprint(d["title"], " ", d["text"])) {
			return nil, fmt.Errorf("VEGAS has a dialog open that needs a person: %v", d)
		}
		if err := backupAutosaves(); err != nil {
			return nil, fmt.Errorf("could not back up VEGAS autosaves, so the restore question was left open: %w", err)
		}
		if _, err := clickDialogButton(inst.PID, "No", ""); err != nil {
			return nil, err
		}
	}
	return send(inst, "status", nil, 30*time.Second)
}

// isAutosaveQuestion: VEGAS's "restore the autosaved project?" startup question
// (left behind by a session that crashed), however VEGAS words it.
func isAutosaveQuestion(text string) bool {
	t := strings.ToLower(text)
	for _, k := range []string{"autosav", "auto-sav", "auto sav"} {
		if strings.Contains(t, k) {
			return true
		}
	}
	return strings.Contains(t, "restore") && strings.Contains(t, "project")
}

func firstAnswering() (instance, bool) {
	for _, inst := range loadInstances(instancesDir(), processAlive) {
		if _, err := send(inst, "ping", nil, 10*time.Second); err == nil {
			return inst, true
		}
	}
	return instance{}, false
}

func answering(pid int) (instance, bool) {
	for _, inst := range loadInstances(instancesDir(), processAlive) {
		if inst.PID == pid {
			if _, err := send(inst, "ping", nil, 10*time.Second); err == nil {
				return inst, true
			}
		}
	}
	return instance{}, false
}

func asList(v any) []map[string]any {
	var out []map[string]any
	if xs, ok := v.([]any); ok {
		for _, x := range xs {
			if m, ok := x.(map[string]any); ok {
				out = append(out, m)
			}
		}
	}
	return out
}

// backupAutosaves copies VEGAS 18's autosave files to a dated folder beside
// them before the restore question is answered No.
func backupAutosaves() error {
	dir := filepath.Join(os.Getenv("LOCALAPPDATA"), "VEGAS Pro", "18.0")
	files, _ := filepath.Glob(filepath.Join(dir, "*autosave*"))
	if len(files) == 0 {
		return nil
	}
	dst := filepath.Join(dir, "autosave-backup-"+time.Now().Format("2006-01-02-150405"))
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, f := range files {
		if fi, err := os.Stat(f); err != nil || fi.IsDir() {
			continue // earlier backup folders match *autosave* too
		}
		if err := copyFile(f, filepath.Join(dst, filepath.Base(f))); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
