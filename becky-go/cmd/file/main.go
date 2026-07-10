// becky-file — one dumb call for safe local file operations, the file-ops
// half of WHORETANA ask #2 (buildplan Phase 3), ported from Mark-XLVII's
// actions/file_controller.py. Whoretana/an agent says "list my desktop",
// "read that note", "save this", "move it to Documents" — becky-file does it
// and returns a plain JSON envelope.
//
//	becky-file <action> [flags]
//	  actions: list | read | write | mkdir | move | copy | find | info
//
//	becky-file list --path desktop
//	becky-file read --path documents --name notes.txt
//	becky-file write --path desktop --name todo.txt --content "buy milk"
//	becky-file move --path downloads --name a.pdf --dest documents
//	becky-file find --path home --name resume --ext .pdf
//	becky-file --selftest        # offline proof of the containment + ops core
//
// Safety (AUTOPILOT.md Law 8b — DELETE NOTHING OF JORDAN'S. EVER.): there is
// NO delete action and NO bulk auto-organize. Every operation is confined to
// the allowed roots (default: the user's home dir; widen with BECKY_FILE_ROOTS,
// an OS-path-list-separated list). write/move/copy refuse to overwrite an
// existing file unless write is passed --overwrite/--append. A path that
// escapes a root via .. or a symlink is denied before any I/O.
//
// Output: the JSON envelope always goes to stdout (beckyio contract), a human
// summary goes to stderr unless --json is passed. Exit codes: 0 = ok; 1 = the
// operation failed (denied, not found, refused clobber) — {"ok":false,...};
// 2 = usage error.
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"becky-go/internal/beckyio"
)

func main() {
	action, opt, asJSON, selftest, usageErr := parseArgs(os.Args[1:])

	if selftest {
		os.Exit(runSelftest())
	}
	if usageErr != "" {
		fmt.Fprintln(os.Stderr, "usage: becky-file <list|read|write|mkdir|move|copy|find|info> [--path P] [--name N] [--dest D] [--content S] [--append] [--overwrite] [--ext E] [--max N] [--hidden] [--json]")
		fmt.Fprintln(os.Stderr, "becky-file:", usageErr)
		os.Exit(2)
	}

	res := run(action, opt)

	if !asJSON {
		printPlain(res)
	}
	beckyio.PrintJSON(res)
	if !res.OK {
		os.Exit(1)
	}
}

// parseArgs does a position-independent scan (Go's stdlib flag package stops at
// the first non-flag arg, which would wrongly reject `becky-file list --json`;
// this is the same bug cmd/notify and cmd/websearch already hit and fixed). The
// first bare (non-flag) token is the action; flags may appear in any order.
func parseArgs(args []string) (action string, opt options, asJSON, selftest bool, usageErr string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--json", "-json":
			asJSON = true
		case "--selftest", "-selftest":
			selftest = true
		case "--append", "-append":
			opt.appendMode = true
		case "--overwrite", "-overwrite":
			opt.overwrite = true
		case "--hidden", "-hidden":
			opt.hidden = true
		case "--path", "-path":
			i = takeValue(args, i, &opt.path, &usageErr, "--path")
		case "--name", "-name":
			i = takeValue(args, i, &opt.name, &usageErr, "--name")
		case "--dest", "-dest", "--destination":
			i = takeValue(args, i, &opt.dest, &usageErr, "--dest")
		case "--content", "-content":
			i = takeValue(args, i, &opt.content, &usageErr, "--content")
		case "--ext", "-ext", "--extension":
			i = takeValue(args, i, &opt.ext, &usageErr, "--ext")
		case "--max", "-max":
			var raw string
			i = takeValue(args, i, &raw, &usageErr, "--max")
			if raw != "" {
				if n, err := strconv.Atoi(raw); err == nil {
					opt.max = n
				} else {
					usageErr = "--max wants a number"
				}
			}
		default:
			if strings.HasPrefix(a, "-") {
				usageErr = "unknown flag: " + a
			} else if action == "" {
				action = a
			} else {
				usageErr = "unexpected extra argument: " + a
			}
		}
	}
	if !selftest && action == "" && usageErr == "" {
		usageErr = "no action given"
	}
	return action, opt, asJSON, selftest, usageErr
}

// takeValue reads the value following a flag at index i, stores it, and returns
// the advanced index. A missing value sets usageErr.
func takeValue(args []string, i int, dst *string, usageErr *string, flag string) int {
	if i+1 >= len(args) {
		*usageErr = flag + " wants a value"
		return i
	}
	*dst = args[i+1]
	return i + 1
}

// printPlain writes a short human summary to stderr; stdout always carries the
// JSON envelope (beckyio's "JSON to stdout, diagnostics to stderr" contract).
func printPlain(res Result) {
	if !res.OK {
		fmt.Fprintln(os.Stderr, "becky-file:", res.Error)
		return
	}
	fmt.Fprintln(os.Stderr, res.Message)
	for _, e := range res.Entries {
		mark := "  "
		if e.IsDir {
			mark = "d "
		}
		fmt.Fprintf(os.Stderr, "%s%s\n", mark, e.Name)
	}
	if res.Content != "" {
		fmt.Fprintln(os.Stderr, res.Content)
		if res.Truncated {
			fmt.Fprintln(os.Stderr, "[truncated]")
		}
	}
}
