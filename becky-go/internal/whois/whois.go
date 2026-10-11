// Package whois is how becky asks Jordan "who is this?" - and the ONLY way it does.
//
// Jordan, 2026-10-11: "Yes, it can pop up and ask me who someone is. We've tried this in
// the past and it was too annoying to use, so make sure it's actually functional this
// time and not counterintuitive." The old becky-name was a terminal program he had to
// start with flags and type names into; questions also used to land in folders he never
// opens. So:
//   - a tool calls Ask only AFTER all its own checks ran (becky-identify: frame-by-frame
//     scrub, Gemma face + whole person, Falcon, reverse image search, voice);
//   - the question goes into ONE queue, and ONE window (whois_popup.py) shows every open
//     question with the pictures; it never opens twice and never stacks;
//   - the answer is a click on a full name (no typing unless it is a new person), and it
//     teaches becky: the face picture is enrolled into that person's face-prints;
//   - "Ask me later" means no popup for 2 hours; the same video + guess is never asked twice.
//
// Files live in Dir() (~/.becky/whois): questions.jsonl, answers.jsonl, pictures, and the
// popup's own alive / snooze stamps. Jordan never has to open that folder.
package whois

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"becky-go/internal/config"
	"becky-go/internal/pyhelpers"
)

// Person is one name the popup offers. Dir is the face-prints folder an answer enrolls into.
type Person struct {
	Name    string   `json:"name"`
	Aliases []string `json:"aliases,omitempty"`
	Dir     string   `json:"dir"`
	HasFace bool     `json:"has_face"` // already has face-prints: gets a big button, the rest are found by typing
}

// Question is one "who is this?" for Jordan.
type Question struct {
	ID      string    `json:"id"`
	Video   string    `json:"video"`
	At      float64   `json:"at"`                // seconds into the video
	Guess   string    `json:"guess"`             // becky's best guess, full name ("" = no idea)
	Checked string    `json:"checked"`           // plain words: what becky already tried
	Frame   string    `json:"frame"`             // the clearest frame, copied into Dir()
	Box     []float64 `json:"box"`               // the face in that frame [x1,y1,x2,y2]
	Speaker string    `json:"speaker,omitempty"` // set = "who is talking here?" about this speaker label (no face box)
	KB      string    `json:"kb"`                // knowledge base an answer is enrolled into
	People  []Person  `json:"people"`
	Asked   string    `json:"asked"`
}

// Answer is one line of answers.jsonl, written by the popup.
type Answer struct {
	ID         string  `json:"id"`
	Video      string  `json:"video"`
	At         float64 `json:"at"`
	Guess      string  `json:"guess"`
	Answer     string  `json:"answer"` // a full name, or "unknown"
	Enrolled   string  `json:"enrolled,omitempty"`
	AnsweredAt string  `json:"answered_at"`
}

// Dir is where the queue lives (BECKY_WHOIS_DIR overrides it, for tests).
func Dir() string {
	if d := os.Getenv("BECKY_WHOIS_DIR"); d != "" {
		return d
	}
	return filepath.Join(filepath.Dir(config.Path()), "whois")
}

// Ask queues q (copying its frame into Dir()) unless the same video + guess was already
// asked. It returns the question id, and whether it was newly added.
func Ask(q Question) (string, bool, error) {
	dir := Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", false, err
	}
	qs, err := Questions()
	if err != nil {
		return "", false, err
	}
	if old, ok := asked(qs, q.Video, q.Guess, q.Speaker); ok {
		return old.ID, false, nil
	}
	q.ID = fmt.Sprintf("q%d", time.Now().UnixNano())
	q.Asked = time.Now().Format(time.RFC3339)
	if q.Frame != "" {
		dst := filepath.Join(dir, q.ID+filepath.Ext(q.Frame))
		if err := copyFile(q.Frame, dst); err != nil {
			return "", false, fmt.Errorf("save the picture for the question: %w", err)
		}
		q.Frame = dst
	}
	// ponytail: append-only JSONL, no lock; two runs asking about the same video in the
	// same millisecond could both add it. Add a lock file if that ever shows up.
	b, _ := json.Marshal(q)
	f, err := os.OpenFile(filepath.Join(dir, "questions.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return "", false, err
	}
	return q.ID, true, nil
}

// Lookup tells a tool what already happened to "is this <guess> in <video>?": answered
// (a is Jordan's answer), still waiting, or never asked. Read it BEFORE the slow checks.
func Lookup(video, guess string) (a Answer, answered, waiting bool, err error) {
	return LookupSpeaker(video, guess, "")
}

