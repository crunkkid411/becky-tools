// rules.go — the embedded AI-tell ruleset for becky-deslop.
//
// Each Rule is a compiled regex plus a replacement string and metadata. Rules
// are grouped into the 17 AI-tell categories from the task spec. The data here
// is ported from the anti-ai-editor skill's cliche-replacements.md and
// detection-patterns.md references (Tier 1 through Tier CD-7) plus the
// WikiProject-AI-Cleanup copula list surfaced by the humanizer skill.
//
// Replacement semantics:
//   - A non-empty Replacement substitutes the match (capture groups via $1...).
//   - An empty Replacement means "remove": the match (and a trailing space, if
//     the removal would leave a double space) is deleted. Capitalization of the
//     following word is restored when the removed phrase started a sentence.
//   - flagOnly marks a rule detect-only: it surfaces in findings but never
//     rewrites text (used for passive voice, dash separators in non-aggressive
//     mode, and patterns that need a human rewrite).
//   - MinFormat gates a rule to a --format level: "full" rules run always,
//     "aggressive" rules only when --format aggressive is selected. "minimal"
//     restricts to the highest-confidence Tier-1 cliches.
package main

import "regexp"

// Category names map onto the 17 task-spec buckets (plus a few sub-tiers).
const (
	catCliche         = "ai-cliche"           // 1
	catNewsTell       = "news-ai-tell"        // 2
	catCopula         = "copula-avoidance"    // 3
	catPassive        = "passive-voice"       // 4
	catMonotony       = "structural-monotony" // 5 (structural, not rule-driven)
	catMeta           = "meta-commentary"     // 6
	catDangling       = "dangling-ing"        // 7
	catPuffery        = "puffery"             // 8
	catGenericCloser  = "generic-closer"      // 9
	catCurlyQuote     = "curly-quotes"        // 10
	catDashSeparator  = "dash-separator"      // 11
	catNovelty        = "novelty-inflation"   // 12
	catSynonymCycling = "synonym-cycling"     // 13 (structural)
	catFalseConcede   = "false-concession"    // 14
	catEmotionalFlat  = "emotional-flatline"  // 15
	catRuleOfThree    = "rule-of-three"       // 16 (structural)
	catInflatedSymbol = "inflated-symbolism"  // 17
	// Supporting tiers that fold into scoring but keep distinct labels.
	catFluff       = "fluff-phrase"
	catRedundant   = "redundant-modifier"
	catFalseAgency = "false-agency"
	catReasoning   = "reasoning-artifact"
	catUnearned    = "unearned-confidence"
	catPlatitude   = "platitude"
)

// flagOnly marks a rule as detect-only. NUL-prefixed so it can never collide
// with a real replacement string.
const flagOnly = "\x00FLAG"

// isFlagOnly reports whether a replacement is the detect-only sentinel.
func isFlagOnly(repl string) bool { return repl == flagOnly }

// Rule is one detect-and-replace directive.
type Rule struct {
	Category    string
	Pattern     string         // source pattern (for JSON rule export)
	re          *regexp.Regexp // compiled
	Replacement string         // "" means remove; may use $1 group refs
	Weight      int            // severity weight (drives the overall score)
	MinFormat   string         // "full" (default), "aggressive", or "minimal"
	Note        string         // short human note shown when removing/flagging
}

// jsonRule is the on-disk shape for --rules FILE overrides.
type jsonRule struct {
	Category    string `json:"category"`
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement"`
	Weight      int    `json:"weight"`
	Format      string `json:"format"`
	Note        string `json:"note"`
}

type jsonRuleFile struct {
	Rules []jsonRule `json:"rules"`
}

// compileAll compiles every regex once at startup.
func compileAll(specs []Rule) []Rule {
	out := make([]Rule, 0, len(specs))
	for _, r := range specs {
		r.re = regexp.MustCompile(r.Pattern)
		if r.MinFormat == "" {
			r.MinFormat = "full"
		}
		out = append(out, r)
	}
	return out
}

// rulesFromJSON turns a parsed override file into compiled rules.
func rulesFromJSON(jf jsonRuleFile) ([]Rule, error) {
	specs := make([]Rule, 0, len(jf.Rules))
	for _, jr := range jf.Rules {
		re, err := regexp.Compile(jr.Pattern)
		if err != nil {
			return nil, err
		}
		fmtLevel := jr.Format
		if fmtLevel == "" {
			fmtLevel = "full"
		}
		w := jr.Weight
		if w == 0 {
			w = 1
		}
		specs = append(specs, Rule{
			Category:    jr.Category,
			Pattern:     jr.Pattern,
			re:          re,
			Replacement: jr.Replacement,
			Weight:      w,
			MinFormat:   fmtLevel,
			Note:        jr.Note,
		})
	}
	return specs, nil
}

