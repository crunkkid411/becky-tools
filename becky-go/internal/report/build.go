package report

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// concludedThreshold is the minimum corroborated_by length to call an
// identification DOCUMENTED. Mirrors the "≥2 independent signals" rule from
// FORENSIC-OUTPUT-PHILOSOPHY.md.
const concludedThreshold = 2

// concludedHighConf is the minimum confidence for a single-signal identification
// to still be called DOCUMENTED. A 0.92 voice match with no other signal is
// still conclusory enough to name.
const concludedHighConf = 0.90

// Build assembles the Report from parsed sidecar data. All logic is deterministic:
// the same sidecars always produce the same Report.
func Build(s Sidecars, sourceName string) Report {
	r := Report{
		Source:      sourceName,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}

	// Determine clip duration: prefer identify/events/transcript sources.
	r.Duration = clipDuration(s)

	// Signals summary.
	r.Signals = buildSignals(s)

	// Entities (from identify sidecar) — apply the corroboration rule.
	var notes []string
	r.Entities, notes = buildEntities(s)
	r.Notes = append(r.Notes, notes...)

	// Merged chronological timeline.
	r.Timeline = buildTimeline(s, r.Entities)

	// Conclusions and review items.
	r.Conclusions, r.ReviewItems = buildFindings(s, r.Entities)

	// Overall degraded flag: true when no sidecar contributed any useful data.
	useful := len(r.Timeline) + len(r.Entities) + len(r.Conclusions) + len(r.ReviewItems)
	if useful == 0 {
		r.Degraded = true
		r.Notes = append(r.Notes, "no forensic data found — all sidecars empty or missing")
	}

	return r
}

// clipDuration returns the best available clip length in seconds.
func clipDuration(s Sidecars) float64 {
	if s.Identify != nil {
		// identify doesn't carry duration; fall through
	}
	if s.Events != nil && s.Events.Duration > 0 {
		return s.Events.Duration
	}
	if s.Transcript != nil && s.Transcript.Duration > 0 {
		return s.Transcript.Duration
	}
	if s.Motion != nil && s.Motion.DurationSec > 0 {
		return s.Motion.DurationSec
	}
	return 0
}

// buildSignals fills the per-tool contribution summary.
func buildSignals(s Sidecars) SignalSummary {
	var sig SignalSummary
	if s.Transcript != nil {
		sig.Transcript = &TranscriptSig{
			Present:      true,
			SegmentCount: len(s.Transcript.Segments),
			Duration:     s.Transcript.Duration,
			Model:        s.Transcript.Model,
		}
	}
	if s.Events != nil {
		sig.Events = &EventsSig{
			Present:    true,
			EventCount: len(s.Events.Events),
		}
	}
	if s.Identify != nil {
		sig.Identify = &IdentifySig{
			Present:           true,
			IdentifiedCount:   len(s.Identify.Identifications),
			UnidentifiedCount: len(s.Identify.Unidentified),
		}
	}
	if s.Motion != nil {
		sub := 0
		for _, b := range s.Motion.MotionBursts {
			if b.SubSecond {
				sub++
			}
		}
		sig.Motion = &MotionSig{
			Present:        true,
			BurstCount:     s.Motion.BurstCount,
			SubSecondCount: sub,
		}
	}
	return sig
}

// buildEntities converts becky-identify's identifications into report Entities
// and applies the corroboration rule. Unidentified speakers/faces are included
// as CANDIDATE entities when they have a named candidate.
func buildEntities(s Sidecars) ([]Entity, []string) {
	if s.Identify == nil {
		return nil, nil
	}
	var entities []Entity
	var notes []string

	for _, id := range s.Identify.Identifications {
		ent := Entity{
			Name:              id.Name,
			Type:              id.Type,
			Confidence:        id.Confidence,
			CorroboratedBy:    id.CorroboratedBy,
			CorroboratedCount: len(id.CorroboratedBy),
			SpeakerID:         id.SpeakerID,
		}
		ent.Concluded, ent.Tag = applyCorroboration(id.Confidence, id.CorroboratedBy)

		// Appearances: build spans from voice segments and face frame timestamps.
		for _, seg := range id.Segments {
			ent.Appearances = append(ent.Appearances, Span{Start: seg.Start, End: seg.End})
		}
		for _, fr := range id.Frames {
			// Frame appearances are point-in-time; represent as a zero-duration span.
			ent.Appearances = append(ent.Appearances, Span{Start: fr.Timestamp, End: fr.Timestamp})
		}
		sortSpans(ent.Appearances)
		entities = append(entities, ent)
	}

	// Include near-miss unidentified entries as CANDIDATE entities when a candidate name is known.
	for _, u := range s.Identify.Unidentified {
		if u.Candidate == "" {
			continue
		}
		ent := Entity{
			Name:              u.Candidate,
			Type:              u.Type,
			Confidence:        u.Confidence,
			CorroboratedBy:    nil,
			CorroboratedCount: 0,
			Concluded:         false,
			Tag:               "CANDIDATE",
			SpeakerID:         u.SpeakerID,
		}
		entities = append(entities, ent)
		notes = append(notes, fmt.Sprintf(
			"%s flagged as unidentified (candidate %q, confidence %.2f) — needs human review",
			u.SpeakerID, u.Candidate, u.Confidence,
		))
	}

	// Sort: concluded (DOCUMENTED) before candidates.
	sort.SliceStable(entities, func(i, j int) bool {
		if entities[i].Concluded != entities[j].Concluded {
			return entities[i].Concluded // concluded first
		}
		return entities[i].Confidence > entities[j].Confidence
	})
	return entities, notes
}

