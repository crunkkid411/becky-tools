// becky-reel — the deterministic media engine for becky-clip. It turns a Reel
// JSON (a multi-source forensic clip list, internal/edl) into a single
// frame-accurate compilation MP4 with the original-timecode lower-third burned
// in, and exports a CMX3600 EDL, a re-based SRT, and frame-exact stills.
//
//	becky-reel render <reel.json> [--output f] [--codec c] [--bitrate b] [--verbose]
//	becky-reel edl    <reel.json> [--output f]
//	becky-reel srt    <reel.json> [--output f]
//	becky-reel frame  <source>    --at <seconds> [--output f]
//
// JSON report to stdout, diagnostics to stderr, non-zero exit on fatal error.
// Offline + deterministic; source videos are opened READ-ONLY (never modified).
// Unlike becky-export, becky-reel ALLOWS libx264 as the degrade-never-crash
// fallback when h264_nvenc is unavailable (R-CUT §7) — handled inside
// internal/reel.Render. Tool paths come from config.Load() (inside internal/reel).
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/edl"
	"becky-go/internal/pathx"
	"becky-go/internal/reel"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	sub := os.Args[1]
	rest := os.Args[2:]

	switch sub {
	case "render":
		cmdRender(rest)
	case "edl":
		cmdEDL(rest)
	case "srt":
		cmdSRT(rest)
	case "frame":
		cmdFrame(rest)
	case "-h", "--help", "help":
		usage()
	default:
		beckyio.Fatalf("unknown subcommand %q (want render|edl|srt|frame)", sub)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `becky-reel — forensic multi-source video compilation engine

usage:
  becky-reel render <reel.json> [--output f] [--codec c] [--bitrate b] [--verbose]
  becky-reel edl    <reel.json> [--output f]
  becky-reel srt    <reel.json> [--output f]
  becky-reel frame  <source>    --at <seconds> [--output f]
`)
}

// cmdRender renders the reel into one compilation MP4.
func cmdRender(argv []string) {
	fs := flag.NewFlagSet("render", flag.ExitOnError)
	out := fs.String("output", "", "output MP4 (default: <reel-name>_reel.mp4)")
	codec := fs.String("codec", "", "video codec (default config h264_nvenc; falls back to libx264)")
	bitrate := fs.String("bitrate", "", "video bitrate (e.g. 12M); default is codec CQ/CRF quality")
	fpsFlag := fs.Float64("fps", 0, "output frame rate (default 30)")
	width := fs.Int("width", 0, "output width (default 1280)")
	height := fs.Int("height", 0, "output height (default 720)")
	verbose := fs.Bool("verbose", false, "show ffmpeg progress on stderr")
	reelPath := positional(fs, argv)
	if reelPath == "" {
		beckyio.Fatalf("usage: becky-reel render <reel.json> [options]")
	}

	r := mustLoadReel(reelPath)
	res, err := reel.Render(r, reel.Options{
		Output:  *out,
		Codec:   *codec,
		Bitrate: *bitrate,
		FPS:     *fpsFlag,
		Width:   *width,
		Height:  *height,
		Verbose: *verbose,
	})
	if err != nil {
		beckyio.Fatalf("render failed: %v", err)
	}
	beckyio.PrintJSON(map[string]any{
		"action":       "render",
		"reel":         mustAbs(reelPath),
		"output":       res.Output,
		"codec":        res.Codec,
		"clips":        res.Clips,
		"duration_sec": res.DurationSec,
		"output_mb":    res.OutputMB,
		"note":         res.Note,
		"rendered":     true,
	})
}

// cmdEDL writes a CMX3600 EDL for the reel.
func cmdEDL(argv []string) {
	fs := flag.NewFlagSet("edl", flag.ExitOnError)
	out := fs.String("output", "", "output EDL (default: <reel-name>.edl next to the reel JSON)")
	reelPath := positional(fs, argv)
	if reelPath == "" {
		beckyio.Fatalf("usage: becky-reel edl <reel.json> [--output f]")
	}
	r := mustLoadReel(reelPath)

	outPath := *out
	if outPath == "" {
		outPath = siblingOutput(reelPath, r, ".edl")
	}
	outPath = mustAbs(outPath)

	if err := writeToFile(outPath, func(w *bufio.Writer) error { return edl.WriteEDL(w, r) }); err != nil {
		beckyio.Fatalf("write EDL: %v", err)
	}
	beckyio.PrintJSON(map[string]any{
		"action": "edl", "reel": mustAbs(reelPath), "output": outPath,
		"clips": len(r.Clips), "format": "cmx3600",
	})
}

