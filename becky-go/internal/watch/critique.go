// critique.go — the model watches the FINISHED FILE and says whether it is
// pointing at the right thing.
//
// Jordan, 2026-08-21, after becky rendered a short framed on a Pikachu poster:
//
//	"Regarding cropping, an LLM needs to verify all of that - we're not picking
//	 random dumb data points and rendering that shit; quickest way to get someone
//	 fired. I re-watch a video clip like 10 fucking times before I hit render -
//	 and I said this last session too. Here's why - it catches obvious mistakes.
//	 If gemma4 had just been utilized with its video and audio understanding and
//	 made to watch the goddamn output when it was focused on the pikachu poster
//	 it would have said 'oh wait, that isn't right...the context is about the
//	 mouse trap and the mcdonalds bag'."
//
// He is describing the Editor/Critic loop from EditDuet (research/paper-2509.10761.md)
// and he is describing it better than the paper does: the critic's job is not to
// score the plan, it is to LOOK AT THE RENDER and catch the obvious mistake that
// every feed-forward pipeline ships.
//
// THIS IS THE DIFFERENCE FROM Decide(). Decide watches the SOURCE and answers
// "what is this clip". Critique watches the RENDERED 9:16 FILE and answers "is
// this pointing at what the clip is about" — a question that cannot be asked of
// a plan, only of an output. becky-short's existing --review pass looks at the
// output too, but its own header says "No model call anywhere in this file": it
// counts faces and checks caption timing. It cannot notice that the thing in
// frame is a poster.
//
// The verdict is not just a yes/no. When the answer is no, the model NAMES what
// should have been in frame, and that name goes straight into
// ground.Options.Target for the re-frame — so the critic does not merely reject,
// it tells the next pass what to look for.
package watch

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// CritiqueOptions is one look at one rendered file.
type CritiqueOptions struct {
	// Rendered is the 9:16 file that was actually produced. NOT the source.
	Rendered string
	// Duration of the rendered file, seconds.
	Duration float64
	// About is what the clip is about, in the model's own words from the watch
	// pass ("The prank is revealed and the person reacts to the hidden camera").
	// This is the yardstick the framing is judged against — without it the
	// critic is guessing at intent from a vertical crop, which is the same blind
	// spot the framing ladder already has.
	About string
	// Transcript is the rendered file's words, timestamped. Optional.
	Transcript string
	// Framing is what becky CURRENTLY framed on, as a bare noun phrase
	// ("colorful poster"). It is deliberately NOT shown to the model — see
	// critiquePrompt for why that was actively harmful — and is used only to
	// reject a verdict that asks for the thing already in use.
	Framing string
}

// Verdict is the critic's answer.
type Verdict struct {
	// OK is true when the framing shows what the clip is about.
	OK bool `json:"ok"`
	// Problem is what is wrong, in one short sentence. Empty when OK.
	Problem string `json:"problem"`
	// Subject is what SHOULD be in frame, as a short noun phrase a detector can
	// be pointed at ("the man in the pink shirt", "the mouse trap"). This is the
	// actionable half: it becomes the next pass's grounding target.
	Subject string `json:"subject"`
	// Raw is the model's unparsed reply, kept for the note when parsing is thin.
	Raw string `json:"-"`
}

// critiqueGridCols/Rows shape the sheet the critic reads. Denser than the watch
// pass's 5x5 because framing is a per-shot question and a 30-span short changes
// what it is pointing at many times; 4x6 at 240px keeps a vertical tile
// readable while covering more of the running time.
const (
	critiqueCols  = 6
	critiqueRows  = 4
	critiqueTileW = 240
)

// Critique watches the rendered file and judges its framing.
//
// It reuses buildGrid, so it inherits the named-font fix and the unlabelled
// fallback: a contact sheet that cannot carry timestamps is still watched.
func (r *Runner) Critique(opt CritiqueOptions) (Verdict, error) {
	if opt.Duration <= 0 {
		return Verdict{}, fmt.Errorf("the rendered file has no measurable length")
	}
	grid, _, _, err := r.buildGridN(opt.Rendered, 0, opt.Duration, critiqueCols, critiqueRows, critiqueTileW)
	if err != nil {
		return Verdict{}, err
	}
	defer os.Remove(grid)

	answer, err := r.ask(grid, critiquePrompt(opt))
	if err != nil {
		return Verdict{}, err
	}
	v, err := parseVerdict(answer)
	v.Raw = answer
	return v, err
}

