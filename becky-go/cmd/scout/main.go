// becky-scout — assess a YouTube playlist for content that could improve or
// extend becky-tools (and things merely useful to Jordan).
//
//	becky-scout <playlist-url-or-id> [--deep] [--new-only --state <file>] [--json]
//	becky-scout --from-json <file> [--json]      # assess a pre-fetched playlist offline
//	becky-scout --catalog                        # show what scout looks for
//
// Jordan keeps "look at this" videos in YouTube playlists the way he keeps model
// cards in Chrome (see becky-radar). This tool reads each video's title, channel,
// description and tags (the text becky can process offline), and asks per video:
// does it name something becky should adopt, upgrade, or build? It cross-references
// becky's freshness manifest (models becky tracks → UPGRADE candidate) and a
// capability catalog (becky's domains → relates to a tool), then CORROBORATES: a
// video is "relevant" only when ≥2 independent signals agree; a lone signal is a
// "candidate". It also flags videos merely USEFUL to Jordan (interests catalog),
// and counts the rest as skipped.
//
// The one online step (resolving the playlist via yt-dlp) needs yt-dlp on PATH;
// if it's missing or the network fails, scout degrades to a plain-language note
// instead of crashing. --deep additionally pulls per-video descriptions/tags.
// --new-only/--state make repeat runs report only newly-added videos.
//
// --propose runs the autonomous build gate (Jordan's "let the models decide"):
// the local Qwen model pitches a concrete becky tool for each surfaced video, the
// independent Gemma-4 model votes, and only proposals BOTH models back become
// becky-new-tool intakes (written to --propose-dir; --build also runs the factory).
// Without the local models it degrades to a note. Exit codes: 0 ok, 1 error, 2 usage.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"becky-go/internal/freshness"
	"becky-go/internal/scout"
)

func main() {
	asJSON := flag.Bool("json", false, "emit JSON instead of a plain-language report")
	showCatalog := flag.Bool("catalog", false, "print becky's capability catalog + Jordan's interests (what scout looks for) and exit")
	fromJSON := flag.String("from-json", "", "assess a pre-fetched playlist JSON file (offline; no yt-dlp needed)")
	deep := flag.Bool("deep", false, "fetch each video's description/tags/channel via yt-dlp (richer, but one request per video)")
	newOnly := flag.Bool("new-only", false, "only assess videos not seen on a previous run (requires --state)")
	statePath := flag.String("state", "", "path to a JSON state file remembering which videos were already assessed")
	propose := flag.Bool("propose", false, "autonomous gate: Qwen proposes tools, Gemma-4 must agree, write intakes for approved ones")
	proposeDir := flag.String("propose-dir", "scout-proposals", "where to write approved becky-new-tool intakes (with --propose)")
	build := flag.Bool("build", false, "with --propose: hand each approved intake to becky-new-tool to actually build it")
	flag.Usage = usage
	flag.Parse()

	if *showCatalog {
		printCatalog()
		return
	}

	// Resolve the playlist source. --from-json reads a pre-fetched playlist file
	// offline (the escape hatch / fixture path); otherwise scout fetches the
	// playlist live via yt-dlp (its single online step). yt-dlp must be on PATH
	// (pip install yt-dlp) — if it isn't, the error becomes a plain degrade note.
	var src scout.PlaylistSource
	ref := ""
	if *fromJSON != "" {
		src = fileSource{path: *fromJSON}
		ref = *fromJSON
	} else {
		rest := flag.Args()
		if len(rest) == 0 {
			usage()
			os.Exit(2)
		}
		ref = rest[0]
		// Be friendly: allow flags placed AFTER the playlist arg too
		// (Go's flag package otherwise stops parsing at the first positional).
		if len(rest) > 1 {
			if err := flag.CommandLine.Parse(rest[1:]); err != nil {
				os.Exit(2)
			}
			if flag.NArg() > 0 {
				usage()
				os.Exit(2)
			}
		}
		src = newYtdlpSource(*deep)
	}

	if *newOnly && *statePath == "" {
		fmt.Fprintln(os.Stderr, "usage: --new-only requires --state <file> (where to remember assessed videos)")
		os.Exit(2)
	}

	deps, err := freshness.LoadManifest()
	if err != nil {
		fmt.Fprintln(os.Stderr, "manifest error:", err)
		os.Exit(1)
	}

	// --new-only wraps the source: it drops videos already recorded in the state
	// file (so the report shows only newly-added entries) while capturing every
	// id fetched this run, which we persist afterwards.
	var seen map[string]bool
	var fetchedIDs []string
	if *newOnly {
		seen = loadState(*statePath)
		src = &stateFilterSource{inner: src, seen: seen, fetched: &fetchedIDs}
	}

	var assessor scout.Assessor // nil → deterministic floor only

	rep := scout.Build(src, ref, deps, nil, nil, assessor)

	// Persist state only on a clean run, so a transient fetch failure doesn't
	// "forget" the playlist (which would re-report everything next time).
	if *newOnly && !rep.Degraded {
		for _, id := range fetchedIDs {
			seen[id] = true
		}
		if err := saveState(*statePath, seen); err != nil {
			fmt.Fprintln(os.Stderr, "scout: could not save state:", err)
		}
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			fmt.Fprintln(os.Stderr, "encode:", err)
			os.Exit(1)
		}
	} else {
		printReport(rep)
	}

	// Autonomous build-proposal gate (opt-in). Skipped on a degraded fetch (no
	// videos to consider). In JSON mode its status goes to stderr so stdout stays
	// valid JSON.
	if *propose && !rep.Degraded {
		w := os.Stdout
		if *asJSON {
			w = os.Stderr
		}
		runPropose(rep, *proposeDir, *build, w)
	}
}

