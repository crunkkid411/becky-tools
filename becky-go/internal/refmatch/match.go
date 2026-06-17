package refmatch

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Thresholds — the corroborate-then-conclude floor (FORENSIC-OUTPUT-PHILOSOPHY).
// We do NOT flood Jordan with a move for every tiny number. A band only earns an EQ
// suggestion when it is audibly off; below that it is "close enough" and stays quiet.
// These are documented constants, deliberately conservative.
const (
	// eqThresholdDB: minimum |target-mine| per band to bother suggesting an EQ move.
	// ~1.5 dB is around the smallest tonal shift a producer reliably hears on a band.
	eqThresholdDB = 1.5
	// gainThresholdDB: minimum overall level difference to call out a fader move.
	gainThresholdDB = 1.0
	// crestThresholdDB: minimum crest-factor difference to call out a compression move.
	// Below this the dynamics already match closely enough.
	crestThresholdDB = 1.5
	// crestPerDBComp: rough rule of thumb mapping a crest-factor gap to bus-comp gain
	// reduction. Honest approximation — it points the direction and rough amount, not
	// an exact ratio/threshold (those depend on the compressor). ~1 dB GR per 1.5 dB
	// crest gap.
	crestPerDBComp = 1.0 / 1.5
	// maxEQMoveDB caps a suggested tonal move. No producer makes a >12 dB broadband
	// correction; a gap bigger than this means the band is essentially ABSENT in one
	// stem (EQ can't create content that isn't there), so we cap the printed move and
	// flag it as "very thin" rather than emitting an absurd "+80 dB" floor artifact.
	maxEQMoveDB = 12.0
	// bandSilenceDB: a band at/under this level has no real content. When BOTH stems
	// are this quiet in a band there is nothing to match (e.g. a kick has no "air"),
	// so we suppress the move entirely instead of comparing two floors.
	bandSilenceDB = -80.0
)

// EQMove is one concrete tonal correction for a band: how many dB to add/cut and the
// center frequency to do it around, plus a plain-English phrasing.
type EQMove struct {
	Band     string  `json:"band"`      // band name, e.g. "presence"
	CenterHz float64 `json:"center_hz"` // geometric center of the band
	DeltaDB  float64 `json:"delta_db"`  // target - mine (positive = boost, negative = cut)
	Text     string  `json:"text"`      // "+2.5 dB around 3.0 kHz (presence)"
}

// BandDelta is the raw per-band comparison (always present for every band, even when
// no move is suggested) so the structured output is complete and feeds other tools.
type BandDelta struct {
	Band      string  `json:"band"`
	CenterHz  float64 `json:"center_hz"`
	MineDB    float64 `json:"mine_db"`
	TargetDB  float64 `json:"target_db"`
	DeltaDB   float64 `json:"delta_db"`  // target - mine
	Suggested bool    `json:"suggested"` // true if it cleared the threshold
}

// MatchPlan is the full deterministic answer: a one-line verdict a non-dev reads
// FIRST, then the concrete moves (gain, EQ, compression), then the raw per-band
// deltas and the summary metrics behind them. Same two profiles in -> identical plan.
type MatchPlan struct {
	Headline       string      `json:"headline"` // plain-English, read this first
	GainDB         float64     `json:"gain_db"`  // overall level move: target - mine
	GainText       string      `json:"gain_text,omitempty"`
	EQMoves        []EQMove    `json:"eq_moves"`       // only the bands that cleared threshold
	CrestDeltaDB   float64     `json:"crest_delta_db"` // target_crest - mine_crest
	CompText       string      `json:"comp_text,omitempty"`
	BandDeltas     []BandDelta `json:"band_deltas"` // EVERY band, for completeness/feeds
	BrightnessNote string      `json:"brightness_note,omitempty"`
	Verdict        string      `json:"verdict"` // "close enough" or "N moves to match"
	MoveCount      int         `json:"move_count"`
	Degraded       bool        `json:"degraded,omitempty"`
	Note           string      `json:"note,omitempty"` // caveats: approx loudness, short audio, etc.
}

