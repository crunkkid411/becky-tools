# R-AI — the becky-clip assistant brain + cost-tiered model router

> Research + concrete design for the "Underlord"-style AI that lets a detective TALK
> to becky-clip ("find every time he offered money for the cat", "add that to the
> timeline", "label this clip with the date") and have the AI drive the program.
> **Design only** — written so a build agent can implement `internal/assistant`
> directly. Offline-by-default; online is an explicit, logged escalation; nothing
> mutates without the human's approval; originals are never modified.
>
> Authored 2026-06-18 (cloud). Re-verify any model-id / `claude` flag / Anthropic
> endpoint past this date via the in-repo `claude-api` skill before wiring the
> frontier tier (the house rule from SPEC-BECKY-ASK §0).

---

## 0. The one-paragraph shape

A detective types a sentence. A **deterministic front gate** tries to satisfy it
with zero model tokens (keyword/regex command parse + literal/`becky-search`
retrieval). If that is not enough, a **small local model** (the existing
`cmd/ask` llama-server transport) parses the fuzzy request into a strict **action
schema** and makes cheap yes/no judgments. Only the genuinely hard semantic work —
"find every time across hours of transcript where he offered money for the cat",
multi-step planning — **escalates** to a **frontier** backend (Jordan's `claude`
CLI on his Max plan, or the Anthropic API). The model never touches 500GB: a
**retrieval funnel** turns the folder into a few candidate cue snippets, and only
those reach the model. Whatever tier answers, the AI returns a **proposed action
list + a human-readable preview**; the GUI shows it ("show me, don't do it") and
**nothing happens until the detective approves**.

This is the becky philosophy applied to an assistant: *anything a single
deterministic tool CAN do, it does (Tier 0); a small fast model makes the cheap
decisions (Tier 1); the Max plan is spent only on what truly needs a frontier
model (Tier 2).*

---

## 1. The cost-tiered router (the heart)

### 1.1 Tier table

| Tier | Engine | Cost | Latency | Offline? | Handles |
|---|---|---|---|---|---|
| **0 — DETERMINISTIC** | Go code: keyword/regex command parse + `becky-search`/literal SRT grep | free | <5 ms | yes | Explicit GUI commands ("add clip 3", "export", "jump to 12:40", "remove the last clip", "label this 06/14"); literal retrieval ("find the word 'cat'"); any utterance that maps 1:1 to one action verb. **Target: most turns land here.** |
| **1 — SMALL LOCAL** | `llama-server` + Qwen3-4B (the `cmd/ask/llama.go` transport, `--temp 0 --seed 42`) | free (local GPU) | ~1–12 s (incl. cold server spawn; reuse warm) | yes | Fuzzy NL → action schema ("chuck the bit where he's mad about the cat onto the end" → `add_clip`); cheap per-item yes/no (the `becky-quotes` per-neighbor "does this sentence add context?" loop); short rephrase/labels. Default for "needs a little intelligence." |
| **2 — FRONTIER** | (a) `claude` CLI headless (Jordan's OAuth/Max plan) **or** (b) Anthropic API (`ANTHROPIC_API_KEY`) | **Max-plan tokens / $** | 3–20 s + network | **NO — explicit, logged escalation** | Hard semantic retrieval over many candidates ("every time he offered money for the cat" = paraphrase/coref/implicature across hours); multi-step planning ("find those, add them all to the timeline in order, and date-label each"); selection feeding `becky-quotes --select-from-json`. |

Tier 2 mid-vs-deep sub-routing: a **mid** model alias (`haiku`,
`claude-haiku-4-5`-class) for bulk/cheap frontier calls (map-reduce candidate
judging across many windows), a **deep** alias (`opus`, `claude-opus-4-8`) reserved
for the final synthesis/plan. Spend the expensive model once, not per window.

### 1.2 The decision function

Pure, deterministic, no model call to *decide the tier* (deciding the tier must
never itself cost a token). Signature:

```go
// Tier is the routing decision. The router tries them low→high and falls back
// high→low on failure (see §1.3).
type Tier int
const (
    TierDeterministic Tier = iota // 0
    TierLocal                     // 1
    TierFrontier                  // 2
)

// Decision is what classify() returns: the tier to TRY first, the parsed
// deterministic action (if Tier 0 already satisfied it), and why.
type Decision struct {
    Tier     Tier
    Actions  []Action // non-nil iff Tier 0 fully parsed it (skip the model)
    Reason   string   // human-readable ("matched command grammar: add_clip")
    Escalate bool     // true iff this is a semantic-retrieval/plan turn (Tier 2)
}

// classifyTier decides the STARTING tier from the utterance + context, with NO
// model call. Order matters: cheapest match wins.
func classifyTier(utt string, cx Context) Decision {
    u := strings.ToLower(strings.TrimSpace(utt))

    // (a) TIER 0 — explicit command grammar. Regex/keyword table of the verbs in
    // §2. If the whole utterance parses to a complete action, return it now.
    if acts, ok := parseCommandGrammar(u, cx); ok {
        return Decision{Tier: TierDeterministic, Actions: acts,
            Reason: "matched command grammar"}
    }

    // (b) TIER 0 — literal retrieval. "find/search/show me <literal>" with no
    // semantic verb ("every time", "whenever", "mentions", "offers", "implies").
    if q, ok := parseLiteralSearch(u); ok && !hasSemanticCue(u) {
        return Decision{Tier: TierDeterministic,
            Actions: []Action{{Verb: "search", Args: Args{"query": q, "mode": "keyword"}}},
            Reason: "literal retrieval"}
    }

    // (c) TIER 2 — semantic-retrieval / multi-step. Cue words that a 4B model
    // selects poorly over hours of transcript, OR a conjoined plan.
    //   semantic: "every time", "whenever", "any point where", "offers/implies/
    //   suggests/about", "mentions", paraphrase asks.
    //   multi-step: contains "and then", or ≥2 distinct action verbs, or
    //   "find ... add ... label ..." chains.
    if hasSemanticCue(u) || isMultiStep(u) {
        return Decision{Tier: TierFrontier, Escalate: true,
            Reason: "semantic retrieval / multi-step plan"}
    }

    // (d) TIER 1 — everything else that still needs intelligence: a fuzzy
    // single-action request the grammar missed ("chuck that angry bit on the end").
    return Decision{Tier: TierLocal, Reason: "fuzzy single-action NL"}
}
```

`hasSemanticCue` / `isMultiStep` are small word-list + verb-count predicates.
They are deliberately *conservative*: when unsure between Tier 1 and Tier 2, start
at **Tier 1** (cheap) and let the *fallback chain* escalate only if Tier 1 can't
produce a confident result — this is what keeps the Max plan from being burned on
turns a local model could have handled.

**Budget discipline (the load-bearing rule):** Tier 2 is gated three ways before a
single token is spent — (1) `classifyTier` chose it, (2) `assistant.online` is
enabled (default **off**; a visible toggle), and (3) the per-session token/$ budget
isn't exhausted. If online is off, a Tier-2 turn **degrades** to Tier 1 with an
honest note ("answered locally — turn on the frontier model for deeper search").

### 1.3 Fallback / degrade chain

Two directions, and they're different:

**Escalate (low→high) — capability fallback.** Try the cheap tier; if it returns
*low confidence or unparseable*, step up:

```
Tier 0 parse fails ─▶ Tier 1 (local model parses to actions)
Tier 1 low-confidence / can't satisfy a semantic ask ─▶ Tier 2 (if online+budget)
Tier 2 unavailable/over-budget ─▶ stay at Tier 1 result + honest "answered locally" note
```

**Degrade (high→low) — availability fallback.** The chosen tier's *engine is
down*; fall to the next thing that still works, never crash (CLAUDE.md "degrade,
never crash"):

```
Frontier(claude CLI) errors/timeout ─▶ Frontier(API) if key set
Frontier(API) errors/no key        ─▶ Tier 1 (local model), note "used local model"
Tier 1 (no llama-server / no GGUF)  ─▶ Tier 0 deterministic retrieval + plain note
Tier 0                              ─▶ always available (it's just code)
```

The terminal floor is always Tier 0: literal `becky-search`/grep + the command
grammar. The detective always gets *something* real, even with every model off.

---

## 2. The action / command schema (the control surface)

The AI's only way to drive the GUI is to emit a list of **actions** from a
**fixed, default-deny allowlist** — the same posture as `SPEC-BECKY-CANVAS §4`
("model emits executable text" dispatches ONLY to an allowlisted set; no free-form
code). Each action is one JSON object `{verb, args}`; the Go backend has one
deterministic handler per verb. An unknown verb is rejected, never executed.

### 2.1 The verb list

| Verb | Args | Handler effect (deterministic) | Mutates? |
|---|---|---|---|
| `search` | `query`, `mode?`(hybrid\|vector\|keyword), `limit?` | runs `becky-search` → hits panel | no (read) |
| `find_quotes` | `criteria`, `srt?`, `select_from_json?` | runs `becky-quotes` (or feeds frontier selection via `--select-from-json`) → candidate regions | no (read) |
| `preview_clip` | `source`, `in`, `out` | scrub/preview a region in the player; no write | no |
| `grab_frame` | `source`, `at` | extract one still to the work dir (copy, never edits source) | no (new file) |
| `add_clip` | `source`, `in`, `out`, `label?`, `at?`(index) | append/insert a clip into the timeline model | **yes** |
| `remove_clip` | `id`\|`index` | drop a clip from the timeline | **yes** |
| `reorder` | `id`\|`index`, `to` | move a clip | **yes** |
| `set_overlay` | `id`\|`index`, `field`, `value` | set a clip's overlay text/field (e.g. date label) | **yes** |
| `set_marker` | `at`, `label?` | drop a timeline marker | **yes** |
| `set_label` | `id`\|`index`, `text` | rename/label a clip | **yes** |
| `export` | `preset?`, `out?`, `range?` | render the compilation (writes a NEW file) | no (new file) |

Notes:
- `source` is a folder-relative path or a video id from the index — **never** an
  absolute outside-folder path (validated against the case folder root).
- `in`/`out`/`at` are timestamps copied **verbatim** from transcript cue boundaries
  or the index — never invented (the `becky-quotes` hard-rule-#4 discipline).
- Read-verbs (`search`, `find_quotes`, `preview_clip`, `grab_frame`) can run
  without approval if the GUI wants instant feedback; **all mutate-verbs require the
  approval envelope** (§2.3).

### 2.2 Inline DSL form (the wire format the model emits)

To cut the per-action token tax (CANVAS §4.1: "action = code-in-the-message"), the
model emits one action per line in a tiny DSL, which the Go side parses to the
JSON above. Strict, line-oriented, easy to validate:

```
add_clip source="2026-06-14_ring.mp4" in=00:13:12,640 out=00:13:20,560 label="offers money for cat"
set_overlay index=1 field=date value="2026-06-14"
```

Equivalent JSON (what handlers receive):

```json
[
  {"verb":"add_clip","args":{"source":"2026-06-14_ring.mp4","in":"00:13:12,640","out":"00:13:20,560","label":"offers money for cat"}},
  {"verb":"set_overlay","args":{"index":1,"field":"date","value":"2026-06-14"}}
]
```

The DSL is parse-validated before anything is shown: unknown verb → drop with a
note; missing required arg → the action is marked "needs a value" in the preview,
not executed. JSON is the canonical internal form; the DSL is just the cheap
transport. (Tier 2 via the `claude` CLI can alternatively be asked for strict JSON
directly with `--json-schema` — see §4.4 — skipping the DSL parse entirely.)

### 2.3 The propose-then-apply envelope ("show me, don't do it")

Every turn that would mutate returns a **Proposal**, never a side effect:

```go
type Proposal struct {
    Summary  string      // one human sentence: "Add 1 clip and date-label it."
    Actions  []Action    // the validated, allowlisted action list
    Preview  []DiffLine  // before→after, per action, for the overlay
    Tier     Tier        // which tier produced it (shown as a small badge)
    Sources  []SourceRef // cue/file provenance for any retrieval that fed it
    Cost     CostNote    // tokens/$ if Tier 2 was used (for the budget meter)
}
```

Flow:

1. Router produces a `Proposal`.
2. GUI renders `Summary` + `Preview` (colour-accented before→after, exactly the
   canvas overlay already built in `internal/canvas`) with **✓ Apply / ✗ Reject**.
3. **Nothing mutates** until the human clicks ✓ (or presses Enter; Esc = reject —
   the key bindings the canvas overlay already uses).
4. On ✓, the backend runs each handler **in order**, each producing an undoable
   timeline edit; on ✗, the proposal is discarded.
5. Approved proposals append a correction record via the existing
   `habits.AppendCorrectionLog` so becky learns the detective's habits (a rejected/
   edited proposal teaches it too).

Read-only proposals (a `search` with no mutate) may auto-apply to the results panel
for snappiness, but anything touching the timeline goes through ✓/✗.

---

## 3. The 500GB context strategy (the retrieval funnel)

**Hard invariant: the model never ingests the folder.** Not 500GB, not 5GB. The
model sees, per turn, only: a fixed system prompt + the action catalog + the
current timeline state + **a handful of retrieved candidate snippets**. Everything
else is the deterministic funnel's job.

### 3.1 What's on disk (built once, cheaply, read-only)

- **Folder index** — a walk of the case folder: every video's filename, duration,
  and the path to its transcript sidecar (`.srt`/`.en.srt`) and any becky sidecars
  (`*.diarize.json`, `*.identify.json`). This is filenames + small JSON, megabytes,
  not the media. (Reuse `internal/sidecar` discovery; never opens the video bytes.)
- **Semantic DB** (optional but ideal) — the existing `forensic.db` that
  `becky-embed` builds: transcript segments embedded (Qwen3-Embedding, 1024-dim) for
  `becky-search` KNN. If present, semantic retrieval is a deterministic vector
  search, no model.

### 3.2 The funnel (per turn)

```
user utterance
   │
   ▼  (Tier 0, deterministic — NO model)
[1] CANDIDATE RETRIEVAL
    • keyword/literal grep over the transcript sidecars, AND
    • becky-search (hybrid: vector + FTS) over forensic.db
    → a ranked list of CUE SNIPPETS {source_file, timestamp, text, score}
      (cap: top-K, e.g. 40–200, NOT the whole transcript)
   │
   ▼  (only if the ask is semantic / Tier 2)
[2] MAP — judge candidates in WINDOWS
    • slice candidates into token-bounded windows (map-reduce, like
      becky-quotes §4.5: ~8–12 min of transcript / a few hundred cues per window,
      1–2 cue overlap)
    • for each window: ONE model call (mid-tier frontier or local) → "which of
      these cue ranges match the ask? (verbatim cue indices + one-line reason)"
   │
   ▼
[3] REDUCE — merge per-window hits
    • dedup by cue range, sort chronologically, keep the corroborated ones
      (FORENSIC-OUTPUT-PHILOSOPHY: conclude when ≥ signals agree; otherwise mark
      candidate)
   │
   ▼
[4] PLAN — ONE final model call (deep-tier) over the SMALL reduced set
    • turns the surviving cue ranges into a Proposal (add_clip × N, ordered,
      labelled) — never re-reads the transcript; operates on the reduced hits only
   │
   ▼
Proposal → propose-then-apply envelope (§2.3)
```

### 3.3 The per-turn context budget (what actually goes to the model)

Fixed, small, and the same every turn (so a warm llama-server KV cache / a frontier
prompt cache can reuse it):

| Block | Contents | ~Budget |
|---|---|---|
| System prompt | role, the forensic-output rules, "emit only allowlisted actions", "timestamps verbatim" | ~600 tok |
| Action catalog | the §2 verb list + DSL grammar | ~400 tok |
| Timeline state | current clips (source/in/out/label/index) — a compact list, not media | ~50 tok/clip |
| Retrieved candidates | ONLY this window's cue snippets from the funnel | window-bounded |
| User utterance | the sentence | tiny |

**Token discipline:** the candidates block is the *only* variable part and it is
**window-bounded by construction** — the funnel never hands the model more than one
window of cues. A 6-hour, 25k-word transcript is judged in ~10–15 windowed calls of
a few hundred cues each, then planned in one. The model's context never scales with
folder size; it scales with *how many candidates survived deterministic retrieval*,
which is bounded by top-K.

### 3.4 Why this honors the constraints

- **Never modifies originals:** the funnel opens transcripts/JSON/DB read-only and
  the index never touches video bytes; `grab_frame`/`export` write to the work dir.
- **Deterministic-first:** steps [1] and [3] are pure code; the model is used only
  where judgment is unavoidable ([2] semantic match, [4] plan), and even [2] is
  skipped entirely for literal asks (Tier 0 answers directly).
- **Context-aware of timeline + files + the directed folder:** the timeline state
  and folder index are in every prompt; "the folder it's directed at" is the funnel's
  search scope.

---

## 4. `internal/assistant` — Go integration sketch (cgo-free)

A new package beside the others. The GUI (WebView2 backend per the becky-clip
spikes, or Gio) calls `Assistant.Handle(utt)` and streams the resulting `Proposal`
to the page. All cgo-free: the local model is reached over HTTP (the existing
llama-server pattern), the frontier over `exec.Command`/`net/http`.

### 4.1 Interfaces (small, per Go house style)

```go
package assistant

// Router decides the tier and produces a Proposal. The ONE entry point the GUI calls.
type Router interface {
    Handle(ctx context.Context, utt string, cx Context) (Proposal, error)
}

// Backend is one model tier's engine. Tier 0 has no Backend (it's pure Go).
type Backend interface {
    Name() string                 // "local" | "claude-cli" | "anthropic-api"
    Available() error             // nil if usable now (binary/key/server present)
    Complete(ctx context.Context, req Request) (string, error) // system+user → text
}

// Context is the per-turn state the funnel + prompts need (all cheap to assemble).
type Context struct {
    FolderRoot string        // the case folder (search scope; originals read-only)
    Index      *FolderIndex  // filenames + sidecar paths (no media bytes)
    DB         string        // forensic.db path ("" if not built)
    Timeline   TimelineState // current clips (compact)
    Online     bool          // is the frontier tier enabled? (default false)
    Budget     *Budget       // remaining tokens/$ this session
}

type Request struct {
    System string
    User   string
    JSONSchema string // optional: ask the backend for strict JSON (claude CLI / API)
    MaxTokens  int
    Tier       Tier   // lets a backend pick mid vs deep model alias
}
```

### 4.2 The router wiring (low→high with degrade)

```go
type router struct {
    local    Backend // llama-server (cmd/ask transport, reused)
    claudeCLI Backend // claude -p
    api      Backend  // Anthropic /v1/messages
    funnel   *Funnel  // deterministic retrieval (becky-search + grep)
    log      func(string, ...any) // logs every online escalation (audit)
}

func (r *router) Handle(ctx context.Context, utt string, cx Context) (Proposal, error) {
    d := classifyTier(utt, cx) // §1.2 — NO model call

    switch d.Tier {
    case TierDeterministic:
        return r.deterministic(ctx, d, cx) // run parsed actions / becky-search; build Proposal

    case TierLocal:
        if err := r.local.Available(); err != nil {
            return r.deterministic(ctx, fallbackToRetrieval(d), cx) // degrade
        }
        return r.viaModel(ctx, r.local, d, cx)

    case TierFrontier:
        if !cx.Online || cx.Budget.Exhausted() {
            return r.viaModel(ctx, r.local, downgrade(d), cx) // budget/offline → local
        }
        be := r.frontier()                // claude CLI preferred, API fallback
        if be == nil {
            return r.viaModel(ctx, r.local, downgrade(d), cx)
        }
        r.log("ONLINE escalation: %s via %s", utt, be.Name()) // explicit + logged
        return r.frontierFunnel(ctx, be, d, cx) // §3.2 map-reduce-plan
    }
    return Proposal{}, fmt.Errorf("unreachable")
}

// frontier picks the first available frontier backend (CLI on the Max plan first,
// API as the degrade), or nil if neither is usable.
func (r *router) frontier() Backend {
    if r.claudeCLI != nil && r.claudeCLI.Available() == nil { return r.claudeCLI }
    if r.api != nil && r.api.Available() == nil { return r.api }
    return nil
}
```

### 4.3 The local backend — REUSE `cmd/ask`

The local backend is a thin adapter over the **already-built** llama-server
transport (`cmd/ask/llama.go`): spawn a transient `llama-server` on a free port →
wait `/health` → POST OpenAI-style `/v1/chat/completions` → read
`message.content`, with `--temp 0 --seed 42` and `enable_thinking=false`. Lift that
code into `internal/llmlocal` (so both `cmd/ask` and the assistant share it) and
wrap it:

```go
type localBackend struct{ c *llmlocal.Client } // the lifted llamaClient
func (b *localBackend) Name() string      { return "local" }
func (b *localBackend) Available() error  { return b.c.Ready() } // GGUF + server exist
func (b *localBackend) Complete(ctx context.Context, req Request) (string, error) {
    return b.c.Chat(ctx, req.System, req.User) // existing chat(); reuse warm server
}
```

(Keep one warm server for the session instead of re-spawning per turn — a small
addition to the lifted client; the `cmd/ask` version spawns per call.)

### 4.4 The `claude` CLI frontier backend — VERIFIED flags

`claude --help` (run 2026-06-18 on Jordan's box) confirms the exact non-interactive
invocation. Use **print mode** with **JSON output** and an **appended system
prompt**, feeding the user content on **stdin** so a long candidate block isn't an
argv length problem:

```go
type claudeCLIBackend struct {
    bin   string // "claude" (resolved on PATH; the npm shim → claude.exe)
    model string // alias: "opus" | "haiku" (durable) — see §4.6
}

func (b *claudeCLIBackend) Available() error { _, err := exec.LookPath(b.bin); return err }

func (b *claudeCLIBackend) Complete(ctx context.Context, req Request) (string, error) {
    args := []string{
        "-p",                                   // print mode, non-interactive, exit
        "--output-format", "json",              // single JSON result envelope
        "--model", b.model,                     // opus (deep) / haiku (mid)
        "--append-system-prompt", req.System,   // becky forensic + action rules
        "--max-turns", "1",                     // one shot, no agentic loop
        // optional structured output:
        // "--json-schema", req.JSONSchema,      // validate strict action JSON
        // optional spend cap (only with -p):
        // "--max-budget-usd", "0.50",
    }
    cmd := exec.CommandContext(ctx, b.bin, args...)
    cmd.Stdin = strings.NewReader(req.User)     // the candidate block + ask via stdin
    cmd.Env = nonInteractiveEnv()               // see note below
    out, err := cmd.Output()
    if err != nil { return "", fmt.Errorf("claude -p: %w", err) }
    // --output-format json wraps the reply: {"type":"result","result":"<text>", ...}
    var env struct{ Result string `json:"result"` }
    if jsonErr := json.Unmarshal(out, &env); jsonErr == nil && env.Result != "" {
        return env.Result, nil
    }
    return string(out), nil // plain-text fallback if the envelope shape shifts
}
```

Verified, load-bearing flags (from `claude --help`, 2026-06-18):
- `-p, --print` — **the** non-interactive switch (prints response and exits; trust
  dialog auto-skipped when piped). Without it `claude` opens an interactive TUI — fatal in a backend.
- `--output-format json` — single JSON result envelope (`{type,result,...}`);
  `stream-json` exists if streaming is wanted later.
- `--model <alias|id>` — `opus`/`sonnet`/`haiku`/`fable` aliases (always point at the
  latest), or a full id.
- `--append-system-prompt <text>` — append becky's rules to the default system
  prompt; `--system-prompt <text>` **replaces** it (use append so the CLI's own
  tool-use scaffolding stays sane). `--system-prompt-file` is **not** in this build's
  help — pass the text, or use `--settings`.
- `--json-schema <schema>` — structured-output validation; lets the CLI return the
  action list as schema-checked JSON directly (skip the DSL parse).
- `--max-budget-usd <amount>` and `--max-turns <n>` — hard spend/loop caps (print
  mode only) — belt-and-suspenders on the budget meter.
- stdin — the prompt can be the positional arg OR piped on stdin; pipe the (possibly
  large) candidate block to dodge argv limits.

Env note: this very build runs *inside* a Claude Code session
(`CLAUDECODE=1`, `CLAUDE_CODE_ENTRYPOINT` set). When shelling out to `claude -p`
from a normal user run of becky-clip those won't be set and it behaves as a fresh
print-mode call on Jordan's Max-plan OAuth. `nonInteractiveEnv()` should ensure no
inherited `CLAUDECODE`/session vars confuse the child; rely on the user's logged-in
OAuth (no key needed for the CLI path). **Auth wall:** if the CLI ever returns a
login/usage-limit error, degrade to the API or local tier and surface the plain
reason — never try to authenticate from inside becky-clip.

### 4.5 The Anthropic API frontier backend — raw `/v1/messages`

Cgo-free `net/http`. Used when the CLI path is unavailable or a key is preferred
(e.g. unattended batch). Stable endpoint + headers:

```go
type apiBackend struct {
    key   string // os.Getenv("ANTHROPIC_API_KEY")
    model string // see §4.6
    http  *http.Client
}
func (b *apiBackend) Available() error {
    if b.key == "" { return fmt.Errorf("ANTHROPIC_API_KEY not set") }
    return nil
}
func (b *apiBackend) Complete(ctx context.Context, req Request) (string, error) {
    body, _ := json.Marshal(map[string]any{
        "model":      b.model,
        "max_tokens": orDefault(req.MaxTokens, 1024),
        "system":     req.System,                          // top-level system field
        "messages":   []map[string]string{{"role": "user", "content": req.User}},
    })
    httpReq, _ := http.NewRequestWithContext(ctx, "POST",
        "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
    httpReq.Header.Set("x-api-key", b.key)
    httpReq.Header.Set("anthropic-version", "2023-06-01") // stable; re-verify via claude-api
    httpReq.Header.Set("content-type", "application/json")
    resp, err := b.http.Do(httpReq)
    if err != nil { return "", err }
    defer resp.Body.Close()
    var r struct{ Content []struct{ Text string `json:"text"` } `json:"content"` }
    if err := json.NewDecoder(resp.Body).Decode(&r); err != nil { return "", err }
    if len(r.Content) == 0 { return "", fmt.Errorf("empty content") }
    return r.Content[0].Text, nil
}
```

For the map step (many window calls) the **Batches API** (50% cheaper) or prompt
caching of the fixed system+catalog block is the cost-right move — but those are
optimizations; the simple per-window call above is correct and implementable first.

### 4.6 Model IDs (re-verify before wiring — house rule)

Prefer the **aliases** as the durable primary — they always resolve to the latest
and survive snapshot bumps, and the `claude` CLI accepts them directly:

| Role | CLI alias | Full ID (as supplied 2026-06-18) | Use |
|---|---|---|---|
| **Deep** (Tier 2 plan/synthesis) | `opus` | `claude-opus-4-8` | the final Proposal; hardest semantic synthesis |
| **Mid** (Tier 2 bulk map) | `haiku` | `claude-haiku-4-5`-class | per-window candidate judging (cheap, many calls) |
| (Balanced, optional) | `sonnet` | current Sonnet snapshot | if a middle option is wanted |

The bundled `claude-api` skill table is **stale** (lists Opus 4.1 / Sonnet 4 /
Haiku 3.5 / `claude-3-5-haiku-latest`). The authoritative current IDs come from this
runtime (this agent = `claude-opus-4-8`; the Agent tool exposes `opus|sonnet|haiku|
fable` aliases) and Jordan's own naming. **Before wiring the API backend, re-verify
the exact snapshot IDs via the `claude-api` skill** (SPEC-BECKY-ASK §0 rule). The
CLI backend sidesteps this entirely by using aliases.

### 4.7 How the GUI (WebView2) calls it + streams the proposal

WebView2 in Go (e.g. `jchv/go-webview2`, cgo-free via the loader) runs the UI as a
local page; Go and JS talk over `Bind` (JS→Go calls) and `Eval` (Go→JS injection):

```go
// Go side: bind one function the page calls on submit.
w.Bind("beckyAsk", func(utt string) error {
    go func() {
        prop, err := router.Handle(context.Background(), utt, currentContext())
        if err != nil { w.Eval(`beckyError(` + jsonStr(err.Error()) + `)`); return }
        // stream the proposal to the page; it renders the ✓/✗ overlay
        w.Eval(`beckyProposal(` + mustJSON(prop) + `)`)
    }()
    return nil // return immediately; result arrives via Eval (non-blocking UI)
})
// page calls window.beckyApply(idx) / window.beckyReject(idx); Go runs handlers on ✓.
w.Bind("beckyApply", func(id string) error { return timeline.Apply(pendingProposal(id)) })
```

The Proposal is JSON-serialized straight to the page; the page's `beckyProposal()`
draws the before→after preview and the ✓/✗ buttons — the same propose-then-apply
overlay the canvas already proved, now in HTML. Long frontier calls run on a
goroutine so the WebView stays responsive; if streaming output is wanted, swap the
single `Eval` for incremental `Eval`s driven by `--output-format stream-json`
(CLI) or the API stream.

---

## 5. Implementation order (for the build agent)

1. **`internal/llmlocal`** — lift `cmd/ask/llama.go` into a shared package; add a
   warm-server option. (Pure refactor; `cmd/ask` keeps working.)
2. **`internal/assistant`** — `Action`/`Args`/`Proposal` types + the verb handlers
   (Tier 0), the DSL parser + validator (allowlist), and `classifyTier`
   (deterministic). Unit-test the grammar + validator + tier decision hard (no
   model needed — pure Go, table-driven).
3. **`Funnel`** — wrap `becky-search` (`exec.Command`) + transcript grep; the
   map-reduce window splitter. Unit-test with fixture sidecars (no model).
4. **`localBackend`** — the Tier-1 adapter. **`claudeCLIBackend`** — Tier-2a (the
   verified flags above). **`apiBackend`** — Tier-2b. Each behind `Available()` so a
   missing engine degrades cleanly.
5. **WebView2 binding** — `beckyAsk`/`beckyApply`/`beckyReject`; render the overlay.
6. Smoke-test the full chain on a real case folder (local agent / Jordan's box):
   literal ask → Tier 0; fuzzy add → Tier 1; "every time he offered money for the
   cat" → Tier 2 funnel → Proposal → ✓.

Every model/network boundary is behind an interface with `Available()`, so the
whole thing builds and unit-tests green with **no model and no network** (CLAUDE.md
§3) — the deterministic Tier 0 + funnel + DSL validator are fully testable offline,
and the three Backends are thin, mockable adapters.

---

## 6. Constraint check (against the brief)

- **Deterministic-first / don't burn the Max plan:** `classifyTier` lands most
  turns at Tier 0; Tier 1 is the default for "needs a little intelligence"; Tier 2
  is triple-gated (classified + online-on + budget) and conservative when unsure.
- **Offline by default; online is explicit + logged:** `Online` defaults false;
  every Tier-2 call goes through `r.log("ONLINE escalation: …")` and a visible badge.
- **Never modify originals:** funnel + index are read-only on media; only
  `grab_frame`/`export` write, to the work dir.
- **500GB never ingested:** the funnel bounds the model's context to one window of
  retrieved candidates, regardless of folder size.
- **Show me, don't do it:** every mutate returns a `Proposal`; nothing applies
  without ✓.
- **Little abstraction, direct control:** the action allowlist *is* the program's
  control surface; each verb is one deterministic handler — no free-form exec.
- **cgo-free:** local = HTTP; frontier = exec/HTTP; WebView2 via the cgo-free
  loader.

---

## 7. Open decisions for Jordan

1. **`--system-prompt-file` absent in this CLI build** — passing a large system
   prompt as `--append-system-prompt <text>` is fine but shows in process args.
   Acceptable, or prefer the API backend for the big-system-prompt calls?
2. **Warm llama-server lifetime** — keep one resident for the becky-clip session
   (faster, holds GPU VRAM) vs spawn-per-turn (frees VRAM for the player)? Recommend
   resident with an idle-timeout shutdown.
3. **Frontier default model** — `opus` for everything Tier 2 (best quality, most
   tokens) vs `haiku` for the map step + `opus` only for the final plan (recommended:
   the split; far cheaper on the Max plan).
4. **Mid-tier id** — confirm the exact `claude-haiku-4-5`-class snapshot via
   `claude-api` before the API backend ships (the CLI alias `haiku` needs no
   confirmation).
