package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// writeNote writes one Obsidian note per video: <upload date>_<id>.md.
func writeNote(vault string, v video, res result) (string, error) {
	if err := os.MkdirAll(vault, 0o755); err != nil {
		return "", err
	}
	date := v.UploadDate
	if len(date) == 8 {
		date = date[:4] + "-" + date[4:6] + "-" + date[6:]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "---\nsource: %s\nchannel: %q\nuploaded: %s\nprocessed: %s\nroute: %s\nroute_confidence: %.2f\ntool: becky-intake\n---\n\n",
		v.URL, v.Channel, date, time.Now().Format("2006-01-02 15:04"), res.Route.Route, res.Route.Confidence)
	fmt.Fprintf(&b, "# %s\n\n", v.Title)
	fmt.Fprintf(&b, "**How becky handled it:** %s. Length: %s, %d links, %d GitHub repos, %d chapters.\n",
		routeWords(res.Route.Route, res.Steps != ""), lengthWords(res.Facts.Minutes), res.Facts.Links, len(res.Facts.Repos), res.Facts.Chapters)
	fmt.Fprintf(&b, "Route odds (Laya): %s", probsLine(res.Route.Probabilities))
	if res.Route.Note != "" {
		fmt.Fprintf(&b, " (changed by code: %s)", res.Route.Note)
	}
	b.WriteString("\n\n")
	if res.Why != nil {
		fmt.Fprintf(&b, "**Why you probably saved it:** closest to your pain point %q (%s, similarity %.2f).\n\n", res.Why.Pain, res.Why.PainID, res.Why.Sim)
	}

	if res.Steps != "" {
		b.WriteString("## What it teaches (Gemma-4 E4B, from the transcript)\n\n")
		b.WriteString(strings.TrimSpace(res.Steps) + "\n\n")
	}
	if len(res.Repos) > 0 {
		b.WriteString("## Linked repos, closest to your pain points first\n\n")
		b.WriteString("| Match | Repo | What it is | Pain it might help | Stars | License |\n|---|---|---|---|---|---|\n")
		for _, r := range res.Repos {
			if r.Err != "" {
				fmt.Fprintf(&b, "| ? | [%s](https://github.com/%s) | could not read: %s | | | |\n", r.Name, r.Name, oneLine(r.Err))
				continue
			}
			fmt.Fprintf(&b, "| %.2f | [%s](https://github.com/%s) | %s | %s | %d | %s |\n",
				r.Match.Sim, r.Name, r.Name, oneLine(r.Description), r.Match.PainID, r.Stars, r.License)
		}
		b.WriteString("\nMatch is text similarity to your pain list (pains.json): a pointer to what to look at first, not a verdict.\n\n")
	}
	if len(res.Degraded) > 0 {
		b.WriteString("## What did not work\n\n")
		for _, d := range res.Degraded {
			fmt.Fprintf(&b, "- %s\n", d)
		}
		b.WriteString("\n")
	}
	path := filepath.Join(vault, fmt.Sprintf("%s_%s.md", date, v.ID))
	return path, os.WriteFile(path, []byte(b.String()), 0o644)
}

func routeWords(route string, haveSteps bool) string {
	switch {
	case route == routeLinks:
		return "The description lists what the video covers, so becky read the links instead of downloading it"
	case haveSteps:
		return "The value is in what is said, so becky transcribed it and wrote out what it teaches"
	default:
		return "becky tried to transcribe it, but got no usable text (see below)"
	}
}

func probsLine(p map[string]float64) string {
	var parts []string
	for _, k := range []string{routeLinks, routeSpeech} {
		parts = append(parts, fmt.Sprintf("%s %.0f%%", k, p[k]*100))
	}
	return strings.Join(parts, ", ")
}
