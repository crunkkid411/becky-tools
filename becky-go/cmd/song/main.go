// becky-song — THE PIPE. One command turns an intent into a song you can hear:
//
//	becky-song "house"  -> mysong.wav (audible) + mysong.json (editable)
//
// It chains becky's tools in-process, no intermediate files to shuttle by hand:
//
//	intent ─▶ beatgen (genre drum beat)
//	       ─▶ arrange.Jam (bass ─▶ chords ─▶ melody, each fitting the stems before it)
//	       ─▶ musictheory.Evaluate (becky checks its own output)
//	       ─▶ audioengine.RenderArrangementWAV (the whole mix ─▶ a WAV, pure Go, no GPU)
//
// The render is offline + deterministic, so this is a PROVABLE pipe: the .wav is
// measurable (ffprobe / volumedetect). Drums use the becky default kit (sine fallback
// when no samples are on disk); pitched parts use the pure-Go synth.
//
//	becky-song <genre|intent> [--out name] [--bpm N] [--seed N] [--bars N] [--drums-only]
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"becky-go/internal/audioengine"
	"becky-go/internal/autoroute"
	"becky-go/internal/beatgen"
	"becky-go/internal/dawmodel"
	"becky-go/internal/intent"
	"becky-go/internal/library"
	"becky-go/internal/musictheory"
	"becky-go/internal/pathx"
	"becky-go/internal/songbuild"
)

// genreBPM is a small default-tempo table; --bpm overrides. Unknown → 128.
var genreBPM = map[string]int{
	"house": 124, "techno": 130, "trap": 140, "dnb": 174, "hiphop": 90,
	"lofi": 80, "breakbeat": 136, "straight": 120,
}

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		usage()
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	// Leading positional words (before the first --flag) are the plain-English
	// intent: "dark trap at 140, 8 bars, just drums". Flags override what it parses.
	pi := 0
	var phraseWords []string
	for pi < len(args) && !strings.HasPrefix(args[pi], "--") {
		phraseWords = append(phraseWords, args[pi])
		pi++
	}
	phrase := strings.Join(phraseWords, " ")
	spec := intent.Parse(phrase)

	out, saveAs, fromTemplate := "", "", ""
	variations := 1
	route := false
	bpm, bars, seed, drumsOnly := spec.BPM, spec.Bars, spec.Seed, spec.DrumsOnly
	rest := args[pi:]
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--out":
			if i+1 < len(rest) {
				out = rest[i+1]
				i++
			}
		case "--save-as":
			if i+1 < len(rest) {
				saveAs = rest[i+1]
				i++
			}
		case "--template":
			if i+1 < len(rest) {
				fromTemplate = rest[i+1]
				i++
			}
		case "--bpm":
			if i+1 < len(rest) {
				if v, err := strconv.Atoi(rest[i+1]); err == nil {
					bpm = v
				}
				i++
			}
		case "--seed":
			if i+1 < len(rest) {
				if v, err := strconv.ParseInt(rest[i+1], 10, 64); err == nil {
					seed = v
				}
				i++
			}
		case "--bars":
			if i+1 < len(rest) {
				if v, err := strconv.Atoi(rest[i+1]); err == nil && v > 0 {
					bars = v
				}
				i++
			}
		case "--drums-only":
			drumsOnly = true
		case "--route":
			route = true
		case "--variations":
			if i+1 < len(rest) {
				if v, err := strconv.Atoi(rest[i+1]); err == nil && v > 0 {
					variations = v
				}
				i++
			}
		}
	}

	genre := spec.Genre
	if genre == "" {
		genre = strings.ToLower(strings.TrimSpace(firstWord(phrase)))
		if genre == "" {
			genre = "straight"
		}
	}
	if bars <= 0 {
		bars = 4
	}
	if seed == 0 {
		seed = 1
	}
	if bpm <= 0 {
		if b, ok := genreBPM[genre]; ok {
			bpm = b
		} else {
			bpm = 128
		}
	}
	if out == "" {
		out = genre + "-song"
	}
	if len(spec.Understood) > 0 {
		fmt.Println("becky heard: " + strings.Join(spec.Understood, ", "))
	}

	// Variations: render N seeded takes (seed, seed+1, …) for browsing ideas.
	if variations > 1 && fromTemplate == "" {
		fmt.Printf("♪ %s @ %d BPM, %d bars — %d variations\n", genre, bpm, bars, variations)
		for v := 0; v < variations; v++ {
			built, berr := songbuild.Build(intent.Spec{
				Genre: genre, BPM: bpm, Bars: bars, Seed: seed + int64(v),
				DrumsOnly: drumsOnly, Root: spec.Root, Scale: spec.Scale,
			})
			if berr != nil {
				fmt.Fprintln(os.Stderr, "becky-song:", berr)
				return 1
			}
			if err := renderOutputs(built, fmt.Sprintf("%s-%d", out, v+1)); err != nil {
				fmt.Fprintln(os.Stderr, "becky-song:", err)
				return 1
			}
		}
		return 0
	}

	var arr *dawmodel.Arrangement
	if fromTemplate != "" {
		// ── pipe FROM the library: start from a saved template ────────────────
		lib, lerr := library.Open()
		if lerr != nil {
			fmt.Fprintln(os.Stderr, "becky-song:", lerr)
			return 1
		}
		loaded, meta, lerr := lib.LoadTemplate(fromTemplate)
		if lerr != nil {
			fmt.Fprintln(os.Stderr, "becky-song:", lerr)
			return 1
		}
		arr = loaded
		if bpm > 0 {
			arr.BPM = bpm
		}
		fmt.Printf("♪ from template %q (%d tracks)\n", meta.Name, len(arr.Tracks))
	} else {
		// ── generate via the shared pipe core (same as becky-canvas) ──────────
		built, berr := songbuild.Build(intent.Spec{
			Genre: genre, BPM: bpm, Bars: bars, Seed: seed,
			DrumsOnly: drumsOnly, Root: spec.Root, Scale: spec.Scale,
		})
		if berr != nil {
			fmt.Fprintln(os.Stderr, "becky-song:", berr)
			return 1
		}
		arr = built
		fmt.Printf("♪ %s @ %d BPM, %d bars\n", genre, bpm, bars)
		for _, tr := range arr.Tracks {
			fmt.Println("  + " + tr.ID)
		}
	}

	// ── route: apply Jordan's deterministic label→bus routing (lightweight, no plugins) ──
	if route {
		routed, assigns := autoroute.Apply(arr, autoroute.Load())
		arr = routed
		for _, a := range assigns {
			fmt.Printf("  → %s routes to %s\n", a.Track, a.Bus)
		}
	}

	// ── 3. evaluate: becky checks its own output ──────────────────────────────
	for _, iss := range musictheory.Evaluate(arr) {
		where := iss.Track
		if where == "" {
			where = "song"
		}
		fmt.Printf("  ⚠ %s [%s]: %s\n", iss.Check, where, iss.Note)
	}

	// ── pipe TO the library: bank this song as a reusable template ────────────
	if saveAs != "" {
		if lib, lerr := library.Open(); lerr == nil {
			if meta, serr := lib.SaveTemplate(saveAs, genre, arr); serr == nil {
				fmt.Printf("  ★ saved to library as %q (recall: becky-song --template %s)\n", meta.Slug, meta.Slug)
			}
		}
	}

	// ── 4. render: the whole mix → wav + json + mid ───────────────────────────
	if err := renderOutputs(arr, out); err != nil {
		fmt.Fprintln(os.Stderr, "becky-song:", err)
		return 1
	}
	return 0
}

