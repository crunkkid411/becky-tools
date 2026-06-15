// ids.go — deterministic node-id and label construction. Stable ids are what make
// the graph reproducible: the same entity always maps to the same node_id, and
// the same entity pair always maps to the same edge_id, regardless of ingest
// order. All slugging is lowercase/ASCII so ids are filesystem- and URL-safe.
package palantir

import (
	"fmt"
	"sort"
	"strings"
)

// sanitizeID turns an arbitrary label into a stable, lowercase, hyphenated slug.
// Non-alphanumeric runs collapse to a single hyphen; leading/trailing hyphens are
// trimmed. An empty result becomes "unknown" so a node_id is never blank.
func sanitizeID(s string) string {
	var b strings.Builder
	lastHyphen := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastHyphen = false
		default:
			if !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "unknown"
	}
	return out
}

// personNodeID is the stable id for a named person ("person:john-clancy").
func personNodeID(name string) string { return "person:" + sanitizeID(name) }

// deviceNodeID is the stable id for an EXIF device ("device:apple-iphone-13").
func deviceNodeID(dev string) string { return "device:" + sanitizeID(dev) }

// eventNodeID is the stable id for an event, keyed by clip+type+timestamp so two
// distinct events in the same clip never collide.
func eventNodeID(src, typ string, ts float64) string {
	return fmt.Sprintf("event:%s:%s@%.1f", sanitizeID(typ), sanitizeID(src), ts)
}

// placeNodeID prefers GPS (rounded to a coarse grid so near-identical fixes share
// a node) and falls back to a slugged place name.
func placeNodeID(lat, lon float64, place string) string {
	if lat != 0 || lon != 0 {
		return fmt.Sprintf("place:gps:%.1f_%.1f", lat, lon)
	}
	return "place:" + sanitizeID(place)
}

// placeLabel is the plain-language label for a place node.
func placeLabel(lat, lon float64, place string) string {
	if strings.TrimSpace(place) != "" {
		return place
	}
	if lat != 0 || lon != 0 {
		return fmt.Sprintf("location near %.4f, %.4f (candidate)", lat, lon)
	}
	return "unknown place"
}

// pairEdgeID builds a stable, order-independent edge id for two node ids. The two
// endpoints are sorted so a~b and b~a yield the same id (undirected edges).
func pairEdgeID(kind, a, b string) string {
	lo, hi := a, b
	if hi < lo {
		lo, hi = hi, lo
	}
	return fmt.Sprintf("%s:%s~%s", kind, lo, hi)
}

// dedupeSorted returns the unique non-empty strings in s, sorted (deterministic).
func dedupeSorted(s []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(s))
	for _, v := range s {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
