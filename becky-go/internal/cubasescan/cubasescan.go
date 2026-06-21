// Package cubasescan is a deterministic, offline extractor for Steinberg Cubase
// project/template/preset files (.cpr / .trackpreset / .vstpreset). Jordan doesn't
// remember his plugin chains — they live inside Cubase. The .cpr format is binary and
// undocumented, but the things he needs (the VST plugin NAMES, track names, bus names)
// are stored as readable strings inside it. This pulls them out: scan a file, get the
// plugins it uses + the track/bus labels — so becky can seed his routing rules and
// per-bus FX chains from his ACTUAL templates instead of guessing.
//
// Pure Go, read-only, degrade-never-crash. The heavy lifting is string extraction +
// matching against a curated dictionary of real pro plugins (so the output is signal,
// not noise). The string-extraction core is fully unit-tested on synthetic data; the
// real run is on Jordan's machine where his .cpr files live.
package cubasescan

import (
	"sort"
	"strings"
)

// asciiStrings returns runs of >= minLen printable ASCII bytes (the way `strings`
// does), preserving order.
func asciiStrings(data []byte, minLen int) []string {
	var out []string
	var cur []byte
	flush := func() {
		if len(cur) >= minLen {
			out = append(out, string(cur))
		}
		cur = cur[:0]
	}
	for _, b := range data {
		if b >= 0x20 && b < 0x7f {
			cur = append(cur, b)
		} else {
			flush()
		}
	}
	flush()
	return out
}

// utf16Strings returns runs of >= minLen printable UTF-16LE characters (Cubase stores
// many names as UTF-16: printable byte followed by 0x00). It scans BOTH byte
// alignments (even and odd start) since a string's offset isn't guaranteed even.
func utf16Strings(data []byte, minLen int) []string {
	var out []string
	for _, start := range []int{0, 1} {
		var cur []byte
		flush := func() {
			if len(cur) >= minLen {
				out = append(out, string(cur))
			}
			cur = cur[:0]
		}
		for i := start; i+1 < len(data); i += 2 {
			b, hi := data[i], data[i+1]
			if hi == 0x00 && b >= 0x20 && b < 0x7f {
				cur = append(cur, b)
			} else {
				flush()
			}
		}
		flush()
	}
	return out
}

// AllStrings returns every printable ASCII + UTF-16 run of >= minLen, deduplicated and
// sorted (a forensic dump of the readable content).
func AllStrings(data []byte, minLen int) []string {
	if minLen < 1 {
		minLen = 4
	}
	seen := map[string]bool{}
	var out []string
	for _, s := range append(asciiStrings(data, minLen), utf16Strings(data, minLen)...) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// pluginDict is a curated dictionary of real pro plugin / instrument names (lowercased
// match tokens). Matching against a dictionary keeps the output precise — only actual
// plugins, not every random string. Extend it freely; it's just data.
var pluginDict = []string{
	// FabFilter
	"pro-q", "pro-c", "pro-l", "pro-mb", "pro-ds", "pro-g", "pro-r", "saturn", "fabfilter",
	// Waves
	"ssl", "cla-", "h-comp", "h-eq", "rcompressor", "rbass", "rvox", "c6", "c4", "f6",
	"renaissance", "puigtec", "abbey road", "scheps", "kramer", "waves tune",
	// iZotope
	"ozone", "neutron", "nectar", "rx ", "izotope", "trash", "vocalsynth",
	// instruments
	"serum", "serum 2", "superior drummer", "ezdrummer", "kontakt", "battery", "maschine",
	"omnisphere", "massive", "sylenth", "vital", "spire", "nexus", "kick 2", "phaseplant",
	// drums / amp sims
	"tal-drum", "tal drum", "addictive drums", "drumshell", "neural dsp", "archetype",
	"bias fx", "amplitube", "guitar rig", "ml sound", "stl tones", "toneforge",
	// mix / master / fx
	"valhalla", "soothe", "soothe2", "gullfoss", "oeksound", "tdr nova", "tdr kotelnikov",
	"slate", "virtual mix rack", "fresh air", "decapitator", "echoboy", "littleAlterBoy",
	"little alter boy", "microshift", "crystallizer", "soundtoys", "kilohearts", "snapheap",
	"raum", "supermassive", "shimmer", "pro tools", "auto-tune", "antares", "melodyne",
	"trackspacer", "shaperbox", "cableguys", "fast reveal", "smart:eq", "sonible",
	"bx_", "brainworx", "plugin alliance", "lindell", "purple audio", "api ",
	// Steinberg stock (Cubase 11)
	"frequency", "compressor", "vintage compressor", "tube compressor", "magneto",
	"quadrafuzz", "studioeq", "reverence", "groove agent", "retrologue", "padshop",
	"halion", "vst amp rack", "supervision", "imager", "maximizer", "limiter",
}

// FindPlugins returns the plugin names recognized in a set of strings (deduped,
// preserving the matched display string, sorted). A string matches if it contains a
// dictionary token; the original (untruncated) string is kept so "FabFilter Pro-Q 3"
// is reported in full.
func FindPlugins(strs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range strs {
		low := strings.ToLower(s)
		for _, tok := range pluginDict {
			if strings.Contains(low, tok) {
				key := strings.ToLower(strings.TrimSpace(s))
				if !seen[key] {
					seen[key] = true
					out = append(out, strings.TrimSpace(s))
				}
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// FileReport is what one scanned file yields.
type FileReport struct {
	Path    string   `json:"path"`
	Plugins []string `json:"plugins"`
	Strings int      `json:"strings"` // count of distinct readable strings
}

// Scan extracts the plugins (and a readable-string count) from a file's raw bytes.
func Scan(path string, data []byte) FileReport {
	all := AllStrings(data, 4)
	return FileReport{Path: path, Plugins: FindPlugins(all), Strings: len(all)}
}
