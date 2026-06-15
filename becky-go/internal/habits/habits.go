// Package habits closes becky's preference-learning loop. becky's tools already
// emit a corrections log — every time an AUTO-generated value is overridden by
// Jordan's MANUAL fix, the op records {scope, field, auto, fixed, context}
// (see internal/dawmodel/corrections.go and internal/hum/hum.go). This package
// turns a REPEATED correction into a learned DEFAULT: "you always pull the kick
// to -7 → that becomes the default", so the 95%-deterministic part gets more
// "you" over time without a preferences menu.
//
// It is a faithful, idiomatic Go port of dawbase's MIT-licensed HabitStore
// (X:/AI-2/dawbase/src/habits.cpp), generalised to a tool-agnostic correction
// record so ANY becky tool (hum/vox/daw/canvas) can feed it.
//
// Invariants (CLAUDE.md §1-2):
//   - Corroborate, then CONCLUDE. A one-off fix is a CANDIDATE, not a default;
//     a value only becomes the learned default once it recurs >= the threshold
//     (>= 2 corroborating observations). A flood of maybes is tool failure.
//   - Deterministic. Same records in => byte-identical habits.json out. Output
//     never depends on Go map iteration order — keys are sorted everywhere.
//   - Degrade, never crash. Bad/missing input yields a typed result + note via
//     wrapped errors, never a panic.
package habits

import "sort"

// MinEvidence is how many times a given fixed value must recur (counting the
// observation that introduces it) before it is promoted from CANDIDATE to the
// learned DEFAULT. Mirrors dawbase's kMinEvidence; the conservative floor that
// keeps a lone correction from hijacking a default.
const MinEvidence = 2

// SchemaVersion is the on-disk habits.json contract version.
const SchemaVersion = 1

// CorrectionRecord is the GENERIC override one becky tool emits and this learner
// consumes. It is intentionally tool-agnostic: any tool that overrides an AUTO
// value with a MANUAL fix maps its own correction onto these fields.
//   - Scope : what the habit is keyed under (a track kind like "kick", a clip,
//     a genre — the bucket a preference belongs to).
//   - Field : which knob was overridden ("gain_db", "note.midi", "quantize").
//   - Auto  : becky's generated value, as text so any param type fits.
//   - Fixed : Jordan's corrected value, as text.
//   - Context: optional musical/forensic facts (genre, bpm, source tool) carried
//     for provenance; it does NOT change the {scope, field} key.
type CorrectionRecord struct {
	Scope   string            `json:"scope"`
	Field   string            `json:"field"`
	Auto    string            `json:"auto"`
	Fixed   string            `json:"fixed"`
	Context map[string]string `json:"context,omitempty"`
}

// Habit is what becky has learned (or is still weighing) for one {scope, field}.
// Counts maps each observed fixed value to how many times it recurred. Default is
// the currently-winning value and Evidence its count, but Default is only the
// LEARNED default when Learned is true (Evidence >= MinEvidence). Sources records
// the distinct context provenance (e.g. tool names) that fed this habit.
type Habit struct {
	Scope    string         `json:"scope"`
	Field    string         `json:"field"`
	Counts   map[string]int `json:"counts"`
	Default  string         `json:"default"`
	Evidence int            `json:"evidence"`
	Learned  bool           `json:"learned"`
	Sources  []string       `json:"sources,omitempty"`
}

// Store is the deterministic habit store keyed on "{scope}\x1f{field}". It is an
// in-memory model; persistence lives in store.go. The map is never iterated for
// OUTPUT — every emitter sorts via Keys() first.
type Store struct {
	Version int              `json:"version"`
	Habits  map[string]Habit `json:"habits"`
}

// NewStore returns an empty store at the current schema version.
func NewStore() *Store {
	return &Store{Version: SchemaVersion, Habits: map[string]Habit{}}
}

// key joins scope+field with a unit-separator that cannot appear in normal text,
// so two different {scope, field} pairs can never collide into one bucket.
func key(scope, field string) string { return scope + "\x1f" + field }

