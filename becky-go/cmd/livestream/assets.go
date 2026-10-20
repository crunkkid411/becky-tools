package main

// assets.go - --video, the contextual pass. Jordan, 2026-10-14 (user-feedback.md):
// "at 2:01 I say; 'he blackmailed me into an apology saying this' - my intent
// there is to insert the video clip of him blackmailing me into an apology.
// Anytime it seems that my intent is to overlay an asset or insert a recording
// of something else, it should be inserted or overlayed accordingly if I've
// provided the clip or asset ... If I have NOT provided any additional assets in
// the project folder or in a notes doc, THAT is when it's appropriate to create
// a marker ... Keep marker labels short, only a few words ... A .txt file called
// MARKERS.txt should be created in the same folder as the project anytime
// markers are created. The MARKERS.txt should list all markers with the exact
// timestamp and further details and context."
//
//   1. The lead model reads the transcript in parts and names every line where
//      he means something to be SHOWN, with a 2-4 word label.
//   2. The files he provided: pictures and videos in the footage folder (not
//      the recording itself) and any file a notes doc in it names. The model
//      picks one for a line only from that list; code checks the name.
//   3. A picture (or a video with no sound) goes over his picture on a B-ROLL
//      track; a video with sound is inserted, everything after it moves later.
//      Anything not provided is a marker, and MARKERS.txt lists them all.

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// showLine is one place he means something to be shown.
type showLine struct {
	ID    int     `json:"id"`
	Label string  `json:"label"`
	What  string  `json:"what"`
	Asset string  `json:"asset,omitempty"` // the provided file, when there is one
	At    float64 `json:"at"`              // when he says it in the recording
}

var showSchema = jsonSchema("show", map[string]any{"type": "object", "properties": map[string]any{
	"show": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{
		"id":    map[string]any{"type": "integer"},
		"label": map[string]any{"type": "string"},
		"what":  map[string]any{"type": "string"},
	}, "required": []string{"id", "label", "what"}}},
}, "required": []string{"show"}})

var pickSchema = jsonSchema("asset", map[string]any{"type": "object", "properties": map[string]any{
	"file": map[string]any{"type": "string"}}, "required": []string{"file"}})

var mediaExt = map[string]bool{".mp4": true, ".mov": true, ".mkv": true, ".webm": true, ".avi": true, ".m4v": true,
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".bmp": true}

// providedAssets lists the files Jordan gave for this video: pictures and
// videos in the footage folder (not the recording, nothing becky made) and the
// existing files a .txt/.md note in it names.
func providedAssets(dir, footage string) []string {
	stem := strings.TrimSuffix(filepath.Base(footage), filepath.Ext(footage))
	var out []string
	add := func(p string) {
		if st, err := os.Stat(p); err == nil && !st.IsDir() && mediaExt[strings.ToLower(filepath.Ext(p))] && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(strings.ToLower(name), strings.ToLower(stem)) {
			continue // the recording and its synced/proxy copies
		}
		if ext := strings.ToLower(filepath.Ext(name)); ext == ".txt" || ext == ".md" {
			if forBecky.MatchString(name) || strings.EqualFold(name, "MARKERS.txt") {
				continue // his feedback / workflow notes name screenshots for becky, not things to show
			}
			b, _ := os.ReadFile(filepath.Join(dir, name))
			for _, m := range noteFile.FindAllString(string(b), -1) {
				m = strings.Trim(m, "\"'`<>()[] ")
				if filepath.IsAbs(m) {
					add(m)
				} else {
					add(filepath.Join(dir, m))
				}
			}
			continue
		}
		add(filepath.Join(dir, name))
	}
	return out
}

// noteFile: a file name with a picture/video extension inside a note.
// forBecky: notes written to becky about the editing (user-feedback.md names
// his screenshots censor-example.JPG and zero-crossing-errors.JPG; obs-workflow.md
// is a spec). ponytail: matched by name; a note to becky named otherwise would
// still offer its files to the model, which picks only a file that fits a line.
var forBecky = regexp.MustCompile(`(?i)feedback|workflow|instruction|readme`)

