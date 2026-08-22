// critic.go — render it, WATCH IT BACK, and fix it if it is wrong.
//
// Jordan, 2026-08-21:
//
//	"I re-watch a video clip like 10 fucking times before I hit render - and I
//	 said this last session too. Here's why - it catches obvious mistakes. If
//	 gemma4 had just been utilized with its video and audio understanding and
//	 made to watch the goddamn output when it was focused on the pikachu poster
//	 it would have said 'oh wait, that isn't right...the context is about the
//	 mouse trap and the mcdonalds bag'. ... Even if Gemma4 or other models have
//	 to be fired up more than once at different steps in the pipeline that is
//	 okay!"
//
// Until now becky was feed-forward: pick a window, decide a crop, render, exit.
// Every detector in the chain is a signal that can be wrong, and nothing ever
// looked at what came out. That is how a short shipped framed on a poster while
// reporting success.
//
// This is the loop:
//
//	render  ->  Gemma-4 WATCHES THE RENDERED FILE  ->  ok?  ->  done
//	                                               \-> no, it should be on "X"
//	                                                    -> aim the grounding
//	                                                       sweep at X, re-frame,
//	                                                       RE-RENDER, watch again
//
// SPEED IS NOT A CONSIDERATION HERE, and that is a deliberate instruction:
// "video editing is iterative, even if it takes a long time... I'm a world class
// video editor and I don't care if it takes an hour; if the edits look like
// shit, I can't use any of this." Each pass costs a full re-render plus a model
// load. That is the price of not shipping the poster.
//
// ONE MODEL AT A TIME, which is a hardware fact on an 8GB card: Reka (5.5GB) does
// the grounding, Gemma-4 (4.2GB) does the watching, and they cannot both be
// resident. So the grounding server is shut down before each critique and
// restarted for each re-frame — which is why groundaim.go's singleton had to stop
// being a sync.Once.
package main

import (
	"fmt"
	"strings"

	"becky-go/internal/config"
	"becky-go/internal/sidecar"
	"becky-go/internal/watch"
)

// criticMaxTranscript is how many cues of the RENDERED file's own words the
// critic is shown. Enough to know what is being talked about over the frames it
// is looking at, short enough to leave the picture room in the context window.
const criticMaxTranscript = 40

// renderAndCritique renders the job, then watches what it rendered and re-renders
// as many times as it is allowed while the critic keeps naming a better subject.
//
// passes is how many CORRECTION passes are permitted (0 = the old feed-forward
// behaviour, render once and ship it). It always returns the last successfully
// rendered result: a critic that cannot run, cannot parse, or cannot improve
// things is a note on a finished short, never a lost one.
func renderAndCritique(cfg config.Config, j job, asp float64, outW, outH int,
	sampleFPS, minCov, maxGap float64, forceCenter, focalPoint, withCaptions bool, capStyle string,
	useJumpcuts bool, tighten float64, useWatch bool, passes int,
	cache *cutCache, asig *audioSigCache, verbose bool) (shortOut, error) {

	// A fresh short inherits nothing from the last one's critique.
	setShortTarget("")

	res, err := render(cfg, j, asp, outW, outH, sampleFPS, minCov, maxGap, forceCenter, focalPoint,
		withCaptions, capStyle, useJumpcuts, tighten, useWatch, cache, asig, verbose)
	if err != nil || passes <= 0 {
		return res, err
	}

	// What the clip is ABOUT is the yardstick. It comes from the watch pass's own
	// words, which are already in the note; without it the critic is judging a
	// vertical crop against nothing and will wave anything through.
	about := aboutFromNote(res.Note)

	for pass := 1; pass <= passes; pass++ {
		v, cerr := critiqueRender(cfg, res, about, verbose)
		if cerr != nil {
			note(&res, "the critic could not watch this render ("+firstLineStr(cerr.Error())+
				"), so it ships as framed")
			return res, nil
		}
		// A verdict that asks for what becky already framed on is the critic
		// agreeing with the mistake, not correcting it.
		if ok, why := v.Usable(currentSubjectFromNote(res.Note)); !ok {
			note(&res, why)
			return res, nil
		}
		if v.OK {
			if pass == 1 {
				note(&res, "the model watched the finished file and agreed the framing shows what the "+
					"clip is about")
			} else {
				note(&res, fmt.Sprintf("re-framed and re-rendered %d time(s); the model watched the "+
					"final file and agreed it is now on the right thing", pass-1))
			}
			return res, nil
		}

		// REJECTED, and it said what to look for instead. Aim there and go again.
		logIfShort(verbose, "  critic (pass %d): %s -> re-framing on %q", pass, v.Problem, v.Subject)
		setShortTarget(v.Subject)

		// RE-RENDER THE WINDOW THE MODEL CHOSE, not the one becky proposed before
		// it watched. j still holds the proposed window; the watch pass's answer
		// came back on res. Passing j here would silently throw away the in/out
		// the model picked every time the critic asked for a re-frame.
		rj := j
		rj.In, rj.Out = res.Start, res.End
		retry, rerr := render(cfg, rj, asp, outW, outH, sampleFPS, minCov, maxGap, forceCenter, focalPoint,
			withCaptions, capStyle, useJumpcuts, tighten, false, cache, asig, verbose)
		if rerr != nil {
			// The re-render failed; the first one did not. Ship the one that works.
			note(&res, fmt.Sprintf("the model watched the finished file and said %q, but re-framing on "+
				"%q would not render (%s) - this is the original framing",
				v.Problem, v.Subject, firstLine(rerr)))
			return res, nil
		}
		note(&retry, fmt.Sprintf("PASS %d: the model watched the previous render and said %q, so becky "+
			"re-framed on %q and rendered it again", pass, v.Problem, v.Subject))
		res = retry
	}

	note(&res, fmt.Sprintf("the model was still not satisfied after %d re-frame(s); this is the last one "+
		"it asked for", passes))
	return res, nil
}

