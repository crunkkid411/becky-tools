package whois

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"becky-go/internal/config"
)

// The same video + guess is asked once; an answer takes it off the open list.
func TestAskOnceAndPending(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BECKY_WHOIS_DIR", dir)
	frame := filepath.Join(dir, "src.jpg")
	if err := os.WriteFile(frame, []byte("jpg"), 0o644); err != nil {
		t.Fatal(err)
	}
	q := Question{Video: `C:\v\clip.mp4`, At: 12, Guess: "Hair Jordan", Frame: frame, Box: []float64{1, 2, 3, 4}}
	id, added, err := Ask(q)
	if err != nil || !added || id == "" {
		t.Fatalf("first ask: %v %v %v", id, added, err)
	}
	id2, added, err := Ask(q)
	if err != nil || added || id2 != id {
		t.Fatalf("second ask of the same video + guess must not add: %v %v %v", id2, added, err)
	}
	if _, added, _ := Ask(Question{Video: `C:\v\clip.mp4`, Guess: "John Anthony Clancy"}); !added {
		t.Fatal("a different guess is a different question")
	}
	qs, _ := Questions()
	if len(qs) != 2 || qs[0].Frame == frame {
		t.Fatalf("questions %+v (the picture must be copied into the queue folder)", qs)
	}
	if _, err := os.Stat(qs[0].Frame); err != nil {
		t.Fatalf("copied picture missing: %v", err)
	}
	p := Pending(qs, []Answer{{ID: id, Answer: "Hair Jordan"}})
	if len(p) != 1 || p[0].Guess != "John Anthony Clancy" {
		t.Fatalf("pending %+v", p)
	}
}

// Nothing open -> no window, and BECKY_NO_POPUP keeps tests and night runs quiet.
func TestShowQuietWhenNothingOpen(t *testing.T) {
	t.Setenv("BECKY_WHOIS_DIR", t.TempDir())
	if err := Show(config.Config{}); err != nil {
		t.Fatal(err)
	}
}

// A tool reads Jordan's answer (or that it is still open) before running its slow checks.
func TestLookup(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BECKY_WHOIS_DIR", dir)
	if _, answered, waiting, _ := Lookup(`C:\v\a.mp4`, "Hair Jordan"); answered || waiting {
		t.Fatal("never asked must be neither")
	}
	id, _, _ := Ask(Question{Video: `C:\v\a.mp4`, Guess: "Hair Jordan"})
	if _, _, waiting, _ := Lookup(`c:\V\a.mp4`, "Hair Jordan"); !waiting {
		t.Fatal("an open question is waiting (paths compare without case)")
	}
	f, _ := os.OpenFile(filepath.Join(dir, "answers.jsonl"), os.O_CREATE|os.O_WRONLY, 0o644)
	f.WriteString(`{"id":"` + id + `","answer":"Hair Jordan","answered_at":"2026-10-11T00:21:36"}` + "\n")
	f.Close()
	if a, answered, _, _ := Lookup(`C:\v\a.mp4`, "Hair Jordan"); !answered || a.Answer != "Hair Jordan" {
		t.Fatalf("answer not found: %+v", a)
	}
}

// "Ask me later" means later: inside the pause nothing opens. With no Python configured,
// an attempt to open the window is an error, which is how this test sees an attempt.
func TestShowRespectsAskMeLater(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BECKY_WHOIS_DIR", dir)
	if _, _, err := Ask(Question{Video: `C:\v\a.mp4`, Guess: "Hair Jordan"}); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour).Format(time.RFC3339)
	if err := os.WriteFile(filepath.Join(dir, "snooze_until"), []byte(later), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Show(config.Config{}); err != nil {
		t.Fatalf("inside the pause nothing may open: %v", err)
	}
	if err := ShowNow(config.Config{}); err == nil {
		t.Fatal("becky-whois (asked on purpose) must still try to open it")
	}
	os.Remove(filepath.Join(dir, "snooze_until"))
	if err := Show(config.Config{}); err == nil {
		t.Fatal("after the pause an open question must try to open the window")
	}
}
