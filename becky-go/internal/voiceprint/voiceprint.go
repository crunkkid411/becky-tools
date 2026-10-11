// Package voiceprint turns stretches of speech into ReDimNet2+ voice prints, so a tool
// can ask "is this the same voice?" (becky-diarize's speaker check). One decode, short
// WAVs cut in Go, ONE helper call for every clip (the model loads once).
package voiceprint

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strings"

	"becky-go/internal/config"
	"becky-go/internal/pyhelpers"
	"becky-go/internal/tts"
)

const rate = 16000

// Span is one stretch of speech, in seconds.
type Span struct{ Start, End float64 }

// Ready says why prints cannot be made here ("" = they can).
func Ready(cfg config.Config) string {
	for _, p := range []string{cfg.SpeakerNameModel, cfg.SoundLabelPython} {
		if p == "" {
			return "the ReDimNet2+ voice model is not set up"
		}
		if _, err := os.Stat(p); err != nil {
			return "the ReDimNet2+ voice model is not set up"
		}
	}
	return ""
}

// Decode reads a file's sound as 16 kHz mono samples.
func Decode(ffmpeg, input string) ([]int16, error) {
	cmd := exec.Command(ffmpeg, "-i", input, "-vn", "-ac", "1", "-ar", "16000",
		"-f", "s16le", "-acodec", "pcm_s16le", "-loglevel", "error", "-")
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg decode: %v: %s", err, strings.TrimSpace(errBuf.String()))
	}
	pcm := make([]int16, len(raw)/2)
	for i := range pcm {
		pcm[i] = int16(binary.LittleEndian.Uint16(raw[2*i:]))
	}
	return pcm, nil
}

// Prints returns one L2-normalised voice print per span (nil where the span had too
// little speech). Temp WAVs are removed.
func Prints(cfg config.Config, pcm []int16, spans []Span, verbose bool) ([][]float64, error) {
	if len(spans) == 0 {
		return nil, nil
	}
	script, err := pyhelpers.Materialize("voice_embed.py", pyhelpers.VoiceEmbed)
	if err != nil {
		return nil, err
	}
	var wavs []string
	defer func() {
		for _, w := range wavs {
			os.Remove(w)
		}
	}()
	for _, s := range spans {
		a, b := int(s.Start*rate), int(s.End*rate)
		if a < 0 {
			a = 0
		}
		if b > len(pcm) {
			b = len(pcm)
		}
		if b <= a {
			b = a
		}
		data, err := tts.WriteWAVPCM16(pcm[a:b], rate)
		if err != nil {
			return nil, err
		}
		f, err := os.CreateTemp("", "becky_vp_*.wav")
		if err != nil {
			return nil, err
		}
		wavs = append(wavs, f.Name())
		_, werr := f.Write(data)
		f.Close()
		if werr != nil {
			return nil, werr
		}
	}
	args := append([]string{script}, wavs...)
	args = append(args, "--model", cfg.SpeakerNameModel, "--backend", "redimnet", "--num-threads", "4", "--device", "cpu")
	cmd := exec.Command(cfg.SoundLabelPython, args...)
	var out, errBuf strings.Builder
	cmd.Stdout = &out
	if verbose {
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stderr = &errBuf
	}
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("voice helper failed: %v %s", err, lastLine(errBuf.String()))
	}
	var res struct {
		Skipped    bool   `json:"skipped"`
		Reason     string `json:"reason"`
		Embeddings []struct {
			Vector []float64 `json:"vector"`
		} `json:"embeddings"`
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if json.Unmarshal([]byte(lines[len(lines)-1]), &res) != nil {
		return nil, fmt.Errorf("could not read the voice helper's answer")
	}
	if res.Skipped {
		return nil, fmt.Errorf("voice helper skipped: %s", res.Reason)
	}
	if len(res.Embeddings) != len(spans) {
		return nil, fmt.Errorf("voice helper gave %d prints for %d clips", len(res.Embeddings), len(spans))
	}
	prints := make([][]float64, len(spans))
	for i, e := range res.Embeddings {
		if len(e.Vector) > 0 {
			prints[i] = Normalize(e.Vector)
		}
	}
	return prints, nil
}

// Normalize scales v to length 1 (a copy).
func Normalize(v []float64) []float64 {
	n := 0.0
	for _, x := range v {
		n += x * x
	}
	n = math.Sqrt(n)
	out := make([]float64, len(v))
	if n == 0 {
		return out
	}
	for i, x := range v {
		out[i] = x / n
	}
	return out
}

// Cosine of two normalised prints.
func Cosine(a, b []float64) float64 {
	s := 0.0
	for i := range a {
		if i < len(b) {
			s += a[i] * b[i]
		}
	}
	return s
}

// Mean is the normalised average of the non-nil prints (nil when there are none).
func Mean(prints [][]float64) []float64 {
	var sum []float64
	for _, p := range prints {
		if p == nil {
			continue
		}
		if sum == nil {
			sum = make([]float64, len(p))
		}
		for i, x := range p {
			sum[i] += x
		}
	}
	if sum == nil {
		return nil
	}
	return Normalize(sum)
}

func lastLine(s string) string {
	l := strings.Split(strings.TrimSpace(s), "\n")
	return l[len(l)-1]
}
