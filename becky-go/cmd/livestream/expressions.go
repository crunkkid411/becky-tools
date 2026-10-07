package main

// expressions.go - put back a silence becky-cut took out when his FACE is the
// moment. Jordan, 2026-10-06: "'Some of my videos got restored' Then I made a
// really eggadurated facial expression - it was visually obvious." becky-cut
// hears silence and cuts it; on the 27-livestream two such cuts held his mouth
// wide open (stream 3:10.6-3:11.5 and 3:15.8-3:16.7, checked by eye in frame
// strips). A cut of up to 2 s INSIDE one kept section goes back in when BOTH
// face models see his mouth held open - insightface (0.4 s+) and MediaPipe's
// jawOpen (0.3 s+): the restore is placed on his timeline as a fact, so it
// needs two signals agreeing (on those two cuts they did). A marker says so.
// This only ever puts picture back; it never cuts anything.

import (
	"fmt"

	"becky-go/internal/config"
)

const expressionGapMax = 2.0 // seconds: a longer silence is a pause, whatever his face does

type restored struct {
	A, B float64
	Look picLook
}

// expressionGaps are becky-cut's cuts inside one kept content range, up to 2 s.
func expressionGaps(ranges []Range, pieces []span) []span {
	var out []span
	for _, r := range ranges {
		var in []span
		for _, p := range pieces {
			if p.A >= r.In-1e-6 && p.B <= r.Out+1e-6 {
				in = append(in, p)
			}
		}
		for i := 1; i < len(in); i++ {
			if g := in[i].A - in[i-1].B; g > 1e-6 && g <= expressionGapMax {
				out = append(out, span{in[i-1].B, in[i].A})
			}
		}
	}
	return out
}

// restoreGaps joins the pieces on each side of every restored gap.
func restoreGaps(pieces []span, back []restored) []span {
	isBack := map[[2]int64]bool{}
	for _, g := range back {
		isBack[[2]int64{ms(g.A), ms(g.B)}] = true
	}
	var out []span
	for _, p := range pieces {
		if n := len(out); n > 0 && isBack[[2]int64{ms(out[n-1].B), ms(p.A)}] {
			out[n-1].B = p.B
			continue
		}
		out = append(out, p)
	}
	return out
}

func ms(t float64) int64 { return int64(t*1000 + 0.5) }

// keepExpressions returns the pieces with every held facial expression put
// back, the gaps it restored, and a note when the face check could not run (the
// pieces are then left exactly as becky-cut made them).
func keepExpressions(cfg config.Config, media, work, stem string, ranges []Range, pieces []span, logf func(string, ...any)) ([]span, []restored, string) {
	gaps := expressionGaps(ranges, pieces)
	if len(gaps) == 0 {
		return pieces, nil, ""
	}
	logf("face check: watching %d short pauses becky-cut took out...", len(gaps))
	pics, err := loadPictures(cfg, media, picCache(work, stem), gaps)
	if err != nil {
		return pieces, nil, "the face check did not run: " + err.Error()
	}
	var back []restored
	for _, g := range gaps {
		l := pics.look(g.A, g.B)
		if in, mp := l.faceHeld(); l.Frames >= 3 && in && mp {
			back = append(back, restored{g.A, g.B, l})
		}
	}
	logf("  %d put back (his face holds an expression)", len(back))
	return restoreGaps(pieces, back), back, ""
}

// expressionMarks: a marker where each restored pause starts on the timeline.
// A marker, not a region: his script deletes everything inside regions.
func expressionMarks(back []restored, ps []piece) []mark {
	var out []mark
	for _, g := range back {
		if t, _, ok := toTimeline(ps, g.A, g.A+0.04); ok {
			out = append(out, mark{At: t, Label: fmt.Sprintf("Kept for the picture - his face (%s); becky-cut had cut this %.1f s pause",
				g.Look.faceWhy(), g.B-g.A)})
		}
	}
	return out
}
