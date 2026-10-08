// becky-besttake - pick the finished take among repeated attempts in a
// recording, from its becky-transcribe JSON. Two signals corroborate each
// restart: a System One decision model (hosted, $5/month cap) and a
// model-free opening-words match; both scores are in the output.
//
//	becky-besttake <transcript.json> [--model id] [--window 6] [--out picks.json]
//
// Output: {"lines": [...], "groups": N, "retake_groups": N, "cut_lines": N}.
// Each line says keep or cut, which attempt it is, and why. Unsure picks are
// flagged for Jordan, never silently decided. Exit codes: 0 ok, 1 error, 2 usage.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"becky-go/internal/beckyio"
	"becky-go/internal/systemone"
)

type result struct {
	Source       string `json:"source"`
	Model        string `json:"model"`
	Lines        []Line `json:"lines"`
	Groups       int    `json:"groups"`
	RetakeGroups int    `json:"retake_groups"`
	CutLines     int    `json:"cut_lines"`
	UnsureGroups int    `json:"unsure_groups"`
}

func main() {
	model := flag.String("model", "", "decision model id (default: becky's default, Perplexity Decider)")
	window := flag.Int("window", 6, "how many earlier lines a restart may point back to")
	out := flag.String("out", "", "write the JSON here instead of stdout")
	var files []string // flags may come before or after the file name
	for args := os.Args[1:]; len(args) > 0; {
		if err := flag.CommandLine.Parse(args); err != nil {
			os.Exit(2)
		}
		if args = flag.Args(); len(args) > 0 {
			files, args = append(files, args[0]), args[1:]
		}
	}
	if len(files) != 1 {
		fmt.Fprintln(os.Stderr, "usage: becky-besttake <transcript.json> [--model id] [--window 6] [--out picks.json]")
		os.Exit(2)
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		beckyio.Fatalf("read transcript: %v", err)
	}
	var tr struct {
		File  string `json:"file"`
		Words []word `json:"words"`
	}
	if err := json.Unmarshal(raw, &tr); err != nil || len(tr.Words) == 0 {
		beckyio.Fatalf("%s is not a becky-transcribe JSON with words", files[0])
	}
	d := systemone.NewHosted("besttake")
	if *model != "" {
		d = d.WithModel(*model)
	}
	res, err := run(context.Background(), d, tr.Words, *window)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}
	res.Source, res.Model = tr.File, d.Model
	if *out == "" {
		beckyio.PrintJSON(res)
		return
	}
	b, _ := json.MarshalIndent(res, "", " ")
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		beckyio.Fatalf("write %s: %v", *out, err)
	}
	fmt.Fprintf(os.Stderr, "becky-besttake: %d lines, %d retake groups, %d lines cut, %d unsure -> %s\n",
		len(res.Lines), res.RetakeGroups, res.CutLines, res.UnsureGroups, *out)
}

func run(ctx context.Context, d systemone.Decider, ws []word, window int) (result, error) {
	lines := splitLines(ws)
	scores, err := restartScores(ctx, d, lines, window)
	if err != nil {
		return result{}, err
	}
	groups := group(lines, window, scores)
	if err := pick(ctx, d, lines, groups); err != nil {
		return result{}, err
	}
	res := result{Lines: lines, Groups: len(groups)}
	for _, g := range groups {
		if len(g) > 1 {
			res.RetakeGroups++
			if lines[g[0][0]].Unsure {
				res.UnsureGroups++
			}
		}
	}
	for _, l := range lines {
		if !l.Keep {
			res.CutLines++
		}
	}
	return res, nil
}