// pointsAt: a sentence where he points at something the viewer should see right
// then - "an apology saying this.", "Here's this short", "this is the stuff he's
// been threatening". The 12B also lists everything he merely mentions (54
// places in the goodbye update, 3 of them pointed at); a marker is a chore for
// Jordan, so only a sentence that points gets one.
var pointsAt = regexp.MustCompile(`(?i)\b(?:this|these)[.!?"')]*$|\bhere(?:['’]s| is| are)\b|\b(?:this|these) (?:is|are) (?:the|what|how|where|him|his|her|it)\b|\blook at\b|\bcheck (?:this|it) out\b|\byou can see\b|\bwatch this\b|\bon (?:the )?screen\b`)

func pointed(ss []Sentence, ls []showLine) []showLine {
	return slices.DeleteFunc(slices.Clone(ls), func(l showLine) bool {
		return l.ID < 0 || l.ID >= len(ss) || !pointsAt.MatchString(strings.TrimSpace(ss[l.ID].Text))
	})
}

var noteFile = regexp.MustCompile(`(?i)(?:[a-z]:\\[^\r\n"<>|]*?|[\w .'()-]+?)\.(?:mp4|mov|mkv|webm|avi|m4v|jpe?g|png|gif|webp|bmp)\b`)

// showLines finds the lines and matches what he provided (cached per edit).
func (r *run) showLines(ss []Sentence, assets []string, fresh bool) []showLine {
	path := filepath.Join(r.work, "show-"+r.tag+".json")
	stampPrompts(path, videoRole, promptThingsToShowFind, promptThingsToShowPickTheFile)
	var saved struct {
		Sentences int        `json:"sentences"`
		Assets    []string   `json:"assets"`
		Lines     []showLine `json:"lines"`
	}
	if b, err := os.ReadFile(path); err == nil && !fresh && json.Unmarshal(b, &saved) == nil && saved.Sentences == len(ss) && slices.Equal(saved.Assets, assets) {
		lines := pointed(ss, saved.Lines)
		r.logf("contextual pass: using the earlier reading (%d place(s) to show something)", len(lines))
		return lines
	}
	spec := r.coarseModel()
	m, err := spec.open()
	if err != nil {
		r.logf("contextual pass: %s could not start (%v) - no markers this time", spec.name, err)
		return nil
	}
	r.logf("contextual pass: %s is looking for things you mean to show...", spec.name)
	var lines []showLine
	missed := 0
	for _, p := range parts(len(ss)) {
		a, b := p[0], p[1]
		if !slices.ContainsFunc(ss[a:b], func(s Sentence) bool { return pointsAt.MatchString(strings.TrimSpace(s.Text)) }) {
			continue // he points at nothing in this part
		}
		user := fill(promptThingsToShowFind, "{TRANSCRIPT}", sentenceLines(ss, a, b))
		var ans struct {
			Show []showLine `json:"show"`
		}
		// 1200 tokens: at 400 a part with several things to show was cut off
		// mid-answer (goodbye update 6:04-8:28). One more try before giving up.
		var raw string
		for try := 0; try < 2; try++ {
			if raw, err = m.chat(videoRole, user, showSchema, 1200); err == nil {
				if err = json.Unmarshal([]byte(raw), &ans); err == nil {
					break
				}
			}
		}
		if err != nil {
			missed++
			r.logf("  contextual pass: %s could not read %s-%s: %v (%s)", spec.name, clock(ss[a].Start), clock(ss[b-1].End), err, tail(raw, 80))
			continue
		}
		for _, s := range ans.Show {
			if s.ID >= a && s.ID < b && !slices.ContainsFunc(lines, func(x showLine) bool { return x.ID == s.ID }) {
				s.Label, s.At = shortLabel(s.Label), ss[s.ID].Start
				lines = append(lines, s)
			}
		}
	}
	slices.SortFunc(lines, func(x, y showLine) int { return x.ID - y.ID })
	lines = pointed(ss, lines)
	if len(assets) > 0 {
		var names []string
		for _, a := range assets {
			names = append(names, filepath.Base(a))
		}
		for i := range lines {
			user := fill(promptThingsToShowPickTheFile,
				"{SAYS}", ss[lines[i].ID].Text, "{WHAT}", lines[i].What, "{FILES}", strings.Join(names, "\n"))
			raw, err := m.chat(videoRole, user, pickSchema, 80)
			var ans struct {
				File string `json:"file"`
			}
			if err == nil && json.Unmarshal([]byte(raw), &ans) == nil {
				if k := slices.Index(names, strings.TrimSpace(ans.File)); k >= 0 {
					lines[i].Asset = assets[k]
				}
			}
		}
	}
	r.logf("  %d place(s) to show something, %d with a file you provided", len(lines), countAssets(lines))
	if missed > 0 {
		return lines // not saved: the next run reads the parts it missed
	}
	saved.Sentences, saved.Assets, saved.Lines = len(ss), assets, lines
	writeJSON(path, saved)
	return lines
}

