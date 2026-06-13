// render.go — human-readable output for --output text and --output markdown.
// JSON output stays in main.go via beckyio.PrintJSON; these renderers write to
// stdout for the two non-JSON formats. Both are deterministic.
package main

import (
	"fmt"
	"io"
	"sort"
)

// renderText writes a compact, grep-friendly listing: one finding per line in
// the classic "file:line: [severity] (category) message" shape, grouped under a
// header and followed by the summary.
func renderText(w io.Writer, r Report) {
	fmt.Fprintf(w, "becky-debt-scan: %s\n", r.Path)
	fmt.Fprintf(w, "languages: %s | files: %d | categories: %s\n",
		joinOr(r.LanguagesDetected, "none"), r.FilesScanned, joinOr(r.Categories, "none"))
	fmt.Fprintln(w)

	if len(r.Findings) == 0 {
		fmt.Fprintln(w, "No technical-debt findings.")
	}
	for _, f := range r.Findings {
		extra := ""
		if f.Complexity > 0 {
			extra = fmt.Sprintf(" [complexity=%d]", f.Complexity)
		}
		if f.AgeDays > 0 {
			extra += fmt.Sprintf(" [age=%dd]", f.AgeDays)
		}
		if f.DupWith != "" {
			extra += fmt.Sprintf(" [dup_with=%s]", f.DupWith)
		}
		fmt.Fprintf(w, "%s:%d: [%s] (%s) %s%s\n", f.File, f.Line, f.Severity, f.Category, f.Message, extra)
	}

	fmt.Fprintln(w)
	fmt.Fprintf(w, "Summary: %d finding(s)\n", r.Summary.Total)
	for _, c := range sortedKeys(r.Summary.ByCategory) {
		fmt.Fprintf(w, "  %-12s %d\n", c, r.Summary.ByCategory[c])
	}
	if len(r.Notes) > 0 {
		fmt.Fprintln(w, "\nNotes:")
		for _, k := range sortedKeys(r.Notes) {
			fmt.Fprintf(w, "  - %s\n", r.Notes[k])
		}
	}
	renderFixSection(w, r, "  - ")
}

// renderMarkdown writes a report suitable for a PR comment or CI artifact: a
// header, a per-category summary table, then a findings table.
func renderMarkdown(w io.Writer, r Report) {
	fmt.Fprintf(w, "# Technical Debt Report\n\n")
	fmt.Fprintf(w, "**Path:** `%s`  \n", r.Path)
	fmt.Fprintf(w, "**Languages:** %s  \n", joinOr(r.LanguagesDetected, "none"))
	fmt.Fprintf(w, "**Files scanned:** %d  \n", r.FilesScanned)
	fmt.Fprintf(w, "**Total findings:** %d\n\n", r.Summary.Total)

	fmt.Fprintf(w, "## Summary\n\n")
	fmt.Fprintf(w, "| Category | Count |\n|---|---|\n")
	for _, c := range sortedKeys(r.Summary.ByCategory) {
		fmt.Fprintf(w, "| %s | %d |\n", c, r.Summary.ByCategory[c])
	}
	fmt.Fprintln(w)

	if len(r.Findings) > 0 {
		fmt.Fprintf(w, "## Findings\n\n")
		fmt.Fprintf(w, "| Severity | Category | Location | Message |\n|---|---|---|---|\n")
		for _, f := range r.Findings {
			loc := fmt.Sprintf("`%s:%d`", f.File, f.Line)
			fmt.Fprintf(w, "| %s | %s | %s | %s |\n", f.Severity, f.Category, loc, mdEscape(f.Message))
		}
		fmt.Fprintln(w)
	}

	if len(r.Notes) > 0 {
		fmt.Fprintf(w, "## Notes\n\n")
		for _, k := range sortedKeys(r.Notes) {
			fmt.Fprintf(w, "- %s\n", r.Notes[k])
		}
		fmt.Fprintln(w)
	}
	renderFixSection(w, r, "- ")
}

// renderFixSection prints applied/planned fixes if any.
func renderFixSection(w io.Writer, r Report, bullet string) {
	if len(r.FixesPlanned) > 0 {
		fmt.Fprintln(w, "\nPlanned fixes (dry-run):")
		for _, f := range r.FixesPlanned {
			fmt.Fprintf(w, "%s%s\n", bullet, f)
		}
	}
	if len(r.FixesApplied) > 0 {
		fmt.Fprintln(w, "\nApplied fixes:")
		for _, f := range r.FixesApplied {
			fmt.Fprintf(w, "%s%s\n", bullet, f)
		}
	}
}

// sortedKeys returns the keys of a map[string]T sorted ascending.
func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func joinOr(items []string, fallback string) string {
	if len(items) == 0 {
		return fallback
	}
	out := items[0]
	for _, it := range items[1:] {
		out += ", " + it
	}
	return out
}

// mdEscape neutralizes pipes so a message can't break a markdown table row.
func mdEscape(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '|' {
			out = append(out, '\\')
		}
		out = append(out, r)
	}
	return string(out)
}
