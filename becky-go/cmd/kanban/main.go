// becky-kanban - one dumb call to edit MissionControl's task Board
// (data\kanban.json). WHORETANA/docs/DEBRIEF-MODE.md phase 1: the board-edit
// tool debrief mode calls so Jordan can add/modify his board by voice, and a
// clean board API so agents stop hand-editing JSON.
//
//	becky-kanban <action> [args] [flags]
//	  actions: add | move | note | list
//
//	becky-kanban add "fix the orb throttling" --col 0 --agent claude
//	becky-kanban list
//	becky-kanban list --col 2
//	becky-kanban move 3 2                 (move card #3 to column 2)
//	becky-kanban move "orb throttling" 2  (move the one matching card to col 2)
//	becky-kanban note 3 "[WORKING 2026-07-11T01:40]"
//	becky-kanban --selftest               # offline proof of the board core
//
// A card is selected by its 0-based index in the array (shown by `list`) OR by a
// case-insensitive text substring that matches exactly one card.
//
// Safety (AUTOPILOT.md Law 8b - DELETE NOTHING OF JORDAN'S. EVER.): the board is
// additive. No delete action exists; move only changes a card's column, note
// only appends to a card's text, and unknown card fields are preserved. A store
// file that exists but won't parse is refused and left untouched. Writes are
// atomic (temp + rename) so MissionControl - which reads this file live - never
// sees a half-written board. Editing Jordan's OWN board is an internal action
// (Law 19 SAFE); this tool never posts or sends anything externally.
//
// Output: the JSON envelope always goes to stdout (beckyio contract); a human
// summary goes to stderr unless --json is passed. Exit codes: 0 = ok; 1 = the
// action failed (bad index, ambiguous match, corrupt store) - {"ok":false,...};
// 2 = usage error.
package main

import (
	"fmt"
	"os"
	"strings"

	"becky-go/internal/beckyio"
)

const usage = "usage: becky-kanban <add|list|move|note> [args] [--col N] [--agent X] [--text S] [--store PATH] [--json]"

func main() {
	action, rest, opt, asJSON, selftest, usageErr := parseArgs(os.Args[1:])

	if selftest {
		os.Exit(runSelftest())
	}
	if usageErr != "" {
		fmt.Fprintln(os.Stderr, usage)
		fmt.Fprintln(os.Stderr, "becky-kanban:", usageErr)
		os.Exit(2)
	}

	res := run(action, rest, opt)

	if !asJSON {
		printPlain(res)
	}
	beckyio.PrintJSON(res)
	if !res.OK {
		os.Exit(1)
	}
}

// parseArgs does a position-independent scan (Go's stdlib flag package stops at
// the first non-flag arg, which would wrongly reject `becky-kanban list --json`;
// the same bug cmd/notify, cmd/websearch, cmd/file and cmd/goal already hit and
// fixed). Bare tokens are collected in order: the first is the action, the rest
// fill the per-action positional slots.
func parseArgs(args []string) (action string, rest []string, opt options, asJSON, selftest bool, usageErr string) {
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--json", "-json":
			asJSON = true
		case "--selftest", "-selftest":
			selftest = true
		case "--store", "-store":
			i = takeValue(args, i, &opt.store, &usageErr, "--store")
		case "--col", "-col", "--column", "-column":
			i = takeValue(args, i, &opt.col, &usageErr, "--col")
		case "--agent", "-agent":
			i = takeValue(args, i, &opt.agent, &usageErr, "--agent")
		case "--text", "-text", "--note", "-note":
			i = takeValue(args, i, &opt.text, &usageErr, "--text")
		default:
			if strings.HasPrefix(a, "-") {
				usageErr = "unknown flag: " + a
			} else {
				pos = append(pos, a)
			}
		}
	}
	if len(pos) > 0 {
		action = pos[0]
		rest = pos[1:]
	}
	if !selftest && action == "" && usageErr == "" {
		usageErr = "no action given"
	}
	return action, rest, opt, asJSON, selftest, usageErr
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
		fmt.Fprintln(os.Stderr, "becky-kanban:", res.Error)
		return
	}
	if res.Message != "" {
		fmt.Fprintln(os.Stderr, res.Message)
	}
	for _, c := range res.Cards {
		text := c.Text
		if len(text) > 100 {
			text = text[:100] + "..."
		}
		fmt.Fprintf(os.Stderr, "  [col %d] #%d  %s\n", c.Col, c.Index, text)
	}
}
