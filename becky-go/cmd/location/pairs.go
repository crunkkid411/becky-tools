package main

import (
	"fmt"

	"becky-go/internal/location"
)

// buildPairVerdicts produces an explicit same/different verdict per pair of
// interest. When the caller supplied --pair restrictions, only those pairs are
// reported; otherwise every SAME_ROOM pair from the clustering is surfaced (the
// corroborated conclusions), which is what feeds becky-framematch exhibits.
func buildPairVerdicts(clips []location.Clip, cr location.ClusterResult, ofInterest [][2]int) []PairVerdict {
	byIdx := map[int]location.Clip{}
	for _, c := range clips {
		byIdx[c.Index] = c
	}
	// Index scored pairs for quick lookup.
	scoreOf := map[[2]int]location.SignalScore{}
	for _, p := range cr.Pairs {
		scoreOf[key(p.A, p.B)] = p.Score
	}

	var out []PairVerdict
	emit := func(a, b int) {
		ca, oka := byIdx[a]
		cb, okb := byIdx[b]
		if !oka || !okb || ca.Degraded != "" || cb.Degraded != "" {
			return
		}
		sc, ok := scoreOf[key(a, b)]
		if !ok {
			return
		}
		level := "DIFFERENT_DWELLING"
		basis := "different rooms — signals do not corroborate same room"
		conf := 0.6
		sameRoom := cr.RoomOf[a] != "" && cr.RoomOf[a] == cr.RoomOf[b]
		if sameRoom {
			level = "SAME_ROOM"
			basis = sameRoomPairBasis(sc)
			conf = round3(0.5 + 0.5*float64(sc.Agreeing)/float64(maxInt(sc.Available, 1)))
		} else if sc.Agreeing == 1 {
			level = "UNDETERMINED"
			basis = "one signal matches but the others disagree — needs a human"
			conf = 0.4
		}
		pv := PairVerdict{
			A:          a,
			B:          b,
			Level:      level,
			Confidence: conf,
			Signals: PairSignals{
				DecorHashHamming: sc.DecorHamming,
				ColorChi2:        roundOrNeg(sc.ColorChi2),
				FeatureInliers:   featureInliers(sc.FeatureDist),
			},
			Basis: basis,
		}
		if level == "SAME_ROOM" {
			pv.ExhibitHint = fmt.Sprintf("becky-framematch %q %q", ca.Path, cb.Path)
		}
		out = append(out, pv)
	}

	if len(ofInterest) > 0 {
		for _, p := range ofInterest {
			a, b := p[0], p[1]
			if a > b {
				a, b = b, a
			}
			emit(a, b)
		}
		return out
	}

	// Default: surface SAME_ROOM pairs (the conclusions worth an exhibit).
	for _, p := range cr.Pairs {
		if cr.RoomOf[p.A] != "" && cr.RoomOf[p.A] == cr.RoomOf[p.B] {
			emit(p.A, p.B)
		}
	}
	return out
}

func sameRoomPairBasis(s location.SignalScore) string {
	switch {
	case s.DecorAgrees && s.ColorAgrees && s.FeatureAgrees:
		return "same room — decor hash + color + matched static features all agree"
	case s.DecorAgrees && s.ColorAgrees:
		return "same room — decor hash and color palette agree"
	case s.DecorAgrees && s.FeatureAgrees:
		return "same room — decor hash and matched static features agree"
	case s.ColorAgrees && s.FeatureAgrees:
		return "same room — color and matched static features agree"
	default:
		return "same room — corroborated by 2+ signals"
	}
}

func featureInliers(featureDist float64) float64 {
	if featureDist < 0 {
		return -1
	}
	return round3(1 - featureDist)
}

func roundOrNeg(f float64) float64 {
	if f < 0 {
		return -1
	}
	return round3(f)
}

func key(a, b int) [2]int {
	if a > b {
		a, b = b, a
	}
	return [2]int{a, b}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func round3(f float64) float64 {
	return float64(int(f*1000+0.5)) / 1000
}
