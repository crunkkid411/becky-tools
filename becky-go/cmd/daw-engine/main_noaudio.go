//go:build !audio

package main

// main_noaudio.go is the DEFAULT (no-cgo, pure-Go) audio seam. It does nothing:
// the real-audio subcommands (--record / --play / --list-real) only exist in the
// `-tags audio` build (main_audio.go). Keeping this no-op here means the default
// `go build ./cmd/daw-engine` stays pure-Go and its device-selection demo is
// unchanged (CLAUDE.md §3). Returning handled=false tells main() to fall through
// to the existing stub-enumerator demo.
func audioModeRun(_ []string) (handled bool, exitCode int) {
	return false, 0
}
