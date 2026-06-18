package refmatch

import (
	"testing"

	"becky-go/internal/dsp"
)

// stemInput builds a StemInput from synthesized samples + a name (the name lets
// stemscan's filename corroboration land a stable role in tests).
func stemInput(t *testing.T, name string, samples []float64) StemInput {
	t.Helper()
	return StemInput{Name: name, Path: name, Data: encodeWAV(samples, testSR)}
}

// lowStem is a sustained low tone (reads bass/low-dominant); brightStem is a high
// tone (reads bright). Names corroborate the role.
func lowStem(t *testing.T, name string, amp float64) StemInput {
	return stemInput(t, name, mix(tone(80, amp, 1.0), tone(120, amp*0.7, 1.0)))
}

func brightStem(t *testing.T, name string, amp float64) StemInput {
	return stemInput(t, name, mix(tone(5000, amp, 1.0), tone(9000, amp*0.7, 1.0)))
}

// --- grouping by role + averaging ---

func TestBuildLibraryGroupsByRole(t *testing.T) {
	stems := []StemInput{
		lowStem(t, "bass_01.wav", 0.5),
		lowStem(t, "bass_02.wav", 0.4),
		brightStem(t, "hat_01.wav", 0.5),
	}
	lib := BuildLibrary("/stems", stems, Options{})
	if len(lib.Roles) < 2 {
		t.Fatalf("expected at least 2 roles (low-ish + bright-ish), got %d: %+v", len(lib.Roles), lib.RoleNames())
	}
	// Every role target must carry its contributing stems, sorted, and the right count.
	for _, rt := range lib.Roles {
		if rt.StemCount != len(rt.ContributingStems) {
			t.Errorf("role %q StemCount %d != len(ContributingStems) %d", rt.Role, rt.StemCount, len(rt.ContributingStems))
		}
		for i := 1; i < len(rt.ContributingStems); i++ {
			if rt.ContributingStems[i-1] > rt.ContributingStems[i] {
				t.Errorf("role %q contributing stems not sorted: %v", rt.Role, rt.ContributingStems)
			}
		}
	}
}

func TestBuildLibraryAveraging(t *testing.T) {
	// Two bass stems at different amplitudes -> the averaged loudness must sit BETWEEN
	// the two individual loudnesses (arithmetic mean in dB).
	loud := lowStem(t, "bass_loud.wav", 0.8)
	quiet := lowStem(t, "bass_quiet.wav", 0.2)

	pl := analyzeStem(t, loud)
	pq := analyzeStem(t, quiet)

	lib := BuildLibrary("/stems", []StemInput{loud, quiet}, Options{})
	// Find the role both bass stems landed in (they should share one).
	var target *RoleTarget
	for i := range lib.Roles {
		if len(lib.Roles[i].ContributingStems) == 2 {
			target = &lib.Roles[i]
			break
		}
	}
	if target == nil {
		t.Fatalf("expected the two bass stems to group together; roles: %+v", lib.RoleNames())
	}
	wantMean := round1((pl.LoudnessDB + pq.LoudnessDB) / 2)
	if target.Profile.LoudnessDB != wantMean {
		t.Errorf("averaged loudness %.1f != arithmetic mean %.1f (%.1f, %.1f)",
			target.Profile.LoudnessDB, wantMean, pl.LoudnessDB, pq.LoudnessDB)
	}
}

// --- determinism: same folder in -> byte-identical library ---

func TestBuildLibraryDeterministic(t *testing.T) {
	stems := []StemInput{
		brightStem(t, "hat_b.wav", 0.5),
		lowStem(t, "bass_a.wav", 0.5),
		lowStem(t, "bass_c.wav", 0.4),
	}
	// Feed in a DIFFERENT input order to prove the sort makes output stable.
	shuffled := []StemInput{stems[2], stems[0], stems[1]}
	l1 := BuildLibrary("/stems", stems, Options{})
	l2 := BuildLibrary("/stems", shuffled, Options{})

	if len(l1.Roles) != len(l2.Roles) {
		t.Fatalf("role count differs: %d vs %d", len(l1.Roles), len(l2.Roles))
	}
	for i := range l1.Roles {
		a, b := l1.Roles[i], l2.Roles[i]
		if a.Role != b.Role || a.StemCount != b.StemCount {
			t.Errorf("role %d differs: %+v vs %+v", i, a, b)
		}
		if a.Profile.LoudnessDB != b.Profile.LoudnessDB || a.Profile.CentroidHz != b.Profile.CentroidHz {
			t.Errorf("role %q profile differs between input orders", a.Role)
		}
		for j := range a.Profile.Bands {
			if a.Profile.Bands[j].EnergyDB != b.Profile.Bands[j].EnergyDB {
				t.Errorf("role %q band %s differs between input orders", a.Role, a.Profile.Bands[j].Name)
			}
		}
	}
}

