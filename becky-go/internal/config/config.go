// Package config loads shared becky settings from ~/.becky/config.json,
// falling back to values auto-detected for this machine. Tools never hardcode
// paths; they read them here so a single config edit retargets every tool.
package config

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Config holds the external paths and defaults every becky tool depends on.
type Config struct {
	Python           string `json:"python"`             // interpreter with sherpa_onnx + torch
	ParakeetModelDir string `json:"parakeet_model_dir"` // sherpa-onnx Parakeet-TDT-0.6B-v3 dir
	AutoEditor       string `json:"auto_editor"`        // auto-editor binary
	FFmpeg           string `json:"ffmpeg"`
	FFprobe          string `json:"ffprobe"`
	SileroVADModel   string `json:"silero_vad_model"`   // silero_vad.onnx for sherpa-onnx VAD
	VADScript        string `json:"vad_script"`         // legacy torch Silero analyzer (optional)
	DiarSegModel     string `json:"diar_seg_model"`     // pyannote-segmentation-3.0 model.onnx
	SpeakerEmbModel  string `json:"speaker_emb_model"`  // CAM++ 3D-Speaker embedding onnx
	Sqlite3          string `json:"sqlite3"`            // sqlite3.exe CLI (for sqlite-vec DB access)
	SqliteVecExt     string `json:"sqlite_vec_ext"`     // sqlite-vec vec0 loadable extension (vec0.dll)
	EmbedModelCache  string `json:"embed_model_cache"`  // sentence-transformers cache dir (Qwen3 weights)
	EmbedServerURL   string `json:"embed_server_url"`   // resident llama-server embedding endpoint (Qwen3-4B)
	EmbedServerModel string `json:"embed_server_model"` // Qwen3-Embedding-4B GGUF path (served by start-embed-server.bat)
	EmbedModel       string `json:"embed_model"`        // default embedding model name: qwen3-4b (server) | qwen3-0.6b (in-process)
	// Gemma-4 E4B-it audio-visual model (becky-validate / internal/avlm). The
	// GGUF + BF16 multimodal projector run on llama.cpp (llama-mtmd-cli /
	// llama-server). BF16 mmproj is required — other quants corrupt audio.
	GemmaModel     string `json:"gemma_model"`      // DEFAULT AVLM: Gemma-4 E4B-it QAT GGUF (Unsloth UD-Q4_K_XL)
	GemmaMMProj    string `json:"gemma_mmproj"`     // BF16 multimodal projector GGUF (vision + audio)
	GemmaModel12B  string `json:"gemma_model_12b"`  // ALTERNATE AVLM: Gemma-4 12B-it QAT GGUF (select via BECKY_AVLM_VARIANT=12b)
	GemmaMMProj12B string `json:"gemma_mmproj_12b"` // BF16 multimodal projector GGUF for the 12B model
	LlamaMtmdCLI   string `json:"llama_mtmd_cli"`   // DEPRECATED: llama-mtmd-cli.exe hard-crashes on Gemma-4; avlm uses llama-server instead
	LlamaServer    string `json:"llama_server"`     // llama-server.exe (becky-validate spawns/reuses this for multimodal inference)
	Web2mdPython   string `json:"web2md_python"`    // interpreter with trafilatura/markdownify/bs4/pyyaml/lxml
	FacePython     string `json:"face_python"`      // interpreter with insightface + onnxruntime + cv2
	FacePyLib      string `json:"face_pylib"`       // extra site-packages dir put on PYTHONPATH for face deps
	FaceModelRoot  string `json:"face_model_root"`  // insightface root (holds models/<name>/)
	FaceModelName  string `json:"face_model_name"`  // insightface model pack, e.g. buffalo_l
	Codec          string `json:"codec"`            // h264_nvenc (never libx264)
	Device         string `json:"device"`           // cpu or cuda
}

// GemmaAVLM resolves the ACTIVE audio-visual model (GGUF path, BF16 mmproj path,
// display label) for becky-validate / becky-edit. The default is the QAT E4B
// model; setting BECKY_AVLM_VARIANT=12b selects the larger 12B QAT model WHEN its
// file is present (otherwise it stays on E4B — degrade, never crash). This is the
// "default E4B, selectable 12B alternate" switch from research/gemma4-qat-upgrade.md.
func (c Config) GemmaAVLM() (model, mmproj, label string) {
	if strings.EqualFold(os.Getenv("BECKY_AVLM_VARIANT"), "12b") && fileExists(c.GemmaModel12B) {
		mp := c.GemmaMMProj12B
		if mp == "" {
			mp = c.GemmaMMProj
		}
		return c.GemmaModel12B, mp, gemmaLabel(c.GemmaModel12B)
	}
	return c.GemmaModel, c.GemmaMMProj, gemmaLabel(c.GemmaModel)
}