func critiquePrompt(opt CritiqueOptions) string {
	var b strings.Builder
	b.WriteString("These frames are a FINISHED vertical short, sampled evenly from start to end and read " +
		"left-to-right then top-to-bottom. It was cropped automatically out of a wider video.\n\n")
	b.WriteString("YOUR JOB IS TO CATCH AN OBVIOUS MISTAKE, the way an editor does when they watch a cut " +
		"back before rendering it. The crop was chosen by detectors that do not understand the video. " +
		"They sometimes lock onto the wrong thing — a poster on the wall, a doorway, an empty sofa — and " +
		"the result plays fine while showing nothing that matters.\n\n")

	// NEVER SHOW THE MODEL WHAT BECKY THINKS IT FRAMED ON. Measured 2026-08-21:
	// the first version passed becky's own note (`grounded "colorful poster"`) in
	// as context, so the critic could catch a stated reason that did not match the
	// pixels. It anchored on it instead and replied "The colorful poster, WHICH
	// THE CLIP IS ABOUT, is not visible in any of the frames" — demanding becky
	// re-frame onto the exact wrong thing it had just been rescued from. A wrong
	// answer offered as context becomes the answer. The critic gets the pictures,
	// the words, and what the clip is about. Nothing else.
	if s := strings.TrimSpace(opt.About); s != "" {
		fmt.Fprintf(&b, "WHAT THIS CLIP IS ABOUT, decided by watching the original video: %s\n\n", s)
	} else {
		b.WriteString("Nobody has told you what this clip is about — work it out from the frames and the " +
			"words below. Do NOT assume the most eye-catching object in frame is the subject: a poster, " +
			"a sofa or a doorway is scenery.\n\n")
	}
	if s := strings.TrimSpace(opt.Transcript); s != "" {
		fmt.Fprintf(&b, "What is being said over these frames:\n%s\n\n", s)
	}

	b.WriteString("Answer these two questions from the FRAMES THEMSELVES:\n" +
		"  1. Across these frames, is the thing the clip is about actually visible and reasonably placed?\n" +
		"  2. If it is not, what SHOULD be in frame? Name it in a few plain words, as a thing a detector " +
		"could be told to look for — a person (\"the man in the pink shirt\"), or an object " +
		"(\"the mouse trap\", \"the McDonald's bag\").\n\n" +
		"Judge it as a whole. A few frames that show a room or a doorway are NORMAL in this kind of video " +
		"and are not a mistake by themselves — people walk, cameras move, and not every shot is on a face. " +
		"Say it is wrong only when the crop is genuinely pointed at something that does not matter, or when " +
		"the thing the clip is about is cut off or out of frame when it should be visible.\n\n")

	b.WriteString("Reply with JSON only:\n" +
		`{"ok": true|false, "problem": "<one short sentence, empty if ok>", "subject": "<what should be in frame, empty if ok>"}`)
	return b.String()
}

func parseVerdict(s string) (Verdict, error) {
	i, j := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if i < 0 || j <= i {
		return Verdict{}, fmt.Errorf("no JSON in the critic's answer")
	}
	var v Verdict
	if err := json.Unmarshal([]byte(s[i:j+1]), &v); err != nil {
		return Verdict{}, fmt.Errorf("the critic's answer did not parse: %w", err)
	}
	v.Problem = strings.TrimSpace(v.Problem)
	v.Subject = strings.TrimSpace(v.Subject)

	// A rejection with nothing to look for instead is not actionable: it would
	// re-run the identical framing pass and get the identical answer. Report it
	// as a note rather than spending a whole re-render on it.
	if !v.OK && v.Subject == "" {
		return v, fmt.Errorf("the critic rejected the framing but did not say what should be in frame instead")
	}
	return v, nil
}

// Usable rejects a verdict that asks for the thing becky is ALREADY framed on.
//
// That is not a correction — it is the critic agreeing with the mistake — and
// acting on it spends a whole re-render to produce the identical file. It is
// also the exact shape the anchoring bug above took, so this is the backstop for
// a prompt that ever drifts back toward leaking becky's own answer into the
// question.
func (v Verdict) Usable(currentFraming string) (bool, string) {
	cur := strings.ToLower(strings.TrimSpace(currentFraming))
	sub := strings.ToLower(v.Subject)
	if v.OK || sub == "" || cur == "" {
		return true, ""
	}
	if strings.Contains(cur, sub) || strings.Contains(sub, cur) {
		return false, fmt.Sprintf("the model watched the finished file and asked for %q — which is what "+
			"becky already framed on, so that is not a correction and the render stands", v.Subject)
	}
	return true, ""
}
