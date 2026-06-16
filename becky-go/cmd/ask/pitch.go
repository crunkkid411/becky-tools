// pitch.go — Phase 3 of becky-ask: when the user describes a NEW capability
// ("I wish becky could…"), deterministically extract a becky-new-tool intake
// record, show it in the chat for approval, and on "y" call becky-new-tool.
//
// This is a thin bridge: pitch.go PRODUCES the intake JSON; all the staged build
// logic, cost control, and human gates live in becky-new-tool, not here.
// (SPEC-BECKY-ASK §3.4 + §4 boundary rule.)
//
// The extraction is deterministic — no model call — because the intake's S1 stage
// in becky-new-tool already does its own (optionally model-refined) normalization.
// We produce a "good enough" first-pass that S1 can improve; Jordan sees the plain-
// English summary and approves or cancels before the factory pipeline starts.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

// PitchRecord mirrors the Intake type in cmd/new-tool/state.go — the JSON shape
// `becky-new-tool --intake-file` expects. Kept in sync by convention (both are
// simple value structs with json tags); if the factory's schema gains a required
// field, add it here too.
type PitchRecord struct {
	Slug             string   `json:"slug"`
	Capability       string   `json:"capability"`
	InputKind        string   `json:"input_kind"`
	OutputKind       string   `json:"output_kind"`
	Constraints      []string `json:"constraints"`
	DefinitionOfDone []string `json:"definition_of_done"`
	CapturedAt       string   `json:"captured_at"`
	NormalizedBy     string   `json:"normalized_by"`
}

// ideaStripRe removes the common idea-framing prefixes from the user's question
// so slug + capability extraction starts from the actual capability text, not
// "I wish becky could" or "build a tool that".
var ideaStripRe = regexp.MustCompile(
	`(?i)^(i wish becky could|it would be nice if becky could|` +
		`becky should be able to|can becky learn to|` +
		`build a tool that|build a tool to|a new tool that|` +
		`a new tool to|new tool that|a tool that|a tool to|` +
		`feature that would|a feature that|` +
		`becky should|becky could|becky needs to)[ ,]+`,
)

var pitchSlugStop = map[string]bool{
	"a": true, "an": true, "the": true, "i": true, "need": true,
	"want": true, "tool": true, "that": true, "to": true, "for": true,
	"of": true, "and": true, "in": true, "on": true, "be": true,
	"do": true, "with": true, "from": true, "into": true, "it": true,
}

var pitchSlugClean = regexp.MustCompile(`[^a-z0-9]+`)

// extractPitchDeterministic builds a PitchRecord from the user's plain-English
// idea using only keyword heuristics — no model. becky-new-tool's own S1
// normalization stage will refine this further after Jordan approves.
func extractPitchDeterministic(question string) PitchRecord {
	// Strip idea framing to expose the raw capability.
	core := ideaStripRe.ReplaceAllString(strings.TrimSpace(question), "")
	if strings.TrimSpace(core) == "" {
		core = strings.TrimSpace(question)
	}

	slug := pitchDeriveSlug(core)
	bare := strings.TrimPrefix(slug, "becky-")

	return PitchRecord{
		Slug:        slug,
		Capability:  pitchCapability(core),
		InputKind:   pitchGuessInputKind(question),
		OutputKind:  pitchGuessOutputKind(question),
		Constraints: []string{"offline"},
		DefinitionOfDone: []string{
			fmt.Sprintf("go build ./cmd/%s passes", bare),
			fmt.Sprintf("go vet ./cmd/%s passes", bare),
			fmt.Sprintf("go test ./cmd/%s/... passes", bare),
			"runs on a test input and exits 0",
			"output is valid JSON on stdout",
		},
		CapturedAt:   time.Now().Format("2006-01-02"),
		NormalizedBy: "deterministic",
	}
}

// pitchDeriveSlug mirrors cmd/new-tool/util.go's deriveSlug: first 3 meaningful
// words from the core text, slugified to becky-<kebab>.
func pitchDeriveSlug(core string) string {
	words := strings.Fields(strings.ToLower(core))
	var kept []string
	for _, w := range words {
		w = pitchSlugClean.ReplaceAllString(w, "")
		if w == "" || pitchSlugStop[w] {
			continue
		}
		kept = append(kept, w)
		if len(kept) >= 3 {
			break
		}
	}
	if len(kept) == 0 {
		return "becky-new-capability"
	}
	return "becky-" + strings.Join(kept, "-")
}

// pitchCapability returns a cleaned, sentence-cased capability description with a
// trailing period.
func pitchCapability(core string) string {
	s := strings.TrimSpace(core)
	if s == "" {
		return "Capability not yet described."
	}
	// Sentence-case the first byte (ASCII only; non-ASCII passes through unchanged).
	if len(s) > 0 && s[0] >= 'a' && s[0] <= 'z' {
		s = string(s[0]-32) + s[1:]
	}
	// Ensure a trailing sentence terminator.
	if !strings.HasSuffix(s, ".") && !strings.HasSuffix(s, "!") && !strings.HasSuffix(s, "?") {
		s += "."
	}
	return s
}

