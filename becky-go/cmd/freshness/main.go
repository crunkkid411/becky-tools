// becky-freshness — keep becky's external models/tools from going stale.
//
//	becky-freshness [--json] [--offline]
//
// Reads the embedded dependency manifest (internal/freshness/manifest.json) and
// reports, in plain language, what each pinned model/library/binary looks like
// upstream right now — so an improvement (a new OCR release, a better ASR model)
// is surfaced AUTOMATICALLY instead of being missed until a human notices.
//
// This is becky's standard-practice freshness check. It is deliberately ONLINE
// (the one network step is explicit and lives only in this tool); becky's
// offline forensic tools never call it at runtime. With --offline it just prints
// what becky pins, no network.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"becky-go/internal/freshness"
)

func main() {
	asJSON := flag.Bool("json", false, "emit JSON instead of a plain-language report")
	offline := flag.Bool("offline", false, "don't touch the network; just list what becky pins")
	flag.Parse()

	deps, err := freshness.LoadManifest()
	if err != nil {
		fmt.Fprintln(os.Stderr, "manifest error:", err)
		os.Exit(1)
	}

	var results []freshness.Result
	if *offline {
		results = make([]freshness.Result, 0, len(deps))
		for _, d := range deps {
			results = append(results, freshness.Result{Dep: d})
		}
	} else {
		results = freshness.CheckAll(deps, freshness.HTTPGet)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(results); err != nil {
			fmt.Fprintln(os.Stderr, "encode:", err)
			os.Exit(1)
		}
		return
	}
	printReport(results, *offline)
}

// printReport writes a plain-language freshness report for a non-developer.
func printReport(results []freshness.Result, offline bool) {
	fmt.Println("becky-freshness — what becky's models/tools look like upstream")
	fmt.Println(strings.Repeat("=", 64))
	if offline {
		fmt.Println("(offline mode — just listing what becky pins; re-run online to check)")
	}
	fmt.Println()

	unreached := 0
	review := 0
	for _, r := range results {
		d := r.Dep
		fmt.Printf("- %s\n", d.Name)
		fmt.Printf("    used by   : %s\n", strings.Join(d.UsedBy, ", "))
		fmt.Printf("    becky uses: %s\n", d.Pinned)
		switch {
		case offline:
			fmt.Printf("    upstream  : %s/%s (not checked)\n", d.Upstream.Type, d.Upstream.Ref)
		case r.Error != "":
			fmt.Printf("    upstream  : couldn't check (%s)\n", r.Error)
			unreached++
		default:
			marker := ""
			if looksWorthReview(d.Pinned, r.Latest) {
				marker = "   <-- REVIEW: upstream differs from what becky uses"
				review++
			}
			fmt.Printf("    upstream  : %s%s\n", r.Latest, marker)
		}
		if d.Note != "" {
			fmt.Printf("    note      : %s\n", d.Note)
		}
		fmt.Println()
	}

	fmt.Println(strings.Repeat("-", 64))
	if offline {
		fmt.Printf("%d dependencies pinned. Run without --offline to check upstream.\n", len(results))
		return
	}
	if review > 0 {
		fmt.Printf("%d dependenc(ies) flagged REVIEW - upstream looks different from what becky uses.\n", review)
		fmt.Println("Tell Claude which to act on (e.g. \"wire PaddleOCR-VL into becky-ocr\").")
	} else {
		fmt.Println("Nothing obviously stale.")
	}
	if unreached > 0 {
		fmt.Printf("(%d couldn't be checked - likely no internet or a rate limit.)\n", unreached)
	}
}

// looksWorthReview is a deliberately conservative heuristic: flag a dependency
// when the upstream marker is not already reflected in becky's pinned string.
// It never claims "up to date" with false confidence - it only nudges a human
// to look. The "wire ... to activate" pins always flag (they are by definition
// not yet using the upstream model).
func looksWorthReview(pinned, latest string) bool {
	if latest == "" {
		return false
	}
	p := strings.ToLower(pinned)
	if strings.Contains(p, "wired") || strings.Contains(p, "not wired") || strings.Contains(p, "activate") {
		return true
	}
	// HF "updated <date>" always differs in text, so don't auto-flag it (too
	// noisy); it is still shown. For clean version/tag markers (PyPI, GitHub),
	// flag when the tag isn't already a substring of what becky pins.
	if strings.HasPrefix(latest, "updated ") {
		return false
	}
	l := strings.ToLower(latest)
	return !strings.Contains(p, l)
}
