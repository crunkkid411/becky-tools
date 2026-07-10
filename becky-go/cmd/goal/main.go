// becky-goal - one dumb call for the durable goal-object store, the seed of
// MissionControl's "durable heartbeat" (MANUS-GAP FIX #3,
// docs/research/manus-gap-analysis.md). Whoretana or an agent says "remember I
// want X", "what am I still waiting on", "mark that done", "log progress on
// that" - becky-goal records it to a JSON file that outlives the session.
//
//	becky-goal <action> [text] [flags]
//	  actions: add | list | update-status | note
//
//	becky-goal add "get the childcare email restored" --due 2026-07-15
//	becky-goal list
//	becky-goal list --status blocked
//	becky-goal update-status g1 active        (or: --id g1 --status active)
//	becky-goal note g1 "drafted the request, waiting on reply"
//	becky-goal --selftest                     # offline proof of the store core
//
// Safety (AUTOPILOT.md Law 8b - DELETE NOTHING OF JORDAN'S. EVER.): the store
// is additive. No delete action exists; update-status only changes a status,
// note only appends. A store file that exists but won't parse is refused and
// left untouched. Writes are atomic (temp + rename).
//
// Output: the JSON envelope always goes to stdout (beckyio contract); a human
// summary goes to stderr unless --json is passed. Exit codes: 0 = ok; 1 = the
// action failed (bad id, invalid status, corrupt store) - {"ok":false,...};
// 2 = usage error.
package main

import (
	"fmt"
	"os"
	"strings"

	"becky-go/internal/beckyio"
)

const usage = "usage: becky-goal <add|list|update-status|note> [text] [--id G] [--status todo|active|blocked|done] [--due D] [--notes S] [--text S] [--store PATH] [--json]"

func main() {
	action, rest, opt, asJSON, selftest, usageErr := parseArgs(os.Args[1:])

	if selftest {
		os.Exit(runSelftest())
	}
	if usageErr != "" {
		fmt.Fprintln(os.Stderr, usage)
		fmt.Fprintln(os.Stderr, "becky-goal:", usageErr)
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
// the first non-flag arg, which would wrongly reject `becky-goal list --json`;
// the same bug cmd/notify, cmd/websearch and cmd/file already hit and fixed).
// Bare tokens are collected in order: the first is the action, the rest fill
// the per-action positional slots.
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
		case "--outcome", "-outcome":
			i = takeValue(args, i, &opt.outcome, &usageErr, "--outcome")
		case "--due", "-due":
			i = takeValue(args, i, &opt.due, &usageErr, "--due")
		case "--notes", "-notes":
			i = takeValue(args, i, &opt.notes, &usageErr, "--notes")
		case "--id", "-id":
			i = takeValue(args, i, &opt.id, &usageErr, "--id")
		case "--status", "-status":
			i = takeValue(args, i, &opt.status, &usageErr, "--status")
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
		fmt.Fprintln(os.Stderr, "becky-goal:", res.Error)
		return
	}
	if res.Message != "" {
		fmt.Fprintln(os.Stderr, res.Message)
	}
	for _, g := range res.Goals {
		due := ""
		if g.Due != "" {
			due = "  (due " + g.Due + ")"
		}
		fmt.Fprintf(os.Stderr, "  [%s] %s  %s%s\n", g.Status, g.ID, g.Outcome, due)
	}
}
