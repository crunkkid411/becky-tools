// becky-otio — turn a becky Reel (internal/edl clip-list) into editor-agnostic
// timeline files, so forensic hits can be reviewed in whatever snappy NLE the
// user prefers WITHOUT becky being married to one editor:
//
//	becky-otio --reel <reel.json> [--format otio,edl,vegas-list,fcpxml,mlt,all] [--out dir] [--audio]
//	becky-otio --reel <reel.json> --via-otio-cli aaf,ale   (needs the OTIO python pkg)
//	becky-otio --selftest
//
//	otio        -> <name>.otio        (DaVinci Resolve / kdenlive 25.04+ import natively)
//	fcpxml      -> <name>.fcpxml      (Final Cut / Premiere via plugin / Resolve — Phase 2 fallback)
//	mlt         -> <name>.kdenlive    (kdenlive native; renders headless via melt)
//	edl         -> <name>.edl         (CMX3600 — every editor; single track, lossy)
//	vegas-list  -> <name>.review.txt  (fed to /vegas/BeckyReviewTimeline.cs on VEGAS Pro 18)
//
// JSON report to stdout, diagnostics to stderr, non-zero exit on fatal error.
// Pure Go, offline, deterministic; source media is never modified. No models. The
// only optional exec is --via-otio-cli (otioconvert), which degrades silently when
// the OpenTimelineIO python package isn't installed. See SPEC-BECKY-OTIO.md.
package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/edl"
	"becky-go/internal/otio"
	"becky-go/internal/pathx"
)

type written struct {
	Format string `json:"format"`
	Path   string `json:"path"`
	Clips  int    `json:"clips"`
}

type report struct {
	Reel     string    `json:"reel"`
	Written  []written `json:"written"`
	Warnings []string  `json:"warnings,omitempty"`
}

