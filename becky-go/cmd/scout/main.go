// becky-scout — assess a YouTube playlist for content that could improve or
// extend becky-tools.
//
//	becky-scout <playlist-url-or-id> [--json] [--catalog]
//
// Jordan keeps "look at this" videos in YouTube playlists the way he keeps model
// cards in Chrome (see becky-radar). This tool reads each video's title, channel,
// description, tags and captions (the text becky can process offline), and asks
// one question per video: does it name something becky should adopt, upgrade, or
// build? It cross-references becky's freshness manifest (models becky already
// tracks → an UPGRADE candidate) and a built-in capability catalog (becky's
// domains → relates to a tool), then CORROBORATES: a video is reported as
// "relevant" only when ≥2 independent signals agree; a lone signal is a
// "candidate"; off-topic videos are counted, not listed.
//
// The one online step (resolving the playlist + captions via yt-dlp) is wired by
// the local agent; with no network/yt-dlp it degrades to a plain-language note
// instead of crashing. An optional local model adds a third independent signal.
// Exit codes: 0 ok, 1 error, 2 usage.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"becky-go/internal/freshness"
	"becky-go/internal/scout"
)

func main() {
	asJSON := flag.Bool("json", false, "emit JSON instead of a plain-language report")
	showCatalog := flag.Bool("catalog", false, "print becky's capability catalog (what scout looks for) and exit")
	flag.Usage = usage
	flag.Parse()

	if *showCatalog {
		printCatalog()
		return
	}

	args := flag.Args()
	if len(args) != 1 {
		usage()
		os.Exit(2)
	}
	ref := args[0]

	deps, err := freshness.LoadManifest()
	if err != nil {
		fmt.Fprintln(os.Stderr, "manifest error:", err)
		os.Exit(1)
	}

	// Cloud build ships the deterministic floor: the real yt-dlp PlaylistSource
	// and the optional local-model Assessor are wired by the local agent
	// (scout.PlaylistSource / scout.Assessor contracts in internal/scout). Until
	// then scout has no playlist to read and honestly reports the degrade.
	var src scout.PlaylistSource = unwiredSource{}
	var assessor scout.Assessor // nil → deterministic floor only

	rep := scout.Build(src, ref, deps, nil, assessor)

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			fmt.Fprintln(os.Stderr, "encode:", err)
			os.Exit(1)
		}
		return
	}
	printReport(rep)
}

// unwiredSource is the cloud-side placeholder PlaylistSource: it has no yt-dlp,
// so it reports the missing capability as a clean degrade (never a crash). The
// local agent replaces this with a real yt-dlp-backed source.
type unwiredSource struct{}

func (unwiredSource) Playlist(ref string) (scout.Playlist, error) {
	return scout.Playlist{}, fmt.Errorf("yt-dlp playlist fetch is not wired in this build " +
		"(cloud build ships the deterministic assessment core only; the local agent wires yt-dlp)")
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: becky-scout <playlist-url-or-id> [--json] [--catalog]")
	fmt.Fprintln(os.Stderr, "  assess a YouTube playlist for things that could improve or extend becky-tools")
}

// printReport writes a plain-language report for a non-developer.
func printReport(rep scout.Report) {
	fmt.Println("becky-scout — videos that could improve or extend becky-tools")
	fmt.Println(strings.Repeat("=", 70))
	fmt.Printf("playlist : %s\n", rep.Playlist)
	fmt.Printf("assessed : %d video(s)   |   signal source: %s\n", rep.Assessed, rep.Model)
	fmt.Println()

	if rep.Degraded {
		fmt.Println("Couldn't assess the playlist this time.")
		fmt.Println("  " + rep.Note)
		return
	}

	printRelevant(rep.Relevant)
	printCandidates(rep.Candidates)
	printSummary(rep)
}

func printRelevant(items []scout.Item) {
	fmt.Println("RELEVANT (corroborated — worth acting on)")
	fmt.Println(strings.Repeat("-", 70))
	if len(items) == 0 {
		fmt.Println("  (nothing in this playlist was corroborated as a becky improvement)")
		fmt.Println()
		return
	}
	for _, it := range items {
		printItem(it)
	}
}

func printCandidates(items []scout.Item) {
	fmt.Println("CANDIDATES (one signal only — review)")
	fmt.Println(strings.Repeat("-", 70))
	if len(items) == 0 {
		fmt.Println("  (no single-signal candidates)")
		fmt.Println()
		return
	}
	for _, it := range items {
		printItem(it)
	}
}

func printItem(it scout.Item) {
	title := it.Title
	if title == "" {
		title = it.URL
	}
	fmt.Printf("- [%s] %s\n", strings.ToUpper(it.Kind), title)
	if it.URL != "" {
		fmt.Printf("    url    : %s\n", it.URL)
	}
	if len(it.BeckyTools) > 0 {
		fmt.Printf("    becky  : %s\n", strings.Join(it.BeckyTools, ", "))
	}
	for _, d := range it.DepMatches {
		fmt.Printf("    tracks : %s (becky pins %s, used by %s)\n", d.Name, d.BeckyPinned, strings.Join(d.UsedBy, ", "))
	}
	for _, idea := range it.Ideas {
		fmt.Printf("    idea   : %s\n", idea)
	}
	fmt.Printf("    verdict: %s\n", it.Verdict)
	fmt.Println()
}

func printSummary(rep scout.Report) {
	fmt.Println(strings.Repeat("-", 70))
	fmt.Printf("%d relevant, %d candidate(s), %d off-topic (skipped).\n",
		len(rep.Relevant), len(rep.Candidates), rep.Skipped)
	switch {
	case len(rep.Relevant) > 0:
		fmt.Println("Tell Claude which to act on (e.g. \"build a tool for the first relevant one\").")
	case len(rep.Candidates) > 0:
		fmt.Println("Nothing corroborated, but some candidates are worth a look above.")
	default:
		fmt.Println("Nothing in this playlist maps to becky.")
	}
}

// printCatalog prints becky's capability catalog so Jordan can see (and the local
// agent can tune) exactly what scout treats as "a becky area".
func printCatalog() {
	fmt.Println("becky-scout capability catalog — what counts as a becky area")
	fmt.Println(strings.Repeat("=", 70))
	for _, c := range scout.DefaultCatalog() {
		fmt.Printf("- %s  (%s)\n", c.Domain, strings.Join(c.Tools, ", "))
		fmt.Printf("    keywords: %s\n", strings.Join(c.Keywords, ", "))
		fmt.Printf("    note    : %s\n\n", c.Note)
	}
}
