// Package clipcheck deterministically scores whether a saved markdown clip
// faithfully captured a web page's content. It is the verification half of the
// iPhone-history archiver: becky-web2md writes the .md, becky-clipcheck confirms
// the .md actually CONTAINS the page (not just that the file exists).
//
// The scoring here is pure and offline — given the markdown body and the live
// page's text (re-fetched by clipfetch.py), it returns recall (did the clip drop
// content?), precision (did the clip invent content?), and a verdict. No model is
// involved; a local LLM is only consulted by the caller for the borderline
// "partial" verdict (corroborate-then-conclude: the deterministic signal decides
// the clear cases; AI adjudicates only the genuinely ambiguous middle).
package clipcheck

import (
	"regexp"
	"strings"
)

// Verdicts.
const (
	VerdictPass    = "pass"    // the clip captured the page
	VerdictPartial = "partial" // borderline — caller may escalate to a local model
	VerdictFail    = "fail"    // the clip dropped significant content (or is empty)
	VerdictThin    = "thin"    // the page itself has little text; the clip captured what there was
)

// Thresholds. Each is a deliberate, testable cutoff — not a magic number buried
// in a conditional.
const (
	passRecall      = 0.85 // >= this fraction of page content blocks present in the clip -> pass
	passPrecision   = 0.90 // >= this fraction of clip words present on the page -> not hallucinated
	failRecall      = 0.50 // < this fraction of page content present -> fail (content dropped)
	blockCovered    = 0.80 // a content block counts as "covered" at this word-overlap ratio
	thinTotalWords  = 40   // a page with fewer content words than this is "thin", not a failure
	mainContentGate = 0.60 // a block counts as "real content" only if this much of it is in the main text
)

// PageContent is the live page's text, as re-extracted by clipfetch.py. PageText
// is trafilatura's main-content text; FullText is all visible text (precision
// denominator); MainBlocks are the substantial paragraphs used for recall.
type PageContent struct {
	PageText   string   `json:"page_text"`
	FullText   string   `json:"full_text"`
	MainBlocks []string `json:"main_blocks"`
}

// Result is the deterministic verdict plus the numbers behind it.
type Result struct {
	Verdict   string  `json:"verdict"`
	Recall    float64 `json:"recall"`
	Precision float64 `json:"precision"`
	Coverage  float64 `json:"coverage"` // char-length ratio md/page (sanity, not a gate)
	Units     int     `json:"units"`    // content blocks/sentences checked
	Covered   int     `json:"covered"`  // how many were found in the clip
	MDWords   int     `json:"md_words"`
	PageWords int     `json:"page_words"`
	Reason    string  `json:"reason"`
}

var (
	linkURLRe = regexp.MustCompile(`\(https?://[^)\s]+[^)]*\)`) // markdown link/image target
	wordRe    = regexp.MustCompile(`[a-z0-9]{2,}`)
	sentRe    = regexp.MustCompile(`[.!?]+|\n+`)
)

// Score compares the markdown body against the page content and returns a verdict.
// Pure: same inputs -> same output, no I/O.
func Score(mdBody string, pc PageContent) Result {
	mdWords := wordSet(mdBody)
	res := Result{MDWords: len(mdWords)}

	// Units of ground-truth content. The blocks come from the WHOLE page (bs4), so
	// they include site chrome — footers, legal text, "related" rails — that the
	// article extraction correctly dropped. Gate each block by how much of it is in
	// the main text (page_text): real article paragraphs overlap it heavily; chrome
	// barely does. This is what stops a good clip from looking like it "dropped
	// content" just because the page has a boilerplate footer. Fall back to
	// sentences of the main text when a site uses no <p>/<li> structure.
	mainVocab := wordSet(pc.PageText)
	gate := len(mainVocab) > 0
	var units []string
	for _, b := range pc.MainBlocks {
		if !gate || overlapRatio(wordSet(b), mainVocab) >= mainContentGate {
			units = append(units, b)
		}
	}
	if len(units) == 0 {
		units = sentences(pc.PageText, 8)
	}
	res.Units = len(units)

	pageWordCount := countWords(pc.PageText)
	if pageWordCount == 0 {
		pageWordCount = totalWords(units)
	}
	res.PageWords = pageWordCount

	if len(mdWords) == 0 {
		res.Verdict = VerdictFail
		res.Reason = "the markdown file has no body text"
		return res
	}

	// Precision: what fraction of the clip's words actually appear on the page?
	// (Guards against template junk / mojibake / hallucinated text.)
	pageVocab := wordSet(pc.FullText + " " + pc.PageText + " " + strings.Join(units, " "))
	res.Precision = overlapRatio(mdWords, pageVocab)

	// Thin page: genuinely little content (a tweet, a product page). Capturing the
	// little there is is success, not failure.
	if res.Units == 0 || totalWords(units) < thinTotalWords {
		res.Verdict = VerdictThin
		res.Recall = 1.0
		res.Coverage = coverageRatio(mdBody, pc.PageText)
		res.Reason = "page has little body text; clip captured what was present"
		return res
	}

	// Recall: how many content blocks survived into the clip?
	covered := 0
	for _, u := range units {
		uw := wordSet(u)
		if len(uw) == 0 {
			continue
		}
		if overlapRatio(uw, mdWords) >= blockCovered {
			covered++
		}
	}
	res.Covered = covered
	res.Recall = float64(covered) / float64(res.Units)
	res.Coverage = coverageRatio(mdBody, pc.PageText)

	switch {
	case res.Recall >= passRecall && res.Precision >= passPrecision:
		res.Verdict = VerdictPass
		res.Reason = "clip contains the page's content"
	case res.Recall < failRecall:
		res.Verdict = VerdictFail
		res.Reason = "clip is missing a large share of the page's content"
	default:
		res.Verdict = VerdictPartial
		res.Reason = "clip captured most but not all of the page's content"
	}
	return res
}

// wordSet returns the set of normalized content words in s (markdown link/image
// targets stripped first so URLs don't pollute the token set).
func wordSet(s string) map[string]struct{} {
	s = linkURLRe.ReplaceAllString(strings.ToLower(s), " ")
	set := make(map[string]struct{})
	for _, w := range wordRe.FindAllString(s, -1) {
		set[w] = struct{}{}
	}
	return set
}

// overlapRatio is |a ∩ b| / |a|.
func overlapRatio(a, b map[string]struct{}) float64 {
	if len(a) == 0 {
		return 0
	}
	hit := 0
	for w := range a {
		if _, ok := b[w]; ok {
			hit++
		}
	}
	return float64(hit) / float64(len(a))
}

// sentences splits text into sentences of at least minWords words.
func sentences(text string, minWords int) []string {
	var out []string
	for _, raw := range sentRe.Split(text, -1) {
		s := strings.TrimSpace(raw)
		if len(strings.Fields(s)) >= minWords {
			out = append(out, s)
		}
	}
	return out
}

func countWords(s string) int { return len(wordRe.FindAllString(strings.ToLower(s), -1)) }

func totalWords(units []string) int {
	n := 0
	for _, u := range units {
		n += len(strings.Fields(u))
	}
	return n
}

// coverageRatio is the char-length ratio md/page, clamped to [0,1] for display.
func coverageRatio(md, page string) float64 {
	if len(page) == 0 {
		return 0
	}
	r := float64(len(md)) / float64(len(page))
	if r > 1 {
		r = 1
	}
	return r
}
