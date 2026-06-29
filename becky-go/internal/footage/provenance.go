package footage

// provenance.go recovers a downloaded video's date + source URL from the machine
// scaffolding yt-dlp bakes into the FILE NAME ("2026-06-27_Title_[VIDEOID].mp4"),
// for the (common) case where no becky/.info.json sidecar is present. These are
// pure helpers over the same patterns the indexer already uses (datePrefixRe and
// ytIDRe, declared in discover.go) so the rule lives in exactly one place; the
// caller decides whether to use them (an explicit sidecar value always wins).

// DateFromName returns the leading recording-date prefix as ISO YYYY-MM-DD —
// dashed "2026-06-27_" or compact "20260627_" — or "" when the name has none.
func DateFromName(name string) string {
	m := datePrefixRe.FindStringSubmatch(name)
	if m == nil {
		return ""
	}
	d := m[1]
	if len(d) == 8 { // compact YYYYMMDD -> dashed
		return d[:4] + "-" + d[4:6] + "-" + d[6:8]
	}
	return d
}

// LinkFromName builds the canonical YouTube watch URL from a bracketed 11-char
// "[VIDEOID]" token, or "" when the name carries none. The id is case-SENSITIVE,
// so (unlike videoIDToken's lowercased match key) it is preserved verbatim.
func LinkFromName(name string) string {
	m := ytIDRe.FindStringSubmatch(name)
	if m == nil {
		return ""
	}
	return "https://www.youtube.com/watch?v=" + m[1]
}
