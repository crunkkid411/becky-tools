// backend.go — the pluggable review-backend abstraction and its three
// implementations: mock (offline, deterministic), claude-code (local headless
// Claude CLI, sensitive data stays on the machine), and openrouter (HTTP API).
//
// Every backend takes the same inputs and returns annotations + the resolved
// model id + an optional graceful-degradation note. A backend NEVER crashes the
// tool: on failure it returns an empty/partial annotation set plus a note, and
// the caller still emits valid JSON and exits 0.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"becky-go/internal/beckyio"
)

// reviewInput is everything a backend needs to review one media file.
type reviewInput struct {
	File        string
	Transcript  transcript
	Events      eventsDoc
	CaseContext string
	Model       string
	Vision      bool
	Frames      []string // extracted key-frame paths (best-effort, --vision)
	Verbose     bool
}

// reviewResult is what a backend returns. Note is set when the backend degraded.
type reviewResult struct {
	Annotations []Annotation
	Model       string
	Note        string
}

// Backend is the small interface every review engine implements. New backends
// only need Name + Review.
type Backend interface {
	Name() string
	Review(ctx context.Context, in reviewInput) (reviewResult, error)
}

// newBackend resolves a backend by name. Unknown names are an error.
func newBackend(name string) (Backend, error) {
	switch strings.ToLower(name) {
	case "mock":
		return mockBackend{}, nil
	case "claude-code", "claude", "cc":
		return claudeCodeBackend{}, nil
	case "openrouter":
		return openRouterBackend{}, nil
	default:
		return nil, fmt.Errorf("unknown backend %q (use claude-code, openrouter, or mock)", name)
	}
}

// ---------------------------------------------------------------------------
// mock backend — deterministic, offline, the guaranteed test path.
// ---------------------------------------------------------------------------

type mockBackend struct{}

func (mockBackend) Name() string { return "mock" }

// referenceCues are case-insensitive phrases/words that hint at a hidden identity
// or place worth resolving. The mock emits a reference_resolution stub for each
// hit. Matching is word-boundary aware (see cueRegexp) so "he" does not match
// inside "the".
var referenceCues = []string{
	"my ex", "ex-wife", "ex-husband", "wife", "husband",
	"girlfriend", "boyfriend", "she", "he", "her", "him", "they", "them",
	"the house", "back home", "over there", "that guy", "the kids",
}

// cueRegexp matches any reference cue as a whole word/phrase, case-insensitively.
var cueRegexp = buildCueRegexp(referenceCues)

func buildCueRegexp(cues []string) *regexp.Regexp {
	parts := make([]string, 0, len(cues))
	for _, c := range cues {
		parts = append(parts, regexp.QuoteMeta(c))
	}
	return regexp.MustCompile(`(?i)\b(` + strings.Join(parts, "|") + `)\b`)
}

// Review derives plausible, deterministic annotations from the REAL transcript +
// events: one notable_moment per event (with its real timestamps) and one
// reference_resolution stub per transcript segment that contains a pronoun /
// relationship cue. No network, fully reproducible.
func (mockBackend) Review(_ context.Context, in reviewInput) (reviewResult, error) {
	var anns []Annotation

	// 1. notable_moment per detected event (uses the event's real timestamps).
	for _, e := range in.Events.Events {
		conf := e.Confidence
		if conf <= 0 {
			conf = 0.5
		}
		anns = append(anns, Annotation{
			Type:         "notable_moment",
			SegmentStart: e.Start,
			SegmentEnd:   e.End,
			Text:         eventText(e),
			Resolution:   fmt.Sprintf("Detected %s event", e.Type),
			Rationale: fmt.Sprintf(
				"becky-events flagged a %s at %.2f-%.2fs%s; surfaced for context review.",
				e.Type, e.Start, e.End, speakerSuffix(e.SpeakerID)),
			Confidence:   round3(conf),
			Significance: significanceForEvent(e),
			Reviewed:     false,
		})
	}

	// 2. reference_resolution stub per transcript segment with a reference cue.
	for _, s := range in.Transcript.Segments {
		cue, ok := firstCue(s.Text)
		if !ok {
			continue
		}
		anns = append(anns, Annotation{
			Type:         "reference_resolution",
			SegmentStart: s.Start,
			SegmentEnd:   s.End,
			Text:         strings.TrimSpace(s.Text),
			Resolution:   "Unresolved reference (mock: no entity lookup)",
			Rationale: fmt.Sprintf(
				"Segment contains the reference cue %q; a real backend would resolve it against the case context.",
				strings.TrimSpace(cue)),
			Confidence:   0.4,
			Significance: "low",
			Reviewed:     false,
		})
	}

	beckyio.Logf(in.Verbose, "mock backend produced %d annotation(s) from %d event(s) + %d segment(s)",
		len(anns), len(in.Events.Events), len(in.Transcript.Segments))

	return reviewResult{
		Annotations: normalize(anns),
		Model:       "mock-deterministic-v1",
	}, nil
}

