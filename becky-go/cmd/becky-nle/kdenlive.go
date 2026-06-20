// kdenlive.go — the headless "drive the REAL kdenlive" surface of becky-nle.
// This is build-tag-neutral (like nle.go) so it compiles in every configuration
// and is exercised by the default `go build ./...`.
//
// The pivot: instead of hand-rolling a video editor, becky generates a VALID
// .kdenlive (MLT) project from a forensic cut-list and either renders it headless
// via the kdenlive-bundled melt.exe OR opens the project in the real kdenlive GUI
// for a human. internal/kdenlive owns the XML + melt; this file owns the CLI glue:
// reading the cut-list, probing sources for the project geometry (reusing becky's
// internal/mediainfo ffprobe wrapper), and printing structured JSON results.
//
// Three verbs (wired in main.go):
//
//	becky-nle --build-project <cutlist.json> [--project out.kdenlive]
//	becky-nle --render        <proj.kdenlive> [--out-file out.mp4] [--vcodec h264_nvenc]
//	becky-nle --open          <proj.kdenlive>
//
// A cut-list can ALSO be assembled inline from a single source (no JSON file):
//
//	becky-nle --build-project --source <video> --in S --out S [--project p.kdenlive]
//
// Degrade-never-crash: a bad cut-list / missing melt / unreadable source returns
// a typed error + a plain-language line and a non-zero exit, never a panic.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"becky-go/internal/config"
	"becky-go/internal/kdenlive"
	"becky-go/internal/mediainfo"
	"becky-go/internal/pathx"
)

// Process exit codes, shared by the headless CLI (main.go, !gui) and the GUI
// build. Defined HERE (build-tag-neutral) so both builds compile: 0 = ok;
// 1 = degraded/failed (missing sidecar/melt, probe/render error); 2 = bad
// invocation (unknown flag / missing required arg / bad cut-list).
const (
	exitOK       = 0
	exitDegraded = 1
	exitBadArgs  = 2
)

// cutList is the JSON input for --build-project: an ordered list of cuts plus an
// optional title + geometry override. Times are SECONDS into each source.
//
//	{
//	  "title": "penguin-bounty",
//	  "clips": [
//	    {"source": "E:/footage/a.mp4", "in": 12.5, "out": 18.0, "name": "the threat"},
//	    {"source": "E:/footage/b.mp4", "in": 4.0,  "out": 9.5}
//	  ]
//	}
type cutList struct {
	Title  string        `json:"title,omitempty"`
	Width  int           `json:"width,omitempty"`  // optional project width override
	Height int           `json:"height,omitempty"` // optional project height override
	FPS    float64       `json:"fps,omitempty"`    // optional project fps override
	Clips  []cutListClip `json:"clips"`
}

type cutListClip struct {
	Source string  `json:"source"`
	In     float64 `json:"in"`
	Out    float64 `json:"out"`
	Name   string  `json:"name,omitempty"`
}

// buildProjectResult is the structured JSON printed by --build-project.
type buildProjectResult struct {
	Project string  `json:"project"` // absolute path of the written .kdenlive
	Clips   int     `json:"clips"`   // number of cuts
	Sources int     `json:"sources"` // number of distinct source files
	Width   int     `json:"width"`   // project geometry actually used
	Height  int     `json:"height"`  //
	FPS     float64 `json:"fps"`     //
	Note    string  `json:"note,omitempty"`
}

