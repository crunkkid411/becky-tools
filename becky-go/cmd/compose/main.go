// becky-compose — deterministic, genre-aware multi-track MIDI generator.
//
//	becky-compose --genre crunkcore [--key F#m] [--bpm 150] [--seed 1] [--out dir]
//	becky-compose --list
//
// Emits one Standard MIDI File per track (drums, bass, chords, melody, lead,
// counter, sfx), a combined multi-track song.mid, and project.json (routing) into
// --out. Pure-Go, offline, deterministic: the SAME flags produce byte-identical
// files. Open the per-track .mid stems in any DAW (or becky-canvas) and tweak to
// taste; project.json carries the bus/sidechain routing so it loads sanely.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"becky-go/internal/music"
)

func main() {
	genre := flag.String("genre", "", "genre id OR band/album (e.g. crunkcore, \"underoath safety\", tocs)")
	key := flag.String("key", "", "key e.g. F#m, Am (default: genre default)")
	bpm := flag.Int("bpm", 0, "tempo BPM (default: genre default)")
	seed := flag.Int64("seed", 1, "deterministic seed (same seed => same song)")
	out := flag.String("out", "", "output directory (default: ./<genre>-<seed>)")
	list := flag.Bool("list", false, "list known genres and exit")
	flag.Parse()

	if *list {
		fmt.Println("known genres:", strings.Join(music.KnownGenres(), ", "))
		return
	}
	if *genre == "" {
		fmt.Fprintln(os.Stderr, "usage: becky-compose --genre <id> [--key F#m] [--bpm N] [--seed N] [--out dir]")
		fmt.Fprintln(os.Stderr, "known genres:", strings.Join(music.KnownGenres(), ", "))
		os.Exit(2)
	}

	p, err := music.ResolveProfile(*genre)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	song := music.Generate(p, *key, *bpm, *seed)
	if label := profileLabel(p); label != "" {
		fmt.Printf("matched: %s\n", label)
	}

	dir := *out
	if dir == "" {
		dir = fmt.Sprintf("%s-%d", song.Genre, song.Seed)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	for _, nt := range song.Tracks {
		path := filepath.Join(dir, nt.Name+".mid")
		if err := os.WriteFile(path, song.TrackSMF(nt).Bytes(), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "song.mid"), song.SMF().Bytes(), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	pj, err := json.MarshalIndent(song.Routing, "", "  ")
	if err == nil {
		err = os.WriteFile(filepath.Join(dir, "project.json"), append(pj, '\n'), 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	fmt.Printf("becky-compose: %s in %s %s @ %d BPM (seed %d)\n", song.Genre, song.Root, song.Scale, song.BPM, song.Seed)
	fmt.Printf("  progression: %s\n", strings.Join(song.Prog, " "))
	fmt.Printf("  %d tracks -> %s/\n", len(song.Tracks), dir)
	for _, nt := range song.Tracks {
		fmt.Printf("    %-8s ch%-2d -> %s.mid\n", nt.Name, nt.Channel, nt.Name)
	}
	fmt.Printf("  song.mid (all tracks) + project.json (808 isolated; kick sidechains music + 808)\n")
}

// profileLabel describes which profile resolved (album-specific when present).
func profileLabel(p music.Profile) string {
	name := p.DisplayName
	if name == "" {
		name = p.ID
	}
	switch {
	case p.Artist != "" && p.Album != "":
		return fmt.Sprintf("%s — %s \"%s\"", name, p.Artist, p.Album)
	case p.Artist != "":
		return fmt.Sprintf("%s — %s", name, p.Artist)
	default:
		return name
	}
}
