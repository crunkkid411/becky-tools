// dwelling.go — group ROOMS into DWELLINGS and produce the corpus verdict.
// A room is one physical space; a dwelling is a set of rooms in one residence.
// Two clips can be different rooms of the same dwelling, so this layer reasons
// ABOVE the room clusters on shared-decor + metadata signals (SPEC §2d).
//
// Corroborate-then-conclude throughout: a dwelling merge needs ≥2 agreeing
// dwelling signals (shared color/decor family + capture-time/GPS proximity); a
// lone signal leaves the verdict UNDETERMINED rather than guessing.
package location

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// VerdictLevel is the corpus-level conclusion.
type VerdictLevel string

const (
	SameRoom       VerdictLevel = "SAME_ROOM"
	SameDwelling   VerdictLevel = "SAME_DWELLING"
	DifferentDwell VerdictLevel = "DIFFERENT_DWELLING"
	Undetermined   VerdictLevel = "UNDETERMINED"
)

// Dwelling groups rooms into one residence with the basis that joined them.
type Dwelling struct {
	ID      string
	RoomIDs []string
	Basis   []string
}

// Verdict is the headline conclusion plus its corroborating basis.
type Verdict struct {
	Headline   string
	Level      VerdictLevel
	Confidence float64
	Basis      []string
}

// DwellingParams tunes the dwelling layer.
type DwellingParams struct {
	// SharedColorChi2 is the max color chi-square between two rooms' medoid
	// fingerprints to count "shared decor/paint family" as a dwelling signal.
	SharedColorChi2 float64
	// CaptureWindow is the max gap between two clips' capture times to count
	// capture-time proximity as a dwelling signal.
	CaptureWindow time.Duration
	// MinSignals dwelling signals required to merge two rooms into one dwelling.
	MinSignals int
}

// DefaultDwellingParams returns the documented defaults.
func DefaultDwellingParams() DwellingParams {
	return DwellingParams{
		SharedColorChi2: 0.35, // looser than same-ROOM color (different rooms, same palette)
		CaptureWindow:   30 * time.Minute,
		MinSignals:      2,
	}
}

// GroupDwellings groups rooms into dwellings and returns the dwellings + the
// corpus verdict. clips supplies per-clip metadata + fingerprints; cr is the room
// clustering already computed.
func GroupDwellings(clips []Clip, cr ClusterResult, t Thresholds, dp DwellingParams) ([]Dwelling, Verdict) {
	active := nonDegraded(clips)
	byIdx := map[int]Clip{}
	for _, c := range active {
		byIdx[c.Index] = c
	}

	// Singleton / one-clip corpus: cannot conclude a relationship.
	if len(active) == 0 {
		return nil, Verdict{
			Level:      Undetermined,
			Confidence: 0,
			Headline:   "No clips could be fingerprinted; no location verdict possible.",
			Basis:      []string{"no usable clips"},
		}
	}
	if len(active) == 1 {
		return nil, Verdict{
			Level:      Undetermined,
			Confidence: 0,
			Headline:   "Only one clip provided — nothing to compare against.",
			Basis:      []string{"only one clip provided"},
		}
	}

	// Single room across all clips → SAME_ROOM headline.
	if len(cr.Rooms) == 1 {
		conf := cr.Rooms[0].Cohesion
		dw := []Dwelling{{ID: "dwelling-1", RoomIDs: []string{cr.Rooms[0].ID}, Basis: []string{"all clips in one room"}}}
		return dw, Verdict{
			Level:      SameRoom,
			Confidence: round3(conf),
			Headline:   fmt.Sprintf("All %d clips were filmed in the SAME room.", len(active)),
			Basis:      sameRoomBasis(cr),
		}
	}

	// Multiple rooms: decide dwelling grouping room-by-room.
	dwellings, mergeBasis := mergeRoomsIntoDwellings(cr, byIdx, dp)

	roomN := len(cr.Rooms)
	dwellN := len(dwellings)

	switch {
	case dwellN == 1:
		conf := dwellingConfidence(mergeBasis)
		return dwellings, Verdict{
			Level:      SameDwelling,
			Confidence: round3(conf),
			Headline:   fmt.Sprintf("%d distinct rooms across %d clips, all consistent with ONE dwelling.", roomN, len(active)),
			Basis:      mergeBasis,
		}
	case dwellN == roomN:
		// No two rooms shared ≥MinSignals dwelling signals → different places,
		// PROVIDED the rooms are also visually far apart. If they conflict (some
		// shared signal but not enough), say UNDETERMINED instead of overclaiming.
		if anyLoneDwellingSignal(cr, byIdx, dp) {
			return dwellings, Verdict{
				Level:      Undetermined,
				Confidence: 0.4,
				Headline:   fmt.Sprintf("%d distinct rooms; a single weak shared signal — cannot conclude same vs different dwelling.", roomN),
				Basis:      []string{"only one dwelling signal present between some rooms — needs a human"},
			}
		}
		return dwellings, Verdict{
			Level:      DifferentDwell,
			Confidence: 0.8,
			Headline:   fmt.Sprintf("%d distinct rooms across %d clips, filmed in DIFFERENT dwellings.", roomN, len(active)),
			Basis:      []string{"no two rooms share ≥2 dwelling signals (decor family / capture-time / GPS)"},
		}
	default:
		// Partial grouping: some rooms grouped, some not.
		return dwellings, Verdict{
			Level:      SameDwelling,
			Confidence: round3(dwellingConfidence(mergeBasis) * 0.9),
			Headline:   fmt.Sprintf("%d rooms across %d clips form %d dwelling(s).", roomN, len(active), dwellN),
			Basis:      mergeBasis,
		}
	}
}

