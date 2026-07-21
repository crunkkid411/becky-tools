# VideoAgent → becky: the intent→verb mapping

*(The doc `BUILD-INPUTS.md:29` promised and nobody wrote. Written 2026-07-21 from
`HANDOFF-VIDEOAGENT-SEAM.md`, the `BUILD_1.md` §10 decision it records, and the
current `cmd/clip/bridge.go` verb table. This is the reference for how an
AI-planned edit lands on Jordan's timeline instead of rendering around him.)*

## The one-sentence decision

HKUDS/VideoAgent has an intent→workflow brain worth keeping and **no editable
intermediate representation** — it renders finished video directly, so a human
can't adjust anything without re-prompting. becky keeps the brain shape and
**replaces the "render the video" terminal step with "emit becky engine
verbs"**: the engine's verb table (plus `apply_edit_batch`) IS the missing
intermediate representation. Jordan's words (`BUILD-INPUTS.md:13`): *"human
review optional right on the timeline instead of burning it all together as an
.mp4."*

## The constraints (settled — do not re-open)

1. **One seam.** The AI uses the SAME shared-state JSON / engine-verb surface
   the human UI uses (`BUILD-INPUTS.md:18-22`). No MCP server, no separate AI
   tool list, no second protocol.
2. **The engine is never forked.** New capability = a new additive verb in
   `bridge.go`'s default-deny table (`BUILD_1.md:27-28`).
3. **Show-me, don't do it.** A model-planned edit arrives as a *proposal* the
   human approves (`ask` → proposal card → `apply_proposal`) — nothing mutates
   until the ✓. A deterministic Tier-0 command may act directly.
4. **One AI pass = one undo span.** Whatever an approved pass did, one Ctrl+Z
   reverses all of it (`apply_edit_batch`, H-4).
5. **The AI announces itself without blocking.** Long turns narrate via the
   `{"event":{kind,source,text}}` NDJSON push (H-5); Jordan keeps editing.

## Where a VideoAgent workflow's steps map

VideoAgent's pipeline stages → becky's equivalents:

| VideoAgent stage | becky equivalent | Verb(s) |
|---|---|---|
| Ingest / index footage | folder index + transcripts + qmd | `open_folder`, `transcribe`, `transcribe_all`, `reindex` |
| Retrieve moments (semantic search) | hybrid BM25+vector recall | `qmd_search` (in-app), `search` (keyword) |
| Judge / filter moments (LLM) | forensic judge pass | `forensic_query` (runs `becky-judge` + `becky-hits`) |
| Plan the edit (LLM intent → op list) | proposal machinery | `ask` → `apply_proposal` / `reject_proposal` |
| Execute the edit | timeline mutation verbs | `add_clip`, `remove_clip`, `reorder`, `reorder_many`, `set_clips`, `set_trim`, `split`, `set_label`, `add_marker`, `set_overlay` |
| **Render the result (VideoAgent's terminal step)** | **NOT terminal — the edit lands editable** | `load_reel` / the proposal's applied ops; render only when Jordan says so: `export`, `export_selection` |
| Silence/dead-air removal | VAD keep-list → clip list | `autocut_silence` → `set_clips` |
| Captioning | cut-snapped caption pipeline | `becky-subtitle` (external tool; `write_srt` for the reel's SRT) |
| Preview / QA | player + stills | `media_url`, `timeline_edl`, `grab_frame`, `thumb`, `peaks2` |

## The intent → verb playbook

How a plain-English intent should decompose. The planner (Tier-0 rules, local
model, or Claude — `internal/assistant.Router`) emits these as proposal
`actions`; an approved proposal routes through `applyActions` →
`ApplyEditBatch`, so the whole pass is one undo span. **A planner never needs a
new surface for any of these — only verbs from the table.**

- **"Cut out the silence"** → `autocut_silence(name)` → proposal whose action
  is `set_clips(keep-segments)`.
- **"Add every clip where he mentions the reward"** → `qmd_search(query)` →
  proposal with one `add_clip{source,in,out,label}` per accepted hit (hit
  resolution via `lastSearchHits`, already wired for "add clip 3").
- **"Find everywhere he asks people to harass X"** (forensic, needs judgement,
  not just recall) → `forensic_query(query)` — recall + LLM judge + reel build
  + load, one verb, one undo span, Q&A cards included (H-7).
- **"Tighten clip 2 by a second on each side"** → `set_trim(id,in,out)`.
- **"Split this at the playhead"** → playhead comes from shared state (H-1:
  `seek` telemetry, `Timeline.Playhead` in the assistant context) →
  `split(id,at)`.
- **"Delete this clip"** → selection comes from shared state (H-1:
  `set_select` → `Timeline.Selected`) → `remove_clip(id)` per selected id, in
  one batch.
- **"Put the intro last"** → `reorder(id,to)` / `reorder_many(ids,to)`.
- **"Label the porch clips"** → `set_label(id,text)` per matching clip, one batch.
- **"Render it"** → `export(output)` — the ONLY step that burns anything, and
  it is always explicit.

## Shared state: what the planner can see (H-1, built 2026-07-21)

`assistant.Context.Timeline` now carries, live from the UI via the
`seek` / `set_select` / `set_threshold` verbs:

- `Playhead` (compilation seconds) — resolves "here", "at the playhead".
- `Selected` (clip IDs) — resolves "this clip", "these".
- `SkipQuietOn` / `SkipQuietDB` — the skip-quiet toggle and level.

Plus what it always had: the ordered clips (id/source/in/out/label) and the
overlay toggles. The prompt's `TIMELINE:` block prints the playhead and
selection so every tier sees them.

## Status of the seam (audited 2026-07-20, updated 2026-07-21)

| Item | State |
|---|---|
| H-4 `apply_edit_batch` | BUILT + tested, exercised via every approved chat edit |
| H-5 `event` stream | BUILT both sides (Go emit + C++ activity panel) |
| H-6 `ask`/`apply_proposal` seam | BUILT both sides, never forked |
| H-1 shared state | **BUILT (Go, 2026-07-21)** — verbs exist, state feeds prompts; C++ was already firing them |
| H-7 `forensic_query` | **BUILT (Go, 2026-07-21)** — verb runs judge→hits→LoadReel with events + tests; **C++ entry point still needed** (a chat route or button that calls the verb and `loadTimelineView`s the reply — same wiring shape as `apply_proposal`) |

## What NOT to port from VideoAgent

- Its **renderer-terminal architecture** — the entire point of the seam is
  that becky's terminal step is an editable timeline, not an .mp4.
- Its **tool-list-per-agent surface** — becky's contract is one dumb call /
  one verb table (CLAUDE.md §2); giving a planner its own tool list is the
  rejected MCP shape.
- Its **re-prompt-to-fix loop** — in becky the human fixes the 10% on the
  timeline by hand; the AI is not re-run to nudge a cut.
