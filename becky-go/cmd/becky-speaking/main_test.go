package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"becky-go/internal/config"
)

// --- resolveFaceInterpreter ---------------------------------------------
//
// Regression for the 2026-08-24 night bug: the first fix pointed cfg.FacePython
// at the DML venv directly and threaded that SAME cfg into runASD too, which
// then failed with "No module named 'torch'" (the DML venv has no torch - it
// is not needed there, LR-ASD scoring uses a different interpreter). The
// second bug, caught by live testing not by reasoning: leaving FacePyLib set
// let its OWN conflicting CPU-only onnxruntime silently win the import over
// the DML venv's own, correctly-installed one.

func TestResolveFaceInterpreterUsesDMLWhenAvailable(t *testing.T) {
	cfg := config.Config{FacePython: "anaconda.exe", FacePyLib: `X:\PythonUserBase`, FaceDMLPython: "dml.exe"}
	got, dev := resolveFaceInterpreter(cfg, "", false)
	if got.FacePython != "dml.exe" {
		t.Errorf("FacePython = %q, want dml.exe", got.FacePython)
	}
	if got.FacePyLib != "" {
		t.Errorf("FacePyLib = %q, want cleared - the DML venv is self-contained and FacePyLib's onnxruntime conflicts with it", got.FacePyLib)
	}
	if dev != "dml" {
		t.Errorf("device = %q, want dml", dev)
	}
}

func TestResolveFaceInterpreterRespectsExplicitDevice(t *testing.T) {
	cfg := config.Config{FacePython: "anaconda.exe", FaceDMLPython: "dml.exe"}
	got, dev := resolveFaceInterpreter(cfg, "cpu", true)
	if got.FacePython != "anaconda.exe" {
		t.Errorf("FacePython = %q, want unchanged - caller passed an explicit --device", got.FacePython)
	}
	if dev != "cpu" {
		t.Errorf("device = %q, want cpu unchanged", dev)
	}
}

func TestResolveFaceInterpreterFallsBackWhenNoDMLVenv(t *testing.T) {
	cfg := config.Config{FacePython: "anaconda.exe"}
	got, dev := resolveFaceInterpreter(cfg, "", false)
	if got.FacePython != "anaconda.exe" || dev != "" {
		t.Errorf("got (%q,%q), want unchanged when FaceDMLPython is not configured", got.FacePython, dev)
	}
}

// The bug that shipped once already: overriding cfg.FacePython in place and
// threading the SAME cfg into runASD. resolveFaceInterpreter must return a
// COPY so the caller's original cfg (used for LR-ASD's torch interpreter)
// is provably untouched.
func TestResolveFaceInterpreterNeverMutatesCallersCfg(t *testing.T) {
	cfg := config.Config{FacePython: "anaconda.exe", FaceDMLPython: "dml.exe"}
	_, _ = resolveFaceInterpreter(cfg, "", false)
	if cfg.FacePython != "anaconda.exe" {
		t.Errorf("caller's cfg.FacePython = %q, want unchanged (anaconda.exe) - runASD needs this interpreter later", cfg.FacePython)
	}
}

// --- The asd.py / face_embed.py SEAMS ---------------------------------------
//
// becky-speaking's whole job is calling two Python helpers correctly. This is
// exactly the class of bug HANDOFF-SHORTS-PIPELINE.md names as already having
// happened twice: becky-validate was called with a --variant flag that did not
// exist, and becky-identify was called without its required --kb, and every unit
// test in both tools stayed green because each was tested in isolation. So these
// tests do not hand-copy the flag list — they PARSE the real argparse calls in
// the real .py files this binary invokes and assert against THAT.

