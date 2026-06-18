// index.go — the verbatim text→cue mapping ported from make_quote_srt.py
// (normalize / build_index / find_fragment_positions / find_fragment_end /
// locate). This is the machinery that lets the tool take a quote STRING and snap
// it back to the exact source cues that contain it, so emitted timestamps are
// copied verbatim from the transcript (SPEC §6, hard rule #4).
//
// The map is: concatenate every cue's normalized text into one long string,
// remembering for each character which cue it came from (char→cue index). A
// matched span [begin,end] in that string then resolves to [cues[begin],
// cues[end]] — real cue boundaries, never invented times.
package quotes

import (
	"regexp"
	"strings"
)

// cueIndex is the searchable normalized transcript plus the per-character map
// back to source cue positions. Build once per run, query many times.
type cueIndex struct {
	cues      []Cue
	normText  string // all cues' normalized text joined by single spaces
	charToCue []int  // charToCue[i] = index into cues for normText[i]
}

var nonAlnumRE = regexp.MustCompile(`[^a-z0-9 ]+`)
var multiSpaceRE = regexp.MustCompile(`\s+`)

// normalize lowercases, drops punctuation to spaces, and collapses whitespace —
// identical to the prototype's normalize(), so matching behaves the same.
func normalize(s string) string {
	s = strings.ToLower(s)
	s = nonAlnumRE.ReplaceAllString(s, " ")
	s = multiSpaceRE.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// buildIndex constructs the char→cue map over the cues' normalized text. Cues
// whose normalized text is empty are skipped (they contribute no searchable
// characters but still exist in the cue list for timestamp lookups).
func buildIndex(cues []Cue) *cueIndex {
	var b strings.Builder
	charToCue := make([]int, 0, 4096)
	for i, c := range cues {
		nt := normalize(c.Text)
		if nt == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
			charToCue = append(charToCue, i)
		}
		b.WriteString(nt)
		for range nt {
			charToCue = append(charToCue, i)
		}
	}
	return &cueIndex{cues: cues, normText: b.String(), charToCue: charToCue}
}

// fragmentPositions returns every begin offset (in normText) where the longest
// findable candidate of frag occurs, plus that candidate's length. It tries the
// whole fragment first, then progressively shorter leading n-word prefixes —
// matching the prototype so a near-verbatim quote still anchors. Empty result
// means the fragment is not in the transcript at all.
func (idx *cueIndex) fragmentPositions(frag string) ([]int, int) {
	words := strings.Fields(frag)
	if len(words) == 0 {
		return nil, 0
	}
	candidates := []string{frag}
	for _, n := range []int{10, 8, 6, 4, 3} {
		if len(words) > n {
			candidates = append(candidates, strings.Join(words[:n], " "))
		}
	}
	for _, cand := range candidates {
		positions := allIndexes(idx.normText, cand)
		if len(positions) > 0 {
			return positions, len(cand)
		}
	}
	return nil, 0
}

// fragmentEnd returns the end-char of the best contiguous match for frag,
// searched at/after fromPos and then anywhere — the prototype's
// find_fragment_end. Used to close a "a ... b" joined quote on its tail phrase.
func (idx *cueIndex) fragmentEnd(frag string, fromPos int) (int, bool) {
	words := strings.Fields(frag)
	if len(words) == 0 {
		return 0, false
	}
	candidates := []string{frag}
	for _, n := range []int{10, 8, 6, 4, 3} {
		if len(words) > n {
			candidates = append(candidates, strings.Join(words[len(words)-n:], " "))
		}
	}
	base := min(fromPos, len(idx.normText))
	for _, cand := range candidates {
		if p := strings.Index(idx.normText[base:], cand); p != -1 {
			return base + p + len(cand) - 1, true
		}
		if p := strings.Index(idx.normText, cand); p != -1 {
			return p + len(cand) - 1, true
		}
	}
	return 0, false
}

// ellipsisRE splits a quote on "..."/"…" so a "<opening> ... <closing>" quote
// resolves to a span from the opening phrase to the closing phrase.
var ellipsisRE = regexp.MustCompile(`\.{2,}|…`)

// locate maps a quote string to a cue span [startCue,endCue]. hint (seconds; <0
// means none) disambiguates a recurring opening phrase by nearest cue start —
// but the region boundaries always come from the matched cues, never the hint
// (faithful to the prototype's locate()). Returns ok=false if the quote's
// opening fragment is not found verbatim in the transcript.
func (idx *cueIndex) locate(quote string, maxRegionSec float64, hint float64) (startCue, endCue int, ok bool) {
	rawFrags := ellipsisRE.Split(quote, -1)
	frags := make([]string, 0, len(rawFrags))
	for _, f := range rawFrags {
		frags = append(frags, normalize(f))
	}
	var substantial []string
	for _, f := range frags {
		if len(strings.Fields(f)) >= 3 {
			substantial = append(substantial, f)
		}
	}
	var startFrag, endFrag string
	if len(substantial) > 0 {
		startFrag = substantial[0]
		endFrag = substantial[len(substantial)-1]
	} else {
		// fall back to the single longest normalized fragment
		longest := ""
		for _, f := range frags {
			if len(f) > len(longest) {
				longest = f
			}
		}
		if longest == "" {
			return 0, 0, false
		}
		startFrag, endFrag = longest, longest
	}

	positions, mlen := idx.fragmentPositions(startFrag)
	if len(positions) == 0 {
		return 0, 0, false
	}
	sBegin := positions[0]
	if hint >= 0 {
		best := positions[0]
		bestDelta := absf(idx.cues[idx.charToCue[best]].Start - hint)
		for _, p := range positions[1:] {
			d := absf(idx.cues[idx.charToCue[p]].Start - hint)
			if d < bestDelta {
				best, bestDelta = p, d
			}
		}
		sBegin = best
	}
	sEnd := sBegin + mlen - 1

	eEnd := sEnd
	if endFrag != startFrag {
		if e, found := idx.fragmentEnd(endFrag, sBegin); found && e >= sBegin {
			eEnd = e
		}
	}

	startCue = idx.charToCue[clampIdx(sBegin, len(idx.charToCue))]
	endCue = idx.charToCue[clampIdx(eEnd, len(idx.charToCue))]
	if endCue < startCue {
		endCue = startCue
	}

	// runaway guard: a joined "a ... b" quote whose halves are far apart gets
	// clamped to the opening fragment's cues (prototype's max_region behavior).
	startTime := idx.cues[startCue].Start
	endTime := idx.cues[endCue].End
	if maxRegionSec > 0 && endTime-startTime > maxRegionSec {
		endCue = idx.charToCue[clampIdx(sEnd, len(idx.charToCue))]
		if endCue < startCue {
			endCue = startCue
		}
	}
	return startCue, endCue, true
}

// allIndexes returns every start offset of substr in s (advancing one position
// at a time, matching the prototype's positions loop).
func allIndexes(s, substr string) []int {
	if substr == "" {
		return nil
	}
	var out []int
	start := 0
	for {
		p := strings.Index(s[start:], substr)
		if p == -1 {
			break
		}
		out = append(out, start+p)
		start = start + p + 1
	}
	return out
}

func clampIdx(i, n int) int {
	if i < 0 {
		return 0
	}
	if i >= n {
		return n - 1
	}
	return i
}

func absf(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
