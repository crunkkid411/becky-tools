package vox

import "math"

// align.go turns DTW output + detected notes into the human-facing analysis: the
// per-syllable warp map (timing), the per-note pitch decisions (pitch.go), the
// per-phrase metrics, and the corrections-log seed. It ANALYZES, it does not bake:
// the renderer is a separate (stubbed) step gated on per-phrase approval (SPEC §5).
// Deterministic: same features + same options => same AnalysisResult.

// AlignOptions are the run knobs (SPEC §4.2 timing/pitch blocks).
type AlignOptions struct {
	Mode         string         // "stack" (match guide) | "tune" (snap to scale)
	Key          string         // tune-mode target key, e.g. "Am"
	BandFrames   int            // Sakoe-Chiba band width in frames (0 = unconstrained)
	Weights      FeatureWeights // DTW feature weights
	MaxShiftSemi float64        // clamp proposed pitch moves (SPEC §3.2)
	FlagShiftMs  float64        // warp shift beyond this is flagged for review
	FlagCents    float64        // pitch move beyond this is flagged for review
	PhraseGapMs  float64        // a silence gap >= this starts a new phrase
}

// DefaultAlignOptions are the SPEC's starting values (under-process by default).
func DefaultAlignOptions() AlignOptions {
	return AlignOptions{
		Mode:         "stack",
		BandFrames:   0,
		Weights:      DefaultWeights(),
		MaxShiftSemi: 2.0,
		FlagShiftMs:  60, // VocALign-class "double sounds like one" tolerance
		FlagCents:    50,
		PhraseGapMs:  300,
	}
}

// Align runs the full deterministic analysis on already-extracted features. A
// skipped/empty extraction degrades to a partial result rather than crashing.
func Align(f VoxFeatures, opt AlignOptions, guide, alt string) AnalysisResult {
	res := AnalysisResult{
		Tool: "becky-vox", SchemaVersion: SchemaVersion, Mode: opt.Mode,
		Guide: guide, Alt: alt, Deterministic: true,
	}
	if f.Skipped {
		res.Degraded = true
		res.Reason = orDefault(f.Reason, "feature extraction skipped")
		return res
	}

	path, _ := DTW(f.Guide, f.Alt, opt.Weights, opt.BandFrames)
	res.WarpMap = buildWarpMap(path, f.Guide, f.Alt, opt)
	res.Notes = MatchPitch(f.GuideNotes, f.AltNotes, f.CrossCheck, opt)
	res.Phrases = buildPhrases(res.Notes, res.WarpMap, opt)
	res.Corrections = collectCorrections(res.WarpMap, res.Notes)
	return res
}

// buildWarpMap reduces the dense DTW path to one entry per alt syllable: the shift
// (alt onset -> guide onset), the local stretch, and a confidence that drops with a
// big shift. Flagged when the shift exceeds the tolerance (SPEC §2.3, §5).
func buildWarpMap(path []WarpStep, guide, alt []FeatureFrame, opt AlignOptions) []WarpEntry {
	if len(path) == 0 {
		return nil
	}
	// Earliest guide frame each alt frame maps to (the path is ascending; keep the
	// first guide index per alt index for a stable, deterministic mapping).
	guideForAlt := map[int]int{}
	for _, s := range path {
		if _, seen := guideForAlt[s.A]; !seen {
			guideForAlt[s.A] = s.G
		}
	}
	out := make([]WarpEntry, 0, len(alt))
	for a := 0; a < len(alt); a++ {
		g, ok := guideForAlt[a]
		if !ok {
			continue
		}
		guideMs := guide[g].T * 1000
		altMs := alt[a].T * 1000
		shift := guideMs - altMs
		stretch := localStretch(path, a)
		out = append(out, WarpEntry{
			GuideOnsetMs: round2(guideMs), AltOnsetMs: round2(altMs),
			ShiftMs: round2(shift), LocalStretch: round3(stretch),
			Confidence: round2(warpConfidence(shift, stretch, opt.FlagShiftMs)),
			Syllable:   a, Flagged: math.Abs(shift) > opt.FlagShiftMs,
		})
	}
	return out
}

// localStretch estimates the warp slope around alt frame a: how many guide frames
// advance while alt frame a is held (1.0 = no stretch).
func localStretch(path []WarpStep, a int) float64 {
	gLo, gHi := -1, -1
	for _, s := range path {
		if s.A == a {
			if gLo < 0 {
				gLo = s.G
			}
			gHi = s.G
		}
	}
	if gHi <= gLo {
		return 1.0
	}
	return float64(gHi - gLo + 1)
}

