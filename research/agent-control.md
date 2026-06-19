# Agent control of a music app (drum machine + piano roll) with a LOCAL model

**Research brief — how to build a chat/agent that controls becky's Maschine-class drum
machine + piano roll with FULL context awareness, driven by a local llama.cpp model on
Jordan's GPU (RTX 3070, 8 GB).**

Date: 2026-06-19. Every load-bearing claim is cited with a URL. Where a number or design
choice is "general practice" with no single canonical source, it is labelled as such.

---

## 0. Bottom line (the recommendation, up front)

Build the agent as a **structured-edit tool-call loop**, NOT free-form LLM MIDI generation.
The model reads a **compact snapshot of the current project** plus the user's plain-English
request, and emits a **constrained JSON list of edit actions** against a fixed schema. Those
actions are **previewed** (before -> after diff) and only **applied on the human's OK**
("show me, don't do it"). This is exactly the pattern the working Ableton/Reaper LLM bridges
use, and it matches becky's existing `dawmodel.DrumGrid` / `music.Project` types and the
propose-then-apply overlay already in `internal/canvas`.

Concretely:

- **Model:** Qwen2.5-7B-Instruct (Q4_K_M, ~4.4–4.7 GB) as the default; **Qwen3-4B** or
  **SmolLM3-3B** as the faster/smaller fallback. All have native tool calling and official GGUF.
- **Constraint:** run `llama-server` and pass a **GBNF grammar** (or `json_schema`) so output
  is *syntactically* guaranteed valid; ALSO restate the schema in the prompt (llama.cpp does
  not inject the schema into the prompt). Use `--jinja` + native `tools` only as the alternative.
- **Context:** send the **selected slice in full** (active pattern / selected notes) + a tiny
  **project summary** (tempo, key, bars, track names, kit) — never the whole project's note data.
  Keep stable content first so `cache_prompt` makes repeated turns near-instant.
- **Determinism:** `temperature 0, top_k 1, top_p 1`, single server slot. Grammar gives the
  tightest reproducible structure. (GPU/batch float non-associativity is the residual caveat.)

