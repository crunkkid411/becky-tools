// Package pyhelpers embeds the small Python glue scripts that drive ONNX model
// inference (via sherpa-onnx). The heavy compute runs in sherpa-onnx's C++/ONNX
// core; Python is only the thin binding. Embedding + materializing at runtime
// keeps the compiled .exe self-contained — no loose .py files to ship.
package pyhelpers

import (
	_ "embed"
	"os"
	"path/filepath"
)

//go:embed transcribe_parakeet.py
var TranscribeParakeet []byte

//go:embed vad_silero.py
var VADSilero []byte

//go:embed diarize_sherpa.py
var DiarizeSherpa []byte

//go:embed voice_embed.py
var VoiceEmbed []byte

//go:embed embed_text.py
var EmbedText []byte

//go:embed web2md.py
var Web2md []byte

//go:embed face_embed.py
var FaceEmbed []byte

// Materialize writes an embedded script to a stable temp path and returns it.
func Materialize(name string, content []byte) (string, error) {
	dir := filepath.Join(os.TempDir(), "becky-pyhelpers")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return "", err
	}
	return path, nil
}
