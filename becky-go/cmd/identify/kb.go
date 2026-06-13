// kb.go — load the enrolled knowledge base directory into in-memory records.
//
// Layout (per 05-becky-identify.md):
//
//	<kb>/entities/<name>.json        # {name, aliases, description}
//	<kb>/voice-prints/<name>/*.wav   # reference voice clips
//	<kb>/locations/<name>/*.jpg      # reference frames
//	<kb>/locations/<name>/*.json     # {name, hash|perceptual_hash, ...} precomputed
//	<kb>/face-prints/<name>/*.jpg    # reference faces (read but unused — no model)
//
// Names come from the directory name; entities/*.json supplies a friendly display
// name + description when present (e.g. dir "defendant" -> name "Defendant").
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"becky-go/internal/beckyio"
)

// Knowledge is the loaded knowledge base.
type Knowledge struct {
	Entities  map[string]Entity // keyed by lowercased dir/entity key
	Voices    []VoicePrint
	Locations []LocationPrint
	Faces     []FacePrint
}

// Entity is the metadata record from entities/<name>.json.
type Entity struct {
	Name        string   `json:"name"`
	Aliases     []string `json:"aliases"`
	Description string   `json:"description"`
}

// VoicePrint is one enrolled voice identity and its reference clip paths.
type VoicePrint struct {
	Key   string   // lowercased dir name
	Name  string   // display name
	Clips []string // absolute .wav paths
}

// LocationPrint is one enrolled location: reference frame paths plus any
// precomputed perceptual hashes loaded from sidecar JSON.
type LocationPrint struct {
	Key    string   // lowercased dir name
	Name   string   // display name
	Frames []string // absolute .jpg/.png paths to hash at runtime
	Hashes []uint64 // precomputed hashes parsed from *.json sidecars
}

// loadKnowledgeBase walks the KB directory and returns the populated records.
func loadKnowledgeBase(kbDir string, verbose bool) (Knowledge, error) {
	kb := Knowledge{Entities: map[string]Entity{}}

	kb.Entities = loadEntities(filepath.Join(kbDir, "entities"), verbose)

	voices, err := loadVoicePrints(filepath.Join(kbDir, "voice-prints"), kb.Entities)
	if err != nil {
		return kb, fmt.Errorf("load voice-prints: %w", err)
	}
	kb.Voices = voices

	locations, err := loadLocations(filepath.Join(kbDir, "locations"), kb.Entities)
	if err != nil {
		return kb, fmt.Errorf("load locations: %w", err)
	}
	kb.Locations = locations

	faces, err := loadFacePrints(filepath.Join(kbDir, "face-prints"), kb.Entities)
	if err != nil {
		return kb, fmt.Errorf("load face-prints: %w", err)
	}
	kb.Faces = faces

	return kb, nil
}

// loadEntities reads entities/*.json into a map keyed by lowercased file stem.
// Missing/unreadable entity files are tolerated (the dir name is the fallback).
func loadEntities(dir string, verbose bool) map[string]Entity {
	out := map[string]Entity{}
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return out
	}
	for _, f := range files {
		data, rerr := os.ReadFile(f)
		if rerr != nil {
			beckyio.Logf(verbose, "warning: read entity %s: %v", f, rerr)
			continue
		}
		var e Entity
		if jerr := json.Unmarshal(data, &e); jerr != nil {
			beckyio.Logf(verbose, "warning: parse entity %s: %v", f, jerr)
			continue
		}
		key := strings.ToLower(stem(f))
		out[key] = e
	}
	return out
}

// loadVoicePrints reads each <dir>/<name>/ subdirectory of .wav clips.
func loadVoicePrints(dir string, entities map[string]Entity) ([]VoicePrint, error) {
	subdirs, err := listSubdirs(dir)
	if err != nil {
		return nil, err
	}
	var prints []VoicePrint
	for _, sub := range subdirs {
		key := strings.ToLower(filepath.Base(sub))
		clips := globAny(sub, "*.wav")
		if len(clips) == 0 {
			continue // no clips -> nothing to enroll
		}
		sort.Strings(clips)
		prints = append(prints, VoicePrint{
			Key:   key,
			Name:  displayName(key, entities),
			Clips: clips,
		})
	}
	sort.Slice(prints, func(i, j int) bool { return prints[i].Key < prints[j].Key })
	return prints, nil
}

