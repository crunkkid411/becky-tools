package reel

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"becky-go/internal/config"
	"becky-go/internal/pathx"
	"becky-go/internal/proc"
)

// webSafeCodecs are the video codecs a WebView2 <video> element can decode
// directly; anything else gets a proxy for preview (R-CUT §4d). h264 covers the
// overwhelming majority of evidence already; vp8/vp9/av1 are browser-playable in
// modern Chromium.
var webSafeCodecs = map[string]bool{
	"h264": true,
	"vp8":  true,
	"vp9":  true,
	"av1":  true,
}

// Proxy transcodes source to a lightweight, web-playable H.264 proxy in outDir
// and returns the proxy path. If the source is ALREADY a web-safe codec, it is
// a no-op: Proxy returns the original path and writes nothing. The source is
// opened READ-ONLY; only the proxy file is written. ffprobe-detect happens
// first so a proxy is built only when actually needed.
func Proxy(source, outDir string) (string, error) {
	cfg := config.Load()
	if _, err := os.Stat(source); err != nil {
		return "", fmt.Errorf("source not found: %s", source)
	}

	// Detect the source video codec first (skip the work if it's web-safe).
	if codec, err := videoCodec(cfg.FFprobe, source); err == nil && webSafeCodecs[codec] {
		return source, nil
	}

	if cfg.FFmpeg == "" || !available(cfg.FFmpeg) {
		return "", fmt.Errorf("ffmpeg not available (config FFmpeg=%q); cannot build proxy", cfg.FFmpeg)
	}
	if outDir == "" {
		outDir = "."
	}
	out := proxyPath(source, outDir)
	args := proxyArgs(source, out)
	if err := runFFmpeg(cfg.FFmpeg, false, args); err != nil {
		return "", err
	}
	if _, err := os.Stat(out); err != nil {
		return "", fmt.Errorf("proxy transcode produced no file: %s", out)
	}
	return out, nil
}

// proxyPath builds "<stem>.proxy.mp4" inside outDir, using pathx.Base so a
// Windows source path is handled on any host.
func proxyPath(source, outDir string) string {
	base := pathx.Base(source)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if stem == "" {
		stem = "proxy"
	}
	return mustAbs(filepath.Join(outDir, stem+".proxy.mp4"))
}

// proxyArgs builds the ffmpeg argv for a fast web-safe H.264 proxy with
// faststart (moov before mdat) so the <video> can range-seek instantly
// (R-CUT §4d). PURE (unit-tested).
func proxyArgs(source, out string) []string {
	return []string{
		"-y", "-hide_banner", "-loglevel", "error",
		"-i", source,
		"-c:v", libx264, "-preset", "veryfast", "-pix_fmt", "yuv420p",
		"-movflags", "+faststart",
		"-c:a", "aac",
		out,
	}
}

// needsProxy reports whether a given video codec name requires a proxy (i.e. is
// NOT web-safe). Exposed for testing the decision without ffprobe.
func needsProxy(codec string) bool {
	return !webSafeCodecs[strings.ToLower(strings.TrimSpace(codec))]
}

