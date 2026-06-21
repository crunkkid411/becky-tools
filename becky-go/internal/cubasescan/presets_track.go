package cubasescan

import (
	"encoding/binary"
	"strings"
)

// presets_track.go splits a Steinberg `.trackpreset` (a whole Cubase channel-strip
// preset that BUNDLES several VST3 plugins + their state) into per-plugin entries.
// Where presets.go handles ONE `.vstpreset` (header + class id + 'Comp' chunk), a
// .trackpreset is a Cubase CONTAINER: it embeds N VST3 component states (the insert
// chain / channel-strip modules) plus the channel's plugin names and routing. So the
// deliverable here is: "for this channel strip, here are the N plugins in chain order,
// each with its class id and a best-effort state-chunk byte range."
//
// FORMAT RESEARCH (2026-06-21). The .trackpreset / .cpr binary layout is NOT publicly
// documented by Steinberg (confirmed: the only community RE project,
// omeriko9/Cubase-Project-File-Reverse-Engineering, targets Cubase SX2 — PRE-VST3 — and
// is incomplete; Steinberg ships no spec). What IS authoritative and stable is the VST3
// preset format itself (steinbergmedia/vst3_public_sdk source/vst/vstpresetfile.h), and a
// host that persists a plugin's state writes that SAME wrapper: the bytes begin with the
// ASCII magic "VST3", an int32 version, a char[32] class id (the component FUID as 32 hex
// chars), an int64 chunk-list offset, then the data chunks, then at that offset a "List"
// trailer of entries (each: char[4] id e.g. "Comp"/"Cont"/"Info", int64 offset, int64
// size). presets.go already parses exactly this. A .trackpreset is therefore, in practice,
// a sequence of these VST3 preset regions glued together with Cubase's own framing around
// them. So the ROBUST extraction is: find every "VST3" magic marker in the container, and
// for each one parse the embedded preset region the SAME way ParseVSTPreset does (reusing
// the proven header+List logic), bounding each region by the start of the NEXT marker.
// Plugin NAMES are then paired to each region by file-offset proximity using the existing
// string extractor (AllStrings) + the curated pluginDict (FindPlugins), since Cubase stores
// the readable module/plugin name near its state. This is deliberately NOT a false-precise
// "I decoded the Cubase container" parser — chunk-region boundaries between adjacent
// embedded presets are HEURISTIC (the next "VST3" marker, or end-of-file). Degrade-never-
// crash on junk: a buffer with no markers yields an empty ChannelStrip, ok=false.

// vst3Magic is the 4-byte marker that opens every embedded VST3 preset region.
const vst3Magic = "VST3"

// PluginState is one plugin in a channel strip: which plugin (ClassID, the 32-char VST3
// component FUID), its serialized settings (Chunk — the bytes to hand IComponent::setState),
// a best-effort human-readable Name, and where in the container its preset region began.
type PluginState struct {
	Name      string `json:"name"`       // best-effort display name, "" if none paired
	ClassID   string `json:"class_id"`   // VST3 component FUID (32 chars)
	CompBytes int    `json:"comp_bytes"` // size of the state chunk (0 = none found)
	Offset    int    `json:"offset"`     // byte offset of this region's "VST3" magic in the container
	Chunk     []byte `json:"-"`          // the raw 'Comp' state chunk to transplant
}

// ChannelStrip is a parsed .trackpreset: the ordered list of plugins it bundles.
type ChannelStrip struct {
	Plugins []PluginState `json:"plugins"`
	// PluginNames is the deduped set of recognized plugin names found anywhere in the
	// container (a forensic cross-check independent of the per-region pairing).
	PluginNames []string `json:"plugin_names"`
}

// findVST3Markers returns the byte offsets of every "VST3" magic in data, in order. A
// region is considered a candidate preset start only if there's room for at least the
// 48-byte header (magic + version + classID + list-offset).
func findVST3Markers(data []byte) []int {
	var offs []int
	mag := []byte(vst3Magic)
	for i := 0; i+48 <= len(data); i++ {
		if data[i] == mag[0] && data[i+1] == mag[1] && data[i+2] == mag[2] && data[i+3] == mag[3] {
			offs = append(offs, i)
		}
	}
	return offs
}

// parsePresetRegion parses ONE embedded VST3 preset that begins at data[start:], whose
// List-trailer offset is interpreted RELATIVE to start (each embedded preset wraps its own
// chunks the way a standalone .vstpreset does). limit bounds the region (start of the next
// marker, or len(data)) so a corrupt list offset can't read into the next plugin. Returns
// the class id, the 'Comp' chunk, and ok=false on a malformed header.
func parsePresetRegion(data []byte, start, limit int) (classID string, chunk []byte, ok bool) {
	if limit > len(data) {
		limit = len(data)
	}
	if start < 0 || start+48 > limit {
		return "", nil, false
	}
	region := data[start:limit]
	if string(region[0:4]) != vst3Magic {
		return "", nil, false
	}
	classID = strings.TrimRight(string(region[8:40]), "\x00 ")
	listOff := binary.LittleEndian.Uint64(region[40:48])
	if listOff == 0 || listOff+8 > uint64(len(region)) {
		return classID, nil, true // valid header, no usable chunk list
	}
	p := int(listOff)
	if string(region[p:p+4]) != "List" {
		return classID, nil, true
	}
	count := binary.LittleEndian.Uint32(region[p+4 : p+8])
	p += 8
	for i := uint32(0); i < count; i++ {
		if p+20 > len(region) {
			break
		}
		id := string(region[p : p+4])
		off := binary.LittleEndian.Uint64(region[p+4 : p+12])
		size := binary.LittleEndian.Uint64(region[p+12 : p+20])
		p += 20
		if id == "Comp" && off+size <= uint64(len(region)) {
			return classID, region[off : off+size], true
		}
	}
	return classID, nil, true
}

