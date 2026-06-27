// becky-proxy — build a low-res, INTRA-FRAME, constant-frame-rate SCRUB proxy
// for a video so timeline scrubbing / frame-stepping is snappy in any MLT editor
// (the Shotcut fork, kdenlive) or the becky-clip <video> preview.
//
// Long-GOP H.264/HEVC scrubs slowly because every seek must decode a whole group
// of pictures; an all-intra constant-frame-rate proxy makes every seek decode
// exactly one frame. The source is opened READ-ONLY and is what any final or
// forensic export must use — NEVER this proxy. See HANDOFF-PROXY-SNAPPINESS.md.
//
//	becky-proxy --src <video> [--out <dir>]   build + verify a scrub proxy, print JSON
//	becky-proxy --selftest                    synthesize a long-GOP clip, build a
//	                                          scrub proxy, ffprobe-verify it is
//	                                          intra-frame + CFR, print PASS/FAIL
//
// Codec/resolution are tunable WITHOUT a rebuild via BECKY_PROXY_CODEC
// (h264 [default] | dnxhr | mjpeg) and BECKY_PROXY_RES (scale height, default
// 540). Offline, deterministic; needs ffmpeg/ffprobe (degrades clearly without).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/reel"
)

// report is the JSON emitted for a build. The intra_frame/cfr booleans are an
// honest self-verification: every run ffprobes its own output and reports
// whether the proxy is actually scrub-friendly, not just that a file appeared.
type report struct {
	Source     string `json:"source"`
	Proxy      string `json:"proxy"`
	Codec      string `json:"codec"`       // actual codec_name of the proxy (ffprobe)
	IntraFrame bool   `json:"intra_frame"` // every checked frame is a keyframe
	CFR        bool   `json:"cfr"`         // constant frame rate (avg == r frame rate)
	Frames     int    `json:"frames"`      // frames inspected for the keyframe check
	Note       string `json:"note,omitempty"`
}

func main() {
	fs := flag.NewFlagSet("becky-proxy", flag.ExitOnError)
	src := fs.String("src", "", "source video to build a scrub proxy for")
	outDir := fs.String("out", "", "output directory (default: alongside the source)")
	selftest := fs.Bool("selftest", false, "synthesize a long-GOP clip, build+verify a scrub proxy, then exit")
	_ = fs.Parse(os.Args[1:])

	cfg := config.Load()
	if *selftest {
		runSelftest(cfg)
		return
	}
	if *src == "" {
		beckyio.Fatalf("--src is required (or use --selftest)")
	}
	if _, err := os.Stat(*src); err != nil {
		beckyio.Fatalf("source not found: %s", *src)
	}
	dir := *outDir
	if dir == "" {
		dir = filepath.Dir(*src)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		beckyio.Fatalf("create out dir: %v", err)
	}

	proxy, err := reel.ScrubProxy(*src, dir)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}
	rep := report{Source: *src, Proxy: proxy}
	if v, e := verifyProxy(cfg.FFprobe, proxy); e == nil {
		rep.Codec, rep.IntraFrame, rep.CFR, rep.Frames = v.codec, v.intra, v.cfr, v.frames
	} else {
		rep.Note = "proxy built but could not be verified: " + e.Error()
	}
	beckyio.PrintJSON(rep)
}