// warpConfidence is high for a small shift, decaying as the shift approaches the
// flag tolerance; an extreme local stretch also pulls it down.
func warpConfidence(shiftMs, stretch, flagMs float64) float64 {
	if flagMs <= 0 {
		flagMs = 60
	}
	s := 1 - clamp01(math.Abs(shiftMs)/(flagMs*2))
	st := 1 - clamp01(math.Abs(stretch-1)/3)
	return clamp01(0.7*s + 0.3*st)
}

// buildPhrases groups notes into phrases by silence gaps and computes per-phrase
// timing-tightness (from the warp shifts in range) and pitch-stability (low cents
// move within the phrase). SPEC §4.2.
func buildPhrases(notes []VoxNote, warp []WarpEntry, opt AlignOptions) []Phrase {
	if len(notes) == 0 {
		return nil
	}
	var phrases []Phrase
	start := 0
	for i := 1; i <= len(notes); i++ {
		gap := i == len(notes) || (notes[i].StartMs-notes[i-1].EndMs) >= opt.PhraseGapMs
		if gap {
			phrases = append(phrases, phraseMetrics(notes[start:i], warp))
			start = i
		}
	}
	return phrases
}

// phraseMetrics computes one phrase's tightness/stability/confidence.
func phraseMetrics(notes []VoxNote, warp []WarpEntry) Phrase {
	startMs, endMs := notes[0].StartMs, notes[len(notes)-1].EndMs
	moves := make([]float64, 0, len(notes))
	for _, n := range notes {
		moves = append(moves, math.Abs(n.MoveCents))
	}
	stability := 1 - clamp01(meanFloat(moves)/100) // a 1-semitone move = 0 stability
	var shifts []float64
	for _, e := range warp {
		if e.AltOnsetMs >= startMs && e.AltOnsetMs <= endMs {
			shifts = append(shifts, math.Abs(e.ShiftMs))
		}
	}
	tightness := 1 - clamp01(meanFloat(shifts)/120)
	return Phrase{
		StartMs: round2(startMs), EndMs: round2(endMs),
		TimingTightness: round2(tightness), PitchStability: round2(stability),
		Confidence: round2(clamp01(0.5*stability + 0.5*tightness)),
	}
}

// collectCorrections seeds the preference-learning log: one row per flagged warp
// shift and one per flagged pitch move (the edits Jordan is most likely to override
// by eye). Corrected stays nil until he edits.
func collectCorrections(warp []WarpEntry, notes []VoxNote) []Correction {
	var out []Correction
	for i, e := range warp {
		if e.Flagged {
			out = append(out, Correction{
				Kind: "warp.shiftMs", Index: i, Auto: e.ShiftMs, Corrected: nil,
				Reason: "warp shift exceeds tolerance — review by eye",
				Context: map[string]interface{}{
					"syllable": e.Syllable, "guideOnsetMs": e.GuideOnsetMs,
					"altOnsetMs": e.AltOnsetMs, "confidence": e.Confidence,
				},
			})
		}
	}
	for i, n := range notes {
		if n.Flagged {
			out = append(out, Correction{
				Kind: "note.moveCents", Index: i, Auto: n.MoveCents, Corrected: nil,
				Reason: "pitch move flagged (ambiguous F0 / cross-check disagreement) — review by eye",
				Context: map[string]interface{}{
					"startMs": n.StartMs, "detectedHz": n.DetectedHz,
					"targetHz": n.TargetHz, "confidence": n.Confidence,
				},
			})
		}
	}
	return out
}

// BuildRenderPlan assembles the accept-masked plan the (stubbed) renderer bakes.
// accept[i] applies note i's move; an out-of-range/empty mask defaults to applying
// only the UNflagged moves (under-process by default, SPEC §5).
func BuildRenderPlan(res AnalysisResult, guideWav, altWav string, accept []bool) RenderPlan {
	mask := make([]bool, len(res.Notes))
	for i := range res.Notes {
		if i < len(accept) {
			mask[i] = accept[i]
		} else {
			mask[i] = !res.Notes[i].Flagged
		}
	}
	return RenderPlan{
		GuideWav: guideWav, AltWav: altWav, Mode: res.Mode,
		WarpMap: res.WarpMap, Notes: res.Notes, Accept: mask,
	}
}
