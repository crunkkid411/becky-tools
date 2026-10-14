// Package vegasctl drives the running VEGAS through becky-vegas: launch, open,
// save, read the timeline, run one of becky's scripts on a job file. VEGAS is
// never force-killed and a message box is never answered blind - its text is
// returned first.
//
// ponytail: cmd/livestream/vegas.go still has its own copy of these helpers
// (it predates this package); move it over when that tool is next touched.
package vegasctl

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Bin finds a sibling becky tool: next to this exe, then on PATH.
func Bin(name string) (string, error) {
	exe := name + ".exe"
	if self, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(self), exe)
		if _, err := os.Stat(cand); err == nil {
			return cand, nil
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("%s was not found - run build-all-tools.bat", name)
}

// Scripts is becky-tools\vegas (BECKY_VEGAS_SCRIPTS overrides).
func Scripts() string {
	if v := strings.TrimSpace(os.Getenv("BECKY_VEGAS_SCRIPTS")); v != "" {
		return v
	}
	if self, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(self), "..", "..", "vegas")
		if _, err := os.Stat(filepath.Join(cand, "BeckyCut.cs")); err == nil {
			abs, _ := filepath.Abs(cand)
			return abs
		}
	}
	return `X:\AI-2\becky-tools\vegas`
}

// Reply is becky-vegas's answer.
type Reply struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error"`
}

// PID pins every command of a run to the VEGAS that Launch answered with.
var PID int

// Call runs one becky-vegas command.
func Call(timeout time.Duration, args ...string) (Reply, error) {
	var r Reply
	bin, err := Bin("becky-vegas")
	if err != nil {
		return r, err
	}
	full := []string{"--timeout", timeout.String()}
	if PID > 0 {
		full = append(full, "--pid", strconv.Itoa(PID))
	}
	full = append(full, args...)
	out, runErr := exec.Command(bin, full...).Output()
	if jerr := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &r); jerr != nil {
		return r, fmt.Errorf("becky-vegas %s: %v (%s)", args[0], runErr, strings.TrimSpace(string(out)))
	}
	if !r.OK {
		return r, fmt.Errorf("VEGAS refused %s: %s", args[0], r.Error)
	}
	return r, nil
}

// Launch starts VEGAS (or uses the open one) and pins the run to it.
func Launch() error {
	r, err := Call(6*time.Minute, "launch")
	if err != nil {
		return err
	}
	var st struct {
		PID int `json:"pid"`
	}
	if json.Unmarshal(r.Result, &st) == nil && st.PID > 0 {
		PID = st.PID
	}
	return nil
}

// MessageBox returns the text of an open VEGAS message box ("" when none).
func MessageBox() string {
	r, err := Call(30*time.Second, "dialogs")
	if err != nil {
		return ""
	}
	var ds []struct {
		Title   string   `json:"title"`
		Text    string   `json:"text"`
		Class   string   `json:"class"`
		Buttons []string `json:"buttons"`
	}
	if json.Unmarshal(r.Result, &ds) != nil {
		return ""
	}
	for _, d := range ds {
		if d.Class == "#32770" && slices.Contains(d.Buttons, "OK") {
			return strings.TrimSpace(d.Title + ": " + d.Text)
		}
	}
	return ""
}

// RunJob runs one of becky's VEGAS scripts on a job file and returns its
// result line ("ok ..."). A message box the script raises is recorded, then
// answered OK (the only button such boxes have), and the run fails with its text.
func RunJob(script, job, body string, timeout time.Duration) (string, error) {
	if err := os.WriteFile(job, []byte(body), 0o644); err != nil {
		return "", err
	}
	_ = os.Remove(job + ".result.txt")
	done := make(chan error, 1)
	go func() {
		_, e := Call(timeout, "run_script", "path="+filepath.Join(Scripts(), script), "job="+job)
		done <- e
	}()
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	var dialog string
	for wait := true; wait; {
		select {
		case e := <-done:
			if e != nil {
				return "", fmt.Errorf("%s failed: %w", script, e)
			}
			wait = false
		case <-tick.C:
			if d := MessageBox(); d != "" {
				dialog = strings.TrimSpace(dialog + " | " + d)
				_, _ = Call(30*time.Second, "dialog_click", "button=OK")
			}
		}
	}
	if dialog != "" {
		return "", fmt.Errorf("%s showed a message: %s", script, dialog)
	}
	res, _ := os.ReadFile(job + ".result.txt")
	if !strings.HasPrefix(string(res), "ok") {
		return "", fmt.Errorf("%s did not finish: %s", script, strings.TrimSpace(string(res)))
	}
	return strings.TrimSpace(string(res)), nil
}

// Event is one event of the open project's timeline (becky-vegas timeline).
type Event struct {
	Kind        string  `json:"kind"`
	Source      string  `json:"source"`
	In          float64 `json:"in"`
	Out         float64 `json:"out"`
	Timeline    float64 `json:"timeline"`
	TimelineEnd float64 `json:"timeline_end"`
	Track       int     `json:"track"`
	Rate        float64 `json:"rate"`
}

// Timeline reads the open project's events and frame rate.
func Timeline() ([]Event, float64, error) {
	r, err := Call(2*time.Minute, "timeline")
	if err != nil {
		return nil, 0, err
	}
	var t struct {
		FPS    float64 `json:"fps"`
		Events []Event `json:"events"`
	}
	if err := json.Unmarshal(r.Result, &t); err != nil {
		return nil, 0, fmt.Errorf("unreadable timeline: %w", err)
	}
	return t.Events, t.FPS, nil
}

// ProjectPath: ONE project per job. becky saves over the project it wrote
// last time; when Jordan has saved it himself since (its size or time no
// longer match becky's stamp in work), the new one gets the next free name.
func ProjectPath(want, work string) (string, bool) {
	st, err := os.Stat(want)
	if err != nil {
		return want, false
	}
	var stamp struct {
		Path string `json:"path"`
		Size int64  `json:"size"`
		Mod  int64  `json:"mod"`
	}
	b, _ := os.ReadFile(filepath.Join(work, "project-stamp.json"))
	if json.Unmarshal(b, &stamp) == nil && strings.EqualFold(stamp.Path, want) && stamp.Size == st.Size() && stamp.Mod == st.ModTime().UnixNano() {
		return want, true
	}
	return FreeName(want), false
}

// Stamp records the project as becky left it (see ProjectPath).
func Stamp(veg, work string) {
	if st, err := os.Stat(veg); err == nil {
		b, _ := json.Marshal(map[string]any{"path": veg, "size": st.Size(), "mod": st.ModTime().UnixNano()})
		_ = os.WriteFile(filepath.Join(work, "project-stamp.json"), b, 0o644)
	}
}

// FreeName returns path, or "name (2).ext", "name (3).ext"... when taken.
func FreeName(path string) string {
	if _, err := os.Stat(path); err != nil {
		return path
	}
	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext)
	for n := 2; ; n++ {
		cand := fmt.Sprintf("%s (%d)%s", stem, n, ext)
		if _, err := os.Stat(cand); err != nil {
			return cand
		}
	}
}
