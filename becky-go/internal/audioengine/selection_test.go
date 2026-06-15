package audioengine

import "testing"

// idOrNil returns a chosen device's ID, or "" when nil — keeps the table tests
// terse and avoids nil-deref in assertions.
func idOrNil(d *Device) string {
	if d == nil {
		return ""
	}
	return d.ID
}

func TestSelectDefaults_rule(t *testing.T) {
	builtinOut := Device{ID: "builtin-out", Kind: KindOutput, IsDefault: true}
	builtinIn := Device{ID: "builtin-in", Kind: KindInput, IsDefault: true}
	ifaceOut := Device{ID: "iface-out", Kind: KindOutput, IsInterface: true}
	ifaceIn := Device{ID: "iface-in", Kind: KindInput, IsInterface: true}

	cases := []struct {
		name    string
		devices []Device
		wantIn  string
		wantOut string
	}{
		{
			name:    "interface present -> interface chosen for both",
			devices: []Device{builtinOut, builtinIn, ifaceOut, ifaceIn},
			wantIn:  "iface-in",
			wantOut: "iface-out",
		},
		{
			name:    "interface absent -> built-in chosen for both",
			devices: []Device{builtinOut, builtinIn},
			wantIn:  "builtin-in",
			wantOut: "builtin-out",
		},
		{
			name:    "interface input only -> interface in, built-in out",
			devices: []Device{builtinOut, builtinIn, ifaceIn},
			wantIn:  "iface-in",
			wantOut: "builtin-out",
		},
		{
			name:    "interface output only -> interface out, built-in in",
			devices: []Device{builtinOut, builtinIn, ifaceOut},
			wantIn:  "builtin-in",
			wantOut: "iface-out",
		},
		{
			name:    "no devices -> nothing chosen (degrade)",
			devices: nil,
			wantIn:  "",
			wantOut: "",
		},
		{
			name:    "output only -> output chosen, input nil (degrade)",
			devices: []Device{builtinOut},
			wantIn:  "",
			wantOut: "builtin-out",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SelectDefaults(c.devices)
			if idOrNil(got.Input) != c.wantIn {
				t.Errorf("input: got %q want %q", idOrNil(got.Input), c.wantIn)
			}
			if idOrNil(got.Output) != c.wantOut {
				t.Errorf("output: got %q want %q", idOrNil(got.Output), c.wantOut)
			}
			if got.Note == "" {
				t.Error("expected a non-empty plain-language note")
			}
		})
	}
}

func TestSelectDefaults_multipleInterfaces_deterministicTiebreak(t *testing.T) {
	// Two interfaces, neither OS-default: the lowest ID wins, deterministically.
	devices := []Device{
		{ID: "iface-b-out", Kind: KindOutput, IsInterface: true},
		{ID: "iface-a-out", Kind: KindOutput, IsInterface: true},
		{ID: "iface-a-in", Kind: KindInput, IsInterface: true},
		{ID: "iface-b-in", Kind: KindInput, IsInterface: true},
	}
	got := SelectDefaults(devices)
	if idOrNil(got.Output) != "iface-a-out" {
		t.Errorf("output tiebreak: got %q want iface-a-out (lowest id)", idOrNil(got.Output))
	}
	if idOrNil(got.Input) != "iface-a-in" {
		t.Errorf("input tiebreak: got %q want iface-a-in (lowest id)", idOrNil(got.Input))
	}
}

func TestSelectDefaults_osDefaultBeatsLowerID(t *testing.T) {
	// Among same-tier built-ins, the OS-default wins even if its ID sorts later.
	devices := []Device{
		{ID: "aaa-out", Kind: KindOutput},                  // lower id, NOT default
		{ID: "zzz-out", Kind: KindOutput, IsDefault: true}, // higher id, IS default
	}
	got := SelectDefaults(devices)
	if idOrNil(got.Output) != "zzz-out" {
		t.Errorf("os-default should win: got %q want zzz-out", idOrNil(got.Output))
	}
}

func TestSelectDefaults_interfaceBeatsDefaultBuiltin(t *testing.T) {
	// The pro interface is preferred even when a built-in is the OS default —
	// the SPEC rule is "prefer the interface", full stop.
	devices := []Device{
		{ID: "builtin-out", Kind: KindOutput, IsDefault: true},
		{ID: "iface-out", Kind: KindOutput, IsInterface: true},
	}
	got := SelectDefaults(devices)
	if idOrNil(got.Output) != "iface-out" {
		t.Errorf("interface must beat default built-in: got %q want iface-out", idOrNil(got.Output))
	}
}

func TestSelectDefaults_deterministic(t *testing.T) {
	devices := []Device{
		{ID: "iface-out", Kind: KindOutput, IsInterface: true},
		{ID: "iface-in", Kind: KindInput, IsInterface: true},
		{ID: "builtin-out", Kind: KindOutput, IsDefault: true},
		{ID: "builtin-in", Kind: KindInput, IsDefault: true},
	}
	first := SelectDefaults(devices)
	for i := 0; i < 5; i++ {
		got := SelectDefaults(devices)
		if idOrNil(got.Input) != idOrNil(first.Input) || idOrNil(got.Output) != idOrNil(first.Output) {
			t.Fatalf("non-deterministic selection on run %d", i)
		}
		if got.Note != first.Note {
			t.Fatalf("non-deterministic note on run %d", i)
		}
	}
}

func TestSelectDefaults_doesNotMutateInput(t *testing.T) {
	devices := []Device{
		{ID: "b-out", Kind: KindOutput, IsInterface: true},
		{ID: "a-out", Kind: KindOutput, IsInterface: true},
	}
	before := []Device{devices[0], devices[1]}
	_ = SelectDefaults(devices)
	for i := range devices {
		if devices[i] != before[i] {
			t.Errorf("input slice mutated at %d: %+v != %+v", i, devices[i], before[i])
		}
	}
}
