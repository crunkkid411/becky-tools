// becky-radar — surface models/tools Jordan flagged in Chrome that becky should
// maybe adopt.
//
//	becky-radar [--json] [--profile <name>] [--days N] [--user-data-dir <path>]
//
// Jordan opens model cards / repos / papers in Chrome on his phone as his "becky
// look at this" queue; Chrome Sync lands them in the desktop Chrome History DB on
// this PC. This tool reads that LOCAL SQLite DB read-only (copying it first, since
// Chrome keeps it locked), classifies which visits name a model/tool, and
// cross-references becky's freshness manifest — so a flagged improvement (the
// PP-OCRv6 miss) is surfaced automatically instead of being re-typed from memory.
//
// Offline & deterministic: reads a local file only, never the network. If the
// Chrome DB is missing or unreadable it degrades to an empty report with a
// plain-language note (it never crashes). Exit codes: 0 ok, 1 error, 2 usage.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"becky-go/internal/freshness"
	"becky-go/internal/radar"
)

func main() {
	asJSON := flag.Bool("json", false, "emit JSON instead of a plain-language report")
	profile := flag.String("profile", "", "Chrome profile to read (default: Default + any Profile N)")
	days := flag.Int("days", 30, "how many days of history to scan")
	userDataDir := flag.String("user-data-dir", "", "Chrome 'User Data' dir (default: from LOCALAPPDATA)")
	list := flag.Bool("list", false, "emit EVERY iPhone-synced page in the window as a JSON URL feed (for becky-web2md), not the model/tool report")
	clean := flag.Bool("clean", true, "in --list mode, drop redirect/search/tracking junk URLs")
	flag.Parse()

	if *days <= 0 {
		fmt.Fprintln(os.Stderr, "usage: --days must be a positive number")
		os.Exit(2)
	}

	// --list is the "archive everything from my phone" feed: all synced visits,
	// not just the model/tool ones. It needs no freshness manifest.
	if *list {
		dir := *userDataDir
		if dir == "" {
			dir = radar.DefaultUserDataDir()
		}
		since := time.Now().AddDate(0, 0, -*days).UTC()
		paths, profiles := radar.DiscoverDBs(dir, *profile)
		rep := radar.BuildList(radar.ChromeSource{DBPaths: paths}, "chrome-local", *days, since, *clean, profiles)
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			fmt.Fprintln(os.Stderr, "encode:", err)
			os.Exit(1)
		}
		if rep.Degraded {
			os.Exit(1)
		}
		return
	}

	deps, err := freshness.LoadManifest()
	if err != nil {
		fmt.Fprintln(os.Stderr, "manifest error:", err)
		os.Exit(1)
	}

	dir := *userDataDir
	if dir == "" {
		dir = radar.DefaultUserDataDir()
	}
	since := time.Now().AddDate(0, 0, -*days).UTC()
	paths, profiles := radar.DiscoverDBs(dir, *profile)
	src := radar.ChromeSource{DBPaths: paths}

	rep := radar.Build(src, deps, "chrome-local", *days, since, profiles)

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

// printReport writes a plain-language radar report for a non-developer.
func printReport(rep radar.Report) {
	fmt.Println("becky-radar — models/tools you looked at that becky should maybe adopt")
	fmt.Println(strings.Repeat("=", 70))
	fmt.Printf("source: %s   |   last %d days (since %s)\n", rep.Source, rep.Days, rep.Since)
	if len(rep.Profiles) > 0 {
		fmt.Printf("Chrome profiles read: %s\n", strings.Join(rep.Profiles, ", "))
	}
	fmt.Println()

	if rep.Degraded {
		fmt.Println("Couldn't read your Chrome history this time.")
		fmt.Println("  " + rep.Note)
		return
	}

	printFlagged(rep.Flagged)
	printSeen(rep.Seen)
	printSummary(rep)
}

// printFlagged prints the corroborated section: visits that map to a tracked dep.
func printFlagged(items []radar.Item) {
	fmt.Println("FLAGGED (worth a look — matches something becky already tracks)")
	fmt.Println(strings.Repeat("-", 70))
	if len(items) == 0 {
		fmt.Println("  (nothing you viewed maps to a tool/model becky tracks)")
		fmt.Println()
		return
	}
	for _, it := range items {
		m := it.BeckyMatch
		title := it.Title
		if title == "" {
			title = it.URL
		}
		fmt.Printf("- %s\n", title)
		fmt.Printf("    you viewed : %s\n", it.URL)
		fmt.Printf("    becky uses : %s (in %s)\n", m.BeckyPinned, strings.Join(m.UsedBy, ", "))
		fmt.Printf("    verdict    : %s\n", m.Verdict)
		fmt.Println()
	}
}

// printSeen prints the candidate section: model/tool sites visited, no dep hit.
func printSeen(items []radar.Item) {
	fmt.Println("SEEN (model/tool sites you visited — candidates, not yet matched to becky)")
	fmt.Println(strings.Repeat("-", 70))
	if len(items) == 0 {
		fmt.Println("  (no other model/tool pages in this window)")
		fmt.Println()
		return
	}
	for _, it := range items {
		title := it.Title
		if title == "" {
			title = it.URL
		}
		fmt.Printf("- [%s] %s\n    %s\n", it.Class, title, it.URL)
	}
	fmt.Println()
}

// printSummary prints the one-line takeaway for a non-developer.
func printSummary(rep radar.Report) {
	fmt.Println(strings.Repeat("-", 70))
	switch {
	case len(rep.Flagged) > 0:
		fmt.Printf("%d flagged, %d other model/tool page(s) seen.\n", len(rep.Flagged), len(rep.Seen))
		fmt.Println("Tell Claude which to act on (e.g. \"upgrade becky-ocr to what I viewed\").")
	case len(rep.Seen) > 0:
		fmt.Printf("Nothing matched becky's tracked tools, but %d model/tool page(s) were seen — review above.\n", len(rep.Seen))
	default:
		fmt.Println("No model/tool pages in this window. Try a larger --days.")
	}
}
