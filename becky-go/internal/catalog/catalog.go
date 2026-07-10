// Package catalog is becky's single, shared knowledge of what becky-tools can do:
// the orchestrator ops + the lower-level becky-*.exe tools, in plain language. It was
// lifted out of cmd/ask (catalog.go) so EVERY front-door — cmd/ask, cmd/harness, and
// cmd/becky-voice — agrees on one source of truth for "what tools exist, how dangerous
// each is, and which pack it belongs to" (SPEC-AGENT-HARNESS.md §4, SPEC-BECKY-VOICE.md
// §3.2 / §4.1). Keep it in sync with cmd/becky's op switch and SKILL.md when ops/tools
// are added.
package catalog

import (
	"sort"
	"strings"
)

// Tier is the GREEN/YELLOW/RED action tier for a tool — the single biggest safety dial
// for an always-on assistant (SPEC-BECKY-VOICE.md §4.1). It is per-tool metadata so it
// cannot be forgotten when a tool is added; an UNKNOWN tool defaults to TierRed.
type Tier string

const (
	// TierGreen: read-only / analytical / trivially reversible. May run proactively.
	TierGreen Tier = "green"
	// TierYellow: in-session edit with undo. Confirm once, then go.
	TierYellow Tier = "yellow"
	// TierRed: destructive / irreversible / outward-facing. Always explicit, never
	// proactive. This is the DEFAULT for any tool whose tier was not set.
	TierRed Tier = "red"
)

// Capability is one thing becky-tools can do, in plain language.
type Capability struct {
	// Verb is the orchestrator op or tool name a human would ultimately run.
	Verb string
	// Summary is a one-line plain-English description (clarity over jargon).
	Summary string
	// Example is a copy-pasteable becky.exe / becky-*.exe invocation.
	Example string
	// Keywords are matched against the user's words for the offline catalog answer.
	Keywords []string
	// Tier is the GREEN/YELLOW/RED action tier; the zero value is treated as RED via
	// the TierOf accessor so an un-set tool is safe by default.
	Tier Tier
	// Pack is which tool-pack(s) this tool belongs to (e.g. "default", "reaper",
	// "forensic"). Empty means it is not assigned to a named pack yet.
	Pack string
}

// TierOf returns the capability's action tier, defaulting an unset/unknown tier to RED
// (fail-safe — SPEC-BECKY-VOICE.md §4.1: "default = RED for unknown").
func (c Capability) TierOf() Tier {
	switch c.Tier {
	case TierGreen, TierYellow, TierRed:
		return c.Tier
	default:
		return TierRed
	}
}

