//go:build audio

package main

// machine_audio.go — the REAL sound path for --play-machine / --play-pad, compiled
// only under `-tags audio`. It enumerates the real devices (preferring Jordan's pro
// interface via the existing SelectDefaults rule) and calls the cgo
// audioengine.PlayMachineLoop / PlayPadOneShot (render → temp WAV → becky_play_wav).

import (
	"fmt"
	"os"

	"becky-go/internal/audioengine"
	"becky-go/internal/drummachine"
)

// playMachineAudio plays the machine's pattern through the chosen output device.
func playMachineAudio(m *drummachine.Machine, kit *audioengine.MachineKit, sampleRate, loops int) int {
	_, sel, _, enumErr := chooseDevices()
	if enumErr != nil {
		fmt.Fprintln(os.Stderr, "machine: device enumeration failed (will use OS default):", enumErr)
		sel.Output = nil
	}
	if err := audioengine.PlayMachineLoop(m, kit, sel.Output, sampleRate, loops); err != nil {
		fmt.Fprintln(os.Stderr, "machine:", err)
		return 1
	}
	fmt.Println("Done.")
	return 0
}

// playPadAudio auditions a single pad through the chosen output device.
func playPadAudio(m *drummachine.Machine, kit *audioengine.MachineKit, pad, vel, sampleRate int) int {
	_, sel, _, enumErr := chooseDevices()
	if enumErr != nil {
		fmt.Fprintln(os.Stderr, "machine: device enumeration failed (will use OS default):", enumErr)
		sel.Output = nil
	}
	if err := audioengine.PlayPadOneShot(m, kit, sel.Output, pad, vel, sampleRate); err != nil {
		fmt.Fprintln(os.Stderr, "machine:", err)
		return 1
	}
	fmt.Println("Done.")
	return 0
}