// ---------------------------------------------------------------------------
// claude-code backend — local headless Claude CLI. Sensitive data stays local.
// ---------------------------------------------------------------------------

type claudeCodeBackend struct{}

func (claudeCodeBackend) Name() string { return "claude-code" }

// claudeEnvelope is the subset of `claude -p --output-format json` we read.
type claudeEnvelope struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	IsError bool   `json:"is_error"`
	Result  string `json:"result"`
}

// parseClaudeEnvelope decodes the CLI's JSON result envelope, tolerating any
// leading warning lines the CLI may print before the JSON object.
func parseClaudeEnvelope(s string) (claudeEnvelope, bool) {
	s = strings.TrimSpace(s)
	var env claudeEnvelope
	if json.Unmarshal([]byte(s), &env) == nil && env.Type != "" {
		return env, true
	}
	// Fall back to the outermost {...} span (handles a stray prefix line).
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		if json.Unmarshal([]byte(s[start:end+1]), &env) == nil && env.Type != "" {
			return env, true
		}
	}
	return claudeEnvelope{}, false
}

// Review shells out to the local claude CLI in headless mode. The model returns
// ONLY a JSON array of annotations (enforced via --append-system-prompt). All
// failure modes (CLI missing, timeout, non-zero exit, unparseable output)
// degrade to an empty annotation set + a note; they never return a hard error.
func (claudeCodeBackend) Review(ctx context.Context, in reviewInput) (reviewResult, error) {
	bin := resolveClaudeBin()
	if bin == "" {
		return reviewResult{
			Model: in.Model,
			Note:  "claude-code backend skipped: claude CLI not found on PATH",
		}, nil
	}

	user := buildUserPrompt(in.CaseContext, in.Transcript, in.Events, in.File)
	if in.Vision && len(in.Frames) > 0 {
		user += "\n\n# Key frames\n" + strings.Join(framePromptLines(in.Frames), "\n")
	}

	model := in.Model
	// Pass the (large, multi-line) prompt on STDIN rather than as a -p argument.
	// On Windows the CLI is a claude.cmd shim; routing a big multi-line arg
	// through cmd.exe mangles/truncates it. Stdin sidesteps that entirely.
	args := []string{"-p",
		"--output-format", "json",
		"--append-system-prompt", systemPrompt}
	if model != "" {
		args = append(args, "--model", model)
	}

	beckyio.Logf(in.Verbose, "claude-code: invoking %s (model=%s, prompt=%d bytes via stdin)...", bin, model, len(user))

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = strings.NewReader(user)
	// Run from a neutral temp dir so the CLI never auto-discovers a project's
	// CLAUDE.md (which otherwise makes the model behave conversationally instead
	// of returning our JSON). We cannot use --bare: it ignores OAuth/keychain
	// auth and would fail with "Not logged in".
	cmd.Dir = os.TempDir()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return reviewResult{Model: model, Note: "claude-code backend degraded: call timed out"}, nil
		}
		return reviewResult{
			Model: model,
			Note:  fmt.Sprintf("claude-code backend degraded: CLI error (%v): %s", err, tail(stderr.String())),
		}, nil
	}

	env, ok := parseClaudeEnvelope(stdout.String())
	if !ok {
		return reviewResult{
			Model: model,
			Note:  fmt.Sprintf("claude-code backend degraded: could not parse CLI envelope: %s", tail(stdout.String())),
		}, nil
	}
	if env.IsError || env.Subtype != "success" {
		return reviewResult{
			Model: model,
			Note:  fmt.Sprintf("claude-code backend degraded: CLI reported error (subtype=%s)", env.Subtype),
		}, nil
	}

	anns, ok := parseAnnotations(env.Result)
	if !ok {
		return reviewResult{
			Model: model,
			Note:  fmt.Sprintf("claude-code backend degraded: model output was not a JSON array: %s", tail(env.Result)),
		}, nil
	}
	beckyio.Logf(in.Verbose, "claude-code: parsed %d annotation(s)", len(anns))
	return reviewResult{Annotations: anns, Model: model}, nil
}

