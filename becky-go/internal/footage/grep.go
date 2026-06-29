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
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"becky-go/internal/sidecar"
)

// transcriptCache memoizes parsed subtitles keyed by "path|modtime|size" so a
// re-search over the same folder doesn't re-read+re-parse the same .srt on every
// keystroke. Jordan's case has ~418 srt totalling ~60 MB; the FIRST search pays
// the parse cost, subsequent searches are near-instant. The modtime+size in the
// key means an externally-changed transcript is re-parsed automatically (a fresh
// becky-transcribe overwrite invalidates its entry). Best-effort and safe to
// share: a stat failure simply parses without caching. Process-lifetime memory,
// bounded by the number of distinct transcripts in the case folder.
var (
	transcriptCacheMu sync.Mutex
	transcriptCache   = map[string]sidecar.Subtitle{}
)

// parseSubtitleCached returns the parsed Subtitle for path, using the in-memory
// cache when the file's modtime+size are unchanged. On any stat/parse error it
// falls back to a direct parse (and does not cache), so a transient failure never
// poisons the cache. Concurrency-safe.
func parseSubtitleCached(path string) (sidecar.Subtitle, error) {
	fi, statErr := os.Stat(path)
	if statErr != nil {
		// Can't form a stable key — parse directly, don't cache.
		return sidecar.ParseSubtitle(path)
	}
	key := path + "|" + fi.ModTime().UTC().Format("20060102T150405.000000000") + "|" + itoa(fi.Size())

	transcriptCacheMu.Lock()
	if sub, ok := transcriptCache[key]; ok {
		transcriptCacheMu.Unlock()
		return sub, nil
	}
	transcriptCacheMu.Unlock()

	sub, err := sidecar.ParseSubtitle(path)
	if err != nil {
		return sidecar.Subtitle{}, err
	}
	transcriptCacheMu.Lock()
	transcriptCache[key] = sub
	transcriptCacheMu.Unlock()
	return sub, nil
}

// itoa is a tiny int64→string without importing strconv into the hot path's
// import list twice (kept local to the cache key builder).
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// Candidate is one retrieved cue snippet: a transcript segment that matched the
// query, with verbatim source + timestamps so a downstream add_clip uses real
// cue boundaries (never invented times). Score is a simple deterministic
// relevance (count of distinct matched terms, then total occurrences) used only
// to rank/cap before the model ever sees it.
type Candidate struct {
	Source    string   `json:"source"`         // absolute path to the source video
	Name      string   `json:"name"`           // video basename
	Date      string   `json:"date,omitempty"` // ISO YYYY-MM-DD from the yt-dlp file name, or "" (lets the UI sort hits by recording date)
	Timestamp float64  `json:"timestamp"`      // segment start, seconds into source
	End       float64  `json:"end"`            // segment end, seconds into source
	Text      string   `json:"text"`           // the matched cue text (verbatim)
	Score     float64  `json:"score"`          // deterministic rank score (higher = better)
	Terms     []string `json:"terms"`          // which query terms hit this cue
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
		sub, err := parseSubtitleCached(v.TranscriptPath)
		if err != nil {
			continue // degrade: unreadable transcript contributes nothing
		}
		out = appendSegmentHits(out, sub.Segments, norm, v.Path, v.Name, DateFromName(v.Name))
	}

	sortCandidates(out)
	return out
}

// GrepOrphans scans the index's ORPHAN transcripts (subtitles paired to no video)
// for the given terms and returns matching cue snippets. The shape matches
// GrepTranscripts so the funnel can merge both, with two deliberate differences
// per orphan: Source is "" (there is no video to play/extract) and Name carries
// the human-derived episode Title. This is what makes Jordan's 418 loose `.en.srt`
// searchable. Read-only and degrade-never-crash: an unreadable orphan transcript
// is skipped. Empty/blank terms yield no candidates.
func GrepOrphans(index FolderIndex, terms []string) []Candidate {
	norm := normalizeTerms(terms)
	if len(norm) == 0 {
		return []Candidate{}
	}
	out := []Candidate{}
	for _, o := range index.Orphans {
		sub, err := parseSubtitleCached(o.Path)
		if err != nil {
			continue // degrade: unreadable transcript contributes nothing
		}
		// Source "" marks a transcript-only hit (no playable/extractable video). The
		// date still comes from the ORIGINAL subtitle file name (Title has it stripped).
		out = appendSegmentHits(out, sub.Segments, norm, "", o.Title, DateFromName(filepath.Base(o.Path)))
	}
	sortCandidates(out)
	return out
}

// appendSegmentHits scans segs for the normalized terms and appends one Candidate
// per matching cue (verbatim text + timestamps) to dst, returning the grown
// slice. source/name set the Candidate's Source/Name (a video path+basename for
// real transcripts, "" + Title for orphans). Shared by GrepTranscripts and
// GrepOrphans so the match+score logic lives in exactly one place.
func appendSegmentHits(dst []Candidate, segs []sidecar.Segment, norm []string, source, name, date string) []Candidate {
	for _, seg := range segs {
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
		dst = append(dst, Candidate{
			Source:    source,
			Name:      name,
			Date:      date,
			Timestamp: seg.Start,
			End:       seg.End,
			Text:      seg.Text,
			Score:     float64(len(hitTerms))*1000 + float64(occurrences),
			Terms:     hitTerms,
		})
	}
	return dst
}

// sortCandidates imposes the deterministic order shared by both greps: score
// desc, then source path (orphans with "" source sort first, stably), then name
// (so two orphans from different episodes order by title), then timestamp — so
// the same index+terms always produce the same ranked, capped list.
func sortCandidates(out []Candidate) {
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Timestamp < out[j].Timestamp
	})
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
