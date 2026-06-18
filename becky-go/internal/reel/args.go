package reel

import (
	"fmt"
	"strings"

	"becky-go/internal/edl"
)

// Normalization defaults. Mixed-source clips must be brought to a common
// fps/size/SAR/pixel-format before concat or the join glitches (R-CUT §7).
const (
	defaultWidth  = 1280
	defaultHeight = 720
	defaultOutFPS = 30.0
)

// buildRenderArgs constructs the full ffmpeg argv for the one-pass multi-source
// assemble + per-clip original-timecode lower-third + concat (R-CUT §5 template
// #2). It is PURE: no exec, no filesystem — so it is unit-tested by asserting
// the returned []string. Render() runs it.
//
// For each clip i:
//   - input-seek "-ss <in> -t <out-in> -i <source>" (frame-accurate re-encode;
//     R-CUT §2 proves -c copy slips to a keyframe, so we always re-encode).
//     BOTH -ss and -t go BEFORE the matching -i so they are INPUT options: -ss
//     is the fast input seek, -t bounds that input's read window. (Placing -t
//     AFTER -i makes it an OUTPUT-duration limit, which — with filter_complex —
//     truncates the WHOLE output to the last input's duration; verified live,
//     it dropped every clip after the first.)
//   - in filter_complex: [i:v] drawtext(lower-third), scale=W:H (aspect-
//     preserving) + pad, setsar=1, fps=OUTFPS, format=yuv420p,
//     setpts=PTS-STARTPTS -> [vN]
//   - then concat=n=<count>:v=1:a=0 over all [vN] -> [vout], mapped to the codec.
//
// Audio is dropped (-an + concat a=0); the forensic compilation is a visual
// record. fontFile/width/height/outFPS/codec come from opts (resolved by
// Render). The output path is the final argv element.
func buildRenderArgs(r edl.Reel, opts resolvedOpts) ([]string, error) {
	if len(r.Clips) == 0 {
		return nil, fmt.Errorf("reel has no clips")
	}

	args := []string{"-y", "-hide_banner", "-loglevel", "error"}

	// Per-clip inputs: input-seek + read-window (BOTH -ss and -t before -i).
	for _, c := range r.Clips {
		args = append(args,
			"-ss", formatSeconds(c.In),
			"-t", formatSeconds(c.Dur()),
			"-i", c.Source,
		)
	}

	// filter_complex (normalize + lower-third per input, then concat).
	filter, outLabel := buildFilterComplex(r, opts)
	args = append(args, "-filter_complex", filter, "-map", outLabel)

	// Codec + quality.
	args = append(args, "-c:v", opts.Codec)
	args = append(args, codecQualityArgs(opts)...)
	args = append(args, "-pix_fmt", "yuv420p", "-an")

	args = append(args, opts.Output)
	return args, nil
}

// buildFilterComplex builds the filter_complex graph string and the final
// output pad label ("[vout]"). Each input is normalized + lower-thirded, then
// all are concatenated.
func buildFilterComplex(r edl.Reel, opts resolvedOpts) (string, string) {
	var chains []string
	var labels []string

	for i, c := range r.Clips {
		fps := c.FPS(opts.OutFPS)
		var steps []string
		if lt := lowerThirdFilter(r.Overlay, c, opts.FontFile, fps); lt != "" {
			steps = append(steps, lt)
		}
		steps = append(steps,
			fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease", opts.Width, opts.Height),
			fmt.Sprintf("pad=%d:%d:(ow-iw)/2:(oh-ih)/2", opts.Width, opts.Height),
			"setsar=1",
			"fps="+formatRate(opts.OutFPS),
			"format=yuv420p",
			"setpts=PTS-STARTPTS",
		)
		label := fmt.Sprintf("[v%d]", i)
		chains = append(chains, fmt.Sprintf("[%d:v]%s%s", i, strings.Join(steps, ","), label))
		labels = append(labels, label)
	}

	const outLabel = "[vout]"
	concat := fmt.Sprintf("%sconcat=n=%d:v=1:a=0%s", strings.Join(labels, ""), len(r.Clips), outLabel)
	graph := strings.Join(chains, ";") + ";" + concat
	return graph, outLabel
}

// codecQualityArgs returns the quality flags for the chosen codec. An explicit
// Bitrate always wins. nvenc gets a VBR/CQ setting for a high-quality-but-
// bounded forensic compilation; libx264 gets a CRF.
func codecQualityArgs(opts resolvedOpts) []string {
	if opts.Bitrate != "" {
		return []string{"-b:v", opts.Bitrate}
	}
	switch {
	case strings.Contains(opts.Codec, "nvenc"):
		// High-quality VBR; -cq 19 is visually near-lossless on these clips (R-CUT §7).
		return []string{"-rc", "vbr", "-cq", "19"}
	case strings.Contains(opts.Codec, "libx264"):
		return []string{"-crf", "18", "-preset", "medium"}
	default:
		return nil
	}
}

// formatSeconds renders a seconds value for ffmpeg -ss/-t with millisecond
// precision (no scientific notation). Negative input clamps to zero.
func formatSeconds(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	return fmt.Sprintf("%.3f", sec)
}
