// DB ingestion of yt-dlp metadata for becky-pipeline's metadata step. The
// parsed .info.json + .live_chat.json (a metadataDoc) are written into the
// shared forensic DB's additive media_meta + live_chat tables so the upload
// time, uploader/channel, title/description, chapters and timestamped chat are
// searchable / available for cross-referencing alongside the transcript.
//
// This is best-effort and degrades gracefully: when the sqlite3 CLI / vec0
// extension are not configured (e.g. a corpus indexed without the embed step),
// it returns an error the caller turns into a note — metadata.json is still
// written. It NEVER fails the pipeline.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"becky-go/internal/beckydb"
	"becky-go/internal/config"
)

// ingestMetadataToDB stores a metadataDoc's info + chat into the forensic DB.
// It opens the DB, ensures the (additive) schema, then upserts the media_meta
// row and each live_chat line. A nil doc.Info with empty chat is a no-op.
func ingestMetadataToDB(cfg config.Config, dbPath, video string, doc metadataDoc) error {
	if doc.Info == nil && len(doc.Chat) == 0 {
		return nil
	}
	db, err := beckydb.Open(cfg, dbPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	if err := db.EnsureSchema(); err != nil {
		return fmt.Errorf("ensure schema: %w", err)
	}

	if doc.Info != nil {
		m := doc.Info
		chaptersJSON := mustJSON(m.Chapters, "[]")
		tagsJSON := mustJSON(m.Tags, "[]")
		row := beckydb.MediaMeta{
			SourceFile:   video,
			VideoID:      m.VideoID,
			Title:        m.Title,
			Description:  m.Description,
			Uploader:     m.Uploader,
			UploaderID:   m.UploaderID,
			Channel:      m.Channel,
			ChannelID:    m.ChannelID,
			ChannelURL:   m.ChannelURL,
			UploadISO:    m.UploadISO,
			UploadUnix:   m.UploadUnix,
			Duration:     m.Duration,
			WebpageURL:   m.WebpageURL,
			ChaptersJSON: chaptersJSON,
			TagsJSON:     tagsJSON,
		}
		if err := db.UpsertMediaMeta(row); err != nil {
			return fmt.Errorf("upsert media_meta: %w", err)
		}
	}

	for i, c := range doc.Chat {
		line := beckydb.ChatLine{
			ChatID:     fmt.Sprintf("%s:%d", sha12(video), i),
			SourceFile: video,
			Author:     c.Author,
			Text:       c.Text,
			OffsetSec:  c.OffsetSec,
		}
		if err := db.InsertLiveChat(line); err != nil {
			return fmt.Errorf("insert live_chat %d: %w", i, err)
		}
	}
	return nil
}

// mustJSON marshals v, returning fallback on error so a bad field never aborts
// ingestion.
func mustJSON(v any, fallback string) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fallback
	}
	return string(b)
}

// sha12 returns the first 12 hex chars of the SHA-256 of s — the same scheme
// becky-embed uses to derive deterministic per-source ids.
func sha12(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])[:12]
}