// pyArgparseFlags scans a Python helper's source for every ap.add_argument(...)
// call and returns (all flag names emitted, flag names marked required=True).
// It is a paren-depth scanner rather than a regex over the whole call body, so a
// nested default like default=os.path.join(...) does not truncate the match.
func pyArgparseFlags(t *testing.T, path string) (all map[string]bool, required map[string]bool) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	src := string(b)

	all = map[string]bool{}
	required = map[string]bool{}
	flagRe := regexp.MustCompile(`^"(--[\w-]+)"`)

	parts := strings.Split(src, "add_argument(")
	for _, part := range parts[1:] {
		body := callBody(part)
		trimmed := strings.TrimSpace(body)
		m := flagRe.FindStringSubmatch(trimmed)
		if m == nil {
			continue // a positional arg, e.g. "images" — not a flag
		}
		name := m[1]
		all[name] = true
		if strings.Contains(body, "required=True") {
			required[name] = true
		}
	}
	if len(all) == 0 {
		t.Fatalf("found no add_argument(...) flags in %s — the seam test cannot verify anything", path)
	}
	return all, required
}

// callBody returns the text up to the paren that closes the one already
// consumed by splitting on "add_argument(" — i.e. the full argument list of one
// add_argument call, however many lines or nested parens it spans.
func callBody(s string) string {
	depth := 1
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[:i]
			}
		}
	}
	return s
}

func TestSeam_ASDFlagsExistAndRequiredOnesAreSupplied(t *testing.T) {
	all, required := pyArgparseFlags(t, "../../internal/pyhelpers/asd.py")

	// Every flag becky-speaking's runASD passes must exist in asd.py's argparse.
	used := []string{"--video", "--tracks", "--repo", "--start", "--end", "--ffmpeg", "--device"}
	for _, f := range used {
		if !all[f] {
			t.Errorf("runASD passes %q, which asd.py does NOT accept (it accepts %v)", f, sortedKeys(all))
		}
	}

	// Every flag asd.py REQUIRES must be one becky-speaking actually supplies.
	usedSet := map[string]bool{}
	for _, f := range used {
		usedSet[f] = true
	}
	for f := range required {
		if !usedSet[f] {
			t.Errorf("asd.py requires %q, but runASD never passes it — this is the exact class of bug "+
				"that shipped becky-identify without --kb", f)
		}
	}
}

func TestSeam_FaceEmbedFlagsExist(t *testing.T) {
	all, _ := pyArgparseFlags(t, "../../internal/pyhelpers/face_embed.py")

	used := []string{"--model-root", "--model-name", "--device", "--all-faces"}
	for _, f := range used {
		if !all[f] {
			t.Errorf("faceembed passes %q, which face_embed.py does NOT accept (it accepts %v)", f, sortedKeys(all))
		}
	}
}

// TestSeam_FaceEmbedEmitsTheAllKeyEmbedAllReadsOn asserts the "all" JSON key
// name asd wiring reads (internal/faceembed's helperRec.All has json tag "all")
// is the exact key face_embed.py writes with --all-faces. A rename on either
// side without the other is precisely the "--variant" class of bug.
func TestSeam_FaceEmbedEmitsTheAllKeyFaceembedReadsOn(t *testing.T) {
	b, err := os.ReadFile("../../internal/pyhelpers/face_embed.py")
	if err != nil {
		t.Fatalf("reading face_embed.py: %v", err)
	}
	if !strings.Contains(string(b), `rec["all"]`) {
		t.Fatal(`face_embed.py no longer writes rec["all"] — internal/faceembed's helperRec.All (json:"all") ` +
			`would silently stop receiving multi-face data`)
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// --- Pure-function checks that do not warrant a full selftest entry ---------

func TestFrameTimes_ZeroFPSNeverDividesByZero(t *testing.T) {
	// Guarded by run()'s own fps<=0 check before this is ever called, but the
	// pure function itself must not panic if that guard is ever removed.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("frameTimes(0, 0, 3) panicked: %v", r)
		}
	}()
	frameTimes(0, 0, 3)
}

func TestDecideSpeaker_MarginIsInclusive(t *testing.T) {
	a, b := 0.70, 0.50 // exactly speakerMargin apart
	tracks := []trackOut{{ID: 1, SpeakingFrac: &a}, {ID: 2, SpeakingFrac: &b}}
	id, conf := decideSpeaker(tracks)
	if id == nil || *id != 1 || conf != "conclusion" {
		t.Errorf("a margin exactly at the threshold should conclude; got id=%v conf=%s", id, conf)
	}
}
