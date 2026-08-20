package shotcut

import (
	"os"
	"reflect"
	"testing"
)

func TestMedian(t *testing.T) {
	if got := median([]float64{3, 1, 2}); got != 2 {
		t.Errorf("median odd = %v, want 2", got)
	}
	if got := median([]float64{1, 2, 3, 4}); got != 2.5 {
		t.Errorf("median even = %v, want 2.5", got)
	}
	if got := median(nil); got != 0 {
		t.Errorf("median empty = %v, want 0", got)
	}
}

func TestMedianAbsDev(t *testing.T) {
	// deviations from median 2 are [1,0,0,1] -> median of those is 0.5
	got := medianAbsDev([]float64{1, 2, 2, 3}, 2)
	if got != 0.5 {
		t.Errorf("medianAbsDev = %v, want 0.5", got)
	}
}

// frames builds one-byte-per-frame synthetic "video" from brightness values,
// so meanAbsDiff between consecutive frames is exactly |values[i+1]-values[i]|
// and the arithmetic in these tests is easy to hand-verify.
func frames(values ...int) [][]byte {
	out := make([][]byte, len(values))
	for i, v := range values {
		out[i] = []byte{byte(v)}
	}
	return out
}

// TestCutTimes_SingleSpikeAndCollapsedRun pins down the exact threshold and
// collapsing arithmetic: a lone spike is one cut at its OWN frame's time; two
// consecutive spikes collapse to one cut at the run's first frame. Every
// spike here is a PERMANENT level change (the brightness stays at the new
// level for several frames), so confirmedCut passes for both — this test is
// about run-collapsing, not confirmation (see the motion-blur tests below for
// that).
func TestCutTimes_SingleSpikeAndCollapsedRun(t *testing.T) {
	// brightness: 100,102,100,102,100,102, +40 jump (stays ~142) ..., +40+40
	// jump (stays ~220) ...
	f := frames(100, 102, 100, 102, 100, 102,
		142, 140, 142, 140, 142, 140, 142, 140, 142, 140,
		180, 220, 218, 216, 214)
	// diffs implied: 2,2,2,2,2,40, 2,2,2,2,2,2,2,2,2, 40,40, 2,2,2 (20 diffs, 21 frames)
	// median=2, MAD=0 -> threshold floors to minDiff=0, so effective threshold
	// is exactly the baseline (2); only the 40s clear it (strictly >).
	got := cutTimes(f, 0, 10, 0, 1)
	want := []float64{0.6, 1.6}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("cutTimes = %v, want %v", got, want)
	}
}

// TestCutTimes_ShortGapMerges pins the runGap=2 tolerance: two hits
// separated by up to two non-hit frames are ONE cut (a soft blend inside a
// real hard cut), while two hits separated by THREE non-hit frames are TWO
// separate cuts. Both jumps are sustained level changes, so confirmation
// passes in every case here. runGap was raised from 1 to 2 after raising
// minDiffFloor to fix a real-footage false positive split what used to be
// one blend-run into two — see the package doc comment.
func TestCutTimes_ShortGapMerges(t *testing.T) {
	merged := frames(100, 101, 102, 142, 143, 144, 184, 185, 186)
	// diffs: 1,1,40,1,1,40,1,1 -> candidates at diff-index 2 and 5, 3 apart:
	// 5-2=3 <= runGap+1(3) -> merges into one cut.
	got := cutTimes(merged, 0, 10, 0, 2)
	want := []float64{0.3} // run starts at diff-index 2 -> frame 3 -> 3/10
	if !reflect.DeepEqual(got, want) {
		t.Errorf("short-gap cutTimes = %v, want %v (should merge)", got, want)
	}

	separate := frames(100, 101, 102, 142, 143, 144, 145, 185, 186, 187)
	// diffs: 1,1,40,1,1,1,40,1,1 -> candidates at diff-index 2 and 6, 4 apart:
	// 6-2=4 > runGap+1(3) -> stays two cuts.
	got2 := cutTimes(separate, 0, 10, 0, 1)
	want2 := []float64{0.3, 0.7}
	if !reflect.DeepEqual(got2, want2) {
		t.Errorf("long-gap cutTimes = %v, want %v (should NOT merge)", got2, want2)
	}
}

