// kitload.go — LoadKitFromSFZ and LoadKitFromFolder: bridge the orphaned sampler
// foundation (internal/kitimport, internal/samplelib) into the drum machine model.
//
// Design rules (CLAUDE.md): pure Go, offline, deterministic, degrade-never-crash.
// A kit with missing samples is NOT an error; only an unreadable file is an error.
//
// Choke source-of-truth: sampler.Sound.ChokeGroup / OffBy (from the SFZ `group` /
// `off_by` opcodes) are the authoritative values. Pad.ChokeGroup is set to
// Sound.ChokeGroup for backward compatibility with callers that don't look at Sound.
package drummachine

import (
	"fmt"
	"sort"
	"strings"

	"becky-go/internal/kitimport"
	"becky-go/internal/pathx"
	"becky-go/internal/samplelib"
	"becky-go/internal/sampler"
)

// LoadKitResult is returned by LoadKitFromSFZ and LoadKitFromFolder.
// Notes are human-readable non-fatal observations (missing samples, overflow, etc.).
type LoadKitResult struct {
	Kit   Kit
	Notes []string
}

// LoadKitFromSFZ parses an SFZ file (or a DecentSampler .dspreset file) and maps
// its Sounds onto the 16 pads of a Kit. The first Sound goes to pad 0, the second
// to pad 1, and so on; any extra Sounds beyond PadCount are silently noted. A kit
// with fewer than PadCount Sounds is valid — unused pads stay at DefaultKit values.
//
// Pad.Sound is set to the parsed Sound; Pad.SamplePath is set to the first variant's
// path (for human readability / backward compat); Pad.ChokeGroup mirrors Sound.ChokeGroup.
//
// Only an unreadable/unparseable file is a fatal error. Missing sample files inside
// the SFZ produce a note in LoadKitResult.Notes (Variant.Missing will be true).
func LoadKitFromSFZ(path string) (LoadKitResult, error) {
	var sounds []sampler.Sound
	var notes []string

	lp := strings.ToLower(pathx.Base(path))
	switch {
	case strings.HasSuffix(lp, ".dspreset"):
		r, err := kitimport.ParseDecentSampler(path)
		if err != nil {
			return LoadKitResult{}, fmt.Errorf("drummachine: parse dspreset %s: %w", pathx.Base(path), err)
		}
		sounds = r.Sounds
		notes = r.Notes
	default: // .sfz and anything else treated as SFZ
		r, err := kitimport.ParseSFZ(path)
		if err != nil {
			return LoadKitResult{}, fmt.Errorf("drummachine: parse sfz %s: %w", pathx.Base(path), err)
		}
		sounds = r.Sounds
		notes = r.Notes
	}

	kit := DefaultKit()
	kitName := stripExt(pathx.Base(path))
	if kitName != "" {
		kit.Name = kitName
	}

	if len(sounds) > PadCount {
		notes = append(notes, fmt.Sprintf("%d sounds in file; only first %d mapped to pads", len(sounds), PadCount))
		sounds = sounds[:PadCount]
	}

	for i, snd := range sounds {
		cp := snd // copy so each pad owns its Sound
		pad := &kit.Pads[i]
		pad.Sound = &cp
		pad.SamplePath = soundFirstPath(&cp)
		// Mirror choke from Sound → Pad.ChokeGroup for backward-compat callers.
		pad.ChokeGroup = cp.ChokeGroup
		if cp.Name != "" {
			pad.Name = cp.Name
		}
	}

	return LoadKitResult{Kit: kit, Notes: notes}, nil
}

