package scout

import "strings"

// Interest is one area Jordan personally finds useful, independent of whether it
// maps to a becky tool. Jordan's playlists ("ai useful") collect things he wants
// to come back to — AI agents he could drive, local/open-source tools, AI for
// music/audio/video, document & automation helpers. The becky catalog answers
// "does this improve a becky tool?"; the interests catalog answers the second,
// lower-stakes question Jordan asked for: "even if it's not a becky tool, would I
// personally find this useful?" A single interest hit is enough to SUGGEST a
// video (a suggestion, not a forensic conclusion) — but it's still labelled by
// area so 100 titles become an organized shortlist, not a flood.
type Interest struct {
	Category string   `json:"category"`
	Keywords []string `json:"keywords"`
	Note     string   `json:"note"`
}

// matchInterests returns the interest categories whose keywords appear in the
// lower-cased haystack (deduped, in catalog order).
func matchInterests(hay string, interests []Interest) []Interest {
	var out []Interest
	for _, in := range interests {
		for _, kw := range in.Keywords {
			if strings.Contains(hay, kw) {
				out = append(out, in)
				break
			}
		}
	}
	return out
}

// DefaultInterests is Jordan's built-in personal-usefulness map, tuned to what
// CLAUDE.md says about him: a non-developer music producer who wants AI-friendly
// creative tools, local/open-source AI, and AI agents/assistants he can actually
// drive. Keep keywords lower-case (the haystack is lower-cased).
func DefaultInterests() []Interest {
	return []Interest{
		{
			Category: "AI agents & coding assistants",
			Keywords: []string{"ai agent", "agentic", "coding agent", "claude code", "claude skill", "cursor", "copilot", "mcp", "model context protocol", "autonomous", "agent workflow", "subagent", "n8n", "tool use", "tool calling"},
			Note:     "an agent/assistant or workflow pattern Jordan could drive directly.",
		},
		{
			Category: "local & open-source AI",
			Keywords: []string{"open source", "open-source", "local llm", "self-host", "self host", "runs locally", "on-device", "ollama", "lm studio", "gguf", "free alternative", "alternative to", "no api key", "offline ai"},
			Note:     "a local/open-source tool Jordan can run himself (fits becky's offline-first stance too).",
		},
		{
			Category: "AI for music & audio production",
			Keywords: []string{"music production", "music ai", "ai music", "daw", "plugin", "vst", "mixing", "mastering", "stem separation", "vocal", "suno", "udio", "sample pack", "drum", "synth preset", "beat making"},
			Note:     "an AI music/audio tool for Jordan's production work (the becky-daw/compose/hum world).",
		},
		{
			Category: "AI for video & images",
			Keywords: []string{"video editing", "ai video", "image generation", "stable diffusion", "comfyui", "flux", "upscal", "runway", "image to video", "text to image", "background removal", "ai art"},
			Note:     "an AI image/video tool Jordan might use for visuals/cover art/clips.",
		},
		{
			Category: "documents, notes & knowledge tools",
			Keywords: []string{"markitdown", "document ingestion", "pdf to", "rag", "retrieval augmented", "knowledge base", "note-taking", "notetaking", "obsidian", "second brain", "transcription", "summariz"},
			Note:     "a document/notes/knowledge helper for organizing and querying material.",
		},
		{
			Category: "productivity & automation",
			Keywords: []string{"automate", "automation", "workflow", "productivity", "no-code", "no code", "low-code", "integrate", "zapier", "make.com", "save time", "shortcut"},
			Note:     "an automation/productivity workflow that could save Jordan time.",
		},
		{
			Category: "AI know-how & how-to",
			Keywords: []string{"how to use", "tutorial", "beginner", "getting started", "tips and tricks", "prompt engineering", "prompting", "explained", "crash course", "guide"},
			Note:     "a learn-by-watching explainer Jordan flagged to come back to.",
		},
	}
}
