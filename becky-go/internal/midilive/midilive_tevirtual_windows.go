//go:build windows

package midilive

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// teVirtualMIDI — the driver that ships with loopMIDI (Tobias Erichsen) and lets
// an application CREATE its own virtual MIDI port. becky uses it so Jordan never
// has to open loopMIDI and add a port by hand: becky makes a port named "becky"
// itself, and Maschine (or any MIDI app) sees it as a normal MIDI device.
//
// No cgo — the driver DLL is reached with a LazyDLL syscall, exactly like the
// winmm code in midilive_windows.go. The 64-bit Go build needs the 64-bit DLL:
// teVirtualMIDI64.dll, installed in C:\Windows\System32 by the loopMIDI setup.
// NewLazySystemDLL resolves it from the System32 search path; if it is missing
// (driver not installed) the first .Find()/.Call() reports it and we degrade to
// ErrVirtualMIDIUnavailable rather than crash.
//
// API (from teVirtualMIDI.h, all WINAPI/__stdcall):
//
//	LPVM_MIDI_PORT virtualMIDICreatePortEx2(LPCWSTR portName, LPVM_MIDI_DATA_CB cb,
//	                                        DWORD_PTR inst, DWORD maxSysex, DWORD flags);
//	BOOL           virtualMIDISendData(LPVM_MIDI_PORT port, LPBYTE data, DWORD len);
//	void           virtualMIDIClosePort(LPVM_MIDI_PORT port);
//	LPCWSTR        virtualMIDIGetVersion(WORD* major, WORD* minor, WORD* rel, WORD* build);
//	LPCWSTR        virtualMIDIGetDriverVersion(WORD* major, WORD* minor, WORD* rel, WORD* build);
var (
	teVM = windows.NewLazySystemDLL("teVirtualMIDI64.dll")

	procVMCreatePortEx2    = teVM.NewProc("virtualMIDICreatePortEx2")
	procVMSendData         = teVM.NewProc("virtualMIDISendData")
	procVMClosePort        = teVM.NewProc("virtualMIDIClosePort")
	procVMGetVersion       = teVM.NewProc("virtualMIDIGetVersion")
	procVMGetDriverVersion = teVM.NewProc("virtualMIDIGetDriverVersion")
)

// teVirtualMIDI flags (TE_VM_FLAGS_* in the header). We make a TRANSMIT-capable
// port (becky sends into it; an instrument receives) and ask the driver to parse
// outgoing data into valid MIDI commands. PARSE_RX must NOT be combined with a
// NULL callback, so we deliberately omit it — a NULL callback puts the port in
// polling mode, which is exactly right for a send-only port (we never read).
const (
	teVMFlagsParseTX           = 0x2 // TE_VM_FLAGS_PARSE_TX
	teVMFlagsInstantiateTXOnly = 0x8 // TE_VM_FLAGS_INSTANTIATE_TX_ONLY

	// createFlags: a transmit-only port with TX parsing. (PARSE_RX is omitted on
	// purpose — see above; it is invalid with a NULL receive callback.)
	teVMCreateFlags = teVMFlagsParseTX | teVMFlagsInstantiateTXOnly

	// teVMDefaultSysexSize is TE_VM_DEFAULT_SYSEX_SIZE (65535): the max length of
	// a single buffer the driver will hand back. We never receive, but the create
	// call still wants a sane maximum; the header's own default is used.
	teVMDefaultSysexSize = 65535
)

// ensureTeVirtualMIDI verifies the driver DLL and the create entry point load.
// It returns a typed, wrapped error (so callers can errors.Is it) carrying the
// real loader reason when the driver is not installed.
func ensureTeVirtualMIDI() error {
	if err := teVM.Load(); err != nil {
		return fmt.Errorf("%w: loading teVirtualMIDI64.dll: %v", ErrVirtualMIDIUnavailable, err)
	}
	if err := procVMCreatePortEx2.Find(); err != nil {
		return fmt.Errorf("%w: virtualMIDICreatePortEx2 not found: %v", ErrVirtualMIDIUnavailable, err)
	}
	return nil
}

