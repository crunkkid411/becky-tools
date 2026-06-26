// Package forensic is the SINGLE place that maps becky tools' real JSON outputs into
// orchestrate Claims/Signals. Every self-regulating entry tool (becky-resolve, becky-presence,
// becky-case) maps through here, so the protocol is applied identically everywhere — there is no
// second, drifting copy of "how an identify hit becomes a claim". Pure + unit-tested; no I/O.
package forensic

import (
	"bytes"
	"encoding/json"
	"strings"

	"becky-go/internal/orchestrate"
)

// ---- becky-identify contract (subset; matches cmd/identify Output) ----

type idIdentification struct {
	Type           string   `json:"type"`
	Name           string   `json:"name"`
	Confidence     float64  `json:"confidence"`
	CorroboratedBy []string `json:"corroborated_by"`
}

type idUnidentified struct {
	Candidate           string  `json:"candidate"`
	CandidateConfidence float64 `json:"candidate_confidence"`
}

type idOutput struct {
	Identifications []idIdentification `json:"identifications"`
	Unidentified    []idUnidentified   `json:"unidentified"`
}

// IdentifyToClaims maps becky-identify JSON into naming claims: a "corroborated" identification
// carries one signal per agreeing modality (distinct sources → can conclude); a single-modality
// match or a demoted candidate carries ONE signal (a candidate that must be escalated to be named).
func IdentifyToClaims(raw []byte) []orchestrate.Claim {
	var o idOutput
	if json.Unmarshal(bytes.TrimSpace(raw), &o) != nil {
		return nil
	}
	var cs []orchestrate.Claim
	for _, id := range o.Identifications {
		if id.Name == "" {
			continue
		}
		var sigs []orchestrate.Signal
		if id.Type == "corroborated" && len(id.CorroboratedBy) > 0 {
			for _, m := range id.CorroboratedBy {
				sigs = append(sigs, orchestrate.Signal{Source: "identify/" + m, Kind: orchestrate.KindPrint, Confidence: id.Confidence})
			}
		} else {
			sigs = append(sigs, orchestrate.Signal{Source: "identify/" + id.Type, Kind: orchestrate.KindPrint, Confidence: id.Confidence})
		}
		cs = append(cs, orchestrate.Claim{Key: "person=" + id.Name, Signals: sigs})
	}
	for _, u := range o.Unidentified {
		if u.Candidate == "" {
			continue
		}
		cs = append(cs, orchestrate.Claim{Key: "person=" + u.Candidate, Signals: []orchestrate.Signal{
			{Source: "identify/candidate", Kind: orchestrate.KindPrint, Confidence: u.CandidateConfidence},
		}})
	}
	return cs
}

// ---- becky-transcribe / becky-motion / becky-validate contracts (subsets) ----

type transcribeDoc struct {
	Segments []struct {
		Start float64 `json:"start"`
		End   float64 `json:"end"`
		Text  string  `json:"text"`
	} `json:"segments"`
}

type motionDoc struct {
	Bursts []struct {
		WindowStart float64 `json:"window_start"`
		WindowEnd   float64 `json:"window_end"`
	} `json:"motion_bursts"`
}

type validateDoc struct {
	Observations []struct {
		SegmentStart float64 `json:"segment_start"`
		SegmentEnd   float64 `json:"segment_end"`
		Visual       string  `json:"visual"`
		Finding      string  `json:"finding"`
		Content      string  `json:"content"`
		Confidence   float64 `json:"confidence"`
	} `json:"observations"`
}

// PresenceSignals maps transcribe + motion + validate JSON into TimedSignals for a subject.
// Subject match is a deterministic case-insensitive substring (no model): a transcript mention,
// or a validate observation whose text NAMES the subject (only then is it a WATCH of the subject).
// Motion bursts are subject-agnostic candidate moments.
func PresenceSignals(subject string, transcribe, motion, validate []byte) []orchestrate.TimedSignal {
	subj := strings.ToLower(strings.TrimSpace(subject))
	var tr transcribeDoc
	var mo motionDoc
	var va validateDoc
	_ = json.Unmarshal(bytes.TrimSpace(transcribe), &tr)
	_ = json.Unmarshal(bytes.TrimSpace(motion), &mo)
	_ = json.Unmarshal(bytes.TrimSpace(validate), &va)

	var sigs []orchestrate.TimedSignal
	for _, s := range tr.Segments {
		if subj != "" && strings.Contains(strings.ToLower(s.Text), subj) {
			sigs = append(sigs, orchestrate.TimedSignal{Source: "becky-transcribe", Kind: orchestrate.KindMention, Confidence: 0.9, Start: s.Start, End: s.End})
		}
	}
	for _, b := range mo.Bursts {
		sigs = append(sigs, orchestrate.TimedSignal{Source: "becky-motion", Kind: orchestrate.KindMotion, Confidence: 0.7, Start: b.WindowStart, End: b.WindowEnd})
	}
	for _, o := range va.Observations {
		text := strings.ToLower(o.Visual + " " + o.Finding + " " + o.Content)
		if subj != "" && strings.Contains(text, subj) {
			conf := o.Confidence
			if conf <= 0 {
				conf = 0.6
			}
			sigs = append(sigs, orchestrate.TimedSignal{Source: "becky-validate", Kind: orchestrate.KindWatched, Confidence: conf, Start: o.SegmentStart, End: o.SegmentEnd})
		}
	}
	return sigs
}
