// extract.go — the Gemma-4 content recovery. This is the model step becky-regrab
// exists for: given a page's visible text (which the deterministic extractor
// failed to turn into a good clip), the local Gemma-4 model reproduces it as
// clean, COMPLETE Markdown. It is a faithful re-formatting task (the text is
// supplied, not generated), and the caller then clipcheck-scores the output, so a
// model that summarizes or drops content is caught rather than trusted.
package main

import (
	"context"
	"strings"
	"time"

	"becky-go/internal/config"
	"becky-go/internal/llmlocal"
)

const (
	recoverTimeout = 300 * time.Second // server spawn + a longer page-length generation
	recoverCtxLen  = 16384             // room for a whole page of input + a page of output
	maxInputChars  = 24000             // ~6k tokens of page text; bigger pages are truncated
	maxOutTokens   = 6144              // allow a full page of markdown back
)

// gemmaExtract asks the local Gemma-4 model to convert a page's visible text into
// complete Markdown. Returns the markdown (no surrounding code fence) or an error
// when no local model is available (caller degrades). Uses the E4B default — it
// fully offloads to the 8 GB GPU and is fast; the 12B doesn't fit and crawls on
// CPU (override with BECKY_AVLM_VARIANT=12b only on a bigger GPU).
func gemmaExtract(cfg config.Config, url, title, pageText string, logf func(string, ...any)) (string, error) {
	model, _, label := cfg.GemmaAVLM()
	c := llmlocal.NewClientCtx(model, cfg.LlamaServer, recoverCtxLen, logf)
	if err := c.Available(); err != nil {
		return "", err
	}
	logf("regrab: recovering content with %s", label)

	input := strings.TrimSpace(pageText)
	if len(input) > maxInputChars {
		input = input[:maxInputChars]
	}

	// Gemma has no system role and small Gemmas get derailed by meta-instructions
	// mixed into a huge input, so the whole task lives in one clearly-delimited user
	// turn that ends on a cue to start emitting Markdown.
	user := "Convert the web page text below into clean Markdown. Reproduce ALL of it faithfully — " +
		"every heading, paragraph, list item, and link text, in order. Do NOT summarize, omit, or add " +
		"your own commentary. Output only the Markdown.\n\n" +
		"URL: " + url + "\nTITLE: " + title + "\n\n" +
		"=== PAGE TEXT START ===\n" + input + "\n=== PAGE TEXT END ===\n\nMarkdown:\n"

	ctx, cancel := context.WithTimeout(context.Background(), recoverTimeout)
	defer cancel()

	ans, err := c.Chat(ctx, "", user, llmlocal.Options{MaxTokens: maxOutTokens})
	if err != nil {
		return "", err
	}
	return stripCodeFence(strings.TrimSpace(ans)), nil
}

// stripCodeFence removes a wrapping ```/```markdown fence if the model added one.
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	// Drop the opening fence line (``` or ```markdown).
	if nl := strings.IndexByte(s, '\n'); nl >= 0 {
		s = s[nl+1:]
	}
	s = strings.TrimRight(s, " \t\r\n")
	if strings.HasSuffix(s, "```") {
		s = strings.TrimSuffix(s, "```")
	}
	return strings.TrimSpace(s)
}
