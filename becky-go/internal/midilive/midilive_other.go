//go:build !windows

package midilive

// Non-Windows stubs. becky's live-MIDI output rides the Windows winmm API, which
// has no Linux/macOS equivalent here, so every entry point degrades to
// ErrUnsupportedOS rather than failing the build or panicking. This is what keeps
// `go build ./...` / `go test ./...` green on becky's Ubuntu CI. The pure-Go
// pattern builder (BuildDrumPattern et al. in midilive.go) still works everywhere.

func listPorts() ([]Port, error) { return nil, ErrUnsupportedOS }

func openIndex(int) (*OutPort, error) { return nil, ErrUnsupportedOS }

func openNamed(string) (*OutPort, error) { return nil, ErrUnsupportedOS }

func (p *OutPort) send(uint32) error { return ErrUnsupportedOS }

func (p *OutPort) close() error { return nil }

// Virtual-port creation rides the Windows teVirtualMIDI driver, which has no
// Linux/macOS equivalent. Creating degrades to ErrUnsupportedOS; a zero port's
// SendBytes/Close behave like any closed port so callers don't special-case OS.

func createVirtualPort(string) (*VirtualPort, error) { return nil, ErrUnsupportedOS }

func (v *VirtualPort) sendBytes([]byte) error { return ErrVirtualPortClosed }

func (v *VirtualPort) close() error { return nil }

func virtualMIDIVersion() (string, string, error) { return "", "", ErrUnsupportedOS }