// runSelftest is the one-command runtime proof: build a long-GOP H.264 source
// (only frame 0 is a keyframe), run becky's own ScrubProxy on it, then prove the
// SOURCE was long-GOP and the PROXY is all-intra + CFR. Without ffmpeg/ffprobe
// (e.g. CI/cloud) it degrades to a clear skip and exits 0 — the arg correctness
// is covered there by reel's unit tests.
func runSelftest(cfg config.Config) {
	if !runnable(cfg.FFmpeg) || !runnable(cfg.FFprobe) {
		beckyio.PrintJSON(map[string]any{
			"selftest": "skipped",
			"note":     "ffmpeg/ffprobe not available; arg correctness is covered by reel unit tests",
		})
		return
	}
	tmp, err := os.MkdirTemp("", "becky-proxy-selftest")
	if err != nil {
		beckyio.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(tmp)

	srcPath := filepath.Join(tmp, "longgop_src.mp4")
	// A 2s 30fps testsrc encoded with a 250-frame GOP -> a single keyframe: the
	// long-GOP shape that scrubs badly.
	mk := exec.Command(cfg.FFmpeg, "-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=size=640x480:rate=30:duration=2",
		"-c:v", "libx264", "-g", "250", "-keyint_min", "250", "-pix_fmt", "yuv420p",
		srcPath)
	if out, e := mk.CombinedOutput(); e != nil {
		beckyio.Fatalf("synthesize source: %v\n%s", e, out)
	}

	srcV, err := verifyProxy(cfg.FFprobe, srcPath)
	if err != nil {
		beckyio.Fatalf("probe source: %v", err)
	}
	proxy, err := reel.ScrubProxy(srcPath, tmp)
	if err != nil {
		beckyio.Fatalf("ScrubProxy: %v", err)
	}
	proxyV, err := verifyProxy(cfg.FFprobe, proxy)
	if err != nil {
		beckyio.Fatalf("probe proxy: %v", err)
	}

	pass := !srcV.intra && proxyV.intra && proxyV.cfr
	result := map[string]any{
		"selftest":          map[string]bool{"pass": pass},
		"source_long_gop":   !srcV.intra,    // want true (source is the bad case)
		"source_keyframes":  srcV.keyframes, // ~1 for a single GOP
		"proxy_intra_frame": proxyV.intra,   // want true (every frame a keyframe)
		"proxy_cfr":         proxyV.cfr,     // want true
		"proxy_codec":       proxyV.codec,
		"proxy_frames":      proxyV.frames,
		"proxy_keyframes":   proxyV.keyframes,
		"proxy":             proxy,
	}
	beckyio.PrintJSON(result)
	if !pass {
		os.Exit(1)
	}
}

type probeResult struct {
	codec     string
	cfr       bool
	intra     bool // every inspected frame is a keyframe
	frames    int  // frames inspected
	keyframes int  // of those, how many were keyframes
}

// verifyProxy ffprobes a file: its video codec, whether the rate is constant
// (avg_frame_rate == r_frame_rate), and whether every frame in the first 2s is a
// keyframe (the intra-frame property that makes scrubbing cheap).
func verifyProxy(ffprobe, file string) (probeResult, error) {
	var pr probeResult
	if !runnable(ffprobe) {
		return pr, fmt.Errorf("ffprobe not available")
	}

	// Stream-level: codec + frame rates.
	sOut, err := exec.Command(ffprobe, "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=codec_name,avg_frame_rate,r_frame_rate",
		"-of", "json", file).Output()
	if err != nil {
		return pr, fmt.Errorf("ffprobe stream: %w", err)
	}
	var sParsed struct {
		Streams []struct {
			CodecName string `json:"codec_name"`
			Avg       string `json:"avg_frame_rate"`
			R         string `json:"r_frame_rate"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(sOut, &sParsed); err != nil {
		return pr, fmt.Errorf("parse stream json: %w", err)
	}
	if len(sParsed.Streams) == 0 {
		return pr, fmt.Errorf("no video stream")
	}
	s := sParsed.Streams[0]
	pr.codec = s.CodecName
	pr.cfr = s.Avg != "" && s.Avg != "0/0" && s.Avg == s.R

	// Frame-level: count keyframes over the first 2 seconds.
	fOut, err := exec.Command(ffprobe, "-v", "error", "-select_streams", "v:0",
		"-read_intervals", "%+2", "-show_entries", "frame=key_frame",
		"-of", "json", file).Output()
	if err != nil {
		return pr, fmt.Errorf("ffprobe frames: %w", err)
	}
	var fParsed struct {
		Frames []struct {
			KeyFrame int `json:"key_frame"`
		} `json:"frames"`
	}
	if err := json.Unmarshal(fOut, &fParsed); err != nil {
		return pr, fmt.Errorf("parse frames json: %w", err)
	}
	pr.frames = len(fParsed.Frames)
	for _, f := range fParsed.Frames {
		if f.KeyFrame == 1 {
			pr.keyframes++
		}
	}
	pr.intra = pr.frames > 0 && pr.keyframes == pr.frames
	return pr, nil
}

// runnable reports whether a binary path looks usable (exists on disk or
// resolves on PATH).
func runnable(bin string) bool {
	if bin == "" {
		return false
	}
	if _, err := os.Stat(bin); err == nil {
		return true
	}
	_, err := exec.LookPath(bin)
	return err == nil
}