// renderOutputs writes the three deliverables for an arrangement: <base>.wav (play),
// <base>.json (becky-canvas), <base>.mid (any DAW).
func renderOutputs(arr *dawmodel.Arrangement, base string) error {
	if err := writeJSON(base+".json", arr); err != nil {
		return err
	}
	if err := audioengine.RenderArrangementWAV(arr, base+".wav", 48000, 1); err != nil {
		return fmt.Errorf("render: %w", err)
	}
	if err := os.WriteFile(base+".mid", arr.ToSMF(), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "becky-song: midi:", err) // non-fatal
	}
	fmt.Printf("✓ %s.wav (play) + %s.json (canvas) + %s.mid (any DAW) — %d tracks, %d notes\n",
		pathx.Base(base), pathx.Base(base), pathx.Base(base), len(arr.Tracks), arr.NoteCount())
	return nil
}

// firstWord returns the first whitespace-separated token of s.
func firstWord(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

func usage() {
	fmt.Fprintln(os.Stderr, "becky-song — plain English → an audible song in one command (the pipe)")
	fmt.Fprintln(os.Stderr, "  becky-song \"<plain english>\" [flags]   → <name>.wav + .json + .mid")
	fmt.Fprintln(os.Stderr, "  understands: genre, mood (dark/happy), 'at 140', 'in F# minor', 'N bars', 'just drums'")
	fmt.Fprintln(os.Stderr, "  flags: --out name  --bpm N  --bars N  --seed N  --drums-only  --variations N")
	fmt.Fprintln(os.Stderr, "         --save-as name (bank it)   --template name (start from a saved one)")
	fmt.Fprintln(os.Stderr, "  e.g. becky-song \"dark trap at 140, 8 bars\"")
	fmt.Fprintln(os.Stderr, "       becky-song \"happy lo-fi in C major\" --variations 4")
	fmt.Fprintf(os.Stderr, "  drum genres with real grooves: %s (others get a generic beat + the right progression)\n",
		strings.Join(beatgen.GenreNames(), ", "))
}

func writeJSON(path string, arr *dawmodel.Arrangement) error {
	data, err := json.MarshalIndent(arr, "", "  ")
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", pathx.Base(path), err)
	}
	return nil
}
