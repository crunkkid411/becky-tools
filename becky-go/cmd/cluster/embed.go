// embed.go — produce appearance embeddings from raw video/audio clips, reusing
// becky's existing model helpers so clustering sees the SAME vectors becky-identify
// would. This is the most directly verifiable input mode: hand becky-cluster a few
// clips and it embeds + clusters them end-to-end, no DB or identify.json needed.
//
//   - VOICE: extract 16 kHz mono WAV per clip (ffmpeg), VAD-gate to speech, run the
//     embedded voice_embed.py (CAM++ speaker embedding) — identical recipe to
//     cmd/identify's voice path. One appearance per clip (the clip's dominant
//     speaker). The deployed CAM++ model outputs 512-d here (not the 192-d the spec
//     assumed); the code records whatever dim it gets. Diarizing
//     each clip into multiple speakers is a future refinement; clip-level voice
//     prints are exactly what the cross-corpus "recurring stranger" question needs.
//   - FACE: sample frames (rotation-corrected, dense — same as cmd/identify), embed
//     via the shared internal/faceembed runner (InsightFace buffalo_l, 512-d), keep
//     the single most-prominent detected face per clip as that clip's appearance.
//     Face needs the F1 rotation fix + enrolled-quality frames (SPEC §2); this path
//     degrades gracefully (a clip with no detectable face contributes nothing).
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/faceembed"
	"becky-go/internal/mediainfo"
	"becky-go/internal/osintexport"
	"becky-go/internal/pyhelpers"
)

// embedClips turns a list of clip paths into appearance records for the requested
// modality. Clips that cannot be embedded (no audio / no face / probe failure) are
// skipped with a verbose note rather than failing the run (graceful degrade).
func embedClips(cfg config.Config, clips []string, modality, device string, verbose bool) ([]appearance, error) {
	switch modality {
	case "voice":
		return embedVoiceClips(cfg, clips, device, verbose)
	case "face":
		return embedFaceClips(cfg, clips, device, verbose)
	default:
		return nil, fmt.Errorf("embedClips: unsupported modality %q", modality)
	}
}

// embedVoiceClips extracts one CAM++ voice embedding per clip (dominant speaker)
// using the same voice_embed.py helper + VAD gating as becky-identify.
func embedVoiceClips(cfg config.Config, clips []string, device string, verbose bool) ([]appearance, error) {
	if cfg.SpeakerEmbModel == "" || !fileExists(cfg.SpeakerEmbModel) {
		return nil, fmt.Errorf("speaker embedding model not found: %q", cfg.SpeakerEmbModel)
	}
	script, err := pyhelpers.Materialize("voice_embed.py", pyhelpers.VoiceEmbed)
	if err != nil {
		return nil, fmt.Errorf("materialize voice helper: %w", err)
	}

	var apps []appearance
	for _, clip := range clips {
		info, perr := mediainfo.Probe(cfg.FFprobe, clip)
		if perr != nil || !info.HasAudio {
			beckyio.Logf(verbose, "  voice: skip %s (no audio / probe failed)", filepath.Base(clip))
			continue
		}
		wav, werr := extractMonoWAV(cfg.FFmpeg, clip)
		if werr != nil {
			beckyio.Logf(verbose, "  voice: skip %s (audio extract failed: %v)", filepath.Base(clip), werr)
			continue
		}
		vec, eerr := runVoiceEmbed(cfg, script, wav, device, verbose)
		os.Remove(wav)
		if eerr != nil || len(vec) == 0 {
			beckyio.Logf(verbose, "  voice: skip %s (embed failed: %v)", filepath.Base(clip), eerr)
			continue
		}
		apps = append(apps, appearance{
			ID:           appearanceID(clip, "voice", 0),
			Modality:     "voice",
			SourceFile:   clip,
			SourceSHA256: sha12(clip),
			Timestamp:    0,
			FrameIndex:   0,
			SpeakerID:    "SPEAKER_00",
			DetScore:     1.0,
			Vector:       normalize(vec),
		})
		beckyio.Logf(verbose, "  voice: embedded %s (dim=%d)", filepath.Base(clip), len(vec))
	}
	return apps, nil
}

// embedFaceClips samples rotation-corrected frames per clip and keeps the single
// most-prominent detected face as that clip's appearance. Mirrors cmd/identify's
// sampling cadence (1 fps base, capped) so the vectors are comparable.
func embedFaceClips(cfg config.Config, clips []string, device string, verbose bool) ([]appearance, error) {
	if cfg.FaceModelRoot == "" {
		return nil, fmt.Errorf("face model root not configured")
	}
	var apps []appearance
	for _, clip := range clips {
		info, perr := mediainfo.Probe(cfg.FFprobe, clip)
		if perr != nil || !info.HasVideo {
			beckyio.Logf(verbose, "  face: skip %s (no video / probe failed)", filepath.Base(clip))
			continue
		}
		best, ts, fidx, ferr := bestFaceInClip(cfg, info, clip, device, verbose)
		if ferr != nil {
			beckyio.Logf(verbose, "  face: skip %s (%v)", filepath.Base(clip), ferr)
			continue
		}
		if best == nil {
			beckyio.Logf(verbose, "  face: no face detected in %s", filepath.Base(clip))
			continue
		}
		apps = append(apps, appearance{
			ID:           appearanceID(clip, "face", fidx),
			Modality:     "face",
			SourceFile:   clip,
			SourceSHA256: sha12(clip),
			Timestamp:    ts,
			FrameIndex:   fidx,
			DetScore:     best.DetScore,
			Vector:       normalize(best.Vector),
		})
		beckyio.Logf(verbose, "  face: embedded %s (det=%.2f @ %.1fs)", filepath.Base(clip), best.DetScore, ts)
	}
	return apps, nil
}

