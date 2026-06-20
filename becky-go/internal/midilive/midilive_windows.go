//go:build windows

package midilive

import (
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// winmm MIDI output API, reached with NO cgo via a LazyDLL. These entry points
// are all we need to enumerate, open, stream to, and close a port.
var (
	winmm                 = windows.NewLazySystemDLL("winmm.dll")
	procMidiOutGetNumDevs = winmm.NewProc("midiOutGetNumDevs")
	procMidiOutGetDevCaps = winmm.NewProc("midiOutGetDevCapsW")
	procMidiOutOpen       = winmm.NewProc("midiOutOpen")
	procMidiOutShortMsg   = winmm.NewProc("midiOutShortMsg")
	procMidiOutClose      = winmm.NewProc("midiOutClose")
	procMidiOutReset      = winmm.NewProc("midiOutReset")
)

// midiOutCapsW mirrors the Win32 MIDIOUTCAPSW struct used by midiOutGetDevCapsW.
// The product name is a fixed 32-UTF16 array (MAXPNAMELEN).
type midiOutCapsW struct {
	wMid           uint16
	wPid           uint16
	vDriverVersion uint32
	szPname        [32]uint16
	wTechnology    uint16
	wVoices        uint16
	wNotes         uint16
	wChannelMask   uint16
	dwSupport      uint32
}

// mmsyserrNoError is MMSYSERR_NOERROR (0): the winmm "success" return code.
const mmsyserrNoError = 0

func listPorts() ([]Port, error) {
	n, _, _ := procMidiOutGetNumDevs.Call()
	count := int(int32(n)) // count fits an int; cast guards a negative driver return
	if count < 0 {
		count = 0
	}
	ports := make([]Port, 0, count)
	for i := 0; i < count; i++ {
		var caps midiOutCapsW
		rc, _, _ := procMidiOutGetDevCaps.Call(
			uintptr(i),
			uintptr(unsafe.Pointer(&caps)),
			unsafe.Sizeof(caps),
		)
		name := fmt.Sprintf("MIDI device %d", i)
		if uint32(rc) == mmsyserrNoError {
			name = windows.UTF16ToString(caps.szPname[:])
		}
		ports = append(ports, Port{Index: i, Name: name})
	}
	return ports, nil
}

func openIndex(index int) (*OutPort, error) {
	ports, err := listPorts()
	if err != nil {
		return nil, err
	}
	name := fmt.Sprintf("MIDI device %d", index)
	for _, p := range ports {
		if p.Index == index {
			name = p.Name
			break
		}
	}
	var h uintptr
	rc, _, _ := procMidiOutOpen.Call(
		uintptr(unsafe.Pointer(&h)),
		uintptr(index),
		0, // no callback
		0, // no callback instance
		0, // CALLBACK_NULL
	)
	if uint32(rc) != mmsyserrNoError {
		return nil, fmt.Errorf("midilive: midiOutOpen(index=%d) failed: mmRESULT=%d", index, uint32(rc))
	}
	return &OutPort{port: Port{Index: index, Name: name}, handle: h, open: true}, nil
}

func openNamed(substr string) (*OutPort, error) {
	ports, err := listPorts()
	if err != nil {
		return nil, err
	}
	want := strings.ToLower(strings.TrimSpace(substr))
	for _, p := range ports {
		if want == "" || strings.Contains(strings.ToLower(p.Name), want) {
			return openIndex(p.Index)
		}
	}
	return nil, fmt.Errorf("%w: %q (have %s)", ErrNoSuchPort, substr, portNames(ports))
}

func portNames(ports []Port) string {
	names := make([]string, len(ports))
	for i, p := range ports {
		names[i] = p.Name
	}
	return strings.Join(names, ", ")
}

func (p *OutPort) send(msg uint32) error {
	if p == nil || !p.open || p.handle == 0 {
		return ErrPortClosed
	}
	rc, _, _ := procMidiOutShortMsg.Call(p.handle, uintptr(msg))
	if uint32(rc) != mmsyserrNoError {
		return fmt.Errorf("midilive: midiOutShortMsg failed: mmRESULT=%d", uint32(rc))
	}
	return nil
}

func (p *OutPort) close() error {
	if p == nil || !p.open || p.handle == 0 {
		return nil
	}
	// Reset first to silence any notes still ringing (all-notes-off), then close.
	procMidiOutReset.Call(p.handle)
	rc, _, _ := procMidiOutClose.Call(p.handle)
	p.open = false
	p.handle = 0
	if uint32(rc) != mmsyserrNoError {
		return fmt.Errorf("midilive: midiOutClose failed: mmRESULT=%d", uint32(rc))
	}
	return nil
}
