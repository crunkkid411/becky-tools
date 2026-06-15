package research

import (
	"strings"
	"testing"
)

func sampleReport() Report {
	return Report{
		Tool: toolVersion, Mode: ModeTopic, Question: "the topic",
		SnapshotSHA256: "abcdef0123456789",
		Findings: []Finding{
			{Claim: "X is true", Status: StatusCorroborated, Verify: "supports", Cites: []int{1, 2}, Basis: "two sources agree"},
			{Claim: "Y maybe", Status: StatusCandidate, Verify: "partial", Cites: []int{2}, Basis: "single source"},
		},
		Sources: []Source{
			{ID: 1, URL: "https://e.com/a", Title: "A", FetchedAt: "2026-06-14T09:12:00Z", ContentSHA256: "aa11bb22cc33dd44", LinkOK: true},
			{ID: 2, URL: "https://e.com/b", Title: "B", FetchedAt: "2026-06-14T09:12:00Z", ContentSHA256: "ee55ff66", LinkOK: false},
		},
		DroppedClaims: []DroppedClaim{{Claim: "Z false", Verify: "unsupported", Reason: "source does not state this"}},
		Notes:         Notes{Honesty: honesty},
	}
}

func TestRenderMarkdown_deterministicAndCited(t *testing.T) {
	rep := sampleReport()
	md := RenderMarkdown(rep)
	if md != RenderMarkdown(rep) {
		t.Error("RenderMarkdown is not deterministic")
	}
	for _, want := range []string{
		"# becky-research report",
		"the topic",
		"[corroborated]", "X is true", "[1][2]", // inline cites
		"[candidate]", "Y maybe",
		"## Sources",
		"https://e.com/a", "LINK NOT OK", // the link_ok=false source flagged
		"## Dropped (unsupported) claims", "Z false",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("report.md missing %q", want)
		}
	}
}

func TestRenderMarkdown_emptyFindings(t *testing.T) {
	rep := Report{Question: "q", Notes: Notes{Honesty: honesty}}
	md := RenderMarkdown(rep)
	if !strings.Contains(md, "No corroborated or candidate findings") {
		t.Error("empty findings should render a clear placeholder, not crash")
	}
}

func TestPlainSummary_counts(t *testing.T) {
	rep := sampleReport()
	rep.Notes.Degrade = "offline; live search skipped"
	s := PlainSummary(rep)
	if !strings.Contains(s, "1 corroborated") || !strings.Contains(s, "1 candidate") {
		t.Errorf("summary counts wrong: %q", s)
	}
	if !strings.Contains(s, "Degraded:") {
		t.Errorf("summary should surface the degrade note: %q", s)
	}
}

func TestRenderMarkdown_upgradeFlags(t *testing.T) {
	rep := sampleReport()
	rep.BeckyUpgrades = []Upgrade{{
		Component: "Parakeet (used by becky-transcribe)", CurrentInBecky: "v3 int8",
		Status: StatusCandidate, Cites: []int{1}, Recommendation: "evaluate before adopting",
	}}
	md := RenderMarkdown(rep)
	if !strings.Contains(md, "## becky dependency flags") || !strings.Contains(md, "Parakeet") {
		t.Error("upgrade flags should render their own section")
	}
}