func createVirtualPort(name string) (*VirtualPort, error) {
	if name == "" {
		name = "becky"
	}
	if err := ensureTeVirtualMIDI(); err != nil {
		return nil, err
	}

	wName, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, fmt.Errorf("midilive: invalid virtual port name %q: %w", name, err)
	}

	// virtualMIDICreatePortEx2(portName, callback=NULL, instance=0, maxSysex, flags)
	// A NULL callback => polling mode (we only ever transmit, so we never poll).
	h, _, callErr := procVMCreatePortEx2.Call(
		uintptr(unsafe.Pointer(wName)),
		0, // LPVM_MIDI_DATA_CB callback = NULL (send-only, polling mode)
		0, // DWORD_PTR dwCallbackInstance = 0
		uintptr(teVMDefaultSysexSize),
		uintptr(teVMCreateFlags),
	)
	if h == 0 {
		// callErr is the GetLastError-derived reason (e.g. ERROR_ALREADY_EXISTS if
		// a port of this name is already open). Surface it verbatim — honesty.
		return nil, fmt.Errorf("%w: virtualMIDICreatePortEx2(%q) failed: %v",
			ErrVirtualMIDIUnavailable, name, callErr)
	}
	return &VirtualPort{name: name, handle: h, open: true}, nil
}

func (v *VirtualPort) sendBytes(b []byte) error {
	if v == nil || !v.open || v.handle == 0 {
		return ErrVirtualPortClosed
	}
	if len(b) == 0 {
		return nil
	}
	// virtualMIDISendData(port, LPBYTE data, DWORD length) -> BOOL (0 == failure).
	ok, _, callErr := procVMSendData.Call(
		v.handle,
		uintptr(unsafe.Pointer(&b[0])),
		uintptr(len(b)),
	)
	if ok == 0 {
		return fmt.Errorf("midilive: virtualMIDISendData failed: %v", callErr)
	}
	return nil
}

func (v *VirtualPort) close() error {
	if v == nil || !v.open || v.handle == 0 {
		return nil
	}
	// virtualMIDIClosePort returns void; it tears the port down in the driver.
	procVMClosePort.Call(v.handle)
	v.open = false
	v.handle = 0
	return nil
}

// virtualMIDIVersion is the OS-delegated implementation behind VirtualMIDIVersion.
// It returns both the client-DLL and the kernel-driver versions; either lookup
// failing yields the wrapped ErrVirtualMIDIUnavailable.
func virtualMIDIVersion() (dll, driver string, err error) {
	dll, err = teVirtualMIDIVersion()
	if err != nil {
		return "", "", err
	}
	driver, err = teVirtualMIDIDriverVersion()
	if err != nil {
		return dll, "", err
	}
	return dll, driver, nil
}

// teVirtualMIDIVersion returns the teVirtualMIDI DLL (client) version string as
// "major.minor.release.build", or an error if the driver can't be loaded. Used
// for the diagnostic line on --create-port so a failure is explainable.
func teVirtualMIDIVersion() (string, error) {
	if err := teVM.Load(); err != nil {
		return "", fmt.Errorf("%w: %v", ErrVirtualMIDIUnavailable, err)
	}
	if err := procVMGetVersion.Find(); err != nil {
		return "", fmt.Errorf("%w: virtualMIDIGetVersion not found: %v", ErrVirtualMIDIUnavailable, err)
	}
	var major, minor, release, build uint16
	procVMGetVersion.Call(
		uintptr(unsafe.Pointer(&major)),
		uintptr(unsafe.Pointer(&minor)),
		uintptr(unsafe.Pointer(&release)),
		uintptr(unsafe.Pointer(&build)),
	)
	return fmt.Sprintf("%d.%d.%d.%d", major, minor, release, build), nil
}

// teVirtualMIDIDriverVersion returns the installed teVirtualMIDI DRIVER version
// (as opposed to the client DLL). A zero/empty driver version is a strong signal
// the kernel driver isn't actually running even if the DLL loaded.
func teVirtualMIDIDriverVersion() (string, error) {
	if err := teVM.Load(); err != nil {
		return "", fmt.Errorf("%w: %v", ErrVirtualMIDIUnavailable, err)
	}
	if err := procVMGetDriverVersion.Find(); err != nil {
		return "", fmt.Errorf("%w: virtualMIDIGetDriverVersion not found: %v", ErrVirtualMIDIUnavailable, err)
	}
	var major, minor, release, build uint16
	procVMGetDriverVersion.Call(
		uintptr(unsafe.Pointer(&major)),
		uintptr(unsafe.Pointer(&minor)),
		uintptr(unsafe.Pointer(&release)),
		uintptr(unsafe.Pointer(&build)),
	)
	return fmt.Sprintf("%d.%d.%d.%d", major, minor, release, build), nil
}
