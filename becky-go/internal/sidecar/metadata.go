// Metadata parsing for the sidecar package: yt-dlp's <name>.info.json and
// <name>.live_chat.json. These give a downloaded video its provenance — when it
// was uploaded (the timeline anchor for cross-referencing), who uploaded it,
// title/description, chapters, source URL/id — plus, for streams/premieres,
// the timestamped live chat (who said what, and when relative to the video).
//
// The upload time is normalized to an ISO-8601 (RFC3339 UTC) string so it slots
// straight into the forensic DB as a real timeline field. All fields degrade to
// their zero value when absent; a missing sidecar is not an error (the caller
// just gets "" from the Find* helpers).
package sidecar

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Chapter is one yt-dlp chapter marker.
type Chapter struct {
	Title     string  `json:"title"`
	StartTime float64 `json:"start_time"`
	EndTime   float64 `json:"end_time"`
}

// Metadata is the normalized subset of a yt-dlp .info.json the becky index keeps.
type Metadata struct {
	Path         string    `json:"path"`          // the .info.json file this came from
	VideoID      string    `json:"video_id"`      // yt-dlp "id"
	Title        string    `json:"title"`         // "title" (falls back to fulltitle)
	Description  string    `json:"description"`   // "description"
	Uploader     string    `json:"uploader"`      // "uploader" (falls back to channel)
	UploaderID   string    `json:"uploader_id"`   // "uploader_id" (e.g. @handle)
	Channel      string    `json:"channel"`       // "channel"
	ChannelID    string    `json:"channel_id"`    // "channel_id" (UC...)
	ChannelURL   string    `json:"channel_url"`   // "channel_url"
	UploadDate   string    `json:"upload_date"`   // raw yt-dlp "YYYYMMDD"
	UploadISO    string    `json:"upload_iso"`    // RFC3339 UTC timeline anchor (from timestamp/upload_date)
	UploadUnix   int64     `json:"upload_unix"`   // unix seconds (0 if unknown)
	Duration     float64   `json:"duration"`      // seconds
	WebpageURL   string    `json:"webpage_url"`   // canonical URL
	OriginalURL  string    `json:"original_url"`  // as-requested URL (may be empty)
	Tags         []string  `json:"tags"`          // "tags"
	Categories   []string  `json:"categories"`    // "categories"
	ViewCount    int64     `json:"view_count"`    // "view_count"
	LikeCount    int64     `json:"like_count"`    // "like_count"
	CommentCount int64     `json:"comment_count"` // "comment_count"
	Chapters     []Chapter `json:"chapters"`      // "chapters" (may be empty)
}

// infoRaw mirrors the yt-dlp .info.json fields we read. timestamp is unix
// seconds; upload_date / release_date are "YYYYMMDD"; release_timestamp is a
// fallback for premieres.
type infoRaw struct {
	ID               string       `json:"id"`
	Title            string       `json:"title"`
	Fulltitle        string       `json:"fulltitle"`
	Description      string       `json:"description"`
	Uploader         string       `json:"uploader"`
	UploaderID       string       `json:"uploader_id"`
	Channel          string       `json:"channel"`
	ChannelID        string       `json:"channel_id"`
	ChannelURL       string       `json:"channel_url"`
	UploadDate       string       `json:"upload_date"`
	ReleaseDate      string       `json:"release_date"`
	Timestamp        *int64       `json:"timestamp"`
	ReleaseTimestamp *int64       `json:"release_timestamp"`
	Duration         float64      `json:"duration"`
	WebpageURL       string       `json:"webpage_url"`
	OriginalURL      string       `json:"original_url"`
	Tags             []string     `json:"tags"`
	Categories       []string     `json:"categories"`
	ViewCount        *int64       `json:"view_count"`
	LikeCount        *int64       `json:"like_count"`
	CommentCount     *int64       `json:"comment_count"`
	Chapters         []chapterRaw `json:"chapters"`
}

type chapterRaw struct {
	Title     string  `json:"title"`
	StartTime float64 `json:"start_time"`
	EndTime   float64 `json:"end_time"`
}

