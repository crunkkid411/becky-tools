package otio

import "testing"

// With an empty PATH (no otioconvert anywhere), the escape hatch must DEGRADE —
// return (false, nil) and report unavailable — never error or panic. This is the
// degrade-never-crash contract that lets becky stay Python-free by default.
func TestOtioConvert_DegradesWhenAbsent(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // a dir with no otioconvert binary

	if OtioCLIAvailable() {
		t.Skip("otioconvert resolved despite an empty PATH (present in cwd?); skipping degrade assertion")
	}
	ran, err := OtioConvert("in.otio", "out.aaf")
	if ran {
		t.Error("ran = true, want false when otioconvert is absent")
	}
	if err != nil {
		t.Errorf("err = %v, want nil (absence is a degrade, not an error)", err)
	}
}
