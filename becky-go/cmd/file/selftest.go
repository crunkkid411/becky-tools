// selftest.go — becky-file's one-command, OFFLINE proof of the real code path:
// the allowed-roots containment check and every operation, run against a fresh
// temp directory set as the only allowed root. No network, no fixed machine
// state, no side effects outside the temp dir. This is becky's "provable
// handoff" gate (STANDARDS-WORKFLOW.md §7).
package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func runSelftest() int {
	root, err := os.MkdirTemp("", "becky-file-selftest-")
	if err != nil {
		fmt.Println("selftest: could not make temp dir:", err)
		return 1
	}
	defer os.RemoveAll(root)

	// Confine every operation to the temp dir for the duration of the selftest.
	prev, had := os.LookupEnv("BECKY_FILE_ROOTS")
	os.Setenv("BECKY_FILE_ROOTS", root)
	defer func() {
		if had {
			os.Setenv("BECKY_FILE_ROOTS", prev)
		} else {
			os.Unsetenv("BECKY_FILE_ROOTS")
		}
	}()

	type check struct {
		name string
		ok   bool
	}
	var checks []check
	add := func(name string, ok bool) { checks = append(checks, check{name, ok}) }

	// write a new file
	r := run("write", options{path: root, name: "note.txt", content: "hello"})
	add("write creates a new file", r.OK)

	// read it back
	r = run("read", options{path: root, name: "note.txt"})
	add("read returns the written content", r.OK && r.Content == "hello")

	// refuse to clobber without a flag (Law 8b)
	r = run("write", options{path: root, name: "note.txt", content: "clobber"})
	add("write REFUSES to overwrite an existing file", !r.OK)

	// overwrite only with the explicit flag
	r = run("write", options{path: root, name: "note.txt", content: "v2", overwrite: true})
	r2 := run("read", options{path: root, name: "note.txt"})
	add("write --overwrite replaces content", r.OK && r2.OK && r2.Content == "v2")

	// append
	r = run("write", options{path: root, name: "note.txt", content: "+more", appendMode: true})
	r2 = run("read", options{path: root, name: "note.txt"})
	add("write --append adds to the file", r.OK && r2.OK && r2.Content == "v2+more")

	// mkdir + list
	run("mkdir", options{path: root, name: "sub"})
	r = run("list", options{path: root})
	sawNote, sawSub := false, false
	for _, e := range r.Entries {
		if e.Name == "note.txt" {
			sawNote = true
		}
		if e.Name == "sub" && e.IsDir {
			sawSub = true
		}
	}
	add("list shows the file and the new folder", r.OK && sawNote && sawSub)

	// move into the subdir
	r = run("move", options{path: root, name: "note.txt", dest: filepath.Join(root, "sub")})
	_, srcErr := os.Stat(filepath.Join(root, "note.txt"))
	_, dstErr := os.Stat(filepath.Join(root, "sub", "note.txt"))
	add("move relocates the file (source gone, dest present)", r.OK && os.IsNotExist(srcErr) && dstErr == nil)

	// copy
	r = run("copy", options{path: filepath.Join(root, "sub"), name: "note.txt", dest: filepath.Join(root, "copy.txt")})
	_, cpErr := os.Stat(filepath.Join(root, "copy.txt"))
	add("copy duplicates the file", r.OK && cpErr == nil)

	// move refuses to clobber an existing destination
	r = run("move", options{path: filepath.Join(root, "sub"), name: "note.txt", dest: filepath.Join(root, "copy.txt")})
	add("move REFUSES to overwrite an existing destination", !r.OK)

	// find by extension
	r = run("find", options{path: root, ext: ".txt"})
	add("find locates .txt files under the root", r.OK && len(r.Entries) >= 2)

	// info
	r = run("info", options{path: filepath.Join(root, "sub"), name: "note.txt"})
	add("info stats an existing file", r.OK && r.Info != nil && r.Info.Size > 0)

	// containment: an absolute path OUTSIDE the root is denied
	outside := filepath.Join(filepath.Dir(root), "becky-file-outside-should-deny.txt")
	r = run("write", options{path: outside, content: "should never be written"})
	_, outErr := os.Stat(outside)
	add("containment DENIES a path outside the allowed root", !r.OK && os.IsNotExist(outErr))

	// containment: a ../ escape in the name is denied
	r = run("read", options{path: root, name: filepath.Join("..", "..", "..", "etc", "hosts")})
	add("containment DENIES a ../ escape in --name", !r.OK)

	failed := 0
	for _, c := range checks {
		status := "PASS"
		if !c.ok {
			status = "FAIL"
			failed++
		}
		fmt.Printf("[%s] %s\n", status, c.name)
	}
	fmt.Println()
	if failed == 0 {
		fmt.Printf("becky-file selftest: PASS (%d/%d checks)\n", len(checks), len(checks))
		return 0
	}
	fmt.Printf("becky-file selftest: FAIL (%d/%d checks failed)\n", failed, len(checks))
	return 1
}