// FindInfoJSON returns the <stem>.info.json next to videoPath, or "" if none.
// yt-dlp may also write "<stem>.<lang>.info.json" variants; the exact stem match
// is preferred, then any "<stem>*.info.json".
func FindInfoJSON(videoPath string) string {
	return findSidecarBySuffix(videoPath, ".info.json")
}

// FindLiveChat returns the <stem>.live_chat.json next to videoPath, or "".
func FindLiveChat(videoPath string) string {
	return findSidecarBySuffix(videoPath, ".live_chat.json")
}

// findSidecarBySuffix returns the file in videoPath's dir whose name is the
// video stem plus suffix (exact), else the first "<stem>*<suffix>" match.
func findSidecarBySuffix(videoPath, suffix string) string {
	dir := filepath.Dir(videoPath)
	stem := strings.ToLower(stemOf(videoPath))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	exact := stem + strings.ToLower(suffix)
	var fallback string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ln := strings.ToLower(e.Name())
		if ln == exact {
			return filepath.Join(dir, e.Name())
		}
		if fallback == "" && strings.HasPrefix(ln, stem) && strings.HasSuffix(ln, strings.ToLower(suffix)) {
			fallback = filepath.Join(dir, e.Name())
		}
	}
	return fallback
}

// ParseInfoJSON parses a yt-dlp .info.json into normalized Metadata. The upload
// time is resolved (timestamp > release_timestamp > upload_date > release_date)
// and rendered as RFC3339 UTC in UploadISO so it can anchor a timeline.
func ParseInfoJSON(path string) (Metadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Metadata{}, fmt.Errorf("read info.json %s: %w", path, err)
	}
	var raw infoRaw
	if err := json.Unmarshal(data, &raw); err != nil {
		return Metadata{}, fmt.Errorf("parse info.json %s: %w", path, err)
	}

	m := Metadata{
		Path:        path,
		VideoID:     raw.ID,
		Title:       firstNonEmpty(raw.Title, raw.Fulltitle),
		Description: raw.Description,
		Uploader:    firstNonEmpty(raw.Uploader, raw.Channel),
		UploaderID:  raw.UploaderID,
		Channel:     firstNonEmpty(raw.Channel, raw.Uploader),
		ChannelID:   raw.ChannelID,
		ChannelURL:  raw.ChannelURL,
		UploadDate:  firstNonEmpty(raw.UploadDate, raw.ReleaseDate),
		Duration:    raw.Duration,
		WebpageURL:  raw.WebpageURL,
		OriginalURL: raw.OriginalURL,
		Tags:        raw.Tags,
		Categories:  raw.Categories,
	}
	m.ViewCount = derefInt(raw.ViewCount)
	m.LikeCount = derefInt(raw.LikeCount)
	m.CommentCount = derefInt(raw.CommentCount)
	for _, c := range raw.Chapters {
		m.Chapters = append(m.Chapters, Chapter{Title: c.Title, StartTime: c.StartTime, EndTime: c.EndTime})
	}

	m.UploadUnix, m.UploadISO = resolveUploadTime(raw)
	return m, nil
}

// resolveUploadTime picks the best available upload instant and returns it as
// (unix seconds, RFC3339 UTC). Prefers the precise unix timestamp; falls back to
// the YYYYMMDD date (treated as 00:00:00 UTC). Returns (0, "") when nothing is
// available.
func resolveUploadTime(raw infoRaw) (int64, string) {
	if raw.Timestamp != nil && *raw.Timestamp > 0 {
		t := time.Unix(*raw.Timestamp, 0).UTC()
		return *raw.Timestamp, t.Format(time.RFC3339)
	}
	if raw.ReleaseTimestamp != nil && *raw.ReleaseTimestamp > 0 {
		t := time.Unix(*raw.ReleaseTimestamp, 0).UTC()
		return *raw.ReleaseTimestamp, t.Format(time.RFC3339)
	}
	date := firstNonEmpty(raw.UploadDate, raw.ReleaseDate)
	if len(date) == 8 {
		if t, err := time.Parse("20060102", date); err == nil {
			return t.Unix(), t.UTC().Format(time.RFC3339)
		}
	}
	return 0, ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func derefInt(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
