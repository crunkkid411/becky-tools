// report.go — aggregation + parsing helpers for the orchestrator ops. Parses the
// JSON the underlying becky tools emit and rolls per-video identify results up into
// a coverage report ("<name> recognized in N/M videos") with the actual clips.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// AppearanceReport is the becky appearances/profile coverage result.
type AppearanceReport struct {
	Name          string       `json:"name"`
	KB            string       `json:"kb"`
	Corpus        string       `json:"corpus"`
	TotalVideos   int          `json:"total_videos"`
	MatchedVideos int          `json:"matched_videos"`
	VoiceMatches  int          `json:"voice_matches"`
	FaceMatches   int          `json:"face_matches"`
	Appearances   []Appearance `json:"appearances"`
	Errors        []VideoError `json:"errors,omitempty"`
	Disclaimer    string       `json:"disclaimer"`
	matchedSet    map[string]bool
}

// Appearance is one recognized moment with its clip/frame back-reference.
type Appearance struct {
	Video      string  `json:"video"`
	Modality   string  `json:"modality"` // voice | face | location
	Confidence float64 `json:"confidence"`
	SpeakerID  string  `json:"speaker_id,omitempty"`
	Timestamp  float64 `json:"timestamp,omitempty"`
}

// VideoError records a per-video identify failure (kept; the sweep continues).
type VideoError struct {
	Video string `json:"video"`
	Error string `json:"error"`
}

// newAppearanceReport starts an empty report for a name over a corpus.
func newAppearanceReport(name, kb, corpus string) *AppearanceReport {
	return &AppearanceReport{
		Name:        name,
		KB:          absOr(kb),
		Corpus:      absOr(corpus),
		Appearances: []Appearance{},
		matchedSet:  map[string]bool{},
		Disclaimer:  "candidate identifications for human review; becky surfaces moments, it does not conclude",
	}
}

// identifyOut mirrors the becky-identify JSON contract (the subset we read).
type identifyOut struct {
	File            string `json:"file"`
	Identifications []struct {
		Type       string  `json:"type"`
		Name       string  `json:"name"`
		Confidence float64 `json:"confidence"`
		SpeakerID  string  `json:"speaker_id"`
		Frames     []struct {
			Timestamp float64 `json:"timestamp"`
		} `json:"frames"`
	} `json:"identifications"`
}

// ingest folds one video's becky-identify output into the report, keeping only
// identifications whose name matches the target person (case-insensitive, with a
// loose alias-style contains match so "John" matches "John Anthony Clancy").
func (r *AppearanceReport) ingest(video string, identifyJSON []byte) {
	r.TotalVideos++
	var out identifyOut
	if err := json.Unmarshal(identifyJSON, &out); err != nil {
		r.Errors = append(r.Errors, VideoError{Video: video, Error: "parse identify output: " + err.Error()})
		return
	}
	for _, id := range out.Identifications {
		if !nameRefersTo(id.Name, r.Name) {
			continue
		}
		ts := 0.0
		if len(id.Frames) > 0 {
			ts = id.Frames[0].Timestamp
		}
		r.Appearances = append(r.Appearances, Appearance{
			Video:      video,
			Modality:   id.Type,
			Confidence: id.Confidence,
			SpeakerID:  id.SpeakerID,
			Timestamp:  ts,
		})
		r.matchedSet[video] = true
		switch id.Type {
		case "voice":
			r.VoiceMatches++
		case "face":
			r.FaceMatches++
		}
	}
}

// addError records a per-video failure and counts the attempted video.
func (r *AppearanceReport) addError(video, msg string) {
	r.TotalVideos++
	r.Errors = append(r.Errors, VideoError{Video: video, Error: msg})
}

// finalize computes the matched-video count after the sweep.
func (r *AppearanceReport) finalize() {
	r.MatchedVideos = len(r.matchedSet)
}

// nameRefersTo loosely matches an identified name against a queried name: exact,
// or one is contained in the other (so "John" finds "John Anthony Clancy").
func nameRefersTo(identified, query string) bool {
	a := coreName(identified)
	b := coreName(query)
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	// Whole-word token-subset (NOT raw substring): every word of the shorter name
	// must appear as a full word in the other, so "John", "John Clancy", or "Clancy"
	// resolve to "John Anthony Clancy", while "John Clancy" does NOT match
	// "Bettina Burke-Clancy (John Clancy's mother)" — its descriptive parenthetical
	// is stripped by coreName and "clancy's" is not the word "clancy".
	return tokenSubset(identified, query) || tokenSubset(query, identified)
}

// coreName lower-cases, trims, and drops any trailing descriptive parenthetical
// — "Bettina Burke-Clancy (John Clancy's mother)" -> "bettina burke-clancy" — so a
// description that mentions another person can't hijack the match.
func coreName(s string) string {
	if i := strings.IndexByte(s, '('); i >= 0 {
		s = s[:i]
	}
	return strings.ToLower(strings.TrimSpace(s))
}

// nameTokens splits a coreName into its set of whole words, stripping surrounding
// punctuation and a trailing possessive so "clancy's" -> "clancy".
func nameTokens(s string) map[string]bool {
	set := make(map[string]bool)
	for _, w := range strings.Fields(coreName(s)) {
		w = strings.Trim(w, ".,'\"")
		w = strings.TrimSuffix(w, "'s")
		if w != "" {
			set[w] = true
		}
	}
	return set
}