// Observe ingests one correction. The fixed value's tally is incremented; once it
// reaches MinEvidence the {scope, field} default flips to it (corroborate, then
// conclude). A previously-learned default is NOT silently overwritten by a single
// new value — the winner is always the most-corroborated fixed value, ties broken
// deterministically by string order. A record with an empty Fixed is ignored
// (nothing to learn) and reported via the returned bool.
func (s *Store) Observe(r CorrectionRecord) bool {
	if s.Habits == nil {
		s.Habits = map[string]Habit{}
	}
	if r.Scope == "" || r.Field == "" || r.Fixed == "" {
		return false
	}
	k := key(r.Scope, r.Field)
	h, ok := s.Habits[k]
	if !ok {
		h = Habit{Scope: r.Scope, Field: r.Field, Counts: map[string]int{}}
	}
	if h.Counts == nil {
		h.Counts = map[string]int{}
	}
	h.Counts[r.Fixed]++
	h.Sources = mergeSource(h.Sources, r.Context["tool"])
	recompute(&h)
	s.Habits[k] = h
	return true
}

// ObserveAll ingests a batch, returning how many records were learnable (had a
// non-empty scope/field/fixed). The difference from len(records) is the number of
// skipped/degraded rows the caller can surface.
func (s *Store) ObserveAll(records []CorrectionRecord) int {
	learned := 0
	for _, r := range records {
		if s.Observe(r) {
			learned++
		}
	}
	return learned
}

// recompute picks the winning fixed value (most corroborated; ties → string
// order) and marks the habit Learned only once that winner has >= MinEvidence
// corroborating observations. This is the corroborate-then-conclude gate.
func recompute(h *Habit) {
	best, bestN := "", 0
	for _, v := range sortedKeys(h.Counts) {
		if n := h.Counts[v]; n > bestN {
			best, bestN = v, n
		}
	}
	h.Default, h.Evidence = best, bestN
	h.Learned = bestN >= MinEvidence
}

// Apply returns the learned default for {scope, field} when one exists, else the
// caller's fresh value unchanged. A candidate (seen once, below threshold) is NOT
// applied — recall is for detection, naming is for the corroborated. This mirrors
// dawbase HabitStore::apply but generalised to a single text value.
func (s *Store) Apply(scope, field, fresh string) string {
	if h, ok := s.Habits[key(scope, field)]; ok && h.Learned {
		return h.Default
	}
	return fresh
}

// Lookup returns the habit for {scope, field} and whether it exists at all
// (learned OR still a candidate).
func (s *Store) Lookup(scope, field string) (Habit, bool) {
	h, ok := s.Habits[key(scope, field)]
	return h, ok
}

// Keys returns the store's habit keys in a stable, deterministic order (sorted by
// scope then field). Every emitter MUST iterate via this — never range over the
// map directly — so reports and JSON are byte-identical run to run.
func (s *Store) Keys() []string {
	ks := make([]string, 0, len(s.Habits))
	for k := range s.Habits {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool {
		a, b := s.Habits[ks[i]], s.Habits[ks[j]]
		if a.Scope != b.Scope {
			return a.Scope < b.Scope
		}
		return a.Field < b.Field
	})
	return ks
}

// Learned returns the habits that have crossed the threshold (sorted order).
func (s *Store) Learned() []Habit { return s.filter(true) }

// Candidates returns the habits still gathering evidence (sorted order).
func (s *Store) Candidates() []Habit { return s.filter(false) }

func (s *Store) filter(wantLearned bool) []Habit {
	var out []Habit
	for _, k := range s.Keys() {
		if h := s.Habits[k]; h.Learned == wantLearned {
			out = append(out, h)
		}
	}
	return out
}

// sortedKeys returns a map's keys sorted, so any reduction over a map is
// order-independent (determinism).
func sortedKeys(m map[string]int) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// mergeSource appends src to the provenance list if non-empty and not already
// present, keeping the list sorted and deduped (deterministic provenance).
func mergeSource(srcs []string, src string) []string {
	if src == "" {
		return srcs
	}
	for _, s := range srcs {
		if s == src {
			return srcs
		}
	}
	srcs = append(srcs, src)
	sort.Strings(srcs)
	return srcs
}
