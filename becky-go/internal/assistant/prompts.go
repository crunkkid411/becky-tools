package assistant

import (
	"fmt"
	"strings"

	"becky-go/internal/footage"
)

// prompts.go assembles the FIXED, small per-turn context (R-AI §3.3): a system
// prompt + the action catalog + the current timeline + (for the funnel) one
// window of candidates. The fixed blocks are byte-identical every turn so a warm
// llama-server KV cache / a frontier prompt cache can reuse them. The candidates
// block is the only variable part and is window-bounded by construction.

// actionCatalog is the verb list + DSL grammar the model is allowed to emit. It
// is the model's entire control surface (default-deny; schema.go enforces it).
const actionCatalog = `You may ONLY emit actions from this fixed allowlist, one per line in the DSL
"verb key=value key=value" (quote values with spaces), or as a JSON array of
{"verb":..., "args":{...}}. Any other verb is rejected.

  search       query=<text> [mode=hybrid|vector|keyword] [limit=N]
  find_quotes  criteria=<text> [srt=<path>]
  preview_clip source=<file> in=<tc> out=<tc>
  grab_frame   source=<file> at=<tc>
  add_clip     source=<file> in=<tc> out=<tc> [label=<text>] [at=<index>]
  remove_clip  (id=<id> | index=<n>)
  reorder      (id=<id> | index=<n>) to=<index>
  set_overlay  (id=<id> | index=<n>) field=<name> value=<text>
  set_marker   at=<tc> [label=<text>]
  set_label    (id=<id> | index=<n>) text=<text>
  export       [preset=<name>] [out=<path>] [range=<a-b>]

Timestamps (in/out/at) MUST be copied verbatim from a candidate cue boundary —
never invent a time. source MUST be a folder-relative filename from the index.`

// baseSystem is the role + forensic rules shared by every model call.
const baseSystem = `You drive becky-clip, a forensic transcript-based video editor for a detective.
Rules:
- Corroborate, then conclude. Surface a candidate only when the transcript
  supports it; do not invent matches.
- Never modify originals. Your actions only build a compilation timeline.
- Emit ONLY allowlisted actions (below). No prose, no free-form code.
- Every timestamp you emit is copied verbatim from a provided cue boundary.`

// mapSystemPrompt instructs the MAP step (step [2]): judge which candidate cues in
// one window match the criteria, returning their 1-based indices.
const mapSystemPrompt = baseSystem + `

TASK: From the numbered candidate cues below, return ONLY the indices (1-based) of
the cues that match the user's criteria. Reply as a JSON array of integers, e.g.
[2,5,9]. If none match, reply []. Do not explain.`

// planSystemPrompt instructs the PLAN step (step [4]): turn the reduced surviving
// cues into a concrete action list (add_clip × N, ordered, labelled).
const planSystemPrompt = baseSystem + "\n\n" + actionCatalog + `

TASK: Turn the matching cues below into an ordered action list that adds each as a
clip to the timeline (add_clip with the cue's verbatim source/in/out and a short
label). Emit ONLY the action list.`

// localSystemPrompt instructs the Tier-1 local model: parse ONE fuzzy
// single-action request into ONE action.
const localSystemPrompt = baseSystem + "\n\n" + actionCatalog + `

TASK: Convert the user's single request into exactly ONE action from the
allowlist. Use the current timeline state for references like "the last clip".
Emit ONLY that one action.`

// timelineBlock renders the current timeline compactly for the prompt (R-AI §3.3,
// ~50 tok/clip). Empty timeline → a short marker so the model knows it's empty.
func timelineBlock(ts TimelineState) string {
	if len(ts.Clips) == 0 {
		return "TIMELINE: (empty)"
	}
	var b strings.Builder
	b.WriteString("TIMELINE:\n")
	for i, c := range ts.Clips {
		label := c.Label
		if label == "" {
			label = "(no label)"
		}
		fmt.Fprintf(&b, "  #%d id=%s %s [%s-%s] %q\n",
			i+1, c.ID, baseName(c.Source), secondsToTimecode(c.In), secondsToTimecode(c.Out), label)
	}
	// H-1 shared state: where Jordan IS. "this clip"/"here" in a request means
	// the selection / the playhead, so the model has to see them.
	fmt.Fprintf(&b, "  PLAYHEAD: %s\n", secondsToTimecode(ts.Playhead))
	if len(ts.Selected) > 0 {
		fmt.Fprintf(&b, "  SELECTED CLIP IDS: %s\n", strings.Join(ts.Selected, ", "))
	}
	return b.String()
}

// mapUserPrompt builds the MAP-step user payload: criteria + one window's
// numbered candidates.
func mapUserPrompt(criteria string, cands []footage.Candidate) string {
	return fmt.Sprintf("CRITERIA: %s\n\nCANDIDATE CUES:\n%s", criteria, candidatesBlock(cands))
}

// planUserPrompt builds the PLAN-step user payload: the timeline + the reduced
// matching cues.
func planUserPrompt(ts TimelineState, criteria string, cands []footage.Candidate) string {
	return fmt.Sprintf("%s\n\nUSER ASK: %s\n\nMATCHING CUES:\n%s",
		timelineBlock(ts), criteria, candidatesBlock(cands))
}

// localUserPrompt builds the Tier-1 user payload: the timeline + the utterance.
func localUserPrompt(ts TimelineState, utt string) string {
	return fmt.Sprintf("%s\n\nREQUEST: %s", timelineBlock(ts), utt)
}

// baseName is a tiny basename helper kept local to avoid importing path utilities
// just for the prompt; it splits on both separators (paths may be Windows-style).
func baseName(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}