// LoadKitFromFolder scans a directory for audio samples, classifies them by role,
// and maps them onto the 16 GM-labelled pads. At most one sample per role is
// chosen (highest-confidence first, then alphabetical for determinism).
//
// Only the top-level folder is scanned (not recursive). Each matching sample is
// wrapped in a minimal sampler.Sound (one layer, one variant, one-shot mode).
//
// Degrade-never-crash: an unreadable directory returns a (DefaultKit, error) so the
// caller can present the error but still have a usable kit.
func LoadKitFromFolder(dir string) (LoadKitResult, error) {
	idx, err := samplelib.Scan(dir, samplelib.ScanOptions{Recursive: false})
	if err != nil {
		return LoadKitResult{Kit: DefaultKit(), Notes: []string{"scan error: " + err.Error()}}, err
	}

	// Role → GM pad mapping (matches DefaultKit's gmDefaults layout).
	roleToPad := map[string]int{
		samplelib.RoleKick:  0,
		samplelib.RoleSnare: 1,
		samplelib.RoleHat:   2, // closed hat → pad 2; open hat handled separately → pad 3
		samplelib.RoleClap:  4,
		samplelib.RoleTom:   6, // low tom
		samplelib.RoleCrash: 9,
		samplelib.RoleRide:  10,
		samplelib.RolePerc:  15,
		samplelib.RoleBass:  8, // hi tom pad repurposed for sub/808
		samplelib.RoleFX:    11,
	}

	// Group samples by role, sort for determinism (high confidence first, then path).
	byRole := map[string][]samplelib.Sample{}
	for _, s := range idx.Samples {
		byRole[s.Role] = append(byRole[s.Role], s)
	}
	for role := range byRole {
		sort.Slice(byRole[role], func(i, j int) bool {
			a, b := byRole[role][i], byRole[role][j]
			if a.RoleConfidence != b.RoleConfidence {
				return confidenceRank(a.RoleConfidence) > confidenceRank(b.RoleConfidence)
			}
			return a.Path < b.Path
		})
	}

	kit := DefaultKit()
	kit.Name = pathx.Base(dir)

	var notes []string
	assigned := map[int]bool{}

	// First pass: assign best sample per role to its canonical pad.
	for role, pad := range roleToPad {
		samples := byRole[role]
		if len(samples) == 0 {
			continue
		}
		if assigned[pad] {
			continue
		}
		best := samples[0]
		snd := singleVariantSound(best)
		kit.Pads[pad].Sound = &snd
		kit.Pads[pad].SamplePath = best.Path
		if snd.Name != "" {
			kit.Pads[pad].Name = snd.Name
		}
		assigned[pad] = true
		if len(samples) > 1 {
			notes = append(notes, fmt.Sprintf("pad %d (%s): used %s; %d other %s samples available",
				pad, kit.Pads[pad].Name, pathx.Base(best.Path), len(samples)-1, role))
		}
	}

	// Second pass: if there are multiple hat samples, assign the second one to pad 3
	// (open hat), keeping the classic closed/open hat choke relationship.
	if hatSamples := byRole[samplelib.RoleHat]; len(hatSamples) > 1 && !assigned[3] {
		best := hatSamples[1]
		snd := singleVariantSound(best)
		kit.Pads[3].Sound = &snd
		kit.Pads[3].SamplePath = best.Path
		assigned[3] = true
	}

	total := len(idx.Samples)
	used := len(assigned)
	if total == 0 {
		notes = append(notes, "no audio samples found in "+dir)
	} else {
		notes = append(notes, fmt.Sprintf("scanned %d samples, assigned %d to pads", total, used))
	}

	return LoadKitResult{Kit: kit, Notes: notes}, nil
}

// ---- helpers ----------------------------------------------------------------

// soundFirstPath returns the sample path of the first variant in the Sound,
// or "" when the Sound has no playable variants.
func soundFirstPath(snd *sampler.Sound) string {
	if snd == nil {
		return ""
	}
	for _, layer := range snd.Layers {
		for _, v := range layer.RoundRobin {
			if v.SamplePath != "" {
				return v.SamplePath
			}
		}
	}
	return ""
}

// singleVariantSound wraps a samplelib.Sample in a minimal sampler.Sound: one
// default velocity layer (1..127), one variant, one-shot mode (drum default).
func singleVariantSound(s samplelib.Sample) sampler.Sound {
	variant := sampler.Variant{
		SamplePath:     s.Path,
		PitchKeycenter: sampler.DefaultKeycenter,
	}
	layer := sampler.Layer{
		VelLo:      1,
		VelHi:      127,
		RoundRobin: []sampler.Variant{variant},
		RRMode:     sampler.Sequential,
	}
	snd := sampler.Sound{
		Name:    stripExt(pathx.Base(s.Path)),
		Layers:  []sampler.Layer{layer},
		OneShot: true,
	}
	return snd.Normalize()
}

// confidenceRank maps a confidence string to a numeric sort key (higher = better).
func confidenceRank(c string) int {
	switch c {
	case samplelib.ConfHigh:
		return 2
	case samplelib.ConfLow:
		return 1
	default:
		return 0
	}
}

// stripExt strips the final extension from a file name.
func stripExt(name string) string {
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		return name[:i]
	}
	return name
}
