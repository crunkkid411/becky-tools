package main

import (
	"fmt"
	"sort"
	"strings"

	"becky-go/internal/beckydb"
	"becky-go/internal/beckyio"
)

// Modalities tracked in coverage, in display order. Kept as a constant so the
// report is deterministic regardless of what the DB happens to contain.
var modalities = []string{"voice", "face", "location"}

// gapFraction is the recognition threshold below which an entity is flagged as a
// coverage gap: recognized in fewer than this fraction of the corpus videos.
// Jordan's framing: "recognized in half the videos ... means we have a problem."
const gapFraction = 0.5

// voiceThresholdHint is the becky-identify voice threshold echoed in gap
// suggestions ("Consider lowering voice threshold (current: 0.5)"). It mirrors
// becky-identify's --voice-threshold default; this tool does not change it.
const voiceThresholdHint = 0.5

// propagationVerifier is recorded as verified_by on rows this tool auto-confirms,
// so propagated identifications are auditable as machine-applied (not human).
const propagationVerifier = "becky-consolidate"

// Report is the becky-consolidate stdout JSON contract (default report mode).
type Report struct {
	DB          string      `json:"db"`
	Threshold   float64     `json:"threshold"`
	DryRun      bool        `json:"dry_run"`
	TotalVideos int         `json:"total_videos"`
	Entities    []EntityCov `json:"entities"`
	Gaps        []Gap       `json:"gaps"`
	Propagation Propagation `json:"propagation"`
}

// EntityCov is one entity's recognition coverage across the corpus: distinct
// videos it was recognized in overall, plus a per-modality breakdown.
type EntityCov struct {
	Name        string             `json:"name"`
	Recognized  int                `json:"recognized"` // distinct videos recognized in (any modality)
	TotalVideos int                `json:"total_videos"`
	Percent     float64            `json:"percent"`         // recognized/total * 100
	Modalities  map[string]ModeCov `json:"modalities"`      // voice/face/location
	Confirmed   int                `json:"confirmed_ids"`   // confirmed identification rows
	Unconfirmed int                `json:"unconfirmed_ids"` // unconfirmed identification rows
}

// ModeCov is one modality's coverage for an entity (e.g. voice 45/92).
type ModeCov struct {
	Videos      int     `json:"videos"` // distinct videos recognized via this modality
	TotalVideos int     `json:"total_videos"`
	Percent     float64 `json:"percent"`
}

// Gap is one flagged coverage gap with deterministic, templated suggestions.
type Gap struct {
	Entity        string   `json:"entity"`
	NotRecognized int      `json:"not_recognized"` // videos with no recognition of this entity
	TotalVideos   int      `json:"total_videos"`
	Suggestions   []string `json:"suggestions"`
}

// Propagation summarizes the name-propagation pass.
type Propagation struct {
	Propagated int          `json:"propagated"` // unconfirmed rows that received a confirmed name
	Skipped    int          `json:"skipped"`    // unconfirmed rows below --threshold
	Details    []PropDetail `json:"details"`
}

// PropDetail records one propagation decision (applied or skipped).
type PropDetail struct {
	ID         string  `json:"id"`
	Entity     string  `json:"entity"`
	SourceFile string  `json:"source_file"`
	Modality   string  `json:"modality"`
	SpeakerID  string  `json:"speaker_id,omitempty"`
	Confidence float64 `json:"confidence"`
	Action     string  `json:"action"`           // "propagated" | "skipped"
	Reason     string  `json:"reason,omitempty"` // why skipped
	VerifiedBy string  `json:"verified_by,omitempty"`
}

