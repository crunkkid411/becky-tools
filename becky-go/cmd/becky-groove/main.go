// becky-groove — drive Hydrogen (the open-source drum machine) from simple params, with
// REAL samples from the producer's library. becky's pivot: don't reinvent a sampler;
// drive a proven one and let any AI control it.
//
// From a tiny description (bpm + which pads hit on which 16th-note steps), becky-groove:
//
//  1. scans the sample library (X:\music-2\SAMPLES, X:\Splice, or --lib) and picks a
//     real kick/snare/hat by role (deterministic: first match by sorted path);
//
//  2. writes a VALID Hydrogen .h2song (+ a drumkit.xml) that references those samples;
//
//  3. EITHER renders the beat to a WAV via Hydrogen's CLI (--render) OR drives a running
//     Hydrogen over OSC (--osc) — play/stop/bpm/load-kit/select-pattern/note-on.
//
//     # write a song + render audio (kick on every quarter, snare on the backbeat, hats on 8ths):
//     becky-groove make --bpm 120 --kick 0,4,8,12 --snare 4,12 --hat 0,2,4,6,8,10,12,14 \
//     --out beat.h2song --render beat.wav
//
//     # drive a running Hydrogen (started with `hydrogen -O 9000`):
//     becky-groove make --bpm 140 --kick 0,4,8,12 --out beat.h2song --osc --osc-port 9000
//
// Everything except the audio render is offline + deterministic; the only library-
// dependent step is WHICH sample gets picked (itself deterministic given the library).
// degrade-never-crash: a missing voice is skipped with a note; a missing Hydrogen CLI is
// a plain typed error, not a panic. Paths may be Windows paths.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"becky-go/internal/hydrogen"
	"becky-go/internal/samplelib"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "make":
		os.Exit(runMake(os.Args[2:]))
	case "-h", "--help", "help":
		usage()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "becky-groove: unknown subcommand %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: becky-groove make [flags]")
	fmt.Fprintln(os.Stderr, "  Build a Hydrogen beat from real samples, then render it OR drive Hydrogen over OSC.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  Pattern (0-based 16th-note steps, comma-separated; e.g. 0,4,8,12):")
	fmt.Fprintln(os.Stderr, "    --kick STEPS    --snare STEPS    --hat STEPS")
	fmt.Fprintln(os.Stderr, "  Sample choice:")
	fmt.Fprintln(os.Stderr, "    --lib DIR       sample library root (default: X:\\music-2\\SAMPLES then X:\\Splice)")
	fmt.Fprintln(os.Stderr, "    --kick-hint S   --snare-hint S   --hat-hint S   prefer samples whose name contains S")
	fmt.Fprintln(os.Stderr, "  Output:")
	fmt.Fprintln(os.Stderr, "    --bpm N         tempo (default 120)")
	fmt.Fprintln(os.Stderr, "    --bars N        repeat the pattern N times in the song (default 1)")
	fmt.Fprintln(os.Stderr, "    --vel V         note velocity 0..1 (default 0.8)")
	fmt.Fprintln(os.Stderr, "    --out FILE      write the .h2song here (default: groove.h2song)")
	fmt.Fprintln(os.Stderr, "    --render FILE   render the beat to a WAV via Hydrogen's CLI")
	fmt.Fprintln(os.Stderr, "    --hydrogen-cli PATH   override the Hydrogen CLI (else BECKY_HYDROGEN_CLI / PATH / install dir)")
	fmt.Fprintln(os.Stderr, "  Live (drive a running `hydrogen -O <port>`):")
	fmt.Fprintln(os.Stderr, "    --osc           after writing, open the song in Hydrogen over OSC and play it")
	fmt.Fprintln(os.Stderr, "    --osc-host H    OSC host (default 127.0.0.1)")
	fmt.Fprintln(os.Stderr, "    --osc-port P    OSC port (default 9000)")
}