DAW-Copilot is a cautionary data point: it *tried* small-LLM -> symbolic MIDI (Llama 3.2 1B)
and **removed it**, switching to audio generation + transcription. The lesson is to use the
LLM for *editing intent* (a few structured actions), not to make it the note generator.
(https://github.com/ariknel/DAW-Copilot)

---

## 1. Representing the FULL app state compactly as context

### 1.1 The token budget is the real constraint (it's VRAM, not the model's max context)

- Qwen2.5-7B / Qwen3 ship at **32,768-token native context**, extensible to 131,072 via YaRN
  (which "may impact performance on shorter texts"). https://huggingface.co/Qwen/Qwen2.5-7B-Instruct
- Llama 3.1/3.2/3.3 advertise **128K** context. https://huggingface.co/meta-llama/Llama-3.1-8B-Instruct
- BUT effective context is much smaller than advertised: NVIDIA's RULER benchmark found that of
  models claiming 32K+, "only about half can effectively handle" their claimed length.
  https://github.com/NVIDIA/RULER
- KV-cache memory scales **linearly with context length**; llama.cpp allocates the full `-c`
  context at load. "an 8B model at 32K context requires approximately 4.5 GB for KV cache alone"
  — on an 8 GB 3070, weights (~4.9 GB) + that does not fit, so **context length is bounded by
  VRAM**, not by 128K. Mitigate with KV quant (`--cache-type-k`/`-v q8_0`), which roughly halves it.
  https://medium.com/rigel-computer-com/optimize-your-gpu-kv-cache-for-llama-cpp-opencode-co-13b6bc74f5ec ·
  https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md

**Implication:** budget the state blob at **low-single-digit thousands of tokens**, not tens of
thousands. On 8 GB plan for ~8K–32K usable context with KV-quant, and reserve most of it for the
schema + the response.

### 1.2 Send only the relevant slice — this is faster AND more accurate

- "Context rot": every one of 18 frontier models tested by Chroma got worse as input grew; the
  "lost-in-the-middle" effect drops performance >30% for mid-context content. It is "an
  architectural property of transformer-based attention." https://www.morphllm.com/context-rot
- Pruning beats full context on **both** axes: an agent benchmark found "Pruning context to the
  last 5 tool call/response pairs ... improved both performance and efficiency ... accuracy
  rising to 79.0% and tokens dropping 63.9%." https://arxiv.org/html/2606.10209v1
- Anthropic's context-engineering guidance: find "the smallest possible set of high-signal tokens
  that maximize the likelihood of your desired outcome."
  https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents

**Strategy:** the model gets (a) a **tiny project summary** (tempo, swing, key, bar count, track
names, current kit) and (b) the **selected region in full** (the active 16-step pattern, or the
selected piano-roll notes). The rest of the project is referenced by name/getter, not dumped.
This mirrors becky's existing "give the model only what it needs" / 500GB-funnel philosophy.

### 1.3 Format: hybrid compact JSON + flat tabular rows (avoid YAML)

- **TOON** (Token-Oriented Object Notation) is purpose-built for tabular LLM context: "TOON
  achieves 76.4% accuracy (vs JSON's 75.0%) while using 39.9% fewer tokens"; biggest wins on
  uniform time-series data (−59%). But "For deeply nested or non-uniform structures, JSON-compact
  often uses fewer tokens," and on flat tables CSV beats it. https://github.com/toon-format/toon
- **YAML is NOT cheaper** in the general case: "YAML consistently resulted in higher token counts
  (+6–10%) than JSON ... because both indentation and newlines consume tokens."
  https://zenn.dev/hanako_tech/articles/25d05ba8f124a4?locale=en
- "24% of JSON tokens were structural symbols" (braces/quotes/commas) — pure overhead, worst with
  pretty-printing; minify it. https://scalevise.com/resources/json-vs-toon-yaml-llm-context-efficiency/

**Design:** the project graph (tracks -> clips -> FX) is nested/non-uniform, so keep it as
**minified JSON**. The high-volume uniform parts (drum grids, note lists) go as **flat one-row-
per-item** lines, which is where 30–60% token savings actually land.

### 1.4 Compact musical pattern representations (proven conventions)

- **Drum grids as one char per step**: established step-sequencer convention, "'X' represents a
  hit and '-' represents silence," e.g. `CH X-X-X-XXX-XXX-XX`. This is the most token-efficient
  pattern form (1 char/step, zero structural overhead). https://drum-patterns.com/create/ ·
  https://learn.adafruit.com/16-step-drum-sequencer/code-the-16-step-drum-sequencer
  -> validates becky's existing `"x...x...x...x..."` idea.
- **Notes as `{pitch, start_time, duration, velocity, mute}`** dicts — the exact note schema the
  working AbletonMCP bridge exposes to the model; maps 1:1 to a piano roll / drum grid.
  https://github.com/ahujasid/ableton-mcp
- **REMI** tokens (`Bar`, `Position`, `Pitch`, `Velocity`, `Duration`) are the well-trodden
  text-native note grid if you ever want the model to *emit* sequences directly.
  https://arxiv.org/abs/2002.00212 (Pop Music Transformer) · https://miditok.readthedocs.io/
- **ABC notation** is the most token-efficient *melodic/score* text format: "ABC notation reaches
  288.21 average tokens per song ... around 38% of MIDI-based representations" (~2.6x more compact),
  used by ChatMusician with a plain text tokenizer. https://arxiv.org/abs/2402.16153

### 1.5 Prompt caching keeps the stable state cheap across turns

- llama.cpp `cache_prompt` (default true in llama-server): "Re-use KV cache from a previous
  request if possible. This way the common prefix does not have to be re-processed, only the
  suffix that differs." It can also be persisted: `--slot-save-path` + `/slots/{id}?action=save`,
  "nearly instant vs. re-processing the full prompt."
  https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md ·
  https://github.com/ggml-org/llama.cpp/discussions/13606
- Caveat for becky's determinism rule: the docs warn `cache_prompt` "can cause nondeterministic
  results" depending on backend batch sizes — test with a fixed batch / single slot. (same README)
- Conceptual target (Anthropic prompt caching): "reduces costs by up to 90% and latency by up to
  85% for long prompts." https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching

**Layout rule:** `[system prompt + action schema + kit definition + project summary]` FIRST
(stable, cached), `[selected slice + user request]` LAST (volatile). Only the suffix is
recomputed each turn -> low time-to-first-token after turn 1.

### 1.6 Recommended state-snapshot shape (what to actually send)

```jsonc
// minified in practice; shown expanded for readability
{
  "tempo": 140, "swing": 0.12, "key": "F#min", "bars": 4, "ppq": 96,
  "tracks": [
    {"i":0,"name":"Kick","kit":"BVKER/Trap","pads":16,"mute":false},
    {"i":1,"name":"Snare","mute":false},
    {"i":9,"name":"Lead","type":"piano","program":81}
  ],
  "selection": {                          // ONLY the slice the user is acting on, in full
    "kind": "drum_pattern", "track": 0, "pattern": 1,
    "grid": "x..x..x.x..x..x."            // one char per 16th step
  },
  "buses": [{"name":"Drums","fx":["comp","glue"]}]
}
```

For a piano-roll selection, `selection` becomes
`{"kind":"notes","track":9,"notes":[[60,0.0,0.5,100],[63,0.5,0.5,90]]}` (rows = `[pitch,start,dur,vel]`).

---

## 2. The TOOL / ACTION schema (covering every control)

### 2.1 Why a flat action LIST, not nested function calls

The model emits a JSON object `{ "actions": [ ... ] }` where each action is a small flat object
with an `op` discriminator. This is the structure the production bridges use (a `type` + `params`
JSON object — "Commands are sent as JSON objects with a `type` and optional `params`",
https://github.com/ahujasid/ableton-mcp), and a flat enum-discriminated union is the easiest shape
to lock down with a GBNF grammar and the easiest for a small model to get right.

Two design ideas worth stealing from the Reaper MCP (`shiehn/total-reaper-mcp`):
- **Human-readable references** so the model and Jordan speak the same language: track =
  `"bass"`/`"track 3"`/`"last"`; volume `"-6dB"`/`"+3"`/`"50%"`; time `"8 bars"`/`"selection"`.
- **Tool-count profiles** (15 / ~53 / 600+) to keep a small model's context manageable — expose a
  minimal verb set by default, the full set on demand. https://github.com/shiehn/total-reaper-mcp

Also steal from `xiaolaa2/ableton-copilot-mcp`: **operation history + rollback for note ops**,
because "Direct manipulation of MIDI clips by AI may result in the loss of original notes and
cannot be undone" — which matches becky's forensic non-overwrite rule.
https://github.com/xiaolaa2/ableton-copilot-mcp

### 2.2 The concrete recommended action schema (v1 superset)

Each action is `{"op": <verb>, ...args}`. `target` uses human-readable refs. Grammar/`enum`
locks `op` to this list. Schema is intentionally flat (small models do better with flat + enums).

```jsonc
{
  "summary": "string — one-line plain-English description of the whole change",
  "actions": [

    // ---- DRUM MACHINE / STEP SEQUENCER ----
    {"op": "set_step",      "track": "kick", "pattern": 1, "step": 4,  "on": true, "velocity": 110},
    {"op": "set_pattern",   "track": "snare","pattern": 1, "grid": "....x.......x..."},   // full 16/32-step string
    {"op": "clear_track",   "track": "hat",  "pattern": 1},
    {"op": "transform_beat","track": "kick", "pattern": 1, "kind": "half_time"},          // half_time|double_time|humanize|busier|sparser|swing|shift
    {"op": "add_fill",      "track": "snare","pattern": 1, "into_beat": 4, "kind": "roll"},

    // ---- PADS / SOUNDS / KIT ----
    {"op": "load_kit",      "kit": "BVKER/Trap Soul", "track": null},                     // null = whole machine
    {"op": "set_pad_sample","pad": 3, "sample": "X:/Splice/.../kick_808.wav"},
    {"op": "set_pad_param", "pad": 3, "param": "decay", "value": 0.4},                    // tune|decay|pan|level|choke

    // ---- PIANO ROLL / NOTES ----
    {"op": "add_notes",     "track": "lead", "notes": [[60,0.0,0.5,100],[63,0.5,0.5,90]]},// [pitch,start_beats,dur_beats,vel]
    {"op": "remove_notes",  "track": "lead", "range": [0.0, 2.0]},                        // beats, or "selection"
    {"op": "transpose",     "track": "lead", "range": "selection", "semitones": 12},
    {"op": "quantize",      "track": "lead", "range": "selection", "grid": "1/16", "strength": 1.0},
    {"op": "set_velocity",  "track": "lead", "range": "selection", "value": 96},

    // ---- TEMPO / GROOVE ----
    {"op": "set_tempo", "bpm": 142},
    {"op": "set_swing", "amount": 0.16},                                                  // 0..1
    {"op": "set_key",   "key": "F#min"},

    // ---- MIXER / FX (maps to becky-wire's existing studio graph) ----
    {"op": "set_volume",   "target": "drums", "value": "-6dB"},
    {"op": "set_pan",      "target": "lead",  "value": "L30"},
    {"op": "mute",         "target": "hat",   "on": true},
    {"op": "solo",         "target": "kick",  "on": true},
    {"op": "add_fx",       "target": "drums", "fx": "glue_comp", "after": "eq"},
    {"op": "set_fx_param", "target": "drums", "fx": "glue_comp", "param": "ratio", "value": 4},
    {"op": "sidechain",    "source": "kick",  "dest": "bass", "amount": 0.7},             // becky-wire verb

    // ---- TRANSPORT / ARRANGEMENT ----
    {"op": "transport", "action": "play"},                                               // play|stop|record|loop
    {"op": "set_loop",  "range": [0, 4]},                                                 // bars
    {"op": "duplicate_pattern", "track": "kick", "from": 1, "to": 2},
    {"op": "arrange",   "pattern": 1, "at_bar": 0, "repeats": 4}
  ]
}
```

Notes on the schema:
- `op` is a closed **enum** — the grammar makes it impossible to emit an unknown verb.
- Args are **flat scalars or short arrays** (no deep nesting) — small models stay reliable, and the
  GBNF stays small/fast.
- `target`/`track`/`range` accept the human-readable references; the deterministic Go applier
  resolves them (and rejects ambiguous ones with a degrade message — never crash).
- This is a *superset* for design; v1 ships a subset (see §5).

### 2.3 If you instead use llama.cpp's native tool calling

llama.cpp supports OpenAI-style tools with `--jinja`; each tool is
`{"type":"function","function":{"name","description","parameters":{...JSON Schema...}}}`,
with `tool_choice` of `auto|any|tool`, and the model returns `tool_calls` with
`finish_reason:"tool"`. https://github.com/ggml-org/llama.cpp/blob/master/docs/function-calling.md
The Anthropic equivalent shape is `{"name","description","input_schema":{type,properties,required}}`
with `tool_choice` `auto|any|tool|none`, and `strict: true` to "ensure Claude's tool calls always
match your schema exactly." https://platform.claude.com/docs/en/agents-and-tools/tool-use/overview

**Recommendation for becky:** prefer the **single `propose_edits(actions)` tool + GBNF grammar**
over many native tools. One tool with a grammar-locked action list is more reproducible, uses fewer
tokens, and sidesteps per-model tool-template differences. (Native multi-tool is the fallback if
you adopt a model whose template is very strong at it.)

---

## 3. Constraining a local model to emit valid JSON

### 3.1 GBNF grammars — the core mechanism (token-level constraint)

- GBNF "is a format for defining formal grammars to constrain model outputs in llama.cpp." It
  "works by directly modifying the next-token selection logic, restricting the model to only being
  able to pick from the tokens that fulfill the rules of the grammar at any given point."
  https://github.com/ggml-org/llama.cpp/blob/master/grammars/README.md ·
  https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md
- The `root` rule "always defines the starting point ... what the entire output must match."
  Operators: `*`=`{0,}`, `+`=`{1,}`, `?`=`{0,1}`, plus `{m}`/`{m,}`/`{m,n}`; `|` alternation;
  `()` grouping; `[...]` char classes with `^` negation; `#` comments. Use bounded `x{0,N}` not
  `x? x? x?...` for sampling speed. (grammars/README.md, above)
- A reference `json.gbnf` ships in-repo (bounded `{0,15}`/`{0,20}` repetitions for efficiency).
  https://github.com/ggml-org/llama.cpp/blob/master/grammars/json.gbnf

A custom action-list grammar (sketch) locks the structure tighter than generic JSON:

```gbnf
root        ::= "{" ws "\"summary\":" ws string "," ws "\"actions\":" ws action-array ws "}"
action-array::= "[" ws (action (ws "," ws action)*)? ws "]"
action      ::= set-step | set-pattern | transpose | set-tempo | add-notes  # ... closed set
op          ::= "\"set_step\"" | "\"set_pattern\"" | "\"transpose\"" | "\"set_tempo\""  # enum
# each action rule fixes exactly which keys/types are legal for that op
```

### 3.2 Server params: `grammar`, `json_schema`, `response_format`

All from https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md:
- `grammar`: raw GBNF string ("Set grammar for grammar-based sampling. Default: no grammar").
- `json_schema`: a JSON Schema, auto-converted to a grammar (e.g. `{}` = any JSON).
- `response_format` (OpenAI-compatible on `/v1/chat/completions`): `{"type":"json_object"}`,
  `{"type":"json_object","schema":{...}}`, or `{"type":"json_schema","schema":{...}}`.
- `additionalProperties` defaults to **false** in llama.cpp ("produces faster grammars + reduces
  hallucinations") — opposite of the JSON-Schema spec default. (grammars/README.md)
- json-schema -> grammar limitations: no `uniqueItems`, `contains`, `if/then/else`, or remote `$ref`.

### 3.3 The critical caveat: the schema is NOT injected into the prompt

> "The JSON schema is only used to constrain the model output and is not injected into the prompt."
> "The model has no visibility into the schema, so if you want it to understand the expected
> structure, describe it explicitly in your prompt." (grammars/README.md + server README)

So: grammar guarantees *syntactic* validity, but you must still **describe the action schema in
the system prompt** (verb list + 1-line examples) for the content to be *semantically* right.

### 3.4 Native tool calling needs `--jinja` and a compatible template

- "OpenAI-style function calling is supported with the `--jinja` flag." Native templates:
  Llama 3.1/3.2/3.3, Functionary v3.x, Hermes 2/3, Qwen 2.5 & 2.5-Coder, Mistral Nemo,
  Command R7B, DeepSeek R1. A generic fallback exists but "may consume more tokens and be less
  efficient." Verify at `http://localhost:8080/props`. **Avoid extreme KV quant** (e.g. `q4_0`):
  it "can substantially degrade the model's tool calling performance."
  https://github.com/ggml-org/llama.cpp/blob/master/docs/function-calling.md

### 3.5 Does grammar-constrained decoding hurt accuracy?

Mixed, leans helpful for *format*, with one nuance:
- Functionary uses grammar sampling specifically "to ensure the model output follows the correct
  function schema." https://github.com/MeetKai/functionary
- But over-rigid constraint can hurt *reasoning*: "Let Me Speak Freely?" found "format restrictions
  ... can degrade ... reasoning performance." https://arxiv.org/abs/2408.02442
- **Mitigation:** let the model reason in a short free-text `summary`/think step FIRST, then
  constrain the final `actions` JSON. (Pairs well with Qwen3/SmolLM3 think modes.)

---

## 4. Recommended local model + how to constrain it

### 4.1 Model choice (RTX 3070, 8 GB, "fast background")

| Model | Size | Tool calling | Context | GGUF | Notes |
|---|---|---|---|---|---|
| **Qwen2.5-7B-Instruct** | 7B | Native + Qwen-Agent | 32K (128K YaRN) | official | **Default.** Strong JSON/structured output; Q4_K_M ~4.4–4.7 GB fits 8 GB. |
| **Qwen3-4B** | 4B | Native (think/no-think) | 32K+ | official | Faster fallback; more context headroom on 8 GB. |
| **SmolLM3-3B** | 3B | Native (`xml_tools`/`python_tools`) | 128K | yes | Smallest viable, dual-mode reasoning. |
| Llama-3.1-8B-Instruct | 8B | Native (`<\|python_tag\|>`/JSON) | 128K | yes | Big context; tighter on 8 GB. |
| Hermes-2-Pro / Hermes-3 | 7–8B | Purpose-built `<tool_call>` | 32–128K | official | "90% on our function calling evaluation." |
| Functionary-small-v3.2 | ~8B | Purpose-built + grammar | 128K | official | Built-in grammar sampling; heavier on 8 GB. |
| xLAM-2-8b-fc-r | 8B | Purpose-built (top BFCL) | 128K | yes | Best raw BFCL at 8B if you want max FC accuracy. |
| LFM2-1.2B | 1.2B | Native (`<\|tool_call_start\|>`) | 32K | official | Smallest/fastest; lower complex-task accuracy. |

Cards: https://huggingface.co/Qwen/Qwen2.5-7B-Instruct · https://huggingface.co/Qwen/Qwen3-4B ·
https://huggingface.co/HuggingFaceTB/SmolLM3-3B · https://huggingface.co/meta-llama/Llama-3.1-8B-Instruct ·
https://huggingface.co/NousResearch/Hermes-2-Pro-Mistral-7B · https://github.com/MeetKai/functionary ·
https://huggingface.co/Salesforce/Llama-xLAM-2-8b-fc-r · https://huggingface.co/LiquidAI/LFM2-1.2B
BFCL (the standard FC benchmark): https://gorilla.cs.berkeley.edu/leaderboard.html

> Note: becky already ships a local Qwen3-4B at
> `X:/AI-2/becky-tools/models/Qwen3-4B-Instruct-2507-Q4_K_M.gguf` (CLAUDE.md §6, canvas branch),
> driven via `llama-completion.exe`. **Start there** — it's already wired and on-disk.

### 4.2 How to constrain it (the actual config)

1. Run `llama-server` with the model; pass **`grammar`** (the custom action-list GBNF) OR
   **`json_schema`** on each `/completion` request.
2. **Restate the action schema in the system prompt** (verbs + one example each) — required,
   since llama.cpp does not inject the schema (§3.3).
3. Set `additionalProperties:false` deliberately (it's the llama.cpp default anyway).
4. If you go native-tools instead: `--jinja`, a compatible template (Qwen 2.5 / Hermes / Llama
   3.1), `tools` + `tool_choice`, parse `tool_calls`; avoid extreme KV quant.

### 4.3 Latency / UX for a fast background model

- Measured: **Llama 3.1 8B Q4_K_M on RTX 3070 ≈ 60 tok/s generation, ~713 ms TTFT** (LocalScore).
  A ~100-token action proposal streams in ~1.7 s. https://www.localscore.ai/model/1
- TTFT dominates perceived snappiness; **prompt caching the stable prefix collapses TTFT** on
  every turn after the first (§1.5).
- **Speculative decoding** for ~1.5–3x: `--model-draft` + a 0.5–1B draft model; "180+ tokens per
  second when using a LLaMA 1B draft model alongside an 8B validator."
  https://github.com/ggml-org/llama.cpp/blob/master/docs/speculative.md ·
  https://arxiv.org/abs/2211.17192
- UX pattern (already used by becky-canvas): run the call OFF the UI thread with a "becky is
  thinking…" indicator, stream the `summary` first so the user sees intent immediately, then render
  the preview when `actions` finish.

### 4.4 Determinism / reproducibility (becky's invariant)

- Set **`temperature 0, top_k 1, top_p 1`**, single server slot (`--parallel 1`). At temp 0 the
  seed is irrelevant ("Fixed seeds ... won't help when temperature is zero").
  https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md ·
  https://www.zansara.dev/posts/2026-03-24-temp-0-llm/
- temp 0 is **not** fully sufficient: GPU float non-associativity + dynamic batching can still vary
  the argmax. Multi-slot makes it worse ("between 5 and 8 unique completion texts" with 8 slots ->
  run a single slot). A deterministic CUDA mode is being added (PR #16016).
  https://github.com/ggml-org/llama.cpp/issues/7052 · https://github.com/ggml-org/llama.cpp/pull/16016
- A raw **GBNF grammar gives the tightest reproducible structure**; combined with single-slot temp-0
  it's as deterministic as the platform allows. The downstream Go applier is fully deterministic, so
  even minor model variation degrades gracefully rather than corrupting the project.

---

## 5. Propose -> Preview -> Apply ("show me, don't do it")

### 5.1 The pattern and why it's mandatory here

The agent must **never mutate the project directly**. It returns a *proposal*; the human approves;
only then does the deterministic Go applier change state. This is:
- Anthropic's agent guidance: agents should "pause for human feedback at checkpoints or when
  encountering blockers," with "appropriate guardrails," and "Human review remains crucial."
  https://www.anthropic.com/engineering/building-effective-agents
- The LangGraph human-in-the-loop pattern: `interrupt()` pauses before a tool runs; the human can
  **approve as-is / edit / reject with feedback / respond**, then `Command(resume=...)` continues.
  https://docs.langchain.com/oss/python/langchain/human-in-the-loop
- Already becky's chosen design: the global "show me, don't do it" overlay in `internal/canvas`
  (`Propose`/`Apply`/`RejectScene`, "approval is EXPLICIT — nothing mutates until the human clicks
  ✓"), and the rollback-for-note-ops idea from `xiaolaa2/ableton-copilot-mcp`.

### 5.2 The loop (concrete)

1. **Assemble context** (§1): stable prefix (schema + summary + kit) + volatile suffix (selection +
   request).
2. **Propose**: model returns `{summary, actions[]}`, grammar-constrained (§3).
3. **Validate (deterministic Go)**: resolve refs, range-check (pitch 0–127, vel 0–127, steps in
   range, dB sane), drop/flag illegal actions with a plain-English note — **degrade, never crash**.
4. **Preview**: render a **before -> after diff** — for drums, the old vs new grid string with
   changed cells highlighted; for notes, ghost/added/removed notes on the piano roll; for mixer, the
   old/new value. Show `summary` as the headline. (This is the becky-canvas overlay, colour-accented
   before/after, ✓ Apply / ✗ Reject; Esc=reject / Enter=approve.)
5. **Apply on ✓**: the Go applier mutates an **immutable copy** of the project (append-only,
   sorted, idempotent — same pattern as becky-wire's `Apply`), and **logs the correction** via
   `habits.AppendCorrectionLog` so becky learns Jordan's habitual fixes. On ✗, discard; optionally
   feed the rejection back as feedback for a re-propose.
6. **Undo**: keep the pre-apply snapshot so any applied change is reversible (forensic non-overwrite).

### 5.3 Tool/schema design rules that make this reliable (from Anthropic ACI guidance)

https://www.anthropic.com/engineering/building-effective-agents — "Put yourself in the model's
shoes. Is it obvious how to use this tool?"; give the model "enough tokens to 'think' before it
writes itself into a corner" (the free-text `summary` first); apply "poka-yoke" — design args so
misuse is hard (closed enums, human-readable refs, bounded ranges). Keep tools/verbs few by default
(the Reaper-MCP profiles idea, §2.1).

---

## 6. v1-minimal vs later split

### v1 — minimal, ship first (deterministic core + the loop end-to-end)
- **Verbs (subset):** `set_step`, `set_pattern`, `transform_beat` (half/double/humanize/swing),
  `add_notes`, `remove_notes`, `transpose`, `quantize`, `set_tempo`, `set_swing`,
  `set_volume`/`mute`/`solo`, `transport`. (These map directly onto existing `dawmodel.DrumGrid`,
  becky-drum, and becky-wire.)
- **Model:** the already-on-disk Qwen3-4B via llama-server, **GBNF grammar** for the action list,
  schema restated in the system prompt, `temp 0 / top_k 1`, single slot.
- **Context:** project summary + selected slice only; stable prefix first for `cache_prompt`.
- **Interaction:** the existing canvas propose/preview/apply overlay; deterministic Go validator +
  applier (immutable copy, undo snapshot); correction logged to becky-habits.
- **Degrade:** if the model/binary is absent, fall back to the existing keyword parser
  (becky-wire/becky-drum already do this) so it works with the model off.

### Later — additive, once v1 is proven
- Full verb superset (§2.2): pads/kit loading & sample assignment, per-pad params, FX add/param,
  sidechain, arrangement (`duplicate_pattern`, `arrange`, `set_loop`).
- **Speculative decoding** (draft model) and/or move to Qwen2.5-7B for harder multi-step requests.
- **Tool-count profiles** (minimal / production / full) to keep the small model's context tight.
- **Re-propose on reject** with the rejection as feedback (evaluator-optimizer loop).
- Native llama.cpp `--jinja` tool calling as an alternate backend if a model proves notably better
  with it; xLAM/Functionary if max FC accuracy is needed.
- Optional **generative fill** (a separate model/tool) for "write me a lead here" — kept distinct
  from the edit-loop, per the DAW-Copilot lesson that small-LLM direct MIDI generation was weak.
- ABC/REMI emit path if you later want the model to author longer melodic content as text.

---

## Sources (load-bearing)

llama.cpp: grammars/README.md, json.gbnf, tools/server/README.md, docs/function-calling.md,
docs/speculative.md (all https://github.com/ggml-org/llama.cpp/...); determinism issues #7052,
PR #16016; https://www.zansara.dev/posts/2026-03-24-temp-0-llm/ ·
Models: Qwen2.5-7B / Qwen3-4B / SmolLM3-3B / Llama-3.1-8B / Hermes-2-Pro / Functionary / xLAM /
LFM2 cards (huggingface.co/...); BFCL https://gorilla.cs.berkeley.edu/leaderboard.html ·
DAW agents: https://github.com/ahujasid/ableton-mcp · https://github.com/shiehn/total-reaper-mcp ·
https://github.com/xiaolaa2/ableton-copilot-mcp · https://github.com/ariknel/DAW-Copilot ·
https://github.com/ace-step/ACE-Step · https://miditok.readthedocs.io/ ·
https://arxiv.org/abs/2002.00212 (REMI) · https://arxiv.org/abs/2402.16153 (ChatMusician) ·
https://arxiv.org/abs/2306.00110 (MuseCoco) ·
Context/latency: https://github.com/toon-format/toon · https://www.morphllm.com/context-rot ·
https://arxiv.org/html/2606.10209v1 · https://github.com/NVIDIA/RULER · https://www.localscore.ai/model/1 ·
https://arxiv.org/abs/2211.17192 ·
Agent patterns / tool design / HITL: https://www.anthropic.com/engineering/building-effective-agents ·
https://platform.claude.com/docs/en/agents-and-tools/tool-use/overview ·
https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents ·
https://docs.langchain.com/oss/python/langchain/human-in-the-loop ·
https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching ·
Constrained-decoding nuance: https://arxiv.org/abs/2408.02442
