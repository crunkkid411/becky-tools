package main

import (
	"flag"
	"os"
)

// flags bundles the shared flag set for the subcommands.
type flags struct {
	set     *flag.FlagSet
	project *string
	genre   *string
	seed    *int64
	out     *string
}

func newFlags(name string) flags {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	return flags{
		set:     fs,
		project: fs.String("project", "", "input arrangement project.json (or a becky-compose manifest)"),
		genre:   fs.String("genre", "", "genre prior for the progression/idiom"),
		seed:    fs.Int64("seed", 1, "RNG seed for humanization (deterministic)"),
		out:     fs.String("out", "", "output path (default <project>.<layer>.json next to source)"),
	}
}