func runMake(argv []string) int {
	fs := flag.NewFlagSet("make", flag.ContinueOnError)
	kick := fs.String("kick", "0,4,8,12", "kick steps (0-based 16th-note, comma-separated)")
	snare := fs.String("snare", "4,12", "snare steps")
	hat := fs.String("hat", "0,2,4,6,8,10,12,14", "hat steps")
	kickHint := fs.String("kick-hint", "", "prefer a kick sample whose name contains this")
	snareHint := fs.String("snare-hint", "", "prefer a snare sample whose name contains this")
	hatHint := fs.String("hat-hint", "", "prefer a hat sample whose name contains this")
	lib := fs.String("lib", "", "sample library root")
	bpm := fs.Float64("bpm", 120, "tempo")
	bars := fs.Int("bars", 1, "repeat the pattern N times")
	vel := fs.Float64("vel", 0.8, "note velocity 0..1")
	out := fs.String("out", "groove.h2song", "write the .h2song here")
	render := fs.String("render", "", "render the beat to a WAV via Hydrogen's CLI")
	cliPath := fs.String("hydrogen-cli", "", "override the Hydrogen CLI exporter")
	useOSC := fs.Bool("osc", false, "drive a running Hydrogen over OSC")
	oscHost := fs.String("osc-host", "127.0.0.1", "OSC host")
	oscPort := fs.Int("osc-port", hydrogen.DefaultOSCPort, "OSC port")
	if err := fs.Parse(argv); err != nil {
		return 2
	}

	// --- parse the step pattern (per voice) ---
	voiceSteps := map[string][]int{}
	var perr error
	voiceSteps["kick"], perr = parseSteps(*kick)
	if perr != nil {
		fmt.Fprintf(os.Stderr, "becky-groove: bad --kick: %v\n", perr)
		return 2
	}
	voiceSteps["snare"], perr = parseSteps(*snare)
	if perr != nil {
		fmt.Fprintf(os.Stderr, "becky-groove: bad --snare: %v\n", perr)
		return 2
	}
	voiceSteps["hat"], perr = parseSteps(*hat)
	if perr != nil {
		fmt.Fprintf(os.Stderr, "becky-groove: bad --hat: %v\n", perr)
		return 2
	}

	// --- scan the library and pick real samples ---
	root := resolveLibRoot(*lib)
	if root == "" {
		fmt.Fprintln(os.Stderr, "becky-groove: no sample library found.")
		fmt.Fprintln(os.Stderr, "  pass --lib <folder> (looked for X:\\music-2\\SAMPLES and X:\\Splice).")
		return 1
	}
	fmt.Fprintf(os.Stderr, "becky-groove: scanning %s ...\n", root)
	idx, err := samplelib.ScanWithCache(root, samplelib.PersistedIndexOptions{
		ScanOpts: samplelib.ScanOptions{Recursive: true},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "becky-groove: scan %s: %v\n", root, err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "becky-groove: indexed %d samples (kick=%d snare=%d hat=%d)\n",
		len(idx.Samples), idx.Count(samplelib.RoleKick), idx.Count(samplelib.RoleSnare), idx.Count(samplelib.RoleHat))

	voices := []hydrogen.BeatVoice{
		{Name: "Kick", Role: samplelib.RoleKick, MidiNote: hydrogen.MIDIKick, NameHint: *kickHint},
		{Name: "Snare", Role: samplelib.RoleSnare, MidiNote: hydrogen.MIDISnare, NameHint: *snareHint},
		{Name: "Hat", Role: samplelib.RoleHat, MidiNote: hydrogen.MIDIHatClsd, NameHint: *hatHint},
	}
	kitName := "becky-groove-kit"
	kit, missing := hydrogen.KitFromLibrary(kitName, idx, voices)
	if len(kit.Instruments) == 0 {
		fmt.Fprintln(os.Stderr, "becky-groove: could not find ANY drum samples in the library; nothing to build.")
		return 1
	}
	for _, inst := range kit.Instruments {
		fmt.Fprintf(os.Stderr, "  %-6s -> %s\n", inst.Name, sampleOf(inst))
	}
	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "  (no sample for: %s — skipped)\n", strings.Join(missing, ", "))
	}

	// --- build the pattern + song ---
	// Map voice name -> the instrument id actually assigned (kit may have skipped some).
	idByName := map[string]int{}
	for _, inst := range kit.Instruments {
		idByName[strings.ToLower(inst.Name)] = inst.ID
	}
	hits := map[int][]int{}
	for voice, steps := range voiceSteps {
		id, ok := idByName[voice]
		if !ok {
			continue // that voice had no sample
		}
		hits[id] = steps
	}
	pat := hydrogen.StepPattern("Pattern 1", hits, *vel)

	seq := make([]string, 0, *bars)
	n := *bars
	if n < 1 {
		n = 1
	}
	for i := 0; i < n; i++ {
		seq = append(seq, "Pattern 1")
	}

	song := hydrogen.Song{
		Name:        "becky groove",
		Author:      "becky",
		BPM:         *bpm,
		Kit:         kit,
		Patterns:    []hydrogen.Pattern{pat},
		Sequence:    seq,
		LoopEnabled: true,
	}

	// Write a self-contained kit alongside the song (drumkit.xml in the song's dir), then
	// the song itself. The song's layers already reference absolute sample paths, so the
	// drumkit.xml is a convenience/record; both are valid Hydrogen files.
	outAbs, _ := filepath.Abs(*out)
	songDir := filepath.Dir(outAbs)
	if _, err := hydrogen.WriteDrumkit(songDir, kit); err != nil {
		fmt.Fprintf(os.Stderr, "becky-groove: warning: could not write drumkit.xml: %v\n", err)
	}
	if err := hydrogen.WriteSong(outAbs, song); err != nil {
		fmt.Fprintf(os.Stderr, "becky-groove: write song: %v\n", err)
		return 1
	}
	fmt.Printf("Saved: %s\n", outAbs)

	// --- render and/or drive OSC ---
	exitCode := 0
	if *render != "" {
		renderAbs, _ := filepath.Abs(*render)
		fmt.Fprintf(os.Stderr, "becky-groove: rendering to %s ...\n", renderAbs)
		err := hydrogen.ExportSong(outAbs, renderAbs, hydrogen.ExportOptions{
			CLIPath: *cliPath,
			Timeout: 120 * time.Second,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "becky-groove: render failed: %v\n", err)
			exitCode = 1
		} else if fi, serr := os.Stat(renderAbs); serr == nil {
			fmt.Printf("Rendered: %s (%d bytes)\n", renderAbs, fi.Size())
		}
	}

	if *useOSC {
		if err := driveOSC(*oscHost, *oscPort, outAbs, kitName, *bpm); err != nil {
			fmt.Fprintf(os.Stderr, "becky-groove: OSC: %v\n", err)
			exitCode = 1
		} else {
			fmt.Printf("Playing in Hydrogen via OSC at %s:%d\n", *oscHost, *oscPort)
		}
	}

	return exitCode
}

