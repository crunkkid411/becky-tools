// watchpass.go — before anything is rendered, a model WATCHES the clip and says
// where it starts and ends.
//
// Jordan, 2026-08-20: "Gemma-4 NEEDS to watch the clip. Literally that's the
// only fucking thing you need to do right now because you're giving ME data and
// signals but nothing usable... GEMMA needs to decide the final output."
//
// This is that. It runs FIRST, on the window becky's cheap signals proposed, and
// its answer REPLACES that window. Everything downstream — pacing, framing,
// captions — then works on the clip the model chose.
//
// ORDER MATTERS AND IT IS A HARDWARE FACT. Gemma-4 E4B is 4.2GB and Reka Edge is
// 4.7GB; they do not both fit on an 8GB card. So the watching model is started,
// asked once, and SHUT DOWN before the framing ladder starts the grounding
// model. One model at a time, which is how Jordan already described working
// here: "yes, we have to run each of the vision models one at a time; we've
// established this. That is NOT a problem."
package main

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"becky-go/internal/config"
	"becky-go/internal/sidecar"
	"becky-go/internal/transcribex"
	"becky-go/internal/watch"
)

// transcriptLines renders a source's subtitle sidecar as the timestamped lines
// the prompt wants. Best effort: no transcript is a weaker watch, not a failure,
// because the frames alone still carry the physical action.
func transcriptLines(src string, limit int) string {
	path, _, err := transcribex.EnsureSRT(src, func(string, ...any) {})
	if err != nil {
		return ""
	}
	sub, err := sidecar.ParseSubtitle(path)
	if err != nil {
		return ""
	}
	var b strings.Builder
	n := 0
	var lastEnd float64
	for _, s := range sub.Segments {
		text := strings.TrimSpace(s.Text)
		if text == "" {
			continue
		}
		// A long silence is EVIDENCE, not an absence — on the mouse-trap prank
		// the payoff lives inside thirteen wordless seconds. Say so explicitly
		// so the model looks at the frames there instead of skipping past it.
		if lastEnd > 0 && s.Start-lastEnd >= 4 {
			fmt.Fprintf(&b, "(%.1f to %.1f NO SPEECH)\n", lastEnd, s.Start)
		}
		fmt.Fprintf(&b, "%.1f-%.1f %s\n", s.Start, s.End, text)
		lastEnd = s.End
		if n++; limit > 0 && n >= limit {
			break
		}
	}
	return b.String()
}

// transcriptLineLimit keeps a feature-length transcript from crowding the
// frames out of the context window. 120 cues is roughly ten minutes of speech.
const transcriptLineLimit = 120

// watchAndDecide replaces j's window with the one the model chose.
//
// Every failure here is a DEGRADE: the proposed window is kept and the reason is
// reported. A missing model must not cost the render.
func watchAndDecide(cfg config.Config, j job, cuts []float64, verbose bool) (job, string) {
	dur, err := sourceDuration(cfg.FFprobe, j.Src)
	if err != nil || dur <= 0 {
		return j, "could not measure the source, so the model did not watch it"
	}
	logf := func(f string, a ...any) { logIfShort(verbose, f, a...) }

	w, err := watch.New(cfg, logf)
	if err != nil {
		return j, "the model could not watch this: " + firstLineStr(err.Error())
	}
	// Shut down BEFORE returning, so the card is free for the framing model.
	defer w.Close()

	d, err := w.Decide(watch.Options{
		Video: j.Src, Start: j.In, End: j.Out, Duration: dur,
		Transcript: transcriptLines(j.Src, transcriptLineLimit), Cuts: cuts,
	})
	if err != nil {
		return j, "the model watched this but its answer was unusable: " + firstLineStr(err.Error())
	}
	// The payoff is recorded whether or not the window moved: it is what the
	// downstream trims are forbidden to remove (deadtail.go). And the fact that
	// a model DID watch is recorded too — from here on the out point is its
	// decision, not something the pose tracker gets to shorten.
	setShortPayoff(d.PayoffAt)
	setShortWatched(true)
	if !d.Changed {
		return j, "the model watched this and kept the window as proposed: " + d.Payoff
	}
	j.In, j.Out = d.Start, d.End
	return j, d.Note
}

// sourceDuration reads a source's length in seconds.
func sourceDuration(ffprobe, src string) (float64, error) {
	out, err := exec.Command(ffprobe, "-v", "error", "-show_entries", "format=duration",
		"-of", "default=nw=1:nk=1", src).Output()
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
}
