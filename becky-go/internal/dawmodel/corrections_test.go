package dawmodel

import "testing"

// TestCorrections_velocityCapturesAutoAndFixed: overriding an auto velocity logs
// the {auto -> fixed} pair with the song's musical context.
func TestCorrections_velocityCapturesAutoAndFixed(t *testing.T) {
	base, ids := fixture() // notes start at vel 88, genre crunkcore, A minor, 140 BPM
	out, err := base.SetVelocity("melody", "melody", ids[:1], 104)
	if err != nil {
		t.Fatal(err)
	}
	if out.CorrectionCount() != 1 {
		t.Fatalf("corrections = %d, want 1", out.CorrectionCount())
	}
	c := out.Corrections[0]
	if c.Kind != "velocity" || c.Auto != "88" || c.Fixed != "104" {
		t.Errorf("correction = %+v, want velocity 88->104", c)
	}
	if c.Genre != "crunkcore" || c.BPM != 140 || c.Scale != "minor" {
		t.Errorf("context = %s/%d/%s, want crunkcore/140/minor", c.Genre, c.BPM, c.Scale)
	}
}

// TestCorrections_noLogWhenUnchanged: setting velocity to the same value logs
// nothing (the log stays a clean taste signal).
func TestCorrections_noLogWhenUnchanged(t *testing.T) {
	base, ids := fixture()
	out, err := base.SetVelocity("melody", "melody", ids[:1], 88) // already 88
	if err != nil {
		t.Fatal(err)
	}
	if out.CorrectionCount() != 0 {
		t.Errorf("no-op set logged %d corrections, want 0", out.CorrectionCount())
	}
}

// TestCorrections_quantizeLogsMoves: quantizing logs one correction per moved note.
func TestCorrections_quantizeLogsMoves(t *testing.T) {
	a, _ := quantClip([]int{5, 120, 247}) // 5->0 moves, 120 stays, 247->240 moves
	out, err := a.Quantize("d", "d", nil, 120, 1.0, 0.5)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(out.CorrectionsByKind("quantize")); got != 2 {
		t.Errorf("quantize corrections = %d, want 2", got)
	}
}

// TestCorrections_immutableLog: logging on the result never touches the receiver.
func TestCorrections_immutableLog(t *testing.T) {
	base, ids := fixture()
	if _, err := base.SetVelocity("melody", "melody", ids, 100); err != nil {
		t.Fatal(err)
	}
	if base.CorrectionCount() != 0 {
		t.Errorf("receiver log mutated: %d, want 0", base.CorrectionCount())
	}
}

// TestCorrections_byKindEmpty: querying an unused kind returns nothing, no panic.
func TestCorrections_byKindEmpty(t *testing.T) {
	base, _ := fixture()
	if got := base.CorrectionsByKind("nope"); got != nil {
		t.Errorf("CorrectionsByKind(nope) = %v, want nil", got)
	}
}
