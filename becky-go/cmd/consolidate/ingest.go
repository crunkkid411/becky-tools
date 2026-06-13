package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"becky-go/internal/beckydb"
	"becky-go/internal/beckyio"
)

// identifyOutput mirrors the becky-identify JSON contract (05-becky-identify.md):
// a list of named identifications[] plus an unidentified[] list. We ingest the
// named identifications[]; unidentified[] entries have no name to track and are
// not stored as identification rows.
type identifyOutput struct {
	File            string            `json:"file"`
	Identifications []identifyEntry   `json:"identifications"`
	Unidentified    []unidentifiedHit `json:"unidentified"`
	Notes           map[string]string `json:"notes"`
}

// identifyEntry is one named identification from becky-identify.
type identifyEntry struct {
	Type       string  `json:"type"` // voice | face | location
	SpeakerID  string  `json:"speaker_id"`
	Name       string  `json:"name"`
	Confidence float64 `json:"confidence"`
	Match      string  `json:"match"`
}

// unidentifiedHit is one detected-but-unmatched speaker (no name to propagate).
type unidentifiedHit struct {
	Type       string  `json:"type"`
	SpeakerID  string  `json:"speaker_id"`
	Confidence float64 `json:"confidence"`
}

// ingestResult is the --ingest stdout JSON contract: a small, valid summary so
// the bridge step is scriptable and chainable like every other becky tool.
type ingestResult struct {
	Mode       string `json:"mode"`
	Source     string `json:"source"`
	VerifiedBy string `json:"verified_by"` // "" when unconfirmed
	Ingested   int    `json:"ingested"`    // identification rows written
	Skipped    int    `json:"skipped"`     // entries without a usable name
	Confirmed  bool   `json:"confirmed"`   // true when --verified-by was supplied
}

// runIngest loads a becky-identify JSON and writes each named identification into
// the shared identifications table. source overrides the JSON's "file" label for
// the corpus key; verifiedBy (when non-empty) marks rows confirmed. It is
// idempotent: deterministic IDs mean re-ingesting the same run replaces rows.
func runIngest(db *beckydb.DB, ingestPath, source, verifiedBy string, verbose bool) error {
	if !fileExists(ingestPath) {
		return fmt.Errorf("--ingest file not found: %s", ingestPath)
	}
	raw, err := os.ReadFile(ingestPath)
	if err != nil {
		return fmt.Errorf("read --ingest file: %w", err)
	}
	var in identifyOutput
	if err := json.Unmarshal(raw, &in); err != nil {
		return fmt.Errorf("parse becky-identify JSON %s: %w", ingestPath, err)
	}

	// The corpus key for this run: explicit --source wins, else the JSON's file.
	srcLabel := strings.TrimSpace(source)
	if srcLabel == "" {
		srcLabel = strings.TrimSpace(in.File)
	}
	if srcLabel == "" {
		return fmt.Errorf("no source: pass --source <video> (the JSON has no \"file\")")
	}

	// Register the source in media so it counts toward the coverage denominator
	// even if every modality for it ends up below threshold. Duration/fps unknown
	// at ingest time (0); becky-embed --source fills probe facts when available.
	if err := db.UpsertMedia(srcLabel, "", 0, 0); err != nil {
		return fmt.Errorf("register source video: %w", err)
	}

	res := ingestResult{
		Mode:       "ingest",
		Source:     srcLabel,
		VerifiedBy: strings.TrimSpace(verifiedBy),
		Confirmed:  strings.TrimSpace(verifiedBy) != "",
	}

	for _, e := range in.Identifications {
		name := strings.TrimSpace(e.Name)
		modality := strings.TrimSpace(e.Type)
		if name == "" || modality == "" {
			// Nothing to track without a name+modality; skip but keep going.
			res.Skipped++
			beckyio.Logf(verbose, "skip ingest entry (missing name/modality): %+v", e)
			continue
		}
		row := beckydb.Identification{
			ID:           identID(srcLabel, modality, e.SpeakerID, name),
			SourceFile:   srcLabel,
			SourceSHA256: "",
			EntityName:   name,
			Modality:     modality,
			Confidence:   e.Confidence,
			SpeakerID:    e.SpeakerID,
			VerifiedBy:   res.VerifiedBy,
		}
		if err := db.UpsertIdentification(row); err != nil {
			return fmt.Errorf("write identification %s: %w", row.ID, err)
		}
		res.Ingested++
		beckyio.Logf(verbose, "ingested %s %q (%s, conf %.4f)%s",
			modality, name, row.SpeakerID, e.Confidence, confirmedTag(res.Confirmed))
	}

	beckyio.Logf(verbose, "ingest: %d row(s) written, %d skipped, source=%q, confirmed=%v",
		res.Ingested, res.Skipped, srcLabel, res.Confirmed)

	beckyio.PrintJSON(res)
	return nil
}

// identID builds the deterministic identifications.id so re-ingesting the same
// becky-identify run replaces (not duplicates) rows. Keyed by source + modality
// + the discriminator: speaker_id for voice/face, else the entity name (e.g.
// location has no speaker). Matches the segments scheme of sha12-prefixed keys.
func identID(source, modality, speakerID, name string) string {
	key := strings.TrimSpace(speakerID)
	if key == "" {
		key = strings.TrimSpace(name)
	}
	return fmt.Sprintf("%s:%s:%s", sha12(source), modality, key)
}

// sha12 returns the first 12 hex chars of the SHA-256 of s — the same short,
// stable prefix scheme segments use for deterministic keys.
func sha12(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}

// confirmedTag annotates a verbose ingest line with the confirmation state.
func confirmedTag(confirmed bool) string {
	if confirmed {
		return " [confirmed]"
	}
	return " [unconfirmed]"
}
