package audioengine

import (
	"errors"
	"testing"
)

func TestStubBackend_degradesNeverPanics(t *testing.T) {
	var b AudioBackend = StubBackend{}

	devs, err := b.Enumerate()
	if !errors.Is(err, ErrNativeUnavailable) {
		t.Errorf("Enumerate: expected ErrNativeUnavailable, got %v", err)
	}
	if len(devs) != 0 {
		t.Errorf("Enumerate: expected no devices from the stub, got %d", len(devs))
	}
	if err := b.Start(Selection{}); !errors.Is(err, ErrNativeUnavailable) {
		t.Errorf("Start: expected ErrNativeUnavailable, got %v", err)
	}
	if err := b.Stop(); !errors.Is(err, ErrNativeUnavailable) {
		t.Errorf("Stop: expected ErrNativeUnavailable, got %v", err)
	}
}

func TestStubPluginHost_degradesNeverPanics(t *testing.T) {
	var h PluginHost = StubPluginHost{}

	desc, err := h.Scan(`C:\plugins\Odin2.clap`)
	if !errors.Is(err, ErrNativeUnavailable) {
		t.Errorf("Scan: expected ErrNativeUnavailable, got %v", err)
	}
	if desc.Name != "" || desc.Format != "" {
		t.Errorf("Scan: expected zero descriptor on degrade, got %+v", desc)
	}
	handle, err := h.Instantiate(`C:\plugins\Odin2.clap`, 48000, 512)
	if !errors.Is(err, ErrNativeUnavailable) {
		t.Errorf("Instantiate: expected ErrNativeUnavailable, got %v", err)
	}
	if handle != 0 {
		t.Errorf("Instantiate: expected zero handle on degrade, got %d", handle)
	}
}

func TestPluginFormats_known(t *testing.T) {
	// Guard the two declared formats so the native shim and Go side agree.
	if FormatCLAP != "clap" {
		t.Errorf("FormatCLAP = %q want clap", FormatCLAP)
	}
	if FormatVST3 != "vst3" {
		t.Errorf("FormatVST3 = %q want vst3", FormatVST3)
	}
}
