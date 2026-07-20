package reel

import (
	"fmt"
	"strings"

	"becky-go/internal/edl"
	"becky-go/internal/subs"
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
//   - then concat over all clips -> [vout] (+ [aout] when audio is on).
//
// AUDIO: when opts.Audio is set (Render turns it on whenever ffprobe is available
// to detect streams), the compilation KEEPS each clip's audio — a transcript/quote
// tool whose export is silent is useless. Clips that have no audio stream get a
// silent anullsrc input bounded to the clip's duration, so the audio concat has a
// segment for every clip and never errors (degrade-never-crash). With audio off we
// keep the old visual-only behaviour (-an, concat a=0). fontFile/width/height/
// outFPS/codec come from opts (resolved by Render). The output path is the final
// argv element.
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

	// Silent fill-inputs for clips that have no audio stream (appended AFTER the N
	// clip inputs, in clip order, so audioInputIndices' numbering lines up). Each is
	// bounded to its clip's duration with -t before -i.
	if opts.Audio {
		for i, c := range r.Clips {
			if !(i < len(opts.ClipHasAudio) && opts.ClipHasAudio[i]) {
				args = append(args,
					"-f", "lavfi",
					"-t", formatSeconds(c.Dur()),
					"-i", "anullsrc=channel_layout=stereo:sample_rate=48000",
				)
			}
		}
	}

	// filter_complex (normalize + lower-third per input, then concat video [+audio]).
	filter, vLabel, aLabel := buildFilterComplex(r, opts)
	args = append(args, "-filter_complex", filter, "-map", vLabel)
	if opts.Audio {
		args = append(args, "-map", aLabel)
	}

	// Codec + quality.
	args = append(args, "-c:v", opts.Codec)
	args = append(args, codecQualityArgs(opts)...)
	args = append(args, "-pix_fmt", "yuv420p")
	if opts.Audio {
		args = append(args, "-c:a", "aac", "-b:a", "192k")
	} else {
		args = append(args, "-an")
	}

	args = append(args, opts.Output)
	return args, nil
}

// audioInputIndices returns, per clip, the ffmpeg INPUT index whose audio stream
// feeds that clip's segment: the clip's own input i when it has an audio stream, or
// a dedicated silent anullsrc input (appended after the N clip inputs, in clip
// order) when it doesn't. Returns nil when opts.Audio is off. buildRenderArgs
// appends the anullsrc inputs in the SAME clip order so these indices line up.
func audioInputIndices(r edl.Reel, opts resolvedOpts) []int {
	if !opts.Audio {
		return nil
	}
	idx := make([]int, len(r.Clips))
	next := len(r.Clips)
	for i := range r.Clips {
		if i < len(opts.ClipHasAudio) && opts.ClipHasAudio[i] {
			idx[i] = i // the clip's own audio: [i:a]
		} else {
			idx[i] = next // a silent anullsrc input
			next++
		}
	}
	return idx
}

// buildFilterComplex builds the filter_complex graph plus the video output label
// ("[vout]") and the audio output label ("[aout]", empty when audio is off). Each
// clip is normalized + lower-thirded -> [vN]; when audio is on, each clip's audio
// (its own stream or a silent fill) is resampled/normalized -> [aN], and the graph
// concatenates the interleaved [v0][a0][v1][a1]... with a=1.
func buildFilterComplex(r edl.Reel, opts resolvedOpts) (graph, vOut, aOut string) {
	var chains []string
	var vLabels, aLabels []string
	aidx := audioInputIndices(r, opts)

	for i, c := range r.Clips {
		fps := c.FPS(opts.OutFPS)
		var steps []string
		// Normalize to the OUTPUT canvas FIRST, then draw the lower-third on it: the
		// burn now sees a known, consistent width (opts.Width) so it can wrap a long
		// filename/URL, and a consistent size regardless of the source resolution.
		// It stays BEFORE the fps filter so drawtext's running timecode still advances
		// at the SOURCE frame rate (the forensic ORIG-TC anchor must not drift).
		steps = append(steps,
			fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease", opts.Width, opts.Height),
			fmt.Sprintf("pad=%d:%d:(ow-iw)/2:(oh-ih)/2", opts.Width, opts.Height),
		)
		if lt := lowerThirdFilter(r.Overlay, c, opts.FontFile, fps, opts.Width, opts.Height); lt != "" {
			steps = append(steps, lt)
		}
		steps = append(steps,
			"setsar=1",
			"fps="+formatRate(opts.OutFPS),
			"format=yuv420p",
			"setpts=PTS-STARTPTS",
		)
		vLabel := fmt.Sprintf("[v%d]", i)
		chains = append(chains, fmt.Sprintf("[%d:v]%s%s", i, strings.Join(steps, ","), vLabel))
		vLabels = append(vLabels, vLabel)

		if opts.Audio {
			aLabel := fmt.Sprintf("[a%d]", i)
			// Normalize every segment to a common rate/layout + reset PTS so concat
			// joins cleanly regardless of the source's audio format.
			chains = append(chains, fmt.Sprintf(
				"[%d:a]aresample=async=1,aformat=sample_rates=48000:channel_layouts=stereo,asetpts=PTS-STARTPTS%s",
				aidx[i], aLabel))
			aLabels = append(aLabels, aLabel)
		}
	}

	vOut = "[vout]"
	if !opts.Audio {
		concat := fmt.Sprintf("%sconcat=n=%d:v=1:a=0%s", strings.Join(vLabels, ""), len(r.Clips), vOut)
		graph, vOut = burnCaptionsChain(strings.Join(chains, ";")+";"+concat, vOut, opts)
		return graph, vOut, ""
	}

	aOut = "[aout]"
	var inter strings.Builder
	for i := range r.Clips {
		inter.WriteString(vLabels[i])
		inter.WriteString(aLabels[i])
	}
	concat := fmt.Sprintf("%sconcat=n=%d:v=1:a=1%s%s", inter.String(), len(r.Clips), vOut, aOut)
	graph, vOut = burnCaptionsChain(strings.Join(chains, ";")+";"+concat, vOut, opts)
	return graph, vOut, aOut
}

// burnCaptionsChain hangs the caption burn-in off the CONCAT's video output, so
// the whole compilation is captioned in the SAME ffmpeg pass that assembles it —
// one encode, no generation loss from a second re-encode.
//
// It goes after the concat, not per-clip, because the .srt is timed to the REEL
// TIMELINE (0 = first frame of the compilation), which is exactly the PTS the
// concat emits. Burning per clip would need every cue re-based to that clip.
//
// Returns the graph and the label the caller must -map: unchanged when there is
// no .srt to burn.
func burnCaptionsChain(graph, vIn string, opts resolvedOpts) (string, string) {
	if strings.TrimSpace(opts.SubtitleSRT) == "" {
		return graph, vIn
	}
	style := subs.DefaultStyle()
	if opts.SubtitleMarginV > 0 {
		style.MarginV = opts.SubtitleMarginV
	}
	const vSub = "[vsub]"
	return graph + ";" + vIn + style.SubtitlesFilter(opts.SubtitleSRT) + vSub, vSub
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
