package main

import (
	"math"
	"testing"
)

// makeEnrolled builds an enrolled voice whose averaged embedding is a single basis vector
// scaled so that a probe's cosine against it can be set precisely. We instead construct
// embeddings directly and rely on cosine() — see probeFor below.

// orthoBasis returns count mutually-orthogonal unit vectors of the given dimension.
func orthoBasis(dim, count int) [][]float64 {
	out := make([][]float64, count)
	for i := 0; i < count; i++ {
		v := make([]float64, dim)
		v[i] = 1
		out[i] = v
	}
	return out
}

// probeWithCosines builds a probe embedding and an enrolled set so that the probe's cosine
// against enrolled[i] equals sims[i] (approximately). Each enrollee is a distinct
// orthogonal basis vector; the probe is the weighted, normalized sum of those bases. With
// orthonormal enrollee vectors, cosine(probe, e_i) == sims[i] / ||sum|| ... so to get the
// EXACT target cosines we make the probe = sum(sims[i] * e_i) then DON'T renormalize the
// enrollee side. Because each e_i is a unit vector, cosine(probe, e_i) = sims[i] / ||probe||.
// We therefore scale so ||probe|| = 1 by construction is not possible while hitting all
// targets, so we instead set the probe to exactly sum(sims[i]*e_i) and assert via the
// engine's own cosine, scaling sims so ||probe||==1: pick sims as the desired RATIOS and
// normalize. To keep tests exact and readable we build the probe and enrolled embeddings
// directly with the helper below.
func enrolledFromCosines(dim int, named map[string]float64) (probe []float64, enrolled []enrolledVoice) {
	// Assign each name a distinct orthonormal basis vector; the probe is the (un-normalized)
	// linear combination using the desired cosines as coefficients. cosine() normalizes both
	// sides, so cosine(probe, e_i) = sims[i] / ||(sims...)||. We therefore must pick sims
	// whose vector norm is 1 to get the literal target. Instead of forcing that, we choose a
	// construction where the probe is ALREADY unit and each enrollee unit, by building the
	// probe as the normalized combination and computing what cosine results — then we just
	// SET sims to be the post-normalization values. Simplest exact approach: make the probe a
	// unit vector and place each enrollee as cos*probe + sin*ortho. That yields cosine exactly.
	names := make([]string, 0, len(named))
	for n := range named {
		names = append(names, n)
	}
	// Deterministic order.
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	// dim must be >= 1 (probe) + len(names) (one ortho dir per enrollee).
	if dim < 1+len(names) {
		dim = 1 + len(names)
	}
	probe = make([]float64, dim)
	probe[0] = 1 // probe = e0 (unit)
	bases := orthoBasis(dim, dim)
	for k, n := range names {
		c := named[n]
		s := math.Sqrt(math.Max(0, 1-c*c))
		ortho := bases[1+k] // a direction orthogonal to probe and to each other
		emb := make([]float64, dim)
		for d := 0; d < dim; d++ {
			emb[d] = c*probe[d] + s*ortho[d]
		}
		enrolled = append(enrolled, enrolledVoice{name: n, key: n, embedding: emb})
	}
	return probe, enrolled
}

