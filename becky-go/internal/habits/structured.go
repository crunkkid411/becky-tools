package habits

// structured.go extends the learner from SCALAR defaults ("kick gain_db → -7")
// to STRUCTURED ones — a small JSON blob that recurs, e.g. Jordan's "usual drum
// bus FX chain" or "always sidechain the bass to the kick at 50%". A structured
// preference is NOT a parallel system: it rides the EXACT same machinery as a
// scalar one. A structured correction is just a CorrectionRecord whose Fixed (and
// Auto) value is a JSON object/array instead of a plain scalar.
//
// The one thing structured values need that scalars don't is CANONICALISATION:
// two blobs that are equal but written with different key order
// ({"from":"kick","to":"bus.bass"} vs {"to":"bus.bass","from":"kick"}) MUST count
// as the SAME value so the recurrence/threshold logic (corroborate, then
// conclude) treats them as corroborating evidence. canonicalValue() normalises a
// JSON blob to a stable, sorted-key form before it is ever counted; scalars pass
// through untouched, so the existing scalar path is byte-for-byte unaffected.
//
// Determinism (CLAUDE.md §2): canonicalisation sorts object keys recursively, so
// the same logical preference always produces the same canonical string and the
// same on-disk store.

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
)

// canonicalValue returns the canonical, count-stable form of a correction value.
//
//   - If s parses as a JSON OBJECT or ARRAY, it is re-encoded with object keys
//     sorted recursively (and no insignificant whitespace), so two logically
//     equal blobs collapse to one bucket regardless of key order or spacing.
//   - Anything else — a plain scalar like "-7", a JSON string, a number token, or
//     a non-JSON string — is returned UNCHANGED. This is what keeps the scalar
//     learning path identical to before: "-7" canonicalises to "-7".
//
// Degrade, never crash: a value that looks like JSON but doesn't parse is returned
// verbatim rather than erroring.
func canonicalValue(s string) string {
	trimmed := strings.TrimSpace(s)
	if !looksStructured(trimmed) {
		return s
	}
	var v any
	dec := json.NewDecoder(strings.NewReader(trimmed))
	dec.UseNumber() // keep numbers as their literal text so 1 vs 1.0 stay distinct
	if err := dec.Decode(&v); err != nil {
		return s // not valid JSON after all — leave it alone (degrade)
	}
	c, err := canonicalEncode(v)
	if err != nil {
		return s
	}
	return c
}

// looksStructured reports whether s begins with a JSON object or array opener.
// A plain scalar (number, quoted string, bare token) is never "structured", so it
// skips canonicalisation entirely and the scalar path is untouched.
func looksStructured(s string) bool {
	return strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[")
}

// canonicalEncode marshals a decoded JSON value with object keys sorted
// recursively. encoding/json already sorts map[string]any keys lexically, so the
// work is decoding into ordered Go containers and re-encoding compactly.
func canonicalEncode(v any) (string, error) {
	normalised := sortValue(v)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(normalised); err != nil {
		return "", err
	}
	// Encoder appends a newline; trim it for a stable, single-line canonical form.
	return strings.TrimRight(buf.String(), "\n"), nil
}

// sortValue walks a decoded JSON value and returns an equivalent value whose maps
// will marshal with sorted keys. encoding/json sorts map keys on marshal, so we
// only need to make sure nested maps stay map[string]any (they already are after
// a generic decode) — but we rebuild explicitly so the recursion is obvious and
// arrays are normalised element-by-element too.
func sortValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		// json.Marshal sorts these keys; sorting here as well documents intent and
		// keeps behaviour stable if the encoder ever changed.
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out[k] = sortValue(t[k])
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i := range t {
			out[i] = sortValue(t[i])
		}
		return out
	default:
		return t
	}
}

// IsStructured reports whether a correction value is a structured (JSON
// object/array) preference rather than a scalar. Exposed so callers and reports
// can distinguish "his usual drum bus chain" from "kick gain → -7".
func IsStructured(value string) bool {
	return looksStructured(strings.TrimSpace(value))
}

// ApplyStructured is the structured-value counterpart to Apply: it returns the
// learned structured default for {scope, field} when one exists AND the habit is a
// structured one, plus true. Otherwise it returns fresh and false — a candidate
// (below MinEvidence) is NOT applied (corroborate, then conclude). The returned
// string is the canonical (sorted-key) JSON, so callers get a stable blob.
//
// Back-compat note: the scalar Apply(scope, field, fresh) is unchanged and still
// works for every habit; ApplyStructured simply adds a typed structured path and a
// found bool so a caller building "my usual drum bus" knows whether a real learned
// preference came back or just its own fresh fallback.
func (s *Store) ApplyStructured(scope, field, fresh string) (string, bool) {
	if h, ok := s.Habits[key(scope, field)]; ok && h.Learned && h.Structured {
		return h.Default, true
	}
	return fresh, false
}

// UsualPreference is one learned structured default for a scope — the answer to
// "set up my usual <scope>". Field is the knob ("chain", "sidechain"), Value is
// the canonical JSON blob, Evidence how many times it recurred.
type UsualPreference struct {
	Scope    string   `json:"scope"`
	Field    string   `json:"field"`
	Value    string   `json:"value"`
	Evidence int      `json:"evidence"`
	Sources  []string `json:"sources,omitempty"`
}

// Usual returns every LEARNED structured default under a scope (e.g. "bus.drums"
// or "routing"), in deterministic {scope, field} order. This is the "my usual X"
// recall API another tool calls to answer "set up my usual drum bus": it returns
// only corroborated (Learned) structured habits — candidates are withheld, in line
// with corroborate-then-conclude. An empty slice means nothing learned for that
// scope yet (a clean miss, not an error).
func (s *Store) Usual(scope string) []UsualPreference {
	var out []UsualPreference
	for _, k := range s.Keys() {
		h := s.Habits[k]
		if h.Scope != scope || !h.Learned || !h.Structured {
			continue
		}
		out = append(out, UsualPreference{
			Scope:    h.Scope,
			Field:    h.Field,
			Value:    h.Default,
			Evidence: h.Evidence,
			Sources:  h.Sources,
		})
	}
	return out
}

// UsualField returns the single learned structured default for an exact
// {scope, field} and whether it was found — the precise-lookup form of Usual when
// the caller already knows which knob it wants (e.g. routing/sidechain).
func (s *Store) UsualField(scope, field string) (UsualPreference, bool) {
	if h, ok := s.Habits[key(scope, field)]; ok && h.Learned && h.Structured {
		return UsualPreference{
			Scope:    h.Scope,
			Field:    h.Field,
			Value:    h.Default,
			Evidence: h.Evidence,
			Sources:  h.Sources,
		}, true
	}
	return UsualPreference{}, false
}

// StructuredLearned returns all learned structured habits across every scope, in
// deterministic order — used by the report to list "complex preferences" apart
// from the scalar defaults.
func (s *Store) StructuredLearned() []Habit {
	var out []Habit
	for _, k := range s.Keys() {
		if h := s.Habits[k]; h.Learned && h.Structured {
			out = append(out, h)
		}
	}
	return out
}

// ScalarLearned returns all learned SCALAR habits (the original kind), in
// deterministic order — the complement of StructuredLearned within Learned().
func (s *Store) ScalarLearned() []Habit {
	var out []Habit
	for _, k := range s.Keys() {
		if h := s.Habits[k]; h.Learned && !h.Structured {
			out = append(out, h)
		}
	}
	return out
}
