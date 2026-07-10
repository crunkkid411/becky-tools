package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withRoot points the allowed-roots sandbox at a fresh temp dir for one test.
func withRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("BECKY_FILE_ROOTS", root)
	return root
}

func TestWriteReadRoundTrip(t *testing.T) {
	root := withRoot(t)
	if r := run("write", options{path: root, name: "a.txt", content: "hi"}); !r.OK {
		t.Fatalf("write failed: %s", r.Error)
	}
	r := run("read", options{path: root, name: "a.txt"})
	if !r.OK || r.Content != "hi" {
		t.Fatalf("read mismatch: ok=%v content=%q err=%s", r.OK, r.Content, r.Error)
	}
}

func TestWriteRefusesClobber(t *testing.T) {
	root := withRoot(t)
	run("write", options{path: root, name: "a.txt", content: "original"})
	r := run("write", options{path: root, name: "a.txt", content: "new"})
	if r.OK {
		t.Fatal("write should refuse to overwrite an existing file without --overwrite")
	}
	// original must be intact
	back := run("read", options{path: root, name: "a.txt"})
	if back.Content != "original" {
		t.Fatalf("existing file was modified despite refusal: %q", back.Content)
	}
}

func TestWriteOverwriteFlag(t *testing.T) {
	root := withRoot(t)
	run("write", options{path: root, name: "a.txt", content: "original"})
	if r := run("write", options{path: root, name: "a.txt", content: "new", overwrite: true}); !r.OK {
		t.Fatalf("write --overwrite failed: %s", r.Error)
	}
	if back := run("read", options{path: root, name: "a.txt"}); back.Content != "new" {
		t.Fatalf("overwrite did not take: %q", back.Content)
	}
}

func TestMoveAndRefuseClobber(t *testing.T) {
	root := withRoot(t)
	run("write", options{path: root, name: "a.txt", content: "x"})
	run("mkdir", options{path: root, name: "sub"})
	if r := run("move", options{path: root, name: "a.txt", dest: filepath.Join(root, "sub")}); !r.OK {
		t.Fatalf("move failed: %s", r.Error)
	}
	if _, err := os.Stat(filepath.Join(root, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("source should be gone after move")
	}
	if _, err := os.Stat(filepath.Join(root, "sub", "a.txt")); err != nil {
		t.Fatal("dest should exist after move")
	}
	// second move onto an existing dest must be refused
	run("write", options{path: root, name: "b.txt", content: "y"})
	r := run("move", options{path: root, name: "b.txt", dest: filepath.Join(root, "sub", "a.txt")})
	if r.OK {
		t.Fatal("move must refuse to overwrite an existing destination")
	}
}

// The security boundary: nothing outside the allowed roots may be touched.
func TestContainmentDeniesOutsideRoot(t *testing.T) {
	root := withRoot(t)
	outside := filepath.Join(filepath.Dir(root), "escape.txt")
	r := run("write", options{path: outside, content: "should never land"})
	if r.OK {
		t.Fatal("write outside the allowed root must be denied")
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		os.Remove(outside)
		t.Fatal("a file was written OUTSIDE the sandbox root")
	}
	if !strings.Contains(strings.ToLower(r.Error), "access denied") {
		t.Fatalf("expected an access-denied error, got: %s", r.Error)
	}
}

func TestContainmentDeniesDotDotInName(t *testing.T) {
	root := withRoot(t)
	r := run("read", options{path: root, name: filepath.Join("..", "secret.txt")})
	if r.OK {
		t.Fatal("a ../ escape in --name must be denied")
	}
}

func TestFindByExtension(t *testing.T) {
	root := withRoot(t)
	run("write", options{path: root, name: "keep.md", content: "1"})
	run("write", options{path: root, name: "skip.txt", content: "2"})
	run("mkdir", options{path: root, name: "d"})
	run("write", options{path: filepath.Join(root, "d"), name: "deep.md", content: "3"})
	r := run("find", options{path: root, ext: "md"})
	if !r.OK {
		t.Fatalf("find failed: %s", r.Error)
	}
	if len(r.Entries) != 2 {
		t.Fatalf("expected 2 .md files, got %d", len(r.Entries))
	}
}

func TestUnknownActionFails(t *testing.T) {
	withRoot(t)
	if r := run("nuke", options{}); r.OK {
		t.Fatal("unknown action must fail")
	}
}

// There must be NO delete action reachable (Law 8b): every destructive verb we
// deliberately dropped must fall through to the unknown-action path.
func TestNoDeleteAction(t *testing.T) {
	withRoot(t)
	for _, verb := range []string{"delete", "rm", "remove", "trash", "organize_desktop"} {
		if r := run(verb, options{path: "home"}); r.OK {
			t.Fatalf("%q must not be an accepted action (no destructive ops)", verb)
		}
	}
}
