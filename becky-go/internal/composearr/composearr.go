// Package composearr converts a becky-compose routing manifest (a music.Project
// plus its per-track .mid stems) into an editable dawmodel.Arrangement — so
// becky's generated music lands in REAPER as a real, bus-routed session (via
// reaper.FromArrangement) instead of a folder of loose .mid files. This is the
// "pipe becky-compose -> becky-reaper build" step from the REAPER DAW handoff.
//
// House rules:
//   - Deterministic: same project + stems -> same arrangement (stems converted in
//     manifest order; note IDs re-allocated monotonically).
//   - Degrade-never-crash: an unreadable/missing stem is SKIPPED with a wrapped
//     error (the rest of the song still converts); a bad project.json is a typed
//     error, never a panic.
package composearr

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"becky-go/internal/dawmodel"
	"becky-go/internal/music"
)

// LoadProject reads a becky-compose project.json and returns the parsed manifest
// plus its base directory (used to resolve each track's relative .mid path).
func LoadProject(path string) (music.Project, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return music.Project{}, "", fmt.Errorf("read project: %w", err)
	}
	var p music.Project
	if err := json.Unmarshal(raw, &p); err != nil {
		return music.Project{}, "", fmt.Errorf("parse project json: %w", err)
	}
	return p, filepath.Dir(path), nil
}

// FromProject builds a dawmodel.Arrangement from a compose manifest. baseDir
// resolves each ProjTrack.Midi. Each stem becomes one MIDI track routed to its
// declared bus (Strip.Bus = ProjTrack.Out); proj.Buses become arrangement buses;
// sidechain edges are attached to the bus they duck. Transport (tempo/key/time
// signature/PPQ) is lifted from the manifest.
//
// It returns the arrangement plus a non-nil error naming any stems that could not
// be read — the arrangement is still usable (partial), per degrade-never-crash.
func FromProject(proj music.Project, baseDir string) (*dawmodel.Arrangement, error) {
	a := dawmodel.New()
	a.Genre = proj.Genre
	if proj.Tempo > 0 {
		a.BPM = proj.Tempo
	}
	if proj.PPQ > 0 {
		a.PPQ = proj.PPQ
	}
	if len(proj.TimeSignature) == 2 && proj.TimeSignature[0] > 0 && proj.TimeSignature[1] > 0 {
		a.Num, a.Den = proj.TimeSignature[0], proj.TimeSignature[1]
	}
	a.Root, a.Scale = proj.Key.Root, proj.Key.Scale
	a.NextID = 1 // re-id merged notes from 1 (0 is the reserved/sentinel id)

	var skipped []string
	for _, pt := range proj.Tracks {
		clips, err := loadStemClips(a, baseDir, pt)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s (%v)", pt.ID, err))
			continue
		}
		a.Tracks = append(a.Tracks, dawmodel.Track{
			ID:    pt.ID,
			Kind:  dawmodel.KindMIDI,
			Clips: clips,
			Strip: dawmodel.Strip{Gain: 1, Pan: 0, Bus: pt.Out},
		})
	}

	a.Buses = busesFromProject(proj)
	applySidechain(a, proj)

	if len(skipped) > 0 {
		return a, fmt.Errorf("could not load %d stem(s): %s", len(skipped), strings.Join(skipped, "; "))
	}
	return a, nil
}

// loadStemClips reads one track's .mid stem and returns its non-empty MIDI clips,
// with every note re-id'd monotonically against the merged arrangement so the
// "note IDs never reused" invariant holds across stems.
func loadStemClips(a *dawmodel.Arrangement, baseDir string, pt music.ProjTrack) ([]dawmodel.Clip, error) {
	midi := strings.TrimSpace(pt.Midi)
	if midi == "" {
		return nil, fmt.Errorf("no midi path in manifest")
	}
	if !filepath.IsAbs(midi) {
		midi = filepath.Join(baseDir, midi)
	}
	data, err := os.ReadFile(midi)
	if err != nil {
		return nil, err
	}
	sub, perr := dawmodel.FromSMF(data) // sub may be a partial arrangement
	var clips []dawmodel.Clip
	if sub != nil {
		for _, t := range sub.Tracks {
			for _, c := range t.Clips {
				if len(c.Notes) == 0 {
					continue
				}
				for i := range c.Notes {
					c.Notes[i].ID = a.NextID
					a.NextID++
				}
				clips = append(clips, c)
			}
		}
	}
	if len(clips) == 0 {
		if perr != nil {
			return nil, fmt.Errorf("no notes: %w", perr)
		}
		return nil, fmt.Errorf("no notes in stem")
	}
	return clips, nil
}

// busesFromProject mirrors the manifest's bus list (id + output) into the
// arrangement's portable bus model. FX chains stay in the manifest; the
// arrangement only needs id/out for the REAPER folder tree + sidechain targets.
func busesFromProject(proj music.Project) []dawmodel.Bus {
	if len(proj.Buses) == 0 {
		return nil
	}
	out := make([]dawmodel.Bus, 0, len(proj.Buses))
	for _, pb := range proj.Buses {
		out = append(out, dawmodel.Bus{ID: pb.ID, Out: pb.Out})
	}
	return out
}

// applySidechain attaches each sidechain edge's source node to the bus it ducks.
// The edge's To may name the bus directly ("bus.808.compressor.sidechain") or an
// FX node on a bus ("comp.music.sidechain" -> the bus that hosts comp.music). An
// edge that can't be mapped to a known bus is skipped (corroborate-then-conclude:
// no guessing), not force-attached.
func applySidechain(a *dawmodel.Arrangement, proj music.Project) {
	if len(a.Buses) == 0 {
		return
	}
	fxBus := map[string]string{} // fx node id -> bus id
	for _, b := range proj.Buses {
		for _, fx := range b.FX {
			fxBus[fx.ID] = b.ID
		}
	}
	idx := map[string]int{} // bus id -> index in a.Buses
	for i, b := range a.Buses {
		idx[b.ID] = i
	}
	for _, e := range proj.Routing {
		if e.Kind != "sidechain" || strings.TrimSpace(e.From) == "" {
			continue
		}
		busID := sidechainTargetBus(e.To, proj.Buses, fxBus)
		if busID == "" {
			continue
		}
		i, ok := idx[busID]
		if !ok {
			continue
		}
		a.Buses[i].Sidechain = appendUnique(a.Buses[i].Sidechain, e.From)
	}
}

// sidechainTargetBus resolves a routing edge's To endpoint to the bus it controls.
func sidechainTargetBus(to string, buses []music.ProjBus, fxBus map[string]string) string {
	for _, b := range buses {
		if to == b.ID || strings.HasPrefix(to, b.ID+".") {
			return b.ID
		}
	}
	if bus, ok := fxBus[strings.TrimSuffix(to, ".sidechain")]; ok {
		return bus
	}
	return ""
}

func appendUnique(xs []string, x string) []string {
	for _, e := range xs {
		if e == x {
			return xs
		}
	}
	return append(xs, x)
}
