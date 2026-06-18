package reel

import (
	"strings"

	"becky-go/internal/edl"
	"becky-go/internal/pathx"
)

// Lower-third layout constants. Deterministic, unobtrusive, bottom by default.
const (
	ltFontSize   = 20 // metadata line
	ltTCFontSize = 22 // the original-timecode line (slightly larger — it's the anchor)
	ltBoxAlpha   = "0.6"
	ltMarginX    = 20 // px from the left edge
	ltLineTC     = 60 // y offset (h-60) for the timecode line
	ltLineMeta   = 30 // y offset (h-30) for the metadata line
	// Top-position offsets (when Overlay.Position == "top").
	ltTopTC   = 20
	ltTopMeta = 50
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
// Two stacked drawtext lines (per R-CUT §4a, proven recipe):
//   - the ORIGINAL-file running timecode: drawtext timecode='<src-in TC>' with
//     timecode_rate=<fps>, which ffmpeg advances one frame at a time, so the
//     burned value equals the position in the ORIGINAL file (the verification
//     anchor). Only emitted when Overlay.ShowTimecode.
//   - a metadata line joining the enabled fields (filename | person | location
//     | date | link) with " | ".
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

	tcY, metaY := positionYExprs(o.Position)

	var parts []string

	if o.ShowTimecode {
		tc := edl.SecondsToTimecode(c.In, fps)
		// timecode= needs the colons escaped; timecode_rate advances it per frame.
		parts = append(parts, joinDrawtext([]string{
			"timecode='" + escapeColons(tc) + "'",
			"timecode_rate=" + formatRate(fps),
			"text='" + escapeDrawtextText("ORIG TC") + "'",
			"x=" + itoa(ltMarginX),
			"y=" + tcY,
			"fontsize=" + itoa(ltTCFontSize),
			"fontcolor=white",
			"box=1",
			"boxcolor=black@" + ltBoxAlpha,
			"fontfile=" + escFont,
		}))
	}

	if meta := metaLine(o, c); meta != "" {
		parts = append(parts, joinDrawtext([]string{
			"text='" + escapeDrawtextText(meta) + "'",
			"x=" + itoa(ltMarginX),
			"y=" + metaY,
			"fontsize=" + itoa(ltFontSize),
			"fontcolor=white",
			"box=1",
			"boxcolor=black@" + ltBoxAlpha,
			"fontfile=" + escFont,
		}))
	}

	return strings.Join(parts, ",")
}

// metaLine joins the enabled metadata fields into the bottom row text. Only
// fields whose Show* toggle is on AND whose value is non-empty are included.
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
	if o.ShowDate && c.Meta.Date != "" {
		fields = append(fields, c.Meta.Date)
	}
	if o.ShowLink && c.Meta.Link != "" {
		fields = append(fields, c.Meta.Link)
	}
	return strings.Join(fields, " | ")
}

// positionYExprs returns the y-coordinate expressions for the timecode and
// metadata lines given the overlay position ("top" or default "bottom").
func positionYExprs(position string) (tcY, metaY string) {
	if strings.EqualFold(position, "top") {
		return itoa(ltTopTC), itoa(ltTopMeta)
	}
	return "h-" + itoa(ltLineTC), "h-" + itoa(ltLineMeta)
}

// joinDrawtext assembles a single "drawtext=<k=v:k=v...>" filter from its
// already-built key=value parts.
func joinDrawtext(kv []string) string {
	return "drawtext=" + strings.Join(kv, ":")
}
