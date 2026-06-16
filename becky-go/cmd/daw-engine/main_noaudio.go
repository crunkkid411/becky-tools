//go:build !audio

package main

// main_noaudio.go is the DEFAULT (no-cgo, pure-Go) audio seam.
//
// The real-audio subcommands (--record / --play / --list-real /
// --play-pattern-audio) only exist in the `-tags audio` build (main_audio.go).
// This stub detects --play-pattern-audio and degrades to a plain-language
// message so the user knows exactly how to get sound. All other audio flags
// return false so main() falls through to the pure-Go device-selection demo.

import (
	"fmt"
	"os"
	"strings"
)

// audioModeRun handles --play-pattern-audio in the no-audio build by printing
// a clear rebuild instruction and exiting 2 (degrade). All other audio-only
// flags are ignored here; they will produce "unknown flag" from flag.Parse in
// main() if typed, which is the existing behaviour for --record / --play.
func audioModeRun(args []string) (handled bool, exitCode int) {
	for _, a := range args {
		if a == "--play-pattern-audio" || a == "-play-pattern-audio" ||
			strings.HasPrefix(a, "--play-pattern-audio=") ||
			strings.HasPrefix(a, "-play-pattern-audio=") {
			fmt.Fprintln(os.Stderr,
				"becky-daw-engine: --play-pattern-audio requires the audio build.\n"+
					"Rebuild with:  go build -tags audio ./cmd/daw-engine\n"+
					"  (Windows: set CC=C:\\msys64\\mingw64\\bin\\gcc.exe first)")
			return true, 2
		}
	}
	return false, 0
}
