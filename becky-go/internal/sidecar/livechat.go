// Live-chat parsing for the sidecar package: yt-dlp's <name>.live_chat.json.
//
// The file is JSONL (one JSON object per line), each wrapping a YouTube
// "replayChatItemAction". The chat lines we care about are
// liveChatTextMessageRenderer items; we pull the author handle, the message text
// (concatenated "runs"), and the video-relative offset (videoOffsetTimeMsec) so
// chat becomes timestamped who/when/what for cross-referencing the transcript.
//
// System/engagement/membership items without text are skipped. Malformed lines
// are skipped individually so one bad line never aborts the whole file.
package sidecar

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ChatMessage is one timestamped live-chat line.
type ChatMessage struct {
	Author    string  `json:"author"`     // authorName.simpleText (e.g. @handle)
	Text      string  `json:"text"`       // concatenated message runs
	OffsetSec float64 `json:"offset_sec"` // seconds into the video (videoOffsetTimeMsec)
}

// chatLine mirrors one JSONL record's relevant nesting.
type chatLine struct {
	ReplayChatItemAction struct {
		Actions             []chatAction `json:"actions"`
		VideoOffsetTimeMsec string       `json:"videoOffsetTimeMsec"`
	} `json:"replayChatItemAction"`
}

type chatAction struct {
	AddChatItemAction struct {
		Item struct {
			LiveChatTextMessageRenderer *textRenderer `json:"liveChatTextMessageRenderer"`
		} `json:"item"`
	} `json:"addChatItemAction"`
}

type textRenderer struct {
	Message struct {
		Runs []struct {
			Text string `json:"text"`
		} `json:"runs"`
	} `json:"message"`
	AuthorName struct {
		SimpleText string `json:"simpleText"`
	} `json:"authorName"`
}

// ParseLiveChat parses a yt-dlp .live_chat.json (JSONL) into timestamped chat
// messages, ordered as they appear in the file (chronological replay order).
func ParseLiveChat(path string) ([]ChatMessage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open live_chat %s: %w", path, err)
	}
	defer f.Close()

	var msgs []ChatMessage
	sc := bufio.NewScanner(f)
	// Live-chat lines can be large (embedded thumbnails/params); grow the buffer.
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec chatLine
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue // skip a malformed line, keep going
		}
		offset := parseOffsetMsec(rec.ReplayChatItemAction.VideoOffsetTimeMsec)
		for _, act := range rec.ReplayChatItemAction.Actions {
			r := act.AddChatItemAction.Item.LiveChatTextMessageRenderer
			if r == nil {
				continue // not a text message (engagement/system/etc.)
			}
			var b strings.Builder
			for _, run := range r.Message.Runs {
				b.WriteString(run.Text)
			}
			text := strings.TrimSpace(b.String())
			if text == "" {
				continue
			}
			msgs = append(msgs, ChatMessage{
				Author:    r.AuthorName.SimpleText,
				Text:      text,
				OffsetSec: offset,
			})
		}
	}
	if err := sc.Err(); err != nil {
		return msgs, fmt.Errorf("scan live_chat %s: %w", path, err)
	}
	return msgs, nil
}

// parseOffsetMsec converts videoOffsetTimeMsec ("12345") to seconds; "" -> 0.
func parseOffsetMsec(s string) float64 {
	if s == "" {
		return 0
	}
	ms, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return float64(ms) / 1000.0
}
