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
