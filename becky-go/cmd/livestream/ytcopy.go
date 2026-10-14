package main

// ytcopy.go - --video: the words that go with the upload, from openshorts'
// YouTube Studio (thumbnail.generate_youtube_description, 2026-10-13): title
// options, a short description and chapters. One change from openshorts: the
// model picks WHICH line starts each chapter (by id) and code works out the time
// on the finished edit - a model is bad at timestamps, and the times must be
// the edit's, not the raw recording's.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var copySchema = jsonSchema("youtube_copy", map[string]any{"type": "object", "properties": map[string]any{
	"titles":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
	"summary": map[string]any{"type": "string"},
	"chapters": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{
		"id":    map[string]any{"type": "integer"},
		"title": map[string]any{"type": "string"},
	}, "required": []string{"id", "title"}}},
}, "required": []string{"titles", "summary", "chapters"}})

type chapter struct {
	At    float64
	Title string
}

// editTime is where source time t plays in the edit laid out from pieces in
// order (-1 when it was cut).
func editTime(pieces []span, t float64) float64 {
	tl := 0.0
	for _, p := range pieces {
		if t >= p.A && t < p.B {
			return tl + t - p.A
		}
		tl += p.B - p.A
	}
	return -1
}

// chapterList turns the model's picks into YouTube chapters: the first at 0:00,
// in edit order, at least 10 s apart; fewer than 3 is no chapters at all
// (YouTube ignores the list then).
func chapterList(picks []chapter) []chapter {
	var out []chapter
	for _, c := range picks {
		if c.At < 0 || strings.TrimSpace(c.Title) == "" {
			continue
		}
		if len(out) == 0 {
			c.At = 0
		} else if c.At-out[len(out)-1].At < 10 {
			continue
		}
		out = append(out, c)
	}
	if len(out) < 3 {
		return nil
	}
	return out
}

// youtubeCopy writes becky-edit\youtube-upload.txt.
func (r *run) youtubeCopy(ss []Sentence, ds []Decision, pieces []span) {
	path := filepath.Join(r.work, "youtube-upload.txt")
	m, err := r.coarseModel().open()
	if err != nil {
		r.logf("upload text: %v", err)
		return
	}
	defer m.client.Close()
	type line struct {
		id int
		at float64
	}
	var kept []line
	for _, d := range ds {
		if t := editTime(pieces, ss[d.ID].Start+0.05); d.Keep && t >= 0 {
			kept = append(kept, line{d.ID, t})
		}
	}
	// edit order (a moved part plays earlier than its source time)
	for i := 1; i < len(kept); i++ {
		for j := i; j > 0 && kept[j].at < kept[j-1].at; j-- {
			kept[j], kept[j-1] = kept[j-1], kept[j]
		}
	}
	var sb strings.Builder
	for _, k := range kept {
		fmt.Fprintf(&sb, "[id %d | %s] %s\n", k.id, clock(k.at), ss[k.id].Text)
	}
	user := "THE FINISHED VIDEO, line by line, with the time each line plays:\n" + sb.String() +
		"\nWrite what goes with the upload, in his voice and plain words:\n" +
		"- titles: 5 title options, at most 70 characters each, specific to what he actually says (a name, a claim, an event), no clickbait he would not stand behind.\n" +
		"- summary: 2-3 sentences for the top of the description.\n" +
		"- chapters: 4 to 10 chapters in the order they play. id is the line where the chapter starts (the first chapter starts at the first line); title is at most 6 words."
	raw, err := m.chat(videoRole, user, copySchema, 900)
	var ans struct {
		Titles   []string `json:"titles"`
		Summary  string   `json:"summary"`
		Chapters []struct {
			ID    int    `json:"id"`
			Title string `json:"title"`
		} `json:"chapters"`
	}
	if err != nil || json.Unmarshal([]byte(raw), &ans) != nil {
		r.logf("upload text: the model's answer was unreadable (%v)", err)
		return
	}
	var picks []chapter
	for _, c := range ans.Chapters {
		if c.ID >= 0 && c.ID < len(ss) {
			picks = append(picks, chapter{editTime(pieces, ss[c.ID].Start+0.05), c.Title})
		}
	}
	var b strings.Builder
	b.WriteString("TITLE OPTIONS\n")
	for _, t := range ans.Titles {
		b.WriteString("- " + t + "\n")
	}
	b.WriteString("\nDESCRIPTION\n" + strings.TrimSpace(ans.Summary) + "\n")
	if cs := chapterList(picks); len(cs) > 0 {
		b.WriteString("\n")
		for _, c := range cs {
			fmt.Fprintf(&b, "%s %s\n", ytClock(c.At), c.Title)
		}
	}
	_ = os.WriteFile(path, []byte(b.String()), 0o644)
	r.logf("upload text (titles, description, chapters): %s", path)
}

// ytClock is YouTube's chapter time: m:ss, or h:mm:ss past an hour.
func ytClock(t float64) string {
	s := int(t)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