// ScrubProxy transcodes source to a low-res, INTRA-FRAME, constant-frame-rate
// proxy in outDir and returns the proxy path. Unlike Proxy, it does NOT
// short-circuit on web-safe H.264: long-GOP H.264 is exactly the evidence that
// scrubs slowly — every seek must decode a whole group of pictures — so it still
// needs a scrub proxy where every frame is a keyframe and the rate is constant
// (a VFR source makes the editor "constantly recalculate the next frame"). The
// source is opened READ-ONLY; only the proxy file is written. Use the ORIGINAL —
// never this proxy — for any final/forensic export (export must stay
// frame-accurate to the source). See HANDOFF-PROXY-SNAPPINESS.md.
//
// Codec and resolution are tunable WITHOUT a rebuild via env:
//   - BECKY_PROXY_CODEC: h264 (default, all-intra .mp4, web-playable so
//     becky-clip's <video> benefits too) | dnxhr (.mov) | mjpeg (.mov).
//   - BECKY_PROXY_RES: scale height in px (default 540).
//
// A fresh proxy (exists, non-empty, mtime >= source) is reused so repeated
// previews of the same clip are instant rather than re-transcoded each click.
func ScrubProxy(source, outDir string) (string, error) {
	cfg := config.Load()
	si, err := os.Stat(source)
	if err != nil {
		return "", fmt.Errorf("source not found: %s", source)
	}
	if outDir == "" {
		outDir = "."
	}
	out := scrubProxyPath(source, outDir)
	if fi, e := os.Stat(out); e == nil && fi.Size() > 0 && !fi.ModTime().Before(si.ModTime()) {
		return out, nil // fresh cached proxy — no re-transcode
	}
	if cfg.FFmpeg == "" || !available(cfg.FFmpeg) {
		return "", fmt.Errorf("ffmpeg not available (config FFmpeg=%q); cannot build scrub proxy", cfg.FFmpeg)
	}
	if err := runFFmpeg(cfg.FFmpeg, false, scrubProxyArgs(source, out)); err != nil {
		return "", err
	}
	if _, err := os.Stat(out); err != nil {
		return "", fmt.Errorf("scrub proxy transcode produced no file: %s", out)
	}
	return out, nil
}

// scrubProxyPath builds "<stem>.scrub.<ext>" inside outDir, where ext follows the
// configured scrub codec (.mp4 for h264, .mov for dnxhr/mjpeg). pathx.Base keeps
// a Windows source path correct on any host.
func scrubProxyPath(source, outDir string) string {
	base := pathx.Base(source)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if stem == "" {
		stem = "proxy"
	}
	return mustAbs(filepath.Join(outDir, stem+".scrub"+scrubCodecFor(os.Getenv("BECKY_PROXY_CODEC")).ext))
}

// scrubProxyArgs builds the ffmpeg argv for an INTRA-FRAME, CONSTANT-frame-rate
// scrub proxy: scaled down, fps-locked, every frame a keyframe (so a seek decodes
// exactly one frame). Defaults to the all-intra H.264 recipe; honors
// BECKY_PROXY_CODEC / BECKY_PROXY_RES. PURE (unit-tested).
func scrubProxyArgs(source, out string) []string {
	c := scrubCodecFor(os.Getenv("BECKY_PROXY_CODEC"))
	vf := fmt.Sprintf("scale=-2:%d,fps=30", scrubProxyHeight())
	args := []string{
		"-y", "-hide_banner", "-loglevel", "error",
		"-i", source,
		"-vf", vf,
	}
	args = append(args, c.videoArgs...)
	args = append(args, c.audioArgs...)
	return append(args, out)
}

// scrubProxyHeight resolves BECKY_PROXY_RES to a scale height, defaulting to 540.
// Garbage / non-positive values fall back to the default.
func scrubProxyHeight() int {
	if v := strings.TrimSpace(os.Getenv("BECKY_PROXY_RES")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 540
}

// scrubCodec is one intra-frame scrub-proxy recipe: output extension plus the
// ffmpeg video/audio encoder args. Every option here is INTRA-FRAME so seeking
// never decodes a group of pictures — the whole point of a scrub proxy.
type scrubCodec struct {
	ext       string
	videoArgs []string
	audioArgs []string
}

// scrubCodecFor maps BECKY_PROXY_CODEC to a recipe. Default is all-intra H.264
// (.mp4, web-playable). dnxhr (DNxHR LB, the most reliable scrubber) and mjpeg
// (weak-hardware fallback, biggest files) are .mov alternatives.
func scrubCodecFor(name string) scrubCodec {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "dnxhr", "dnxhd", "dnxhr_lb":
		return scrubCodec{
			ext:       ".mov",
			videoArgs: []string{"-c:v", "dnxhd", "-profile:v", "dnxhr_lb", "-pix_fmt", "yuv422p"},
			audioArgs: []string{"-c:a", "pcm_s16le"},
		}
	case "mjpeg", "mjpg":
		return scrubCodec{
			ext:       ".mov",
			videoArgs: []string{"-c:v", "mjpeg", "-q:v", "5", "-pix_fmt", "yuvj420p"},
			audioArgs: []string{"-c:a", "pcm_s16le"},
		}
	default: // all-intra H.264: every frame a keyframe (-g 1), no scene-cut GOPs.
		return scrubCodec{
			ext: ".mp4",
			videoArgs: []string{
				"-c:v", libx264, "-preset", "veryfast", "-crf", "20",
				"-g", "1", "-keyint_min", "1", "-sc_threshold", "0",
				"-pix_fmt", "yuv420p", "-movflags", "+faststart",
			},
			audioArgs: []string{"-c:a", "aac"},
		}
	}
}

