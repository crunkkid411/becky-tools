package audioengine

import "testing"

func TestStubEnumerator_withInterface(t *testing.T) {
	devs, err := StubEnumerator{WithInterface: true}.Enumerate()
	if err != nil {
		t.Fatalf("enumerate: %v", err)
	}
	if len(devs) != 4 {
		t.Fatalf("expected 4 devices (built-in pair + interface pair), got %d", len(devs))
	}
	// The selection rule over this set must pick the interface for both sides.
	sel := SelectDefaults(devs)
	if idOrNil(sel.Input) != "iface-in" || idOrNil(sel.Output) != "iface-out" {
		t.Errorf("interface set: got in=%q out=%q want iface-in/iface-out",
			idOrNil(sel.Input), idOrNil(sel.Output))
	}
}

func TestStubEnumerator_withoutInterface(t *testing.T) {
	devs, err := StubEnumerator{WithInterface: false}.Enumerate()
	if err != nil {
		t.Fatalf("enumerate: %v", err)
	}
	if len(devs) != 2 {
		t.Fatalf("expected 2 built-in devices, got %d", len(devs))
	}
	sel := SelectDefaults(devs)
	if idOrNil(sel.Input) != "builtin-in" || idOrNil(sel.Output) != "builtin-out" {
		t.Errorf("built-in set: got in=%q out=%q want builtin-in/builtin-out",
			idOrNil(sel.Input), idOrNil(sel.Output))
	}
}

func TestStubEnumerator_deterministicOrder(t *testing.T) {
	first, err := StubEnumerator{WithInterface: true}.Enumerate()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		got, err := StubEnumerator{WithInterface: true}.Enumerate()
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(first) {
			t.Fatalf("run %d: length changed", i)
		}
		for j := range got {
			if got[j] != first[j] {
				t.Fatalf("run %d: device %d differs: %+v != %+v", i, j, got[j], first[j])
			}
		}
	}
}

func TestStubEnumerator_satisfiesDeviceEnumerator(t *testing.T) {
	// The CLI depends only on the narrow DeviceEnumerator seam.
	var e DeviceEnumerator = StubEnumerator{WithInterface: true}
	if _, err := e.Enumerate(); err != nil {
		t.Fatalf("seam enumerate: %v", err)
	}
}