// TestCutTimes_MinDiffFloorsAStaticShot: near-zero variance (a locked-off
// shot) must never manufacture a cut from encoder noise, even though
// median+6*MAD is tiny in that regime.
func TestCutTimes_MinDiffFloorsAStaticShot(t *testing.T) {
	f := frames(100, 100, 101, 101, 100, 101, 100, 101, 101)
	got := cutTimes(f, 0, 10, minDiffFloor, 1)
	if len(got) != 0 {
		t.Errorf("static shot produced cuts %v, want none (floor=%v)", got, minDiffFloor)
	}
}

// TestCutTimes_RejectsMotionBlurFalsePositive is the regression test for the
// REAL bug found verifying this package on test-for-clips.mp4: a fast head
// whip / hand raise produced a one-frame luma spike that looked exactly like
// a cut, at frames that turned out (on visual inspection of the extracted
// PNGs) to be the same continuous shot — the picture snapped back to the same
// composition a few frames later. confirmedCut exists specifically to catch
// this: a transient spike that decays back must NOT be reported as a cut.
func TestCutTimes_RejectsMotionBlurFalsePositive(t *testing.T) {
	f := frames(100, 100, 100, 100, 150, 101, 100, 100, 100, 100)
	got := cutTimes(f, 0, 10, 6.0, 2)
	if len(got) != 0 {
		t.Errorf("motion-blur spike produced a cut %v, want none (it decays back to baseline)", got)
	}
}

// TestCutTimes_ConfirmsARealSustainedCut is the positive twin of the above: a
// jump that STAYS at the new level (a real shot change) must still be
// reported, so confirmedCut is a filter on false positives, not a blanket
// suppressor.
func TestCutTimes_ConfirmsARealSustainedCut(t *testing.T) {
	f := frames(100, 100, 100, 100, 200, 200, 200, 200, 200, 200)
	got := cutTimes(f, 0, 10, 6.0, 2)
	want := []float64{0.4} // frame 4 -> 4/10
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sustained cut = %v, want %v (a real cut must still be reported)", got, want)
	}
}

// TestConfirmedCut_TrustsASpikeItCannotCheck: too close to either edge of the
// decoded window to look both sides — this can only rule OUT a false
// positive, never manufacture a true one, so "can't tell" keeps the spike.
func TestConfirmedCut_TrustsASpikeItCannotCheck(t *testing.T) {
	f := frames(100, 200, 200, 200)
	if !confirmedCut(f, 1, 6.0, 5) {
		t.Error("confirmedCut rejected a candidate it could not check both sides of, want trusted")
	}
}

// TestDetect_RealFootage validates against a hand-verified cut list from
// research/jordan-edit-reverse-engineered.md ("Finding 1" — the master's own
// existing cuts, 20-55s). Skips when the source file isn't present on this
// machine (it is Jordan's own footage, not checked into the repo).
func TestDetect_RealFootage(t *testing.T) {
	video := `X:/AI-2/becky-tools/2024-08-30_We_Tried_the_ULTIMATE_Fast_Food_Test_BLINDFOLD_Tasting_[unswA5Jv7fI].mp4`
	if _, err := os.Stat(video); err != nil {
		t.Skipf("real footage not present on this machine: %v", err)
	}
	ref := []float64{21.81, 23.04, 24.48, 28.41, 30.05, 31.95, 33.42, 34.85, 36.59, 37.29, 38.02, 39.66,
		42.36, 44.70, 45.00, 45.70, 46.93, 48.17, 49.33, 50.97, 52.20, 53.04, 54.04, 54.64}

	cuts, err := Detect(Options{Video: video, Start: 20, End: 55})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	const tol = 0.15
	matchedRef := make([]bool, len(ref))
	tp := 0
	for _, d := range cuts {
		for j, r := range ref {
			if matchedRef[j] {
				continue
			}
			delta := d - r
			if delta < 0 {
				delta = -delta
			}
			if delta <= tol {
				matchedRef[j] = true
				tp++
				break
			}
		}
	}
	precision := float64(tp) / float64(len(cuts))
	recall := float64(tp) / float64(len(ref))
	t.Logf("detected=%d ref=%d tp=%d precision=%.3f recall=%.3f", len(cuts), len(ref), tp, precision, recall)
	// MEASURED 2026-08-20, after cutTimes began considering LOCAL MAXIMA inside a
	// busy stretch instead of only the first frame of an over-threshold run, with
	// minProminence added to keep motion ramps out:
	//
	//	before: precision 0.944  recall 0.708  (17 of 24 real cuts)
	//	after : precision 0.909  recall 0.833  (20 of 24)
	//
	// Recall is what matters most for becky-short, because Jordan INHERITS the
	// source's cuts rather than choosing them: a cut this detector misses is a
	// shot boundary his edit would have kept. End to end that moved becky from
	// removing 7.6% of the reference window to 9.5%, against his own 10.3%, at
	// 0.152s per boundary against his 0.15-0.18s.
	//
	// Gates leave footage-variation headroom without letting either number back
	// to where it was.
	if precision < 0.85 {
		t.Errorf("precision regressed: %.3f < 0.85", precision)
	}
	if recall < 0.78 {
		t.Errorf("recall regressed: %.3f < 0.78", recall)
	}
}