// gemmaLabel derives a short, honest display label from a GGUF filename so a
// report names the model that actually ran (QAT vs legacy, E4B vs 12B).
func gemmaLabel(path string) string {
	if strings.TrimSpace(path) == "" {
		return "gemma-4"
	}
	base := strings.ToLower(filepath.Base(path))
	switch {
	case strings.Contains(base, "12b") && strings.Contains(base, "qat"):
		return "gemma-4-12B-it-qat"
	case strings.Contains(base, "12b"):
		return "gemma-4-12B-it"
	case strings.Contains(base, "qat"):
		return "gemma-4-E4B-it-qat"
	default:
		return "gemma-4-E4B-it"
	}
}

// Path returns the location of the shared config file.
func Path() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".becky", "config.json")
	}
	return filepath.Join(home, ".becky", "config.json")
}

// Load merges auto-detected defaults with overrides from ~/.becky/config.json.
func Load() Config {
	cfg := defaults()
	data, err := os.ReadFile(Path())
	if err == nil {
		var override Config
		if json.Unmarshal(data, &override) == nil {
			cfg = merge(cfg, override)
		}
	}
	return cfg
}

func defaults() Config {
	return Config{
		Python: detectPython(),
		ParakeetModelDir: firstExisting(
			`X:\AI-2\kevs-obsidian-ingestion-engine\models\asr\sherpa-onnx-nemo-parakeet-tdt-0.6b-v3-int8`,
		),
		AutoEditor: resolve("auto-editor", `C:\Users\only1\bin\auto-editor.exe`),
		FFmpeg:     resolve("ffmpeg", `C:\ProgramData\anaconda3\Library\bin\ffmpeg.exe`),
		FFprobe:    resolve("ffprobe", `C:\ProgramData\anaconda3\Library\bin\ffprobe.exe`),
		SileroVADModel: firstExisting(
			`X:\AI-2\becky-tools\models\silero_vad.onnx`,
			`C:\Users\only1\.bun\install\cache\@jjhbw\silero-vad@1.0.3@@@1\weights\silero_vad.onnx`,
		),
		VADScript: firstExisting(
			`X:\AI-2\content_generators\auto-editor\resources\vad_analyzer.py`,
		),
		DiarSegModel: firstExisting(
			`X:\AI-2\kevs-obsidian-ingestion-engine\models\diarization\sherpa-onnx-pyannote-segmentation-3-0\model.onnx`,
		),
		SpeakerEmbModel: firstExisting(
			`X:\AI-2\kevs-obsidian-ingestion-engine\models\diarization\3dspeaker_speech_campplus_sv_en_voxceleb_16k.onnx`,
		),
		Sqlite3: resolve("sqlite3", `C:\ProgramData\anaconda3\Library\bin\sqlite3.exe`),
		SqliteVecExt: firstExisting(
			`X:\AI-2\kevs-obsidian-ingestion-engine\models\sqlite-vec\vec0.dll`,
		),
		EmbedModelCache: firstExisting(
			`X:\AI-2\kevs-obsidian-ingestion-engine\models\embeddings`,
		),
		// Resident llama-server embedding backend (Qwen3-Embedding-4B). The server
		// itself is launched by start-embed-server.bat (NOT by the Go tools); these
		// just point the tools at the endpoint + record the served GGUF for clarity.
		EmbedServerURL: "http://127.0.0.1:8088",
		EmbedServerModel: firstExisting(
			`X:\AI-2\becky-tools\models\embeddings\gguf\Qwen3-Embedding-4B-Q5_K_M.gguf`,
		),
		EmbedModel: "qwen3-4b",
		// Gemma-4 audio-visual model + BF16 projector for becky-validate / becky-edit.
		// DEFAULT is the QAT (quantization-aware-trained) E4B GGUF — near-bf16 quality
		// at 4-bit memory, the Unsloth UD-Q4_K_XL build a naïve q4_0 would throw away
		// (research/gemma4-qat-upgrade.md). Falls back to the legacy non-QAT E4B GGUF
		// when the QAT file isn't downloaded yet, so there is no regression. The 12B
		// QAT is the selectable alternate (BECKY_AVLM_VARIANT=12b). Downloaded to
		// models/gemma4/ by scripts/get-gemma4-qat.ps1. BF16 mmproj is mandatory.
		GemmaModel: firstExisting(
			`X:\AI-2\becky-tools\models\gemma4\gemma-4-E4B-it-qat-UD-Q4_K_XL.gguf`,
			`X:\AI-2\becky-tools\models\gemma4\gemma-4-E4B-it-Q4_K_M.gguf`,
		),
		GemmaMMProj: firstExisting(
			`X:\AI-2\becky-tools\models\gemma4\mmproj-BF16.gguf`,
		),
		GemmaModel12B: firstExisting(
			`X:\AI-2\becky-tools\models\gemma4\gemma-4-12B-it-qat-UD-Q4_K_XL.gguf`,
		),
		GemmaMMProj12B: firstExisting(
			`X:\AI-2\becky-tools\models\gemma4\mmproj-12B-BF16.gguf`,
			`X:\AI-2\becky-tools\models\gemma4\mmproj-BF16.gguf`,
		),
		LlamaMtmdCLI: resolve("llama-mtmd-cli", `C:\llama.cpp\build\bin\llama-mtmd-cli.exe`),
		LlamaServer:  resolve("llama-server", `C:\llama.cpp\build\bin\llama-server.exe`),
		Web2mdPython: detectWeb2mdPython(),
		// Face recognition (insightface buffalo_l): the deps were pip-installed for
		// the anaconda interpreter but land in a --target site-packages dir that is
		// NOT on the default path, so FacePyLib is exported via PYTHONPATH at runtime.
		FacePython:    detectWeb2mdPython(), // anaconda base: has insightface/onnxruntime/cv2 via FacePyLib
		FacePyLib:     firstExisting(`X:\PythonUserBase\Lib\site-packages`),
		FaceModelRoot: `X:\AI-2\becky-tools\models\face`,
		FaceModelName: "buffalo_l",
		Codec:         "h264_nvenc",
		Device:        "cpu",
	}
}