// stateFilterSource wraps another source for --new-only: it drops videos whose
// id is already in `seen`, and records every fetched id in `fetched` so the
// caller can persist the updated state after a clean run.
type stateFilterSource struct {
	inner   scout.PlaylistSource
	seen    map[string]bool
	fetched *[]string
}

func (s *stateFilterSource) Playlist(ref string) (scout.Playlist, error) {
	pl, err := s.inner.Playlist(ref)
	if err != nil {
		return pl, err
	}
	kept := pl.Videos[:0:0]
	for _, v := range pl.Videos {
		if v.ID != "" {
			*s.fetched = append(*s.fetched, v.ID)
		}
		if v.ID != "" && s.seen[v.ID] {
			continue
		}
		kept = append(kept, v)
	}
	pl.Videos = kept
	return pl, nil
}

// scoutState is the persisted --new-only memory: the set of video ids already
// assessed, plus when it was last written (human context only).
type scoutState struct {
	SeenVideoIDs []string `json:"seen_video_ids"`
	Updated      string   `json:"updated"`
}

// loadState reads the seen-id set; a missing/unreadable file is an empty set
// (first run), never an error.
func loadState(path string) map[string]bool {
	seen := map[string]bool{}
	b, err := os.ReadFile(path)
	if err != nil {
		return seen
	}
	var st scoutState
	if json.Unmarshal(b, &st) == nil {
		for _, id := range st.SeenVideoIDs {
			seen[id] = true
		}
	}
	return seen
}