func main() {
	fs := flag.NewFlagSet("becky-otio", flag.ExitOnError)
	reelPath := fs.String("reel", "", "path to a Reel JSON (internal/edl); '-' for stdin")
	format := fs.String("format", "otio", "comma list: otio,fcpxml,mlt,edl,vegas-list,all")
	outDir := fs.String("out", "", "output directory (default: alongside the reel, or cwd for stdin)")
	audio := fs.Bool("audio", false, "also emit a parallel audio track (otio)")
	viaOtioCLI := fs.String("via-otio-cli", "", "after writing .otio, run otioconvert to also emit <name>.<ext> (comma list, e.g. aaf,ale); needs the OTIO python package on PATH, degrades silently if absent")
	selftest := fs.Bool("selftest", false, "run the offline self-test (no files needed) and exit")
	_ = fs.Parse(os.Args[1:])

	if *selftest {
		runSelftest()
		return
	}
	if *reelPath == "" {
		beckyio.Fatalf("--reel is required (or use --selftest)")
	}

	r, srcName, err := loadReel(*reelPath)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}

	dir := *outDir
	if dir == "" {
		if *reelPath == "-" {
			dir = "."
		} else {
			dir = filepath.Dir(*reelPath)
		}
	}
	name := reelBaseName(r, srcName)

	formats := expandFormats(*format)
	rep := report{Reel: name}
	opts := otio.Options{IncludeAudio: *audio}
	var otioPath string // remembered so --via-otio-cli can convert from it

	for _, f := range formats {
		switch f {
		case "otio":
			path := filepath.Join(dir, name+".otio")
			n, err := writeFile(path, func(b *bytes.Buffer) error { return otio.WriteOTIO(b, r, opts) })
			if err != nil {
				rep.Warnings = append(rep.Warnings, "otio: "+err.Error())
				continue
			}
			rep.Written = append(rep.Written, written{"otio", path, countPlayable(r)})
			otioPath = path
			_ = n
		case "fcpxml":
			path := filepath.Join(dir, name+".fcpxml")
			_, err := writeFile(path, func(b *bytes.Buffer) error { return otio.WriteFCPXML(b, r, opts) })
			if err != nil {
				rep.Warnings = append(rep.Warnings, "fcpxml: "+err.Error())
				continue
			}
			rep.Written = append(rep.Written, written{"fcpxml", path, countPlayable(r)})
		case "mlt":
			path := filepath.Join(dir, name+".kdenlive")
			var clips int
			_, err := writeFile(path, func(b *bytes.Buffer) error {
				c, e := otio.WriteMLT(b, r, opts)
				clips = c
				return e
			})
			if err != nil {
				rep.Warnings = append(rep.Warnings, "mlt: "+err.Error())
				continue
			}
			rep.Written = append(rep.Written, written{"mlt", path, clips})
		case "vegas-list":
			path := filepath.Join(dir, name+".review.txt")
			var clips int
			_, err := writeFile(path, func(b *bytes.Buffer) error {
				c, e := otio.WriteVegasList(b, r)
				clips = c
				return e
			})
			if err != nil {
				rep.Warnings = append(rep.Warnings, "vegas-list: "+err.Error())
				continue
			}
			rep.Written = append(rep.Written, written{"vegas-list", path, clips})
		case "edl":
			path := filepath.Join(dir, name+".edl")
			_, err := writeFile(path, func(b *bytes.Buffer) error { return edl.WriteEDL(b, r) })
			if err != nil {
				rep.Warnings = append(rep.Warnings, "edl: "+err.Error())
				continue
			}
			rep.Written = append(rep.Written, written{"edl", path, countPlayable(r)})
		default:
			rep.Warnings = append(rep.Warnings, "unknown format: "+f)
		}
	}

	// --via-otio-cli: optionally reach adapter formats (AAF/ALE/...) by shelling
	// otioconvert against a generated .otio. Degrade-never-crash: if otioconvert
	// isn't installed we keep the .otio and note it; becky never needs Python.
	if strings.TrimSpace(*viaOtioCLI) != "" {
		if otioPath == "" { // user didn't request otio; write a base one to convert
			otioPath = filepath.Join(dir, name+".otio")
			if _, err := writeFile(otioPath, func(b *bytes.Buffer) error { return otio.WriteOTIO(b, r, opts) }); err != nil {
				rep.Warnings = append(rep.Warnings, "via-otio-cli: could not write base .otio: "+err.Error())
				otioPath = ""
			} else {
				rep.Written = append(rep.Written, written{"otio", otioPath, countPlayable(r)})
			}
		}
		switch {
		case otioPath == "":
			// already warned above
		case !otio.OtioCLIAvailable():
			rep.Warnings = append(rep.Warnings, "via-otio-cli requested but 'otioconvert' is not on PATH (install the OpenTimelineIO python package); kept the .otio only")
		default:
			for _, ext := range strings.Split(*viaOtioCLI, ",") {
				ext = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(ext), "."))
				if ext == "" {
					continue
				}
				outPath := filepath.Join(dir, name+"."+ext)
				ran, err := otio.OtioConvert(otioPath, outPath)
				if err != nil {
					rep.Warnings = append(rep.Warnings, "via-otio-cli "+ext+": "+err.Error())
					continue
				}
				if ran {
					rep.Written = append(rep.Written, written{"otioconvert:" + ext, outPath, countPlayable(r)})
				}
			}
		}
	}

	// Surface missing source files as warnings (the clip is still exported — the
	// editor shows it offline, which truthfully tells the human the file moved).
	for _, c := range r.Clips {
		if c.Source != "" {
			if _, statErr := os.Stat(c.Source); statErr != nil {
				rep.Warnings = append(rep.Warnings, "source not found (clip still exported, will show offline): "+c.Source)
			}
		}
	}

	if len(rep.Written) == 0 {
		beckyio.Fatalf("nothing written (formats=%q); warnings: %s", *format, strings.Join(rep.Warnings, "; "))
	}
	beckyio.PrintJSON(rep)
}

