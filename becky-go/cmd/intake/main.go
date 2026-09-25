// becky-intake — turn Jordan's "ai-useful" YouTube playlist into Obsidian notes,
// routed by a local System One model instead of one fixed pipeline.
//
//	becky-intake <playlist-or-video-url> [--limit 3] [--state F] [--vault DIR] [--temp DIR] [--json]
//
// Per new video:
//  1. code measures the facts (length, links, GitHub repos, chapters) from yt-dlp metadata;
//  2. Laya (becky-decide) picks the route: links or speech, and code vetoes a shaky "links";
//  3. every linked repo is read (gh api); a local embedding model matches video + repos to pains.json;
//     the speech route downloads the AUDIO to TEMP\<id>, becky-transcribe it, and local Gemma-4
//     E4B writes the step list; the TEMP folder is then deleted by code, success or failure;
//  4. one Obsidian note per video + one line in the state file so it is never redone.
//
// No video file is kept. Jordan's global yt-dlp.conf is never read or changed
// (every call passes --ignore-config). Exit codes: 0 ok (even if some videos
// degraded), 1 error, 2 usage.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/llmlocal"
	"becky-go/internal/systemone"
)

const (
	defaultVault = `C:\Users\only1\Documents\Obsidian\browser_data\YouTube`
	defaultTemp  = `X:\AI-2\becky-tools\research\playlist-intake\TEMP`
	defaultState = `X:\AI-2\becky-tools\research\playlist-intake\seen.json`
	// maxTranscriptChars keeps transcript + prompt inside Gemma's 16k context.
	maxTranscriptChars = 40000
)

type result struct {
	ID       string        `json:"id"`
	Title    string        `json:"title"`
	Facts    facts         `json:"facts"`
	Route    routeDecision `json:"route"`
	Why      *painMatch    `json:"why,omitempty"` // the pain this video most likely speaks to
	Repos    []repo        `json:"repos,omitempty"`
	Steps    string        `json:"steps,omitempty"`
	Note     string        `json:"note_path,omitempty"`
	Degraded []string      `json:"degraded,omitempty"`
}

func main() {
	limit := flag.Int("limit", 3, "process at most this many unseen videos per run")
	statePath := flag.String("state", defaultState, "JSON file of video ids already processed")
	painsPath := flag.String("pains", defaultPains, "JSON list of Jordan's pain points to match against")
	vault := flag.String("vault", defaultVault, "folder the Obsidian notes are written to")
	temp := flag.String("temp", defaultTemp, "folder named TEMP for downloads (emptied by code)")
	asJSON := flag.Bool("json", false, "print the full JSON result")
	dryRun := flag.Bool("dry-run", false, "only decide each video's route; no downloads, notes or state")
	pick := flag.String("ids", "", "comma-separated video ids to process instead of the unseen ones (for testing)")
	// Accept the URL before or after the flags (becky-scout's calling style).
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		args = append(args[1:], args[0])
	}
	_ = flag.CommandLine.Parse(args)
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: becky-intake <playlist-or-video-url> [--limit N] [--state F] [--vault DIR] [--temp DIR] [--json]")
		os.Exit(2)
	}
	if _, err := tempDirFor(*temp, "aaaaaaaaaaa"); err != nil {
		beckyio.Fatalf("%v", err)
	}
	sweepTemp(*temp)

	sys1 := systemone.New()
	if err := sys1.Available(); err != nil {
		beckyio.Fatalf("%v", err)
	}
	pains, err := loadPains(*painsPath)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}
	seen := loadSeen(*statePath)
	var ids []string
	if *pick != "" {
		ids, seen = strings.Split(*pick, ","), map[string]bool{}
	} else if _, ids, err = playlistIDs(flag.Arg(0)); err != nil {
		beckyio.Fatalf("could not read the playlist: %v", err)
	}
	var todo []string
	for _, id := range ids {
		if !seen[id] && len(todo) < *limit {
			todo = append(todo, id)
		}
	}
	fmt.Fprintf(os.Stderr, "%d videos in list, %d already done, processing %d\n", len(ids), len(seen), len(todo))

	cfg := config.Load()
	ctx := context.Background()
	var results []result
	for i, id := range todo {
		if i > 0 {
			time.Sleep(5 * time.Second) // gentle on YouTube
		}
		if *dryRun {
			res := result{ID: id}
			if v, err := fetchVideo(id); err != nil {
				res.Degraded = []string{err.Error()}
			} else {
				res.Title, res.Facts = v.Title, measure(v)
				res.Route, _ = decideRoute(ctx, sys1, v, res.Facts)
			}
			results = append(results, res)
			fmt.Fprintf(os.Stderr, "%s  %5.1f min %3d repos  %-12s %3.0f%%  %s\n", id, res.Facts.Minutes, len(res.Facts.Repos),
				res.Route.Route, 100*res.Route.Probabilities[res.Route.Route], res.Title)
			continue
		}
		res := processVideo(ctx, cfg, sys1, pains, id, *vault, *temp)
		results = append(results, res)
		if res.Note != "" {
			seen[id] = true
			saveSeen(*statePath, seen)
		}
		fmt.Fprintf(os.Stderr, "%s  %-12s %s\n", id, res.Route.Route, res.Title)
	}
	if *asJSON {
		beckyio.PrintJSON(results)
	}
}

