package habits

import (
	"strings"
	"testing"
)

// srec is a constructor for a STRUCTURED correction record (Fixed is a JSON blob).
func srec(scope, field, fixedJSON string) CorrectionRecord {
	return CorrectionRecord{Scope: scope, Field: field, Auto: "{}", Fixed: fixedJSON}
}

// TestCanonicalValue covers the canonicalisation contract: equal blobs with
// different key order / whitespace collapse to one form; scalars pass through
// UNCHANGED (the scalar path must stay identical).
func TestCanonicalValue(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"scalar number passes through", "-7", "-7"},
		{"scalar string passes through", "C4", "C4"},
		{"empty passes through", "", ""},
		{"plain object sorted", `{"to":"bus.bass","from":"kick"}`, `{"from":"kick","to":"bus.bass"}`},
		{"whitespace normalised", `{ "from" : "kick" ,  "to":"bus.bass" }`, `{"from":"kick","to":"bus.bass"}`},
		{"nested keys sorted", `{"b":{"y":2,"x":1},"a":1}`, `{"a":1,"b":{"x":1,"y":2}}`},
		{"array elements canonicalised", `[{"b":2,"a":1}]`, `[{"a":1,"b":2}]`},
		{"malformed json degrades verbatim", `{not json`, `{not json`},
		{"number precision preserved", `{"amt":0.5}`, `{"amt":0.5}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := canonicalValue(tc.in); got != tc.want {
				t.Errorf("canonicalValue(%q)=%q want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestStructured_canonicalCounting is the load-bearing structured test: two
// equivalent blobs written with DIFFERENT key order must corroborate each other so
// the habit crosses the threshold and is applied — not split into two candidates.
func TestStructured_canonicalCounting(t *testing.T) {
	s := NewStore()
	s.Observe(srec("routing", "sidechain", `{"from":"kick","to":"bus.bass","amount":0.5}`))
	// Same preference, keys in a different order — must count as the SAME value.
	s.Observe(srec("routing", "sidechain", `{"amount":0.5,"to":"bus.bass","from":"kick"}`))

	h, ok := s.Lookup("routing", "sidechain")
	if !ok {
		t.Fatal("expected a routing/sidechain habit")
	}
	if !h.Learned {
		t.Fatalf("two equivalent blobs should cross threshold: %+v", h)
	}
	if !h.Structured {
		t.Errorf("habit should be marked Structured: %+v", h)
	}
	if len(h.Counts) != 1 {
		t.Errorf("equivalent blobs must collapse to ONE count bucket, got %d: %+v", len(h.Counts), h.Counts)
	}
	if h.Evidence != 2 {
		t.Errorf("evidence=%d want 2 (both observations corroborate)", h.Evidence)
	}
}

// TestStructured_thresholdGate confirms a single structured fix stays a CANDIDATE
// (not applied) until it recurs to MinEvidence — corroborate, then conclude.
func TestStructured_thresholdGate(t *testing.T) {
	blob := `{"from":"kick","to":"bus.bass","amount":0.5}`
	canon := canonicalValue(blob)

	s := NewStore()
	s.Observe(srec("routing", "sidechain", blob))
	if v, found := s.ApplyStructured("routing", "sidechain", "FRESH"); found || v != "FRESH" {
		t.Errorf("below threshold ApplyStructured should pass through fresh, got (%q,%v)", v, found)
	}
	s.Observe(srec("routing", "sidechain", blob))
	if v, found := s.ApplyStructured("routing", "sidechain", "FRESH"); !found || v != canon {
		t.Errorf("at threshold ApplyStructured should return learned canonical blob, got (%q,%v) want (%q,true)", v, found, canon)
	}
}

// TestUsual_hitAndMiss exercises the "my usual X" recall: a learned structured
// chain is recalled by scope; an unknown scope and a not-yet-corroborated one
// both miss.
func TestUsual_hitAndMiss(t *testing.T) {
	chain := `{"fx":["ssl-comp","saturator"],"order":"serial"}`
	s := NewStore()
	// Drum-bus chain learned (recurs twice).
	s.Observe(srec("bus.drums", "chain", chain))
	s.Observe(srec("bus.drums", "chain", chain))
	// A different bus seen only once — must NOT be recalled (candidate).
	s.Observe(srec("bus.vocal", "chain", `{"fx":["deesser"]}`))

	hit := s.Usual("bus.drums")
	if len(hit) != 1 {
		t.Fatalf("usual(bus.drums) should return 1 learned pref, got %d: %+v", len(hit), hit)
	}
	if hit[0].Field != "chain" || hit[0].Value != canonicalValue(chain) || hit[0].Evidence != 2 {
		t.Errorf("usual(bus.drums)[0] wrong: %+v", hit[0])
	}

	if got := s.Usual("bus.vocal"); len(got) != 0 {
		t.Errorf("usual(bus.vocal) is only a candidate; should be empty, got %+v", got)
	}
	if got := s.Usual("no-such-scope"); len(got) != 0 {
		t.Errorf("usual(unknown) should be empty, got %+v", got)
	}

	// UsualField precise lookup hit + miss.
	if p, ok := s.UsualField("bus.drums", "chain"); !ok || p.Value != canonicalValue(chain) {
		t.Errorf("UsualField hit wrong: (%+v,%v)", p, ok)
	}
	if _, ok := s.UsualField("bus.vocal", "chain"); ok {
		t.Error("UsualField for a candidate should miss")
	}
}

// TestScalarPath_unchanged is the back-compat guard: a scalar habit learns,
// applies, and is NOT marked Structured exactly as before structured support.
func TestScalarPath_unchanged(t *testing.T) {
	s := NewStore()
	s.Observe(rec("kick", "gain_db", "-7"))
	s.Observe(rec("kick", "gain_db", "-7"))

	h, ok := s.Lookup("kick", "gain_db")
	if !ok || !h.Learned || h.Default != "-7" {
		t.Fatalf("scalar learning broke: %+v", h)
	}
	if h.Structured {
		t.Errorf("scalar habit must NOT be marked Structured: %+v", h)
	}
	// scalar Apply unchanged
	if got := s.Apply("kick", "gain_db", "0"); got != "-7" {
		t.Errorf("scalar Apply=%q want -7", got)
	}
	// ApplyStructured must NOT claim a scalar habit as a structured one
	if v, found := s.ApplyStructured("kick", "gain_db", "FRESH"); found || v != "FRESH" {
		t.Errorf("ApplyStructured on a scalar habit should miss, got (%q,%v)", v, found)
	}
	// a scalar habit is never a "usual" structured setup
	if got := s.Usual("kick"); len(got) != 0 {
		t.Errorf("scalar habit should not appear in Usual, got %+v", got)
	}
}

// TestStructured_malformedLineDegrades feeds a malformed JSONL line interleaved
// with a structured one through the on-disk path: the bad line is skipped, the
// good structured preference still learns (degrade, never crash).
func TestStructured_malformedLineDegrades(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/daw.jsonl"
	blob := `{\"from\":\"kick\",\"to\":\"bus.bass\",\"amount\":0.5}`
	good := `{"tool":"daw","scope":"routing","field":"sidechain","auto":"{}","fixed":"` + blob + `"}`
	writeJSONL(t, p,
		"\n"+
			`{ totally broken`+"\n"+ // malformed — skipped
			good+"\n"+
			good+"\n", // recurs → learned
	)

	recs, err := LoadCorrectionLog(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("malformed line must be skipped; got %d records: %+v", len(recs), recs)
	}
	s := NewStore()
	s.ObserveAll(recs)
	v, found := s.ApplyStructured("routing", "sidechain", "FRESH")
	if !found {
		t.Fatalf("structured preference should be learned from the 2 good lines")
	}
	if !strings.Contains(v, `"amount":0.5`) || !strings.Contains(v, `"from":"kick"`) {
		t.Errorf("learned structured value malformed: %q", v)
	}
}

// TestStructured_marshalDeterministic confirms structured habits serialise
// deterministically across insertion order (canonical counts + sorted keys).
func TestStructured_marshalDeterministic(t *testing.T) {
	build := func(orders ...string) *Store {
		s := NewStore()
		for _, blob := range orders {
			s.Observe(srec("bus.drums", "chain", blob))
		}
		return s
	}
	a := build(
		`{"fx":["comp","sat"],"order":"serial"}`,
		`{"order":"serial","fx":["comp","sat"]}`,
	)
	b := build(
		`{"order":"serial","fx":["comp","sat"]}`,
		`{"fx":["comp","sat"],"order":"serial"}`,
	)
	ab, _ := a.Marshal()
	bb, _ := b.Marshal()
	if string(ab) != string(bb) {
		t.Errorf("structured store not deterministic across order:\n--- a ---\n%s\n--- b ---\n%s", ab, bb)
	}
}
