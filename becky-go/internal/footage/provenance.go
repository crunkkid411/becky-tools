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

// VideoIDFromName returns the bracketed 11-char YouTube video ID token from
// name ("...[VIDEOID]...") or "" when the name carries none. The id is
// case-SENSITIVE, so (unlike videoIDToken's lowercased match key) it is
// preserved verbatim. Exposed bare (not just as part of a URL) for callers
// that need the raw id, e.g. qmdindex's frontmatter.
func VideoIDFromName(name string) string {
	m := ytIDRe.FindStringSubmatch(name)
	if m == nil {
		return ""
	}
	return m[1]
}

// LinkFromName builds the canonical YouTube watch URL from a bracketed 11-char
// "[VIDEOID]" token, or "" when the name carries none.
func LinkFromName(name string) string {
	id := VideoIDFromName(name)
	if id == "" {
		return ""
	}
	return "https://www.youtube.com/watch?v=" + id
}
