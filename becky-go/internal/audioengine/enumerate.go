package audioengine

// enumerate.go provides a tiny DeviceEnumerator seam plus a deterministic STUB
// enumerator. The pure-Go foundation cannot touch real hardware (that is the
// native Phase-2 AudioBackend.Enumerate), so the headless CLI demo
// (cmd/daw-engine) enumerates through this seam: the stub returns a fixed,
// deterministic device set so the device-selection rule can be SHOWN and TESTED
// end-to-end with no cgo and no audio library.

// DeviceEnumerator is the narrow interface the CLI depends on to obtain devices.
// In Phase-2 the native AudioBackend satisfies it (its Enumerate has the same
// signature); today the StubEnumerator below satisfies it for the headless demo.
type DeviceEnumerator interface {
	Enumerate() ([]Device, error)
}

// StubEnumerator returns a fixed, deterministic device set for the headless demo
// and tests. It models the common scenarios: a laptop built-in pair plus, when
// WithInterface is true, a pro audio-interface pair — so the selection rule's
// "prefer the interface" branch can be exercised without hardware. It is NOT the
// real device source; that is the native AudioBackend (SPEC §1.2).
type StubEnumerator struct {
	// WithInterface, when true, includes the pro interface input+output devices
	// so the demo shows the interface being preferred. When false, only the
	// built-in pair is present (the fallback branch).
	WithInterface bool
}

// Enumerate returns the deterministic device set. The order is fixed so output is
// reproducible (becky's determinism invariant); the selection rule does not
// depend on order, but the --list output does.
func (e StubEnumerator) Enumerate() ([]Device, error) {
	devices := []Device{
		{
			ID:         "builtin-out",
			Name:       "Built-in Output (Speakers)",
			Kind:       KindOutput,
			IsDefault:  true,
			Channels:   2,
			SampleRate: 48000,
		},
		{
			ID:         "builtin-in",
			Name:       "Built-in Microphone",
			Kind:       KindInput,
			IsDefault:  true,
			Channels:   1,
			SampleRate: 48000,
		},
	}
	if e.WithInterface {
		devices = append(devices,
			Device{
				ID:          "iface-out",
				Name:        "Pro Audio Interface (Main Out)",
				Kind:        KindOutput,
				IsInterface: true,
				Channels:    2,
				SampleRate:  48000,
			},
			Device{
				ID:          "iface-in",
				Name:        "Pro Audio Interface (Line In)",
				Kind:        KindInput,
				IsInterface: true,
				Channels:    2,
				SampleRate:  48000,
			},
		)
	}
	return devices, nil
}

// Compile-time assertions: both the stub enumerator and the native-shaped
// StubBackend satisfy the DeviceEnumerator seam, so the CLI can be driven by
// either without code changes when Phase-2 lands.
var (
	_ DeviceEnumerator = StubEnumerator{}
	_ DeviceEnumerator = StubBackend{}
)
