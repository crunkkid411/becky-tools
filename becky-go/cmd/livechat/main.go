// becky-livechat - a livestream's chat replay, timed to the stream, and (given
// becky's transcript) where Jordan read each message aloud and how long after.
//
//	becky-livechat <youtube id | url | file with [id] in its name> [--transcript t.json] [--out chat.json]
//	becky-livechat --chat <id>.live_chat.json [--transcript t.json]   (already downloaded)
//
// Jordan, 2026-10-07: "we can download the live chat along with the video via
// yt-dlp (just don't change my .conf file please) - if we are provided the
// time of each chat and can determine how much of a delay / lag there was,
// that may be helpful in clarifying whether i'm talking to chat or not."
// yt-dlp runs only through internal/ytdlp (--ignore-config, 1 call / 90 s).
//
// Output: {"video_id", "messages": [{t, author, text, kind, amount, read_at,
// read_words}], "delay": {median, n}, "replies": [{at, line, msg, author, chat,
// lag, p}]}. t is seconds into the stream when the message arrived; read_at is
// when Jordan said most of its words, if he did. replies (reply.go) are the
// lines where he ANSWERS a message without reading it out, decided by a System
// One model through the capped internal/systemone client (--no-replies: skip).
// Exit codes: 0 ok, 1 error, 2 usage.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"becky-go/internal/beckyio"
	"becky-go/internal/systemone"
	"becky-go/internal/ytdlp"
)

var idInName = regexp.MustCompile(`(?:v=|youtu\.be/|\[)([A-Za-z0-9_-]{11})(?:\]|&|$)`)

func main() {
	chatFile := flag.String("chat", "", "an already downloaded <id>.live_chat.json")
	transcript := flag.String("transcript", "", "becky-transcribe JSON of the stream: finds where each message was read aloud")
	out := flag.String("out", "", "write JSON here instead of stdout")
	noReplies := flag.Bool("no-replies", false, "skip finding the lines where he answers chat (System One, paid, capped)")
	var refs []string // flags may come before or after the video reference
	for args := os.Args[1:]; len(args) > 0; {
		if err := flag.CommandLine.Parse(args); err != nil {
			os.Exit(2)
		}
		if args = flag.Args(); len(args) > 0 {
			refs, args = append(refs, args[0]), args[1:]
		}
	}
	if (*chatFile == "") == (len(refs) == 0) || len(refs) > 1 {
		fmt.Fprintln(os.Stderr, "usage: becky-livechat <youtube id | url | file with [id]> [--transcript t.json] [--out chat.json]\n       becky-livechat --chat <id>.live_chat.json [--transcript t.json]")
		os.Exit(2)
	}
	id := ""
	if *chatFile == "" {
		id = videoID(refs[0])
		if id == "" {
			beckyio.Fatalf("no YouTube video id in %q", refs[0])
		}
		path, err := download(id)
		if err != nil {
			beckyio.Fatalf("%v", err)
		}
		*chatFile = path
	}
	msgs, err := readChat(*chatFile)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}
	res := result{VideoID: id, Messages: msgs}
	if *transcript != "" {
		ws, err := readWords(*transcript)
		if err != nil {
			beckyio.Fatalf("%v", err)
		}
		res.Delay = matchReadAloud(res.Messages, ws)
		if !*noReplies {
			logf := func(f string, a ...any) { fmt.Fprintf(os.Stderr, "becky-livechat: "+f+"\n", a...) }
			res.Replies, err = findReplies(context.Background(), systemone.NewHosted("becky-livechat"), res.Messages, ws, logf)
			if err != nil {
				res.Note = "finding replies to chat stopped early: " + err.Error()
				logf("%s", res.Note)
			}
		}
	}
	if *out == "" {
		beckyio.PrintJSON(res)
		return
	}
	b, _ := json.MarshalIndent(res, "", " ")
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		beckyio.Fatalf("write %s: %v", *out, err)
	}
	read := 0
	for _, m := range res.Messages {
		if m.ReadAt != nil {
			read++
		}
	}
	fmt.Fprintf(os.Stderr, "becky-livechat: %d messages, %d read aloud, delay median %.1fs (%d matches), %d answered -> %s\n",
		len(res.Messages), read, res.Delay.Median, res.Delay.N, len(res.Replies), *out)
}

type result struct {
	VideoID  string    `json:"video_id,omitempty"`
	Messages []message `json:"messages"`
	Delay    delay     `json:"delay"`
	Replies  []reply   `json:"replies,omitempty"`
	Note     string    `json:"note,omitempty"`
}

func videoID(ref string) string {
	if len(ref) == 11 && regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`).MatchString(ref) {
		return ref
	}
	if m := idInName.FindStringSubmatch(filepath.Base(ref)); m != nil {
		return m[1]
	}
	if m := idInName.FindStringSubmatch(ref); m != nil {
		return m[1]
	}
	return ""
}

// download fetches only the chat replay (no video) into the temp folder.
func download(id string) (string, error) {
	dir := filepath.Join(os.TempDir(), "becky-livechat")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, id+".live_chat.json")
	if _, err := os.Stat(path); err == nil {
		return path, nil // already fetched: no second request
	}
	if _, err := ytdlp.Run("--skip-download", "--write-subs", "--sub-langs", "live_chat", "--no-playlist",
		"-P", dir, "-o", "%(id)s.%(ext)s", "https://www.youtube.com/watch?v="+id); err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("YouTube has no chat replay for %s", id)
	}
	return path, nil
}

func readWords(path string) ([]word, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var tr struct {
		Words []word `json:"words"`
	}
	if err := json.Unmarshal(raw, &tr); err != nil || len(tr.Words) == 0 {
		return nil, fmt.Errorf("%s is not a becky-transcribe JSON with words", path)
	}
	return tr.Words, nil
}

// readChat parses yt-dlp's live_chat.json: one JSON object per line.
func readChat(path string) ([]message, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []message
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		out = append(out, parseLine(sc.Bytes())...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].T < out[j].T })
	return out, sc.Err()
}