// OrchestratorOps are the plain-language operations the `becky` orchestrator exposes
// (see cmd/becky/main.go). becky-ask drives these for the human — it is the chat
// front-door, becky.exe is the command engine underneath.
var OrchestratorOps = []Capability{
	{
		Verb:     "enroll-wiki",
		Summary:  "Build the known-people knowledge base (voice + face) automatically from the case wiki — no manual clip-making.",
		Example:  `becky enroll-wiki --wiki "<wiki-dir>" --kb kb-final`,
		Keywords: []string{"enroll", "wiki", "knowledge base", "kb", "known people", "build kb", "learn people", "set up"},
		Tier:     TierYellow, Pack: "forensic",
	},
	{
		Verb:     "index",
		Summary:  "Transcribe and embed a folder of videos so the whole corpus becomes searchable.",
		Example:  `becky index "<corpus-dir>" --db forensic.db --kb kb-final`,
		Keywords: []string{"index", "search index", "embed", "make searchable", "ingest folder", "corpus"},
		Tier:     TierYellow, Pack: "forensic",
	},
	{
		Verb:     "profile",
		Summary:  "One-card summary for a person: who they are plus everywhere they appear in the corpus.",
		Example:  `becky profile "John Clancy" --kb kb-final --corpus "<folder>"`,
		Keywords: []string{"profile", "summary", "who is", "card", "person summary", "dossier"},
		Tier:     TierGreen, Pack: "forensic",
	},
	{
		Verb:     "appearances",
		Summary:  "Find which videos a named person appears in, by voice and face, with the clips.",
		Example:  `becky appearances "Shelby" --kb kb-final --corpus "<folder>"`,
		Keywords: []string{"appearances", "appear", "where is", "which videos", "find person", "spot", "recognize"},
		Tier:     TierGreen, Pack: "forensic",
	},
	{
		Verb:     "find",
		Summary:  "Natural-language / keyword search across the transcribed corpus, with timestamps.",
		Example:  `becky find "affair" --db forensic.db`,
		Keywords: []string{"find", "search", "look for", "what was said", "mentions", "transcript search", "keyword"},
		Tier:     TierGreen, Pack: "default",
	},
	{
		Verb:     "corroborate",
		Summary:  "Cross-reference a claim: surface the supporting moments across the corpus for human review.",
		Example:  `becky corroborate "<claim>" --kb kb-final --corpus "<folder>"`,
		Keywords: []string{"corroborate", "cross-reference", "support", "verify claim", "evidence for", "back up"},
		Tier:     TierGreen, Pack: "forensic",
	},
	{
		Verb:     "this is <name>",
		Summary:  "Teach the knowledge base one person from one clip, in plain language.",
		Example:  `becky "this is Shelby" "<clip.mp4>" --kb kb-final`,
		Keywords: []string{"teach", "this is", "that's", "label", "tag person", "add person"},
		Tier:     TierYellow, Pack: "forensic",
	},
}

