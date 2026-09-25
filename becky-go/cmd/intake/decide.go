package main

import (
	"context"
	"fmt"
	"strings"

	"becky-go/internal/systemone"
)

// Routes. The only decision that changes the work is where the video's value
// lives: in the links its description lists (read those, skip the download),
// or in what is said and shown (download the audio to TEMP and transcribe).
// Linked repos are read on BOTH routes, and no route skips a video: Jordan
// saved every one of them for a reason.
const (
	routeLinks  = "links"
	routeSpeech = "speech"
)

// The links route skips the download, so it is taken only when two signals
// agree: the description links enough GitHub repos to carry the content (code
// counts) AND Laya is confident. A wrong "links" loses the video's content; a
// wrong "speech" only costs a transcription, so every doubt goes to speech.
// Tuned 2026-09-25 on 13 playlist videos: Laya alone sent 33- and 46-minute
// talks to "links" because their descriptions were full of sponsor links.
const (
	minReposForLinksRoute = 3
	minLinksConfidence    = 0.7
)

var routeQuestion = systemone.Choice("Where is this video's useful content?",
	systemone.Option{Key: routeLinks, Desc: "in the list of projects or links in its description; reading those links covers it"},
	systemone.Option{Key: routeSpeech, Desc: "in what is said or shown in the video itself"},
)

func routeState(v video, f facts) string {
	desc := v.Description
	if len(desc) > 1200 {
		desc = desc[:1200]
	}
	return fmt.Sprintf("Title: %s\nChannel: %s\nLength: %s\nLinks in description: %d (GitHub repos: %d)\nChapters: %d\nDescription:\n%s",
		v.Title, v.Channel, lengthWords(f.Minutes), f.Links, len(f.Repos), f.Chapters, desc)
}

// lengthWords states length in words: Laya reads numbers as text (Jev weakness #2).
func lengthWords(min float64) string {
	switch {
	case min < 1:
		return "under a minute (a short)"
	case min < 5:
		return "a few minutes"
	case min < 20:
		return fmt.Sprintf("about %.0f minutes", min)
	default:
		return fmt.Sprintf("long, about %.0f minutes", min)
	}
}

type routeDecision struct {
	Route         string             `json:"route"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    float64            `json:"confidence"`
	Note          string             `json:"note,omitempty"`
}

// decideRoute asks Laya, then applies the one fact code owns: the links route
// needs links.
func decideRoute(ctx context.Context, r systemone.Runner, v video, f facts) (routeDecision, error) {
	resp, err := r.Decide(ctx, systemone.Request{State: routeState(v, f), Questions: map[string]systemone.Question{"route": routeQuestion}})
	if err != nil {
		return routeDecision{}, err
	}
	a := resp.Answers["route"]
	return guardRoute(a.Choice, a.Probabilities, a.Confidence, f), nil
}

// guardRoute is the code half of the decision (pure, unit-tested).
func guardRoute(choice string, probs map[string]float64, conf float64, f facts) routeDecision {
	d := routeDecision{Route: choice, Probabilities: probs, Confidence: conf}
	if d.Route != routeLinks {
		d.Route = routeSpeech
		return d
	}
	switch {
	case len(f.Repos) < minReposForLinksRoute:
		d.Note = fmt.Sprintf("model said links, but the description links only %d GitHub repos", len(f.Repos))
		d.Route = routeSpeech
	case probs[routeLinks] < minLinksConfidence:
		d.Note = fmt.Sprintf("model leaned links at only %.0f%%, so becky transcribed it to be safe", 100*probs[routeLinks])
		d.Route = routeSpeech
	}
	return d
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "|", "/")), " ")
}