// tokenSubset reports whether every whole word of sub appears in full's word set
// (requires >=1 sub word). Used both directions so the shorter name can be either.
func tokenSubset(full, sub string) bool {
	fullSet := nameTokens(full)
	subSet := nameTokens(sub)
	if len(subSet) == 0 {
		return false
	}
	for w := range subSet {
		if !fullSet[w] {
			return false
		}
	}
	return true
}

// EntityCard is the KB metadata surfaced in a profile.
type EntityCard struct {
	Name        string   `json:"name"`
	Aliases     []string `json:"aliases"`
	Description string   `json:"description"`
	VoicePrints int      `json:"voice_prints"`
	FacePrints  int      `json:"face_prints"`
}

// loadEntityCard finds the person in the KB (by entities/*.json name/alias, or by a
// voice-prints/face-prints subdir name) and returns their card. The bool reports
// whether the person is enrolled (has any voice or face print).
func loadEntityCard(kb, name string) (bool, EntityCard) {
	card := EntityCard{Name: name, Aliases: []string{}}

	files, _ := filepath.Glob(filepath.Join(kb, "entities", "*.json"))
	best := 0
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var e EntityCard
		if json.Unmarshal(data, &e) != nil {
			continue
		}
		// Keep the STRONGEST match (exact name > exact alias > substring > token-subset)
		// so "John Clancy" resolves to "John Anthony Clancy" rather than a weaker hit.
		if s := matchScore(e.Name, e.Aliases, name); s > best {
			best = s
			card.Name = e.Name
			card.Aliases = e.Aliases
			card.Description = e.Description
		}
	}

	card.VoicePrints = countPrintDir(kb, "voice-prints", card.Name)
	card.FacePrints = countPrintDir(kb, "face-prints", card.Name)
	enrolled := card.VoicePrints > 0 || card.FacePrints > 0
	return enrolled, card
}

// countPrintDir counts files in the KB print dir whose subdir matches the RESOLVED
// entity name. It deliberately does NOT also match the raw query: matching the query
// would credit the resolved person with another entity's prints (e.g. "John Clancy"
// counting John's dir even after resolving to the wrong entity). When resolution
// fails, resolvedName is the query itself, so a query-named dir is still found.
func countPrintDir(kb, kind, resolvedName string) int {
	base := filepath.Join(kb, kind)
	entries, err := os.ReadDir(base)
	if err != nil {
		return 0
	}
	total := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if nameRefersTo(e.Name(), resolvedName) {
			fs, _ := os.ReadDir(filepath.Join(base, e.Name()))
			for _, f := range fs {
				if !f.IsDir() {
					total++
				}
			}
		}
	}
	return total
}

func aliasRefersTo(aliases []string, name string) bool {
	for _, a := range aliases {
		if nameRefersTo(a, name) {
			return true
		}
	}
	return false
}

// matchScore ranks how strongly query names this entity: 4 exact (core) name,
// 3 exact alias, 2 whole-word token-subset, 1 fuzzy alias, 0 no match. Higher wins
// so an exact "John Clancy" beats a token-subset of "John Anthony Clancy", and a
// descriptive parenthetical can never out-rank the real owner of the name.
func matchScore(entityName string, aliases []string, query string) int {
	a := coreName(entityName)
	b := coreName(query)
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 4
	}
	for _, al := range aliases {
		if coreName(al) == b {
			return 3
		}
	}
	if tokenSubset(entityName, query) || tokenSubset(query, entityName) {
		return 2
	}
	if aliasRefersTo(aliases, query) {
		return 1
	}
	return 0
}

// --- search + pipeline parsers ---

// searchResult is one becky-search hit (the subset we surface).
type searchResult struct {
	Rank       int     `json:"rank"`
	SourceFile string  `json:"source_file"`
	Timestamp  float64 `json:"timestamp"`
	Text       string  `json:"text"`
	Speaker    string  `json:"speaker"`
	Similarity float64 `json:"similarity"`
	Matched    string  `json:"matched"`
}

type searchResults struct {
	results []searchResult
}

// parseSearchResults reads becky-search stdout. The schema nests hits under
// "results"; we tolerate either {results:[...]} or a bare [...] array.
func parseSearchResults(stdout []byte) searchResults {
	var wrapper struct {
		Results []searchResult `json:"results"`
	}
	if json.Unmarshal(stdout, &wrapper) == nil && wrapper.Results != nil {
		return searchResults{results: wrapper.Results}
	}
	var arr []searchResult
	if json.Unmarshal(stdout, &arr) == nil {
		return searchResults{results: arr}
	}
	return searchResults{}
}

// pipelineSummary is the rolled-up becky-pipeline manifest view used by index.
type pipelineSummary struct {
	totalVideos   int
	okVideos      int
	partialVideos int
	embedFailed   bool
}

// parsePipelineManifest reads the becky-pipeline manifest to summarize index runs.
func parsePipelineManifest(stdout []byte) pipelineSummary {
	var man struct {
		Videos []struct {
			Status string `json:"status"`
			Steps  []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"steps"`
		} `json:"videos"`
	}
	var s pipelineSummary
	if json.Unmarshal(stdout, &man) != nil {
		return s
	}
	for _, v := range man.Videos {
		s.totalVideos++
		switch v.Status {
		case "ok":
			s.okVideos++
		case "partial":
			s.partialVideos++
		}
		for _, st := range v.Steps {
			if st.Name == "embed" && st.Status == "failed" {
				s.embedFailed = true
			}
		}
	}
	return s
}
