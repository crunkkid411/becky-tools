package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParseArgsBasic(t *testing.T) {
	opt, asJSON, _, selftest, usageErr := parseArgs([]string{"--desc", "a CSV to JSON CLI", "--json", "--lang", "python"})
	if usageErr != "" {
		t.Fatalf("unexpected usage error: %s", usageErr)
	}
	if selftest {
		t.Fatal("selftest should not be set")
	}
	if !asJSON {
		t.Fatal("--json should set asJSON")
	}
	if opt.Description != "a CSV to JSON CLI" {
		t.Fatalf("description = %q", opt.Description)
	}
	if opt.Language != "python" {
		t.Fatalf("language = %q", opt.Language)
	}
	if opt.Timeout != 30 || opt.MaxAttempts != 5 {
		t.Fatalf("unexpected defaults: timeout=%d maxAttempts=%d", opt.Timeout, opt.MaxAttempts)
	}
}

// TestParseArgsFlagAfterBareValue guards the position-independent-scan bug
// that already bit cmd/notify, cmd/websearch and cmd/file: a bare description
// value must not swallow a later flag.
func TestParseArgsFlagAfterBareValue(t *testing.T) {
	opt, asJSON, _, _, usageErr := parseArgs([]string{"--desc", "build a thing", "--json"})
	if usageErr != "" {
		t.Fatalf("unexpected usage error: %s", usageErr)
	}
	if !asJSON {
		t.Fatal("--json after --desc's value should still be recognized")
	}
	if opt.Description != "build a thing" {
		t.Fatalf("description = %q", opt.Description)
	}
}

func TestParseArgsMissingDescription(t *testing.T) {
	_, _, _, _, usageErr := parseArgs([]string{"--lang", "python"})
	if usageErr == "" {
		t.Fatal("expected a usage error for a missing --desc")
	}
}

func TestParseArgsUnknownFlag(t *testing.T) {
	_, _, _, _, usageErr := parseArgs([]string{"--desc", "x", "--nope"})
	if usageErr == "" {
		t.Fatal("expected a usage error for an unknown flag")
	}
}

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"csv2json":       "csv2json",
		"My Cool Thing!": "My_Cool_Thing_",
		"__leading":      "leading",
		"a-b_c":          "a-b_c",
	}
	for in, want := range cases {
		if got := sanitizeName(in); got != want {
			t.Errorf("sanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClassifyErrorOrder(t *testing.T) {
	// "importerror" text is claimed by dependency_error before import_error is
	// ever checked - this mirrors the ORIGINAL Mark-XXXIX branch order exactly
	// (see runner.go's doc comment); this test pins that behavior so a future
	// refactor doesn't accidentally "fix" it and silently change classification.
	if got := classifyError("ImportError: cannot import name 'x'"); got != errDependency {
		t.Fatalf("classifyError = %q, want %q (dependency_error wins on the word importerror)", got, errDependency)
	}
	if got := classifyError("cannot import foo from bar"); got != errImport {
		t.Fatalf("classifyError = %q, want %q", got, errImport)
	}
}

func TestRunProjectTimeoutIsNotAnError(t *testing.T) {
	if _, err := exec.LookPath("python"); err != nil {
		if _, err := exec.LookPath("python3"); err != nil {
			t.Skip("no python interpreter on PATH")
		}
	}
	// runProject splits the run command on whitespace only (matching the
	// ORIGINAL Python source's plain `.split()` - no shell quoting support),
	// so the sleep script is a file, not an inline -c string with spaces.
	dir := t.TempDir()
	script := dir + string(filepath.Separator) + "sleep.py"
	if err := os.WriteFile(script, []byte("import time\ntime.sleep(5)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := runProject("python "+script, dir, 1)
	if hasError(out) {
		t.Fatalf("a timeout must not classify as an error, got: %s", out)
	}
}
