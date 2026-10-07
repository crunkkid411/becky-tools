package main

// vegas.go - build the edit in the running VEGAS through becky-vegas (report
// item #7's verbs): launch, new_project, the keep-list applicator script
// (BeckyKeepList.cs), regions and markers (BeckyMarks.cs), save. VEGAS is never
// force-killed and a dialog is never answered blind: its text goes into the
// report first.
//
// The kept pieces go in DIRECTLY, dead air already out. Until 2026-10-06 the
// content ranges went in and BeckyCut.cs (Jordan's own one-click tool, never
// changed) took the dead air out; on every test its result was exactly
// finalPieces (27-livestream: 43 of 43 pieces, frame for frame). Placing them
// directly lets the picture check put back a silence becky-cut took out - his
// held facial expression after "Some of my videos got restored" (expressions.go).
//
// Every command of a run goes to ONE VEGAS (vegasPID, from launch; or
// BECKY_VEGAS_PID): if Jordan clicks another VEGAS mid-run, nothing follows him.

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// beckyBin finds a sibling becky tool: next to this exe, then on PATH.
func beckyBin(name string) (string, error) {
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

// vegasScripts is becky-tools\vegas (BECKY_VEGAS_SCRIPTS overrides).
func vegasScripts() string {
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

type vegasReply struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error"`
}

// vegasPID pins every command of this run to the VEGAS that launch answered with.
var vegasPID int

// vegas runs one becky-vegas command.
func vegas(timeout time.Duration, args ...string) (vegasReply, error) {
	var r vegasReply
	bin, err := beckyBin("becky-vegas")
	if err != nil {
		return r, err
	}
	full := []string{"--timeout", timeout.String()}
	if vegasPID > 0 {
		full = append(full, "--pid", strconv.Itoa(vegasPID))
	}
	full = append(full, args...)
	out, runErr := exec.Command(bin, full...).Output()
	if jerr := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &r); jerr != nil {
		return r, fmt.Errorf("becky-vegas %s: %v (%s)", args[0], runErr, lastLines(string(out), 3))
	}
	if !r.OK {
		return r, fmt.Errorf("VEGAS refused %s: %s", args[0], r.Error)
	}
	return r, nil
}

// messageBox returns the text of an open VEGAS message box ("" when none). Only
// a Windows message box (#32770 with an OK button) counts: BeckyCut's own
// progress window is modal too, and must be left alone.
func messageBox() string {
	r, err := vegas(30*time.Second, "dialogs")
	if err != nil {
		return ""
	}
	return pickMessageBox(r.Result)
}

func pickMessageBox(raw json.RawMessage) string {
	var ds []struct {
		Title   string   `json:"title"`
		Text    string   `json:"text"`
		Class   string   `json:"class"`
		Buttons []string `json:"buttons"`
	}
	if json.Unmarshal(raw, &ds) != nil {
		return ""
	}
	for _, d := range ds {
		if d.Class == "#32770" && slices.Contains(d.Buttons, "OK") {
			return strings.TrimSpace(d.Title + ": " + d.Text)
		}
	}
	return ""
}

// runScriptWatched runs a VEGAS script while watching for a message box. A
// script's message box blocks the call, so its text is recorded first and then
// it is answered OK (the only button such boxes have) - the text is what matters.
func runScriptWatched(timeout time.Duration, args ...string) (dialog string, err error) {
	done := make(chan error, 1)
	go func() {
		_, e := vegas(timeout, append([]string{"run_script"}, args...)...)
		done <- e
	}()
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		select {
		case e := <-done:
			return dialog, e
		case <-tick.C:
			if d := messageBox(); d != "" {
				if dialog != "" {
					dialog += " | "
				}
				dialog += d
				_, _ = vegas(30*time.Second, "dialog_click", "button=OK")
			}
		}
	}
}

type tlEvent struct {
	Kind        string  `json:"kind"`
	Source      string  `json:"source"`
	In          float64 `json:"in"`
	Out         float64 `json:"out"`
	Timeline    float64 `json:"timeline"`
	TimelineEnd float64 `json:"timeline_end"`
	Grouped     bool    `json:"grouped"`
}