// Match compares mine against reference and returns the moves to make mine sound like
// reference. reference is the target (the stem that already sounds right); mine is the
// stem to fix. It is pure and deterministic: sorted output, fixed thresholds, no
// randomness. Degraded inputs produce a degraded plan with an honest note, never a
// panic.
func Match(reference, mine Profile) MatchPlan {
	// Defensive: ensure both band lists are in canonical order before zipping.
	sortBandsByDef(reference.Bands)
	sortBandsByDef(mine.Bands)

	plan := MatchPlan{
		GainDB:       round1(reference.LoudnessDB - mine.LoudnessDB),
		CrestDeltaDB: round1(reference.CrestDB - mine.CrestDB),
	}

	// --- per-band tonal deltas ---
	for _, bd := range bandDefs {
		rb, rok := reference.BandByName(bd.name)
		mb, mok := mine.BandByName(bd.name)
		if !rok || !mok {
			continue
		}
		delta := round1(rb.EnergyDB - mb.EnergyDB)
		center := geoMean(bd.lo, bd.hi)
		// Both stems essentially silent in this band -> nothing to match (e.g. a kick
		// has no "air"); don't compare two noise floors and don't suggest a move.
		bothSilent := rb.EnergyDB <= bandSilenceDB && mb.EnergyDB <= bandSilenceDB
		suggested := !bothSilent && math.Abs(delta) >= eqThresholdDB
		plan.BandDeltas = append(plan.BandDeltas, BandDelta{
			Band: bd.name, CenterHz: round1(center),
			MineDB: round1(mb.EnergyDB), TargetDB: round1(rb.EnergyDB),
			DeltaDB: delta, Suggested: suggested,
		})
		if suggested {
			// Cap the printed move: a gap beyond maxEQMoveDB means the band is nearly
			// absent in one stem and EQ can't manufacture it, so we cap + flag "thin"
			// rather than emit an unactionable floor delta.
			moveDelta := delta
			capped := false
			if moveDelta > maxEQMoveDB {
				moveDelta, capped = maxEQMoveDB, true
			} else if moveDelta < -maxEQMoveDB {
				moveDelta, capped = -maxEQMoveDB, true
			}
			plan.EQMoves = append(plan.EQMoves, EQMove{
				Band: bd.name, CenterHz: round1(center), DeltaDB: moveDelta,
				Text: eqText(moveDelta, center, bd.name, capped),
			})
		}
	}
	// EQMoves are produced in fixed band order already; keep deterministic.
	sort.SliceStable(plan.EQMoves, func(i, j int) bool {
		return plan.EQMoves[i].CenterHz < plan.EQMoves[j].CenterHz
	})

	// --- overall gain move ---
	if math.Abs(plan.GainDB) >= gainThresholdDB {
		plan.GainText = gainText(plan.GainDB)
	}

	// --- compression hint from the crest delta ---
	plan.CompText = compText(plan.CrestDeltaDB)

	// --- brightness note (centroid) ---
	plan.BrightnessNote = brightnessNote(reference.CentroidHz, mine.CentroidHz)

	// --- move count + verdict + headline ---
	plan.MoveCount = len(plan.EQMoves)
	if plan.GainText != "" {
		plan.MoveCount++
	}
	if plan.CompText != "" {
		plan.MoveCount++
	}
	plan.Headline = headline(reference, mine, plan)
	if plan.MoveCount == 0 {
		plan.Verdict = "close enough — your stem already matches the reference"
	} else {
		plan.Verdict = fmt.Sprintf("%d %s to match the reference", plan.MoveCount, plural(plan.MoveCount, "move", "moves"))
	}

	// --- degrade propagation + honest caveats ---
	var notes []string
	if reference.Degraded || mine.Degraded {
		plan.Degraded = true
		notes = append(notes, "one or both stems were measured from thin/short/silent audio — treat the numbers as approximate")
	}
	if reference.KWeighted || mine.KWeighted {
		notes = append(notes, "loudness uses becky's K-weight approximation, not certified LUFS — good for 'louder/quieter than', not for compliance")
	} else {
		notes = append(notes, "loudness is RMS dBFS (relative), not certified LUFS")
	}
	plan.Note = strings.Join(notes, "; ")
	return plan
}

// eqText phrases one EQ move in producer language: "+2.5 dB around 3.0 kHz (presence)".
// When capped is set the gap exceeded maxEQMoveDB, meaning the band is nearly absent in
// one stem — we say so, because EQ alone won't close a gap that large.
func eqText(delta, centerHz float64, band string, capped bool) string {
	dir := "+"
	if delta < 0 {
		dir = "-" // explicit minus for a cut
	}
	base := fmt.Sprintf("%s%.1f dB around %s (%s)", dir, math.Abs(delta), hzLabel(centerHz), band)
	if capped {
		if delta > 0 {
			return base + " — your stem is very thin here vs the reference; EQ can only do so much, fix it at the source"
		}
		return base + " — the reference has almost none of this; consider cutting it at the source"
	}
	return base
}

// gainText phrases the overall fader move. Positive = turn up (reference is louder).
func gainText(g float64) string {
	if g > 0 {
		return fmt.Sprintf("turn your stem UP %.1f dB (it's quieter than the reference)", g)
	}
	return fmt.Sprintf("turn your stem DOWN %.1f dB (it's louder than the reference)", math.Abs(g))
}