func defaultOpts() voiceOptions {
	return voiceOptions{threshold: 0.45, nameThreshold: 0.75, nameMargin: 0.06}
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

// --- topTwo ---

// 3 enrollees with cosines {0.73, 0.71, 0.20} -> best=0.73 / runnerUp=0.71.
func TestTopTwoThreeEnrollees(t *testing.T) {
	probe, enrolled := enrolledFromCosines(8, map[string]float64{"John": 0.73, "Mike": 0.71, "Shelby": 0.20})
	best, runner := topTwo(probe, enrolled)
	if best.name != "John" || !approx(best.sim, 0.73) {
		t.Fatalf("best = %+v, want John/0.73", best)
	}
	if runner.name != "Mike" || !approx(runner.sim, 0.71) {
		t.Fatalf("runnerUp = %+v, want Mike/0.71", runner)
	}
}

// 1 enrollee -> runnerUp empty, and the caller treats margin as best.
func TestTopTwoSingleEnrollee(t *testing.T) {
	probe, enrolled := enrolledFromCosines(4, map[string]float64{"Shelby": 0.84})
	best, runner := topTwo(probe, enrolled)
	if best.name != "Shelby" || !approx(best.sim, 0.84) {
		t.Fatalf("best = %+v, want Shelby/0.84", best)
	}
	if runner.name != "" {
		t.Fatalf("runnerUp should be empty for a single enrollee, got %+v", runner)
	}
}

// 0 enrollees -> both empty, best.sim 0.
func TestTopTwoNoEnrollees(t *testing.T) {
	best, runner := topTwo([]float64{1, 0, 0}, nil)
	if best.name != "" || runner.name != "" {
		t.Fatalf("expected empty best/runnerUp, got %+v / %+v", best, runner)
	}
	if best.sim != 0 {
		t.Errorf("best.sim = %v, want 0 for no enrollees", best.sim)
	}
}

// --- THE regression: ambiguous top-2 margin must NOT be named ---

// best=0.73 for "John", runnerUp=0.74 for "Mike": best is below 0.75 AND the pair is a
// coin-flip -> NOT named (ambiguous / below-name-threshold), emitted as a candidate.
func TestNotNamedWhenAmbiguousNextNearest(t *testing.T) {
	probe, enrolled := enrolledFromCosines(8, map[string]float64{"John": 0.73, "Mike": 0.74, "Shelby": 0.20})
	speakers := []speakerAudio{{id: "SPEAKER_02", segments: []SpeakerSpan{{0, 1}}, embedding: probe}}
	ids := matchSpeakers(speakers, enrolled, defaultOpts())
	if len(ids) != 0 {
		t.Fatalf("a 0.73-vs-0.74 coin flip must NOT be named, got %+v", ids)
	}
	unids := unmatchedDescriptions(speakers, enrolled, defaultOpts())
	if len(unids) != 1 {
		t.Fatalf("expected 1 demoted candidate, got %+v", unids)
	}
	// Mike (0.74) is the in-cast best here; below 0.75 naming floor -> below-name-threshold.
	if unids[0].Candidate != "Mike" {
		t.Errorf("candidate = %q, want Mike (the top-1)", unids[0].Candidate)
	}
	if unids[0].WhyUnnamed != whyBelowNameThresh {
		t.Errorf("why_unnamed = %q, want %q", unids[0].WhyUnnamed, whyBelowNameThresh)
	}
}

// best=0.73 / runnerUp=0.04 (distant), naming threshold 0.75 -> below-name-threshold ->
// candidate "John", NOT named (the lone-weak-match case).
func TestNotNamedBelowNameThreshold(t *testing.T) {
	probe, enrolled := enrolledFromCosines(6, map[string]float64{"John": 0.73, "Mike": 0.04})
	speakers := []speakerAudio{{id: "SPEAKER_03", segments: []SpeakerSpan{{0, 1}}, embedding: probe}}
	opts := defaultOpts()
	if ids := matchSpeakers(speakers, enrolled, opts); len(ids) != 0 {
		t.Fatalf("0.73 < 0.75 naming floor must NOT be named, got %+v", ids)
	}
	unids := unmatchedDescriptions(speakers, enrolled, opts)
	if len(unids) != 1 {
		t.Fatalf("expected 1 candidate, got %+v", unids)
	}
	u := unids[0]
	if u.Candidate != "John" {
		t.Errorf("candidate = %q, want John", u.Candidate)
	}
	if !approx(u.CandidateConfidence, 0.73) {
		t.Errorf("candidate_confidence = %v, want 0.73", u.CandidateConfidence)
	}
	if u.WhyUnnamed != whyBelowNameThresh {
		t.Errorf("why_unnamed = %q, want %q", u.WhyUnnamed, whyBelowNameThresh)
	}
}

// best=0.80 / runnerUp=0.78 (margin 0.02 < 0.06): above the naming floor but too close ->
// ambiguous-margin, candidate names both, NOT named.
func TestNotNamedAmbiguousMarginAboveFloor(t *testing.T) {
	probe, enrolled := enrolledFromCosines(8, map[string]float64{"John": 0.80, "Mike": 0.78, "Shelby": 0.10})
	speakers := []speakerAudio{{id: "SPEAKER_04", segments: []SpeakerSpan{{0, 1}}, embedding: probe}}
	opts := defaultOpts()
	if ids := matchSpeakers(speakers, enrolled, opts); len(ids) != 0 {
		t.Fatalf("a 0.80-over-0.78 ambiguity (margin 0.02 < 0.06) must NOT be named, got %+v", ids)
	}
	unids := unmatchedDescriptions(speakers, enrolled, opts)
	if len(unids) != 1 {
		t.Fatalf("expected 1 candidate, got %+v", unids)
	}
	u := unids[0]
	if u.Candidate != "John" || u.RunnerUp != "Mike" {
		t.Errorf("candidate/runner = %q/%q, want John/Mike", u.Candidate, u.RunnerUp)
	}
	if !approx(u.VoiceMargin, 0.02) {
		t.Errorf("voice_margin = %v, want ~0.02", u.VoiceMargin)
	}
	if u.WhyUnnamed != whyAmbiguousMargin {
		t.Errorf("why_unnamed = %q, want %q", u.WhyUnnamed, whyAmbiguousMargin)
	}
}

// --- the unambiguous strong match still names ---

// best=0.84 / runnerUp=0.05 -> NAMED, with voice_margin ~0.79 and runner_up audit.
func TestStrongUnambiguousMatchNamed(t *testing.T) {
	probe, enrolled := enrolledFromCosines(8, map[string]float64{"Shelby": 0.84, "John": 0.05, "Mike": 0.02})
	speakers := []speakerAudio{{id: "SPEAKER_01", segments: []SpeakerSpan{{0, 1}}, embedding: probe}}
	ids := matchSpeakers(speakers, enrolled, defaultOpts())
	if len(ids) != 1 {
		t.Fatalf("a 0.84/0.05 match should be NAMED, got %+v", ids)
	}
	got := ids[0]
	if got.Name != "Shelby" {
		t.Errorf("name = %q, want Shelby", got.Name)
	}
	if !approx(got.Confidence, 0.84) {
		t.Errorf("confidence = %v, want 0.84", got.Confidence)
	}
	if !approx(got.VoiceMargin, 0.79) {
		t.Errorf("voice_margin = %v, want ~0.79 (0.84 - 0.05)", got.VoiceMargin)
	}
	if got.RunnerUp != "John" {
		t.Errorf("runner_up = %q, want John", got.RunnerUp)
	}
	if !approx(got.RunnerUpConfidence, 0.05) {
		t.Errorf("runner_up_confidence = %v, want 0.05", got.RunnerUpConfidence)
	}
}

// A strong match with a near-tied runner-up but a WIDE gap is named (sanity: margin gate
// only fires on a small gap). Also: a single strong enrollee (no rival) is named.
func TestSingleStrongEnrolleeNamed(t *testing.T) {
	probe, enrolled := enrolledFromCosines(4, map[string]float64{"Shelby": 0.84})
	speakers := []speakerAudio{{id: "SPEAKER_00", segments: []SpeakerSpan{{0, 1}}, embedding: probe}}
	ids := matchSpeakers(speakers, enrolled, defaultOpts())
	if len(ids) != 1 || ids[0].Name != "Shelby" {
		t.Fatalf("a single 0.84 enrollee should be named Shelby, got %+v", ids)
	}
	if ids[0].RunnerUp != "" {
		t.Errorf("runner_up should be empty for a single enrollee, got %q", ids[0].RunnerUp)
	}
	// margin == best when there is no rival.
	if !approx(ids[0].VoiceMargin, 0.84) {
		t.Errorf("voice_margin = %v, want 0.84 (no rival)", ids[0].VoiceMargin)
	}
}

// --- below the detection floor ---

// best=0.40 (< 0.45 detection) -> generic unknown, why_unnamed below-detection, no candidate.
func TestBelowDetectionGenericUnknown(t *testing.T) {
	probe, enrolled := enrolledFromCosines(6, map[string]float64{"John": 0.40, "Mike": 0.30})
	speakers := []speakerAudio{{id: "SPEAKER_07", segments: []SpeakerSpan{{0, 1}}, embedding: probe}}
	opts := defaultOpts()
	if ids := matchSpeakers(speakers, enrolled, opts); len(ids) != 0 {
		t.Fatalf("a 0.40 best (< 0.45) must NOT be named, got %+v", ids)
	}
	unids := unmatchedDescriptions(speakers, enrolled, opts)
	if len(unids) != 1 {
		t.Fatalf("expected 1 unknown, got %+v", unids)
	}
	u := unids[0]
	if u.Candidate != "" {
		t.Errorf("below detection should carry NO candidate name, got %q", u.Candidate)
	}
	if u.Description != "unidentified speaker, unknown identity" {
		t.Errorf("description = %q, want the generic unknown", u.Description)
	}
	if u.WhyUnnamed != whyBelowDetection {
		t.Errorf("why_unnamed = %q, want %q", u.WhyUnnamed, whyBelowDetection)
	}
}

// --- --cast plausibility guard ---

// enrollees {Shelby, John, Mike}; speaker is a strong 0.80 match for Mike, but
// --cast "Shelby,John" -> Mike suppressed; result is unknown with why_unnamed not-in-cast,
// NOT "Mike". (The absent/excluded enrollee can never be the answer.)
func TestCastSuppressesAbsentWinner(t *testing.T) {
	probe, enrolled := enrolledFromCosines(8, map[string]float64{"Mike": 0.80, "John": 0.30, "Shelby": 0.10})
	speakers := []speakerAudio{{id: "SPEAKER_06", segments: []SpeakerSpan{{0, 1}}, embedding: probe}}
	opts := defaultOpts()
	opts.cast = []string{"Shelby", "John"}

	ids := matchSpeakers(speakers, enrolled, opts)
	for _, id := range ids {
		if id.Name == "Mike" {
			t.Fatalf("Mike is not in --cast and must never be named, got %+v", ids)
		}
	}
	if len(ids) != 0 {
		t.Fatalf("with the real winner suppressed, no in-cast enrollee should be substituted, got %+v", ids)
	}
	unids := unmatchedDescriptions(speakers, enrolled, opts)
	if len(unids) != 1 {
		t.Fatalf("expected 1 unidentified, got %+v", unids)
	}
	if unids[0].WhyUnnamed != whyNotInCast {
		t.Errorf("why_unnamed = %q, want %q", unids[0].WhyUnnamed, whyNotInCast)
	}
	if unids[0].Candidate == "Mike" {
		t.Errorf("not-in-cast must not surface Mike as a candidate, got %q", unids[0].Candidate)
	}
}

// --cast "Shelby" with a genuine Shelby match 0.84 -> still NAMED Shelby (cast does not
// block a present, in-cast enrollee).
func TestCastDoesNotBlockPresentEnrollee(t *testing.T) {
	probe, enrolled := enrolledFromCosines(8, map[string]float64{"Shelby": 0.84, "John": 0.10, "Mike": 0.05})
	speakers := []speakerAudio{{id: "SPEAKER_01", segments: []SpeakerSpan{{0, 1}}, embedding: probe}}
	opts := defaultOpts()
	opts.cast = []string{"Shelby"}

	ids := matchSpeakers(speakers, enrolled, opts)
	if len(ids) != 1 || ids[0].Name != "Shelby" {
		t.Fatalf("an in-cast Shelby at 0.84 should still be named, got %+v", ids)
	}
	// With only Shelby eligible (the in-cast set), there is no runner-up.
	if ids[0].RunnerUp != "" {
		t.Errorf("runner_up should be empty (only Shelby in cast), got %q", ids[0].RunnerUp)
	}
}

// resolveCast: an unknown cast name matching no enrollee is reported ignored and the run
// proceeds as if unset (degrade, never crash).
func TestResolveCastUnknownNameIgnored(t *testing.T) {
	kb := Knowledge{
		Entities: map[string]Entity{},
		Voices: []VoicePrint{
			{Key: "shelby", Name: "Shelby"},
			{Key: "john", Name: "John"},
		},
	}
	// All-unknown -> filter ignored entirely (nil list), note explains it.
	list, note := resolveCast("Nobody", kb)
	if list != nil {
		t.Errorf("an all-unknown cast should be ignored (nil), got %v", list)
	}
	if note == "" {
		t.Errorf("expected a notes.cast explanation, got empty")
	}

	// Partial: one known + one unknown -> keep the list, note the ignored one.
	list2, note2 := resolveCast("Shelby,Nobody", kb)
	if len(list2) != 2 {
		t.Fatalf("partial cast should keep all named entries, got %v", list2)
	}
	if note2 == "" {
		t.Errorf("expected a note about the ignored unknown name")
	}

	// Empty -> no cast, no note.
	if l, n := resolveCast("", kb); l != nil || n != "" {
		t.Errorf("empty cast should produce nil/empty, got %v / %q", l, n)
	}
}

// resolveCast resolves against dir key, display name, and entity aliases (case-insensitive).
func TestResolveCastMatchesKeyNameAlias(t *testing.T) {
	kb := Knowledge{
		Entities: map[string]Entity{
			"shelby": {Name: "Shelby Smith", Aliases: []string{"Shel"}},
		},
		Voices: []VoicePrint{
			{Key: "shelby", Name: "Shelby Smith"},
		},
	}
	// dir key (lowercased), display name, and alias all resolve.
	for _, name := range []string{"shelby", "Shelby Smith", "shel"} {
		list, note := resolveCast(name, kb)
		if len(list) != 1 {
			t.Errorf("cast %q should resolve to the enrollee, got list %v note %q", name, list, note)
		}
	}
}

// filterCast keeps only in-cast enrollees (matched by key/name/alias).
func TestFilterCast(t *testing.T) {
	enrolled := []enrolledVoice{
		{name: "Shelby", key: "shelby", aliases: []string{"Shel"}},
		{name: "John", key: "john"},
		{name: "Mike", key: "mike"},
	}
	got := filterCast(enrolled, []string{"shel", "John"})
	if len(got) != 2 {
		t.Fatalf("expected 2 in-cast enrollees, got %+v", got)
	}
	names := map[string]bool{}
	for _, e := range got {
		names[e.name] = true
	}
	if !names["Shelby"] || !names["John"] || names["Mike"] {
		t.Errorf("filterCast kept the wrong set: %+v", names)
	}
	// Empty cast -> unchanged.
	if all := filterCast(enrolled, nil); len(all) != 3 {
		t.Errorf("empty cast should keep all, got %d", len(all))
	}
}
