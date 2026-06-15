// catalog.go — becky-harness's built-in knowledge of which becky-*.exe tools exist, so
// the default-deny allowlist can REJECT a request that names a tool becky does not have
// (a hard error, no silent drop — SPEC-AGENT-HARNESS.md §2.1/§4.1) and so the generated
// Pi extension carries a plain description per tool.
//
// SPEC §4.1 flags a future refactor to a shared internal/catalog that both cmd/ask and
// cmd/harness import (single source of truth). This cloud build keeps a self-contained
// copy here per the scope rules; the names mirror SKILL.md / cmd/ask/catalog.go and must
// be kept in sync when tools are added. Order is irrelevant (it is a set/map).
//
// Fact-Forcing-Gate self-certification:
//  1. Callers: cmd/harness/main.go calls catalogSet()/catalogDescriptions().
//  2. No-dup: cmd/ask/catalog.go is package main in another cmd dir (not importable);
//     this is the harness's own minimal catalog until the §4.1 shared package lands.
//  3. Data shape: a static map[string]string (tool name -> one-line summary); no files.
//  4. Verbatim instruction: "use subagents in parallel.. build everything".
package main

// beckyTools is the catalog of sharp becky-*.exe tools a harness request may declare.
// Keep in sync with SKILL.md and cmd/ask/catalog.go when tools are added/removed.
var beckyTools = map[string]string{
	"becky-transcribe": "Transcribe an audio/video file to timestamped text (srt/txt/vtt/json).",
	"becky-diarize":    "Report how many speakers there are and when each one talks.",
	"becky-identify":   "Match KNOWN people in a video by voice and face against the KB.",
	"becky-validate":   "Plain-language description of on-screen actions (forensic, human-reviewed).",
	"becky-events":     "Surface notable moments / events in a video for review.",
	"becky-search":     "Natural-language / keyword search across the transcribed corpus.",
	"becky-ocr":        "Read on-screen / document text from frames or images to JSON.",
	"becky-osint":      "Cross-reference a person/handle against prepared OSINT evidence.",
	"becky-cluster":    "Group unlabeled faces/voices into per-person clusters.",
	"becky-enroll":     "Add a known person to the knowledge base from a clip.",
	"becky-vision":     "Right-sized VLM frame analysis (LFM2.5-VL backends).",
	"becky-compose":    "Deterministic genre-aware multi-track MIDI generation.",
}

// catalogSet returns the catalog as a name->true set for allowlist membership checks.
func catalogSet() map[string]bool {
	out := make(map[string]bool, len(beckyTools))
	for name := range beckyTools {
		out[name] = true
	}
	return out
}

// catalogDescriptions returns the name->summary map for generating tool descriptions.
func catalogDescriptions() map[string]string {
	out := make(map[string]string, len(beckyTools))
	for name, desc := range beckyTools {
		out[name] = desc
	}
	return out
}