// ScrubProxySegment transcodes ONLY the [inSec,outSec) window of source to a
// low-res, INTRA-FRAME, constant-frame-rate proxy in outDir — the WINDOWED
// sibling of ScrubProxy, for a timeline whose clip only uses a fraction of a
// long source. Same intra-frame/constant-fps recipe as ScrubProxy
// (scrubCodecFor), just bounded to the requested span via an accurate input
// seek (-ss before -i) plus a duration limit (-t after -i), so scrub feel
// matches the whole-file proxy without paying to transcode footage the
// timeline never uses. The source is opened READ-ONLY; only the proxy file is
// written. Use the ORIGINAL — never this proxy — for any final/forensic
// export. See HANDOFF-PROXY-SNAPPINESS.md / ScrubProxy.
//
// A fresh cached proxy (exists, non-empty, mtime >= source) is reused, same as
// ScrubProxy, keyed by BOTH the source and the requested window so different
// timeline clips of the same source cache independently instead of colliding.
func ScrubProxySegment(source string, inSec, outSec float64, outDir string) (string, error) {
	cfg := config.Load()
	si, err := os.Stat(source)
	if err != nil {
		return "", fmt.Errorf("source not found: %s", source)
	}
	if outDir == "" {
		outDir = "."
	}
	out := scrubProxySegmentPath(source, outDir, inSec, outSec)
	if fi, e := os.Stat(out); e == nil && fi.Size() > 0 && !fi.ModTime().Before(si.ModTime()) {
		return out, nil // fresh cached proxy — no re-transcode
	}
	if cfg.FFmpeg == "" || !available(cfg.FFmpeg) {
		return "", fmt.Errorf("ffmpeg not available (config FFmpeg=%q); cannot build windowed scrub proxy", cfg.FFmpeg)
	}
	if err := runFFmpeg(cfg.FFmpeg, false, scrubProxySegmentArgs(source, out, inSec, outSec)); err != nil {
		return "", err
	}
	if _, err := os.Stat(out); err != nil {
		return "", fmt.Errorf("windowed scrub proxy transcode produced no file: %s", out)
	}
	return out, nil
}

// SegmentProxyPath returns the deterministic path a windowed scrub proxy for
// source's [inSec,outSec) window WOULD occupy in outDir — without building it or
// checking whether it exists. Exported so the engine (and its tests) can locate
// the proxy the UI builds lazily. Normalizes the window exactly as ScrubSegment
// does (swap a reversed span, clamp a negative in) so the path matches what
// ScrubProxySegment actually writes.
func SegmentProxyPath(source, outDir string, inSec, outSec float64) string {
	if outSec < inSec {
		inSec, outSec = outSec, inSec
	}
	if inSec < 0 {
		inSec = 0
	}
	if outDir == "" {
		outDir = "."
	}
	return scrubProxySegmentPath(source, outDir, inSec, outSec)
}

