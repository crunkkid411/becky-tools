// R9 RENDER — pure templating of the assembled Report into report.md (numbered
// inline [n] citations + a source table) and the plain-language stderr summary a
// non-developer reads. No model, no network; identical Report → identical text.
package research

import (
	"fmt"
	"strings"
)

// RenderMarkdown produces the report.md body: a header, the findings stated by
// their forensic conclusion (corroborated plainly, candidates flagged), any
// becky-upgrade flags, and a numbered source table. Deterministic over the Report.
func RenderMarkdown(rep Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# becky-research report\n\n")
	fmt.Fprintf(&b, "**Question:** %s\n\n", rep.Question)
	fmt.Fprintf(&b, "**Mode:** %s  \n", rep.Mode)
	fmt.Fprintf(&b, "**Snapshot:** `%s`\n\n", rep.SnapshotSHA256)
	if rep.Notes.Degrade != "" {
		fmt.Fprintf(&b, "> Note: %s\n\n", rep.Notes.Degrade)
	}

	b.WriteString("## Findings\n\n")
	if len(rep.Findings) == 0 {
		b.WriteString("_No corroborated or candidate findings were produced._\n\n")
	}
	for _, f := range rep.Findings {
		fmt.Fprintf(&b, "- **[%s]** %s %s\n", f.Status, f.Claim, citeMarks(f.Cites))
		fmt.Fprintf(&b, "  - %s\n", f.Basis)
	}
	b.WriteString("\n")

	if len(rep.BeckyUpgrades) > 0 {
		b.WriteString("## becky dependency flags\n\n")
		for _, u := range rep.BeckyUpgrades {
			fmt.Fprintf(&b, "- **[%s]** %s %s\n", u.Status, u.Component, citeMarks(u.Cites))
			fmt.Fprintf(&b, "  - becky uses: %s — %s\n", u.CurrentInBecky, u.Recommendation)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Sources\n\n")
	for _, s := range rep.Sources {
		ok := "ok"
		if !s.LinkOK {
			ok = "LINK NOT OK"
		}
		fmt.Fprintf(&b, "%d. %s — %s (%s, fetched %s, sha %s)\n",
			s.ID, titleOr(s.Title, s.URL), s.URL, ok, s.FetchedAt, shortSHA(s.ContentSHA256))
	}
	if len(rep.DroppedClaims) > 0 {
		b.WriteString("\n## Dropped (unsupported) claims\n\n")
		for _, d := range rep.DroppedClaims {
			fmt.Fprintf(&b, "- %s — %s\n", d.Claim, d.Reason)
		}
	}
	return b.String()
}

// citeMarks renders the [n][m] inline citation marks for a claim.
func citeMarks(cites []int) string {
	if len(cites) == 0 {
		return ""
	}
	var b strings.Builder
	for _, c := range cites {
		fmt.Fprintf(&b, "[%d]", c)
	}
	return b.String()
}

func titleOr(title, fallback string) string {
	if strings.TrimSpace(title) != "" {
		return title
	}
	return fallback
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// PlainSummary is the one-paragraph stderr headline for a non-developer: how many
// corroborated findings, how many candidates, how many sources, and any degrade.
func PlainSummary(rep Report) string {
	corr, cand := 0, 0
	for _, f := range rep.Findings {
		if f.Status == StatusCorroborated {
			corr++
		} else {
			cand++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "becky-research: %d corroborated finding(s), %d candidate(s), over %d source(s).\n",
		corr, cand, len(rep.Sources))
	if len(rep.BeckyUpgrades) > 0 {
		fmt.Fprintf(&b, "%d becky dependency flag(s) — a source mentions a newer release of something becky uses.\n",
			len(rep.BeckyUpgrades))
	}
	if rep.Notes.Degrade != "" {
		fmt.Fprintf(&b, "Degraded: %s. The report is partial but every claim is still tied to a captured source.\n",
			rep.Notes.Degrade)
	}
	return b.String()
}
