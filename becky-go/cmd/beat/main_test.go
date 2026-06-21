package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"becky-go/internal/dawmodel"
)

// readArr loads an arrangement file written by a becky-beat command.
func readArr(t *testing.T, path string) dawmodel.Arrangement {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var arr dawmodel.Arrangement
	if err := json.Unmarshal(data, &arr); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return arr
}

// drumSteps returns the distinct step indices (start/StepTicks) for a GM note.
func drumSteps(arr dawmodel.Arrangement, note int) []int {
	seen := map[int]bool{}
	for _, t := range arr.Tracks {
		for _, c := range t.Clips {
			for _, n := range c.Notes {
				if n.Pitch == note {
					seen[n.Start/120] = true
				}
			}
		}
	}
	var out []int
	for s := range seen {
		out = append(out, s)
	}
	return out
}

func TestRun_usage(t *testing.T) {
	if code := run(nil); code != exitUsage {
		t.Errorf("no args = usage, got %d", code)
	}
	if code := run([]string{"bogus"}); code != exitUsage {
		t.Errorf("unknown command = usage, got %d", code)
	}
	if code := run([]string{"--help"}); code != exitOK {
		t.Errorf("--help = ok, got %d", code)
	}
}

func TestNew_producesSpreadBeat(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "beat.json")
	if code := run([]string{"new", "--out", out, "--genre", "trap", "--seed", "7", "--bpm", "140"}); code != exitOK {
		t.Fatalf("new exit = %d", code)
	}
	arr := readArr(t, out)
	if arr.BPM != 140 {
		t.Errorf("bpm = %d, want 140", arr.BPM)
	}
	if arr.NoteCount() == 0 {
		t.Fatal("generated beat has no notes")
	}
	// Regression for the StepTicks=0 bug: notes must NOT all collapse onto tick 0.
	starts := map[int]bool{}
	for _, tr := range arr.Tracks {
		for _, c := range tr.Clips {
			for _, n := range c.Notes {
				starts[n.Start] = true
			}
		}
	}
	if len(starts) < 2 {
		t.Errorf("all notes share a start tick (StepTicks bug): starts=%v", starts)
	}
}

func TestNew_deterministic(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.json")
	b := filepath.Join(dir, "b.json")
	run([]string{"new", "--out", a, "--genre", "house", "--seed", "5"})
	run([]string{"new", "--out", b, "--genre", "house", "--seed", "5"})
	da, _ := os.ReadFile(a)
	db, _ := os.ReadFile(b)
	if string(da) != string(db) {
		t.Error("same genre + seed must yield byte-identical output")
	}
}

func TestNew_seedVariesOutput(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.json")
	b := filepath.Join(dir, "b.json")
	run([]string{"new", "--out", a, "--genre", "trap", "--seed", "1"})
	run([]string{"new", "--out", b, "--genre", "trap", "--seed", "2"})
	da, _ := os.ReadFile(a)
	db, _ := os.ReadFile(b)
	if string(da) == string(db) {
		t.Error("different seeds should differ")
	}
}

func TestNew_requiresOut(t *testing.T) {
	if code := run([]string{"new", "--genre", "trap"}); code != exitUsage {
		t.Errorf("missing --out = usage, got %d", code)
	}
}

func TestEuclid_evenlySpaced(t *testing.T) {
	dir := t.TempDir()
	beat := filepath.Join(dir, "beat.json")
	run([]string{"new", "--out", beat, "--genre", "trap", "--seed", "7"})
	out := filepath.Join(dir, "euclid.json")
	if code := run([]string{"euclid", "--project", beat, "--lane", "kick", "--pulses", "4", "--steps", "16", "--out", out}); code != exitOK {
		t.Fatalf("euclid exit = %d", code)
	}
	got := drumSteps(readArr(t, out), 36) // 36 = kick
	want := map[int]bool{0: true, 4: true, 8: true, 12: true}
	if len(got) != 4 {
		t.Fatalf("E(4,16) kick should have 4 onsets, got %v", got)
	}
	for _, s := range got {
		if !want[s] {
			t.Errorf("kick onset at unexpected step %d (want 0,4,8,12), got %v", s, got)
		}
	}
}

