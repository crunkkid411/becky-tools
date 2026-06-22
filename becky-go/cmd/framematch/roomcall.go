// roomcall.go — combine the independent signals into a room CALL, the
// corroborate-then-conclude heart of the hardened tool. Per
// FORENSIC-OUTPUT-PHILOSOPHY.md and the CLAUDE.md invariant: a same-room
// CONCLUSION needs >=2 independent strong signals agreeing; a lone weak signal
// must stay a candidate, never a conclusion.
package main

// Room-call verdicts (mirrors the JSON enum).
const (
	callSameRoom      = "same_room"
	callDifferentRoom = "different_room"
	callCandidate     = "candidate"
	callUnknown       = "unknown"
)

// signalVote is one independent signal's vote on whether two frames share a room.
type signalVote int

const (
	voteUnknown  signalVote = iota // not enough signal to judge
	voteAgree                      // signal says same room
	voteDisagree                   // signal says different room
)

// roomInputs are the resolved per-pair signals fed to the room call.
type roomInputs struct {
	roiHamming   int  // ROI-aHash Hamming (primary signal)
	roiOK        bool // ROI hashes parsed (false → ROI signal unknown)
	roiFeatured  bool // ROI carried structure (false → featureless → unknown)
	roiThreshold int  // "agree" boundary for ROI Hamming

	keypointsOn     bool // keypoint corroboration enabled
	keypointInliers int  // geometrically-consistent shared features
	keypointPop     int  // judgeable keypoint population (min detected)
	minInliers      int  // "agree" boundary for inliers

	wholeHamming   int // whole-frame aHash Hamming (weak/provenance only)
	wholeThreshold int // legacy --threshold
}

// roomResult is the computed call for one pair.
type roomResult struct {
	call         string
	confidence   float64
	signalsUsed  []string
	roiVote      signalVote
	keypointVote signalVote
}

// roiClearDiff is how many ROI-Hamming bits past the agree threshold counts as a
// clear DISAGREE (vs the ambiguous middle band → unknown). 8x8 aHash is coarse,
// so a clear difference is a meaningful gap.
const roiClearDiff = 16

// evalROI converts the ROI-hash signal into a vote.
func evalROI(in roomInputs) signalVote {
	if !in.roiOK || !in.roiFeatured {
		return voteUnknown
	}
	if in.roiHamming <= in.roiThreshold {
		return voteAgree
	}
	if in.roiHamming >= in.roiThreshold+roiClearDiff {
		return voteDisagree
	}
	return voteUnknown
}

// evalKeypoints converts the keypoint signal into a vote. Too few keypoints to
// judge → unknown (never a guess on a featureless ROI).
func evalKeypoints(in roomInputs) signalVote {
	if !in.keypointsOn {
		return voteUnknown
	}
	const minPopulation = 6 // need at least this many keypoints to judge at all
	if in.keypointPop < minPopulation {
		return voteUnknown
	}
	if in.keypointInliers >= in.minInliers {
		return voteAgree
	}
	if in.keypointInliers == 0 {
		return voteDisagree
	}
	return voteUnknown
}

// computeRoomCall applies the §2.3 signal-combination table. The two STRONG
// signals are ROI-aHash and keypoints; whole-frame aHash is a weak tie-breaker
// only and never alone produces a conclusion.
func computeRoomCall(in roomInputs) roomResult {
	roi := evalROI(in)
	kp := evalKeypoints(in)

	res := roomResult{roiVote: roi, keypointVote: kp}

	agree := 0
	disagree := 0
	if roi == voteAgree {
		agree++
		res.signalsUsed = append(res.signalsUsed, "roi_ahash")
	} else if roi == voteDisagree {
		disagree++
		res.signalsUsed = append(res.signalsUsed, "roi_ahash")
	}
	if kp == voteAgree {
		agree++
		res.signalsUsed = append(res.signalsUsed, "keypoints")
	} else if kp == voteDisagree {
		disagree++
		res.signalsUsed = append(res.signalsUsed, "keypoints")
	}

	switch {
	case agree >= 2:
		res.call = callSameRoom
	case disagree >= 2:
		res.call = callDifferentRoom
	case agree == 1 && disagree == 0:
		// Exactly one strong signal agrees → candidate (the ≥2-signal invariant:
		// a lone signal, even a strong one, can never conclude same_room).
		res.call = callCandidate
	case disagree == 1 && agree == 0:
		// One strong signal disagrees, the other unknown → candidate for review,
		// not a different-room conclusion (need ≥2 to conclude either way).
		res.call = callCandidate
	case agree == 1 && disagree == 1:
		// Conflict between the two strong signals → candidate (human resolves).
		res.call = callCandidate
	default:
		res.call = callUnknown
	}

	res.confidence = confidenceOf(in, res)
	return res
}

