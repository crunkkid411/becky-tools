package habits

// report.go renders the store for a NON-DEVELOPER (CLAUDE.md: Jordan is not a
// developer). It produces the plain-language "here's what becky has learned, and
// here's what it's still weighing" view, plus a machine Report for --json. All
// ordering comes from Store.Keys()/Learned()/Candidates(), so the text and the
// JSON are deterministic. Nothing here mutates the store.

import (
	"fmt"
	"strings"
)

// Report is the machine-readable summary emitted by `becky-habits show --json`.
// LearnedHabits are the corroborated defaults; Candidates are fixes seen once,
// still below the corroboration threshold. SchemaVersion ties it to the on-disk
// contract; MinEvidence is echoed so a consumer knows the threshold in force.
type Report struct {
	Tool          string  `json:"tool"`
	SchemaVersion int     `json:"schemaVersion"`
	MinEvidence   int     `json:"minEvidence"`
	Learned       []Habit `json:"learned"`
	Candidates    []Habit `json:"candidates"`
	Note          string  `json:"note,omitempty"`
}

// BuildReport assembles the JSON report from the store.
func (s *Store) BuildReport() Report {
	return Report{
		Tool:          "becky-habits",
		SchemaVersion: s.version(),
		MinEvidence:   MinEvidence,
		Learned:       s.Learned(),
		Candidates:    s.Candidates(),
	}
}

// Describe returns the plain-language report. It states the corroborated defaults
// as conclusions ("becky will default kick gain_db to -7") and lists candidates
// as still-weighing ("seen once — not a default yet"), mirroring becky's
// corroborate-then-conclude voice: don't hedge a learned default, don't promote a
// lone fix.
func (s *Store) Describe() string {
	learned, cands := s.Learned(), s.Candidates()
	if len(learned) == 0 && len(cands) == 0 {
		return "becky hasn't learned any habits yet — feed it a corrections log with `becky-habits observe <file>`."
	}
	var b strings.Builder
	b.WriteString("becky-habits — what becky has learned from your corrections\n")
	b.WriteString(strings.Repeat("=", 60) + "\n\n")

	b.WriteString(fmt.Sprintf("LEARNED defaults (corroborated >= %d times):\n", MinEvidence))
	if len(learned) == 0 {
		b.WriteString("  (none yet — keep correcting and a repeated fix becomes a default)\n")
	}
	for _, h := range learned {
		b.WriteString("  - " + describeLearned(h) + "\n")
	}

	b.WriteString("\nstill a CANDIDATE (seen once — not a default yet):\n")
	if len(cands) == 0 {
		b.WriteString("  (none)\n")
	}
	for _, h := range cands {
		b.WriteString("  - " + describeCandidate(h) + "\n")
	}
	return b.String()
}

// describeLearned phrases one corroborated default as a conclusion.
func describeLearned(h Habit) string {
	s := fmt.Sprintf("%s %s → defaults to %q (seen %dx)", h.Scope, h.Field, h.Default, h.Evidence)
	if len(h.Sources) > 0 {
		s += " from " + strings.Join(h.Sources, ", ")
	}
	return s
}

// describeCandidate phrases an under-threshold fix as still-weighing.
func describeCandidate(h Habit) string {
	return fmt.Sprintf("%s %s → %q so far (seen %dx, need %d)",
		h.Scope, h.Field, h.Default, h.Evidence, MinEvidence)
}