func countAssets(ls []showLine) int {
	n := 0
	for _, l := range ls {
		if l.Asset != "" {
			n++
		}
	}
	return n
}

// shortLabel keeps a marker label to a few words (Jordan: "difficult to read
// markers longer than one sentence").
func shortLabel(s string) string {
	ws := strings.Fields(strings.Trim(s, ".\"' "))
	if len(ws) > 4 {
		ws = ws[:4]
	}
	return strings.ToLower(strings.Join(ws, " "))
}

// showAt is where a line's thing goes on the timeline: right after the line,
// on the cut that follows it when that cut is within a second.
func showAt(ps []piece, s Sentence) (float64, bool) {
	_, t1, ok := toTimeline(ps, s.Start, s.End)
	if !ok {
		return 0, false
	}
	for _, p := range ps {
		if end := p.TL + (p.Out - p.In); t1 >= p.TL && t1 <= end+1e-6 && end-t1 <= 1.0 {
			return end, true
		}
	}
	return t1, true
}

// placement is one provided file on the timeline.
type placement struct {
	Line   showLine
	Frame  int64
	Frames int64 // its length on the timeline
	Insert bool  // a video with sound: everything after it moves later
}

// probeAsset: its length in seconds (0 for a picture) and whether it has sound.
func probeAsset(ffprobe, file string) (float64, bool) {
	out, err := exec.Command(ffprobe, "-v", "error", "-show_entries", "format=duration:stream=codec_type", "-of", "default=nw=1", file).Output()
	if err != nil {
		return 0, false
	}
	dur, sound := 0.0, false
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimSpace(l)
		if v, ok := strings.CutPrefix(l, "duration="); ok {
			dur, _ = strconv.ParseFloat(v, 64)
		}
		sound = sound || l == "codec_type=audio"
	}
	if ext := strings.ToLower(filepath.Ext(file)); ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".bmp" || ext == ".webp" {
		dur = 0
	}
	return dur, sound
}

const pictureSeconds = 4.0 // a provided picture stays this long

// placeAssets turns the lines with a file into placements; the others become
// markers. Positions are on the timeline as built; an insert moves everything
// after it, so placements are listed latest first (each insert leaves the
// positions before it untouched) and markers are shifted by the inserts before them.
func placeAssets(lines []showLine, ss []Sentence, ps []piece, fps float64, probe func(string) (float64, bool)) ([]placement, []mark, []showLine) {
	var pl []placement
	var ms []mark
	var marked []showLine // the line behind each marker
	for _, l := range lines {
		at, ok := showAt(ps, ss[l.ID])
		if !ok {
			continue
		}
		if l.Asset == "" {
			ms, marked = append(ms, mark{At: at, Label: l.Label}), append(marked, l)
			continue
		}
		dur, sound := probe(l.Asset)
		insert := dur > 0 && sound
		if dur <= 0 {
			dur = pictureSeconds
		}
		pl = append(pl, placement{Line: l, Frame: int64(math.Round(at * fps)), Frames: max(int64(math.Round(dur*fps)), 1), Insert: insert})
	}
	for i := range ms {
		f := int64(math.Round(ms[i].At * fps))
		for _, p := range pl {
			if p.Insert && p.Frame <= f {
				f += p.Frames
			}
		}
		ms[i].At = float64(f) / fps
	}
	slices.SortFunc(pl, func(a, b placement) int { return int(b.Frame - a.Frame) })
	return pl, ms, marked
}