// confidenceOf derives a [0,1] readability score from how far inside their
// thresholds the agreeing signals sit (NOT a probability). A conclusion scores
// higher than a candidate; an unknown is low.
func confidenceOf(in roomInputs, res roomResult) float64 {
	switch res.call {
	case callSameRoom:
		// Average the two agreeing margins, mapped to [0.6,1.0].
		roiM := margin(in.roiThreshold-in.roiHamming, in.roiThreshold+1)
		kpM := margin(in.keypointInliers-in.minInliers, in.minInliers+1)
		m := (roiM + kpM) / 2
		return round3(0.6 + 0.4*m)
	case callDifferentRoom:
		return round3(0.7)
	case callCandidate:
		// One agreeing signal's margin maps into [0.3,0.6).
		var m float64
		if res.roiVote == voteAgree {
			m = margin(in.roiThreshold-in.roiHamming, in.roiThreshold+1)
		} else if res.keypointVote == voteAgree {
			m = margin(in.keypointInliers-in.minInliers, in.minInliers+1)
		}
		return round3(0.3 + 0.3*m)
	default:
		return 0.0
	}
}

// margin maps a non-negative slack over a span into [0,1].
func margin(slack, span int) float64 {
	if span <= 0 {
		return 0
	}
	if slack < 0 {
		slack = 0
	}
	m := float64(slack) / float64(span)
	if m > 1 {
		m = 1
	}
	return m
}

// roomCallClass maps a room call to a CSS class suffix (defaults to unknown for
// any unexpected value so the exhibit always styles cleanly).
func roomCallClass(call string) string {
	switch call {
	case callSameRoom, callDifferentRoom, callCandidate:
		return call
	default:
		return callUnknown
	}
}

// roomCallPhrase renders the call in WORDS with the signal count, for the exhibit
// (per ACCESSIBILITY.md: state the call in words, never color/symbol alone).
func roomCallPhrase(res roomResult) string {
	switch res.call {
	case callSameRoom:
		return "SAME ROOM — 2 signals agree"
	case callDifferentRoom:
		return "DIFFERENT ROOM — signals disagree"
	case callCandidate:
		n := len(res.signalsUsed)
		if n == 1 {
			return "CANDIDATE — 1 signal only (needs a second to confirm)"
		}
		return "CANDIDATE — signals conflict (human review)"
	default:
		return "UNKNOWN — not enough signal to call"
	}
}

// whatToLookForCall builds the reviewer hint naming WHICH signals fired and the
// ROI used, so the human eye is pointed at the corroborating structure.
func whatToLookForCall(res roomResult, in roomInputs, roiSpec string) string {
	switch res.call {
	case callSameRoom:
		return "SAME ROOM: the " + roiSpec + " hash matches AND static-decor features line up — " +
			"compare the vent, trim, and wall/ceiling corners to confirm it is the same room."
	case callDifferentRoom:
		return "DIFFERENT ROOM: the background structure disagrees — the ceiling/wall pattern and " +
			"decor do not line up; treat overall light/composition similarity as coincidental."
	case callCandidate:
		if len(res.signalsUsed) == 1 && res.roiVote == voteAgree {
			return "CANDIDATE: only the " + roiSpec + " hash matched — find a SPECIFIC shared fixture " +
				"(a vent, outlet, mark, or trim line) before treating this as the same room."
		}
		if len(res.signalsUsed) == 1 && res.keypointVote == voteAgree {
			return "CANDIDATE: only static-decor features matched — confirm the wall/ceiling band " +
				"matches too before concluding the same room."
		}
		return "CANDIDATE: the signals conflict (one says same, one says different) — a human must " +
			"compare the fixed structure directly to resolve it."
	default:
		return "UNKNOWN: not enough background signal to judge (e.g. a blank/featureless ROI) — " +
			"reframe or pick a different ROI band, then re-run."
	}
}