// cmdSRT writes a re-based SRT for the reel (compilation-timeline cues).
func cmdSRT(argv []string) {
	fs := flag.NewFlagSet("srt", flag.ExitOnError)
	out := fs.String("output", "", "output SRT (default: <reel-name>.srt next to the reel JSON)")
	reelPath := positional(fs, argv)
	if reelPath == "" {
		beckyio.Fatalf("usage: becky-reel srt <reel.json> [--output f]")
	}
	r := mustLoadReel(reelPath)

	outPath := *out
	if outPath == "" {
		outPath = siblingOutput(reelPath, r, ".srt")
	}
	outPath = mustAbs(outPath)

	if err := writeToFile(outPath, func(w *bufio.Writer) error { return edl.WriteSRT(w, r) }); err != nil {
		beckyio.Fatalf("write SRT: %v", err)
	}
	beckyio.PrintJSON(map[string]any{
		"action": "srt", "reel": mustAbs(reelPath), "output": outPath,
		"clips": len(r.Clips), "rebased": true,
	})
}

// cmdFrame grabs a single frame-accurate still from a source video.
func cmdFrame(argv []string) {
	fs := flag.NewFlagSet("frame", flag.ExitOnError)
	at := fs.Float64("at", -1, "timestamp in seconds (required)")
	out := fs.String("output", "", "output PNG (default: <source-stem>_<at>s.png)")
	source := positional(fs, argv)
	if source == "" {
		beckyio.Fatalf("usage: becky-reel frame <source> --at <seconds> [--output f]")
	}
	if *at < 0 {
		beckyio.Fatalf("frame requires --at <seconds> (>= 0)")
	}
	if _, err := os.Stat(source); err != nil {
		beckyio.Fatalf("source not found: %s", source)
	}

	outPath := *out
	if outPath == "" {
		outPath = frameOutput(source, *at)
	}
	outPath = mustAbs(outPath)

	if err := reel.GrabFrame(source, *at, outPath); err != nil {
		beckyio.Fatalf("frame grab failed: %v", err)
	}
	beckyio.PrintJSON(map[string]any{
		"action": "frame", "source": mustAbs(source), "at": *at, "output": outPath,
	})
}

// positional pulls the single positional arg out of argv and parses the rest as
// flags on fs, tolerating the positional appearing BEFORE or AFTER the flags. It
// is flag-value aware: a "--flag value" pair (non-boolean flag) is kept together
// so the flag's value is never mistaken for the positional. "--flag=value" and
// boolean flags are handled too.
func positional(fs *flag.FlagSet, argv []string) string {
	var pos string
	var flags []string
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		switch {
		case strings.HasPrefix(a, "-"):
			flags = append(flags, a)
			// If this is a non-boolean flag in "--name value" form, the next
			// token is its value, not the positional.
			if !strings.Contains(a, "=") && flagTakesValue(fs, a) && i+1 < len(argv) {
				flags = append(flags, argv[i+1])
				i++
			}
		case pos == "":
			pos = a
		default:
			// Extra positional — fold into flags so fs.Parse reports it.
			flags = append(flags, a)
		}
	}
	_ = fs.Parse(flags)
	return pos
}

// flagTakesValue reports whether the flag named by token (e.g. "--output" or
// "-output") is defined on fs and is NOT a boolean flag (booleans take no
// separate value argument).
func flagTakesValue(fs *flag.FlagSet, token string) bool {
	name := strings.TrimLeft(token, "-")
	f := fs.Lookup(name)
	if f == nil {
		return false
	}
	if bv, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bv.IsBoolFlag() {
		return false
	}
	return true
}

func mustLoadReel(path string) edl.Reel {
	r, err := edl.Load(path)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}
	if len(r.Clips) == 0 {
		beckyio.Fatalf("reel %q has no clips", path)
	}
	return r
}

// writeToFile opens path for writing and runs fn against a buffered writer,
// flushing on success. The source videos are never touched — only this output.
func writeToFile(path string, fn func(*bufio.Writer) error) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	if err := fn(w); err != nil {
		return err
	}
	return w.Flush()
}

// siblingOutput builds "<reel-name>.ext" next to the reel JSON file.
func siblingOutput(reelPath string, r edl.Reel, ext string) string {
	dir := filepath.Dir(reelPath)
	name := strings.TrimSpace(r.Name)
	if name == "" {
		base := pathx.Base(reelPath)
		name = strings.TrimSuffix(base, filepath.Ext(base))
	}
	return filepath.Join(dir, slug(name)+ext)
}

// frameOutput builds "<source-stem>_<at>s.png" next to the source.
func frameOutput(source string, at float64) string {
	base := pathx.Base(source)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	dir := pathx.Dir(source)
	name := fmt.Sprintf("%s_%.3fs.png", stem, at)
	if dir == "" {
		return name
	}
	return filepath.Join(dir, name)
}

func mustAbs(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// slug lowercases and replaces runs of non-alphanumeric chars with a single '-'.
func slug(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "becky"
	}
	return out
}