// readTimeline returns the audio events as pieces, plus a health line.
func readTimeline() ([]piece, string, error) {
	r, err := vegas(2*time.Minute, "timeline")
	if err != nil {
		return nil, "", err
	}
	var t struct {
		Events []tlEvent `json:"events"`
	}
	if err := json.Unmarshal(r.Result, &t); err != nil {
		return nil, "", fmt.Errorf("unreadable timeline: %w", err)
	}
	var a, v []tlEvent
	ungrouped := 0
	for _, e := range t.Events {
		if !e.Grouped {
			ungrouped++
		}
		switch e.Kind {
		case "audio":
			a = append(a, e)
		case "video":
			v = append(v, e)
		}
	}
	sort.Slice(a, func(i, j int) bool { return a[i].Timeline < a[j].Timeline })
	sort.Slice(v, func(i, j int) bool { return v[i].Timeline < v[j].Timeline })
	gaps, mismatch := 0, abs(len(a)-len(v))
	for i := range a {
		if i > 0 && a[i].Timeline-a[i-1].TimelineEnd > 1e-4 {
			gaps++
		}
		if i < len(v) && (absf(a[i].In-v[i].In) > 1e-4 || absf(a[i].Timeline-v[i].Timeline) > 1e-4) {
			mismatch++
		}
	}
	ps := make([]piece, len(a))
	for i, e := range a {
		ps[i] = piece{In: e.In, Out: e.Out, TL: e.Timeline}
	}
	health := fmt.Sprintf("%d audio + %d video events, %d gaps, %d picture/sound mismatches, %d ungrouped", len(a), len(v), gaps, mismatch, ungrouped)
	return ps, health, nil
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func absf(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// freeVegName returns path, or "name (2).veg", "name (3).veg"... when taken:
// an earlier project is never overwritten.
func freeVegName(path string) string {
	if _, err := os.Stat(path); err != nil {
		return path
	}
	stem := strings.TrimSuffix(path, filepath.Ext(path))
	for n := 2; ; n++ {
		cand := fmt.Sprintf("%s (%d).veg", stem, n)
		if _, err := os.Stat(cand); err != nil {
			return cand
		}
	}
}

// mark is one region (Len > 0) or marker (Len == 0) on the finished timeline.
type mark struct {
	At    float64 `json:"at"`
	Len   float64 `json:"len,omitempty"`
	Label string  `json:"label"`
}

// launchVegas starts VEGAS (or uses the open one - BECKY_VEGAS_PID picks which)
// and pins the rest of the run to it.
func launchVegas() error {
	r, err := vegas(6*time.Minute, "launch")
	if err != nil {
		return err
	}
	var st struct {
		PID int `json:"pid"`
	}
	if json.Unmarshal(r.Result, &st) == nil && st.PID > 0 {
		vegasPID = st.PID
	}
	return nil
}

// runJob runs one of becky's VEGAS scripts on a job file and returns its result
// line ("ok ..."), or why it failed.
func runJob(script, job, body string, timeout time.Duration) (string, error) {
	if err := os.WriteFile(job, []byte(body), 0o644); err != nil {
		return "", err
	}
	_ = os.Remove(job + ".result.txt")
	if d, err := runScriptWatched(timeout, "path="+filepath.Join(vegasScripts(), script), "job="+job); err != nil || d != "" {
		return "", fmt.Errorf("%s failed: %v %s", script, err, d)
	}
	res, _ := os.ReadFile(job + ".result.txt")
	if !strings.HasPrefix(string(res), "ok") {
		return "", fmt.Errorf("%s did not finish: %s", script, strings.TrimSpace(string(res)))
	}
	return strings.TrimSpace(string(res)), nil
}

// buildInVegas assembles the edit from the final pieces and returns the
// timeline VEGAS actually made.
func buildInVegas(media, work, veg string, pieces []span, fps float64, logf func(string, ...any)) ([]piece, string, error) {
	logf("VEGAS: starting it (or using the open one)...")
	if err := launchVegas(); err != nil {
		return nil, "", err
	}
	if _, err := vegas(2*time.Minute, "new_project"); err != nil {
		return nil, "", err
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "media\t%s\n", media)
	for _, p := range pieces {
		fmt.Fprintf(&sb, "range\t%d\t%d\n", int64(math.Round(p.A*fps)), int64(math.Round(p.B*fps)))
	}
	logf("VEGAS: placing %d kept pieces...", len(pieces))
	res, err := runJob("BeckyKeepList.cs", filepath.Join(work, "vegas-job.txt"), sb.String(), 5*time.Minute)
	if err != nil {
		return nil, "", err
	}
	logf("  %s", res)
	if _, err := vegas(2*time.Minute, "save", "path="+veg); err != nil {
		return nil, "", err
	}
	ps, health, err := readTimeline()
	if err != nil {
		return nil, "", err
	}
	logf("  timeline: %s", health)
	return ps, health, nil
}

// markJob is BeckyMarks.cs's job file: every position in whole frames of the
// timeline, so a region edge sits exactly where an event edge can (the pieces
// are placed with Timecode.FromFrames too).
func markJob(ms []mark, fps float64) string {
	clean := strings.NewReplacer("\t", " ", "\r", " ", "\n", " ")
	var sb strings.Builder
	for _, m := range ms {
		f0 := int64(math.Round(m.At * fps))
		if m.Len > 0 {
			n := max(int64(math.Round((m.At+m.Len)*fps))-f0, 1)
			fmt.Fprintf(&sb, "region\t%d\t%d\t%s\n", f0, n, clean.Replace(m.Label))
		} else {
			fmt.Fprintf(&sb, "marker\t%d\t%s\n", f0, clean.Replace(m.Label))
		}
	}
	return sb.String()
}

// addMarks puts the regions and markers on the timeline, then saves.
func addMarks(ms []mark, fps float64, work string, logf func(string, ...any)) error {
	if len(ms) > 0 {
		res, err := runJob("BeckyMarks.cs", filepath.Join(work, "vegas-marks.txt"), markJob(ms, fps), 5*time.Minute)
		if err != nil {
			return err
		}
		logf("VEGAS: %s (every edge on a frame); saving...", res)
	}
	_, err := vegas(2*time.Minute, "save")
	return err
}
