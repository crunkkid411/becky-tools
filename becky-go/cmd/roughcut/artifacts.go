package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/proc"
)

// writeArtifacts emits the four reviewable files: library.yaml (the manifest),
// cut.yaml (the edit itself, readable in a text editor), vegas_cut.json (what
// the Vegas script assembles) and qa.json (the honesty fields).
func writeArtifacts(out, dir string, clips []clip, events []event, markers []markerOut, dropped, cutAsRetake []qaCue, totalKeep, tl float64) error {
	// ---- vegas_cut.json ----------------------------------------------------
	fps, w, h := 30.0, 1920, 1080
	if len(clips) > 0 {
		if clips[0].FPS > 0 {
			fps = clips[0].FPS
		}
		if clips[0].Width > 0 {
			w, h = clips[0].Width, clips[0].Height
		}
	}
	type region struct {
		T     float64 `json:"t"`
		Len   float64 `json:"len"`
		Label string  `json:"label"`
	}
	var regions []region
	for _, c := range clips {
		first, last := -1, -1
		for i := range events {
			if !strings.EqualFold(filepath.Base(events[i].Source), filepath.Base(c.Path)) {
				continue
			}
			if first < 0 {
				first = i
			}
			last = i
		}
		if first < 0 {
			continue
		}
		end := events[last].TLStart + (events[last].Out - events[last].In)
		regions = append(regions, region{events[first].TLStart, end - events[first].TLStart, c.Stem})
	}
	vj, _ := json.MarshalIndent(map[string]any{
		"version":   "1",
		"project":   filepath.Base(dir) + " rough cut",
		"fps":       fps,
		"width":     w,
		"height":    h,
		"save_path": filepath.Join(out, "rough_cut.veg"),
		"events":    events,
		"markers":   markers,
		"regions":   regions,
	}, "", "  ")
	if err := os.WriteFile(filepath.Join(out, "vegas_cut.json"), vj, 0o644); err != nil {
		return err
	}

	// ---- cut.yaml ----------------------------------------------------------
	var b strings.Builder
	fmt.Fprintf(&b, "# rough cut IR - %s\n# %d events, %.1fs of %.1fs timeline. Sources are READ-ONLY.\n", filepath.Base(dir), len(events), totalKeep, tl)
	for i, e := range events {
		fmt.Fprintf(&b, "- source: %s\n  in: %.3f\n  out: %.3f\n  timeline: %.3f\n", filepath.Base(e.Source), e.In, e.Out, e.TLStart)
		if e.Dialogue != "" {
			d := e.Dialogue
			if len(d) > 200 {
				d = d[:200] + "..."
			}
			fmt.Fprintf(&b, "  dialogue: %q\n", d)
		}
		_ = i
	}
	if err := os.WriteFile(filepath.Join(out, "cut.yaml"), []byte(b.String()), 0o644); err != nil {
		return err
	}

	// ---- library.yaml ------------------------------------------------------
	var lb strings.Builder
	fmt.Fprintf(&lb, "project: %s\nfootage_summary: \"\"\nuser_context: \"\"\nclips:\n", filepath.Base(dir))
	for _, c := range clips {
		fmt.Fprintf(&lb, "  - file: %s\n    creation_time: %q\n    duration: %.1f\n    fps: %.3f\n", c.Path, c.CreationTime, c.Duration, c.FPS)
		if c.SRT != "" {
			fmt.Fprintf(&lb, "    srt: %s\n", c.SRT)
		}
		fmt.Fprintf(&lb, "    cut_json: %s\n    audio_profile: %s\n", filepath.Join(out, c.Stem+".cut.json"), filepath.Join(out, c.Stem+".audio_profile.json"))
	}
	if err := os.WriteFile(filepath.Join(out, "library.yaml"), []byte(lb.String()), 0o644); err != nil {
		return err
	}

	// ---- qa.json -----------------------------------------------------------
	qj, _ := json.MarshalIndent(map[string]any{
		"timeline_seconds":  tl,
		"keep_seconds":      totalKeep,
		"events":            len(events),
		"dropped_cues":      dropped,
		"retake_cues_cut":   cutAsRetake,
		"dropped_cue_count": len(dropped),
	}, "", "  ")
	return os.WriteFile(filepath.Join(out, "qa.json"), qj, 0o644)
}

// launchVegasPro starts Vegas Pro headless with the timeline-builder script.
// The script reads BECKY_ROUGHCUT_JSON, builds the timeline, saves the .veg and
// exits - Jordan walks away and comes back to a populated project.
func launchVegasPro(out, scriptOverride string, verbose bool) error {
	jsonPath, _ := filepath.Abs(filepath.Join(out, "vegas_cut.json"))
	script := scriptOverride
	if script == "" {
		if exe, err := os.Executable(); err == nil {
			cand := filepath.Join(filepath.Dir(exe), "..", "..", "vegas", "BeckyRoughCut.cs")
			if _, err := os.Stat(cand); err == nil {
				script, _ = filepath.Abs(cand)
			}
		}
	}
	if script == "" {
		return fmt.Errorf("vegas/BeckyRoughCut.cs not found; pass --vegas-script")
	}
	vegasExe := ""
	matches, _ := filepath.Glob(`C:\Program Files\VEGAS\VEGAS Pro *\vegas1*.exe`)
	if len(matches) > 0 {
		vegasExe = matches[len(matches)-1]
	}
	if vegasExe == "" {
		return fmt.Errorf("no Vegas Pro install found under C:\\Program Files\\VEGAS")
	}
	cmd := exec.Command(vegasExe, "-SCRIPT:"+script)
	cmd.Env = append(os.Environ(), "BECKY_ROUGHCUT_JSON="+jsonPath)
	proc.NoWindow(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launch vegas: %w", err)
	}
	beckyio.Logf(verbose, "vegas launched (%s) building %s", filepath.Base(vegasExe), jsonPath)
	return nil
}
