// becky-reaper authors REAL REAPER projects (.rpp) from becky's arrangement
// model and drives REAPER to render them. This is the fork-first DAW: REAPER is
// the (already-installed, fully scriptable) DAW Jordan opens; becky is the AI
// brain that builds and renders his sessions.
//
// Usage:
//
//	becky-reaper template --out song.rpp            # Jordan's Cubase bus tree, 132 BPM
//	becky-reaper demo     --out demo.rpp --render    # tiny audible synth-bass riff + render
//	becky-reaper build    --in arrangement.json --out song.rpp [--render]
//	becky-reaper render   --rpp song.rpp [--out out.wav]
//
// The .rpp writer is deterministic and needs nothing installed (CI-safe). The
// render subcommand shells out to REAPER (BECKY_REAPER overrides the path) and
// degrades with a clear message if REAPER is absent.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"becky-go/internal/dawmodel"
	"becky-go/internal/reaper"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "template":
		err = cmdTemplate(os.Args[2:])
	case "demo":
		err = cmdDemo(os.Args[2:])
	case "build":
		err = cmdBuild(os.Args[2:])
	case "render":
		err = cmdRender(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `becky-reaper - becky authors + drives REAPER projects

  template --out song.rpp [--render]      Jordan's Cubase-style bus tree (132 BPM)
  demo     --out demo.rpp [--render]      tiny audible synth-bass riff
  build    --in arr.json --out song.rpp [--render]   from a becky arrangement
  render   --rpp song.rpp [--out out.wav] render an existing .rpp via REAPER

env: BECKY_REAPER overrides the REAPER executable path.
`)
}

func cmdTemplate(args []string) error {
	out, render := outRenderFlags(args, "song.rpp")
	p := reaper.JordanTemplate(renderTarget(out, render))
	return writeAndMaybeRender(p, out, render)
}

func cmdDemo(args []string) error {
	out, render := outRenderFlags(args, "demo.rpp")
	p := reaper.DemoProject(renderTarget(out, render))
	return writeAndMaybeRender(p, out, render)
}

func cmdBuild(args []string) error {
	var in, out string
	render := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--in":
			i++
			if i < len(args) {
				in = args[i]
			}
		case "--out":
			i++
			if i < len(args) {
				out = args[i]
			}
		case "--render":
			render = true
		}
	}
	if in == "" || out == "" {
		return fmt.Errorf("build needs --in <arrangement.json> and --out <song.rpp>")
	}
	raw, err := os.ReadFile(in)
	if err != nil {
		return fmt.Errorf("read arrangement: %w", err)
	}
	var a dawmodel.Arrangement
	if err := json.Unmarshal(raw, &a); err != nil {
		return fmt.Errorf("parse arrangement json: %w", err)
	}
	p := reaper.FromArrangement(&a, renderTarget(out, render))
	return writeAndMaybeRender(p, out, render)
}

func cmdRender(args []string) error {
	var rpp, out string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--rpp":
			i++
			if i < len(args) {
				rpp = args[i]
			}
		case "--out":
			i++
			if i < len(args) {
				out = args[i]
			}
		}
	}
	if rpp == "" {
		return fmt.Errorf("render needs --rpp <song.rpp>")
	}
	abs, _ := filepath.Abs(rpp)
	return runReaperRender(abs, out)
}

func outRenderFlags(args []string, defOut string) (string, bool) {
	out, render := defOut, false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--out":
			i++
			if i < len(args) {
				out = args[i]
			}
		case "--render":
			render = true
		}
	}
	return out, render
}

// renderTarget returns the absolute WAV path embedded in the .rpp render
// settings when --render is requested (so `reaper -renderproject` lands a known
// file), otherwise "".
func renderTarget(out string, render bool) string {
	if !render {
		return ""
	}
	abs, _ := filepath.Abs(out)
	return wavSibling(abs)
}

func wavSibling(rpp string) string {
	ext := filepath.Ext(rpp)
	return rpp[:len(rpp)-len(ext)] + ".wav"
}

func writeAndMaybeRender(p reaper.Project, out string, render bool) error {
	rpp := reaper.WriteRPP(p)
	if err := os.WriteFile(out, []byte(rpp), 0o644); err != nil {
		return fmt.Errorf("write rpp: %w", err)
	}
	abs, _ := filepath.Abs(out)
	fmt.Printf("wrote REAPER project: %s (%d tracks)\n", abs, len(p.Tracks))
	if !render {
		fmt.Println("open it in REAPER, or re-run with --render to bounce a WAV")
		return nil
	}
	return runReaperRender(abs, p.RenderFile)
}

// runReaperRender shells out to REAPER's headless batch renderer. Degrades with
// a clear message (not a crash) when REAPER is not installed.
func runReaperRender(rpp, wantOut string) error {
	exe := reaperExe()
	if exe == "" {
		return fmt.Errorf("REAPER not found (set BECKY_REAPER to reaper.exe); wrote the .rpp anyway - open it manually")
	}
	fmt.Printf("rendering via %s ...\n", exe)
	cmd := exec.Command(exe, "-renderproject", rpp, "-nosplash")
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launch REAPER: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("REAPER render: %w", err)
		}
	case <-time.After(5 * time.Minute):
		_ = cmd.Process.Kill()
		return fmt.Errorf("REAPER render timed out after 5m (killed)")
	}
	if wantOut != "" {
		if fi, err := os.Stat(wantOut); err == nil {
			fmt.Printf("rendered: %s (%d bytes)\n", wantOut, fi.Size())
		} else {
			fmt.Printf("render finished but %s not found (REAPER may have used a different name)\n", wantOut)
		}
	}
	return nil
}

func reaperExe() string {
	if e := os.Getenv("BECKY_REAPER"); e != "" {
		if _, err := os.Stat(e); err == nil {
			return e
		}
	}
	for _, c := range []string{
		`C:\Program Files\REAPER (x64)\reaper.exe`,
		`C:\Program Files\REAPER\reaper.exe`,
	} {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}