// saveState writes the seen-id set back (sorted, for a stable diff-able file).
func saveState(path string, seen map[string]bool) error {
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	b, err := json.MarshalIndent(scoutState{SeenVideoIDs: ids, Updated: time.Now().UTC().Format(time.RFC3339)}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// fileSource reads a pre-fetched playlist from a local JSON file. It accepts
// either a {"id","title","url","videos":[...]} object or a bare array of videos
// (the shape a yt-dlp dump or a simple scraper emits). No network.
type fileSource struct{ path string }

func (f fileSource) Playlist(ref string) (scout.Playlist, error) {
	b, err := os.ReadFile(f.path)
	if err != nil {
		return scout.Playlist{}, fmt.Errorf("read %s: %w", f.path, err)
	}
	trimmed := strings.TrimSpace(string(b))
	if strings.HasPrefix(trimmed, "[") {
		var vids []scout.Video
		if err := json.Unmarshal(b, &vids); err != nil {
			return scout.Playlist{}, fmt.Errorf("parse video array %s: %w", f.path, err)
		}
		return scout.Playlist{URL: f.path, Videos: fillPositions(vids)}, nil
	}
	var pl scout.Playlist
	if err := json.Unmarshal(b, &pl); err != nil {
		return scout.Playlist{}, fmt.Errorf("parse playlist %s: %w", f.path, err)
	}
	pl.Videos = fillPositions(pl.Videos)
	return pl, nil
}

// fillPositions assigns 1-based positions to any video missing one, preserving
// file order — so a scraper that didn't number entries still sorts stably.
func fillPositions(vids []scout.Video) []scout.Video {
	for i := range vids {
		if vids[i].Position == 0 {
			vids[i].Position = i + 1
		}
	}
	return vids
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: becky-scout <playlist-url-or-id> [--deep] [--new-only --state <file>] [--propose [--build]] [--json]")
	fmt.Fprintln(os.Stderr, "       becky-scout --from-json <file> [--propose] [--json]   (assess a pre-fetched playlist offline)")
	fmt.Fprintln(os.Stderr, "       becky-scout --catalog                                 (show what scout looks for)")
	fmt.Fprintln(os.Stderr, "  assess a YouTube playlist for things that could improve/extend becky — or just be useful to you.")
	fmt.Fprintln(os.Stderr, "  live fetch needs yt-dlp on PATH (pip install yt-dlp); --deep adds per-video descriptions/tags.")
	fmt.Fprintln(os.Stderr, "  --propose: Qwen pitches tools, Gemma-4 must agree; approved ones become becky-new-tool intakes")
	fmt.Fprintln(os.Stderr, "  (--build also runs the factory). Needs the local models; degrades to a note without them.")
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
	printUseful(rep.Useful)
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

func printUseful(items []scout.Item) {
	fmt.Println("USEFUL TO YOU (not a becky tool, but in your interest areas)")
	fmt.Println(strings.Repeat("-", 70))
	if len(items) == 0 {
		fmt.Println("  (nothing extra flagged for you)")
		fmt.Println()
		return
	}
	for _, it := range items {
		title := it.Title
		if title == "" {
			title = it.URL
		}
		fmt.Printf("- %s\n", title)
		if it.URL != "" {
			fmt.Printf("    url      : %s\n", it.URL)
		}
		fmt.Printf("    areas    : %s\n", strings.Join(it.Interests, ", "))
		fmt.Println()
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
	fmt.Printf("%d relevant, %d candidate(s), %d useful-to-you, %d off-topic (skipped).\n",
		len(rep.Relevant), len(rep.Candidates), len(rep.Useful), rep.Skipped)
	switch {
	case len(rep.Relevant) > 0:
		fmt.Println("Tell Claude which to act on (e.g. \"build a tool for the first relevant one\").")
	case len(rep.Candidates) > 0:
		fmt.Println("Nothing corroborated, but some candidates are worth a look above.")
	case len(rep.Useful) > 0:
		fmt.Println("Nothing maps to becky, but some videos look personally useful above.")
	default:
		fmt.Println("Nothing in this playlist maps to becky or your interests.")
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
	fmt.Println()
	fmt.Println("personal interests — what counts as \"useful to you\" (even if not a becky tool)")
	fmt.Println(strings.Repeat("=", 70))
	for _, in := range scout.DefaultInterests() {
		fmt.Printf("- %s\n", in.Category)
		fmt.Printf("    keywords: %s\n", strings.Join(in.Keywords, ", "))
		fmt.Printf("    note    : %s\n\n", in.Note)
	}
}