// buildReport reads all identifications, runs propagation (unless --dry-run),
// then computes coverage + gaps over the FINAL state so the report reflects what
// the DB now holds. Order matters: propagation may newly confirm rows, which
// raises coverage — the report should show the post-propagation picture.
func buildReport(db *beckydb.DB, dbPath string, threshold float64, dryRun, verbose bool) (Report, error) {
	videos, err := db.DistinctSourceFiles()
	if err != nil {
		return Report{}, fmt.Errorf("list corpus videos: %w", err)
	}
	total := len(videos)
	beckyio.Logf(verbose, "corpus: %d distinct video(s)", total)

	ids, err := db.ListIdentifications()
	if err != nil {
		return Report{}, fmt.Errorf("list identifications: %w", err)
	}
	beckyio.Logf(verbose, "identifications: %d row(s)", len(ids))

	// Propagation pass. In dry-run we compute the plan but do not write; the
	// returned ids slice is updated in memory either way so coverage reflects it.
	prop, ids := propagate(db, ids, threshold, dryRun, verbose)

	report := Report{
		DB:          dbPath,
		Threshold:   threshold,
		DryRun:      dryRun,
		TotalVideos: total,
		Entities:    coverage(ids, total),
		Propagation: prop,
	}
	report.Gaps = gaps(report.Entities, total)
	return report, nil
}

// propagate carries each entity's CONFIRMED name onto that entity's OTHER
// unconfirmed identification rows whose confidence clears the threshold. Only
// entities that already have at least one confirmation are touched, so no name
// is ever invented. Returns the propagation summary and the in-memory-updated
// identifications (so coverage reflects the result, even under --dry-run).
func propagate(db *beckydb.DB, ids []beckydb.Identification, threshold float64, dryRun, verbose bool) (Propagation, []beckydb.Identification) {
	confirmedEntities := confirmedEntitySet(ids)
	prop := Propagation{Details: []PropDetail{}}

	// Work on a copy so the caller's slice is not mutated in place (immutability
	// rule); we return the updated copy.
	out := make([]beckydb.Identification, len(ids))
	copy(out, ids)

	for i := range out {
		row := out[i]
		if row.Confirmed() {
			continue // already confirmed; nothing to propagate onto it
		}
		name := strings.TrimSpace(row.EntityName)
		if name == "" || !confirmedEntities[name] {
			continue // no confirmation exists for this entity -> never propagate
		}
		detail := PropDetail{
			ID:         row.ID,
			Entity:     name,
			SourceFile: row.SourceFile,
			Modality:   row.Modality,
			SpeakerID:  row.SpeakerID,
			Confidence: row.Confidence,
		}
		if row.Confidence < threshold {
			detail.Action = "skipped"
			detail.Reason = fmt.Sprintf("confidence %.4f < threshold %.2f", row.Confidence, threshold)
			prop.Skipped++
			prop.Details = append(prop.Details, detail)
			beckyio.Logf(verbose, "skip %s: %s", row.ID, detail.Reason)
			continue
		}

		// Above threshold: propagate the confirmed name.
		detail.Action = "propagated"
		detail.VerifiedBy = propagationVerifier
		if !dryRun {
			if err := db.SetIdentificationVerified(row.ID, name, propagationVerifier); err != nil {
				// A write failure is reported as a skip with the reason, not a crash.
				detail.Action = "skipped"
				detail.Reason = "write failed: " + err.Error()
				detail.VerifiedBy = ""
				prop.Skipped++
				prop.Details = append(prop.Details, detail)
				beckyio.Logf(true, "warning: propagate write failed for %s: %v", row.ID, err)
				continue
			}
			out[i].VerifiedBy = propagationVerifier // reflect the write in coverage
		}
		prop.Propagated++
		prop.Details = append(prop.Details, detail)
		beckyio.Logf(verbose, "propagate %s -> %q (conf %.4f%s)", row.ID, name, row.Confidence, dryRunTag(dryRun))
	}
	return prop, out
}

// confirmedEntitySet returns the set of entity names that have at least one
// confirmed identification — the only entities propagation is allowed to spread.
func confirmedEntitySet(ids []beckydb.Identification) map[string]bool {
	set := map[string]bool{}
	for _, id := range ids {
		if id.Confirmed() {
			if name := strings.TrimSpace(id.EntityName); name != "" {
				set[name] = true
			}
		}
	}
	return set
}

