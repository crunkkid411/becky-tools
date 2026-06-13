// catalog.go — becky-ask's built-in knowledge of what becky-tools can do. This is
// the data the SHELL uses to answer "can becky do X?" deterministically (no LLM) and,
// in the spec, to assemble a workflow. The catalog mirrors the orchestrator's named
// ops (cmd/becky/main.go) and the underlying becky-*.exe tools (SKILL.md). Keep it in
// sync with cmd/becky's op switch and SKILL.md when ops/tools are added.
package main

import (
	"sort"
	"strings"
)

// capability is one thing becky-tools can do, in plain language.
type capability struct {
	// Verb is the orchestrator op or tool name a human would ultimately run.
	Verb string
	// Summary is a one-line plain-English description (clarity over jargon).
	Summary string
	// Example is a copy-pasteable becky.exe / becky-*.exe invocation.
	Example string
	// Keywords are matched against the user's words for the offline catalog answer.
	Keywords []string
}

// orchestratorOps are the plain-language operations the `becky` orchestrator exposes
// (see cmd/becky/main.go). becky-ask drives these for the human — it is the chat
// front-door, becky.exe is the command engine underneath.
var orchestratorOps = []capability{
	{
		Verb:     "enroll-wiki",
		Summary:  "Build the known-people knowledge base (voice + face) automatically from the case wiki — no manual clip-making.",
		Example:  `becky enroll-wiki --wiki "<wiki-dir>" --kb kb-final`,
		Keywords: []string{"enroll", "wiki", "knowledge base", "kb", "known people", "build kb", "learn people", "set up"},
	},
	{
		Verb:     "index",
		Summary:  "Transcribe and embed a folder of videos so the whole corpus becomes searchable.",
		Example:  `becky index "<corpus-dir>" --db forensic.db --kb kb-final`,
		Keywords: []string{"index", "search index", "embed", "make searchable", "ingest folder", "corpus"},
	},
	{
		Verb:     "profile",
		Summary:  "One-card summary for a person: who they are plus everywhere they appear in the corpus.",
		Example:  `becky profile "John Clancy" --kb kb-final --corpus "<folder>"`,
		Keywords: []string{"profile", "summary", "who is", "card", "person summary", "dossier"},
	},
	{
		Verb:     "appearances",
		Summary:  "Find which videos a named person appears in, by voice and face, with the clips.",
		Example:  `becky appearances "Shelby" --kb kb-final --corpus "<folder>"`,
		Keywords: []string{"appearances", "appear", "where is", "which videos", "find person", "spot", "recognize"},
	},
	{
		Verb:     "find",
		Summary:  "Natural-language / keyword search across the transcribed corpus, with timestamps.",
		Example:  `becky find "affair" --db forensic.db`,
		Keywords: []string{"find", "search", "look for", "what was said", "mentions", "transcript search", "keyword"},
	},
	{
		Verb:     "corroborate",
		Summary:  "Cross-reference a claim: surface the supporting moments across the corpus for human review.",
		Example:  `becky corroborate "<claim>" --kb kb-final --corpus "<folder>"`,
		Keywords: []string{"corroborate", "cross-reference", "support", "verify claim", "evidence for", "back up"},
	},
	{
		Verb:     "this is <name>",
		Summary:  "Teach the knowledge base one person from one clip, in plain language.",
		Example:  `becky "this is Shelby" "<clip.mp4>" --kb kb-final`,
		Keywords: []string{"teach", "this is", "that's", "label", "tag person", "add person"},
	},
}