// loadReel reads a Reel from a path or stdin ("-"). srcName is the input file's
// base name (for output naming when the Reel has no Name).
func loadReel(path string) (edl.Reel, string, error) {
	if path == "-" {
		var r edl.Reel
		if err := json.NewDecoder(os.Stdin).Decode(&r); err != nil {
			return edl.Reel{}, "", fmt.Errorf("parse reel JSON from stdin: %w", err)
		}
		if r.Version == "" {
			r.Version = "1"
		}
		return r, "reel", nil
	}
	r, err := edl.Load(path)
	if err != nil {
		return edl.Reel{}, "", err
	}
	base := pathx.Base(path)
	return r, strings.TrimSuffix(base, filepath.Ext(base)), nil
}

// reelBaseName picks a safe output stem: the Reel.Name (sanitized) if set, else
// the input file's stem.
func reelBaseName(r edl.Reel, srcName string) string {
	n := strings.TrimSpace(r.Name)
	if n == "" {
		n = srcName
	}
	if n == "" {
		n = "becky-review"
	}
	return sanitize(n)
}

func sanitize(s string) string {
	repl := func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', '\n', '\t':
			return '_'
		}
		return r
	}
	return strings.Map(repl, s)
}

func expandFormats(spec string) []string {
	if strings.TrimSpace(spec) == "all" {
		return []string{"otio", "fcpxml", "mlt", "edl", "vegas-list"}
	}
	var out []string
	for _, f := range strings.Split(spec, ",") {
		f = strings.TrimSpace(f)
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

func countPlayable(r edl.Reel) int {
	n := 0
	for _, c := range r.Clips {
		if c.Dur() > 0 {
			n++
		}
	}
	return n
}

// writeFile renders into a buffer first, then writes 0644 — so a render error
// never leaves a half-written file. Returns the byte count.
func writeFile(path string, render func(*bytes.Buffer) error) (int, error) {
	var b bytes.Buffer
	if err := render(&b); err != nil {
		return 0, err
	}
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		return 0, err
	}
	return b.Len(), nil
}

// runSelftest exercises the real code path with no input files and asserts the
// outputs by value — the one-command offline proof (SPEC §8.1).
func runSelftest() {
	r := edl.Reel{
		Name: "selftest",
		Clips: []edl.Clip{
			{ID: "c1", Source: `C:\Videos\cam1.mp4`, In: 65.0, Out: 73.5, Label: "cat closeup", Meta: edl.ClipMeta{SourceFPS: 30}},
			{ID: "c2", Source: `C:\Videos\cam2.mp4`, In: 120, Out: 128, Meta: edl.ClipMeta{SourceFPS: 25}},
		},
	}
	fail := false
	check := func(name string, ok bool, detail string) {
		status := "PASS"
		if !ok {
			status, fail = "FAIL", true
		}
		fmt.Fprintf(os.Stderr, "[%s] %s %s\n", status, name, detail)
	}

	// OTIO: parse back, assert clip count + clip A frames.
	var ob bytes.Buffer
	_ = otio.WriteOTIO(&ob, r, otio.Options{})
	var tl map[string]any
	otioOK := json.Unmarshal(ob.Bytes(), &tl) == nil
	clipCount, startA, durA := 0, 0.0, 0.0
	if otioOK {
		tracks, _ := tl["tracks"].(map[string]any)["children"].([]any)
		if len(tracks) == 1 {
			clips, _ := tracks[0].(map[string]any)["children"].([]any)
			clipCount = len(clips)
			if clipCount == 2 {
				sr := clips[0].(map[string]any)["source_range"].(map[string]any)
				startA = sr["start_time"].(map[string]any)["value"].(float64)
				durA = sr["duration"].(map[string]any)["value"].(float64)
			}
		}
	}
	check("otio.valid_json", otioOK, "")
	check("otio.clip_count", clipCount == 2, fmt.Sprintf("(got %d, want 2)", clipCount))
	check("otio.clipA_frames", startA == 1950 && durA == 255, fmt.Sprintf("(start=%v want 1950, dur=%v want 255)", startA, durA))

	// vegas-list: 2 clips + exact first clip line.
	var vb bytes.Buffer
	n, _ := otio.WriteVegasList(&vb, r)
	lines := strings.Split(strings.TrimRight(vb.String(), "\n"), "\n")
	firstClipLine := ""
	if len(lines) >= 2 {
		firstClipLine = lines[1]
	}
	check("vegaslist.clip_count", n == 2, fmt.Sprintf("(got %d, want 2)", n))
	check("vegaslist.first_line", firstClipLine == `C:\Videos\cam1.mp4 | 65 | 73.5 | cat closeup`, "("+firstClipLine+")")

	// edl: writes without error and produces content.
	var eb bytes.Buffer
	edlErr := edl.WriteEDL(&eb, r)
	check("edl.writes", edlErr == nil && eb.Len() > 0, "")

	// mlt: parse back, assert 2 producers/entries + clip A frames (timeline 30fps).
	var mb bytes.Buffer
	mClips, mWErr := otio.WriteMLT(&mb, r, otio.Options{})
	var mdoc struct {
		Producers []struct {
			ID string `xml:"id,attr"`
		} `xml:"producer"`
		Playlists []struct {
			Entries []struct {
				In  int `xml:"in,attr"`
				Out int `xml:"out,attr"`
			} `xml:"entry"`
		} `xml:"playlist"`
	}
	mErr := xml.Unmarshal(mb.Bytes(), &mdoc)
	mEntries, mInA, mOutA := 0, -1, -1
	if mErr == nil && len(mdoc.Playlists) == 1 {
		mEntries = len(mdoc.Playlists[0].Entries)
		if mEntries >= 1 {
			mInA, mOutA = mdoc.Playlists[0].Entries[0].In, mdoc.Playlists[0].Entries[0].Out
		}
	}
	check("mlt.valid_xml", mWErr == nil && mErr == nil, "")
	check("mlt.clip_count", mClips == 2 && mEntries == 2 && len(mdoc.Producers) == 2,
		fmt.Sprintf("(clips=%d entries=%d producers=%d, want 2/2/2)", mClips, mEntries, len(mdoc.Producers)))
	check("mlt.clipA_frames", mInA == 1950 && mOutA == 2204, fmt.Sprintf("(in=%d out=%d, want 1950/2204)", mInA, mOutA))

	// fcpxml: parse back, assert version + spine count + clip A rational times.
	var fb bytes.Buffer
	fWErr := otio.WriteFCPXML(&fb, r, otio.Options{})
	var fdoc struct {
		Version string `xml:"version,attr"`
		Library struct {
			Event struct {
				Project struct {
					Sequence struct {
						Spine struct {
							Clips []struct {
								Start    string `xml:"start,attr"`
								Duration string `xml:"duration,attr"`
							} `xml:"asset-clip"`
						} `xml:"spine"`
					} `xml:"sequence"`
				} `xml:"project"`
			} `xml:"event"`
		} `xml:"library"`
	}
	fErr := xml.Unmarshal(fb.Bytes(), &fdoc)
	fClips := len(fdoc.Library.Event.Project.Sequence.Spine.Clips)
	fStartA, fDurA := "", ""
	if fClips >= 1 {
		fStartA = fdoc.Library.Event.Project.Sequence.Spine.Clips[0].Start
		fDurA = fdoc.Library.Event.Project.Sequence.Spine.Clips[0].Duration
	}
	check("fcpxml.valid_xml", fWErr == nil && fErr == nil && fdoc.Version == "1.10", fmt.Sprintf("(ver=%q)", fdoc.Version))
	check("fcpxml.clip_count", fClips == 2, fmt.Sprintf("(got %d, want 2)", fClips))
	check("fcpxml.clipA_times", fStartA == "1950/30s" && fDurA == "255/30s",
		fmt.Sprintf("(start=%q dur=%q, want 1950/30s 255/30s)", fStartA, fDurA))

	if fail {
		fmt.Fprintln(os.Stderr, "selftest: FAIL")
		os.Exit(1)
	}
	beckyio.PrintJSON(map[string]any{"selftest": "ok", "formats": []string{"otio", "fcpxml", "mlt", "vegas-list", "edl"}})
}
