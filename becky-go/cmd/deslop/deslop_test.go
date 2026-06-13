// deslop_test.go — table-driven tests covering several AI-tell categories, the
// markdown skip rule, format gating, determinism, and the custom-rules path.
package main

import (
	"strings"
	"testing"
)

// findCategory reports whether any finding has the given category.
func findCategory(fs []Finding, cat string) bool {
	for _, f := range fs {
		if f.Category == cat {
			return true
		}
	}
	return false
}

// TestCategoryDetection verifies representative slop triggers each major
// rule-driven category and that cleaned text drops/rewrites the cliche.
func TestCategoryDetection(t *testing.T) {
	rules := builtinRules()
	cases := []struct {
		name        string
		input       string
		wantCat     string
		wantCleaned string // substring that must appear in cleaned output ("" = skip)
		absentClean string // substring that must NOT appear in cleaned output ("" = skip)
	}{
		{"cliche-utilize", "We utilize the cache heavily.", catCliche, "We use the cache", "utilize"},
		{"cliche-leverage", "They leverage robust tooling.", catCliche, "They use", "leverage"},
		{"cliche-delve", "Let us delve into the data.", catCliche, "examine", "delve"},
		{"copula-serves", "The library serves as a bridge.", catCopula, "is a bridge", "serves as a"},
		{"copula-boasts", "The city boasts a rich past.", catCopula, "has a rich", "boasts a"},
		{"meta-in-article", "In this article, we explain caching.", catMeta, "", "In this article"},
		{"puffery-testament", "Her work is a testament to grit.", catPuffery, "", "testament"},
		{"closer-future", "The future looks bright for the team.", catGenericCloser, "", "future looks bright"},
		{"dangling-ing", "Revenue grew, highlighting its importance to us.", catDangling, "Revenue grew", "highlighting"},
		{"fluff-in-order", "We refactored in order to ship faster.", catFluff, "refactored to ship", "in order to"},
		{"redundant-very-unique", "It is a very unique design.", catRedundant, "a unique design", "very unique"},
		{"passive-flag", "The server was restarted by the team.", catPassive, "", ""},
		{"novelty-flag", "Here is the insight everyone's missing.", catNovelty, "", ""},
		{"emotional-flag", "What surprised me most was the latency.", catEmotionalFlat, "", ""},
		{"symbolism", "Under the hood it uses goroutines.", catInflatedSymbol, "internally", "Under the hood"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := process(tc.input, rules, "full")
			if !findCategory(res.Findings, tc.wantCat) {
				t.Fatalf("expected category %q for %q; findings=%+v", tc.wantCat, tc.input, res.Findings)
			}
			if tc.wantCleaned != "" && !strings.Contains(res.Cleaned, tc.wantCleaned) {
				t.Errorf("cleaned %q missing %q", res.Cleaned, tc.wantCleaned)
			}
			if tc.absentClean != "" && strings.Contains(res.Cleaned, tc.absentClean) {
				t.Errorf("cleaned %q still contains %q", res.Cleaned, tc.absentClean)
			}
		})
	}
}

// TestCurlyQuotes checks ChatGPT curly quotes are flagged and straightened.
func TestCurlyQuotes(t *testing.T) {
	rules := builtinRules()
	in := "He said “hello” and it’s fine."
	res := process(in, rules, "full")
	if !findCategory(res.Findings, catCurlyQuote) {
		t.Fatalf("expected curly-quote finding; got %+v", res.Findings)
	}
	if strings.ContainsAny(res.Cleaned, "“”‘’") {
		t.Errorf("curly quotes remain in cleaned output: %q", res.Cleaned)
	}
	if !strings.Contains(res.Cleaned, `"hello"`) || !strings.Contains(res.Cleaned, "it's") {
		t.Errorf("quotes not straightened correctly: %q", res.Cleaned)
	}
}

