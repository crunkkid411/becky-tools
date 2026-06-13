// runner.go — shared plumbing for the becky orchestrator: locate the becky-*.exe
// binaries, run one with captured stdout/stderr, and parse common flags. The
// orchestrator is a thin driver; all heavy lifting stays in the underlying tools.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"becky-go/internal/beckyio"
)

// commonFlags holds flags every op shares.
type commonFlags struct {
	bin     string
	verbose bool
	jsonOut bool // suppress the plain-English headline on stderr
}

// binPath returns the absolute path to becky-<tool>(.exe), resolved from the
// --bin dir if set, else next to the running becky executable.
func (c commonFlags) binPath(tool string) (string, error) {
	name := "becky-" + tool
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	dirs := []string{}
	if c.bin != "" {
		dirs = append(dirs, c.bin)
	}
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(exe))
	}
	for _, d := range dirs {
		cand := filepath.Join(d, name)
		if fileExists(cand) {
			return cand, nil
		}
	}
	return "", fmt.Errorf("%s not found (pass --bin <dir>)", name)
}

// extractCommon pulls --bin/--verbose/--json out of an arg slice and returns the
// remaining args. A tiny hand parser keeps each op free to take a positional
// argument before its flags (e.g. `becky find "query" --db x`).
func extractCommon(args []string) (commonFlags, []string) {
	var cf commonFlags
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--bin" && i+1 < len(args):
			cf.bin = args[i+1]
			i++
		case strings.HasPrefix(a, "--bin="):
			cf.bin = strings.TrimPrefix(a, "--bin=")
		case a == "--verbose" || a == "-v":
			cf.verbose = true
		case a == "--json":
			cf.jsonOut = true
		default:
			rest = append(rest, a)
		}
	}
	return cf, rest
}

// flagValue pulls a `--name value` (or `--name=value`) out of args, returning the
// value and the remaining args. Returns def if absent.
func flagValue(args []string, name, def string) (string, []string) {
	val := def
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--"+name && i+1 < len(args):
			val = args[i+1]
			i++
		case strings.HasPrefix(a, "--"+name+"="):
			val = strings.TrimPrefix(a, "--"+name+"=")
		default:
			rest = append(rest, a)
		}
	}
	return val, rest
}

// flagValues pulls all repeated `--name value` occurrences out of args.
func flagValues(args []string, name string) ([]string, []string) {
	var vals []string
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--"+name && i+1 < len(args):
			vals = append(vals, args[i+1])
			i++
		case strings.HasPrefix(a, "--"+name+"="):
			vals = append(vals, strings.TrimPrefix(a, "--"+name+"="))
		default:
			rest = append(rest, a)
		}
	}
	return vals, rest
}

// hasFlag reports whether a boolean flag "--name" is present in args.
func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == "--"+name {
			return true
		}
	}
	return false
}

// dropFlag returns args with every occurrence of the boolean flag "--name"
// removed (so the remaining args can be scanned for a positional).
func dropFlag(args []string, name string) []string {
	out := args[:0:0]
	for _, a := range args {
		if a == "--"+name {
			continue
		}
		out = append(out, a)
	}
	return out
}

// firstPositional returns the first non-flag argument (a quoted name/query/claim).
func firstPositional(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

// runTool executes a becky tool, returning its stdout. stderr is streamed when
// verbose, else captured and folded into the error on failure.
func runTool(cf commonFlags, tool string, args []string) ([]byte, error) {
	bin, err := cf.binPath(tool)
	if err != nil {
		return nil, err
	}
	beckyio.Logf(cf.verbose, "  $ becky-%s %s", tool, strings.Join(args, " "))
	cmd := exec.Command(bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	if cf.verbose {
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stderr = &stderr
	}
	runErr := cmd.Run()
	if runErr != nil {
		return stdout.Bytes(), fmt.Errorf("becky-%s failed (exit %d): %s",
			tool, exitCodeOf(runErr), tailStr(stderr.String(), 600))
	}
	return stdout.Bytes(), nil
}

func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

func tailStr(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = "..." + s[len(s)-n:]
	}
	return strings.Join(strings.Fields(s), " ")
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// headline prints the plain-English summary to stderr unless --json was set.
func headline(cf commonFlags, format string, a ...any) {
	if cf.jsonOut {
		return
	}
	fmt.Fprintf(os.Stderr, format+"\n", a...)
}

func absOr(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}