// activeForFormat reports whether a rule fires under the chosen --format.
//   - minimal: only run rules explicitly tagged "minimal".
//   - full:    run "minimal" + "full" rules.
//   - aggressive: run everything.
func activeForFormat(rule Rule, chosen string) bool {
	switch chosen {
	case "minimal":
		return rule.MinFormat == "minimal"
	case "aggressive":
		return true
	default: // full
		return rule.MinFormat == "minimal" || rule.MinFormat == "full"
	}
}

// ci wraps a pattern body in a case-insensitive flag.
func ci(body string) string { return "(?i)" + body }

// builtinRules returns the compiled embedded ruleset.
func builtinRules() []Rule { return compileAll(builtinSpecs()) }

// builtinSpecs is the authored (uncompiled) ruleset.
func builtinSpecs() []Rule {
	var s []Rule

	// ---- Category 1: AI cliches (Tier 1, weight 3) -------------------------
	cliche := func(pat, repl, minFmt string) Rule {
		return Rule{Category: catCliche, Pattern: ci(pat), Replacement: repl, Weight: 3, MinFormat: minFmt}
	}
	s = append(s,
		cliche(`\butilize\b`, "use", "minimal"),
		cliche(`\butilizes\b`, "uses", "minimal"),
		cliche(`\butilized\b`, "used", "minimal"),
		cliche(`\butilizing\b`, "using", "minimal"),
		cliche(`\bleverage\b`, "use", "minimal"),
		cliche(`\bleverages\b`, "uses", "minimal"),
		cliche(`\bleveraged\b`, "used", "minimal"),
		cliche(`\bleveraging\b`, "using", "minimal"),
		cliche(`\bdelve into\b`, "examine", "minimal"),
		cliche(`\bdelves into\b`, "examines", "minimal"),
		cliche(`\bdelved into\b`, "examined", "minimal"),
		cliche(`\bdelve\b`, "dig", "minimal"),
		cliche(`\bfacilitate\b`, "help", "full"),
		cliche(`\bfacilitates\b`, "helps", "full"),
		cliche(`\bfacilitated\b`, "helped", "full"),
		cliche(`\bsynergy\b`, "combined effect", "full"),
		cliche(`\bsynergies\b`, "combined effects", "full"),
		cliche(`\bholistic\b`, "complete", "full"),
		cliche(`\bholistically\b`, "completely", "full"),
		cliche(`\bparadigm shift\b`, "shift", "full"),
		cliche(`\bactionable insights\b`, "specific recommendations", "full"),
		cliche(`\bactionable\b`, "specific", "aggressive"),
		cliche(`\bimpactful\b`, "effective", "full"),
		cliche(`\bcutting[- ]edge\b`, "modern", "full"),
		cliche(`\bstate[- ]of[- ]the[- ]art\b`, "advanced", "full"),
		cliche(`\bgame[- ]?changer\b`, "breakthrough", "full"),
		cliche(`\bdeep dive\b`, "detailed look", "full"),
		cliche(`\blow-hanging fruit\b`, "easy wins", "full"),
		cliche(`\bmove the needle\b`, "make a difference", "full"),
		cliche(`\bcircle back\b`, "return", "full"),
		cliche(`\btouch base\b`, "check in", "full"),
		cliche(`\blearnings\b`, "lessons", "full"),
		cliche(`\brich tapestry\b`, "mix", "full"),
		cliche(`\btapestry\b`, "mix", "full"),
		cliche(`\bin today's fast-paced world\b`, "", "minimal"),
		cliche(`\bin today's [a-z]+ world\b`, "", "full"),
		cliche(`\bembark on a journey of\b`, "", "full"),
		cliche(`\bembark on a journey\b`, "", "full"),
		cliche(`\bat the end of the day\b`, "", "full"),
		cliche(`\brobust and comprehensive\b`, "comprehensive", "full"),
		cliche(`\bcomprehensive and robust\b`, "comprehensive", "full"),
		// "robust"/"landscape" alone are context-dependent: aggressive only.
		cliche(`\brobust\b`, "solid", "aggressive"),
		cliche(`\blandscape\b`, "field", "aggressive"),
		cliche(`\bseamless integration\b`, "smooth integration", "full"),
		cliche(`\bseamlessly integrate\b`, "integrate cleanly", "full"),
	)

	// ---- Category 2: News AI tells (Tier 1-News, weight 3) -----------------
	news := func(pat, repl, minFmt, note string) Rule {
		return Rule{Category: catNewsTell, Pattern: ci(pat), Replacement: repl, Weight: 3, MinFormat: minFmt, Note: note}
	}
	s = append(s,
		news(`\bworth sitting with(?: for a (?:second|moment))?\b`, "", "full", "if it's worth sitting with, the reader will"),
		news(`\bdeserves a closer look\b`, "", "full", "state what the closer look reveals, or cut"),
		news(`\b(?:consequences|implications) (?:extend|go|reach) (?:beyond|past|further than)\b`, flagOnly, "full", "state the specific downstream effect"),
		news(`\b(?:beyond|transcends?) (?:the night|this moment|the keynote|the event) itself\b`, "", "full", "name the concrete effect"),
		news(`\bthat's the kind of\b`, "", "aggressive", "let the facts frame significance"),
		news(`\byou'd be hard-pressed\b`, "", "aggressive", "fake superlative"),
		news(`\bit's hard to overstate\b`, "", "full", "state the specific impact"),
		news(`\bshould be a (?:must-see|highlight|instant classic|showdown|blockbuster)\b`, flagOnly, "aggressive", "editorial prediction, not news"),
		news(`\b(?:blockbuster|stacked|loaded) (?:card|lineup|show|event)\b`, flagOnly, "aggressive", "state what's actually in it"),
	)

	// ---- Category 3: Copula avoidance (Tier 1b, weight 3) ------------------
	cop := func(pat, repl string) Rule {
		return Rule{Category: catCopula, Pattern: ci(pat), Replacement: repl, Weight: 3, MinFormat: "full"}
	}
	s = append(s,
		cop(`\bserves as (a|an|the)\b`, "is $1"),
		cop(`\bstands as (a|an|the)\b`, "is $1"),
		cop(`\bfunctions as (a|an|the)\b`, "is $1"),
		cop(`\bacts as (a|an|the)\b`, "is $1"),
		cop(`\bboasts (a|an|the)\b`, "has $1"),
		cop(`\bfeatures (a|an|the)\b`, "has $1"),
		cop(`\boffers (a|an|the)\b`, "has $1"),
	)

	// ---- Category 4: Passive voice (Tier 4, weight 1) — detect-only --------
	pass := func(pat string) Rule {
		return Rule{Category: catPassive, Pattern: ci(pat), Replacement: flagOnly, Weight: 1, MinFormat: "full", Note: "prefer active voice; name the actor"}
	}
	s = append(s,
		pass(`\bwas \w+ed by\b`),
		pass(`\bwere \w+ed by\b`),
		pass(`\bhas been \w+ed\b`),
		pass(`\bhave been \w+ed\b`),
		pass(`\bwill be \w+ed\b`),
		pass(`\bit was found that\b`),
		pass(`\bit has been shown\b`),
		pass(`\bit should be noted\b`),
	)

	// ---- Category 6: Meta-commentary (Tier 2, weight 2) --------------------
	meta := func(pat, repl, minFmt string) Rule {
		return Rule{Category: catMeta, Pattern: ci(pat), Replacement: repl, Weight: 2, MinFormat: minFmt}
	}
	s = append(s,
		meta(`\bin this (?:article|post|guide|tutorial),?\s*(?:we(?:'ll| will))?\b`, "", "full"),
		meta(`\bas (?:we've|we have) (?:discussed|seen|mentioned)\b`, "", "full"),
		meta(`\blet me explain\b`, "", "full"),
		meta(`\blet's (?:explore|examine|look at|dive into)\b`, "", "aggressive"),
		meta(`\bwithout further ado\b`, "", "full"),
		meta(`\bfirst and foremost\b`, "first", "full"),
		meta(`\blast but not least\b`, "finally", "full"),
		meta(`\bin conclusion\b`, "", "full"),
		meta(`\bto sum(?:marize)? up\b`, "", "full"),
		meta(`\ball in all\b`, "", "full"),
		meta(`\bhaving said that\b`, "however", "full"),
		meta(`\bthat being said\b`, "however", "full"),
		meta(`\bit goes without saying\b`, "", "full"),
		meta(`\bneedless to say\b`, "", "full"),
		meta(`\bit's (?:important|worth) (?:to note|noting) that\b`, "", "full"),
		meta(`\bit is (?:important|worth) (?:to note|noting) that\b`, "", "full"),
	)
	// Reasoning-chain artifacts (Tier 2g): distinct label, meta weight.
	s = append(s,
		Rule{Category: catReasoning, Pattern: ci(`\blet me (?:think|break this down|walk through)\b`), Replacement: "", Weight: 2, MinFormat: "full"},
		Rule{Category: catReasoning, Pattern: ci(`\bhere's my (?:thought process|reasoning|thinking)\b`), Replacement: "", Weight: 2, MinFormat: "full"},
		Rule{Category: catReasoning, Pattern: ci(`\bbreaking this down\b`), Replacement: "", Weight: 2, MinFormat: "full"},
	)

	// ---- Category 7: Dangling -ing clauses (Tier 2b, weight 2) -------------
	dang := func(word string) Rule {
		return Rule{Category: catDangling, Pattern: ci(`,\s*(?:` + word + `)\b[^.;!?\n]*`), Replacement: "", Weight: 2, MinFormat: "full"}
	}
	s = append(s,
		dang("highlighting"),
		dang("underscoring"),
		dang("emphasizing"),
		dang("showcasing"),
		dang("symbolizing"),
		dang("reflecting"),
		dang("fostering"),
		dang("cultivating"),
		dang("encompassing"),
	)

	// ---- Category 8: Puffery / significance (Tier 2c, weight 2) ------------
	puff := func(pat, repl string) Rule {
		return Rule{Category: catPuffery, Pattern: ci(pat), Replacement: repl, Weight: 2, MinFormat: "full"}
	}
	s = append(s,
		puff(`\b(?:is|stands as) a testament to\b`, ""),
		puff(`\ba testament to\b`, ""),
		puff(`\bindelible mark\b`, "lasting effect"),
		puff(`\benduring legacy\b`, ""),
		puff(`\blasting (?:impact|impression|legacy)\b`, ""),
		puff(`\bprofound (?:impact|influence|effect)\b`, "effect"),
		puff(`\b(?:pivotal|crucial|vital) role in the (?:evolving|ever-changing|shifting) landscape\b`, "major role"),
	)

	// ---- Category 9: Generic closers (Tier 2d, weight 2) -------------------
	closer := func(pat, repl string) Rule {
		return Rule{Category: catGenericCloser, Pattern: ci(pat), Replacement: repl, Weight: 2, MinFormat: "full"}
	}
	s = append(s,
		closer(`\bthe future (?:looks|is) bright\b`, ""),
		closer(`\bexciting times (?:lie|are) ahead\b`, ""),
		closer(`\bonly time will tell\b`, ""),
		closer(`\b(?:continues|continue) to (?:thrive|evolve|grow|inspire|shape)\b`, ""),
		closer(`\b(?:poised|positioned|well-positioned) (?:to|for)\b`, ""),
	)

	// ---- Category 10: Curly quotes (Tier 3b, weight 1) — literal runes ------
	s = append(s,
		Rule{Category: catCurlyQuote, Pattern: "[“”]", Replacement: `"`, Weight: 1, MinFormat: "minimal"},
		Rule{Category: catCurlyQuote, Pattern: "[‘’]", Replacement: "'", Weight: 1, MinFormat: "minimal"},
	)

	// ---- Category 11: Dash-as-separator (style, weight 2) ------------------
	// Aggressive rewrites to a period; full mode flags only (never mangles prose).
	// CLI flags like --verbose are excluded by requiring spaces / word-emdash-word.
	s = append(s,
		Rule{Category: catDashSeparator, Pattern: `(\S) -- (\S)`, Replacement: "$1. $2", Weight: 2, MinFormat: "aggressive", Note: "split clauses with a period, colon, or parentheses"},
		Rule{Category: catDashSeparator, Pattern: `(\S)\s*—\s*(\S)`, Replacement: "$1. $2", Weight: 2, MinFormat: "aggressive", Note: "split clauses with a period, colon, or parentheses"},
		Rule{Category: catDashSeparator, Pattern: `\S -- \S`, Replacement: flagOnly, Weight: 2, MinFormat: "full", Note: "split clauses with a period, colon, or parentheses"},
		Rule{Category: catDashSeparator, Pattern: `\S\s*—\s*\S`, Replacement: flagOnly, Weight: 2, MinFormat: "full", Note: "split clauses with a period, colon, or parentheses"},
	)

	// ---- Category 12: Novelty inflation (Tier 1g, weight 3) — flag-only -----
	nov := func(pat, note string) Rule {
		return Rule{Category: catNovelty, Pattern: ci(pat), Replacement: flagOnly, Weight: 3, MinFormat: "full", Note: note}
	}
	s = append(s,
		nov(`\bthe (?:failure mode|insight|pattern|problem) nobody'?s (?:naming|talking about|discussing)\b`, "state the actual pattern"),
		nov(`\bwhat nobody tells you about\b`, "just write the content"),
		nov(`\bthe insight everyone'?s missing\b`, "state the insight directly"),
		nov(`\bthe thing (?:most people|nobody|few people) (?:get|miss|overlook)\b`, "state what they miss"),
	)

	// ---- Category 14: False concession (Tier 2e, weight 2) — flag-only ------
	s = append(s,
		Rule{Category: catFalseConcede, Pattern: ci(`\bwhile .+? (?:is|are|was|were) (?:impressive|notable|significant|remarkable|commendable),?\s+.+? (?:remains?|continues?|persists?|presents?) (?:a )?(?:challenge|concern|issue|question)\b`), Replacement: flagOnly, Weight: 2, MinFormat: "full", Note: "make both sides specific and falsifiable"},
		Rule{Category: catFalseConcede, Pattern: ci(`\bdespite .+? (?:achievements?|progress|advances?|successes?),?\s+.+? (?:still|yet|nevertheless)\b`), Replacement: flagOnly, Weight: 2, MinFormat: "full", Note: "name the concrete trade-off"},
	)

	// ---- Category 15: Emotional flatline (Tier 2f, weight 2) — flag-only ----
	emo := func(pat, note string) Rule {
		return Rule{Category: catEmotionalFlat, Pattern: ci(pat), Replacement: flagOnly, Weight: 2, MinFormat: "full", Note: note}
	}
	s = append(s,
		emo(`\bwhat (?:surprised|struck|impressed|fascinated) me (?:most|was)\b`, "state the surprising fact"),
		emo(`\bi was (?:fascinated|surprised|impressed|struck) (?:to discover|to learn|by|that)\b`, "earn the emotion with a fact"),
		emo(`\bwhat (?:really )?stands out (?:is|here)\b`, "present the standout fact"),
		emo(`\bthe most (?:surprising|fascinating|striking|impressive) (?:thing|part|aspect)\b`, "present the thing, don't label it"),
	)

	// ---- Category 17: Inflated symbolism (puffery + dead metaphors) ---------
	sym := func(pat, repl string) Rule {
		return Rule{Category: catInflatedSymbol, Pattern: ci(pat), Replacement: repl, Weight: 2, MinFormat: "full"}
	}
	s = append(s,
		sym(`\borchestrat(?:e|es|ed|ing) a (?:ballet|dance|symphony)\b`, ""),
		sym(`\bsymphony of (services|systems|components)\b`, "mix of $1"),
		sym(`\btapestry of (services|systems|code)\b`, "mix of $1"),
		sym(`\bunder the hood\b`, "internally"),
		sym(`\bthe secret sauce\b`, "the key part"),
		sym(`\bbehind the scenes\b`, "internally"),
		sym(`\bheavy lifting\b`, "the hard work"),
		sym(`\bsilver bullet\b`, "perfect fix"),
		sym(`\bmagic happens\b`, ""),
	)

	// ---- False agency (Tier 1c, weight 3) — mostly flag-only ---------------
	s = append(s,
		Rule{Category: catFalseAgency, Pattern: ci(`\b(?:the decision|the culture|the conversation|the narrative|the dynamic|the tone) (?:emerges?|shifts?|moves?|becomes?|changes?|evolves?)\b`), Replacement: flagOnly, Weight: 3, MinFormat: "full", Note: "name the person or team that did it"},
		Rule{Category: catFalseAgency, Pattern: ci(`\bthe data tells us\b`), Replacement: "the data shows", Weight: 3, MinFormat: "full"},
	)

	// ---- Unearned confidence (Tier CD-2, weight 2) — flag-only ------------
	unc := func(pat string) Rule {
		return Rule{Category: catUnearned, Pattern: ci(pat), Replacement: flagOnly, Weight: 2, MinFormat: "full", Note: "replace with a specific, falsifiable claim"}
	}
	s = append(s,
		unc(`\bhandles all edge cases(?: gracefully)?\b`),
		unc(`\bworks seamlessly\b`),
		unc(`\bfully (?:reliable|tested)\b`),
		unc(`\bcompletely secure\b`),
		unc(`\bbulletproof\b`),
		unc(`\bjust works\b`),
		unc(`\bnever fails\b`),
	)

	// ---- Platitude injection (Tier CD-3, weight 2) — flag-only -----------
	plat := func(pat string) Rule {
		return Rule{Category: catPlatitude, Pattern: ci(pat), Replacement: flagOnly, Weight: 2, MinFormat: "full", Note: "delete the truism; start with the concrete claim"}
	}
	s = append(s,
		plat(`\bat its core,?\s+\w+`),
		plat(`\bthe beauty of .+? is that\b`),
		plat(`\bthe power of .+? lies in\b`),
		plat(`\bin the world of software\b`),
		plat(`\bin the realm of\b`),
	)

	// ---- Tier 3 fluff phrases (weight 1) ----------------------------------
	fluff := func(pat, repl string) Rule {
		return Rule{Category: catFluff, Pattern: ci(pat), Replacement: repl, Weight: 1, MinFormat: "full"}
	}
	s = append(s,
		fluff(`\ba wide variety of\b`, "many"),
		fluff(`\ba variety of\b`, "various"),
		fluff(`\ba large number of\b`, "many"),
		fluff(`\bdue to the fact that\b`, "because"),
		fluff(`\bin order to\b`, "to"),
		fluff(`\bfor the purpose of\b`, "to"),
		fluff(`\bin the event that\b`, "if"),
		fluff(`\bat this point in time\b`, "now"),
		fluff(`\bin the near future\b`, "soon"),
		fluff(`\bon a (?:daily|regular|weekly) basis\b`, "regularly"),
		fluff(`\b(?:is|are) able to\b`, "can"),
		fluff(`\bhas the (?:ability|capacity) to\b`, "can"),
		fluff(`\bin spite of the fact that\b`, "although"),
		fluff(`\bwith regard to\b`, "about"),
		fluff(`\bin light of\b`, "given"),
		fluff(`\bin terms of\b`, "for"),
		fluff(`\bby means of\b`, "by"),
		fluff(`\bfor all intents and purposes\b`, "essentially"),
		fluff(`\bthe fact of the matter is\b`, ""),
		fluff(`\bwhen all is said and done\b`, "ultimately"),
		fluff(`\beach and every\b`, "every"),
	)

	// ---- Tier 5 redundant modifiers (weight 1) ----------------------------
	redun := func(pat, repl, minFmt string) Rule {
		return Rule{Category: catRedundant, Pattern: ci(pat), Replacement: repl, Weight: 1, MinFormat: minFmt}
	}
	s = append(s,
		redun(`\bvery unique\b`, "unique", "full"),
		redun(`\bcompletely finished\b`, "finished", "full"),
		redun(`\babsolutely essential\b`, "essential", "full"),
		redun(`\bcompletely unanimous\b`, "unanimous", "full"),
		redun(`\bextremely critical\b`, "critical", "full"),
		redun(`\bhighly innovative\b`, "innovative", "full"),
		redun(`\btruly exceptional\b`, "exceptional", "full"),
		redun(`\bquite frankly\b`, "", "aggressive"),
		redun(`\bobviously,?\s`, "", "aggressive"),
		redun(`\bclearly,?\s`, "", "aggressive"),
	)

	return s
}

// categoryList returns the stable, sorted set of categories the engine knows
// about so JSON "counts" always includes every bucket (zeroed when clean).
func categoryList() []string {
	seen := map[string]bool{}
	var cats []string
	for _, r := range builtinSpecs() {
		if !seen[r.Category] {
			seen[r.Category] = true
			cats = append(cats, r.Category)
		}
	}
	for _, extra := range []string{catMonotony, catSynonymCycling, catRuleOfThree} {
		if !seen[extra] {
			seen[extra] = true
			cats = append(cats, extra)
		}
	}
	sortStrings(cats)
	return cats
}

// sortStrings is a tiny insertion sort to keep category ordering deterministic
// without importing sort for a handful of strings.
func sortStrings(a []string) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}
