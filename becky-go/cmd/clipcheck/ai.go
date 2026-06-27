// ai.go — the ONLY place becky-clipcheck consults a model. It is reached solely
// for the borderline "partial" verdict; the clear pass/fail cases are decided by
// the deterministic scorer with no model at all. The model is a LOCAL Gemma-4
// (the same QAT GGUF the rest of becky uses), driven in text mode for one short
// PASS/FAIL judgement. Any failure leaves the verdict as the deterministic
// "partial" — the model never makes things worse, only resolves the middle.
package main

import (
	"context"
	"strings"
	"time"

	"becky-go/internal/clipcheck"
	"becky-go/internal/config"
	"becky-go/internal/llmlocal"
)

const (
	adjudicateTimeout = 180 * time.Second // server spawn (~11s cold) + one short chat
	maxExcerptChars   = 4000              // keep the prompt within the model's context
)

// adjudicate asks the local Gemma-4 model whether mdBody faithfully captured
// pageText. Returns (verdict, reason, true) only when the model gives a clear
// PASS or FAIL; otherwise (.., false) so the caller keeps the deterministic
// "partial". Never panics.
func adjudicate(cfg config.Config, pageText, mdBody string, logf func(string, ...any)) (verdict, reason string, ok bool) {
	model, _, label := cfg.GemmaAVLM()
	c := llmlocal.NewClient(model, cfg.LlamaServer, logf)
	if err := c.Available(); err != nil {
		logf("clipcheck: local model unavailable (%v); leaving verdict as partial", err)
		return "", "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), adjudicateTimeout)
	defer cancel()

	system := "You verify whether a saved Markdown clip faithfully captured a web page's MAIN content. " +
		"Reply with exactly one line: PASS or FAIL, then ' - ' and a brief reason. " +
		"PASS if the markdown contains essentially all of the page's main article/body text. " +
		"FAIL if a significant portion of the body content is missing from the markdown."
	user := "PAGE MAIN TEXT (may be truncated):\n" + truncate(pageText, maxExcerptChars) +
		"\n\nMARKDOWN CLIP BODY (may be truncated):\n" + truncate(mdBody, maxExcerptChars) +
		"\n\nDid the markdown faithfully capture the page's main content? Answer PASS or FAIL."

	ans, err := c.Chat(ctx, system, user, llmlocal.Options{MaxTokens: 64})
	if err != nil {
		logf("clipcheck: model adjudication failed (%v); leaving verdict as partial", err)
		return "", "", false
	}
	line := firstLine(strings.TrimSpace(ans))
	switch up := strings.ToUpper(line); {
	case strings.HasPrefix(up, "PASS"):
		return clipcheck.VerdictPass, "local model (" + label + "): " + line, true
	case strings.HasPrefix(up, "FAIL"):
		return clipcheck.VerdictFail, "local model (" + label + "): " + line, true
	default:
		logf("clipcheck: model reply was not PASS/FAIL (%q); leaving partial", line)
		return "", "", false
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + " ...[truncated]"
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}
