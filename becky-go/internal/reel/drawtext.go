package reel

import (
	"strings"

	"becky-go/internal/edl"
	"becky-go/internal/footage"
	"becky-go/internal/pathx"
)

// Lower-third layout constants. Deterministic, unobtrusive, bottom by default.
const (
	ltFontSize   = 26 // filename / Date / Link lines
	ltTCFontSize = 28 // the original-timecode line (slightly larger — it's the anchor)
	ltBoxAlpha   = "0.6"
	ltMarginX    = 20 // px from the left edge
	ltLineH      = 38 // vertical step between stacked lines
	ltBottomPad  = 24 // px from the bottom edge to the lowest line (default position)
	ltTopPad     = 20 // px from the top edge to the highest line (Position == "top")
)

// defaultFont is the deterministic forensic font. Consolas is monospaced so the
// timecode digits do not jitter frame-to-frame (R-CUT §4a). Passing an explicit
// fontfile also silences the anaconda ffmpeg's non-deterministic fontconfig
// fallback. BECKY_REEL_FONT overrides it (set by the engine, not hardcoded here).
const defaultFont = `C:/Windows/Fonts/consola.ttf`

// lowerThirdFilter builds the per-clip drawtext filter chain for the forensic
// lower-third, honoring each Overlay.Show* toggle and the clip's Meta values.
// It returns "" when nothing should be drawn (overlay disabled or no enabled
// line has content), so the caller can omit the filter entirely.
//
// Each enabled field is its OWN stacked line (top -> bottom: filename, ORIG TC,
// Date, Link), so a long filename or URL never gets concatenated into one
// over-wide row. Lines are positioned by index off the chosen edge.
//   - the ORIGINAL-file running timecode: drawtext timecode='<src-in TC>' with
//     timecode_rate=<fps>, which ffmpeg advances one frame at a time, so the
//     burned value equals the position in the ORIGINAL file (the verification
//     anchor). Only emitted when Overlay.ShowTimecode.
//   - the filename line joins filename | person | location.
//   - the Date and Link lines (labeled), each from the sidecar Meta or, for a
//     yt-dlp download with no sidecar, recovered from the file name.
//
// fontFile is the resolved font path; fps is the clip's effective frame rate.
func lowerThirdFilter(o edl.Overlay, c edl.Clip, fontFile string, fps float64) string {
	if !o.Enabled {
		return ""
	}
	if fontFile == "" {
		fontFile = defaultFont
	}
	escFont := escapeFontPath(fontFile)

	// Collect the lines in display order (top -> bottom). The timecode line is
	// special (drawtext timecode=); the rest are plain text=.
	type ltLine struct {
		tc   bool
		text string
		size int
	}
	var lines []ltLine
	if meta := metaLine(o, c); meta != "" {
		lines = append(lines, ltLine{text: meta, size: ltFontSize})
	}
	if o.ShowTimecode {
		lines = append(lines, ltLine{tc: true, size: ltTCFontSize})
	}
	if date := overlayDate(o, c); date != "" {
		lines = append(lines, ltLine{text: "Date: " + date, size: ltFontSize})
	}
	if link := overlayLink(o, c); link != "" {
		lines = append(lines, ltLine{text: "Link: " + link, size: ltFontSize})
	}
	if len(lines) == 0 {
		return ""
	}

	n := len(lines)
	var parts []string
	for i, ln := range lines {
		y := lineYExpr(o.Position, i, n)
		if ln.tc {
			tc := edl.SecondsToTimecode(c.In, fps)
			// timecode= needs the colons escaped; timecode_rate advances it per frame.
			parts = append(parts, joinDrawtext([]string{
				"timecode='" + escapeColons(tc) + "'",
				"timecode_rate=" + formatRate(fps),
				"text='" + escapeDrawtextText("ORIG TC") + "'",
				"x=" + itoa(ltMarginX),
				"y=" + y,
				"fontsize=" + itoa(ln.size),
				"fontcolor=white",
				"box=1",
				"boxcolor=black@" + ltBoxAlpha,
				"fontfile=" + escFont,
			}))
			continue
		}
		parts = append(parts, joinDrawtext([]string{
			"text='" + escapeDrawtextText(ln.text) + "'",
			"x=" + itoa(ltMarginX),
			"y=" + y,
			"fontsize=" + itoa(ln.size),
			"fontcolor=white",
			"box=1",
			"boxcolor=black@" + ltBoxAlpha,
			"fontfile=" + escFont,
		}))
	}

	return strings.Join(parts, ",")
}

// metaLine joins the enabled identity fields (filename | person | location) into
// the top row. Date/Link get their OWN lines (see lowerThirdFilter), so they are
// not joined here — that kept the row from running past the video on long URLs.
func metaLine(o edl.Overlay, c edl.Clip) string {
	var fields []string
	if o.ShowFilename {
		name := pathx.Base(c.Source)
		if name == "" {
			name = c.Source
		}
		if name != "" {
			fields = append(fields, name)
		}
	}
	if o.ShowPerson && c.Meta.Person != "" {
		fields = append(fields, c.Meta.Person)
	}
	if o.ShowLocation && c.Meta.Location != "" {
		fields = append(fields, c.Meta.Location)
	}
	return strings.Join(fields, " | ")
}

// overlayDate is the date for the lower-third when ShowDate is on: the sidecar
// value if set, else recovered from a yt-dlp file name ("YYYY-MM-DD_..."). "".
func overlayDate(o edl.Overlay, c edl.Clip) string {
	if !o.ShowDate {
		return ""
	}
	if c.Meta.Date != "" {
		return c.Meta.Date
	}
	return footage.DateFromName(pathx.Base(c.Source))
}

// overlayLink is the source URL for the lower-third when ShowLink is on: the
// sidecar value if set, else built from a yt-dlp "[VIDEOID]" file-name token. "".
func overlayLink(o edl.Overlay, c edl.Clip) string {
	if !o.ShowLink {
		return ""
	}
	if c.Meta.Link != "" {
		return c.Meta.Link
	}
	return footage.LinkFromName(pathx.Base(c.Source))
}

// lineYExpr returns the ffmpeg y expression for stacked line i of n (i=0 is the
// TOP line). Bottom-anchored by default; "top" stacks downward from the top edge.
func lineYExpr(position string, i, n int) string {
	if strings.EqualFold(position, "top") {
		return itoa(ltTopPad + i*ltLineH)
	}
	return "h-" + itoa(ltBottomPad+(n-1-i)*ltLineH)
}

// joinDrawtext assembles a single "drawtext=<k=v:k=v...>" filter from its
// already-built key=value parts.
func joinDrawtext(kv []string) string {
	return "drawtext=" + strings.Join(kv, ":")
}
