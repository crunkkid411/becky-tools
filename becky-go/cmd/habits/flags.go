package main

import (
	"flag"
	"strings"
)

// newFlags builds a per-subcommand FlagSet that returns an error on parse failure
// (ContinueOnError) instead of calling os.Exit, so the dispatch in run() can map a
// bad flag to the usage exit code (2) and stay unit-testable.
func newFlags(name string) *flag.FlagSet {
	return flag.NewFlagSet(name, flag.ContinueOnError)
}

// splitArgs separates positional (non-flag) tokens from flag tokens so a natural
// CLI line like `observe file.json --store x` works. Go's flag.Parse stops at the
// first non-flag token, so a positional placed BEFORE a flag would otherwise hide
// the flag. We pull positionals out first; everything from the first "-…" token
// onward (including its value) is treated as flags. A flag's value token (e.g. the
// "x" in "--store x") is kept with the flags, never mistaken for a positional.
func splitArgs(args []string) (positional, flags []string) {
	i := 0
	for i < len(args) && !strings.HasPrefix(args[i], "-") {
		positional = append(positional, args[i])
		i++
	}
	flags = args[i:]
	return positional, flags
}
