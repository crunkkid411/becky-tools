package main

import (
	"os"
	"path/filepath"
	"testing"

	"becky-go/internal/facenaming"
)

func fixtureClusters() facenaming.Clusters {
	return facenaming.Clusters{
		Tool:     "becky-cluster v1.0.0",
		Modality: "both",
		Clusters: []facenaming.Cluster{
			{ClusterID: "face-A", Modality: "face", MemberCount: 3, DistinctSourceFiles: 2, Cohesion: 0.71, Representative: "a1.mp4",
				Members: []facenaming.Member{{SourceFile: "a1.mp4", DetScore: 0.9}, {SourceFile: "a2.mp4", DetScore: 0.8}}},
		},
	}
}

// TestDispatch_DryRun asserts --dry-run selects the dry-run path (exit 0) without a TTY.
func TestDispatch_DryRun(t *testing.T) {
	rc := runConfig{dryRun: true, kb: "kb"}
	if code := dispatch(rc, fixtureClusters(), false); code != 0 {
		t.Fatalf("dry-run exit code = %d, want 0", code)
	}
}

// TestDispatch_NoTTY_HeadlessParse asserts: no TTY + no --names + no --dry-run prints a
// parsed summary and exits 0, never launching the TUI (mirrors becky-ask no-TTY).
func TestDispatch_NoTTY_HeadlessParse(t *testing.T) {
	rc := runConfig{kb: "kb"}
	if code := dispatch(rc, fixtureClusters(), false); code != 0 {
		t.Fatalf("no-TTY exit code = %d, want 0", code)
	}
}

// TestRunNamesFile_AppliesMapHeadless asserts --names applies a {id:name} map with the
// real exec enroller resolving to "no binary" -> each clip skipped-with-reason (no
// panic, exit 0), proving the headless apply wiring without models.
func TestRunNamesFile_AppliesMapHeadless(t *testing.T) {
	dir := t.TempDir()
	namesPath := filepath.Join(dir, "names.json")
	if err := os.WriteFile(namesPath, []byte(`{"face-A":"Braxton"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, "audit.json")
	rc := runConfig{kb: filepath.Join(dir, "kb"), namesPath: namesPath, outPath: outPath, binDir: dir}
	if code := dispatch(rc, fixtureClusters(), false); code != 0 {
		t.Fatalf("names-file exit code = %d, want 0", code)
	}
	// The audit file should have been written.
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("expected audit %s written: %v", outPath, err)
	}
}

// TestNewCardModel_Builds asserts the TUI model constructs with fakes (the card
// COMPILES + is constructible). Rendering is a local/display step, not asserted here.
func TestNewCardModel_Builds(t *testing.T) {
	order := facenaming.WalkOrder(fixtureClusters(), "", 0)
	m := newCardModel(order, "kb", facenaming.DefaultEnrollCap, recordingShower{}, noopEnroller{})
	if len(m.clusters) != 1 {
		t.Fatalf("expected 1 cluster in model, got %d", len(m.clusters))
	}
	// The facts/quality lines render to the expected plain text.
	if got, want := factsLine(order[0]), "Person A — seen in 3 clip(s) (2 file(s))"; got != want {
		t.Errorf("factsLine = %q, want %q", got, want)
	}
	if got, want := qualityLine(order[0]), "cohesion 0.71 · face"; got != want {
		t.Errorf("qualityLine = %q, want %q", got, want)
	}
	if got := personLabel("face-A"); got != "Person A" {
		t.Errorf("personLabel = %q, want Person A", got)
	}
}

// TestShellJoin quotes tokens with spaces (used by --dry-run display).
func TestShellJoin(t *testing.T) {
	got := shellJoin([]string{"becky-enroll", "--clip", "a b.mp4", "--name", "Braxton"})
	want := `becky-enroll --clip "a b.mp4" --name Braxton`
	if got != want {
		t.Errorf("shellJoin = %q, want %q", got, want)
	}
}

type recordingShower struct{}

func (recordingShower) Show(string) error { return nil }

type noopEnroller struct{}

func (noopEnroller) Enroll(_, _, _ string) error { return nil }
