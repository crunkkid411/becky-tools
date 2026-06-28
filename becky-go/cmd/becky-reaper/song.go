package main

// song.go — `becky-reaper song`: the ONE-COMMAND AI-music path. It composes a song
// from a genre (becky-compose's engine), optionally applies plain-English edits
// (becky-daw ask's engine), and writes an openable, audible REAPER session — no
// intermediate files for the user to manage, no GUI, no GPU.
//
//	becky-reaper song --genre crunkcore --seed 7 \
//	    --do "set tempo to 96" --do "mute the sfx" \
//	    --out song.rpp [--render]
//
// It is the turnkey version of the proven three-step chain
// (becky-compose -> becky-daw ask -> becky-reaper build). Offline + deterministic.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"becky-go/internal/composearr"
	"becky-go/internal/ctledit"
	"becky-go/internal/ctlmodel"
	"becky-go/internal/music"
	"becky-go/internal/reaper"
)

func cmdSong(args []string) error {
	fs := flag.NewFlagSet("song", flag.ContinueOnError)
	genre := fs.String("genre", "", "genre id OR band/album (e.g. crunkcore, \"underoath safety\")")
	key := fs.String("key", "", "key e.g. F#m, Am (default: genre default)")
	bpm := fs.Int("bpm", 0, "tempo BPM (default: genre default)")
	seed := fs.Int64("seed", 1, "deterministic seed (same seed => same song)")
	out := fs.String("out", "song.rpp", "output .rpp path")
	render := fs.Bool("render", false, "also bounce a WAV via REAPER (needs REAPER installed)")
	var dos multiString
	fs.Var(&dos, "do", "a plain-English edit to apply (repeatable), e.g. --do \"mute the sfx\"")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *genre == "" {
		return fmt.Errorf("song needs --genre <id> (try: becky-compose --list)")
	}

	// 1) Compose in-memory.
	p, err := music.ResolveProfile(*genre)
	if err != nil {
		return err
	}
	s := music.Generate(p, *key, *bpm, *seed)
	fmt.Printf("composed %s in %s %s @ %d BPM (seed %d), %d tracks\n", s.Genre, s.Root, s.Scale, s.BPM, s.Seed, len(s.Tracks))

	// 2) Materialize stems+project into a temp dir so composearr can build the routed
	//    arrangement (it reads per-track .mid stems from disk). Cleaned up after.
	tmp, err := os.MkdirTemp("", "becky-song-")
	if err != nil {
		return fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)
	if err := materializeProject(tmp, s); err != nil {
		return err
	}

	proj, baseDir, err := composearr.LoadProject(filepath.Join(tmp, "project.json"))
	if err != nil {
		return err
	}
	arr, cerr := composearr.FromProject(proj, baseDir)
	if cerr != nil {
		fmt.Fprintf(os.Stderr, "warning: %v\n", cerr)
	}
	if arr == nil || len(arr.Tracks) == 0 {
		return fmt.Errorf("no tracks converted from the composed project")
	}

	// 3) Apply plain-English edits (offline keyword path; model path when wired).
	if len(dos) > 0 {
		proposer := ctlmodel.PickProposer()
		applied, skipped := 0, 0
		for _, phrase := range dos {
			batch := proposer.Propose(phrase, arr)
			if len(batch.Edits) == 0 {
				fmt.Printf("  • %q -> becky: %s\n", phrase, batch.Summary)
				continue
			}
			next, res, aerr := ctledit.Apply(arr, batch, nil)
			if aerr != nil {
				return fmt.Errorf("apply %q: %w", phrase, aerr)
			}
			arr = next
			applied += res.Applied
			skipped += res.Skipped
			fmt.Printf("  • %q -> %s (applied %d, skipped %d)\n", phrase, batch.Summary, res.Applied, res.Skipped)
		}
		fmt.Printf("edits: %d applied, %d skipped\n", applied, skipped)
	}

	// 4) Author the REAPER session.
	rp := reaper.FromArrangement(arr, renderTarget(*out, *render))
	if err := writeAndMaybeRender(rp, *out, *render); err != nil {
		return err
	}
	fmt.Printf("wrote %s — open it in REAPER\n", *out)
	return nil
}

// materializeProject writes the song's per-track .mid stems + project.json into dir,
// mirroring becky-compose's on-disk layout so composearr can load it.
func materializeProject(dir string, s *music.Song) error {
	for _, nt := range s.Tracks {
		path := filepath.Join(dir, nt.Name+".mid")
		if err := os.WriteFile(path, s.TrackSMF(nt).Bytes(), 0o644); err != nil {
			return fmt.Errorf("write stem %s: %w", nt.Name, err)
		}
	}
	pj, err := json.MarshalIndent(s.Routing, "", "  ")
	if err != nil {
		return fmt.Errorf("encode project: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "project.json"), append(pj, '\n'), 0o644); err != nil {
		return fmt.Errorf("write project.json: %w", err)
	}
	return nil
}

// multiString collects a repeatable string flag (--do "x" --do "y").
type multiString []string

func (m *multiString) String() string {
	if m == nil {
		return ""
	}
	return fmt.Sprint([]string(*m))
}
func (m *multiString) Set(v string) error {
	*m = append(*m, v)
	return nil
}
