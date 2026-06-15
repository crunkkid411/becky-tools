package canvas

import "testing"

func TestCorrectionsLog_appendIsImmutableAndOrdered(t *testing.T) {
	log := NewCorrectionsLog()
	if log.Len() != 0 || log.Entries == nil {
		t.Fatalf("new log should be empty and non-nil: %+v", log)
	}

	l1 := log.Append(Correction{Kind: FixTiming, TrackID: "bass", At: 480, Before: 0, After: 12})
	// The original log must be untouched (immutable update).
	if log.Len() != 0 {
		t.Errorf("Append mutated the original log (len=%d)", log.Len())
	}
	if l1.Len() != 1 {
		t.Fatalf("appended log len=%d, want 1", l1.Len())
	}
	if l1.Entries[0].Seq != 0 {
		t.Errorf("first correction Seq=%d, want 0", l1.Entries[0].Seq)
	}

	l2 := l1.Append(Correction{Kind: FixPitch, TrackID: "melody", At: 960, Before: 60, After: 62})
	if l2.Len() != 2 || l2.Entries[1].Seq != 1 {
		t.Errorf("second correction Seq=%d (len %d), want Seq 1 len 2", l2.Entries[1].Seq, l2.Len())
	}
	// l1 still has exactly one entry (no shared-slice bleed-through).
	if l1.Len() != 1 {
		t.Errorf("earlier log mutated by later Append: len=%d", l1.Len())
	}
}

func TestCorrection_kindsAreStable(t *testing.T) {
	cases := map[CorrectionKind]string{
		FixTiming: "timing",
		FixPitch:  "pitch",
		FixGain:   "gain",
		FixRoute:  "route",
		FixOther:  "other",
	}
	for k, want := range cases {
		if string(k) != want {
			t.Errorf("correction kind %v = %q, want %q", k, string(k), want)
		}
	}
}
