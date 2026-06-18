//go:build !audio

package main

// machine_noaudio.go — the DEFAULT (no-cgo) stub for the --play-machine / --play-pad
// SOUND path. The schedule dump (--schedule) works in this build (pure Go); actually
// playing requires the audio build, so these print a clear rebuild hint and exit 2
// (degrade) — same contract as --play-pattern-audio's no-audio stub.

import (
	"fmt"
	"os"

	"becky-go/internal/audioengine"
	"becky-go/internal/drummachine"
)

const rebuildHint = "becky-daw-engine: playing a drum machine requires the audio build.\n" +
	"Rebuild with:  go build -tags audio ./cmd/daw-engine\n" +
	"  (Windows: set CC=C:\\msys64\\mingw64\\bin\\gcc.exe first)\n" +
	"Tip: add --schedule to print the event timing without sound (works in this build)."

func playMachineAudio(_ *drummachine.Machine, _ *audioengine.MachineKit, _, _ int) int {
	fmt.Fprintln(os.Stderr, rebuildHint)
	return 2
}

func playPadAudio(_ *drummachine.Machine, _ *audioengine.MachineKit, _, _, _ int) int {
	fmt.Fprintln(os.Stderr, rebuildHint)
	return 2
}