// namePos pairs a recognized plugin name with its byte offset in the container.
type namePos struct {
	name string
	off  int
}

// locateNames returns each recognized name with its first byte offset (ASCII or UTF-16LE),
// sorted by offset. Names not found in the bytes are dropped.
func locateNames(data []byte, names []string) []namePos {
	var out []namePos
	for _, n := range names {
		if idx := indexNear(data, n); idx >= 0 {
			out = append(out, namePos{name: n, off: idx})
		}
	}
	// stable sort by offset (small N — insertion sort keeps deps minimal + deterministic).
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].off < out[j-1].off; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// assignNames pairs each plugin region (given by its marker offset, in chain order) with a
// recognized name. Cubase writes a module's readable name JUST BEFORE its state, so the rule
// is: each region claims the nearest still-UNUSED name at-or-before its marker; if none
// precedes it, it falls back to the nearest unused name after it. Each name is used at most
// once, so adjacent regions can't both grab the same label. Deterministic.
func assignNames(regionOffsets []int, named []namePos) []string {
	out := make([]string, len(regionOffsets))
	used := make([]bool, len(named))
	for i, ro := range regionOffsets {
		best := -1
		bestDist := -1
		// prefer a preceding, unused name (closest before the marker).
		for k, np := range named {
			if used[k] || np.off > ro {
				continue
			}
			d := ro - np.off
			if bestDist < 0 || d < bestDist {
				bestDist, best = d, k
			}
		}
		// fall back to the nearest unused name after the marker.
		if best < 0 {
			for k, np := range named {
				if used[k] {
					continue
				}
				d := np.off - ro
				if bestDist < 0 || d < bestDist {
					bestDist, best = d, k
				}
			}
		}
		if best >= 0 {
			out[i] = named[best].name
			used[best] = true
		}
	}
	return out
}

// indexNear finds the byte offset of name as either ASCII or UTF-16LE bytes, whichever
// occurs first (-1 if neither). Cheap and deterministic.
func indexNear(data []byte, name string) int {
	ai := indexBytes(data, []byte(name))
	ui := indexBytes(data, utf16LEBytes(name))
	switch {
	case ai < 0:
		return ui
	case ui < 0:
		return ai
	case ai < ui:
		return ai
	default:
		return ui
	}
}

// utf16LEBytes encodes an ASCII string as UTF-16LE (each char followed by 0x00).
func utf16LEBytes(s string) []byte {
	out := make([]byte, 0, len(s)*2)
	for i := 0; i < len(s); i++ {
		out = append(out, s[i], 0x00)
	}
	return out
}

// indexBytes is bytes.Index without importing bytes (keep deps minimal); returns -1 if not
// found or needle is empty.
func indexBytes(hay, needle []byte) int {
	if len(needle) == 0 || len(needle) > len(hay) {
		return -1
	}
	for i := 0; i+len(needle) <= len(hay); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			if hay[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// ParseTrackPreset splits a Cubase .trackpreset container into its per-plugin states, in
// chain (file) order. For each embedded "VST3" preset region it extracts the class id, the
// 'Comp' state chunk, and a best-effort plugin name paired by offset proximity (via the
// curated pluginDict). ok=false means the container held no parseable VST3 region (junk or a
// non-trackpreset). Degrade-never-crash: a malformed region is skipped, not fatal.
func ParseTrackPreset(data []byte) (ChannelStrip, bool) {
	var cs ChannelStrip
	if len(data) < 48 {
		return cs, false
	}
	// Recognized plugin names across the whole container (forensic cross-check + the pool
	// the per-region pairing draws from).
	cs.PluginNames = FindPlugins(AllStrings(data, 4))

	markers := findVST3Markers(data)
	if len(markers) == 0 {
		return cs, false
	}
	// First pass: parse every region, keeping the marker offsets of the ones that parsed.
	type parsed struct {
		classID string
		chunk   []byte
		off     int
	}
	var regions []parsed
	for i, start := range markers {
		limit := len(data)
		if i+1 < len(markers) {
			limit = markers[i+1]
		}
		classID, chunk, ok := parsePresetRegion(data, start, limit)
		if !ok {
			continue
		}
		regions = append(regions, parsed{classID: classID, chunk: chunk, off: start})
	}
	if len(regions) == 0 {
		return cs, false
	}
	// Second pass: assign names globally (each name used once, preferring the one written
	// just before each region's marker), then build the ordered plugin list.
	offsets := make([]int, len(regions))
	for i, r := range regions {
		offsets[i] = r.off
	}
	names := assignNames(offsets, locateNames(data, cs.PluginNames))
	for i, r := range regions {
		cs.Plugins = append(cs.Plugins, PluginState{
			ClassID:   r.classID,
			CompBytes: len(r.chunk),
			Offset:    r.off,
			Chunk:     r.chunk,
			Name:      names[i],
		})
	}
	if len(cs.Plugins) == 0 {
		return cs, false
	}
	return cs, true
}
