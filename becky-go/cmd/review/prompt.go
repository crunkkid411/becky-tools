// prompt.go — builds the per-file review prompt (system instruction + case
// context + transcript + events) and supplies a sensible default case context
// when the caller does not provide one.
package main

import (
	"fmt"
	"strings"
)

// systemPrompt instructs the model to act as a forensic context reviewer and to
// emit ONLY a JSON array of annotations matching the schema. It is passed via
// --append-system-prompt for the claude-code backend and as the system role for
// openrouter.
const systemPrompt = `You are a forensic media context reviewer assisting an investigation.
You are given the full transcript and the detected events for a SINGLE media file,
plus case context describing known entities, timeline anchors, and what to flag.

Your job: surface nuanced context that the deterministic ingest models miss —
especially WHO/WHAT a vague reference points to (pronouns, "my ex", "she", "the house"),
and notable moments (admissions, threats, denials, location/identity cues).

Output rules (STRICT):
- Respond with ONLY a JSON array of annotation objects. No prose, no markdown, no code fences.
- Each annotation object MUST have ALL of these fields:
  "type" (string: reference_resolution | notable_moment | entity_mention | location_cue | other),
  "segment_start" (number, seconds),
  "segment_end" (number, seconds),
  "text" (string: the exact transcript/event text the note is about),
  "resolution" (string: who/what it resolves to, or a one-line finding),
  "rationale" (string: WHY — never empty; cite the case context or transcript evidence),
  "confidence" (number 0.0-1.0),
  "significance" (string: low | medium | high),
  "reviewed" (boolean: always false).
- Use NAMED entities from the case context when you can justify it; never invent facts.
- Prefer fewer, well-reasoned annotations over many speculative ones.
- If you find nothing noteworthy, return an empty array: []`

// defaultCaseContext is used when no --case-context file is supplied. It keeps
// the tool runnable end-to-end without a case file while still steering the model
// toward reference resolution and notable-moment detection.
const defaultCaseContext = `# Case Context (default — no case file supplied)

No specific case file was provided. Apply general forensic-review guidance:

## What to flag
- Vague references that hide an identity or place: "my ex", "she", "he", "the wife",
  "that guy", "the house", "back home", "over there".
- Notable moments: admissions, denials of wrongdoing, threats, references to police,
  money, weapons, drugs, or prior incidents.
- Identity / location cues that could corroborate or contradict the record.

## What to ignore
- Ordinary small talk with no investigative value.
- Filler words and false starts that carry no meaning.

## Investigation priorities
- Resolve ambiguous references to the most likely entity, with explicit reasoning
  and a calibrated confidence. State uncertainty honestly.`

// buildUserPrompt assembles the case context, transcript, and events into the
// user-facing prompt body. It is deterministic so the mock backend can derive
// the same structure offline.
func buildUserPrompt(caseContext string, tr transcript, ev eventsDoc, file string) string {
	var b strings.Builder
	b.WriteString("# Media file under review\n")
	fmt.Fprintf(&b, "file: %s\n", file)
	if tr.Duration > 0 {
		fmt.Fprintf(&b, "duration_seconds: %.3f\n", tr.Duration)
	}
	if tr.Language != "" {
		fmt.Fprintf(&b, "language: %s\n", tr.Language)
	}

	b.WriteString("\n# Case context\n")
	b.WriteString(strings.TrimSpace(caseContext))
	b.WriteString("\n")

	b.WriteString("\n# Transcript segments (start-end seconds | text)\n")
	if len(tr.Segments) == 0 && tr.Text != "" {
		fmt.Fprintf(&b, "(no timed segments) full text: %s\n", tr.Text)
	}
	for _, s := range tr.Segments {
		fmt.Fprintf(&b, "[%.2f-%.2f] %s\n", s.Start, s.End, strings.TrimSpace(s.Text))
	}

	b.WriteString("\n# Detected events (type | start-end seconds | description)\n")
	if len(ev.Events) == 0 {
		b.WriteString("(none)\n")
	}
	for _, e := range ev.Events {
		desc := e.Description
		if desc == "" {
			desc = e.Type
		}
		fmt.Fprintf(&b, "[%s] %.2f-%.2f %s\n", e.Type, e.Start, e.End, desc)
	}

	b.WriteString("\nReturn ONLY the JSON array of annotations now.")
	return b.String()
}