// coverage computes per-entity recognition coverage over the corpus. An entity
// is "recognized in" a video if it has any identification row for that video;
// per-modality coverage counts distinct videos per voice/face/location.
func coverage(ids []beckydb.Identification, total int) []EntityCov {
	// entity -> set of videos (overall) and entity -> modality -> set of videos.
	overall := map[string]map[string]bool{}
	perMode := map[string]map[string]map[string]bool{}
	confirmed := map[string]int{}
	unconfirmed := map[string]int{}

	for _, id := range ids {
		name := strings.TrimSpace(id.EntityName)
		if name == "" {
			continue
		}
		if overall[name] == nil {
			overall[name] = map[string]bool{}
			perMode[name] = map[string]map[string]bool{}
		}
		overall[name][id.SourceFile] = true
		if perMode[name][id.Modality] == nil {
			perMode[name][id.Modality] = map[string]bool{}
		}
		perMode[name][id.Modality][id.SourceFile] = true
		if id.Confirmed() {
			confirmed[name]++
		} else {
			unconfirmed[name]++
		}
	}

	names := make([]string, 0, len(overall))
	for name := range overall {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]EntityCov, 0, len(names))
	for _, name := range names {
		ec := EntityCov{
			Name:        name,
			Recognized:  len(overall[name]),
			TotalVideos: total,
			Percent:     pct(len(overall[name]), total),
			Modalities:  map[string]ModeCov{},
			Confirmed:   confirmed[name],
			Unconfirmed: unconfirmed[name],
		}
		for _, m := range modalities {
			videos := len(perMode[name][m])
			ec.Modalities[m] = ModeCov{
				Videos:      videos,
				TotalVideos: total,
				Percent:     pct(videos, total),
			}
		}
		out = append(out, ec)
	}
	return out
}

// gaps flags entities recognized in fewer than gapFraction of the corpus videos,
// attaching deterministic, templated suggestions (no LLM). Entities at high
// coverage produce no gap.
func gaps(entities []EntityCov, total int) []Gap {
	out := []Gap{}
	if total == 0 {
		return out
	}
	for _, e := range entities {
		frac := float64(e.Recognized) / float64(total)
		if frac >= gapFraction {
			continue
		}
		missing := total - e.Recognized
		out = append(out, Gap{
			Entity:        e.Name,
			NotRecognized: missing,
			TotalVideos:   total,
			Suggestions:   suggestionsFor(e, missing),
		})
	}
	return out
}

// suggestionsFor builds the templated remediation hints for a gap. The hints are
// deterministic and based purely on which modalities are weak.
func suggestionsFor(e EntityCov, missing int) []string {
	s := []string{
		fmt.Sprintf("Not recognized in %d of %d videos", missing, e.TotalVideos),
	}
	if e.Modalities["voice"].Videos < e.TotalVideos {
		s = append(s, fmt.Sprintf("Consider lowering voice threshold (current: %.1f)", voiceThresholdHint))
		s = append(s, "Consider enrolling more reference voice clips")
	}
	if e.Modalities["face"].Videos == 0 {
		s = append(s, "No face coverage: enroll face prints (ArcFace model required)")
	}
	if e.Modalities["location"].Videos == 0 {
		s = append(s, "No location coverage: enroll reference location frames")
	}
	return s
}

// pct returns n/total as a percentage rounded to one decimal, 0 when total is 0.
func pct(n, total int) float64 {
	if total <= 0 {
		return 0
	}
	return round1(float64(n) / float64(total) * 100)
}

// round1 rounds to one decimal place for stable, readable percentages.
func round1(f float64) float64 {
	return float64(int64(f*10+0.5)) / 10
}

// dryRunTag annotates a verbose propagate line when no write happened.
func dryRunTag(dryRun bool) string {
	if dryRun {
		return ", dry-run"
	}
	return ""
}