// compText turns the crest-factor delta into a compression hint. A POSITIVE delta
// (reference crest > mine crest) means the reference is MORE dynamic than you -> you
// are over-compressed -> ease off. A NEGATIVE delta means you are more dynamic than
// the reference -> add bus compression. Within threshold -> no move (empty string).
func compText(crestDelta float64) string {
	if math.Abs(crestDelta) < crestThresholdDB {
		return ""
	}
	gr := round1(math.Abs(crestDelta) * crestPerDBComp)
	if gr < 0.5 {
		gr = 0.5
	}
	if crestDelta < 0 {
		// mine is more dynamic than the reference -> tighten it up
		return fmt.Sprintf("your stem is MORE dynamic than the reference — add ~%.1f dB of bus compression (gentle ratio, slow-ish) to tighten it", gr)
	}
	// reference is more dynamic than mine -> I'm flatter/over-squashed
	return fmt.Sprintf("the reference is MORE dynamic than your stem — ease OFF ~%.1f dB of compression so it breathes like the reference", gr)
}

// brightnessNote summarizes the centroid difference in one human line (a corroborating
// read of the EQ picture — bright vs dark overall). Quiet when centroids are close or
// either is unmeasured.
func brightnessNote(refC, mineC float64) string {
	if refC <= 0 || mineC <= 0 {
		return ""
	}
	// Compare in octaves so the threshold is perceptual, not Hz-linear.
	ratio := refC / mineC
	oct := math.Log2(ratio)
	if math.Abs(oct) < 0.15 { // < ~1/6 octave: essentially the same brightness
		return ""
	}
	if oct > 0 {
		return fmt.Sprintf("overall your stem is DARKER than the reference (brightness %s vs %s)", hzLabel(mineC), hzLabel(refC))
	}
	return fmt.Sprintf("overall your stem is BRIGHTER than the reference (brightness %s vs %s)", hzLabel(mineC), hzLabel(refC))
}

// headline is the FIRST line a non-dev reads: the biggest tonal offender + the
// loudness gap + the move count, in plain words. Built deterministically from the
// computed plan.
func headline(reference, mine Profile, plan MatchPlan) string {
	if plan.Degraded && plan.MoveCount == 0 {
		return "couldn't measure enough audio to compare — give me a longer/louder stem"
	}
	// Find the largest-magnitude SUGGESTED move for the lead clause. Using EQMoves
	// (already threshold-filtered and capped) keeps a both-silent floor artifact from
	// ever leading the headline.
	var lead EQMove
	for _, m := range plan.EQMoves {
		if math.Abs(m.DeltaDB) > math.Abs(lead.DeltaDB) {
			lead = m
		}
	}
	parts := []string{}
	if lead.Band != "" {
		word := "darker"
		if lead.DeltaDB < 0 { // mine is louder in this band than target
			word = "heavier"
		}
		parts = append(parts, fmt.Sprintf("your %s is %.1f dB %s", lead.Band, math.Abs(lead.DeltaDB), word))
	}
	if plan.GainText != "" {
		if plan.GainDB > 0 {
			parts = append(parts, fmt.Sprintf("%.1f dB quieter", plan.GainDB))
		} else {
			parts = append(parts, fmt.Sprintf("%.1f dB louder", math.Abs(plan.GainDB)))
		}
	}
	lead2 := "your stem already matches the reference"
	if len(parts) > 0 {
		lead2 = strings.Join(parts, " and ") + " than the reference"
	}
	if plan.MoveCount == 0 {
		return "Your stem already matches the reference — nothing to do"
	}
	return fmt.Sprintf("%s — %d %s to match it",
		capitalize(lead2), plan.MoveCount, plural(plan.MoveCount, "move", "moves"))
}

// --- formatting helpers ---

// hzLabel formats a frequency as "3.0 kHz" / "250 Hz" the way an EQ shows it.
func hzLabel(hz float64) string {
	if hz >= 1000 {
		return fmt.Sprintf("%.1f kHz", hz/1000)
	}
	return fmt.Sprintf("%.0f Hz", hz)
}

// geoMean returns the geometric center of a band — the right "center frequency" for a
// log-spaced EQ band (NOT the arithmetic mean).
func geoMean(lo, hi float64) float64 {
	if lo <= 0 || hi <= 0 {
		return (lo + hi) / 2
	}
	return math.Sqrt(lo * hi)
}

// round1 rounds to one decimal place so output is stable and readable.
func round1(v float64) float64 {
	return math.Round(v*10) / 10
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