// detectWeb2mdPython prefers an interpreter that already has the web-extraction
// stack (trafilatura/markdownify/bs4/pyyaml/lxml). Anaconda base is the verified
// target on this machine; falls back to the kevs venv, then PATH python.
func detectWeb2mdPython() string {
	candidates := []string{
		`C:\ProgramData\anaconda3\python.exe`,
		`X:\AI-2\kevs-obsidian-ingestion-engine\.venv\Scripts\python.exe`,
	}
	for _, c := range candidates {
		if fileExists(c) {
			return c
		}
	}
	if p, err := exec.LookPath("python"); err == nil {
		return p
	}
	return "python"
}

// detectPython prefers the kevs venv (it already has sherpa_onnx + torch),
// then any python on PATH.
func detectPython() string {
	candidates := []string{
		`X:\AI-2\kevs-obsidian-ingestion-engine\.venv\Scripts\python.exe`,
	}
	for _, c := range candidates {
		if fileExists(c) {
			return c
		}
	}
	if p, err := exec.LookPath("python"); err == nil {
		return p
	}
	return "python"
}

// resolve prefers a binary on PATH, then a known fallback, then the bare name.
func resolve(name, fallback string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	if fileExists(fallback) {
		return fallback
	}
	return name
}

func firstExisting(paths ...string) string {
	for _, p := range paths {
		if fileExists(p) {
			return p
		}
	}
	if len(paths) > 0 {
		return paths[0]
	}
	return ""
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func merge(base, over Config) Config {
	if over.Python != "" {
		base.Python = over.Python
	}
	if over.ParakeetModelDir != "" {
		base.ParakeetModelDir = over.ParakeetModelDir
	}
	if over.AutoEditor != "" {
		base.AutoEditor = over.AutoEditor
	}
	if over.FFmpeg != "" {
		base.FFmpeg = over.FFmpeg
	}
	if over.FFprobe != "" {
		base.FFprobe = over.FFprobe
	}
	if over.SileroVADModel != "" {
		base.SileroVADModel = over.SileroVADModel
	}
	if over.VADScript != "" {
		base.VADScript = over.VADScript
	}
	if over.DiarSegModel != "" {
		base.DiarSegModel = over.DiarSegModel
	}
	if over.SpeakerEmbModel != "" {
		base.SpeakerEmbModel = over.SpeakerEmbModel
	}
	if over.Sqlite3 != "" {
		base.Sqlite3 = over.Sqlite3
	}
	if over.SqliteVecExt != "" {
		base.SqliteVecExt = over.SqliteVecExt
	}
	if over.EmbedModelCache != "" {
		base.EmbedModelCache = over.EmbedModelCache
	}
	if over.EmbedServerURL != "" {
		base.EmbedServerURL = over.EmbedServerURL
	}
	if over.EmbedServerModel != "" {
		base.EmbedServerModel = over.EmbedServerModel
	}
	if over.EmbedModel != "" {
		base.EmbedModel = over.EmbedModel
	}
	if over.GemmaModel != "" {
		base.GemmaModel = over.GemmaModel
	}
	if over.GemmaMMProj != "" {
		base.GemmaMMProj = over.GemmaMMProj
	}
	if over.GemmaModel12B != "" {
		base.GemmaModel12B = over.GemmaModel12B
	}
	if over.GemmaMMProj12B != "" {
		base.GemmaMMProj12B = over.GemmaMMProj12B
	}
	if over.LlamaMtmdCLI != "" {
		base.LlamaMtmdCLI = over.LlamaMtmdCLI
	}
	if over.LlamaServer != "" {
		base.LlamaServer = over.LlamaServer
	}
	if over.Web2mdPython != "" {
		base.Web2mdPython = over.Web2mdPython
	}
	if over.FacePython != "" {
		base.FacePython = over.FacePython
	}
	if over.FacePyLib != "" {
		base.FacePyLib = over.FacePyLib
	}
	if over.FaceModelRoot != "" {
		base.FaceModelRoot = over.FaceModelRoot
	}
	if over.FaceModelName != "" {
		base.FaceModelName = over.FaceModelName
	}
	if over.Codec != "" {
		base.Codec = over.Codec
	}
	if over.Device != "" {
		base.Device = over.Device
	}
	return base
}
