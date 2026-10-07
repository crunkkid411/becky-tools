package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// backfill adds YouTube's transcript to up to n existing becky-intake notes
// that have none, newest upload first. Captions only: a video YouTube has no
// transcript for is reported and left alone (no audio download, no GPU).
// Each note costs two yt-dlp calls, so with the 90 s gate about 3 minutes.
func backfill(vault, temp string, n int) {
	notes, _ := filepath.Glob(filepath.Join(vault, "*.md"))
	sort.Sort(sort.Reverse(sort.StringSlice(notes)))
	tried := 0
	for _, note := range notes {
		base := strings.TrimSuffix(filepath.Base(note), ".md")
		if tried >= n || strings.HasSuffix(base, ".transcript") || len(base) < 11 {
			continue
		}
		if _, err := os.Stat(filepath.Join(vault, base+".transcript.md")); err == nil {
			continue
		}
		raw, err := os.ReadFile(note)
		id := base[len(base)-11:]
		if err != nil || !strings.Contains(string(raw), "tool: becky-intake") || !idRe.MatchString(id) {
			continue
		}
		tried++
		fmt.Fprintf(os.Stderr, "%s  %s\n", id, backfillOne(vault, temp, note, base, id, string(raw)))
	}
}

func backfillOne(vault, temp, note, base, id, raw string) string {
	v, err := fetchVideo(id)
	if err != nil {
		return "skipped: " + err.Error()
	}
	lang, from := captionTrack(v)
	if lang == "" {
		return "skipped: YouTube has no transcript for this video"
	}
	var text string
	if err := withTempDir(temp, id, func(dir string) (e error) {
		text, e = fetchCaptions(dir, v, lang)
		return e
	}); err != nil || text == "" {
		return fmt.Sprintf("skipped: captions not downloaded (%v)", err)
	}
	if err := writeTranscript(vault, base, v.Title, v.URL, from, text); err != nil {
		return "skipped: " + err.Error()
	}
	const anchor = "**How becky handled it:**"
	if strings.Contains(raw, anchor) {
		raw = strings.Replace(raw, anchor, transcriptLink(base, from)+"\n\n"+anchor, 1)
		if err := os.WriteFile(note, []byte(raw), 0o644); err != nil {
			return "transcript saved, but the note link was not added: " + err.Error()
		}
	}
	return "transcript added (" + from + ")"
}