// applyCorroboration decides the tag and concluded status for one identification.
// FORENSIC-OUTPUT-PHILOSOPHY.md rule: ≥2 independent signals → DOCUMENTED.
// A single signal at very high confidence (≥concludedHighConf) is also DOCUMENTED.
func applyCorroboration(confidence float64, corroboratedBy []string) (concluded bool, tag string) {
	n := len(corroboratedBy)
	switch {
	case n >= concludedThreshold:
		return true, "DOCUMENTED"
	case n >= 1 && confidence >= concludedHighConf:
		return true, "DOCUMENTED"
	default:
		return false, "CANDIDATE"
	}
}

// buildTimeline merges observations from all tools into a single chronological
// list of Moments.
func buildTimeline(s Sidecars, entities []Entity) []Moment {
	// Build a speaker-ID → name lookup so we can resolve speaker IDs.
	speakerName := makeSpeakerLookup(entities)

	var moments []Moment

	// Transcript: each segment is a "speech" moment.
	if s.Transcript != nil {
		for _, seg := range s.Transcript.Segments {
			if seg.Text == "" {
				continue
			}
			tag := "DOCUMENTED"
			if seg.LowConfidence {
				tag = "ANALYSIS"
			}
			moments = append(moments, Moment{
				Time:        seg.Start,
				End:         seg.End,
				Type:        "speech",
				Source:      "transcript",
				Description: seg.Text,
				Tag:         tag,
			})
		}
	}

	// Events: each event becomes a Moment, with the speaker resolved where possible.
	if s.Events != nil {
		for _, ev := range s.Events.Events {
			name := speakerName[ev.SpeakerID]
			if name == "" {
				name = ev.SpeakerID
			}
			tag := eventTag(ev.Confidence)
			moments = append(moments, Moment{
				Time:        ev.Start,
				End:         ev.End,
				Type:        "event",
				Source:      "events",
				Description: ev.Description,
				Confidence:  ev.Confidence,
				Tag:         tag,
				Speaker:     name,
			})
		}
	}

	// Identify: each identification's earliest appearance becomes a moment.
	if s.Identify != nil {
		for _, id := range s.Identify.Identifications {
			start := earliestSpanStart(id.Segments, id.Frames)
			if start < 0 {
				continue
			}
			_, tag := applyCorroboration(id.Confidence, id.CorroboratedBy)
			desc := fmt.Sprintf("%s identified via %s (confidence %.2f)",
				id.Name, corrobDesc(id.CorroboratedBy, id.Type), id.Confidence)
			moments = append(moments, Moment{
				Time:        start,
				Type:        "identification",
				Source:      "identify",
				Description: desc,
				Confidence:  id.Confidence,
				Tag:         tag,
				Speaker:     id.Name,
			})
		}
	}

	// Motion: each burst is a Moment; flag sub-second ones prominently.
	if s.Motion != nil {
		for _, b := range s.Motion.MotionBursts {
			tag := "ANALYSIS"
			desc := fmt.Sprintf("motion burst (score %.2f)", b.MotionScore)
			if b.SubSecond {
				desc = fmt.Sprintf("sub-second motion burst — not visible at 1 fps (score %.2f, duration %.2fs) — route to becky-validate", b.MotionScore, b.WindowEnd-b.WindowStart)
				tag = "CANDIDATE"
			}
			moments = append(moments, Moment{
				Time:        b.WindowStart,
				End:         b.WindowEnd,
				Type:        "motion_burst",
				Source:      "motion",
				Description: desc,
				Confidence:  b.MotionScore,
				Tag:         tag,
				SubSecond:   b.SubSecond,
			})
		}
	}

	// Sort by time, then by source priority (identify > events > transcript > motion)
	// so same-timestamp events appear in a useful order.
	sort.SliceStable(moments, func(i, j int) bool {
		if moments[i].Time != moments[j].Time {
			return moments[i].Time < moments[j].Time
		}
		return sourcePriority(moments[i].Source) > sourcePriority(moments[j].Source)
	})
	return moments
}

func sourcePriority(src string) int {
	switch src {
	case "identify":
		return 4
	case "events":
		return 3
	case "transcript":
		return 2
	case "motion":
		return 1
	}
	return 0
}

