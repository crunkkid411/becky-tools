package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/proc"
)

// writeArtifacts emits the reviewable files: library.yaml (manifest),
// cut.yaml (the edit, readable in a text editor), qa.json (the honesty
// fields) and vegas_cut.json - the four-track layout vegas/BeckyRoughCut.cs
// assembles: his video+audio, quotes video+audio, quotes inserted
// sequentially at their markers.
func writeArtifacts(out, dir string, lay layout, dropped, cutAsRetake []qaCue, totalKeep, tl float64, gains map[string]float64) error {
	fps, w, h := 30.0, 1920, 1080

	// one audible level for the whole main audio track: the median measured
	// gain (per-clip values vary only a few dB on this footage).
	audioGain := 20.0
	if len(gains) > 0 {
		var vs []float64
		for _, v := range gains {
			vs = append(vs, v)
		}
		sort.Float64s(vs)
		audioGain = vs[len(vs)/2]
	}

	vj, _ := json.MarshalIndent(map[string]any{
		"version":       "1",
		"project":       filepath.Base(dir) + " rough cut",
		"fps":           fps,
		"width":         w,
		"height":        h,
		"save_path":     filepath.Join(out, "rough_cut.veg"),
		"events":        lay.Events,
		"quotes":        lay.Quotes,
		"markers":       lay.Markers,
		"regions":       lay.Regions,
		"gains":         gains,
		"audio_gain_db": audioGain,
	}, "", "  ")
	if err := os.WriteFile(filepath.Join(out, "vegas_cut.json"), vj, 0o644); err != nil {
		return err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# rough cut IR - %s\n# %d main events + %d quote inserts, %.1fs timeline. Sources are READ-ONLY.\n", filepath.Base(dir), len(lay.Events), len(lay.Quotes), tl)
	for _, e := range lay.Events {
		fmt.Fprintf(&b, "- source: %s\n  in: %.3f\n  out: %.3f\n  timeline: %.3f\n", baseName(e.Source), e.In, e.Out, e.TL)
		if e.Dialogue != "" {
			d := e.Dialogue
			if len(d) > 200 {
				d = d[:200] + "..."
			}
			fmt.Fprintf(&b, "  dialogue: %q\n", d)
		}
	}
	for _, q := range lay.Quotes {
		fmt.Fprintf(&b, "- quote: %s\n  in: %.3f\n  out: %.3f\n  timeline: %.3f\n", baseName(q.Source), q.In, q.Out, q.TL)
	}
	if err := os.WriteFile(filepath.Join(out, "cut.yaml"), []byte(b.String()), 0o644); err != nil {
		return err
	}

	qj, _ := json.MarshalIndent(map[string]any{
		"timeline_seconds":  tl,
		"keep_seconds":      totalKeep,
		"events":            len(lay.Events),
		"quote_inserts":     len(lay.Quotes),
		"dropped_cues":      dropped,
		"retake_cues_cut":   cutAsRetake,
		"dropped_cue_count": len(dropped),
	}, "", "  ")
	return os.WriteFile(filepath.Join(out, "qa.json"), qj, 0o644)
}

// writePendingMarkers persists the placed subset of pendingMarkers (each
// already carrying its resolved timeline position, TL) so the standalone
// --triage-markers pass can run later - possibly hours later, once the GPU
// is free of the LR-ASD sweep - without re-running the whole detection
// pipeline just to recover which review/retake marker sits where.
func writePendingMarkers(out string, pm []pendingMarker) error {
	b, err := json.MarshalIndent(pm, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, "pending_markers.json"), b, 0o644)
}

// library.yaml is written separately (it needs the clip inventory).
func writeLibrary(out, dir string, clips []clip, summaries map[string]string) error {
	var lb strings.Builder
	fmt.Fprintf(&lb, "project: %s\nfootage_summary: \"\"\nuser_context: \"\"\nclips:\n", filepath.Base(dir))
	for _, c := range clips {
		fmt.Fprintf(&lb, "  - file: %s\n    creation_time: %q\n    duration: %.1f\n    fps: %.3f\n", c.Path, c.CreationTime, c.Duration, c.FPS)
		if c.SRT != "" {
			fmt.Fprintf(&lb, "    srt: %s\n", c.SRT)
		}
		if s := summaries[c.Stem]; s != "" {
			fmt.Fprintf(&lb, "    summary: %q\n", s)
		}
		fmt.Fprintf(&lb, "    dossier: %s\n", filepath.Join(out, c.Stem+".dossier.json"))
		fmt.Fprintf(&lb, "    cut_json: %s\n    audio_profile: %s\n", filepath.Join(out, c.Stem+".cut.json"), filepath.Join(out, c.Stem+".audio_profile.json"))
	}
	return os.WriteFile(filepath.Join(out, "library.yaml"), []byte(lb.String()), 0o644)
}

// launchVegasPro starts Vegas headless with the timeline-builder script.
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