// ToolCatalog is the lower-level becky-*.exe tools the orchestrator chains. becky-ask
// knows them so it can explain a workflow step-by-step (and, in the spec, assemble one).
var ToolCatalog = []Capability{
	{Verb: "becky-transcribe", Summary: "Turn speech into text with timestamps (srt/txt/vtt/json).", Example: `becky-transcribe "<video>" --format srt`, Keywords: []string{"transcribe", "subtitles", "captions", "what is said", "speech to text", "srt"}, Tier: TierGreen, Pack: "default"},
	{Verb: "becky-diarize", Summary: "Tell how many speakers there are and when each one talks.", Example: `becky-diarize "<video>"`, Keywords: []string{"diarize", "speakers", "who spoke when", "speaker count", "voices"}, Tier: TierGreen, Pack: "default"},
	{Verb: "becky-identify", Summary: "Match KNOWN people in a video by voice and face against the KB.", Example: `becky-identify "<video>" --kb kb-final`, Keywords: []string{"identify", "recognize", "who is in", "match face", "match voice"}, Tier: TierGreen, Pack: "forensic"},
	{Verb: "becky-validate", Summary: "Plain-language description of on-screen actions (forensic, human-reviewed).", Example: `becky-validate "<video>" --backend gemma4-local`, Keywords: []string{"validate", "describe", "what happens", "on-screen", "actions", "physical"}, Tier: TierGreen, Pack: "forensic"},
	{Verb: "becky-events", Summary: "Surface notable moments / events in a video for review.", Example: `becky-events "<video>"`, Keywords: []string{"events", "notable", "moments", "highlights", "timeline"}, Tier: TierGreen, Pack: "default"},
	{Verb: "becky-osint", Summary: "Pull on-screen OSINT signals (text, places, identifiers) from frames.", Example: `becky-osint "<video>"`, Keywords: []string{"osint", "on-screen text", "location", "address", "place", "signs"}, Tier: TierGreen, Pack: "forensic"},
	{Verb: "becky-ocr", Summary: "Read text that appears on screen (signs, documents, captions).", Example: `becky-ocr "<video>"`, Keywords: []string{"ocr", "read text", "on-screen text", "document", "sign"}, Tier: TierGreen, Pack: "default"},
	{Verb: "becky-framematch", Summary: "Find same-location / same-subject frame pairs for a visual comparison exhibit.", Example: `becky-framematch "<frames-dir>"`, Keywords: []string{"framematch", "same place", "same location", "compare frames", "match shots", "exhibit"}, Tier: TierGreen, Pack: "forensic"},
	{Verb: "becky-cut", Summary: "Cut silence / dead air out of a video (auto-editor + VAD pass).", Example: `becky-cut "<video>"`, Keywords: []string{"cut", "trim", "silence", "dead air", "edit"}, Tier: TierYellow, Pack: "default"},
	{Verb: "becky-pipeline", Summary: "Run the full forensic pass (transcribe + diarize + identify + events) over a video or folder; resumable.", Example: `becky-pipeline "<video-or-folder>" --kb kb-final --steps transcribe,diarize,identify,events --out ingest-out`, Keywords: []string{"pipeline", "full pass", "everything", "all steps", "ingest"}, Tier: TierGreen, Pack: "default"},
	{Verb: "becky-search", Summary: "Hybrid keyword + vector search over the embedded corpus.", Example: `becky-search "<query>" --db forensic.db`, Keywords: []string{"search engine", "vector search", "semantic search"}, Tier: TierGreen, Pack: "default"},
	{Verb: "becky-review", Summary: "The LLM step: summarize / reason over collected findings (the only tool that calls an LLM).", Example: `becky-review "<findings.json>"`, Keywords: []string{"review", "summarize findings", "llm", "reason", "analysis"}, Tier: TierGreen, Pack: "default"},
	{Verb: "becky-research", Summary: "Deep-research harness: fan-out search + corroborate + cited synthesis.", Example: `becky-research "<question>"`, Keywords: []string{"research", "deep research", "investigate", "look into"}, Tier: TierGreen, Pack: "default"},
	{Verb: "becky-radar", Summary: "Read Chrome history and surface flagged models/tools vs becky's deps.", Example: `becky-radar`, Keywords: []string{"radar", "browser history", "new models", "watch"}, Tier: TierGreen, Pack: "default"},
	{Verb: "becky-scout", Summary: "Assess a YouTube playlist video-by-video for things that could improve becky.", Example: `becky-scout "<playlist-url>"`, Keywords: []string{"scout", "playlist", "youtube", "assess videos"}, Tier: TierGreen, Pack: "default"},
	{Verb: "becky-web2md", Summary: "Convert a web page to clean markdown (for building the case wiki).", Example: `becky-web2md "<url>"`, Keywords: []string{"web2md", "web to markdown", "scrape page", "save article"}, Tier: TierGreen, Pack: "default"},
	{Verb: "becky-clipcheck", Summary: "Confirm a saved markdown clip actually contains its web page (deterministic; local model only on borderline).", Example: `becky-clipcheck "<file.md>"`, Keywords: []string{"clipcheck", "verify clip", "fidelity", "did it save", "check markdown"}, Tier: TierGreen, Pack: "default"},
	{Verb: "becky-regrab", Summary: "Re-grab a page web2md missed: a local Gemma-4 model extracts the content from the page text, then it's verified.", Example: `becky-regrab "<url>"`, Keywords: []string{"regrab", "re-grab", "recover page", "missed page", "gemma extract", "retry download"}, Tier: TierGreen, Pack: "default"},
	{Verb: "becky-export", Summary: "Export findings / clips into a shareable package.", Example: `becky-export "<results>"`, Keywords: []string{"export", "package", "report out", "share"}, Tier: TierRed, Pack: "default"},
	{Verb: "reaper-bridge", Summary: "Drive the open REAPER session: author/edit takes, params, and the arrangement.", Example: `becky-reaper --apply "<edit.json>"`, Keywords: []string{"reaper", "daw", "take", "mixer", "arrangement", "track"}, Tier: TierYellow, Pack: "reaper"},
	{Verb: "becky-imagegen", Summary: "Generate an image from a text prompt, fully local (Krea-2 via stable-diffusion.cpp).", Example: `becky-imagegen --prompt "a lovely cat" --out cat.png`, Keywords: []string{"imagegen", "image", "generate image", "text to image", "picture", "art", "draw", "krea", "stable diffusion", "flux"}, Tier: TierYellow, Pack: "default"},
	// Added 2026-07-10 (P1 slice C, becky-AI-Agent-review-1.md F7): these three
	// existed on disk since 2026-07-09 but were missing from the catalog, which
	// is exactly the "tribal knowledge, not a tool call" bug the review named.
	{Verb: "becky-vision", Summary: "Describe an image or read on-screen text with a local vision model (escalates automatically on hard reads, corroborates with OCR).", Example: `becky-vision --image "<path>" --prompt "What does this show?"`, Keywords: []string{"vision", "image", "describe image", "screenshot", "photo", "what is this", "look at", "see", "picture of"}, Tier: TierGreen, Pack: "default"},
	{Verb: "becky-perceive", Summary: "Point at a phrase and get pixel bounding boxes for every match in an image (open-vocabulary detection, CPU/0 VRAM).", Example: `becky-perceive "<image>" "the red button"`, Keywords: []string{"perceive", "where is", "locate", "find in image", "bounding box", "point at", "detect"}, Tier: TierGreen, Pack: "default"},
	{Verb: "search_library", Summary: "Search Jordan's bookmarks, GitHub stars, research, and AI chat history in one call.", Example: `search_library "hostinger setup"`, Keywords: []string{"search library", "bookmarks", "stars", "research", "ai chats", "find in library"}, Tier: TierGreen, Pack: "default"},
	// Added 2026-07-10 (AUTOPILOT.md Law 16 / P5 card): the deterministic form of
	// the manual "local due-diligence crawl" every autopilot tick was already
	// doing ad hoc.
	{Verb: "becky-crawl", Summary: "Read-only local-model doc crawler: extracts every constraint/tool/decision/repeated-request from a repo's CLAUDE.md/AGENTS.md/README/docs, cached by doc-content hash.", Example: `becky-crawl --repo "<dir>" --card "<what you're about to build>"`, Keywords: []string{"crawl", "due diligence", "constraints", "read docs", "law 16", "digest"}, Tier: TierGreen, Pack: "default"},
	// Added 2026-07-10 (Manus-gap fix #1, AUTOPILOT.md): the first world-action
	// channel - pure Telegram Bot API, zero browser, zero OAuth consent flow.
	{Verb: "becky-notify", Summary: "Send Jordan a Telegram message via the Bot API - the pure-API world-action channel (no browser, no OAuth).", Example: `becky-notify "message text" [--json]`, Keywords: []string{"notify", "telegram", "message", "alert", "reach jordan", "tell jordan", "send message", "world action"}, Tier: TierRed, Pack: "default"},
	// Added 2026-07-10 (AUTOPILOT.md P5): gives any dumb local model a visual
	// language - draw ascii boxes/arrows, get back a rendered diagram + a
	// Show Me page, no design skill required.
	{Verb: "becky-diagram", Summary: "Render an ascii-art diagram to SVG+PNG and a high-contrast Show Me HTML page, one dumb call.", Example: `becky-diagram --in diagram.txt --title "Downtime Engine" --out data\showme\downtime-engine`, Keywords: []string{"diagram", "ascii art", "svgbob", "visualize", "draw diagram", "flowchart", "show me diagram"}, Tier: TierGreen, Pack: "default"},
	// Added 2026-07-10 (WHORETANA ask #2 / buildplan Phase 3): ported from
	// Mark-XXXIX's actions/web_search.py, real live web results, no browser,
	// no cookies. Read-only/reversible, so TierGreen despite being outward-
	// facing (it queries, it never acts on Jordan's behalf).
	{Verb: "becky-web-search", Summary: "Search the live web via Google Custom Search JSON API - real results, no browser, no cookies, one dumb call.", Example: `becky-web-search "query text" [--max 8] [--json]`, Keywords: []string{"search", "web search", "google", "look up", "find online", "research", "world knowledge"}, Tier: TierGreen, Pack: "default"},
	// Added 2026-07-10 (WHORETANA ask #2 / buildplan Phase 3, slice 2): ported
	// from Mark-XLVII's actions/file_controller.py. Safe local file ops confined
	// to allowed roots (default: home). NO delete, NO bulk auto-organize, NO
	// clobber (Law 8b - DELETE NOTHING OF JORDAN'S). TierYellow: it can create/
	// move/copy inside the sandbox (reversible, confirm-once), never destroys.
	{Verb: "becky-file", Summary: "Safe local file ops confined to allowed roots: list, read, write, mkdir, move, copy, find, info - no delete, never clobbers.", Example: `becky-file list --path desktop  |  becky-file read --path documents --name notes.txt`, Keywords: []string{"file", "files", "list files", "read file", "write file", "save file", "move file", "copy file", "find file", "folder", "desktop", "downloads", "documents"}, Tier: TierYellow, Pack: "default"},
	// Added 2026-07-10 (MANUS-GAP FIX #3, docs/research/manus-gap-analysis.md):
	// durable goal MEMORY that outlives a Claude Code session - the seed of the
	// "durable heartbeat". Backed by data\goals.json (bare array, same shape MC
	// reads kanban.json). Additive only: no delete, update-status changes a
	// status, note appends progress; a corrupt store is refused, never clobbered
	// (Law 8b). TierYellow: it records intent, reversible, never destroys.
	{Verb: "becky-goal", Summary: "Durable goal store that outlives a session: add an outcome, list goals, update-status (todo/active/blocked/done), append progress notes - no delete.", Example: `becky-goal add "restore the childcare email" --due 2026-07-15  |  becky-goal list --status blocked`, Keywords: []string{"goal", "goals", "objective", "outcome", "remember", "todo", "track", "what am i waiting on", "mark done", "progress", "intent"}, Tier: TierYellow, Pack: "default"},
}