func TestEuclid_requiresLaneAndPulses(t *testing.T) {
	dir := t.TempDir()
	beat := filepath.Join(dir, "beat.json")
	run([]string{"new", "--out", beat, "--genre", "trap", "--seed", "7"})
	if code := run([]string{"euclid", "--project", beat}); code != exitUsage {
		t.Errorf("euclid without lane/pulses = usage, got %d", code)
	}
}

func TestTransform_nonDestructive(t *testing.T) {
	dir := t.TempDir()
	beat := filepath.Join(dir, "beat.json")
	run([]string{"new", "--out", beat, "--genre", "trap", "--seed", "7"})
	before, _ := os.ReadFile(beat)
	out := filepath.Join(dir, "rand.json")
	if code := run([]string{"randomize", "--project", beat, "--seed", "9", "--out", out}); code != exitOK {
		t.Fatalf("randomize exit = %d", code)
	}
	after, _ := os.ReadFile(beat)
	if string(before) != string(after) {
		t.Error("transform must not modify the input project")
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("expected output %s: %v", out, err)
	}
}

func TestTransform_badProject(t *testing.T) {
	if code := run([]string{"randomize", "--project", "/no/such.json"}); code != exitErr {
		t.Errorf("missing project = runtime error, got %d", code)
	}
}

func TestTransform_requiresProject(t *testing.T) {
	if code := run([]string{"mutate"}); code != exitUsage {
		t.Errorf("missing --project = usage, got %d", code)
	}
}

func TestRemix_writesAndNonDestructive(t *testing.T) {
	dir := t.TempDir()
	beat := filepath.Join(dir, "beat.json")
	run([]string{"new", "--out", beat, "--genre", "house", "--seed", "5"})
	before, _ := os.ReadFile(beat)
	out := filepath.Join(dir, "remix.json")
	if code := run([]string{"remix", "--project", beat, "--amount", "0.3", "--seed", "2", "--out", out}); code != exitOK {
		t.Fatalf("remix exit = %d", code)
	}
	ra := readArr(t, out)
	if ra.NoteCount() == 0 {
		t.Error("remix produced an empty beat")
	}
	after, _ := os.ReadFile(beat)
	if string(before) != string(after) {
		t.Error("remix must not modify the input")
	}
}

func TestVary_writesNDistinctDeterministic(t *testing.T) {
	dir := t.TempDir()
	beat := filepath.Join(dir, "beat.json")
	run([]string{"new", "--out", beat, "--genre", "trap", "--seed", "5"})
	od := filepath.Join(dir, "vars")
	if code := run([]string{"vary", "--project", beat, "--count", "3", "--seed", "7", "--outdir", od}); code != exitOK {
		t.Fatalf("vary exit = %d", code)
	}
	var contents []string
	for i := 1; i <= 3; i++ {
		p := filepath.Join(od, "beat.var"+itoa(i)+".json")
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("expected variation %d at %s: %v", i, p, err)
		}
		contents = append(contents, string(b))
	}
	// At least two of the three must differ (they're distinct seeds).
	if contents[0] == contents[1] && contents[1] == contents[2] {
		t.Error("variations are all identical — expected distinct beats")
	}
	// Determinism: a second run into a new dir reproduces var1 byte-for-byte.
	od2 := filepath.Join(dir, "vars2")
	run([]string{"vary", "--project", beat, "--count", "3", "--seed", "7", "--outdir", od2})
	a, _ := os.ReadFile(filepath.Join(od, "beat.var1.json"))
	b, _ := os.ReadFile(filepath.Join(od2, "beat.var1.json"))
	if string(a) != string(b) {
		t.Error("vary must be deterministic for the same seed")
	}
}

func TestVary_requiresProject(t *testing.T) {
	if code := run([]string{"vary"}); code != exitUsage {
		t.Errorf("vary without --project = usage, got %d", code)
	}
}

// itoa is a tiny strconv.Itoa shim so the test file needs no extra import.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func TestDefaultOut(t *testing.T) {
	cases := map[string]string{
		"/x/song.json": filepath.FromSlash("/x/song.beat.json"),
		"beat.json":    "beat.beat.json",
	}
	for in, want := range cases {
		if got := defaultOut(in, ""); filepath.FromSlash(got) != want {
			t.Errorf("defaultOut(%q) = %q, want %q", in, got, want)
		}
	}
	if got := defaultOut("x.json", "explicit.json"); got != "explicit.json" {
		t.Errorf("explicit --out should win, got %q", got)
	}
}
