package main

// breath.go - EXAMPLES ONLY, it changes nothing in the edit.
//
// Jordan, 2026-10-05: becky-cut keeps loud breaths because "they are loud enough
// and part of the same chunk"; he asked for a separate pass over the KEPT clips
// that "must come AFTER the first VAD filter pass and does NOT affect the first
// VAD filter pass in any way", and wanted to hear the idea before testing. Until
// he says yes this step only MARKS about five such spots for him to judge (the
// same way the 1-3 frame cuts were judged): a gap inside a kept piece where
// neither transcription pass heard a word, that is still louder than
// becky-cut's own silence threshold. It reads becky-cut's finished decisions
// and the audio; it never touches becky-cut or BeckyCut.cs.

import (
	"fmt"
	"math"
	"sort"
)

const (
	breathMinGap   = 0.35 // seconds without a word that counts as a pause
	breathExamples = 5
)

type breath struct {
	A, B float64 // source seconds
	DB   float64
}

// breathSpots finds the loud wordless gaps inside the final pieces.
func breathSpots(words []Word, pieces []span, au *audio, threshold float64) []breath {
	var out []breath
	pi := 0
	for i := 0; i+1 < len(words); i++ {
		a, b := words[i].End, words[i+1].Start
		if b-a < breathMinGap {
			continue
		}
		for pi < len(pieces) && pieces[pi].B <= a {
			pi++
		}
		if pi >= len(pieces) || pieces[pi].A > a || pieces[pi].B < b {
			continue // the gap is not wholly inside one kept piece: becky-cut already cut there
		}
		// Measure the middle of the gap (50 ms in from each word).
		lo, hi := a+0.05, b-0.05
		var sum float64
		n := 0
		for t := lo; t < hi; t += 0.02 {
			d := au.dbAt(t)
			sum += math.Pow(10, d/10)
			n++
		}
		if n == 0 {
			continue
		}
		db := 10 * math.Log10(sum/float64(n))
		if db > threshold {
			out = append(out, breath{a, b, db})
		}
	}
	return out
}

// breathMarks picks about five examples spread across the edit (the longest gap
// in each fifth of the spots, in time order) and the total seconds they add up to.
func breathMarks(spots []breath) ([]breath, float64) {
	total := 0.0
	for _, s := range spots {
		total += s.B - s.A
	}
	var picks []breath
	for k := 0; k < breathExamples && len(spots) > 0; k++ {
		lo, hi := k*len(spots)/breathExamples, (k+1)*len(spots)/breathExamples
		if hi <= lo {
			continue
		}
		best := lo
		for i := lo; i < hi; i++ {
			if spots[i].B-spots[i].A > spots[best].B-spots[best].A {
				best = i
			}
		}
		picks = append(picks, spots[best])
	}
	sort.Slice(picks, func(i, j int) bool { return picks[i].A < picks[j].A })
	return picks, total
}

func breathLabel(n, of int, b breath) string {
	return fmt.Sprintf("Breath example %d of %d: %.1f s with no words but louder than the silence (%.0f dB) - becky could treat it as a pause", n, of, b.B-b.A, b.DB)
}