// mergeRoomsIntoDwellings unions rooms that share ≥dp.MinSignals dwelling
// signals. Deterministic: rooms processed in id order, lowest-index canonical.
func mergeRoomsIntoDwellings(cr ClusterResult, byIdx map[int]Clip, dp DwellingParams) ([]Dwelling, []string) {
	rooms := cr.Rooms
	n := len(rooms)
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra == rb {
			return
		}
		if ra < rb {
			parent[rb] = ra
		} else {
			parent[ra] = rb
		}
	}

	var basis []string
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			sigs := dwellingSignals(rooms[i], rooms[j], byIdx, dp)
			if len(sigs) >= dp.MinSignals {
				union(i, j)
				basis = append(basis, fmt.Sprintf("%s and %s: %s (%d signals)",
					rooms[i].ID, rooms[j].ID, strings.Join(sigs, " + "), len(sigs)))
			}
		}
	}

	members := map[int][]string{}
	for i := 0; i < n; i++ {
		r := find(i)
		members[r] = append(members[r], rooms[i].ID)
	}
	roots := make([]int, 0, len(members))
	for r := range members {
		roots = append(roots, r)
	}
	sort.Ints(roots)
	out := make([]Dwelling, 0, len(roots))
	for di, r := range roots {
		ids := members[r]
		sort.Strings(ids)
		b := []string{"distinct rooms in one residence"}
		if len(ids) == 1 {
			b = []string{"standalone room"}
		}
		out = append(out, Dwelling{
			ID:      fmt.Sprintf("dwelling-%d", di+1),
			RoomIDs: ids,
			Basis:   b,
		})
	}
	return out, basis
}

// dwellingSignals returns the names of the INDEPENDENT dwelling signals shared by
// two rooms: shared decor/paint family (color), capture-time proximity, and
// GPS co-location. Each is one corroborating point.
func dwellingSignals(a, b Room, byIdx map[int]Clip, dp DwellingParams) []string {
	var sigs []string

	// Shared decor/paint family: color chi-square between the rooms' medoid
	// fingerprints under the looser dwelling threshold.
	fa := roomMedoidPrint(a, byIdx)
	fb := roomMedoidPrint(b, byIdx)
	if c := colorChi2(fa.ColorHist, fb.ColorHist); c >= 0 && c <= dp.SharedColorChi2 {
		sigs = append(sigs, "shared decor/paint palette")
	}

	// Capture-time proximity (any cross-room clip pair within the window).
	if captureProximate(a, b, byIdx, dp.CaptureWindow) {
		sigs = append(sigs, "capture times within window")
	}

	// GPS co-location (any cross-room clip pair within ~50m).
	if gpsProximate(a, b, byIdx) {
		sigs = append(sigs, "GPS co-located")
	}
	return sigs
}

