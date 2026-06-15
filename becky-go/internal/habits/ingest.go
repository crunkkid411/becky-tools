package habits

// ingest.go parses a corrections file into the GENERIC []CorrectionRecord this
// learner consumes. becky tools emit corrections in slightly different shapes
// (internal/dawmodel/corrections.go uses {kind, clip, auto, fixed, genre/bpm};
// internal/hum/hum.go uses {field, auto, corrected, context}). Rather than couple
// the learner to any one tool, the CLI accepts the generic record directly, and
// this loose decoder also maps the two common legacy field-names (kind→scope,
// corrected→fixed) so existing logs feed in without a rewrite. Degrade, never
// crash: a malformed file is a wrapped error, not a panic.

import (
	"encoding/json"
	"fmt"
)

// rawRecord is a permissive view over a single correction row. It accepts the
// canonical {scope, field, auto, fixed} AND the two legacy aliases so a log from
// becky-daw (kind/fixed) or becky-hum (field/corrected) ingests unchanged.
type rawRecord struct {
	Scope     string            `json:"scope"`
	Kind      string            `json:"kind"` // dawmodel alias for scope
	Field     string            `json:"field"`
	Auto      json.RawMessage   `json:"auto"`      // text OR number — normalised to text
	Fixed     json.RawMessage   `json:"fixed"`     // dawmodel: text
	Corrected json.RawMessage   `json:"corrected"` // hum alias for fixed (may be null)
	Context   map[string]string `json:"context,omitempty"`
}

// ingestFile is the permissive envelope: a bare JSON array of rows, OR an object
// with a "corrections" array (so a tool's full result JSON can be piped in too).
type ingestFile struct {
	Corrections []rawRecord `json:"corrections"`
}

// ParseRecords decodes a corrections file body into generic records. It accepts a
// bare array or a {"corrections": [...]} object. Rows missing a learnable
// scope/field/fixed are kept as-is (Observe skips them and the CLI counts them);
// only structurally invalid JSON is an error.
func ParseRecords(body []byte) ([]CorrectionRecord, error) {
	var rows []rawRecord
	if err := json.Unmarshal(body, &rows); err != nil {
		var env ingestFile
		if err2 := json.Unmarshal(body, &env); err2 != nil {
			return nil, fmt.Errorf("parse corrections: %w", err)
		}
		rows = env.Corrections
	}
	out := make([]CorrectionRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.normalise())
	}
	return out, nil
}

// normalise collapses a permissive raw row into the canonical record, resolving
// the kind→scope and corrected→fixed aliases and rendering numeric values as text
// (so a JSON number 5 and the string "5" land in the same habit bucket).
func (r rawRecord) normalise() CorrectionRecord {
	scope := r.Scope
	if scope == "" {
		scope = r.Kind
	}
	fixed := jsonScalarText(r.Fixed)
	if fixed == "" {
		fixed = jsonScalarText(r.Corrected)
	}
	return CorrectionRecord{
		Scope:   scope,
		Field:   r.Field,
		Auto:    jsonScalarText(r.Auto),
		Fixed:   fixed,
		Context: r.Context,
	}
}

// jsonScalarText renders a JSON scalar (string OR number) as text and treats null
// / absent as empty. A quoted string decodes to its value; anything else (number,
// bool) is returned as its raw token so numeric corrections compare as text.
func jsonScalarText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}
