package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/edl"
)

// importReport is the JSON contract for --import.
type importReport struct {
	Source     string   `json:"source"`   // the edit file that was read
	Format     string   `json:"format"`   // vegas-edl-txt | fcp7-xml
	Output     string   `json:"output"`   // the Reel JSON written
	Clips      int      `json:"clips"`    // number of cuts imported
	Duration   float64  `json:"duration"` // total edit length, seconds
	FPS        float64  `json:"fps,omitempty"`
	Unresolved []string `json:"unresolved,omitempty"` // media that could not be found on disk
	Warnings   []string `json:"warnings,omitempty"`
}

// runImport turns an already-cut Vegas/FCP7 edit into a becky Reel JSON, which
// every downstream becky surface already understands: becky-review-native opens
// it with Load Reel, becky-subtitle captions it, becky-otio exports it back out.
func runImport(editPath, out string) {
	res, err := edl.ImportTimeline(editPath)
	if err != nil {
		beckyio.Fatalf("import %s: %v", filepath.Base(editPath), err)
	}

	outPath := resolveImportOut(editPath, out, res.Reel.Name)
	b, err := json.MarshalIndent(res.Reel, "", "  ")
	if err != nil {
		beckyio.Fatalf("encode reel: %v", err)
	}
	if err := os.WriteFile(outPath, append(b, '\n'), 0o644); err != nil {
		beckyio.Fatalf("write %s: %v", outPath, err)
	}

	rep := importReport{
		Source:     mustAbsPath(editPath),
		Format:     res.Format,
		Output:     mustAbsPath(outPath),
		Clips:      len(res.Reel.Clips),
		Duration:   round3(res.Reel.Duration()),
		FPS:        round3(res.FPS),
		Unresolved: res.Unresolved,
	}
	if len(res.Unresolved) > 0 {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf(
			"%d source file(s) not found on disk — the reel imported but will not play until the media is located",
			len(res.Unresolved)))
	}
	beckyio.PrintJSON(rep)
}

// resolveImportOut decides where the Reel JSON goes: an explicit .json path is
// used as-is, a directory gets "<name>.reel.json", and an empty value writes
// alongside the edit file.
func resolveImportOut(editPath, out, name string) string {
	if out == "" {
		return filepath.Join(filepath.Dir(editPath), name+".reel.json")
	}
	if strings.EqualFold(filepath.Ext(out), ".json") {
		return out
	}
	return filepath.Join(out, name+".reel.json")
}

func mustAbsPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

func round3(f float64) float64 { return float64(int(f*1000+0.5)) / 1000 }
