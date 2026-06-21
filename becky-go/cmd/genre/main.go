// becky-genre — the genre-research pipeline's command line (STANDARDS-MUSIC-RESEARCH.md).
// It prints the research query templates, scaffolds the "5 elements" to fill from
// research, and turns the distilled elements into a real becky-compose genre profile.
//
//	becky-genre queries crunkcore          # the searches to run (Hooktheory/Chordify/…)
//	becky-genre scaffold nightcore > n.json # a blank 5-elements file to fill from research
//	becky-genre build --elements n.json    # distilled elements -> profiles/<id>.json
//
// Only the research + distillation (filling the scaffold) needs a model/network; the
// build is pure deterministic Go. A new profile is embedded into the DB on the next
// `go build` (profiles are //go:embed-ed) — so a researched genre becomes permanent.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"becky-go/internal/genreprofile"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 0 {
		usage()
		return 2
	}
	switch args[0] {
	case "queries":
		return queries(args[1:])
	case "scaffold":
		return scaffold(args[1:])
	case "build":
		return build(args[1:])
	case "-h", "--help", "help":
		usage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "becky-genre: unknown command %q\n", args[0])
		usage()
		return 2
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "becky-genre — research a genre's theory into a permanent profile")
	fmt.Fprintln(os.Stderr, "  queries <genre>            print the research search queries")
	fmt.Fprintln(os.Stderr, "  scaffold <id>              print a blank 5-elements JSON to fill from research")
	fmt.Fprintln(os.Stderr, "  build --elements e.json [--out path]   distilled elements -> a genre profile")
}

func queries(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "becky-genre queries: name a genre")
		return 2
	}
	g := strings.Join(args, " ")
	fmt.Printf("Research %q — run these (aim at Hooktheory, Chordify, Ultimate Guitar):\n", g)
	for _, q := range genreprofile.Queries(g) {
		fmt.Println("  •", q)
	}
	fmt.Println("If Jordan named a song/artist, those references are GOLD — also run:")
	for _, q := range genreprofile.ReferenceQueries("<the named song/artist>") {
		fmt.Println("  •", q)
	}
	return 0
}

func scaffold(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "becky-genre scaffold: name an id (e.g. nightcore)")
		return 2
	}
	e := genreprofile.BlankElements(args[0])
	data, _ := json.MarshalIndent(e, "", "  ")
	fmt.Println(string(data))
	return 0
}

func build(args []string) int {
	elements, out := "", ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--elements":
			if i+1 < len(args) {
				elements = args[i+1]
				i++
			}
		case "--out":
			if i+1 < len(args) {
				out = args[i+1]
				i++
			}
		}
	}
	if elements == "" {
		fmt.Fprintln(os.Stderr, "becky-genre build: --elements e.json is required (from `scaffold` + research)")
		return 2
	}
	data, err := os.ReadFile(elements)
	if err != nil {
		fmt.Fprintln(os.Stderr, "becky-genre:", err)
		return 1
	}
	var e genreprofile.Elements
	if err := json.Unmarshal(data, &e); err != nil {
		fmt.Fprintln(os.Stderr, "becky-genre: parse elements:", err)
		return 1
	}
	p, err := genreprofile.ProfileFromElements(e)
	if err != nil {
		fmt.Fprintln(os.Stderr, "becky-genre:", err)
		return 1
	}
	if out == "" {
		// Default to the embedded profiles dir if it exists, else the cwd.
		if st, statErr := os.Stat(filepath.Join("internal", "music", "profiles")); statErr == nil && st.IsDir() {
			out = filepath.Join("internal", "music", "profiles", p.ID+".json")
		} else {
			out = p.ID + ".json"
		}
	}
	body, _ := json.MarshalIndent(p, "", "  ")
	if err := os.WriteFile(out, body, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "becky-genre:", err)
		return 1
	}
	fmt.Printf("✓ wrote genre profile %q → %s\n", p.ID, out)
	fmt.Printf("  %d progression(s), %d track role(s), %d section(s). It joins the DB on the next `go build`.\n",
		len(p.Progressions), len(p.Tracks), len(p.Arrangement))
	return 0
}
