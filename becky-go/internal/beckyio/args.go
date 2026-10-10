package beckyio

import (
	"flag"
	"strings"
)

// Positional parses flags that may come before, between or after the plain arguments (Go's flag
// package stops at the first plain one) and returns the plain arguments. A flag given as
// "--name value" takes the next word unless it is an on/off switch. Define every flag first.
func Positional(fs *flag.FlagSet, args []string) []string {
	var pos, flags []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			pos = append(pos, a)
			continue
		}
		flags = append(flags, a)
		if f := fs.Lookup(strings.TrimLeft(a, "-")); f != nil && !isSwitch(f) && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	_ = fs.Parse(flags)
	return pos
}

func isSwitch(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}
