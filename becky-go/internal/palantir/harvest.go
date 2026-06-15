// harvest.go — corpus discovery (the front of step A). Walks a corpus root, finds
// becky evidence JSON by filename convention, and routes each file to the right
// ingest reader. Discovery never fails the run: an unreadable corpus yields an empty
// Ingest with a plain-language note (degrade-never-crash), and the orchestrator
// still emits a valid (empty) graph.
//
// Classification is by basename substring on *.json:
//
//	*identify*.json -> AddIdentify   *event*.json   -> AddEvents
//	*osint*.json    -> AddOSINT      *cluster*.json -> AddCluster
//
// A single explicit --cluster file is also accepted (ClusterPath) since clusters are
// corpus-wide, not per-clip.
package palantir

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"becky-go/internal/pathx"
)

// HarvestOptions selects what to ingest. Root is walked recursively; ClusterPath, if
// set, is folded in explicitly (a corpus-wide cluster file living outside the walk).
type HarvestOptions struct {
	Root        string
	ClusterPath string
}

// Harvest discovers and ingests every recognized becky output under opts.Root,
// returning the populated Ingest. Discovery errors become notes, never failures.
func Harvest(opts HarvestOptions) Ingest {
	in := Ingest{}
	paths, walkErr := discover(opts.Root)
	if walkErr != "" {
		in.Notes = append(in.Notes, walkErr)
	}
	for _, p := range paths {
		routeFile(&in, p)
	}
	if strings.TrimSpace(opts.ClusterPath) != "" {
		in.AddCluster(opts.ClusterPath)
	}
	in.Sort()
	return in
}

// discover walks root and returns the recognized *.json evidence files in sorted
// order (so ingest order — and the input hash — is deterministic).
func discover(root string) ([]string, string) {
	if strings.TrimSpace(root) == "" {
		return nil, "no corpus root given; nothing to ingest."
	}
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries, keep walking
		}
		if d.IsDir() {
			return nil
		}
		if classify(path) != "" {
			found = append(found, path)
		}
		return nil
	})
	sort.Strings(found)
	if err != nil {
		return found, "couldn't fully read the corpus directory: " + err.Error()
	}
	if len(found) == 0 {
		return found, "no becky evidence JSON found under the corpus root (expected identify/events/osint/cluster *.json)."
	}
	return found, ""
}

// routeFile dispatches one discovered file to its reader by classification.
func routeFile(in *Ingest, path string) {
	switch classify(path) {
	case "identify":
		in.AddIdentify(path)
	case "events":
		in.AddEvents(path)
	case "osint":
		in.AddOSINT(path)
	case "cluster":
		in.AddCluster(path)
	}
}

// classify returns the evidence kind for a path by basename substring, or "" if the
// file is not a recognized becky output. Order matters: cluster is checked before
// the generic readers so "cluster.json" is never misread.
func classify(path string) string {
	name := strings.ToLower(pathx.Base(path))
	if !strings.HasSuffix(name, ".json") {
		return ""
	}
	switch {
	case strings.Contains(name, "cluster"):
		return "cluster"
	case strings.Contains(name, "identify"):
		return "identify"
	case strings.Contains(name, "event"):
		return "events"
	case strings.Contains(name, "osint"):
		return "osint"
	}
	return ""
}
