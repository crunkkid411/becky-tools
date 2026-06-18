package footage

// grep.go is the Tier-0 deterministic candidate retrieval that feeds the funnel
// (R-AI §3.2 step [1]). Given the folder index and a set of literal terms, it
// scans every transcript sidecar segment for term hits and returns ranked
// Candidate cue snippets {source, timestamp, text, score}. No model, no DB — pure
// keyword matching over the already-parsed transcript segments, so it runs in
// milliseconds and keeps go test green offline.
//
// This is the LITERAL half of retrieval. Semantic retrieval (vector KNN over
// forensic.db) is the caller's job via the becky-search binary; the funnel
// merges both. Keeping grep here means the deterministic floor always works even
// with every model and the DB absent (R-AI §1.3: "terminal floor is Tier 0").

import (
	"sort"
	"strings"

	"becky-go/internal/sidecar"
)

// Candidate is one retrieved cue snippet: a transcript segment that matched the
// query, with verbatim source + timestamps so a downstream add_clip uses real
// cue boundaries (never invented times). Score is a simple deterministic
// relevance (count of distinct matched terms, then total occurrences) used only
// to rank/cap before the model ever sees it.
type Candidate struct {
	Source    string   `json:"source"`    // absolute path to the source video
	Name      string   `json:"name"`      // video basename
	Timestamp float64  `json:"timestamp"` // segment start, seconds into source
	End       float64  `json:"end"`       // segment end, seconds into source
	Text      string   `json:"text"`      // the matched cue text (verbatim)
	Score     float64  `json:"score"`     // deterministic rank score (higher = better)
	Terms     []string `json:"terms"`     // which query terms hit this cue
}

// GrepTranscripts scans the transcripts of every video in the index for the
// given terms (case-insensitive substring match) and returns matching cue
// snippets, ranked best-first and deterministically ordered. Matching is OR
// across terms; a cue that hits more distinct terms ranks higher. Empty/blank
// terms are ignored; a term list that is entirely blank yields no candidates.
//
// Read-only and degrade-never-crash: a transcript that fails to parse is skipped
// (its video simply contributes no candidates), never fatal. The video bytes are
// never opened.
func GrepTranscripts(index FolderIndex, terms []string) []Candidate {
	norm := normalizeTerms(terms)
	if len(norm) == 0 {
		return []Candidate{}
	}

	out := []Candidate{}
	for _, v := range index.Videos {
		if !v.HasTranscript {
			continue
		}
		sub, err := sidecar.ParseSubtitle(v.TranscriptPath)
		if err != nil {
			continue // degrade: unreadable transcript contributes nothing
		}
		for _, seg := range sub.Segments {
			hay := strings.ToLower(seg.Text)
			var hitTerms []string
			occurrences := 0
			for _, term := range norm {
				if c := strings.Count(hay, term); c > 0 {
					hitTerms = append(hitTerms, term)
					occurrences += c
				}
			}
			if len(hitTerms) == 0 {
				continue
			}
			out = append(out, Candidate{
				Source:    v.Path,
				Name:      v.Name,
				Timestamp: seg.Start,
				End:       seg.End,
				Text:      seg.Text,
				Score:     float64(len(hitTerms))*1000 + float64(occurrences),
				Terms:     hitTerms,
			})
		}
	}

	// Deterministic order: score desc, then source path, then timestamp — so the
	// same index+terms always produce the same ranked, capped list.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		return out[i].Timestamp < out[j].Timestamp
	})
	return out
}

// normalizeTerms lowercases, trims, and drops blank terms, de-duplicating while
// preserving first-seen order so the score is stable.
func normalizeTerms(terms []string) []string {
	seen := make(map[string]bool, len(terms))
	out := make([]string, 0, len(terms))
	for _, t := range terms {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}