const (
	faceSampleEverySec = 1.0
	faceMaxFrames      = 60
	faceJPEGQuality    = 3
)

// bestFaceInClip samples frames and returns the most-prominent detected face
// (highest det_score) across the clip, with its timestamp + frame index.
func bestFaceInClip(cfg config.Config, info mediainfo.Info, clip, device string, verbose bool) (*faceembed.Face, float64, int, error) {
	dur := info.Duration
	if dur <= 0 {
		dur = faceSampleEverySec
	}
	step := faceSampleEverySec
	if dur > faceSampleEverySec*float64(faceMaxFrames) {
		step = dur / float64(faceMaxFrames)
	}
	rot := osintexport.DisplayRotation(cfg.FFprobe, clip)
	dir, err := os.MkdirTemp("", "becky_clusterfaces_")
	if err != nil {
		return nil, 0, 0, err
	}
	defer os.RemoveAll(dir)

	var paths []string
	var times []float64
	idx := 0
	for t := 0.0; t < dur && len(paths) < faceMaxFrames; t += step {
		p := filepath.Join(dir, fmt.Sprintf("f_%04d.jpg", idx))
		if e := osintexport.ExtractFrameRotated(cfg.FFmpeg, clip, t, p, "jpg", faceJPEGQuality, rot); e == nil {
			paths = append(paths, p)
			times = append(times, t)
		}
		idx++
	}
	if len(paths) == 0 {
		return nil, 0, 0, fmt.Errorf("no frames sampled")
	}
	recs, err := faceembed.Embed(cfg, paths, device, verbose)
	if err != nil {
		return nil, 0, 0, err
	}

	fps := info.FPS
	if fps <= 0 {
		fps = 30
	}
	var best *faceembed.Face
	var bestTS float64
	var bestFrame int
	for i := range recs {
		r := recs[i]
		if !r.Found || len(r.Vector) == 0 {
			continue
		}
		if best == nil || r.DetScore > best.DetScore {
			best = &recs[i]
			if i < len(times) {
				bestTS = times[i]
			}
			bestFrame = int(bestTS*fps + 0.5)
		}
	}
	return best, bestTS, bestFrame, nil
}

// runVoiceEmbed runs voice_embed.py over one WAV and returns its 192-d vector.
// Speech-gated via Silero VAD (same as becky-identify) so a clip embedding and an
// enrolled print embedding are gated identically.
func runVoiceEmbed(cfg config.Config, script, wav, device string, verbose bool) ([]float64, error) {
	dev := device
	if dev == "" {
		dev = cfg.Device
	}
	args := []string{script, wav, "--model", cfg.SpeakerEmbModel, "--num-threads", "4", "--device", dev}
	if cfg.SileroVADModel != "" {
		if _, statErr := os.Stat(cfg.SileroVADModel); statErr == nil {
			args = append(args, "--vad-model", cfg.SileroVADModel)
		}
	}
	cmd := exec.Command(cfg.Python, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	if verbose {
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stderr = &stderr
	}
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("voice helper failed: %v\n%s", err, tail(stderr.String()))
	}
	res, ok := parseVoiceHelperJSON(stdout.String())
	if !ok {
		return nil, fmt.Errorf("could not parse voice helper output:\n%s", tail(stdout.String()))
	}
	if res.Skipped {
		return nil, fmt.Errorf("voice helper skipped: %s", res.Reason)
	}
	if len(res.Embeddings) == 0 || len(res.Embeddings[0].Vector) == 0 {
		return nil, fmt.Errorf("voice helper returned no embedding")
	}
	return res.Embeddings[0].Vector, nil
}

// extractMonoWAV writes a 16 kHz mono PCM WAV for sherpa/VAD (standard recipe).
func extractMonoWAV(ffmpeg, input string) (string, error) {
	tmp, err := os.CreateTemp("", "becky_cluster_*.wav")
	if err != nil {
		return "", err
	}
	path := tmp.Name()
	tmp.Close()
	args := []string{"-y", "-i", input, "-vn", "-ac", "1", "-ar", "16000",
		"-acodec", "pcm_s16le", "-loglevel", "error", path}
	cmd := exec.Command(ffmpeg, args...)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		os.Remove(path)
		return "", fmt.Errorf("ffmpeg: %v\n%s", err, tail(errBuf.String()))
	}
	return path, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 800 {
		return s[len(s)-800:]
	}
	return s
}