// All returns the orchestrator ops and the tool catalog concatenated, ops first.
func All() []Capability {
	out := make([]Capability, 0, len(OrchestratorOps)+len(ToolCatalog))
	out = append(out, OrchestratorOps...)
	out = append(out, ToolCatalog...)
	return out
}

// Lookup returns the catalog entry for a verb (orchestrator op or tool) and whether it
// was found. An unknown verb is the caller's cue to treat it as TierRed.
func Lookup(verb string) (Capability, bool) {
	for _, c := range All() {
		if c.Verb == verb {
			return c, true
		}
	}
	return Capability{}, false
}

// TierOf returns the action tier for any verb, defaulting an unknown verb to TierRed.
func TierOf(verb string) Tier {
	if c, ok := Lookup(verb); ok {
		return c.TierOf()
	}
	return TierRed
}

// InPack returns the catalog entries assigned to the named pack.
func InPack(pack string) []Capability {
	var out []Capability
	for _, c := range All() {
		if c.Pack == pack {
			out = append(out, c)
		}
	}
	return out
}

// MatchCapabilities returns catalog entries whose keywords appear in the question,
// best-effort and case-insensitive. Used by the offline "can becky do X?" answer.
func MatchCapabilities(question string) []Capability {
	q := strings.ToLower(question)
	var hits []Capability
	seen := map[string]bool{}
	for _, group := range [][]Capability{OrchestratorOps, ToolCatalog} {
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

// AllOpsList returns the orchestrator ops, sorted, for the "what can you do?" overview.
func AllOpsList() []Capability {
	out := append([]Capability{}, OrchestratorOps...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Verb < out[j].Verb })
	return out
}
