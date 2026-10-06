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
var clientVerbs = map[string]bool{"launch": true, "new_project": true, "save": true, "dialog_click": true}

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

// runClientVerb carries out one client-side verb against the chosen VEGAS.
func runClientVerb(cmd string, args map[string]any, inst instance, timeout time.Duration) (any, error) {
	switch cmd {
	case "new_project":
		st, err := send(inst, "status", nil, timeout)
		if err != nil {
			return nil, err
		}
		if m, _ := st.(map[string]any); m != nil && m["project_modified"] == true {
			return nil, errors.New("the open project has unsaved changes - save it first (becky-vegas save)")
		}
		if err := runJob(inst, "new_project.cs", newProjectScript(), timeout); err != nil {
			return nil, err
		}
		return send(inst, "status", nil, timeout)
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
