package main

// questions.go is the engine half of the Becky Review HUMAN-REVIEW Q&A panel. A
// forensic hit-list can carry a "?" question per clip; becky-hits groups those into a
// `<reel>.questions.json` sidecar next to the reel. Becky Review pre-loads it (via
// BECKY_REVIEW_QUESTIONS, mirroring BECKY_REVIEW_REEL) and shows each question as a
// clickable card in the right panel — the reviewer clicks it, watches the tied clips on
// the timeline, and types the answer. Answers are appended to a durable
// `_forensic_answers.json` beside the questions file; an agent later routes them back
// into the wiki (the GUI never edits arbitrary markdown itself).
//
// Read-only over evidence: it reads the small questions JSON and writes only the answers
// JSON. Degrade-never-crash: a missing/bad questions file just yields no cards.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ReviewQuestion is one human-review question + the timeline clip IDs it's about.
// Matches the `<reel>.questions.json` sidecar becky-hits writes.
type ReviewQuestion struct {
	ID       string   `json:"id"`
	Question string   `json:"question"`
	ClipIDs  []string `json:"clip_ids"`
	Answered bool     `json:"answered,omitempty"`
	Answer   string   `json:"answer,omitempty"`
}

// questionsFile is the sidecar shape becky-hits emits.
type questionsFile struct {
	Questions []ReviewQuestion `json:"questions"`
}

// reviewAnswer is one saved answer (appended to _forensic_answers.json).
type reviewAnswer struct {
	ID         string `json:"id"`
	Question   string `json:"question"`
	Answer     string `json:"answer"`
	AnsweredAt string `json:"answered_at"`
}

// LoadQuestions reads a `<reel>.questions.json` sidecar into the App (call once at
// startup). A bad/absent path leaves the App with no questions (degrade, never crash).
func (a *App) LoadQuestions(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var qf questionsFile
	if err := json.Unmarshal(b, &qf); err != nil {
		return err
	}
	a.mu.Lock()
	a.questions = qf.Questions
	a.questionsPath = path
	// Fold any answers already saved this run back onto the cards.
	for _, ans := range a.loadAnswersLocked() {
		for i := range a.questions {
			if a.questions[i].ID == ans.ID {
				a.questions[i].Answered = true
				a.questions[i].Answer = ans.Answer
			}
		}
	}
	a.mu.Unlock()
	return nil
}

// Questions returns the loaded human-review questions (empty when none).
func (a *App) Questions() []ReviewQuestion {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.questionsLocked()
}

// SaveAnswer records an answer to a question: appends it to _forensic_answers.json
// (the durable record an agent routes into the wiki) and marks the in-memory card
// answered. Returns the updated question list so the UI can re-render.
func (a *App) SaveAnswer(id, question, answer string) ([]ReviewQuestion, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.questions {
		if a.questions[i].ID == id {
			a.questions[i].Answered = true
			a.questions[i].Answer = answer
			if question == "" {
				question = a.questions[i].Question
			}
		}
	}
	rec := reviewAnswer{ID: id, Question: question, Answer: answer, AnsweredAt: time.Now().UTC().Format(time.RFC3339)}
	all := append(a.loadAnswersLocked(), rec)
	if err := a.writeAnswersLocked(all); err != nil {
		return a.questionsLocked(), err
	}
	return a.questionsLocked(), nil
}

func (a *App) questionsLocked() []ReviewQuestion {
	out := make([]ReviewQuestion, len(a.questions))
	copy(out, a.questions)
	return out
}

// answersPath is _forensic_answers.json beside the questions file (or the work dir).
func (a *App) answersPath() string {
	dir := a.workDir
	if a.questionsPath != "" {
		dir = filepath.Dir(a.questionsPath)
	}
	return filepath.Join(dir, "_forensic_answers.json")
}

func (a *App) loadAnswersLocked() []reviewAnswer {
	b, err := os.ReadFile(a.answersPath())
	if err != nil {
		return nil
	}
	var out []reviewAnswer
	if json.Unmarshal(b, &out) != nil {
		return nil
	}
	return out
}

func (a *App) writeAnswersLocked(all []reviewAnswer) error {
	b, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	p := a.answersPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o644)
}