// resolveClaudeBin finds a runnable claude entrypoint. On Windows the CLI is
// claude.ps1/claude.cmd on PATH; LookPath("claude") resolves either via PATHEXT.
func resolveClaudeBin() string {
	for _, name := range []string{"claude", "claude.cmd", "claude.exe"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// openrouter backend — HTTP chat-completions. Best-effort; clean skip w/o key.
// ---------------------------------------------------------------------------

type openRouterBackend struct{}

func (openRouterBackend) Name() string { return "openrouter" }

const openRouterURL = "https://openrouter.ai/api/v1/chat/completions"

// orMessage / orRequest / orResponse are the minimal OpenAI-compatible shapes
// OpenRouter uses.
type orMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type orRequest struct {
	Model    string      `json:"model"`
	Messages []orMessage `json:"messages"`
}
type orResponse struct {
	Choices []struct {
		Message orMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Review POSTs the prompt to OpenRouter when OPENROUTER_API_KEY is set. With no
// key it returns a clean skip note (no crash). Any HTTP/parse failure degrades
// to an empty set + note.
func (openRouterBackend) Review(ctx context.Context, in reviewInput) (reviewResult, error) {
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" {
		return reviewResult{
			Model: in.Model,
			Note:  "openrouter backend skipped: OPENROUTER_API_KEY not set",
		}, nil
	}

	user := buildUserPrompt(in.CaseContext, in.Transcript, in.Events, in.File)
	body, _ := json.Marshal(orRequest{
		Model: in.Model,
		Messages: []orMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: user},
		},
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openRouterURL, bytes.NewReader(body))
	if err != nil {
		return reviewResult{Model: in.Model, Note: fmt.Sprintf("openrouter backend degraded: %v", err)}, nil
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")

	beckyio.Logf(in.Verbose, "openrouter: POST %s (model=%s)...", openRouterURL, in.Model)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return reviewResult{Model: in.Model, Note: "openrouter backend degraded: request timed out"}, nil
		}
		return reviewResult{Model: in.Model, Note: fmt.Sprintf("openrouter backend degraded: %v", err)}, nil
	}
	defer resp.Body.Close()

	var or orResponse
	if err := json.NewDecoder(resp.Body).Decode(&or); err != nil {
		return reviewResult{Model: in.Model, Note: fmt.Sprintf("openrouter backend degraded: decode failed (HTTP %d)", resp.StatusCode)}, nil
	}
	if or.Error != nil {
		return reviewResult{Model: in.Model, Note: fmt.Sprintf("openrouter backend degraded: API error: %s", or.Error.Message)}, nil
	}
	if len(or.Choices) == 0 {
		return reviewResult{Model: in.Model, Note: fmt.Sprintf("openrouter backend degraded: no choices (HTTP %d)", resp.StatusCode)}, nil
	}

	anns, ok := parseAnnotations(or.Choices[0].Message.Content)
	if !ok {
		return reviewResult{Model: in.Model, Note: "openrouter backend degraded: model output was not a JSON array"}, nil
	}
	return reviewResult{Annotations: anns, Model: in.Model}, nil
}

// ---------------------------------------------------------------------------
// shared helpers
// ---------------------------------------------------------------------------

// eventText returns the most descriptive text available for an event.
func eventText(e eventItem) string {
	if e.Description != "" {
		return e.Description
	}
	return e.Type
}

func speakerSuffix(speaker string) string {
	if speaker == "" {
		return ""
	}
	return " (" + speaker + ")"
}

// significanceForEvent maps event types to a default significance level.
func significanceForEvent(e eventItem) string {
	switch e.Type {
	case "second_speaker", "multi_face":
		return "high"
	case "phone_call", "location_change":
		return "medium"
	default:
		return "low"
	}
}

// firstCue returns the first reference cue found in text as a whole word/phrase
// (case-insensitive), or false if none is present.
func firstCue(text string) (string, bool) {
	if m := cueRegexp.FindString(text); m != "" {
		return m, true
	}
	return "", false
}

// framePromptLines describes extracted frames for the prompt body.
func framePromptLines(frames []string) []string {
	lines := make([]string, 0, len(frames))
	for _, f := range frames {
		lines = append(lines, "- "+f)
	}
	return lines
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 600 {
		return s[len(s)-600:]
	}
	return s
}
