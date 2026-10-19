package main

// videovegas.go - --video, after the edit is built and checked: the censor,
// the subtle zooms and crops, Jordan's hj-starter track effects and the things
// he means to show, each one BeckyFX.cs job, in that order. One failing job is
// reported and the rest still run; the edit itself is already saved.

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// starterFX is Jordan's "hj-starter" filter package, plug-in by plug-in in his
// order (VEGAS 18 scripts cannot add a filter package). Jordan, 2026-10-14:
// "Add the following Track FX to the primary video track in this order".
var starterFX = [][2]string{
	{"Beauty Box", "POLISH"},
	{"AutoLooks", "polish"},
	{"Color Corrector", "hj-simple"},
	{"Color Curves", "hair-help"},
	{"Color Curves", "POLISH-subtle"},
}

// Subtle zooms and crops to break up the talking head (Jordan, 2026-10-14:
// "very subtle so that it's not distracting"), about one a minute, taking
// turns: a slow creep in his own style (VEGAS Picture In Picture, his measured
// creep 1.11x, habits.md) across 8-12 s, then a still crop that starts on a cut.
const (
	looksFirst = 20.0 // seconds into the video before the first one
	looksEvery = 60.0
	creepScale = 1.11
	creepMin   = 8.0
	creepMax   = 12.0
	cropScale  = 1.12
	cropMin    = 3.0
	cropMax    = 8.0
)

// looksJob picks the stretches: whole pieces, so every move starts and ends on
// one of his cuts.
func looksJob(ps []piece, fps float64) (string, int) {
	var sb strings.Builder
	n := 0
	next := looksFirst
	creep := true
	for i := 0; i < len(ps); i++ {
		if ps[i].TL < next {
			continue
		}
		lo, hi := cropMin, cropMax
		if creep {
			lo, hi = creepMin, creepMax
		}
		j, dur := i, 0.0
		for j < len(ps) && dur+(ps[j].Out-ps[j].In) <= hi {
			dur += ps[j].Out - ps[j].In
			j++
		}
		if dur < lo {
			continue // too short a stretch here; try from the next cut
		}
		f0 := int64(math.Round(ps[i].TL * fps))
		f1 := int64(math.Round((ps[i].TL + dur) * fps))
		if creep {
			// Event Pan/Crop, not the Picture In Picture plug-in (Jordan, 2026-10-18)
			fmt.Fprintf(&sb, "panzoom\t%d\t%d\t%.2f\t0.5\n", f0, f1-f0, creepScale)
		} else {
			fmt.Fprintf(&sb, "zoom\t%d\t%d\t%.2f\t0.5\t0.4\t1\n", f0, f1-f0, cropScale)
		}
		n++
		creep = !creep
		next = ps[i].TL + dur + looksEvery
		i = j - 1
	}
	return sb.String(), n
}

// starterJob puts the hj-starter effects on every video track that shows the
// recording (all of them for a split screen; never b-roll).
func starterJob(footage string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "trackfx\t0\t0\t%s", footage)
	for _, fx := range starterFX {
		fmt.Fprintf(&sb, "\t%s\t%s", fx[0], fx[1])
	}
	sb.WriteString("\n")
	return sb.String()
}

// videoVegas runs the jobs and returns what went on the timeline, for the
// report, plus the markers (shifted past any insert) and the placements.
type videoDone struct {
	Bleeps    []bleep
	Looks     int
	Starter   string
	Placed    []placement
	Markers   []mark
	MarkLines []showLine
	Problems  []string
}

func (r *run) videoVegas(words []Word, keep []bool, au *audio, ps []piece, ss []Sentence, lines []showLine) videoDone {
	var d videoDone
	job := func(name, body string, timeout time.Duration) (string, bool) {
		if body == "" {
			return "", true
		}
		res, err := runJob("BeckyFX.cs", filepath.Join(r.work, "vegas-"+name+".txt"), body, timeout)
		if err != nil {
			d.Problems = append(d.Problems, fmt.Sprintf("%s: %v", name, err))
			r.logf("VEGAS: the %s could not be added: %v", name, err)
			return "", false
		}
		r.logf("VEGAS %s: %s", name, res)
		return res, true
	}
	speech := max(len(r.audio)-1, 0) // BeckyKeepList puts the mic on the last audio track

	// 1. the censor
	d.Bleeps = censorJob(words, keep, au, ps, r.fps)
	if len(d.Bleeps) > 0 {
		if _, err := os.Stat(censorSound); err != nil {
			d.Problems = append(d.Problems, "censor: the censor sound is missing ("+censorSound+")")
			d.Bleeps = nil
		} else {
			var sb strings.Builder
			for _, b := range d.Bleeps {
				fmt.Fprintf(&sb, "bleep\t%d\t%d\t%s\t%.1f\t%d\n", b.F0, b.Len, censorSound, censorGain, speech)
			}
			if _, ok := job("censor", sb.String(), 5*time.Minute); !ok {
				d.Bleeps = nil
			}
		}
	}
	// 2. subtle zooms and crops
	if body, n := looksJob(ps, r.fps); n > 0 {
		if _, ok := job("zooms", body, 10*time.Minute); ok {
			d.Looks = n
		}
	}
	// 3. his hj-starter track effects
	d.Starter, _ = job("starter-fx", starterJob(r.footage), 5*time.Minute)
	// 4. what he means to show: his files on the timeline, markers for the rest
	d.Placed, d.Markers, d.MarkLines = placeAssets(lines, ss, ps, r.fps, func(f string) (float64, bool) { return probeAsset(r.cfg.FFprobe, f) })
	if _, ok := job("assets", assetJob(d.Placed, speech), 10*time.Minute); !ok {
		// the job is all or nothing (one Undo step, cancelled on an error), so
		// nothing moved: every line becomes a marker where it was
		bare := make([]showLine, len(lines))
		for i, l := range lines {
			bare[i], bare[i].Asset = l, ""
		}
		d.Placed = nil
		_, d.Markers, d.MarkLines = placeAssets(bare, ss, ps, r.fps, nil)
	}
	return d
}
