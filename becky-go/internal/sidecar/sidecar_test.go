package sidecar

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleSRT = `1
00:00:00,080 --> 00:00:04,960
I have documentation that I bought my

2
00:00:02,080 --> 00:00:07,040
cat last year in October because I was

3
00:00:04,960 --> 00:00:08,880
lonely as [ __ ] It was way before I met
`

func TestParseSRTBasic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "clip.en.srt")
	if err := os.WriteFile(p, []byte(sampleSRT), 0o644); err != nil {
		t.Fatal(err)
	}
	sub, err := ParseSubtitle(p)
	if err != nil {
		t.Fatalf("ParseSubtitle: %v", err)
	}
	if sub.Format != "srt" {
		t.Errorf("format = %q, want srt", sub.Format)
	}
	if len(sub.Segments) == 0 {
		t.Fatal("expected segments, got 0")
	}
	if sub.Segments[0].Start != 0.08 {
		t.Errorf("seg0 start = %v, want 0.08", sub.Segments[0].Start)
	}
	if sub.Segments[0].Text == "" {
		t.Error("seg0 text is empty")
	}
	// Full text should read the words once, in order (no rolling duplication).
	want := "I have documentation that I bought my cat last year in October because I was lonely as [ __ ] It was way before I met"
	if sub.Text != want {
		t.Errorf("dedup text mismatch:\n got: %q\nwant: %q", sub.Text, want)
	}
}

// TestRollingDedup proves consecutive overlapping captions collapse to the
// incremental new text (YouTube auto-sub rolling style).
func TestRollingDedup(t *testing.T) {
	segs := []Segment{
		{Start: 4.96, End: 9.92, Text: "Whoa. We're doing it. We're doing the"},
		{Start: 7.27, End: 13.24, Text: "We're doing the live streams. We're making it happen."},
	}
	out := dedupRolling(segs)
	if len(out) != 2 {
		t.Fatalf("expected 2 deduped segments, got %d: %+v", len(out), out)
	}
	if out[1].Text != "live streams. We're making it happen." {
		t.Errorf("seg1 text = %q, want the incremental tail", out[1].Text)
	}
	// Timestamps come straight from the .srt: the deduped tail keeps its cue's
	// ORIGINAL start (7.27), it is NOT clamped forward to the previous cue's end.
	// A search hit must seek to exactly the timestamp the .srt lists.
	if out[0].Start != 4.96 {
		t.Errorf("seg0 start = %v, want the literal .srt cue start 4.96", out[0].Start)
	}
	if out[1].Start != 7.27 {
		t.Errorf("seg1 start = %v, want the literal .srt cue start 7.27 (never clamped)", out[1].Start)
	}
}

func TestParseVTT(t *testing.T) {
	vtt := "WEBVTT\n\n00:00.000 --> 00:03.853\n'Cause my parents came to visit me in Disney World in\n\n00:03.949 --> 00:07.659\nlike September, I think. And so\n"
	dir := t.TempDir()
	p := filepath.Join(dir, "clip.vtt")
	if err := os.WriteFile(p, []byte(vtt), 0o644); err != nil {
		t.Fatal(err)
	}
	sub, err := ParseSubtitle(p)
	if err != nil {
		t.Fatalf("ParseSubtitle: %v", err)
	}
	if sub.Format != "vtt" {
		t.Errorf("format = %q, want vtt", sub.Format)
	}
	if len(sub.Segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(sub.Segments))
	}
	if sub.Segments[0].Start != 0 || sub.Segments[0].End != 3.853 {
		t.Errorf("seg0 timing = %v..%v, want 0..3.853", sub.Segments[0].Start, sub.Segments[0].End)
	}
}

func TestParseJSON3(t *testing.T) {
	j := `{"events":[
	  {"tStartMs":2800,"dDurationMs":4479,"segs":[{"utf8":"Boom"},{"utf8":". there"}]},
	  {"tStartMs":9000,"dDurationMs":1000,"segs":[{"utf8":"\n"}]},
	  {"tStartMs":10000,"dDurationMs":2000,"segs":[{"utf8":"Hi Panda"}]}
	]}`
	dir := t.TempDir()
	p := filepath.Join(dir, "clip.json3")
	if err := os.WriteFile(p, []byte(j), 0o644); err != nil {
		t.Fatal(err)
	}
	sub, err := ParseSubtitle(p)
	if err != nil {
		t.Fatalf("ParseSubtitle: %v", err)
	}
	if sub.Format != "json3" {
		t.Errorf("format = %q, want json3", sub.Format)
	}
	if len(sub.Segments) != 2 {
		t.Fatalf("expected 2 segments, got %d: %+v", len(sub.Segments), sub.Segments)
	}
	if sub.Segments[0].Start != 2.8 {
		t.Errorf("seg0 start = %v, want 2.8", sub.Segments[0].Start)
	}
	if sub.Segments[0].Text != "Boom. there" {
		t.Errorf("seg0 text = %q", sub.Segments[0].Text)
	}
}

