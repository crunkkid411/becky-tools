// becky-route — apply Jordan's DETERMINISTIC routing in one shot. No more re-routing
// 16 drum channels + double-tracked guitars + a synth bus + 9 vocal layers by hand:
// label a track and it lands on the right bus. The rules are yours, edited once.
//
//	becky-route apply --project song.json        route every track + buses + sidechains
//	becky-route bus "serum bass"                  explain where one label routes (dummy-proof)
//	becky-route rules [--init]                    show the ruleset (or write the editable default)
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"becky-go/internal/autoroute"
	"becky-go/internal/dawmodel"
	"becky-go/internal/pathx"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 0 {
		usage()
		return 2
	}
	switch args[0] {
	case "apply":
		return apply(args[1:])
	case "bus":
		return bus(args[1:])
	case "rules":
		return rules(args[1:])
	case "-h", "--help", "help":
		usage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "becky-route: unknown command %q\n", args[0])
		usage()
		return 2
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "becky-route — deterministic label→bus routing, applied in one shot")
	fmt.Fprintln(os.Stderr, "  apply --project p.json [--out o]   route tracks + buses + sidechains")
	fmt.Fprintln(os.Stderr, "  bus \"<label>\"                      explain where a label routes")
	fmt.Fprintln(os.Stderr, "  rules [--init]                     show the ruleset (or init the editable default)")
	fmt.Fprintf(os.Stderr, "  ruleset file: %s (edit it once; it applies everywhere)\n", autoroute.Path())
}

func apply(args []string) int {
	project, out := "", ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--project":
			if i+1 < len(args) {
				project = args[i+1]
				i++
			}
		case "--out":
			if i+1 < len(args) {
				out = args[i+1]
				i++
			}
		}
	}
	if project == "" {
		fmt.Fprintln(os.Stderr, "becky-route apply: --project is required")
		return 2
	}
	data, err := os.ReadFile(project)
	if err != nil {
		fmt.Fprintln(os.Stderr, "becky-route:", err)
		return 1
	}
	var arr dawmodel.Arrangement
	if err := json.Unmarshal(data, &arr); err != nil {
		fmt.Fprintln(os.Stderr, "becky-route: parse:", err)
		return 1
	}
	routed, assigns := autoroute.Apply(&arr, autoroute.Load())
	for _, a := range assigns {
		fmt.Printf("  %-14s → %s\n", a.Track, a.Bus)
	}
	if out == "" {
		out = project // route in place (it's additive: buses + routing)
	}
	body, _ := json.MarshalIndent(routed, "", "  ")
	if err := os.WriteFile(out, body, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "becky-route:", err)
		return 1
	}
	fmt.Printf("✓ routed %d tracks → %d buses, wrote %s\n", len(routed.Tracks), len(routed.Buses), pathx.Base(out))
	return 0
}

func bus(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "becky-route bus: name a label, e.g. becky-route bus \"serum bass\"")
		return 2
	}
	label := strings.Join(args, " ")
	rs := autoroute.Load()
	fmt.Printf("%q → %s\n", label, rs.BusFor(label))
	return 0
}

func rules(args []string) int {
	rs := autoroute.Load()
	for _, a := range args {
		if a == "--init" {
			rs = autoroute.DefaultRuleset()
			if err := autoroute.Save(rs); err != nil {
				fmt.Fprintln(os.Stderr, "becky-route:", err)
				return 1
			}
			fmt.Printf("✓ wrote the editable default ruleset → %s\n", autoroute.Path())
		}
	}
	data, _ := json.MarshalIndent(rs, "", "  ")
	fmt.Println(string(data))
	return 0
}