// toolCatalog is the lower-level becky-*.exe tools the orchestrator chains. becky-ask
// knows them so it can explain a workflow step-by-step (and, in the spec, assemble one).
var toolCatalog = []capability{
	{Verb: "becky-transcribe", Summary: "Turn speech into text with timestamps (srt/txt/vtt/json).", Example: `becky-transcribe "<video>" --format srt`, Keywords: []string{"transcribe", "subtitles", "captions", "what is said", "speech to text", "srt"}},
	{Verb: "becky-diarize", Summary: "Tell how many speakers there are and when each one talks.", Example: `becky-diarize "<video>"`, Keywords: []string{"diarize", "speakers", "who spoke when", "speaker count", "voices"}},
	{Verb: "becky-identify", Summary: "Match KNOWN people in a video by voice and face against the KB.", Example: `becky-identify "<video>" --kb kb-final`, Keywords: []string{"identify", "recognize", "who is in", "match face", "match voice"}},
	{Verb: "becky-validate", Summary: "Plain-language description of on-screen actions (forensic, human-reviewed).", Example: `becky-validate "<video>" --backend gemma4-local`, Keywords: []string{"validate", "describe", "what happens", "on-screen", "actions", "physical"}},
	{Verb: "becky-events", Summary: "Surface notable moments / events in a video for review.", Example: `becky-events "<video>"`, Keywords: []string{"events", "notable", "moments", "highlights", "timeline"}},
	{Verb: "becky-osint", Summary: "Pull on-screen OSINT signals (text, places, identifiers) from frames.", Example: `becky-osint "<video>"`, Keywords: []string{"osint", "on-screen text", "location", "address", "place", "signs"}},
	{Verb: "becky-ocr", Summary: "Read text that appears on screen (signs, documents, captions).", Example: `becky-ocr "<video>"`, Keywords: []string{"ocr", "read text", "on-screen text", "document", "sign"}},
	{Verb: "becky-framematch", Summary: "Find same-location / same-subject frame pairs for a visual comparison exhibit.", Example: `becky-framematch "<frames-dir>"`, Keywords: []string{"framematch", "same place", "same location", "compare frames", "match shots", "exhibit"}},
	{Verb: "becky-cut", Summary: "Cut silence / dead air out of a video (auto-editor + VAD pass).", Example: `becky-cut "<video>"`, Keywords: []string{"cut", "trim", "silence", "dead air", "edit"}},
	{Verb: "becky-pipeline", Summary: "Run the full forensic pass (transcribe + diarize + identify + events) over a video or folder; resumable.", Example: `becky-pipeline "<video-or-folder>" --kb kb-final --steps transcribe,diarize,identify,events --out ingest-out`, Keywords: []string{"pipeline", "full pass", "everything", "all steps", "ingest"}},
	{Verb: "becky-search", Summary: "Hybrid keyword + vector search over the embedded corpus.", Example: `becky-search "<query>" --db forensic.db`, Keywords: []string{"search engine", "vector search", "semantic search"}},
	{Verb: "becky-review", Summary: "The LLM step: summarize / reason over collected findings (the only tool that calls an LLM).", Example: `becky-review "<findings.json>"`, Keywords: []string{"review", "summarize findings", "llm", "reason", "analysis"}},
	{Verb: "becky-web2md", Summary: "Convert a web page to clean markdown (for building the case wiki).", Example: `becky-web2md "<url>"`, Keywords: []string{"web2md", "web to markdown", "scrape page", "save article"}},
	{Verb: "becky-export", Summary: "Export findings / clips into a shareable package.", Example: `becky-export "<results>"`, Keywords: []string{"export", "package", "report out", "share"}},
}

// matchCapabilities returns catalog entries whose keywords appear in the question,
// best-effort and case-insensitive. Used by the offline "can becky do X?" answer.
func matchCapabilities(question string) []capability {
	q := strings.ToLower(question)
	var hits []capability
	seen := map[string]bool{}
	for _, group := range [][]capability{orchestratorOps, toolCatalog} {
		for _, c := range group {
			if seen[c.Verb] {
				continue
			}
			for _, kw := range c.Keywords {
				if strings.Contains(q, kw) {
					hits = append(hits, c)
					seen[c.Verb] = true
					break
				}
			}
		}
	}
	return hits
}

// allOpsList returns the orchestrator ops, sorted, for the "what can you do?"
// overview shown on first launch and on the `?`/`help` command.
func allOpsList() []capability {
	out := append([]capability{}, orchestratorOps...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Verb < out[j].Verb })
	return out
}