// anyLoneDwellingSignal reports whether any room pair shares EXACTLY one dwelling
// signal — the "one weak signal → UNDETERMINED" case.
func anyLoneDwellingSignal(cr ClusterResult, byIdx map[int]Clip, dp DwellingParams) bool {
	for i := 0; i < len(cr.Rooms); i++ {
		for j := i + 1; j < len(cr.Rooms); j++ {
			if len(dwellingSignals(cr.Rooms[i], cr.Rooms[j], byIdx, dp)) == 1 {
				return true
			}
		}
	}
	return false
}

func roomMedoidPrint(r Room, byIdx map[int]Clip) Fingerprint {
	if len(r.Clips) == 0 {
		return Fingerprint{}
	}
	return byIdx[r.Clips[0]].Print // clips are sorted; first is a stable representative
}

func captureProximate(a, b Room, byIdx map[int]Clip, window time.Duration) bool {
	for _, ai := range a.Clips {
		ta, oka := parseTime(byIdx[ai].CaptureTime)
		if !oka {
			continue
		}
		for _, bi := range b.Clips {
			tb, okb := parseTime(byIdx[bi].CaptureTime)
			if !okb {
				continue
			}
			d := ta.Sub(tb)
			if d < 0 {
				d = -d
			}
			if d <= window {
				return true
			}
		}
	}
	return false
}

func gpsProximate(a, b Room, byIdx map[int]Clip) bool {
	for _, ai := range a.Clips {
		la, loa, oka := parseGPS(byIdx[ai].GPS)
		if !oka {
			continue
		}
		for _, bi := range b.Clips {
			lb, lob, okb := parseGPS(byIdx[bi].GPS)
			if !okb {
				continue
			}
			if haversineMeters(la, loa, lb, lob) <= 50 {
				return true
			}
		}
	}
	return false
}

func parseTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	if t, err := time.Parse("2006-01-02T15:04:05", s); err == nil {
		return t, true
	}
	return time.Time{}, false
}

func parseGPS(s string) (float64, float64, bool) {
	if s == "" {
		return 0, 0, false
	}
	parts := strings.Split(s, ",")
	if len(parts) != 2 {
		return 0, 0, false
	}
	lat, e1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	lon, e2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if e1 != nil || e2 != nil {
		return 0, 0, false
	}
	return lat, lon, true
}

func haversineMeters(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371000.0
	rad := func(d float64) float64 { return d * math.Pi / 180 }
	dLat := rad(lat2 - lat1)
	dLon := rad(lon2 - lon1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(rad(lat1))*math.Cos(rad(lat2))*math.Sin(dLon/2)*math.Sin(dLon/2)
	return r * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

func dwellingConfidence(basis []string) float64 {
	if len(basis) == 0 {
		return 0.6
	}
	// More corroborating room links → higher confidence, capped.
	c := 0.7 + 0.08*float64(len(basis))
	if c > 0.95 {
		c = 0.95
	}
	return c
}

func sameRoomBasis(cr ClusterResult) []string {
	if len(cr.Rooms) == 0 {
		return nil
	}
	return []string{fmt.Sprintf("all clips cluster into one room (cohesion %.2f)", cr.Rooms[0].Cohesion)}
}

func nonDegraded(clips []Clip) []Clip {
	out := make([]Clip, 0, len(clips))
	for _, c := range clips {
		if c.Degraded == "" {
			out = append(out, c)
		}
	}
	return out
}
