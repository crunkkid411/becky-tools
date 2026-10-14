package clippick

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Chat is one schema-enforced model call: system + user in, raw JSON out.
type Chat func(system, user string, schema map[string]any, maxTokens int) (string, error)

// Options are the clip length band and how many windows one scoring call reads
// (openshorts: 8 for Gemini, 3 for a local 4-8k-context model - a truncated
// prompt scores garbage silently).
type Options struct {
	MinSec, MaxSec float64
	ScoreBatch     int
	Logf           func(string, ...any)
}

const role = `You are a senior short-form video editor working for Hair Jordan, a YouTuber. You turn his long videos and livestreams into vertical shorts for YouTube Shorts, TikTok and Instagram Reels.`

const scorePrompt = `RANK these candidate windows of the transcript by how well each would work as a standalone short.

Rules:
- Score EVERY window: exactly one entry per input window, with the id you were given. Do not drop the weak ones - say they are weak.
- score is an integer from 0 to 100, and the ranking is what matters: use the whole range instead of clustering. Most windows of a normal video are not clippable, so reserve 70+ for the ones that pass the test below, and put filler, housekeeping, outros, rambling transitions and low-signal padding under 30 even when the topic is interesting.
- THE 2-SECOND TEST is the main criterion: would the first 2 seconds of this moment make a cold viewer (no context) keep watching? Windows that only work with earlier context score low.
- Prefer windows with strong hooks, conflict, surprise, outrage, emotion, novelty, big numbers, or a clear payoff.

VIDEO_DURATION_SECONDS: %.0f
WINDOWS_JSON:
%s`

const detailPrompt = `Choose the BEST short clips from these shortlisted candidate windows.

CLIP RULES:
- Each clip is %g to %g seconds long, in absolute seconds from the start of the source video, and stays inside its candidate window.
- THE 2-SECOND RULE: the clip MUST open on its strongest moment. If the first 2 seconds would not stop a cold viewer from scrolling, move the start or skip the clip.
- Start slightly before the hook and end slightly after the payoff. Never cut in the middle of a word or phrase.
- STANDS ALONE: the clip must make sense to someone who has seen nothing else. If it opens on a pronoun, a "that", a "so anyway", or an answer whose question was asked earlier, move the START earlier to where the idea begins, or skip it. Never fix it by cutting the ending short: a clip that loses its payoff has traded down.
- HOW MANY: return %d to %d clips. Work through EVERY window - they were already scored as the best moments of the video. Two clips from one window are fine when they are genuinely different moments. Only fall short when the material truly does not hold them, and never pad with a clip you would not post yourself.
- DIVERSITY: never two clips that make the same point, tell the same story or land the same joke.

COPY (in the language of the transcript):
- viral_hook_text: max 10 words, about THIS moment (the concrete thing, claim, number or name in it), never a line that would fit any clip of the video. Patterns: an open question, a hot take, a number shock, a story loop, a POV.
- video_title_for_youtube_short: max 100 characters, curiosity-driven, no fake claims.
- video_description_for_tiktok / video_description_for_instagram: 1-2 punchy sentences that tease the payoff, then 3-5 relevant hashtags.
- predicted_score: an honest 0-100 estimate of how well it will do.
- why: one sentence, max 20 words, naming what makes THIS moment a clip.

VIDEO_DURATION_SECONDS: %.0f
CANDIDATE_WINDOWS_JSON:
%s`

var scoreSchema = schema("window_scores", map[string]any{"type": "object", "properties": map[string]any{
	"windows": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{
		"id":     map[string]any{"type": "string"},
		"score":  map[string]any{"type": "integer"},
		"reason": map[string]any{"type": "string"},
	}, "required": []string{"id", "score", "reason"}}},
}, "required": []string{"windows"}})

var detailSchema = schema("clips", map[string]any{"type": "object", "properties": map[string]any{
	"shorts": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{
		"start":                           map[string]any{"type": "number"},
		"end":                             map[string]any{"type": "number"},
		"source_window_id":                map[string]any{"type": "string"},
		"predicted_score":                 map[string]any{"type": "integer"},
		"viral_hook_text":                 map[string]any{"type": "string"},
		"video_title_for_youtube_short":   map[string]any{"type": "string"},
		"video_description_for_tiktok":    map[string]any{"type": "string"},
		"video_description_for_instagram": map[string]any{"type": "string"},
		"why":                             map[string]any{"type": "string"},
	}, "required": []string{"start", "end", "source_window_id", "predicted_score", "viral_hook_text", "video_title_for_youtube_short", "video_description_for_tiktok", "video_description_for_instagram", "why"}}},
}, "required": []string{"shorts"}})

