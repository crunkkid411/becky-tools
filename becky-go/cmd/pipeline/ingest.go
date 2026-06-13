// Sidecar ingestion for becky-pipeline: reuse the data yt-dlp already downloaded
// instead of re-transcribing a 500 GB library with Parakeet.
//
// Before the transcribe step shells out to becky-transcribe, the pipeline looks
// for a subtitle sidecar (<stem>.en.srt / .srt / .vtt / .json3) next to the
// video. If one exists (and --force-transcribe is not set), it is parsed into the
// SAME transcript.json shape becky-transcribe emits — so every downstream step
// (embed/search) works unchanged — and stamped transcript_source="youtube-srt".
// Parakeet is skipped for that file. When no sidecar exists, the normal
// becky-transcribe run happens and its output is stamped transcript_source=
// "parakeet". A separate metadata step ingests <stem>.info.json + .live_chat.json.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"becky-go/internal/mediainfo"
	"becky-go/internal/sidecar"
)

// transcriptDoc is the becky-transcribe JSON contract plus a transcript_source
// provenance field. It is the exact shape becky-embed reads (file/duration/
// model/language/segments) so a sidecar transcript is a drop-in replacement.
type transcriptDoc struct {
	File             string            `json:"file"`
	Duration         float64           `json:"duration"`
	Model            string            `json:"model"`
	Language         string            `json:"language"`
	Text             string            `json:"text"`
	Words            []any             `json:"words"`
	Segments         []sidecar.Segment `json:"segments"`
	TranscriptSource string            `json:"transcript_source"` // youtube-srt | parakeet
	SidecarPath      string            `json:"sidecar_path,omitempty"`
}

// writeSidecarTranscript parses the subtitle sidecar and writes transcript.json
// in the becky-transcribe shape with transcript_source="youtube-srt". It returns
// the parsed Subtitle so the caller can report segment counts. duration comes
// from the probe (best-effort; falls back to the last segment end).
func writeSidecarTranscript(sidecarPath, video, outPath, lang, ffprobe string) (sidecar.Subtitle, error) {
	sub, err := sidecar.ParseSubtitle(sidecarPath)
	if err != nil {
		return sidecar.Subtitle{}, err
	}
	dur := probeDuration(ffprobe, video)
	if dur == 0 && len(sub.Segments) > 0 {
		dur = sub.Segments[len(sub.Segments)-1].End
	}
	doc := transcriptDoc{
		File:             video,
		Duration:         round3(dur),
		Model:            "youtube-" + sub.Format,
		Language:         lang,
		Text:             sub.Text,
		Words:            []any{},
		Segments:         sub.Segments,
		TranscriptSource: "youtube-srt",
		SidecarPath:      sidecarPath,
	}
	if err := writeJSONFile(outPath, doc); err != nil {
		return sidecar.Subtitle{}, fmt.Errorf("write sidecar transcript: %w", err)
	}
	return sub, nil
}

// stampTranscriptSource rewrites an existing transcript.json (produced by
// becky-transcribe) to add transcript_source="parakeet". It is best-effort: a
// read/parse failure leaves the file as becky-transcribe wrote it (still valid).
func stampTranscriptSource(path, source string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var m map[string]any
	if json.Unmarshal(data, &m) != nil {
		return
	}
	if _, ok := m["transcript_source"]; ok {
		return // already stamped
	}
	m["transcript_source"] = source
	_ = writeJSONFile(path, m)
}

// probeDuration returns the media duration in seconds, or 0 if probing fails.
func probeDuration(ffprobe, video string) float64 {
	if ffprobe == "" {
		return 0
	}
	info, err := mediainfo.Probe(ffprobe, video)
	if err != nil {
		return 0
	}
	return info.Duration
}

// round3 rounds to milliseconds (matches becky-transcribe's rounding).
func round3(f float64) float64 {
	return float64(int(f*1000+0.5)) / 1000
}

// --- metadata step (info.json + live_chat.json) ---

// metadataDoc is the metadata.json the metadata step writes: the normalized
// yt-dlp .info.json fields plus any timestamped live chat. It is the
// human-readable artifact; the DB ingestion (see metadb.go) stores the same data
// in queryable tables.
type metadataDoc struct {
	Video     string                `json:"video"`
	Info      *sidecar.Metadata     `json:"info,omitempty"`
	Chat      []sidecar.ChatMessage `json:"chat"`
	ChatCount int                   `json:"chat_count"`
	Sources   map[string]string     `json:"sources"` // which sidecar each part came from
}

// ingestMetadata parses the .info.json and .live_chat.json next to video (if
// present) and writes metadata.json. It returns the doc (for DB ingestion and
// reporting) and whether anything was found. A video with no metadata sidecars
// yields found=false and writes nothing (graceful no-op).
func ingestMetadata(video, outPath string) (metadataDoc, bool, error) {
	doc := metadataDoc{
		Video:   video,
		Chat:    []sidecar.ChatMessage{},
		Sources: map[string]string{},
	}
	found := false

	if infoPath := sidecar.FindInfoJSON(video); infoPath != "" {
		m, err := sidecar.ParseInfoJSON(infoPath)
		if err != nil {
			return doc, false, fmt.Errorf("parse info.json: %w", err)
		}
		doc.Info = &m
		doc.Sources["info"] = infoPath
		found = true
	}
	if chatPath := sidecar.FindLiveChat(video); chatPath != "" {
		// A partial chat parse is still useful; record what we got + the source
		// either way (err is non-nil only on an I/O fault mid-file).
		msgs, _ := sidecar.ParseLiveChat(chatPath)
		doc.Chat = msgs
		doc.ChatCount = len(msgs)
		doc.Sources["live_chat"] = chatPath
		found = true
	}

	if !found {
		return doc, false, nil
	}
	if err := writeJSONFile(outPath, doc); err != nil {
		return doc, true, fmt.Errorf("write metadata.json: %w", err)
	}
	return doc, true, nil
}