// driveOSC opens the freshly-written song in a running Hydrogen and starts playback.
func driveOSC(host string, port int, songPath, kitName string, bpm float64) error {
	c := hydrogen.NewOSCClient(host, port)
	// Open the song (it carries the kit + patterns), set tempo, then play.
	if err := c.OpenSong(songPath); err != nil {
		return err
	}
	// Give Hydrogen a moment to load the song before tempo/play.
	time.Sleep(400 * time.Millisecond)
	if err := c.SetBPM(bpm); err != nil {
		return err
	}
	if err := c.Play(); err != nil {
		return err
	}
	return nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// parseSteps parses "0,4,8,12" into []int. Empty -> nil (no hits). Whitespace tolerant.
// Out-of-grid values are kept here and filtered by StepPattern (which ignores 0>15);
// a non-integer token is an error so the user learns about a typo.
func parseSteps(s string) ([]int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("%q is not a step number", p)
		}
		out = append(out, v)
	}
	sort.Ints(out)
	return out, nil
}

// resolveLibRoot picks the sample library root: explicit --lib, else the known becky
// roots (X:\music-2\SAMPLES then X:\Splice) if they exist.
func resolveLibRoot(lib string) string {
	if strings.TrimSpace(lib) != "" {
		if dirExists(lib) {
			return lib
		}
		return "" // explicit but missing
	}
	if runtime.GOOS == "windows" {
		for _, cand := range []string{`X:\music-2\SAMPLES`, `X:\Splice`} {
			if dirExists(cand) {
				return cand
			}
		}
	}
	return ""
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func sampleOf(inst hydrogen.Instrument) string {
	if len(inst.Layers) == 0 {
		return "(no sample)"
	}
	return inst.Layers[0].Filename
}