func schema(name string, s map[string]any) map[string]any {
	return map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": name, "schema": s}}
}

type scored struct {
	ID     string `json:"id"`
	Score  int    `json:"score"`
	Reason string `json:"reason"`
}

// Pick runs both passes and returns the clips, snapped and deduplicated, in
// time order.
func Pick(chat Chat, segs []Segment, words []Word, duration float64, o Options) ([]Clip, error) {
	logf := o.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if o.MinSec <= 0 {
		o.MinSec = 15
	}
	if o.MaxSec <= o.MinSec {
		o.MaxSec = 60
	}
	windows := BuildWindows(segs, duration, max(90, o.MaxSec*1.5), 30)
	logf("%d scoring windows", len(windows))

	// pass 1: score every window
	var all []scored
	for _, b := range ScoreBatches(windows, max(o.ScoreBatch, 1)) {
		raw, err := chat(role, fmt.Sprintf(scorePrompt, duration, payload(b)), scoreSchema, 120*len(b)+200)
		if err != nil {
			return nil, fmt.Errorf("scoring %s-%s: %w", b[0].ID, b[len(b)-1].ID, err)
		}
		var ans struct {
			Windows []scored `json:"windows"`
		}
		if err := json.Unmarshal([]byte(raw), &ans); err != nil {
			return nil, fmt.Errorf("scoring %s: unreadable answer: %w", b[0].ID, err)
		}
		all = append(all, ans.Windows...)
	}
	byID := map[string]Window{}
	for _, w := range windows {
		byID[w.ID] = w
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Score > all[j].Score })
	target := ShortlistTarget(duration)
	var short []Window
	scoreOf := map[string]scored{}
	for _, s := range all {
		if w, ok := byID[s.ID]; ok && len(short) < target {
			if _, dup := scoreOf[s.ID]; dup {
				continue
			}
			short = append(short, w)
			scoreOf[s.ID] = s
		}
	}
	if len(short) == 0 {
		short = windows[:min(target, len(windows))]
	}
	sort.Slice(short, func(i, j int) bool { return short[i].Start < short[j].Start })
	logf("shortlisted %d of %d windows", len(short), len(windows))

	// pass 2: the clips inside the shortlist
	lo, hi := ClipCountTargets(len(short))
	clips, err := detail(chat, short, lo, hi, duration, o)
	if err != nil {
		return nil, err
	}
	if len(clips) < lo { // the floor is enforced in code: the unused windows get one more ask
		used := map[string]bool{}
		for _, c := range clips {
			used[c.Window] = true
		}
		var spare []Window
		for _, w := range short {
			if !used[w.ID] {
				spare = append(spare, w)
			}
		}
		if len(spare) > 0 {
			miss := lo - len(clips)
			logf("detail gave %d of %d clips; asking the %d unused window(s) for %d more", len(clips), lo, len(spare), miss)
			if extra, err := detail(chat, spare, miss, max(miss, len(spare)), duration, o); err == nil {
				clips = append(clips, extra...)
			}
		}
	}
	sort.SliceStable(clips, func(i, j int) bool { return clips[i].Start < clips[j].Start })
	clips = TrimToBest(clips, hi)
	for i := range clips {
		c := &clips[i]
		c.Proposed = [2]float64{c.Start, c.End}
		c.Start, c.End = Snap(c.Start, c.End, words, duration, o.MinSec, o.MaxSec)
		if s, ok := scoreOf[c.Window]; ok {
			c.WindowScore, c.WindowRationle = s.Score, s.Reason
		}
	}
	return DedupeOverlapping(clips, 0.25), nil
}

func detail(chat Chat, ws []Window, lo, hi int, duration float64, o Options) ([]Clip, error) {
	raw, err := chat(role, fmt.Sprintf(detailPrompt, o.MinSec, o.MaxSec, lo, hi, duration, payload(ws)), detailSchema, 350*hi+300)
	if err != nil {
		return nil, fmt.Errorf("picking the clips: %w", err)
	}
	var ans struct {
		Shorts []Clip `json:"shorts"`
	}
	if err := json.Unmarshal([]byte(raw), &ans); err != nil {
		return nil, fmt.Errorf("picking the clips: unreadable answer: %w", err)
	}
	var out []Clip
	for _, c := range ans.Shorts {
		if c.End > c.Start && c.Start >= 0 && c.End <= duration+1 {
			out = append(out, c)
		}
	}
	return out, nil
}

func payload(ws []Window) string {
	b, _ := json.Marshal(ws)
	return strings.ReplaceAll(string(b), "},{", "},\n{")
}
