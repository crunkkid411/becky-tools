// enhance.go — HONEST, logged image enhancement. Brightness / contrast / gamma /
// saturation only, via ffmpeg's `eq` filter, applied to a COPY of an extracted
// frame so the unedited frame (and the source video) stay untouched. Every
// adjustment is returned as an Enhance record that the manifest and the exhibit
// page both display — an exhibit must survive "what did you do to this image?".
//
// FORBIDDEN here by construction (no flag exists for any of these): geometry
// stretch/warp, AI diffusion/generation, cloning, content alteration. `eq` only
// remaps tone/color; it can never add or move content. Cropping/rotating to
// align framing is a separate, manual, optional step — never an automatic
// content-altering warp.
package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"becky-go/internal/config"
)

// enhanceOpts are the honest tone/color adjustments a reviewer can request. The
// neutral defaults (0/1/1/1) mean "no change"; only a non-neutral value applies.
type enhanceOpts struct {
	brightness float64 // eq brightness, -1.0..1.0 (0 = none)
	contrast   float64 // eq contrast, -2.0..2.0 (1 = none)
	gamma      float64 // eq gamma, 0.1..10.0 (1 = none)
	saturation float64 // eq saturation, 0.0..3.0 (1 = none)
}

// active reports whether any adjustment differs from the no-op neutral values.
func (o enhanceOpts) active() bool {
	return o.brightness != 0 || o.contrast != 1 || o.gamma != 1 || o.saturation != 1
}

// eqFilter renders the `eq` filter string for the chosen adjustments. ffmpeg
// treats omitted eq params as their neutral value, so we always emit all four
// for an explicit, auditable record.
func (o enhanceOpts) eqFilter() string {
	return fmt.Sprintf("eq=brightness=%g:contrast=%g:gamma=%g:saturation=%g",
		o.brightness, o.contrast, o.gamma, o.saturation)
}

// applyEnhance writes an enhanced COPY of frameSrc and returns the Enhance log
// record. The output path is <stem>_enhanced<ext>; the input frame is only read.
// frameLabel is "A" or "B" (for the log). note is the plain-language reason.
func applyEnhance(cfg config.Config, frameLabel, frameSrc string, o enhanceOpts, note string) (Enhance, error) {
	ext := filepath.Ext(frameSrc)
	out := strings.TrimSuffix(frameSrc, ext) + "_enhanced" + ext
	filter := o.eqFilter()

	cmd := exec.Command(cfg.FFmpeg, "-y",
		"-i", frameSrc,
		"-vf", filter,
		"-loglevel", "error", out)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return Enhance{}, fmt.Errorf("ffmpeg eq enhance %s: %v: %s", frameLabel, err, tailStr(errBuf.String()))
	}

	return Enhance{
		Frame:      frameLabel,
		SourcePath: filepath.ToSlash(frameSrc),
		OutputPath: filepath.ToSlash(out),
		Filter:     filter,
		Brightness: o.brightness,
		Contrast:   o.contrast,
		Gamma:      o.gamma,
		Saturation: o.saturation,
		Note:       note,
	}, nil
}

// tailStr trims and caps an ffmpeg stderr blob for error messages.
func tailStr(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 600 {
		return s[len(s)-600:]
	}
	return s
}
