package cubasescan

import (
	"encoding/binary"
	"strings"
)

// presets.go extracts the actual DIALED-IN SETTINGS of a plugin from a Steinberg
// `.vstpreset` file. A VST3 plugin's settings are an opaque binary "state chunk" the
// plugin serializes itself; the .vstpreset is the standard, host-agnostic wrapper
// around it (header + the plugin's class id + the chunk). becky does NOT recreate the
// knob values — it transplants this exact chunk into the SAME plugin hosted in
// becky-canvas (the apply side already exists: becky-vst `vst.state.load` →
// IComponent::setState). So "my 1176 the way I always have it" comes across verbatim,
// as long as becky hosts the same plugin.
//
// .vstpreset layout (VST3 SDK): "VST3" | int32 version | char[32] classID |
// int64 listOffset … then the data chunks … then at listOffset: "List" | int32 count |
// count×( char[4] chunkID | int64 offset | int64 size ). The "Comp" chunk is the
// processor state setState() consumes. All ints little-endian.

// VSTPreset is a parsed .vstpreset: which plugin it is (ClassID) and its settings
// (CompChunk — the bytes to hand IComponent::setState).
type VSTPreset struct {
	ClassID   string `json:"class_id"`
	CompBytes int    `json:"comp_bytes"` // size of the state chunk (0 = none found)
	chunk     []byte
}

// Chunk returns the raw component state (the settings) to transplant into the plugin.
func (p VSTPreset) Chunk() []byte { return p.chunk }

// ParseVSTPreset reads a .vstpreset's header + the "Comp" (processor) state chunk.
// ok=false means it isn't a valid VST3 preset. Degrade-never-crash on truncation.
func ParseVSTPreset(data []byte) (VSTPreset, bool) {
	if len(data) < 48 || string(data[0:4]) != "VST3" {
		return VSTPreset{}, false
	}
	classID := strings.TrimRight(string(data[8:40]), "\x00 ")
	listOff := binary.LittleEndian.Uint64(data[40:48])
	out := VSTPreset{ClassID: classID}
	if listOff == 0 || listOff+8 > uint64(len(data)) {
		return out, true // valid header, but no usable chunk list
	}
	p := int(listOff)
	if string(data[p:p+4]) != "List" {
		return out, true
	}
	count := binary.LittleEndian.Uint32(data[p+4 : p+8])
	p += 8
	for i := uint32(0); i < count; i++ {
		if p+20 > len(data) {
			break
		}
		id := string(data[p : p+4])
		off := binary.LittleEndian.Uint64(data[p+4 : p+12])
		size := binary.LittleEndian.Uint64(data[p+12 : p+20])
		p += 20
		if id == "Comp" && off+size <= uint64(len(data)) {
			out.chunk = data[off : off+size]
			out.CompBytes = int(size)
			return out, true
		}
	}
	return out, true
}