// LookupSpeaker is Lookup for a "who is talking here?" question about one speaker label.
func LookupSpeaker(video, guess, speaker string) (a Answer, answered, waiting bool, err error) {
	qs, err := Questions()
	if err != nil {
		return Answer{}, false, false, err
	}
	q, ok := asked(qs, video, guess, speaker)
	if !ok {
		return Answer{}, false, false, nil
	}
	as, err := Answers()
	if err != nil {
		return Answer{}, false, false, err
	}
	for _, a := range as {
		if a.ID == q.ID {
			return a, true, false, nil
		}
	}
	return Answer{}, false, true, nil
}

// asked finds an earlier question about the same video with the same guess (pure).
func asked(qs []Question, video, guess, speaker string) (Question, bool) {
	for _, q := range qs {
		if strings.EqualFold(filepath.Clean(q.Video), filepath.Clean(video)) && q.Guess == guess && q.Speaker == speaker {
			return q, true
		}
	}
	return Question{}, false
}

// Questions reads every question ever asked.
func Questions() ([]Question, error) {
	var qs []Question
	err := readLines(filepath.Join(Dir(), "questions.jsonl"), func(b []byte) {
		var q Question
		if json.Unmarshal(b, &q) == nil && q.ID != "" {
			qs = append(qs, q)
		}
	})
	return qs, err
}

// Answers reads every answer Jordan gave.
func Answers() ([]Answer, error) {
	var as []Answer
	err := readLines(filepath.Join(Dir(), "answers.jsonl"), func(b []byte) {
		var a Answer
		if json.Unmarshal(b, &a) == nil && a.ID != "" {
			as = append(as, a)
		}
	})
	return as, err
}

// Pending is every question without an answer (pure).
func Pending(qs []Question, as []Answer) []Question {
	done := map[string]bool{}
	for _, a := range as {
		done[a.ID] = true
	}
	var out []Question
	for _, q := range qs {
		if !done[q.ID] {
			out = append(out, q)
		}
	}
	return out
}

// Show opens the popup when there is an open question, unless it is already open,
// Jordan said "Ask me later" less than 2 hours ago, or BECKY_NO_POPUP is set.
// It never waits for the window: the calling tool finishes and returns its result.
func Show(cfg config.Config) error { return show(cfg, true) }

// ShowNow opens the window even inside an "Ask me later" pause (someone asked for it).
func ShowNow(cfg config.Config) error { return show(cfg, false) }

func show(cfg config.Config, respectSnooze bool) error {
	qs, err := Questions()
	if err != nil {
		return err
	}
	as, err := Answers()
	if err != nil {
		return err
	}
	if len(Pending(qs, as)) == 0 || os.Getenv("BECKY_NO_POPUP") != "" {
		return nil
	}
	dir := Dir()
	if fresh(filepath.Join(dir, "popup.alive"), 8*time.Second) { // already open: it picks the new question up itself
		return nil
	}
	if until, err := os.ReadFile(filepath.Join(dir, "snooze_until")); err == nil && respectSnooze {
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(until))); err == nil && time.Now().Before(t) {
			return nil
		}
	}
	script, err := pyhelpers.Materialize("whois_popup.py", pyhelpers.WhoisPopup)
	if err != nil {
		return err
	}
	cmd := exec.Command(windowPython(cfg), script, "--dir", dir, "--ffplay", sibling(cfg.FFmpeg, "ffplay"))
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open the who-is-this window: %w", err)
	}
	return cmd.Process.Release()
}

// windowPython is pythonw.exe next to the face Python (anaconda: has tkinter + PIL), so no
// black console window flashes up behind the popup.
func windowPython(cfg config.Config) string {
	py := cfg.FacePython
	if py == "" {
		py = cfg.Python
	}
	if w := sibling(py, "pythonw"); w != "" {
		return w
	}
	return py
}

// sibling is the exe called name in the same folder as path ("" when it is not there).
func sibling(path, name string) string {
	if path == "" {
		return ""
	}
	p := filepath.Join(filepath.Dir(path), name+filepath.Ext(path))
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

func fresh(path string, within time.Duration) bool {
	st, err := os.Stat(path)
	return err == nil && time.Since(st.ModTime()) < within
}

func readLines(path string, each func([]byte)) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		if b := sc.Bytes(); len(b) > 0 {
			each(b)
		}
	}
	return sc.Err()
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
