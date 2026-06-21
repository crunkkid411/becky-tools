// becky-fxchain — define + store the per-bus VST FX CHAINS that load onto a bus when
// Jordan "finalizes" a song. The ROUTING (label→bus) is becky-route; this is the FX
// layer on top: an ordered list of plugins (+ their saved-state presets) per bus.
//
// IMPORTANT: the defaults are NOT deterministic — Jordan picks them. `init` writes the
// standard buses EMPTY and ready to fill; becky never presumes a "most-used" default.
//
//	becky-fxchain init                                  write the editable EMPTY default
//	becky-fxchain list                                  show every bus + its chain
//	becky-fxchain show DRUMS                             show one bus's chain in order
//	becky-fxchain add DRUMS "Pro-C 2" --preset glue.vstpreset --class-id ABC123
package main

import (
	"fmt"
	"os"
	"strings"

	"becky-go/internal/fxchain"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 0 {
		usage()
		return 2
	}
	switch args[0] {
	case "init":
		return initCmd()
	case "list":
		return list()
	case "show":
		return show(args[1:])
	case "add":
		return add(args[1:])
	case "-h", "--help", "help":
		usage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "becky-fxchain: unknown command %q\n", args[0])
		usage()
		return 2
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "becky-fxchain — per-bus VST FX chains (the plugins that load on finalize)")
	fmt.Fprintln(os.Stderr, "  init                                 write the editable EMPTY default")
	fmt.Fprintln(os.Stderr, "  list                                 show every bus + its chain")
	fmt.Fprintln(os.Stderr, "  show <bus>                           show one bus's chain in order")
	fmt.Fprintln(os.Stderr, "  add <bus> <plugin> [--preset p] [--class-id id] [--bypass]")
	fmt.Fprintf(os.Stderr, "  config file: %s (your chains; edit once)\n", fxchain.Path())
}

func initCmd() int {
	c := fxchain.DefaultChains()
	if err := fxchain.Save(c); err != nil {
		fmt.Fprintln(os.Stderr, "becky-fxchain:", err)
		return 1
	}
	fmt.Printf("✓ wrote the editable EMPTY default (%d buses, no presumptuous plugins) → %s\n", len(c.ByBus), fxchain.Path())
	return 0
}

func list() int {
	c := fxchain.Load()
	for _, bus := range c.Buses() {
		printChain(c.Get(bus))
	}
	return 0
}

func show(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "becky-fxchain show: name a bus, e.g. becky-fxchain show DRUMS")
		return 2
	}
	bus := args[0]
	c := fxchain.Load()
	printChain(c.Get(bus))
	return 0
}

func add(args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "becky-fxchain add: usage: add <bus> <plugin> [--preset p] [--class-id id] [--bypass]")
		return 2
	}
	bus := args[0]
	var nameParts []string
	p := fxchain.Plugin{}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--preset":
			if i+1 < len(args) {
				p.PresetRef = args[i+1]
				i++
			}
		case "--class-id":
			if i+1 < len(args) {
				p.ClassID = args[i+1]
				i++
			}
		case "--bypass":
			p.Bypass = true
		default:
			nameParts = append(nameParts, args[i])
		}
	}
	p.Name = strings.Join(nameParts, " ")
	if p.Name == "" {
		fmt.Fprintln(os.Stderr, "becky-fxchain add: a plugin name is required")
		return 2
	}
	c := fxchain.Load().Add(bus, p)
	if err := fxchain.Save(c); err != nil {
		fmt.Fprintln(os.Stderr, "becky-fxchain:", err)
		return 1
	}
	ch := c.Get(bus)
	fmt.Printf("✓ added %q to %s (now %d in chain)\n", p.Name, bus, len(ch.Plugins))
	return 0
}

func printChain(ch fxchain.Chain) {
	if len(ch.Plugins) == 0 {
		fmt.Printf("%-8s (empty)\n", ch.Bus)
		return
	}
	fmt.Printf("%s\n", ch.Bus)
	for i, p := range ch.Plugins {
		line := fmt.Sprintf("  %d. %s", i+1, p.Name)
		if p.Bypass {
			line += " [bypassed]"
		}
		if p.PresetRef != "" {
			line += "  preset=" + p.PresetRef
		}
		fmt.Println(line)
	}
}