func processVideo(ctx context.Context, cfg config.Config, sys1 systemone.Runner, pains []pain, id, vault, temp string) result {
	res := result{ID: id}
	v, err := fetchVideo(id)
	if err != nil {
		res.Degraded = append(res.Degraded, "metadata: "+err.Error())
		return res
	}
	res.Title = v.Title
	res.Facts = measure(v)
	res.Route, err = decideRoute(ctx, sys1, v, res.Facts)
	if err != nil {
		res.Degraded = append(res.Degraded, "routing: "+err.Error())
		return res
	}

	if res.Route.Route == routeSpeech {
		err := withTempVideo(temp, id, func(audio string) error {
			text, err := transcribe(audio)
			if err != nil {
				return err
			}
			if text == "" { // music-only or silent: nothing for Gemma to summarise
				res.Degraded = append(res.Degraded, "No speech found in this video (music or on-screen text only). "+
					"Reading on-screen text is not built yet, so only the title, description and links were used.")
				return nil
			}
			res.Steps, err = summarizeSteps(ctx, cfg, v, text)
			return err
		})
		if err != nil {
			res.Degraded = append(res.Degraded, "tutorial: "+err.Error())
		}
	}
	// Repos are read on every route that links any: a tutorial's repo is still
	// worth matching, and it costs no download.
	if len(res.Facts.Repos) > 0 {
		for _, name := range res.Facts.Repos {
			res.Repos = append(res.Repos, fetchRepo(name))
		}
	}
	// One embedding pass: the video itself, then every readable repo.
	texts := []string{videoText(v, res.Steps)}
	var idx []int
	for i, r := range res.Repos {
		if r.Err == "" {
			texts = append(texts, r.text())
			idx = append(idx, i)
		}
	}
	if m, err := matchPains(ctx, pains, texts); err != nil {
		res.Degraded = append(res.Degraded, "pain matching: "+err.Error())
	} else {
		res.Why = &m[0]
		for k, i := range idx {
			res.Repos[i].Match = m[k+1]
		}
		sort.SliceStable(res.Repos, func(a, b int) bool { return res.Repos[a].Match.Sim > res.Repos[b].Match.Sim })
	}
	path, err := writeNote(vault, v, res)
	if err != nil {
		res.Degraded = append(res.Degraded, "note: "+err.Error())
		return res
	}
	res.Note = path
	return res
}

func videoText(v video, steps string) string {
	s := v.Title + "\n" + v.Description + "\n" + steps
	if len(s) > 2000 {
		s = s[:2000]
	}
	return s
}

func transcribe(audio string) (string, error) {
	out, err := exec.Command("becky-transcribe", audio, "--format", "txt").Output()
	if err != nil {
		return "", fmt.Errorf("becky-transcribe: %v", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// summarizeSteps is the one text-writing step, so it goes to Gemma-4 E4B (a
// System One model cannot write).
func summarizeSteps(ctx context.Context, cfg config.Config, v video, transcript string) (string, error) {
	if len(transcript) > maxTranscriptChars {
		transcript = transcript[:maxTranscriptChars]
	}
	c := llmlocal.NewClientCtx(cfg.GemmaModel, cfg.LlamaServer, 16384, nil)
	if err := c.Available(); err != nil {
		return "", err
	}
	system := "You turn a tutorial transcript into a short, exact how-to. Only use what the transcript says. " +
		"Output markdown: a one-sentence summary, then a numbered list of steps, then a list named 'Tools and settings' " +
		"with every tool, command, model or setting that is named. If the transcript is not a tutorial, say so in one line."
	user := fmt.Sprintf("Video: %s (%s)\n\nTranscript:\n%s", v.Title, v.Channel, transcript)
	return c.Chat(ctx, system, user, llmlocal.Options{MaxTokens: 1200})
}

func loadSeen(path string) map[string]bool {
	seen := map[string]bool{}
	raw, err := os.ReadFile(path)
	if err != nil {
		return seen
	}
	var ids []string
	if json.Unmarshal(raw, &ids) == nil {
		for _, id := range ids {
			seen[id] = true
		}
	}
	return seen
}

func saveSeen(path string, seen map[string]bool) {
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	b, _ := json.MarshalIndent(ids, "", " ")
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, b, 0o644)
}