// pitchGuessInputKind infers the primary input modality from the question text.
// Mirrors cmd/new-tool/util.go's guessInputKind so the two tools agree.
func pitchGuessInputKind(q string) string {
	p := strings.ToLower(q)
	switch {
	case strings.Contains(p, "video") || strings.Contains(p, "clip") ||
		strings.Contains(p, "mp4") || strings.Contains(p, "frame"):
		return "video"
	case strings.Contains(p, "audio") || strings.Contains(p, "voice") ||
		strings.Contains(p, "speech") || strings.Contains(p, "wav") ||
		strings.Contains(p, "mp3"):
		return "audio"
	case strings.Contains(p, "image") || strings.Contains(p, "photo") ||
		strings.Contains(p, "picture") || strings.Contains(p, "jpg") ||
		strings.Contains(p, "png"):
		return "image"
	case strings.Contains(p, "url") || strings.Contains(p, "http") ||
		strings.Contains(p, "web"):
		return "url"
	case strings.Contains(p, "json") || strings.Contains(p, "transcript") ||
		strings.Contains(p, "text file"):
		return "json"
	default:
		return "text"
	}
}

// pitchGuessOutputKind guesses the output shape from question keywords.
func pitchGuessOutputKind(q string) string {
	p := strings.ToLower(q)
	switch {
	case strings.Contains(p, "spreadsheet") || strings.Contains(p, "csv") ||
		strings.Contains(p, "excel"):
		return "csv"
	case strings.Contains(p, "report") || strings.Contains(p, "markdown"):
		return "text"
	default:
		return "json"
	}
}

// savePitchFile writes pitch as indented JSON to an OS temp file and returns the
// path. The file lives for the process lifetime; a crash before "y" just leaves a
// harmless temp file behind.
func savePitchFile(pitch PitchRecord) (string, error) {
	f, err := os.CreateTemp("", "becky-pitch-*.json")
	if err != nil {
		return "", fmt.Errorf("create pitch file: %w", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(pitch); err != nil {
		return "", fmt.Errorf("write pitch: %w", err)
	}
	return f.Name(), nil
}

// pitchReply renders the pitch in plain English for the chat window: what will be
// built, what it takes, what it gives, and a y/n confirm prompt. Per the spec, the
// human MUST explicitly approve before the factory pipeline starts.
func pitchReply(pitch PitchRecord) string {
	var b strings.Builder
	b.WriteString(beckyStyle.Render("Here's the new tool I'll pitch to the factory:"))
	b.WriteString("\n\n")
	b.WriteString(beckyStyle.Render("  Name:  ") + systemStyle.Render(pitch.Slug))
	b.WriteString("\n")
	b.WriteString(beckyStyle.Render("  Does:  ") + systemStyle.Render(pitch.Capability))
	b.WriteString("\n")
	b.WriteString(beckyStyle.Render("  Takes: ") + systemStyle.Render(pitch.InputKind))
	b.WriteString("\n")
	b.WriteString(beckyStyle.Render("  Gives: ") + systemStyle.Render(pitch.OutputKind))
	b.WriteString("\n\n")
	b.WriteString(systemStyle.Render("Building it runs becky-new-tool — a staged, metered pipeline with its own"))
	b.WriteString("\n")
	b.WriteString(systemStyle.Render("approval gates. The tool name / capability can be refined before it starts."))
	b.WriteString("\n\n")
	b.WriteString(beckyStyle.Render("Build it now? ") + systemStyle.Render("(y = run becky-new-tool · n = cancel and save pitch)"))
	return b.String()
}

// pitchCommand returns the becky-new-tool argv for the given intake file.
// runCommand (run.go) resolves the binary path and handles .exe on Windows.
func pitchCommand(pitchFile string) []string {
	return []string{"becky-new-tool", "--intake-file", pitchFile}
}

// buildNewToolRouted is the entry point called by router.go when the gate
// decides decideNewTool. It:
//  1. Checks the offline catalog — if an existing tool covers the request, prefer
//     that over pitching a new one (saves Jordan from a needless factory run).
//  2. Extracts the pitch deterministically.
//  3. Saves the pitch JSON to a temp file.
//  4. Returns a routed with the styled pitch reply + the factory command staged for
//     y/n confirmation. On failure (temp-write error), returns a graceful text-only
//     fallback (degrade, never crash).
func buildNewToolRouted(question string) routed {
	// If an existing capability covers it, answer that instead — cheaper and correct.
	if hits := matchCapabilities(question); len(hits) > 0 {
		return routed{Reply: capabilityReply(question, hits)}
	}

	pitch := extractPitchDeterministic(question)
	f, err := savePitchFile(pitch)
	if err != nil {
		return routed{Reply: pitchFallbackReply(question, err)}
	}
	return routed{
		Reply:   pitchReply(pitch),
		Pending: pitchCommand(f),
	}
}

// pitchFallbackReply is shown when the pitch file cannot be saved. It
// acknowledges the idea and gives Jordan the manual fallback command.
func pitchFallbackReply(question string, saveErr error) string {
	short := question
	if len(short) > 80 {
		short = short[:77] + "..."
	}
	var b strings.Builder
	b.WriteString(beckyStyle.Render("That sounds like a new capability — I've noted the idea."))
	b.WriteString("\n\n")
	b.WriteString(systemStyle.Render("To build it manually, run:"))
	b.WriteString("\n")
	b.WriteString(systemStyle.Render(fmt.Sprintf(`  becky-new-tool --pain %q`, short)))
	b.WriteString("\n")
	b.WriteString(systemStyle.Render(fmt.Sprintf("(Could not save pitch file: %v)", saveErr)))
	return b.String()
}