// FacePrint is one enrolled face identity and its reference image paths.
type FacePrint struct {
	Key   string   // lowercased dir name
	Name  string   // display name
	Faces []string // absolute .jpg/.jpeg/.png paths
}

// loadFacePrints reads each <dir>/<name>/ subdirectory of reference face images.
func loadFacePrints(dir string, entities map[string]Entity) ([]FacePrint, error) {
	subdirs, err := listSubdirs(dir)
	if err != nil {
		return nil, err
	}
	var prints []FacePrint
	for _, sub := range subdirs {
		key := strings.ToLower(filepath.Base(sub))
		imgs := append(globAny(sub, "*.jpg"), globAny(sub, "*.jpeg")...)
		imgs = append(imgs, globAny(sub, "*.png")...)
		if len(imgs) == 0 {
			continue
		}
		sort.Strings(imgs)
		prints = append(prints, FacePrint{
			Key:   key,
			Name:  displayName(key, entities),
			Faces: imgs,
		})
	}
	sort.Slice(prints, func(i, j int) bool { return prints[i].Key < prints[j].Key })
	return prints, nil
}

// loadLocations reads each <dir>/<name>/ subdirectory: image frames are kept for
// runtime hashing, and any *.json sidecars are parsed for precomputed hashes.
func loadLocations(dir string, entities map[string]Entity) ([]LocationPrint, error) {
	subdirs, err := listSubdirs(dir)
	if err != nil {
		return nil, err
	}
	var prints []LocationPrint
	for _, sub := range subdirs {
		key := strings.ToLower(filepath.Base(sub))
		frames := append(globAny(sub, "*.jpg"), globAny(sub, "*.jpeg")...)
		frames = append(frames, globAny(sub, "*.png")...)
		sort.Strings(frames)
		hashes := loadLocationHashes(sub)
		if len(frames) == 0 && len(hashes) == 0 {
			continue
		}
		prints = append(prints, LocationPrint{
			Key:    key,
			Name:   displayName(key, entities),
			Frames: frames,
			Hashes: hashes,
		})
	}
	sort.Slice(prints, func(i, j int) bool { return prints[i].Key < prints[j].Key })
	return prints, nil
}

// locationSidecar is the precomputed-hash JSON shape. Both "hash" and
// "perceptual_hash" are accepted; values may be 16-char hex or decimal uint64.
type locationSidecar struct {
	Hash           string `json:"hash"`
	PerceptualHash string `json:"perceptual_hash"`
}

// loadLocationHashes parses *.json sidecars in a location dir for stored hashes.
func loadLocationHashes(dir string) []uint64 {
	var hashes []uint64
	for _, f := range globAny(dir, "*.json") {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var sc locationSidecar
		if json.Unmarshal(data, &sc) != nil {
			continue
		}
		raw := sc.PerceptualHash
		if raw == "" {
			raw = sc.Hash
		}
		if h, ok := parseHash(raw); ok {
			hashes = append(hashes, h)
		}
	}
	return hashes
}

// displayName resolves a dir key to a friendly name: the entity record's Name if
// present, otherwise the dir key Title-cased ("defendant" -> "Defendant").
func displayName(key string, entities map[string]Entity) string {
	if e, ok := entities[key]; ok && strings.TrimSpace(e.Name) != "" {
		return e.Name
	}
	return titleize(key)
}

func titleize(s string) string {
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.ReplaceAll(s, "-", " ")
	parts := strings.Fields(s)
	for i, p := range parts {
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	if len(parts) == 0 {
		return s
	}
	return strings.Join(parts, " ")
}

// listSubdirs returns the immediate subdirectories of dir (empty if dir absent).
func listSubdirs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, filepath.Join(dir, e.Name()))
		}
	}
	return dirs, nil
}

// globAny returns case-insensitive matches by globbing both lower and upper ext.
func globAny(dir, pattern string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range []string{pattern, strings.ToUpper(pattern)} {
		matches, _ := filepath.Glob(filepath.Join(dir, p))
		for _, m := range matches {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	return out
}

func stem(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