// CachedScrubProxySegment returns (path, true) when a FRESH windowed scrub proxy
// for source's [inSec,outSec) window already exists in outDir (present, non-empty,
// and not older than the source), WITHOUT building anything; ("", false) otherwise.
// This lets TimelineEDL PREFER a proxy the UI has already built lazily while safely
// falling back to the raw source when none exists yet — so the timeline can never
// regress to un-playable, only to today's raw-source behavior.
func CachedScrubProxySegment(source string, inSec, outSec float64, outDir string) (string, bool) {
	path := SegmentProxyPath(source, outDir, inSec, outSec)
	fi, err := os.Stat(path)
	if err != nil || fi.Size() == 0 {
		return "", false
	}
	if si, err := os.Stat(source); err == nil && fi.ModTime().Before(si.ModTime()) {
		return "", false // stale — source changed since the proxy was built
	}
	return path, true
}

// scrubProxySegmentPath builds "<stem>.<inMs>-<outMs>.scrub.<ext>" inside
// outDir — the windowed sibling of scrubProxyPath, so each distinct timeline
// span of a source caches to its own file instead of colliding with the
// whole-file scrub proxy or with other windows of the same source.
// Millisecond integers keep the name stable and free of float-formatting
// ambiguity.
func scrubProxySegmentPath(source, outDir string, inSec, outSec float64) string {
	base := pathx.Base(source)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if stem == "" {
		stem = "proxy"
	}
	ext := scrubCodecFor(os.Getenv("BECKY_PROXY_CODEC")).ext
	name := fmt.Sprintf("%s.%d-%d.scrub%s", stem, millis(inSec), millis(outSec), ext)
	return mustAbs(filepath.Join(outDir, name))
}

// millis rounds seconds to the nearest whole millisecond (for cache filenames).
func millis(sec float64) int64 {
	return int64(sec*1000 + 0.5)
}

// scrubProxySegmentArgs builds the ffmpeg argv for a WINDOWED intra-frame,
// constant-frame-rate scrub proxy: an accurate input seek (-ss before -i)
// brackets the start, a duration limit (-t after -i) bounds the end, then the
// SAME scale/fps/codec recipe as scrubProxyArgs runs on just that span. Honors
// the same BECKY_PROXY_CODEC / BECKY_PROXY_RES env overrides. Both -ss and -t
// go through formatSeconds — a fixed "%.3f" (invariant-decimal, never
// locale-dependent) — same as every other ffmpeg time arg in this package.
// PURE (unit-tested).
func scrubProxySegmentArgs(source, out string, inSec, outSec float64) []string {
	c := scrubCodecFor(os.Getenv("BECKY_PROXY_CODEC"))
	vf := fmt.Sprintf("scale=-2:%d,fps=30", scrubProxyHeight())
	args := []string{
		"-y", "-hide_banner", "-loglevel", "error",
		"-ss", formatSeconds(inSec),
		"-i", source,
		"-t", formatSeconds(outSec - inSec),
		"-vf", vf,
	}
	args = append(args, c.videoArgs...)
	args = append(args, c.audioArgs...)
	return append(args, out)
}

// videoCodec returns the first video stream's codec_name via ffprobe. An empty
// codec / probe failure yields ("", err) and the caller treats it as "unknown"
// (build a proxy to be safe).
func videoCodec(ffprobe, source string) (string, error) {
	if ffprobe == "" || !available(ffprobe) {
		return "", fmt.Errorf("ffprobe not available")
	}
	cmd := exec.Command(ffprobe,
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=codec_name",
		"-print_format", "json",
		source,
	)
	proc.NoWindow(cmd) // no console-window flash on video-click (GUI is windowsgui)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("ffprobe failed: %w", err)
	}
	var parsed struct {
		Streams []struct {
			CodecName string `json:"codec_name"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return "", fmt.Errorf("parse ffprobe output: %w", err)
	}
	if len(parsed.Streams) == 0 {
		return "", fmt.Errorf("no video stream")
	}
	return strings.ToLower(parsed.Streams[0].CodecName), nil
}
