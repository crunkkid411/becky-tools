package habits

import "testing"

// TestParseRecords_bareArray decodes the canonical bare-array form.
func TestParseRecords_bareArray(t *testing.T) {
	body := []byte(`[
		{"scope":"kick","field":"gain_db","auto":"-3","fixed":"-7",
		 "context":{"tool":"becky-daw"}}
	]`)
	recs, err := ParseRecords(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d records want 1", len(recs))
	}
	r := recs[0]
	if r.Scope != "kick" || r.Field != "gain_db" || r.Fixed != "-7" || r.Context["tool"] != "becky-daw" {
		t.Errorf("unexpected record %+v", r)
	}
}

// TestParseRecords_envelope decodes the {"corrections":[...]} object form (a full
// tool result JSON piped in).
func TestParseRecords_envelope(t *testing.T) {
	body := []byte(`{"corrections":[{"scope":"snare","field":"velocity","fixed":"118"}]}`)
	recs, err := ParseRecords(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(recs) != 1 || recs[0].Scope != "snare" || recs[0].Fixed != "118" {
		t.Errorf("unexpected %+v", recs)
	}
}

// TestParseRecords_aliases maps the legacy dawmodel (kind) and hum (corrected)
// field names onto scope/fixed so existing logs ingest without a rewrite.
func TestParseRecords_aliases(t *testing.T) {
	body := []byte(`[
		{"kind":"kick","field":"gain_db","auto":"-3","fixed":"-7"},
		{"scope":"snare","field":"note.midi","auto":60,"corrected":62}
	]`)
	recs, err := ParseRecords(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if recs[0].Scope != "kick" {
		t.Errorf("kind alias not mapped to scope: %+v", recs[0])
	}
	// numeric auto/corrected normalise to text; corrected aliases to fixed.
	if recs[1].Scope != "snare" || recs[1].Auto != "60" || recs[1].Fixed != "62" {
		t.Errorf("hum-shape record not normalised: %+v", recs[1])
	}
}

// TestParseRecords_aliasedRecordsLearn confirms an aliased log actually flows
// through Observe → a learned default (end-to-end of the ingest contract).
func TestParseRecords_aliasedRecordsLearn(t *testing.T) {
	body := []byte(`[
		{"kind":"kick","field":"gain_db","fixed":"-7"},
		{"kind":"kick","field":"gain_db","fixed":"-7"}
	]`)
	recs, err := ParseRecords(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	s := NewStore()
	if n := s.ObserveAll(recs); n != 2 {
		t.Fatalf("learnable=%d want 2", n)
	}
	if got := s.Apply("kick", "gain_db", "0"); got != "-7" {
		t.Errorf("aliased records did not learn: Apply=%q want -7", got)
	}
}

// TestParseRecords_nullCorrectedSkipped confirms a hum row whose corrected is null
// (Jordan hasn't fixed it yet) normalises to an empty Fixed and is then skipped by
// Observe — there's nothing to learn from an un-overridden auto value.
func TestParseRecords_nullCorrectedSkipped(t *testing.T) {
	body := []byte(`[{"scope":"kick","field":"gain_db","auto":"-3","corrected":null}]`)
	recs, err := ParseRecords(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if recs[0].Fixed != "" {
		t.Errorf("null corrected should be empty fixed, got %q", recs[0].Fixed)
	}
	s := NewStore()
	if n := s.ObserveAll(recs); n != 0 {
		t.Errorf("a null-corrected row should be unlearnable, learnable=%d", n)
	}
}

// TestParseRecords_badJSONDegrades confirms structurally invalid JSON is a wrapped
// error, not a panic.
func TestParseRecords_badJSONDegrades(t *testing.T) {
	if _, err := ParseRecords([]byte("{not json")); err == nil {
		t.Error("expected an error for malformed JSON")
	}
}

// TestParseRecords_empty handles an empty array gracefully.
func TestParseRecords_empty(t *testing.T) {
	recs, err := ParseRecords([]byte(`[]`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("expected 0 records, got %d", len(recs))
	}
}