// TestMarkdownSkip is the headline correctness test: cliches inside fenced code,
// inline code, and YAML frontmatter must be neither flagged nor changed.
func TestMarkdownSkip(t *testing.T) {
	rules := builtinRules()

	t.Run("fenced-code", func(t *testing.T) {
		in := "Prose: we leverage tools.\n\n```\nwe leverage utilize delve\n```\n\nMore prose: utilize this.\n"
		res := process(in, rules, "full")
		if !strings.Contains(res.Cleaned, "we leverage utilize delve") {
			t.Errorf("fenced code was altered: %q", res.Cleaned)
		}
		if strings.Contains(res.Cleaned, "we leverage tools") {
			t.Errorf("prose outside fence not cleaned: %q", res.Cleaned)
		}
		for _, f := range res.Findings {
			if f.Line == 4 {
				t.Errorf("finding inside fenced code block: %+v", f)
			}
		}
	})

	t.Run("inline-code", func(t *testing.T) {
		in := "Call `utilize()` here but utilize prose."
		res := process(in, rules, "full")
		if !strings.Contains(res.Cleaned, "`utilize()`") {
			t.Errorf("inline code was altered: %q", res.Cleaned)
		}
		if strings.Contains(res.Cleaned, "but utilize prose") {
			t.Errorf("prose after inline code not cleaned: %q", res.Cleaned)
		}
	})

	t.Run("frontmatter", func(t *testing.T) {
		in := "---\ntitle: We utilize delve leverage\n---\n\nBody: we utilize delve.\n"
		res := process(in, rules, "full")
		if !strings.Contains(res.Cleaned, "title: We utilize delve leverage") {
			t.Errorf("frontmatter was altered: %q", res.Cleaned)
		}
		if strings.Contains(res.Cleaned, "Body: we utilize delve") {
			t.Errorf("body after frontmatter not cleaned: %q", res.Cleaned)
		}
	})
}

// TestFormatGating verifies minimal < full < aggressive rule activation.
func TestFormatGating(t *testing.T) {
	rules := builtinRules()
	in := "We utilize robust tools in order to win."

	minRes := process(in, rules, "minimal")
	if !findCategory(minRes.Findings, catCliche) {
		t.Errorf("minimal should flag utilize: %+v", minRes.Findings)
	}
	if findCategory(minRes.Findings, catFluff) {
		t.Errorf("minimal should NOT flag fluff: %+v", minRes.Findings)
	}

	fullRes := process(in, rules, "full")
	if !findCategory(fullRes.Findings, catFluff) {
		t.Errorf("full should flag fluff (in order to): %+v", fullRes.Findings)
	}
	if strings.Contains(fullRes.Cleaned, "solid tools") {
		t.Errorf("full should not rewrite bare 'robust': %q", fullRes.Cleaned)
	}

	aggRes := process(in, rules, "aggressive")
	if !strings.Contains(aggRes.Cleaned, "solid") {
		t.Errorf("aggressive should rewrite bare 'robust': %q", aggRes.Cleaned)
	}
}

// TestDashSeparator: em-dash/double-dash flagged in full, rewritten in
// aggressive, and CLI flags never flagged.
func TestDashSeparator(t *testing.T) {
	rules := builtinRules()

	in := "The system is fast -- it handles 1K req/s."
	full := process(in, rules, "full")
	if !findCategory(full.Findings, catDashSeparator) {
		t.Errorf("full should flag dash separator: %+v", full.Findings)
	}
	if full.Cleaned != in {
		t.Errorf("full should not rewrite dash separator: %q", full.Cleaned)
	}
	agg := process(in, rules, "aggressive")
	if !strings.Contains(agg.Cleaned, "fast. It handles") {
		t.Errorf("aggressive should split on dash: %q", agg.Cleaned)
	}

	cli := "Use the --verbose flag to enable logging."
	cliRes := process(cli, rules, "aggressive")
	if findCategory(cliRes.Findings, catDashSeparator) {
		t.Errorf("CLI flag --verbose must not be flagged: %+v", cliRes.Findings)
	}
	if cliRes.Cleaned != cli {
		t.Errorf("CLI flag text must not change: %q", cliRes.Cleaned)
	}
}

