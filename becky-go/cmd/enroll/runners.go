// runners.go — external-tool helpers for enrollment: shell out to becky-diarize
// (reusing its proven sherpa-onnx + VAD-gated recipe rather than reimplementing
// diarization), sample frames with ffmpeg, and copy files into the KB.
//
// becky-enroll is a thin orchestrator: it CHAINS the existing becky-diarize.exe and
// the shared internal/faceembed runner. It never re-implements their compute.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/mediainfo"
	"becky-go/internal/osintexport"
)

// reSpeakerID extracts a SPEAKER_NN token from a free-text "who speaks" note.
var reSpeakerID = regexp.MustCompile(`(?i)(SPEAKER[_ ]?\d{1,2})`)

// diarOutput mirrors the becky-diarize JSON contract (file/duration/speakers).
type diarOutput struct {
	File     string        `json:"file"`
	Duration float64       `json:"duration"`
	Speakers []diarSpeaker `json:"speakers"`
}

// diarSpeaker is one diarized cluster and its segments.
type diarSpeaker struct {
	ID       string        `json:"id"`
	Segments []diarSegment `json:"segments"`
}

// diarSegment is one (start,end) span attributed to a speaker.
type diarSegment struct {
	Start      float64 `json:"start"`
	End        float64 `json:"end"`
	Confidence float64 `json:"confidence"`
}

// runDiarize shells out to becky-diarize.exe over a video and parses its JSON.
// Reusing the binary gives us its VAD gating + sherpa recipe unchanged.
func runDiarize(diarizeBin, video, device string, verbose bool) (diarOutput, error) {
	if diarizeBin == "" {
		return diarOutput{}, fmt.Errorf("becky-diarize binary not found")
	}
	args := []string{video, "--format", "json"}
	if device != "" {
		args = append(args, "--device", device)
	}
	if verbose {
		args = append(args, "--verbose")
	}
	cmd := exec.Command(diarizeBin, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	if verbose {
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stderr = &stderr
	}
	if err := cmd.Run(); err != nil {
		return diarOutput{}, fmt.Errorf("%v: %s", err, tail(stderr.String()))
	}
	var out diarOutput
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &out); err != nil {
		return diarOutput{}, fmt.Errorf("parse diarize output: %w", err)
	}
	return out, nil
}

// sampleFrames extracts frames every faceSampleEvery seconds into a temp dir and
// returns parallel (path, timestampSec) slices. Reuses osintexport.ExtractFrame so
// frame extraction matches the rest of the toolset.
func sampleFrames(cfg config.Config, video string, info mediainfo.Info, verbose bool) ([]string, []float64, error) {
	dur := info.Duration
	if dur <= 0 {
		dur = faceSampleEvery
	}
	dir, err := os.MkdirTemp("", "becky_enroll_frames_")
	if err != nil {
		return nil, nil, err
	}
	var paths []string
	var times []float64
	idx := 0
	for t := 0.0; t < dur && len(paths) < faceMaxFrames; t += faceSampleEvery {
		p := filepath.Join(dir, fmt.Sprintf("f_%04d.jpg", idx))
		if err := osintexport.ExtractFrame(cfg.FFmpeg, video, t, p, "jpg", faceJPEGQuality); err == nil {
			paths = append(paths, p)
			times = append(times, t)
		}
		idx++
	}
	beckyio.Logf(verbose, "  sampled %d frame(s) every %.0fs from %s", len(paths), faceSampleEvery, filepath.Base(video))
	if len(paths) == 0 {
		os.RemoveAll(dir)
	}
	return paths, times, nil
}

// copyFile copies src to dst, creating parent dirs. Source files are never modified.
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
