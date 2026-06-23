# Director / VideoDB mining — what becky-edit should borrow (and not overlook)

> **Research brief, 2026-06-23.** Read the ACTUAL code of `video-db/Director` (the agent
> framework) and `video-db/videodb-cookbook` (recipe notebooks), not from memory. Every
> finding below cites the file it came from. Purpose: becky-edit is a LOCAL/OFFLINE/
> deterministic/forensic NLE (fork Shotcut + a small embedded Gemma that shares live editor
> state and calls deterministic tools). Director solved the *agent-driven-video-editing*
> problem in the cloud; we steal the **concepts**, not the code (different stack; their core
> is Apache-2.0 Python over a proprietary hosted `videodb.io` API + paid GenAI vendors —
> none of which we can or want to use). This doc flags exactly where their cloud model does
> NOT translate.

---

## (a) What they are

**Director** (`video-db/Director`) is "ChatGPT for videos": a Python/Flask backend +
Vue frontend where a **ReasoningEngine** (one orchestrator LLM) interprets a natural-language
request and orchestrates ~25 **agents** (each an LLM tool) to search, clip, edit, caption,
dub, generate, and stream video. The heavy lifting is a hosted **VideoDB** service ("video as
data": upload → index transcript+scenes → semantic search → server-side Timeline render). The
frontend streams partial results live over Socket.IO.

**videodb-cookbook** is ~50 Jupyter notebooks showing real usage of the VideoDB SDK directly
(the layer Director's agents call): `editor/feature/*` (timeline basics, trimming-vs-timing,
clip control layer, caption assets, fit/position/aspect), `editor/creative/*` (montages, lyric
videos, copyright detection), and `examples/*` (intro/outro insertion, audio overlay, beep
curse words, keyword-search counters, scene-index metadata, multicam).

The single most relevant fact for us: **their editor is a declarative timeline model**
(`Timeline → Track → TrackItem(start) → Clip(duration, transition, filter, …) → Asset`) that
renders deterministically. The LLM never touches pixels — it emits a *description* of the
timeline. That maps 1:1 onto MLT XML (Shotcut's engine) and onto becky's existing
`internal/edl` / `dawmodel`. That is the whole ballgame.

---

## (b) The five findings

### 1. Agent / tool architecture & the dispatch loop

**Two-level loop. The outer loop is the gold.** (`backend/director/core/reasoning.py`)

- `ReasoningEngine.run()` runs up to `max_iterations = 10` of `step()`. Each `step()`:
  1. Calls the LLM with the **full reasoning context** + `tools=[agent.to_llm_format() for
     agent in self.agents]` — i.e. **every agent is advertised to the model as one tool**
     (name + description + JSON-Schema parameters).
  2. If the response has `tool_calls`, it appends the assistant message (with tool_calls) to
     context, then **executes each tool call** via `run_agent(name, **arguments)` and appends
     a `role=tool` message carrying `agent_response.__str__()` (status + message + data) and
     the `tool_call_id`.
  3. If `finish_reason in {stop, end_turn}`, it does a **"Final Cut"** summary LLM pass and
     stops.
- **Tools report back as a typed envelope**: `AgentResponse{ status: success|error, message:
  str, data: dict }` (`agents/base.py`). The string form of that envelope is fed straight back
  into the model as the tool result, so the orchestrator sees outcome + payload and decides the
  next step. **Errors are first-class** — a failed agent is recorded in `self.failed_agents` and
  flips the final status to error, but does NOT crash the loop. (This is becky's "degrade, never
  crash" already living in someone else's code.)
- **Tool schema is auto-inferred** from the agent's `run()` signature + docstring
  (`get_parameters()` via `openai_function_calling.FunctionInferrer`), with a manual-JSON
  fallback. Each agent exposes `to_llm_format() → {name, description, parameters}`.
- **The agent description IS the routing logic.** There is no separate router/classifier — the
  model picks the tool purely from rich, example-laden descriptions. The `editing` agent's
  description is a mini-manual (capabilities list + 8 concrete example phrasings + "NOTES").
  The `video_generation` vs `comparison` agents even encode disambiguation rules *in prose* ("do
  not use this agent if the request mentions 'compare'/'vs'… use the comparison agent instead").
- **Nested loops for complex agents.** The `editing` agent is itself a *second* tool-call loop
  (up to 25 iterations) over its own two tools `get_media` and `code_executor`
  (`agents/editing/agent.py`). So Director has an **orchestrator loop → sub-agent loop**
  hierarchy. For becky this is the difference between `ctlagent` (plan/route) and a focused
  inner loop for a hard multi-step op (e.g. "build the montage" trims+orders many clips).

### 2. Tool catalog — what we expose vs. what becky-edit risks overlooking

Director's registered agents (`handler.py`) and the cookbook recipes, grouped:

| Director capability | becky-edit status | Verdict |
|---|---|---|
| **search** (semantic + keyword, over transcript OR scene index) → returns ranked **shots** {video_id, start, end, text, score} | becky-clip `.srt` search exists | Have — but borrow the **shot model + tunable thresholds** (below) |
| **prompt_clip** (transcript → LLM picks relevant sentences → clips) | core becky-clip idea | Have — borrow their **chunking + parallel + exact-substring** discipline |
| **editing** timeline: trim, crop, scale, 9-position layout, fit modes, opacity, **z-index layering**, **multi-track**, transitions, filters | Shotcut gives all of it | Free from host |
| **subtitle** — 8 named caption *templates* + word-level timing + animations (karaoke/reveal/highlight) | not planned | **OVERLOOKING — add captions-as-a-tool to roadmap** |
| **index** (build transcript index AND **scene/visual index**) | transcript only | **OVERLOOKING — scene/visual index is a forensic gap (below)** |
| **frame** — grab a still frame at timestamp ("screenshot this moment") | not planned | **Borrow now — trivial, high forensic value** |
| **comparison** — run N variations **in parallel**, show side-by-side | n/a | Concept worth noting (parallel tool fan-out) |
| **summarize_video** / **transcription** | becky has ASR | Have |
| **stream_video / download** — playback + export URL | Shotcut export | Free from host |
| **censor** ("beep curse words"), **dubbing**, **clone_voice**, **voice_replacement**, **audio_generation**, **image/video_generation**, **web_search**, **slack/composio** (notify/CRM) | n/a | **DO NOT translate** — cloud GenAI/SaaS, online, non-deterministic. Skip. (becky-tts is the one local-voice exception, separate track.) |

**The two we are genuinely overlooking and should add:**
- **A `frame`/still-grab tool** — "screenshot the moment he says X" is forensic bread-and-butter
  and one MLT/ffmpeg call.
- **A scene/visual index** alongside the transcript index — Director searches `scene` (visual
  description) and `multimodal` (transcript ∧ visual). becky-edit today is transcript-only, so
  "find the clip where the red car is in frame" is impossible. becky already ships
  `becky-vision` / framematch — wiring a visual-description index into the search tool is the
  highest-value capability gap. Roadmap, not now.

### 3. Session / state — how the agent reasons over the timeline

- **`Session.state` is a plain dict** (`core/session.py`): `{conn, collection, video}` plus
  per-agent scratch in `session.agent_context`. State is **injected into the model as TEXT, not
  JSON**: `build_context()` writes a one-line digest per asset — `"- title:…, video_id:…,
  media_description:…, length:…, video_stream:…"` — so the model reasons over a **compact catalog
  string**, never raw frames or full transcripts. This is the deliberate "minimal-but-sufficient"
  move and it matches becky's own canon (`research/agent-control.md`: "send the selected slice in
  full + a tiny project summary, never the whole project").
- **Context is persisted and resumable.** `reasoning_context` is a list of `ContextMessage`
  (role/content/tool_calls/tool_call_id) serialized to a DB and reloaded; `to_llm_msg()` renders
  each role correctly (assistant carries tool_calls, tool carries tool_call_id). becky-edit's
  bridge should persist the same so a session survives a window reopen.
- **`edited_context` hook** — a session can be launched with a hand-edited context to *replay or
  steer* the conversation. Cheap, powerful, and a good forensic-reproducibility feature.
- **Clever bit: images are down-converted to text in `format_user_message()`** before hitting the
  model ("User has uploaded image with details: {json}"). They keep the *reference*, drop the
  *bytes*. For a small local Gemma with a tight context window this is exactly right.

### 4. Reasoning / multi-step + streaming

- **Multi-step = iterate the same loop, feed results back.** No separate planner DAG — the model
  re-plans every turn from accumulated tool results (`reasoning_context`). Capped at 10 outer / 25
  inner iterations so it can't spin forever (becky's "max-3-fix circuit breaker" instinct).
- **Streaming of intermediate results is the headline UX.** Two channels on `OutputMessage`:
  - `actions: List[str]` — a running human-readable activity log ("Running @search agent",
    "Generating compilation clip…", "Crafting your video edit…"), pushed live via
    `push_update()` → Socket.IO after *every* step.
  - `content: List[BaseContent]` — typed result cards (`TextContent`, `VideoContent`,
    `SearchResultsContent{shots}`, `ImageContent`) with their own `status` (progress/success/
    error) and `status_message`, so each card shows a spinner then fills in.
  - **`step_reasoning`** — many tool parameters require a short "what this step accomplishes"
    string ("Verifying audio duration", "Combined 3 clips with audio overlay") that is surfaced to
    the user. The model **narrates its own plan as it executes**. Cheap transparency; great fit for
    Jordan's "show me, don't do it."
- **"Final Cut" summarization pass** — after the work, a second LLM call with `SUMMARIZATION_PROMPT`
  produces a plain-language, jargon-free wrap-up of what every agent did. becky can do this 100%
  deterministically (template the action log) — no model needed, and more honest.

### 5. Other genuinely smart concepts

- **LLM-generates-code, sandbox-executes-it (with auto-repair).** The `editing` agent has the LLM
  emit **executable Timeline Python** against a documented SDK class reference embedded in the
  prompt; `code_executor.py` `exec()`s it, requires a `stream_url` variable as the success
  contract, and on failure returns a **typed error** (`SyntaxError`/`NameError`/`ValueError` +
  line number) **back into the loop** so the model fixes its own code. It even has a
  **deterministic fallback**: on audio-transcoding failure it regexes `VideoAsset(...)` → adds
  `volume=0` and retries video-only. *Concept to steal, not the mechanism:* don't let a tiny local
  model freehand-`exec()` arbitrary Python on Jordan's machine (security + determinism disaster).
  Instead emit a **constrained JSON edit-list against becky's fixed `ctledit` schema** (becky
  already does this) and feed typed validation errors back the same way for self-repair.
- **The "no LLM knowledge" guardrail in the system prompt** (`reasoning.py`): *"Do not use
  knowledge from the LLM's training data unless the user explicitly requests it… if the info isn't
  in the video, say 'not available in the current video' and ASK before answering from training
  data."* This is **almost verbatim becky's forensic non-negotiable** (trust the tool output,
  never let the model invent facts/names). Worth porting into becky-edit's Gemma system prompt
  literally — it's a tested phrasing of our own rule.
- **Search returns a ready-made compilation.** The `search` agent doesn't just return shots — it
  calls `search_results.compile()` to render a **stitched preview clip of all matches** in one
  shot (`agents/search.py`). For becky: "find every bounty offer" → one preview reel of all hits,
  not 40 rows to click. Maps onto becky's existing `reel`.
- **Tunable, self-documenting retrieval knobs.** The search tool exposes `result_threshold`,
  `score_threshold`, and a `dynamic_score_percentage` (adaptive: keep top-x% of the score *range*
  when there's a big gap between head and tail results) — each documented in the schema so the
  model can set them. Good pattern for becky's transcript search precision/recall tuning.
- **prompt_clip's transcript discipline** (`agents/prompt_clip.py`): chunk the transcript (~10k),
  run chunks **in parallel** (ThreadPoolExecutor) through the LLM with `response_format=json_object`,
  demand **exact substrings** back (so timestamps stay verifiable), enforce a **min 20-word /
  meaningful-boundary** rule, and support `spoken|visual|multimodal` content types by clubbing
  transcript onto scene windows. Directly applicable to becky-edit's quote-finding.

---

## (c) Borrow NOW for becky-edit (improves the current build)

1. **Model the `ctlagent` loop on `ReasoningEngine.step()`**: advertise every deterministic tool
   to Gemma as a tool-schema; on a tool_call, run it, append a **typed result envelope**
   (`{status, message, data}`) as a `role=tool` message, loop until `stop`/`end_turn`; cap
   iterations (their 10/25 → becky's circuit breaker). This is the proven shape for becky's missing
   multi-step loop.
2. **One typed `ToolResponse{status, message, data}` envelope, stringified back into context** —
   and a failure list that flips final status but never crashes the loop. (= "degrade, never crash"
   for free.)
3. **State-as-compact-text digest, not JSON dumps.** Feed Gemma a one-line-per-clip catalog +
   playhead/selection/active-track summary + the *selected* slice in full — never the whole
   timeline's frames or full transcript. Drop image bytes, keep references (their
   `format_user_message`). Critical for an 8 GB-GPU local model.
4. **Stream a live `actions[]` activity log + per-result `status` cards over the NDJSON seam** to
   the Becky dock, exactly like their Socket.IO `push_update()`. Plus a **`step_reasoning`** field
   on each tool call so Gemma narrates its plan — feeds becky's "show me, don't do it" overlay.
5. **Rich, example-laden tool descriptions as the router.** Write each becky tool's description
   like Director's `editing` description (capabilities + 6–8 concrete example phrasings + explicit
   "use X not Y" disambiguation). For a *small* model this prose routing matters even more.
6. **Self-repair via typed validation errors.** When Gemma's edit-list fails becky's `ctledit`
   schema validation, return the typed error (which field, why) back into the loop so it fixes it
   — but validate-and-apply a **JSON edit-list**, NOT `exec()`'d code.
7. **Port the "no training-data facts" guardrail prompt** into Gemma's system prompt verbatim —
   it's a battle-tested wording of becky's forensic rule.
8. **Add a `frame`/still-grab tool now** ("screenshot the moment X is said") — one ffmpeg call,
   high forensic value, trivially deterministic.
9. **Search returns a stitched preview reel of all hits** (their `compile()` → becky's `reel`), not
   just a list — and expose `score_threshold` / `result_threshold` retrieval knobs in the tool
   schema.
10. **Persist & reload the reasoning context** (resumable sessions) and keep an `edited_context`
    entry point for reproducible replay.

## (d) Roadmap / future ideas (defer)

- **Scene/visual + multimodal index** for search (`spoken|visual|multimodal`) — wire `becky-vision`
  / framematch descriptions into the search tool so "find the clip with the red car" works. Highest-
  value deferred capability.
- **Captions-as-a-tool** with named style templates + word-level karaoke timing (their `subtitle`
  agent's 8 templates) — once basic editing is solid.
- **Nested sub-agent loop** for one genuinely hard op (montage assembly: many trims+orders),
  mirroring their orchestrator→editing two-level loop. Only when the flat loop proves insufficient.
- **`comparison`-style parallel tool fan-out** (render N variants, show side-by-side) — useful for
  "try 3 cut points", low priority.
- **Deterministic "Final Cut" summary** of the action log at session end (template it; no model).

## Where their approach does NOT translate (honest flags)

- **Their whole engine is the hosted VideoDB cloud API** (`videodb.connect()`, server-side index +
  Timeline render, `stream_url`/`player_url` outputs). becky is offline → **MLT/ffmpeg locally** is
  our render, **our own ASR/embeddings** are our index. Take their *timeline data model* and
  *loop*, not their I/O.
- **LLM-writes-and-we-`exec()`-arbitrary-Python** is a security + determinism non-starter locally.
  Replace with constrained-JSON-edit-list against `ctledit` (becky already does this).
- **A large frontier orchestrator LLM** (OpenAI/Anthropic/Gemini via API) is doing the reasoning.
  becky's Gemma is small and local → lean HARDER on: tighter tool descriptions, compact text state,
  JSON-schema/GBNF-constrained output, lower iteration caps, and deterministic post-processing.
- **~15 of their 25 agents are online GenAI/SaaS** (image/video/audio generation, dubbing, voice
  clone, web search, Slack/Composio). All off-limits for a local/offline/forensic tool — ignore.
- **Non-determinism is acceptable to them, fatal to us.** Their search uses LLM relevance + adaptive
  thresholds and their summaries are free-form. becky must keep timestamps as **exact, verifiable
  substrings** and prefer templated/deterministic output wherever a model isn't strictly required.
