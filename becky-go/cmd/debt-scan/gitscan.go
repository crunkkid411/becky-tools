// gitscan.go — git integration: repo detection, --since changed-file lists, and
// the blame helper used for TODO ageing.
//
// All git use is best-effort: if git is not on PATH, or the scan root is not a
// repo, every function here degrades to "no git" and the caller reports markers
// without ages / scans the full tree instead of a diff. We never fail a scan
// because of git.
package main

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// gitInfo describes the git context of a scan root.
type gitInfo struct {
	available bool   // git binary on PATH and root is inside a work tree
	root      string // absolute path of the repo top level
}

// detectGit reports whether path is inside a git work tree and where its top
// level is. Returns gitInfo{available:false} on any failure.
func detectGit(path string) gitInfo {
	if _, err := exec.LookPath("git"); err != nil {
		return gitInfo{}
	}
	out, err := runGit(path, "rev-parse", "--show-toplevel")
	if err != nil {
		return gitInfo{}
	}
	root := strings.TrimSpace(out)
	if root == "" {
		return gitInfo{}
	}
	abs, aerr := filepath.Abs(root)
	if aerr != nil {
		abs = root
	}
	return gitInfo{available: true, root: filepath.Clean(abs)}
}

// changedFiles returns the absolute paths changed since ref (committed diff plus
// the working tree), cleaned for membership tests. An error is returned only
// when git itself fails so the caller can surface a clear note.
func changedFiles(gi gitInfo, ref string) (map[string]bool, error) {
	if !gi.available {
		return nil, errNoGit
	}
	var paths []string
	for _, args := range [][]string{
		{"diff", "--name-only", ref + "...HEAD"}, // committed changes ref..HEAD
		{"diff", "--name-only", ref},             // plus uncommitted working tree
	} {
		out, err := runGit(gi.root, args...)
		if err != nil {
			continue // tolerate one form failing on shallow/odd refs; try next
		}
		paths = append(paths, strings.Split(out, "\n")...)
	}
	if len(paths) == 0 {
		// Distinguish "no changes" (valid) from "bad ref": probe the ref.
		if _, err := runGit(gi.root, "rev-parse", "--verify", ref); err != nil {
			return nil, errBadRef
		}
	}
	allow := map[string]bool{}
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		allow[filepath.Clean(filepath.Join(gi.root, p))] = true
	}
	return allow, nil
}

// runGit runs a git command rooted at dir and returns stdout. stderr is folded
// into the error so callers can log a useful reason.
func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var out, errBuf strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", &gitError{op: strings.Join(args, " "), msg: strings.TrimSpace(errBuf.String()), err: err}
	}
	return out.String(), nil
}

// gitError carries the failing op and git's stderr.
type gitError struct {
	op  string
	msg string
	err error
}

func (e *gitError) Error() string {
	if e.msg != "" {
		return "git " + e.op + ": " + e.msg
	}
	return "git " + e.op + ": " + e.err.Error()
}

// Sentinel errors for the --since path.
var (
	errNoGit  = constError("not a git repository (or git not installed)")
	errBadRef = constError("git ref not found")
)

type constError string

func (e constError) Error() string { return string(e) }