// TestDetect_RealFootage_NoFalsePositiveOnFastMotion is the real-footage
// twin of TestCutTimes_RejectsMotionBlurFalsePositive: this exact window of
// Jordan's own raw footage is what exposed the bug (two "cuts" reported at
// 33.2s/35.2s that visual inspection showed were a head/hand motion blur
// inside ONE continuous shot, not a real cut). Skips when the file isn't
// present on this machine.
func TestDetect_RealFootage_NoFalsePositiveOnFastMotion(t *testing.T) {
	video := `X:/AI-2/becky-tools/test-for-clips.mp4`
	if _, err := os.Stat(video); err != nil {
		t.Skipf("real footage not present on this machine: %v", err)
	}
	cuts, err := Detect(Options{Video: video, Start: 30, End: 45})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(cuts) != 0 {
		t.Errorf("Detect found %v in a continuous raw-footage window with fast head/hand motion, want none "+
			"(this exact case was visually verified as ONE shot, not a cut)", cuts)
	}
}

// The WHOLE raw file, not a hand-picked window. A brightness-difference-only
// detector reported 56 cuts here - some 200ms apart - and frames either side of
// them showed the same shot with the subject merely moving. That mattered well
// beyond this package: becky-short reads "this source has cuts" as "already
// edited", so 56 phantom cuts silently switched a raw clip onto the
// preserve-and-tighten path and skipped silence removal entirely.
func TestDetect_RawFootageHasNoCutsAtAll(t *testing.T) {
	video := `X:/AI-2/becky-tools/test-for-clips.mp4`
	if _, err := os.Stat(video); err != nil {
		t.Skipf("real footage not present on this machine: %v", err)
	}
	cuts, err := Detect(Options{Video: video})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(cuts) != 0 {
		t.Errorf("Detect found %d cuts in a single-take raw file, want 0: %v", len(cuts), cuts)
	}
}

// TestProminence pins the number that separates a cut from a wobble on a motion
// ramp, using the two MEASURED populations (see minProminence). Values, not
// truthiness: the gate has to sit in the empty band between them.
func TestProminenceSeparatesCutsFromMotionRamps(t *testing.T) {
	// A motion ramp: everything around the peak is already high, so the peak
	// barely stands out. These are the measured phantom values, 2.24 to 2.89.
	ramp := make([]float64, 61)
	for i := range ramp {
		ramp[i] = 8
	}
	ramp[30] = 22 // 22/8 = 2.75, right in the phantom band
	if p := prominence(ramp, 30, 15); p >= minProminence {
		t.Errorf("a wobble on a motion ramp scored %.2f, at or above the %.2f gate", p, minProminence)
	}

	// An isolated cut: ordinary content either side, one big jump.
	quiet := make([]float64, 61)
	for i := range quiet {
		quiet[i] = 3
	}
	quiet[30] = 70
	if p := prominence(quiet, 30, 15); p < minProminence {
		t.Errorf("an isolated cut scored %.2f, below the %.2f gate", p, minProminence)
	}

	// The gate must sit BETWEEN the two measured populations, not on top of
	// either. Worst phantom 2.89, weakest real cut kept 4.01.
	if minProminence <= 2.89 {
		t.Errorf("minProminence %.2f does not clear the worst measured phantom (2.89)", minProminence)
	}
	if minProminence >= 4.01 {
		t.Errorf("minProminence %.2f would reject the weakest real cut measured (4.01)", minProminence)
	}

	// A dead-still neighbourhood must not divide by ~zero into a huge score.
	still := make([]float64, 61)
	still[30] = 1
	if p := prominence(still, 30, 15); p > 3 {
		t.Errorf("a lone flicker in a dead-still stretch scored %.2f; the median floor should cap it", p)
	}
}
