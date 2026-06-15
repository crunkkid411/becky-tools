package audioengine

import "sort"

// The device-default rule (SPEC-BECKY-DAW-ENGINE.md): when Jordan's pro AUDIO
// INTERFACE is plugged in, DEFAULT to it for BOTH input and output; fall back to
// the laptop built-in only when the interface is absent. This file implements
// that rule in pure Go so it is unit-testable with zero hardware.

// Selection is the chosen input/output pair plus a plain-language note for the
// non-developer report. It is a typed result so "no devices" degrades to an
// empty selection + explanatory note instead of a panic (CLAUDE.md §2).
type Selection struct {
	Input  *Device `json:"input"`
	Output *Device `json:"output"`
	// Note explains the choice (or the degrade) in plain language.
	Note string `json:"note"`
}

// SelectDefaults applies the device-default rule to a device list and returns the
// chosen input and output. It is deterministic: the same device list always
// yields the same selection (the tiebreak below is total and stable).
//
// Rule, per Kind, in priority order:
//  1. Prefer a device with IsInterface == true (the pro interface), if any.
//  2. Otherwise fall back to a built-in device (IsInterface == false).
//
// Within a tier the winner is chosen by a deterministic tiebreak (see
// pickPreferred): OS-default first, then the lowest ID lexicographically. This
// makes "multiple interfaces plugged in" resolve to one stable choice.
func SelectDefaults(devices []Device) Selection {
	in := devicesOfKind(devices, KindInput)
	out := devicesOfKind(devices, KindOutput)
	sel := Selection{
		Input:  pickPreferred(in),
		Output: pickPreferred(out),
	}
	sel.Note = selectionNote(sel)
	return sel
}

// devicesOfKind returns the subset of devices matching the given Kind. It copies
// matched elements into a fresh slice so callers cannot mutate the input
// (immutability — coding-style.md), and so pointers are stable for the result.
func devicesOfKind(devices []Device, kind DeviceKind) []Device {
	out := make([]Device, 0, len(devices))
	for _, d := range devices {
		if d.Kind == kind {
			out = append(out, d)
		}
	}
	return out
}

// pickPreferred chooses one device from a single-Kind list per the rule: any
// interface beats any built-in; ties broken deterministically. Returns nil when
// the list is empty (degrade path — caller records it in the Note).
func pickPreferred(devices []Device) *Device {
	if len(devices) == 0 {
		return nil
	}
	interfaces := make([]Device, 0, len(devices))
	builtins := make([]Device, 0, len(devices))
	for _, d := range devices {
		if d.IsInterface {
			interfaces = append(interfaces, d)
		} else {
			builtins = append(builtins, d)
		}
	}
	if len(interfaces) > 0 {
		return tiebreak(interfaces)
	}
	return tiebreak(builtins)
}

// tiebreak deterministically selects one device from a non-empty, same-tier list:
// an OS-default device wins; otherwise the lowest ID lexicographically wins. The
// returned pointer is to a fresh copy so the result is independent of the input
// slice's backing array.
func tiebreak(devices []Device) *Device {
	ranked := make([]Device, len(devices))
	copy(ranked, devices)
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].IsDefault != ranked[j].IsDefault {
			return ranked[i].IsDefault // true sorts before false
		}
		return ranked[i].ID < ranked[j].ID
	})
	chosen := ranked[0]
	return &chosen
}

// selectionNote produces a plain-language explanation of the choice for the
// non-developer report, including the degrade case when a side is missing.
func selectionNote(sel Selection) string {
	switch {
	case sel.Input == nil && sel.Output == nil:
		return "no audio devices found — engine will stay silent until one is connected"
	case sel.Output == nil:
		return "no output device found — input selected but nothing to play through"
	case sel.Input == nil:
		return "no input device found — output selected but nothing to record from"
	default:
		return sideNote("input", *sel.Input) + "; " + sideNote("output", *sel.Output)
	}
}

// sideNote describes one chosen side (input or output) and why it was picked.
func sideNote(side string, d Device) string {
	if d.IsInterface {
		return side + ": " + d.DisplayName() + " (pro audio interface — preferred)"
	}
	return side + ": " + d.DisplayName() + " (built-in — no interface present)"
}
