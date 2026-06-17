package scout

import "strings"

// Capability is one thing becky already does (or a domain it lives in), with the
// keywords that mark a video as touching it and the becky tool(s) it maps to. The
// catalog is the deterministic "does this relate to becky?" floor — independent
// of the freshness manifest (which names specific PINNED models) and of any
// model, so a hit here is a genuinely separate signal during corroboration.
type Capability struct {
	Domain   string   `json:"domain"`
	Tools    []string `json:"tools"`
	Keywords []string `json:"keywords"`
	Note     string   `json:"note"` // what content in this area could improve/extend becky
}

// matchCapabilities returns the capabilities whose keywords appear in the
// lower-cased haystack. Keywords are matched as whole-ish substrings; multi-word
// keywords match as phrases.
func matchCapabilities(hay string, catalog []Capability) []Capability {
	var out []Capability
	for _, c := range catalog {
		for _, kw := range c.Keywords {
			if strings.Contains(hay, kw) {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// DefaultCatalog is becky's built-in capability map: one row per domain becky
// operates in, derived from the tool catalog and the freshness manifest. Keep it
// conservative — a keyword here means "this video is in a becky area," which is
// only HALF of a stated conclusion (the other half must be an independent signal).
//
// Keywords are lower-case (the haystack is lower-cased). Prefer specific terms;
// avoid words so generic they fire on unrelated videos.
func DefaultCatalog() []Capability {
	return []Capability{
		{
			Domain:   "speech-to-text (ASR)",
			Tools:    []string{"becky-transcribe", "becky-diarize"},
			Keywords: []string{"speech recognition", "speech-to-text", "transcription", "transcribe", "asr ", "parakeet", "whisper", "nemo", "sherpa-onnx", "sherpa onnx"},
			Note:     "newer/faster/more-accurate ASR or a better timestamp/word-alignment method could improve becky-transcribe.",
		},
		{
			Domain:   "speaker diarization",
			Tools:    []string{"becky-diarize", "becky-identify"},
			Keywords: []string{"diariz", "speaker segmentation", "speaker embedding", "pyannote", "speaker recognition", "who spoke when"},
			Note:     "improved diarization / speaker-embedding models could improve becky-diarize and the voice side of becky-identify.",
		},
		{
			Domain:   "face recognition & clustering",
			Tools:    []string{"becky-identify", "becky-enroll", "becky-cluster"},
			Keywords: []string{"face recognition", "face detection", "facial recognition", "arcface", "insightface", "face embedding", "face clustering", "re-identification", "reid"},
			Note:     "a stronger face detector/embedding (vs InsightFace buffalo_l) could improve becky-identify/enroll/cluster recall.",
		},
		{
			Domain:   "OCR / text in images",
			Tools:    []string{"becky-ocr"},
			Keywords: []string{"ocr", "optical character recognition", "paddleocr", "text detection", "text recognition", "document parsing", "scene text", "rapidocr"},
			Note:     "a newer OCR pipeline (PP-OCRv6+ or a doc-VLM) could improve becky-ocr accuracy on hard frames.",
		},
		{
			Domain:   "embeddings & semantic search",
			Tools:    []string{"becky-embed", "becky-search"},
			Keywords: []string{"text embedding", "embeddings", "semantic search", "vector search", "retrieval", "reranker", "rerank", "qwen3-embedding", "sentence transformer"},
			Note:     "a better embedding model or retrieval/rerank trick could improve becky-embed/becky-search.",
		},
		{
			Domain:   "vision-language models (VLM)",
			Tools:    []string{"becky-vision", "becky-validate"},
			Keywords: []string{"vision language", "vision-language", "vlm", "multimodal model", "image captioning", "visual question answering", "vqa", "lfm2", "liquid foundation", "gemma", "image understanding"},
			Note:     "a right-sized VLM (LFM2.5-VL line) could improve frame triage, becky-ocr doc→JSON, and becky-vision.",
		},
		{
			Domain:   "video understanding (motion/scene/events)",
			Tools:    []string{"becky-motion", "becky-events", "becky-framematch"},
			Keywords: []string{"action recognition", "scene detection", "shot boundary", "object detection", "object tracking", "video understanding", "temporal localization", "optical flow"},
			Note:     "better scene/shot/action detection could improve becky-motion/events/framematch.",
		},
		{
			Domain:   "local LLMs / inference runtime",
			Tools:    []string{"becky-ask", "becky-research"},
			Keywords: []string{"llama.cpp", "llama cpp", "gguf", "quantization", "quantized", "ollama", "local llm", "on-device llm", "inference engine", "vllm", "kv cache", "speculative decoding"},
			Note:     "a faster/leaner local-inference technique (quant, KV-cache, spec-decoding) could improve every becky tool that calls a local model.",
		},
		{
			Domain:   "agents & research harnesses",
			Tools:    []string{"becky-harness", "becky-omni", "becky-research"},
			Keywords: []string{"agentic", "ai agent", "agent framework", "tool use", "tool calling", "react agent", "deep research", "multi-agent", "autonomous agent"},
			Note:     "an agent/orchestration pattern could improve becky-harness/omni/research.",
		},
		{
			Domain:   "OSINT / entity graphs",
			Tools:    []string{"becky-palantir", "becky-osint"},
			Keywords: []string{"osint", "entity graph", "entity resolution", "knowledge graph", "link analysis", "open source intelligence"},
			Note:     "an entity-resolution / link-analysis method could improve becky-palantir/osint.",
		},
		{
			Domain:   "music generation & MIDI",
			Tools:    []string{"becky-compose", "becky-hum"},
			Keywords: []string{"midi generation", "music generation", "symbolic music", "music transformer", "chord progression", "melody generation", "music theory"},
			Note:     "a generation/theory technique could improve becky-compose and the hum→MIDI side of becky-hum.",
		},
		{
			Domain:   "audio analysis (key/tempo/pitch)",
			Tools:    []string{"becky-hum", "becky-vox"},
			Keywords: []string{"pitch detection", "pitch tracking", "f0 estimation", "key detection", "tempo estimation", "beat tracking", "basic-pitch", "crepe", "pyin", "melody extraction"},
			Note:     "a precise f0/key/tempo method could improve becky-hum/becky-vox.",
		},
		{
			Domain:   "DAW / audio production",
			Tools:    []string{"becky-daw", "becky-daw-engine", "becky-mix", "becky-canvas"},
			Keywords: []string{"vst3", "clap plugin", "audio plugin", "vocal alignment", "vocal tuning", "melodyne", "vocalign", "sidechain", "mixing", "mastering"},
			Note:     "a plugin-hosting / vocal-align / mix technique could improve the becky-daw/mix/canvas suite.",
		},
	}
}