func TestFindSubtitlePriority(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "myclip.mp4")
	mustWrite(t, video, "x")
	mustWrite(t, filepath.Join(dir, "myclip.srt"), "1\n00:00:00,000 --> 00:00:01,000\nbare\n")
	mustWrite(t, filepath.Join(dir, "myclip.en.srt"), "1\n00:00:00,000 --> 00:00:01,000\nenglish\n")

	got := FindSubtitle(video)
	if filepath.Base(got) != "myclip.en.srt" {
		t.Errorf("FindSubtitle = %q, want myclip.en.srt", got)
	}
}

func TestFindSubtitleNone(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "lonely.mp4")
	mustWrite(t, video, "x")
	if got := FindSubtitle(video); got != "" {
		t.Errorf("FindSubtitle = %q, want empty", got)
	}
}

func TestParseInfoJSON(t *testing.T) {
	info := `{
	  "id":"abc123",
	  "title":"Demo Title",
	  "description":"line one\nline two",
	  "uploader":"Some Channel",
	  "uploader_id":"@somechan",
	  "channel":"Some Channel",
	  "channel_id":"UC0000000000000000000000",
	  "channel_url":"https://www.youtube.com/channel/UC0000000000000000000000",
	  "upload_date":"20260106",
	  "timestamp":1767742010,
	  "duration":148,
	  "webpage_url":"https://www.youtube.com/watch?v=abc123",
	  "view_count":8702,
	  "like_count":111,
	  "comment_count":28,
	  "chapters":[{"title":"Intro","start_time":0,"end_time":30}]
	}`
	dir := t.TempDir()
	p := filepath.Join(dir, "clip.info.json")
	mustWrite(t, p, info)

	m, err := ParseInfoJSON(p)
	if err != nil {
		t.Fatalf("ParseInfoJSON: %v", err)
	}
	if m.VideoID != "abc123" || m.Title != "Demo Title" {
		t.Errorf("id/title = %q/%q", m.VideoID, m.Title)
	}
	if m.UploadISO != "2026-01-06T23:26:50Z" {
		t.Errorf("upload_iso = %q, want 2026-01-06T23:26:50Z", m.UploadISO)
	}
	if m.UploadUnix != 1767742010 {
		t.Errorf("upload_unix = %d", m.UploadUnix)
	}
	if m.Uploader != "Some Channel" || m.UploaderID != "@somechan" {
		t.Errorf("uploader = %q/%q", m.Uploader, m.UploaderID)
	}
	if m.Duration != 148 {
		t.Errorf("duration = %v", m.Duration)
	}
	if len(m.Chapters) != 1 || m.Chapters[0].Title != "Intro" {
		t.Errorf("chapters = %+v", m.Chapters)
	}
	if m.CommentCount != 28 {
		t.Errorf("comment_count = %d", m.CommentCount)
	}
}

func TestParseInfoJSONDateFallback(t *testing.T) {
	info := `{"id":"x","title":"T","upload_date":"20260101"}`
	dir := t.TempDir()
	p := filepath.Join(dir, "c.info.json")
	mustWrite(t, p, info)
	m, err := ParseInfoJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	if m.UploadISO != "2026-01-01T00:00:00Z" {
		t.Errorf("upload_iso = %q, want 2026-01-01T00:00:00Z", m.UploadISO)
	}
}

func TestParseLiveChat(t *testing.T) {
	jsonl := `{"replayChatItemAction":{"actions":[{"addChatItemAction":{"item":{"liveChatViewerEngagementMessageRenderer":{"message":{"runs":[{"text":"Live chat replay is on."}]}}}}}],"videoOffsetTimeMsec":"0"}}
{"replayChatItemAction":{"actions":[{"addChatItemAction":{"item":{"liveChatTextMessageRenderer":{"message":{"runs":[{"text":"hello "},{"text":"world"}]},"authorName":{"simpleText":"@viewer1"}}}}}],"videoOffsetTimeMsec":"12345"}}
`
	dir := t.TempDir()
	p := filepath.Join(dir, "clip.live_chat.json")
	mustWrite(t, p, jsonl)

	msgs, err := ParseLiveChat(p)
	if err != nil {
		t.Fatalf("ParseLiveChat: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 text message, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Author != "@viewer1" {
		t.Errorf("author = %q", msgs[0].Author)
	}
	if msgs[0].Text != "hello world" {
		t.Errorf("text = %q", msgs[0].Text)
	}
	if msgs[0].OffsetSec != 12.345 {
		t.Errorf("offset = %v, want 12.345", msgs[0].OffsetSec)
	}
}

func TestFindInfoAndLiveChat(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "vid.mp4")
	mustWrite(t, video, "x")
	mustWrite(t, filepath.Join(dir, "vid.info.json"), `{"id":"z"}`)
	mustWrite(t, filepath.Join(dir, "vid.live_chat.json"), ``)

	if filepath.Base(FindInfoJSON(video)) != "vid.info.json" {
		t.Errorf("FindInfoJSON = %q", FindInfoJSON(video))
	}
	if filepath.Base(FindLiveChat(video)) != "vid.live_chat.json" {
		t.Errorf("FindLiveChat = %q", FindLiveChat(video))
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