// doBuildProject reads a cut-list (from JSON file or the inline single-source
// flags), probes each distinct source for geometry/length, and writes a VALID
// .kdenlive project. Returns an exit code.
func doBuildProject(cutPath, inlineSource string, inlineIn, inlineOut float64, projOut string, stdout, stderr *os.File) int {
	cl, err := loadCutList(cutPath, inlineSource, inlineIn, inlineOut)
	if err != nil {
		fmt.Fprintln(stderr, "becky-nle:", err)
		return exitBadArgs
	}
	if len(cl.Clips) == 0 {
		fmt.Fprintln(stderr, "becky-nle: the cut-list has no clips")
		return exitBadArgs
	}

	proj := kdenlive.NewProject(projectTitle(cl, projOut))
	proj.Width, proj.Height, proj.FPS = cl.Width, cl.Height, cl.FPS

	cfg := config.Load()
	var note string
	probed := map[string]bool{}
	for _, c := range cl.Clips {
		abs := mustAbsPath(c.Source)
		proj.AddClip(kdenlive.Clip{Source: abs, In: c.In, Out: c.Out, Name: c.Name})
		if probed[abs] {
			continue
		}
		probed[abs] = true
		if src, ok := probeSource(cfg, abs); ok {
			proj.SetSource(src)
			// Geometry auto-matches the FIRST source (the becky rule) unless the
			// cut-list overrode it.
			if proj.Width <= 0 || proj.Height <= 0 {
				if info, e := mediainfo.Probe(cfg.FFprobe, abs); e == nil {
					if proj.Width <= 0 {
						proj.Width = info.Width
					}
					if proj.Height <= 0 {
						proj.Height = info.Height
					}
				}
			}
		} else if note == "" {
			note = "ffprobe unavailable or a source was unreadable; using cut-extent length + fallback geometry"
		}
	}

	if projOut == "" {
		projOut = defaultProjectPath(cl, proj)
	}
	projOut = mustAbsPath(projOut)

	if err := kdenlive.WriteProject(proj, projOut); err != nil {
		fmt.Fprintln(stderr, "becky-nle:", friendlyBuildErr(err))
		return exitDegraded
	}

	res := buildProjectResult{
		Project: projOut,
		Clips:   len(proj.Clips),
		Sources: len(probed),
		Width:   projGeomW(proj),
		Height:  projGeomH(proj),
		FPS:     projGeomFPS(proj),
		Note:    note,
	}
	printJSON(stdout, res)
	fmt.Fprintf(stderr, "becky-nle: wrote %s (%d clips, %d sources)\n", pathx.Base(projOut), res.Clips, res.Sources)
	fmt.Fprintf(stderr, "becky-nle: render it with  becky-nle --render %q\n", projOut)
	fmt.Fprintf(stderr, "becky-nle: or open it in kdenlive with  becky-nle --open %q\n", projOut)
	return exitOK
}

// doRender renders an existing .kdenlive to an MP4 via melt (h264_nvenc, libx264
// fallback). outFile="" -> "<proj-dir>/<stem>.mp4".
func doRender(projPath, outFile, vcodec string, stdout, stderr *os.File) int {
	if strings.TrimSpace(projPath) == "" {
		fmt.Fprintln(stderr, "becky-nle: --render needs a .kdenlive project path")
		return exitBadArgs
	}
	projPath = mustAbsPath(projPath)
	if _, err := os.Stat(projPath); err != nil {
		fmt.Fprintln(stderr, "becky-nle: project not found:", projPath)
		return exitDegraded
	}
	if outFile == "" {
		outFile = defaultRenderOutput(projPath)
	}
	outFile = mustAbsPath(outFile)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	res, err := kdenlive.Render(ctx, projPath, outFile, kdenlive.RenderOptions{
		Vcodec:  vcodec,
		Verbose: true, // stream melt progress so a long render shows life
	})
	if err != nil {
		fmt.Fprintln(stderr, "becky-nle:", friendlyRenderErr(err))
		return exitDegraded
	}

	printJSON(stdout, res)
	msg := fmt.Sprintf("becky-nle: rendered %s (%s)", pathx.Base(res.Output), res.Vcodec)
	if res.Note != "" {
		msg += " — " + res.Note
	}
	fmt.Fprintln(stderr, msg)
	return exitOK
}

// doOpen launches the real kdenlive GUI on a project for a human to edit.
func doOpen(projPath string, stderr *os.File) int {
	if strings.TrimSpace(projPath) == "" {
		fmt.Fprintln(stderr, "becky-nle: --open needs a .kdenlive project path")
		return exitBadArgs
	}
	projPath = mustAbsPath(projPath)
	if err := kdenlive.Open(projPath); err != nil {
		fmt.Fprintln(stderr, "becky-nle:", err)
		return exitDegraded
	}
	fmt.Fprintf(stderr, "becky-nle: opened %s in kdenlive\n", pathx.Base(projPath))
	return exitOK
}

// --- helpers ----------------------------------------------------------------