// TestStructuralCategories exercises the document-level heuristics.
func TestStructuralCategories(t *testing.T) {
	rules := builtinRules()

	t.Run("monotony", func(t *testing.T) {
		in := "The cat sat on the mat. The dog ran in the park. The bird flew over the lake. The fish swam in the pond. The fox hid in the den."
		res := process(in, rules, "full")
		if !findCategory(res.Findings, catMonotony) {
			t.Errorf("expected monotony finding: %+v", res.Findings)
		}
	})

	t.Run("synonym-cycling", func(t *testing.T) {
		in := "We faced challenges. These obstacles slowed us. The hurdles took weeks to clear."
		res := process(in, rules, "full")
		if !findCategory(res.Findings, catSynonymCycling) {
			t.Errorf("expected synonym-cycling finding: %+v", res.Findings)
		}
	})

	t.Run("rule-of-three", func(t *testing.T) {
		in := "It was fast, cheap, and easy. We tested apples, oranges, and pears. The plan was bold, clear, and simple. Stay calm, cool, and collected."
		res := process(in, rules, "full")
		if !findCategory(res.Findings, catRuleOfThree) {
			t.Errorf("expected rule-of-three overuse finding: %+v", res.Findings)
		}
	})
}

// TestCleanInputZeroFindings: natural human text yields no findings.
func TestCleanInputZeroFindings(t *testing.T) {
	rules := builtinRules()
	in := "The cache was stale. I spent three hours debugging before realizing the CDN had a 24-hour TTL. Changed it to 5 minutes and the problem went away."
	res := process(in, rules, "full")
	if !res.Clean || len(res.Findings) != 0 {
		t.Errorf("expected clean natural text; got %d findings: %+v", len(res.Findings), res.Findings)
	}
}

// TestDeterminism: same input + format yields byte-identical cleaned output.
func TestDeterminism(t *testing.T) {
	rules := builtinRules()
	in := "In this article we utilize robust tools, leveraging synergy to facilitate growth."
	a := process(in, rules, "aggressive")
	b := process(in, rules, "aggressive")
	if a.Cleaned != b.Cleaned {
		t.Errorf("non-deterministic cleaned output:\n a=%q\n b=%q", a.Cleaned, b.Cleaned)
	}
	if a.Score != b.Score || len(a.Findings) != len(b.Findings) {
		t.Errorf("non-deterministic findings/score")
	}
}

// TestLineCol checks 1-based line/column reporting on a multi-line input.
func TestLineCol(t *testing.T) {
	rules := builtinRules()
	in := "Clean line one.\nWe utilize this thing.\n"
	res := process(in, rules, "full")
	var got *Finding
	for i := range res.Findings {
		if res.Findings[i].Category == catCliche {
			got = &res.Findings[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("no cliche finding: %+v", res.Findings)
	}
	if got.Line != 2 {
		t.Errorf("expected line 2, got %d", got.Line)
	}
	if got.Col != 4 { // "We " then "utilize" starts at col 4
		t.Errorf("expected col 4, got %d", got.Col)
	}
}

// TestCustomRules verifies a --rules override fully replaces the embedded set.
func TestCustomRules(t *testing.T) {
	jf := jsonRuleFile{Rules: []jsonRule{
		{Category: "custom", Pattern: `(?i)\bfoobar\b`, Replacement: "baz", Weight: 5, Format: "full"},
	}}
	rules, err := rulesFromJSON(jf)
	if err != nil {
		t.Fatalf("rulesFromJSON: %v", err)
	}
	res := process("This foobar should change but utilize should not.", rules, "full")
	if !strings.Contains(res.Cleaned, "This baz should change") {
		t.Errorf("custom rule not applied: %q", res.Cleaned)
	}
	if !strings.Contains(res.Cleaned, "utilize should not") {
		t.Errorf("embedded rules should be absent under custom ruleset: %q", res.Cleaned)
	}
}

// TestCountsAlwaysComplete: counts map seeds every category even when clean.
func TestCountsAlwaysComplete(t *testing.T) {
	rules := builtinRules()
	res := process("Nothing to see here.", rules, "full")
	for _, cat := range categoryList() {
		if _, ok := res.Counts[cat]; !ok {
			t.Errorf("counts missing category %q", cat)
		}
	}
}
