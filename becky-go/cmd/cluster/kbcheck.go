// kbcheck.go — cross-check each cluster's centroid against the enrolled KB (SPEC
// §7.2). If a cluster's center matches a known person above the matching threshold,
// it is labeled is_known=true so the UNKNOWN set is purely the strangers (the real
// "who" question). Reuses the exact KB layout becky-identify uses:
//
//	<kb>/voice-prints/<Name>/*.wav   and   <kb>/face-prints/<Name>/*.jpg
//
// embedded with the same CAM++ / InsightFace runners, then averaged per name.
//
// This is candidate-not-conclusion at the boundary: a match here means "this
// recurring person looks like an ENROLLED person" (so it's probably NOT a new
// stranger); a non-match leaves the cluster as an unnamed Person A for a human.
package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/faceembed"
	"becky-go/internal/pyhelpers"
)

// enrolledPrint is one KB identity's fused (averaged, normalized) embedding.
type enrolledPrint struct {
	name   string
	vector []float64
}

// KBCrossCheck is the per-cluster KB comparison surfaced in the output.
type KBCrossCheck struct {
	BestKnown string  `json:"best_known"` // nearest enrolled name ("" if none enrolled)
	Cosine    float64 `json:"cosine"`     // centroid cosine to that name
	IsKnown   bool    `json:"is_known"`   // true if cosine >= the matching threshold
}

// loadEnrolledPrints embeds the enrolled prints of the given modality from the KB
// directory and fuses them per name. Returns nil (no error) when the KB has none
// of that modality, so the cross-check simply degrades to "no enrolled comparison".
func loadEnrolledPrints(cfg config.Config, kbDir, modality, device string, verbose bool) ([]enrolledPrint, error) {
	switch modality {
	case "voice":
		return loadEnrolledVoices(cfg, kbDir, device, verbose)
	case "face":
		return loadEnrolledFaces(cfg, kbDir, device, verbose)
	default:
		return nil, nil
	}
}

// loadEnrolledVoices reads <kb>/voice-prints/<Name>/*.wav, embeds each name's clips
// with voice_embed.py (VAD-gated), and averages them into one print per name.
func loadEnrolledVoices(cfg config.Config, kbDir, device string, verbose bool) ([]enrolledPrint, error) {
	root := filepath.Join(kbDir, "voice-prints")
	subdirs := listSubdirs(root)
	if len(subdirs) == 0 {
		return nil, nil
	}
	script, err := pyhelpers.Materialize("voice_embed.py", pyhelpers.VoiceEmbed)
	if err != nil {
		return nil, err
	}
	var prints []enrolledPrint
	for _, sub := range subdirs {
		name := dirDisplayName(sub)
		clips := globExt(sub, ".wav")
		var vecs [][]float64
		for _, wav := range clips {
			vec, eerr := runVoiceEmbed(cfg, script, wav, device, verbose)
			if eerr == nil && len(vec) > 0 {
				vecs = append(vecs, normalize(vec))
			}
		}
		if len(vecs) == 0 {
			continue
		}
		prints = append(prints, enrolledPrint{name: name, vector: averageNormalized(vecs)})
		beckyio.Logf(verbose, "  kb voice: %q averaged %d clip(s)", name, len(vecs))
	}
	sort.Slice(prints, func(i, j int) bool { return prints[i].name < prints[j].name })
	return prints, nil
}

// loadEnrolledFaces reads <kb>/face-prints/<Name>/*.jpg|jpeg|png, embeds them via
// the shared faceembed runner, and fuses per name. Degrades to nil if the face
// model/deps are missing (the cross-check is optional).
func loadEnrolledFaces(cfg config.Config, kbDir, device string, verbose bool) ([]enrolledPrint, error) {
	root := filepath.Join(kbDir, "face-prints")
	subdirs := listSubdirs(root)
	if len(subdirs) == 0 {
		return nil, nil
	}
	var imgs []string
	owner := map[string]string{}
	for _, sub := range subdirs {
		name := dirDisplayName(sub)
		for _, img := range append(append(globExt(sub, ".jpg"), globExt(sub, ".jpeg")...), globExt(sub, ".png")...) {
			imgs = append(imgs, img)
			owner[img] = name
		}
	}
	if len(imgs) == 0 {
		return nil, nil
	}
	recs, err := faceembed.Embed(cfg, imgs, device, verbose)
	if err != nil {
		beckyio.Logf(verbose, "  kb face: embed failed (%v); skipping face cross-check", err)
		return nil, nil // optional: degrade rather than fail the whole run
	}
	byName := map[string][][]float64{}
	for _, r := range recs {
		if r.Found && len(r.Vector) > 0 {
			byName[owner[r.Path]] = append(byName[owner[r.Path]], normalize(r.Vector))
		}
	}
	var prints []enrolledPrint
	for name, vs := range byName {
		prints = append(prints, enrolledPrint{name: name, vector: averageNormalized(vs)})
		beckyio.Logf(verbose, "  kb face: %q fused %d image(s)", name, len(vs))
	}
	sort.Slice(prints, func(i, j int) bool { return prints[i].name < prints[j].name })
	return prints, nil
}

// crossCheck compares a cluster centroid to the enrolled prints and returns the
// nearest known name + cosine + whether it clears the matching threshold. With no
// enrolled prints it returns a zero-value check (BestKnown "", IsKnown false).
func crossCheck(cent []float64, prints []enrolledPrint, matchThreshold float64) KBCrossCheck {
	if len(cent) == 0 || len(prints) == 0 {
		return KBCrossCheck{}
	}
	bestName, bestSim := "", -1.0
	for _, p := range prints {
		if s := cosine(cent, p.vector); s > bestSim {
			bestSim, bestName = s, p.name
		}
	}
	return KBCrossCheck{
		BestKnown: bestName,
		Cosine:    round4(bestSim),
		IsKnown:   bestSim >= matchThreshold,
	}
}

// listSubdirs returns the immediate subdirectories of dir (nil if dir is absent).
func listSubdirs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(dirs)
	return dirs
}

// globExt returns case-insensitive matches for an extension in dir.
func globExt(dir, ext string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range []string{"*" + ext, "*" + strings.ToUpper(ext)} {
		matches, _ := filepath.Glob(filepath.Join(dir, p))
		for _, m := range matches {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	sort.Strings(out)
	return out
}

// dirDisplayName is the enrolled identity's name = its directory base name (the KB
// uses the directory name as the person's name, e.g. "John Anthony Clancy").
func dirDisplayName(dir string) string {
	return filepath.Base(dir)
}

func round4(f float64) float64 { return float64(int(f*10000+0.5)) / 10000 }