// critiqueRender frees the card, starts Gemma-4, shows it the rendered file and
// shuts it down again.
func critiqueRender(cfg config.Config, res shortOut, about string, verbose bool) (watch.Verdict, error) {
	// THE CARD HAS ROOM FOR ONE. Reka is still resident from the framing pass.
	closeGrounder()

	logf := func(f string, a ...any) { logIfShort(verbose, f, a...) }
	w, err := watch.New(cfg, logf)
	if err != nil {
		return watch.Verdict{}, err
	}
	defer w.Close()

	dur, derr := sourceDuration(cfg.FFprobe, res.Out)
	if derr != nil || dur <= 0 {
		return watch.Verdict{}, fmt.Errorf("could not measure the rendered file")
	}
	return w.Critique(watch.CritiqueOptions{
		Rendered:   res.Out,
		Duration:   dur,
		About:      about,
		Framing:    currentSubjectFromNote(res.Note),
		Transcript: renderedTranscript(res.Out),
	})
}

// aboutFromNote digs the watch pass's own description of the clip back out of
// the note it wrote. The watch pass phrases its answer three ways depending on
// whether the window moved, so match on the shared part rather than the prefix.
// CUT TO THE SEGMENT FIRST, THEN LOOK FOR QUOTES. This searched the whole rest
// of the note for a quoted phrase, and the note is a "; "-joined list that goes
// on to include the framing ladder's own `grounded "colorful poster"`. So on a
// real note it skipped past the watch pass's sentence entirely and returned
// "colorful poster" as WHAT THE CLIP IS ABOUT — handing the critic becky's
// mistake as the yardstick it was supposed to judge that mistake against. It
// duly replied "The colorful poster, which the clip is about, is not visible"
// and demanded a re-frame onto the poster. Measured 2026-08-21.
func aboutFromNote(note string) string {
	for _, marker := range []string{"payoff at ", "kept the window as proposed: "} {
		i := strings.Index(note, marker)
		if i < 0 {
			continue
		}
		seg := note[i+len(marker):]
		if b := strings.Index(seg, "; "); b >= 0 {
			seg = seg[:b]
		}
		// Within THIS segment only: `55.4s is "The prankster reveals..."`.
		if a := strings.Index(seg, `"`); a >= 0 {
			if b := strings.Index(seg[a+1:], `"`); b > 0 {
				return seg[a+1 : a+1+b]
			}
		}
		return strings.TrimSpace(seg)
	}
	return ""
}

// renderedTranscript reads the .srt that was burned into this short, so the
// critic knows what is being SAID over the frames it is looking at. The sidecar
// is written beside the output by the caption pass; no sidecar is a quieter
// critique, not a failure.
func renderedTranscript(out string) string {
	path := captionSidecarPath(out)
	sub, err := sidecar.ParseSubtitle(path)
	if err != nil || len(sub.Segments) == 0 {
		return ""
	}
	var b strings.Builder
	for i, s := range sub.Segments {
		if i >= criticMaxTranscript {
			break
		}
		if t := strings.TrimSpace(s.Text); t != "" {
			fmt.Fprintf(&b, "%.1f-%.1f %s\n", s.Start, s.End, t)
		}
	}
	return b.String()
}

// currentSubjectFromNote pulls the noun phrase becky is CURRENTLY framed on back
// out of its own note. The framing ladder writes it as grounded "colorful
// poster", so that is what is matched. Empty when the ladder framed on a person
// or on motion instead, in which case there is nothing for the critic to
// accidentally echo back.
func currentSubjectFromNote(n string) string {
	const marker = `grounded "`
	i := strings.Index(n, marker)
	if i < 0 {
		return ""
	}
	rest := n[i+len(marker):]
	j := strings.Index(rest, `"`)
	if j <= 0 {
		return ""
	}
	return rest[:j]
}