// shiftAfterInserts moves a timeline time by the inserts at or before it.
func shiftAfterInserts(t float64, pl []placement, fps float64) float64 {
	f := int64(math.Round(t * fps))
	g := f
	for _, p := range pl {
		if p.Insert && p.Frame <= f {
			g += p.Frames
		}
	}
	return t + float64(g-f)/fps
}

// assetJob is BeckyFX.cs's lines for the placements.
func assetJob(pl []placement, speech int) string {
	var sb strings.Builder
	for _, p := range pl {
		verb := "overlay"
		if p.Insert {
			verb = "insert"
		}
		fmt.Fprintf(&sb, "%s\t%d\t%d\t%s\t%d\n", verb, p.Frame, p.Frames, p.Line.Asset, speech)
	}
	return sb.String()
}

// frameClock is Jordan's VEGAS ruler time: m:ss.ff (h:mm:ss.ff past an hour).
func frameClock(t, fps float64) string {
	f := int64(math.Round(t * fps))
	n := int64(math.Round(fps))
	s := f / n
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d.%02d", s/3600, s/60%60, s%60, f%n)
	}
	return fmt.Sprintf("%d:%02d.%02d", s/60, s%60, f%n)
}

// writeMarkers writes MARKERS.txt next to the project: every marker, its exact
// place on the timeline, what is missing and what he said there.
func writeMarkers(path string, marked []showLine, ss []Sentence, ms []mark, fps float64) error {
	if len(ms) == 0 {
		return nil
	}
	var sb strings.Builder
	sb.WriteString("MARKERS - things to add to this video (becky could not find them in the folder)\r\n")
	sb.WriteString("Times are on the VEGAS timeline of the edited project (minutes:seconds.frames).\r\n\r\n")
	for k, m := range ms {
		l := marked[k]
		fmt.Fprintf(&sb, "%s  %s\r\n    What to add: %s\r\n    You said (at %s in the recording): \"%s\"\r\n",
			frameClock(m.At, fps), m.Label, l.What, clock(ss[l.ID].Start), ss[l.ID].Text)
		if l.ID > 0 {
			fmt.Fprintf(&sb, "    Just before: \"%s\"\r\n", ss[l.ID-1].Text)
		}
		if l.ID+1 < len(ss) {
			fmt.Fprintf(&sb, "    Just after: \"%s\"\r\n", ss[l.ID+1].Text)
		}
		sb.WriteString("\r\n")
	}
	return os.WriteFile(path, []byte(sb.String()), 0o644)
}

// Prompts Jordan can edit (prompts.go).
var promptThingsToShowFind = P(dirYouTube, "Things to show - find", "THE TRANSCRIPT (one part):\n{TRANSCRIPT}"+
	"\nList every sentence where Jordan means something to be SHOWN on screen that is not him talking to the camera: a clip, a video, a livestream, a post, a screenshot, a message, a picture of something - \"saying this\", \"look at this\", \"here's what he posted\", \"he made a video saying...\" when he is about to show it.\n"+
	"- id: the sentence\n- label: 2 to 4 words naming the thing to show, like \"blackmail apology\" or \"kickflip website\"\n- what: one short sentence: what should be shown there\n"+
	"Only places where showing it is clearly his intent. Most parts have none: answer {\"show\": []}.")

var promptThingsToShowPickTheFile = P(dirYouTube, "Things to show - pick the file", "Jordan says: \"{SAYS}\"\nWhat he means to show: {WHAT}\n\nThe files he provided for this video:\n{FILES}\n\nWhich file is the thing he means? Answer with its exact name, or \"\" when none of them is.")