// loadCutList builds a cutList from either a JSON file or the inline single-source
// flags (exactly one path must be provided).
func loadCutList(cutPath, inlineSource string, inlineIn, inlineOut float64) (cutList, error) {
	hasFile := strings.TrimSpace(cutPath) != ""
	hasInline := strings.TrimSpace(inlineSource) != ""

	switch {
	case hasFile && hasInline:
		return cutList{}, fmt.Errorf("pass EITHER a cut-list JSON OR --source/--in/--out, not both")
	case hasFile:
		data, err := os.ReadFile(cutPath)
		if err != nil {
			return cutList{}, fmt.Errorf("can't read cut-list %s: %w", cutPath, err)
		}
		var cl cutList
		if err := json.Unmarshal(data, &cl); err != nil {
			return cutList{}, fmt.Errorf("cut-list %s is not valid JSON: %w", cutPath, err)
		}
		return cl, nil
	case hasInline:
		if inlineOut <= inlineIn {
			return cutList{}, fmt.Errorf("empty range: --out (%.3f) must be greater than --in (%.3f)", inlineOut, inlineIn)
		}
		return cutList{
			Clips: []cutListClip{{Source: inlineSource, In: inlineIn, Out: inlineOut}},
		}, nil
	default:
		return cutList{}, fmt.Errorf("--build-project needs a cut-list JSON path, or --source with --in/--out")
	}
}

// probeSource probes a source for length+fps via ffprobe (internal/mediainfo).
// ok=false when ffprobe is unavailable or the file can't be read — the caller
// then relies on the cut-extent fallback in internal/kdenlive.
func probeSource(cfg config.Config, path string) (kdenlive.Source, bool) {
	if cfg.FFprobe == "" {
		return kdenlive.Source{}, false
	}
	info, err := mediainfo.Probe(cfg.FFprobe, path)
	if err != nil {
		return kdenlive.Source{}, false
	}
	return kdenlive.Source{Path: path, LengthSec: info.Duration, FPS: info.FPS}, true
}

// projectTitle derives a project title: the cut-list's, else the output stem,
// else "becky-compilation".
func projectTitle(cl cutList, projOut string) string {
	if t := strings.TrimSpace(cl.Title); t != "" {
		return t
	}
	if projOut != "" {
		return stem(pathx.Base(projOut))
	}
	return "becky-compilation"
}

// defaultProjectPath builds "<first-source-dir>/<title>.kdenlive" — next to the
// footage (the becky protocol: outputs live next to the source).
func defaultProjectPath(cl cutList, proj *kdenlive.Project) string {
	title := slugify(projectTitle(cl, ""))
	if title == "" {
		title = "becky-compilation"
	}
	name := title + ".kdenlive"
	if len(proj.Clips) > 0 {
		if dir := pathx.Dir(proj.Clips[0].Source); dir != "" {
			return dir + string(filepath.Separator) + name
		}
	}
	return name
}

// defaultRenderOutput builds "<proj-dir>/<stem>.mp4" next to the project file.
func defaultRenderOutput(projPath string) string {
	dir := pathx.Dir(projPath)
	name := stem(pathx.Base(projPath)) + ".mp4"
	if dir == "" {
		return name
	}
	return dir + string(filepath.Separator) + name
}

// slugify lowercases and replaces non-alphanumeric runs with single dashes.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// mustAbsPath returns the absolute form of p, falling back to p on error.
func mustAbsPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

// printJSON writes v as indented JSON + newline to w (best-effort).
func printJSON(w *os.File, v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return
	}
	fmt.Fprintln(w, string(b))
}

// projGeom* report the geometry the project will actually emit (post-fallback),
// mirroring internal/kdenlive's dims()/fps() so the printed result is truthful.
func projGeomW(p *kdenlive.Project) int {
	if p.Width > 0 {
		return p.Width
	}
	return 1280
}
func projGeomH(p *kdenlive.Project) int {
	if p.Height > 0 {
		return p.Height
	}
	return 720
}
func projGeomFPS(p *kdenlive.Project) float64 {
	if p.FPS > 0 {
		return p.FPS
	}
	if len(p.Clips) > 0 {
		if s, ok := p.Sources[p.Clips[0].Source]; ok && s.FPS > 0 {
			return s.FPS
		}
	}
	return kdenlive.DefaultFPS
}

func friendlyBuildErr(err error) string {
	return "couldn't write the kdenlive project — " + err.Error()
}

func friendlyRenderErr(err error) string {
	return "the render failed — " + err.Error() +
		"\n  (need kdenlive's melt.exe; install kdenlive or set BECKY_MELT; ffmpeg/ffprobe must be available)"
}
