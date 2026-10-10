// Package people answers "who is known to be here, between these two times?" from
// becky-identify's output, so tools that describe footage (becky-shots, becky-mediasearch) can
// NAME a person becky has actually identified, and only then.
//
// Only becky-identify's `identifications` count (voice segments, face/location frames). Its
// `unidentified` entries, including demoted near-misses, are never used: a name appears only
// when becky-identify was willing to state it.
package people

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// frameReach is how long a face/location sighting at one frame counts for, either side.
const frameReach = 1.0

// Span is one stretch where a named person is known to be present.
type Span struct {
	Name       string
	Start, End float64
	Basis      string // voice | face | location | corroborated
}

// Load reads a becky-identify JSON file. A missing path is not an error (no names known).
func Load(path string) ([]Span, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out struct {
		Identifications []struct {
			Type     string `json:"type"`
			Name     string `json:"name"`
			Segments []struct {
				Start float64 `json:"start"`
				End   float64 `json:"end"`
			} `json:"segments"`
			Frames []struct {
				Timestamp float64 `json:"timestamp"`
			} `json:"frames"`
		} `json:"identifications"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var spans []Span
	for _, id := range out.Identifications {
		if id.Name == "" {
			continue
		}
		for _, s := range id.Segments {
			spans = append(spans, Span{id.Name, s.Start, s.End, id.Type})
		}
		for _, f := range id.Frames {
			spans = append(spans, Span{id.Name, f.Timestamp - frameReach, f.Timestamp + frameReach, id.Type})
		}
	}
	return spans, nil
}

// In returns the names present anywhere in [a, b], sorted, each once.
func In(spans []Span, a, b float64) []string {
	seen := map[string]bool{}
	var names []string
	for _, s := range spans {
		if s.Start < b && a < s.End && !seen[s.Name] {
			seen[s.Name] = true
			names = append(names, s.Name)
		}
	}
	sort.Strings(names)
	return names
}

// Speaking returns the one name whose span covers most of [a, b] (at least half), else "".
// Used to put a name on a transcript line.
func Speaking(spans []Span, a, b float64) string {
	if b <= a {
		return ""
	}
	cover := map[string]float64{}
	for _, s := range spans {
		if o := min(b, s.End) - max(a, s.Start); o > 0 {
			cover[s.Name] += o
		}
	}
	best, bestO := "", 0.0
	for n, o := range cover {
		if o > bestO || (o == bestO && n < best) {
			best, bestO = n, o
		}
	}
	if bestO < (b-a)/2 {
		return ""
	}
	return best
}
