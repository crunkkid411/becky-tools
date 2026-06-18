package reel

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"becky-go/internal/config"
	"becky-go/internal/pathx"
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