// buildFindings separates the timeline into conclusions (DOCUMENTED, high
// confidence, corroborated) and review items (everything else).
func buildFindings(s Sidecars, entities []Entity) (conclusions, reviewItems []Finding) {
	// Entity-level findings: one finding per entity.
	for _, ent := range entities {
		first, last := entityTimeRange(ent.Appearances)
		f := Finding{
			What:       entityFindingDesc(ent),
			When:       formatTimeRange(first, last),
			WhenSec:    first,
			Confidence: ent.Confidence,
			Sources:    entitySources(ent),
			Tag:        ent.Tag,
		}
		if ent.Concluded {
			conclusions = append(conclusions, f)
		} else {
			reviewItems = append(reviewItems, f)
		}
	}

	// Event-level findings: high-confidence events → conclusions, low → review.
	if s.Events != nil {
		for _, ev := range s.Events.Events {
			tag := eventTag(ev.Confidence)
			f := Finding{
				What:       ev.Description,
				When:       formatTime(ev.Start),
				WhenSec:    ev.Start,
				Confidence: ev.Confidence,
				Sources:    []string{"events"},
				Tag:        tag,
			}
			if tag == "DOCUMENTED" {
				conclusions = append(conclusions, f)
			} else {
				reviewItems = append(reviewItems, f)
			}
		}
	}

	// Motion bursts that recommend review → review items.
	if s.Motion != nil {
		for _, b := range s.Motion.MotionBursts {
			if !b.RecommendReview && !b.SubSecond {
				continue
			}
			desc := fmt.Sprintf("motion burst at %.1fs (score %.2f)", b.PeakTime, b.MotionScore)
			if b.SubSecond {
				desc = fmt.Sprintf("sub-second motion burst at %.1fs — missed by 1-fps sampling (score %.2f)", b.PeakTime, b.MotionScore)
			}
			reviewItems = append(reviewItems, Finding{
				What:       desc,
				When:       formatTimeRange(b.WindowStart, b.WindowEnd),
				WhenSec:    b.WindowStart,
				Confidence: b.MotionScore,
				Sources:    []string{"motion"},
				Tag:        "CANDIDATE",
			})
		}
	}

	sortFindings(conclusions)
	sortFindings(reviewItems)
	return conclusions, reviewItems
}

// --- helpers ---

func makeSpeakerLookup(entities []Entity) map[string]string {
	m := make(map[string]string)
	for _, e := range entities {
		if e.SpeakerID != "" && e.Name != "" && e.Concluded {
			m[e.SpeakerID] = e.Name
		}
	}
	return m
}

func eventTag(confidence float64) string {
	if confidence >= 0.8 {
		return "DOCUMENTED"
	}
	return "ANALYSIS"
}

func earliestSpanStart(segs []identifySpan, frames []identifyFrame) float64 {
	best := math.MaxFloat64
	for _, s := range segs {
		if s.Start < best {
			best = s.Start
		}
	}
	for _, f := range frames {
		if f.Timestamp < best {
			best = f.Timestamp
		}
	}
	if best == math.MaxFloat64 {
		return -1
	}
	return best
}

func corrobDesc(corroboratedBy []string, typ string) string {
	if len(corroboratedBy) > 0 {
		return strings.Join(corroboratedBy, "+")
	}
	return typ
}

func entitySources(ent Entity) []string {
	srcs := []string{"identify"}
	for _, c := range ent.CorroboratedBy {
		srcs = append(srcs, c)
	}
	return srcs
}

func entityFindingDesc(ent Entity) string {
	if len(ent.CorroboratedBy) >= 2 {
		return fmt.Sprintf("%s (corroborated by %s, confidence %.2f)",
			ent.Name, strings.Join(ent.CorroboratedBy, "+"), ent.Confidence)
	}
	if ent.Concluded {
		return fmt.Sprintf("%s (confidence %.2f)", ent.Name, ent.Confidence)
	}
	return fmt.Sprintf("%s — candidate only, single-signal (confidence %.2f)", ent.Name, ent.Confidence)
}

func entityTimeRange(appearances []Span) (first, last float64) {
	if len(appearances) == 0 {
		return 0, 0
	}
	first = appearances[0].Start
	last = appearances[len(appearances)-1].End
	return first, last
}

func sortSpans(spans []Span) {
	sort.Slice(spans, func(i, j int) bool { return spans[i].Start < spans[j].Start })
}

func sortFindings(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		if math.Abs(findings[i].WhenSec-findings[j].WhenSec) < 0.001 {
			return findings[i].Confidence > findings[j].Confidence
		}
		return findings[i].WhenSec < findings[j].WhenSec
	})
}

func formatTime(sec float64) string {
	if sec < 0 {
		return "unknown"
	}
	m := int(sec) / 60
	s := int(sec) % 60
	return fmt.Sprintf("%d:%02d", m, s)
}

func formatTimeRange(start, end float64) string {
	if start <= 0 && end <= 0 {
		return "unknown"
	}
	if end <= start || end-start < 0.1 {
		return formatTime(start)
	}
	return fmt.Sprintf("%s–%s", formatTime(start), formatTime(end))
}
