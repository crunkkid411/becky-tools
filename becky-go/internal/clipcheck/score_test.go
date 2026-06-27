package clipcheck

import (
	"fmt"
	"strings"
	"testing"
)

// words makes a block of n distinct words ("p0 p1 ... p{n-1}") so word-overlap is
// meaningful (distinct tokens, not a single repeated word).
func words(prefix string, n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = fmt.Sprintf("%s%d", prefix, i)
	}
	return strings.Join(parts, " ")
}

func pageOf(blocks ...string) PageContent {
	joined := strings.Join(blocks, " ")
	return PageContent{PageText: joined, FullText: joined, MainBlocks: blocks}
}

func TestScore_passWhenClipHasAllBlocks(t *testing.T) {
	a, b := words("a", 45), words("b", 45)
	pc := pageOf(a, b)
	res := Score(a+"\n\n"+b, pc)
	if res.Verdict != VerdictPass {
		t.Fatalf("verdict = %q (recall %.2f precision %.2f), want pass", res.Verdict, res.Recall, res.Precision)
	}
	if res.Recall != 1.0 {
		t.Errorf("recall = %.2f, want 1.0", res.Recall)
	}
}

func TestScore_failWhenClipDropsBlocks(t *testing.T) {
	a, b, c, d := words("a", 45), words("b", 45), words("c", 45), words("d", 45)
	pc := pageOf(a, b, c, d)
	res := Score(a, pc) // clip has only 1 of 4 blocks
	if res.Verdict != VerdictFail {
		t.Fatalf("verdict = %q (recall %.2f), want fail (dropped 3 of 4 blocks)", res.Verdict, res.Recall)
	}
	if res.Recall != 0.25 {
		t.Errorf("recall = %.2f, want 0.25", res.Recall)
	}
}

func TestScore_partialWhenHalfPresent(t *testing.T) {
	a, b := words("a", 45), words("b", 45)
	pc := pageOf(a, b)
	res := Score(a, pc) // clip has 1 of 2 blocks -> recall 0.5 -> borderline
	if res.Verdict != VerdictPartial {
		t.Fatalf("verdict = %q (recall %.2f), want partial at the 0.5 boundary", res.Verdict, res.Recall)
	}
}

func TestScore_failWhenEmptyClip(t *testing.T) {
	pc := pageOf(words("a", 45))
	res := Score("   \n  ", pc)
	if res.Verdict != VerdictFail {
		t.Fatalf("empty clip should fail, got %q", res.Verdict)
	}
	if res.MDWords != 0 {
		t.Errorf("md_words = %d, want 0", res.MDWords)
	}
}

func TestScore_thinPage(t *testing.T) {
	// A page with one short block (< 40 words) is "thin", not a failure.
	short := words("t", 12)
	pc := PageContent{PageText: short, FullText: short, MainBlocks: []string{short}}
	res := Score(short, pc)
	if res.Verdict != VerdictThin {
		t.Fatalf("verdict = %q, want thin", res.Verdict)
	}
	if res.Recall != 1.0 {
		t.Errorf("thin recall = %.2f, want 1.0", res.Recall)
	}
}

func TestScore_lowPrecisionGatesToPartial(t *testing.T) {
	// Clip contains the one real block plus 40 invented words not on the page:
	// recall is perfect but precision drops below threshold -> partial, not pass.
	a := words("a", 45)
	pc := pageOf(a)
	invented := words("zzz", 40)
	res := Score(a+" "+invented, pc)
	if res.Verdict != VerdictPartial {
		t.Fatalf("verdict = %q (recall %.2f precision %.2f), want partial", res.Verdict, res.Recall, res.Precision)
	}
	if res.Precision >= passPrecision {
		t.Errorf("precision = %.2f, expected below %.2f", res.Precision, passPrecision)
	}
}

func TestScore_ignoresChromeBlocks(t *testing.T) {
	// Regression: a page's footer/legal boilerplate is a >=40-word <p> block but is
	// NOT in the main text. A clip that captured the article (and rightly dropped
	// the footer) must still PASS — the chrome block must not count as "missing".
	article := words("a", 45)
	footer := words("foot", 45) // disjoint vocab; not part of the main text
	pc := PageContent{
		PageText:   article,                   // main content only
		FullText:   article + " " + footer,    // footer is visible, but chrome
		MainBlocks: []string{article, footer}, // bs4 sees both
	}
	res := Score(article, pc) // clip has the article, not the footer
	if res.Verdict != VerdictPass {
		t.Fatalf("verdict = %q (recall %.2f, units %d), want pass — footer block must be gated out",
			res.Verdict, res.Recall, res.Units)
	}
	if res.Units != 1 {
		t.Errorf("units = %d, want 1 (only the article block counts)", res.Units)
	}
}

func TestScore_sentenceFallbackWhenNoBlocks(t *testing.T) {
	// No MainBlocks: recall falls back to sentence coverage of PageText. Each
	// sentence is long enough that the page is not "thin".
	s1 := words("s", 25) + "."
	s2 := words("q", 25) + "."
	pc := PageContent{PageText: s1 + " " + s2, FullText: s1 + " " + s2}
	res := Score(s1+" "+s2, pc)
	if res.Units != 2 {
		t.Fatalf("want 2 sentence units, got %d", res.Units)
	}
	if res.Verdict != VerdictPass {
		t.Errorf("verdict = %q, want pass (both sentences present)", res.Verdict)
	}
}