// --- degrade-never-crash: bad files are skipped-with-reason, not fatal ---

func TestBuildLibraryDegradeBadFiles(t *testing.T) {
	stems := []StemInput{
		lowStem(t, "good.wav", 0.5),
		{Name: "broken.wav", Path: "broken.wav", Data: []byte("not a wav at all")},
		{Name: "ioerr.wav", Path: "ioerr.wav", Err: errFake{}},
		{Name: "empty.wav", Path: "empty.wav", Data: encodeWAV(nil, testSR)},
	}
	lib := BuildLibrary("/stems", stems, Options{})
	if len(lib.Skipped) < 2 {
		t.Errorf("expected the broken/io-error/empty files skipped, got %d: %+v", len(lib.Skipped), lib.Skipped)
	}
	// The good stem must still have produced a role target.
	if len(lib.Roles) == 0 {
		t.Errorf("the one good stem should still yield a role target")
	}
	// Skipped reasons must be non-empty plain text.
	for _, s := range lib.Skipped {
		if s.Reason == "" {
			t.Errorf("skipped %q has no reason", s.Name)
		}
	}
}

// --- degraded contributor flags the role target degraded ---

func TestBuildLibraryDegradePropagates(t *testing.T) {
	// A sub-frame-length stem decodes but is too short -> Analyze marks it degraded.
	short := stemInput(t, "bass_short.wav", tone(80, 0.5, 0.001))
	lib := BuildLibrary("/stems", []StemInput{short}, Options{})
	if len(lib.Roles) == 0 {
		// short stems may classify as unknown but should still produce a target group.
		t.Skip("short stem produced no role group (acceptable); nothing to assert")
	}
	any := false
	for _, rt := range lib.Roles {
		if rt.Degraded {
			any = true
		}
	}
	if !any {
		t.Errorf("a degraded contributor should flag its role target degraded")
	}
}

// --- TargetForRole + RoleNames lookups ---

func TestTargetForRole(t *testing.T) {
	lib := BuildLibrary("/stems", []StemInput{lowStem(t, "bass_01.wav", 0.5)}, Options{})
	if len(lib.Roles) == 0 {
		t.Fatalf("expected a role target")
	}
	role := lib.Roles[0].Role
	if _, ok := lib.TargetForRole(role); !ok {
		t.Errorf("TargetForRole(%q) should find the target", role)
	}
	if _, ok := lib.TargetForRole("definitely-not-a-role"); ok {
		t.Errorf("TargetForRole on a missing role should return false")
	}
	names := lib.RoleNames()
	if len(names) != len(lib.Roles) {
		t.Errorf("RoleNames count %d != role count %d", len(names), len(lib.Roles))
	}
}

// --- honesty banner is always present ---

func TestLibraryNotePresent(t *testing.T) {
	lib := BuildLibrary("/stems", []StemInput{lowStem(t, "bass.wav", 0.5)}, Options{})
	if lib.Note == "" {
		t.Errorf("library must carry the global honesty banner")
	}
	if !contains(lib.Note, "NOT certified LUFS") {
		t.Errorf("library note must disclose RMS-not-LUFS, got %q", lib.Note)
	}
}

// --- helpers ---

type errFake struct{}

func (errFake) Error() string { return "simulated read failure" }

// analyzeStem decodes a StemInput's WAV bytes and profiles it, the same way
// BuildLibrary does internally — used to predict the expected averaged value.
func analyzeStem(t *testing.T, s StemInput) Profile {
	t.Helper()
	a, err := dsp.DecodeWAV(s.Data)
	if err != nil {
		t.Fatalf("decode %s: %v", s.Name, err)
	}
	return Analyze(a, Options{})
}
