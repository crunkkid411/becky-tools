# HANDOFF: VideoAgent seam (H-4..H-7) — Go side audit + C++ wiring spec

Written by the Go-side agent for `fix/becky-review-3-audio`. Scope was
`becky-go/cmd/clip/*`, `internal/editmodel/*`, `internal/edittools/*`,
`internal/ctlagent/*` — NOT `native/becky-review/main.cpp` (the orchestrator
owns it), `internal/subs/*`, or `internal/reel/*`. Everything below the "What
the C++ side must do" headers is a **spec for another agent to implement in
main.cpp** — nothing in main.cpp was touched by this pass.

Background: `BUILD_1.md` §4-H (L477-484) and §10 (L578-621), `BUILD-INPUTS.md`
L11-30, `CONTINUE-HERE.md` "The missing half". The decision this seam
implements: VideoAgent has no editable intermediate representation between
intent and rendered output, so becky's adapter keeps its intent→workflow
brain and replaces its "render the video" terminal step with "emit becky
engine verbs" — the engine's verb table + `apply_edit_batch` (H-4) IS that
missing intermediate representation.

## Transport recap (so the per-item sections don't repeat it)

The C++ app (`native/becky-review/main.cpp`) spawns `becky-review-engine.exe
bridge` (falls back to `clip.exe bridge` — see `engineStart()`,
main.cpp:95-128) once at boot and talks NDJSON over its stdin/stdout
(`main.cpp:78-168`, `cmd/clip/main.go:122-213`). One line per request, one
line per reply, request/reply correlated by a client-chosen `id`:

```
stdin :  {"id":"r1","verb":"search","args":{"query":"cat"}}
stdout:  {"id":"r1","reply":{"ok":true,"data":[...]}}
```

`reply` is always `{"ok":bool,"data":<verb-specific>,"error":string}` — the
same envelope `App.Call` returns everywhere (`cmd/clip/bridge.go:22-43`).
Every existing `engineCall(verb, args, timeoutSec)` in main.cpp already
speaks this; H-4/H-6/H-7 add no new transport, only new/existing **verbs**.

H-5 adds ONE new top-level line shape sharing the same stdout stream — see
its section below.

---

## H-4 — `apply_edit_batch` (atomic multi-op undo span)

**Status: DONE, Go side. Verb exists, is tested, and is exercised end-to-end
today via the H-6 chat path — but no UI element calls the bare
`apply_edit_batch` verb directly.**

Evidence:
- Verb + op application: `cmd/clip/edit_batch.go:1-227` (`ApplyEditBatch`,
  `applyOneOpLocked`). One `pushUndoLocked()` call before the loop
  (`edit_batch.go:68`), each op inside the loop does **not** push its own
  undo entry — that's the atomicity.
- Bridge dispatch: `cmd/clip/bridge.go:223-232` (`case "apply_edit_batch"`).
- Malformed-op parsing (H-2): `cmd/clip/bridge.go:434-457` (`argEditOps`) —
  a non-object entry or a missing `verb` is dropped before `ApplyEditBatch`
  ever sees it; inside the batch, an unknown verb hits the default case in
  `applyOneOpLocked` (`edit_batch.go:210-212`) and is reported per-op, never
  a crash.
- Tests asserting VALUES (not just "it builds"):
  `cmd/clip/edit_batch_test.go` — `TestApplyEditBatchIsOneUndoSpan` (asserts
  `Undo()` count, the actual point of H-4), `TestApplyEditBatchEmptyIsRejectedNoUndoEntry`,
  `TestApplyEditBatchBadOpIsSkippedNotFatal`, `TestApplyEditBatchViaBridgeVerb`.
- **Already wired into H-6's approved-proposal path**: `applyActions`
  (`cmd/clip/app.go:1353-1389`) routes every clip-mutating action from an
  approved chat proposal through ONE `ApplyEditBatch` call instead of one
  undo entry per action — regression-tested by
  `TestApplyActionsBatchesClipMutationsIntoOneUndo` (`edit_batch_test.go:108-136`).
- **What is NOT wired**: main.cpp never calls `engineCall("apply_edit_batch", ...)`
  directly — only `engineCall("apply_proposal", ...)` (main.cpp:4714), which
  goes through `applyActions` → `ApplyEditBatch` under the hood. So a human
  using the chat already gets H-4's atomicity for free; a future scripted
  multi-op planner (P-AI-2) that wants to hand the engine a raw op list with
  no proposal round-trip has no button yet, but the verb it needs already
  exists and is tested.

### Wire shape

Request:
```json
{"id":"r7","verb":"apply_edit_batch","args":{"ops":[
  {"verb":"add_clip","args":{"source":"C:/case/ring.mp4","in":1.0,"out":3.0,"label":"a"}},
  {"verb":"set_label","args":{"id":"c1","text":"relabeled"}}
]}}
```
`ops[].verb` is one of `add_clip|remove_clip|reorder|set_trim|split|set_label`
(exactly the single-clip bridge verbs' arg shapes — see `bridge.go:115-144`
for each one's args). Any other verb name is reported, not applied.

Reply:
```json
{"id":"r7","reply":{"ok":true,"data":{
  "timeline": { "name":"...", "clips":[ /* ClipView[] */ ], "overlay":{...}, "duration_sec":6.0 },
  "results": [
    {"verb":"add_clip","ok":true,"new_id":"c2"},
    {"verb":"set_label","ok":true}
  ]
}}}
```
`EditOpResult` (`edit_batch.go:26-32`): `{verb, ok, error?, new_id?}` —
`new_id` is set only by `add_clip`/`split`. An **empty `ops` array** is a
batch-level error, not a partial success: `{"ok":false,"error":"empty edit batch"}`.

### What the C++ side must send / do

1. Build `ops` as a plain array — reuse whatever code already builds a single
   `add_clip`/`set_trim`/etc. args object per op (the args shapes are
   identical to the single-clip verbs already called at main.cpp:2834/2821/3592/etc.).
2. On `ok:true`: call the SAME `loadTimelineView(d["timeline"])` already used
   after `load_reel`/`apply_proposal` (main.cpp:2525, 4717) — no new
   timeline-rendering code needed.
3. Walk `data.results[]` and surface any `ok:false` entries as a plain-language
   line (e.g. append to the existing `g_renderMsg`/`g_askAnswer` status text) —
   don't silently drop them; that's the human's only visibility into a
   partially-failed batch.
4. One Ctrl+Z after this call must revert the WHOLE batch — no special client
   code needed, this falls out of the existing `undo` verb already wired.

### Failure modes

- Malformed op (missing `verb`, non-object entry): dropped before it reaches
  the batch; never appears in `results`, never crashes (H-2).
- Unknown op verb inside an otherwise-valid array: `results[i] = {verb, ok:false, error:"unknown batch op verb"}` —
  every OTHER op in the batch still applies (H-2's "one bad thing degrades,
  doesn't take down the rest").
- Empty `ops`: batch-level `ok:false`, no undo entry pushed — don't call `undo`
  expecting to find this batch; there's nothing to find.
- Engine process dead / unreachable: this is `engineCall`'s existing
  `{"ok":false,"error":"engine not running"}` / `"engine timeout / no reply"`
  path (main.cpp:173-204) — already handled generically, nothing batch-specific
  to add (H-3).

### One-command proof (no GUI)

Go-side, asserting the actual undo-count value:
```
cd X:\AI-2\becky-tools\becky-go
go test ./cmd/clip/... -run TestApplyEditBatchIsOneUndoSpan -v
```
End-to-end through the real NDJSON transport (what the C++ agent should run
to prove the wire-level contract before wiring a button — adjust the folder
path to a real case folder with at least one video):
```powershell
@'
{"id":"1","verb":"open_folder","args":{"folder":"C:/case"}}
{"id":"2","verb":"apply_edit_batch","args":{"ops":[{"verb":"add_clip","args":{"source":"C:/case/ring.mp4","in":1,"out":3}},{"verb":"add_clip","args":{"source":"C:/case/ring.mp4","in":4,"out":6}}]}}
{"id":"3","verb":"undo","args":{}}
{"id":"4","verb":"undo","args":{}}
'@ | X:\AI-2\becky-tools\becky-go\bin\becky-review-engine.exe bridge
```
Assert: reply `2`'s `data.timeline.clips` has length 2; reply `3`'s
`data.changed=true` and `data.timeline.clips` has length **0** (both clips
gone in ONE undo, not one-at-a-time); reply `4`'s `data.changed=false`
(nothing left to undo — proves the batch cost exactly one entry).

---

## H-5 — agent-activity `event` stream

**Status: WAS MISSING ENTIRELY. Built this pass, Go side only — no consumer
in main.cpp yet.**

Before this pass there was no event/notify/poll mechanism anywhere in
`cmd/clip` (grepped `event|Event|notify|Notify|poll|Poll` across the package —
zero hits outside generated JS/CSS assets) and main.cpp's `engineReader`
(main.cpp:142-169) only ever recognized a line containing both `"id"` and
`"reply"`. GUI-RULES.md §2 (L77-78) had already specified the concept — an
async `event` push for "becky is thinking…"/job progress — but nothing
implemented it.

### What was built

- `cmd/clip/app.go:118-138`: `EventEmitter func(kind, source, text string)`
  type + `App.emit EventEmitter` field + `App.emitEvent(kind, source, text)` —
  a nil-safe call site (no-op when nothing is listening, so every existing
  caller — tests, the WebView2 build, headless `call` — is unaffected).
- `cmd/clip/app.go:1296-1311` (`Ask`): emits `("started","ask", "Thinking: <utterance, ≤80 runes>")`
  before the turn runs, and `("done","ask", <one-liner>)` after — the
  one-liner is `Proposal.PreviewText` on success (already the exact "one
  human sentence" GUI-RULES.md/H-6 use for the chat's own answer text) or
  `"Could not answer: <error>"` on failure.
- `cmd/clip/edit_batch.go:74,77-93` (`ApplyEditBatch`/`batchSummary`): emits
  ONE `("done","apply_edit_batch", "Applied N edit(s)[, M failed].")` per
  batch, after the ops run. Covers BOTH the raw `apply_edit_batch` verb and
  every H-6 approved-proposal apply (since `applyActions` routes through this
  same function) — one call site, both paths covered. The rejected-empty-batch
  path emits nothing (there's no activity to report).
- `cmd/clip/main.go:139-149` (doc), `:191-206` (`cmdBridge`): the actual NDJSON
  write. `app.emit` is assigned before the stdin scanner loop starts (no race
  with the first request), shares the same `outMu`/`out` writer as reply
  lines (one physical writer, two logical line shapes) so replies and events
  can never interleave into a garbled line.

Deliberate scope limit: only `ask` and `apply_edit_batch` emit events. `kind`
support is `started`/`done` only — no `progress` is emitted, because the only
place a mid-turn progress signal could come from is inside
`internal/assistant.Router.Assist`, which is outside this pass's owned
directories. The wire shape below reserves `"progress"` as a valid `kind` for
whenever that's wired up; no Go code currently sends it.

Also deliberately NOT correlated to a specific request `id` — these are an
activity-log style push (GUI-RULES.md's own examples — "becky is thinking…",
job progress — are log lines, not request-scoped acks). Jordan drives one
chat turn at a time in practice; if concurrent asks become real, add a
`turn_id` field then (nothing about the current shape blocks that later).

### Wire shape

```json
{"event":{"kind":"started","source":"ask","text":"Thinking: add clip 1"}}
{"event":{"kind":"done","source":"ask","text":"Add 1 clip."}}
{"event":{"kind":"done","source":"apply_edit_batch","text":"Applied 2 edit(s)."}}
```
`kind`: `"started"|"progress"|"done"` (string). `source`: the seam that
produced it — currently `"ask"` or `"apply_edit_batch"`, both string
constants, treat as open-ended. `text`: always non-empty when the line exists
at all (an event with empty text is never sent — `emitEvent` drops it).

**The line has no `"id"` key.** That is the entire discriminator: a reader
that currently does `if (j.contains("id") && j.contains("reply"))` (main.cpp:156)
already safely ignores every event line as-is — this shipped with ZERO risk
to the current binary. Nothing needs to change in main.cpp for H-5 to be
"safe"; something needs to change for it to be **visible**.

### What the C++ side must send / do

Nothing to send — this is engine→client only. To consume it, add a branch in
`engineReader()` (main.cpp:154-163) alongside the existing `id`+`reply` check:

```cpp
json j = json::parse(line);
if (j.contains("id") && j.contains("reply")) {
    // ...existing reply-routing, unchanged...
} else if (j.contains("event") && j["event"].is_object()) {
    // H-2: type-guard every field individually — a malformed event must be
    // dropped, never crash the reader thread that also owns reply delivery.
    json ev = j["event"];
    std::string kind = ev.value("kind", std::string());
    std::string source = ev.value("source", std::string());
    std::string text = ev.value("text", std::string());
    if (!text.empty()) {
        std::lock_guard<std::mutex> lk(g_activityMx);   // new small mutex, new small deque
        g_activityLog.push_back({kind, source, text, nowSec()});
        if (g_activityLog.size() > 50) g_activityLog.pop_front();  // cap — this is a status feed, not a database
    }
}
```
Render `g_activityLog` as a small scrolling list in the right panel (the
`qa` ImGui window, main.cpp:4617-4734 is the existing home for AI-facing UI) —
e.g. above or below the existing `g_askAnswer`/proposal block. Keep it
passive: no buttons, no interaction, just "here's what becky has been doing."

### Failure modes

- Any field missing or the wrong JSON type (`ev.value(key, default)` already
  degrades to the default rather than throwing) — never crash the reader.
- `j["event"]` present but not an object (e.g. a future protocol bug sends
  `"event":"oops"`) — the `is_object()` guard above skips it, doesn't throw.
- Engine dead: no events arrive, same as no replies arrive — already covered
  by the existing `g_engine.alive` watchdog; no new dead-engine handling
  needed since this is best-effort status, not a required signal.

### One-command proof (no GUI)

```
cd X:\AI-2\becky-tools\becky-go
go test ./cmd/clip/... -run TestAskEmitsStartedAndDoneEvents -v
go test ./cmd/clip/... -run TestApplyEditBatchEmitsDoneEvent -v
```
Through the real transport (watch for the `{"event":...}` line arriving
BEFORE the `{"id":"2","reply":...}` line for the same request — the ordering
is guaranteed: `emitEvent("started",...)` runs, then completes its write, on
the same goroutine that later writes the reply, serialized through the same
mutex):
```powershell
@'
{"id":"1","verb":"open_folder","args":{"folder":"C:/case"}}
{"id":"2","verb":"ask","args":{"utterance":"add clip 1"}}
'@ | X:\AI-2\becky-tools\becky-go\bin\becky-review-engine.exe bridge
```
Expect three lines after the folder opens: an `{"event":{"kind":"started","source":"ask",...}}`
line, then the `{"id":"2","reply":{...}}` line, then an
`{"event":{"kind":"done","source":"ask",...}}` line.

---

## H-6 — plain-language intent → timeline edits (ask / apply_proposal / reject_proposal)

**Status: DONE, both sides. This is the one item that's actually fully wired
end-to-end already — audit found no gap to fix.**

Evidence:
- Go seam: `Ask` (`app.go:1249+`, now `:1245-1312` with the H-5 events added),
  `ApplyProposal` (`app.go:1323-1331`), `RejectProposal` (`app.go:1334-1336`);
  bridge cases at `bridge.go:212-222`.
- `ApplyProposal` → `applyActions` → `ApplyEditBatch`: the H-4 atomicity IS
  applied to every approved chat edit (`app.go:1338-1389`, see H-4 section
  above), regression-tested by `TestApplyActionsBatchesClipMutationsIntoOneUndo`.
- C++ side already calls all three verbs and renders the round trip:
  `engineCall("ask", ...)` at main.cpp:4671, proposal preview + diff render at
  main.cpp:4700-4712, `engineCall("apply_proposal", ...)` at main.cpp:4714
  (applies `loadTimelineView(d["timeline"])` on success, shows "Ctrl+Z
  reverts the whole pass"), `engineCall("reject_proposal", ...)` at
  main.cpp:4727. This is the "show-me, don't do it" pattern from
  GUI-RULES.md §4 already working: nothing mutates until the human clicks
  Apply.
- This is the SAME seam the human types into — no separate AI tool surface,
  per Jordan's explicit constraint (`BUILD-INPUTS.md:18-22`). Confirmed: `ask`
  is a single bridge verb indistinguishable in shape from any other verb call.

### Wire shape

Request:
```json
{"id":"r8","verb":"ask","args":{"utterance":"add clip 1"}}
```
Reply (a MUTATING turn — has `id` + at least one action):
```json
{"id":"r8","reply":{"ok":true,"data":{
  "id":"p1",
  "preview_text":"Add 1 clip.",
  "actions":[{"verb":"add_clip","args":{"source":"C:/case/ring.mp4","in":"1","out":"3"}}],
  "invalid":[],
  "preview":[{"label":"timeline","before":"0 clips","after":"1 clip"}],
  "tier":1,
  "sources":[],
  "note":"",
  "cost":{},
  "mutates":true,
  "exec_commands":[]
}}}
```
A NON-mutating turn (a plain answer, or a Tier-0 command that already ran
some other way) carries `mutates:false` and/or an empty `id`/`actions` — the
existing C++ code already treats "no id AND no actions" as "nothing to
approve, no proposal card" (main.cpp:4683-4691); that check is correct and
needs no change. `tier` is a small int: `0`=deterministic, `1`=local model,
`2`=frontier/Claude (`internal/assistant/state.go:15-19`).

Apply:
```json
{"id":"r9","verb":"apply_proposal","args":{"id":"p1"}}
```
```json
{"id":"r9","reply":{"ok":true,"data":{
  "timeline": {"name":"...","clips":[...],"overlay":{...},"duration_sec":2.0},
  "exec_commands":[]
}}}
```
Reject:
```json
{"id":"r10","verb":"reject_proposal","args":{"id":"p1"}}
```
```json
{"id":"r10","reply":{"ok":true,"data":{"rejected":"p1"}}}
```

### What the C++ side must send / do

Already done — see the evidence list above. No action needed; this section
exists so a future agent doesn't re-discover the same wiring from scratch.

### Failure modes (already handled)

- `ask` failure (e.g. no AI backend available): `ok:false`, main.cpp already
  falls back to `g_askAnswer = r.value("error", ...)` (main.cpp:4693-4694,
  H-3's "visible plain-language error, never a hang").
- `apply_proposal` on an expired/unknown id: `Router.Apply` returns an error
  (verified by reading `ApplyProposal`'s error path, `app.go:1323-1328`); C++
  already shows `"Apply failed: " + error` (main.cpp:4720).
- Proposal actions that failed VALIDATION (parsed but unusable — bad verb,
  missing required arg) surface in `invalid[]` and are never executed
  (`assistant.Invalid`, `schema.go:72-79`) — shown, not silently dropped; no
  current C++ code renders `invalid[]` in the diff list (main.cpp:4705-4711
  only walks `preview[]`). Not a correctness bug (invalid actions genuinely
  don't run), but a legibility gap — worth a follow-up render pass if Jordan
  hits a proposal with rejected sub-actions and wonders why only some landed.

### One-command proof (no GUI)

```
go test ./cmd/clip/... -run TestAskAddClipByHitUsesLastSearch -v
go test ./cmd/clip/... -run TestApplyActionsBatchesClipMutationsIntoOneUndo -v
```
Both already exist and pass; they're the deterministic Tier-0 path (no model
needed), which is exactly what CI runs. A frontier-tier ("find every time he
threatens...") proof needs a live model and is a local-machine-only check —
not scripted here since it costs tokens per Jordan's money rule.

---

## H-7 — forensic path reachable in-app (query → qmd recall → becky-judge → becky-hits → timeline)

**Status: the PIPELINE works and is verified (three separate, tested Go
binaries). It is reachable only as an EXTERNAL pre-launch flow, not as an
in-app interactive action while Becky Review is already running. This is the
one real gap among H-4..H-7, and it's a scope decision, not a bug — flagging
for a decision, not fixing it in this pass (out of this task's code-change
scope, which was H-4/H-5 only).**

The three stages exist as separate CLI tools, each with its own `--selftest`:
- Stage 1 (RECALL): `internal/qmd` — hybrid BM25+vector search, already
  exposed in-app as the `qmd_search` bridge verb (`cmd/clip/qmd.go`,
  `bridge.go:59-62`) and used by the library search panel today.
- Stage 2 (JUDGE): `becky-go/cmd/becky-judge` — reads Stage-1 candidates, a
  large LLM (Claude via `internal/agentrun`) resolves coded language /
  rejects noise per a rubric+alias map, writes `_forensic_hits.json`
  (`cmd/becky-judge/main.go:1-24`).
- Stage 3 (reel-build): `becky-go/cmd/becky-hits` — turns that hit-list into
  a real `edl.Reel` resolved against the open case folder
  (`cmd/becky-hits/main.go:1-20`).

Today's ACTUAL flow (verified working, per project memory
`becky-hits-forensic-reel`): an outside agent runs `becky-judge` then
`becky-hits` to produce a reel JSON, then either (a) sets `BECKY_REVIEW_REEL`
before Becky Review starts — `cmd/clip/main.go`'s `cmdBridge` pre-loads it at
boot (grep `BECKY_REVIEW_REEL` in main.go) — or (b) the app is already open
and something calls the EXISTING `load_reel` verb (`bridge.go:166-168`,
`app.go:1078-1091`) with the resulting path. **(b) already works with zero
new code** — `load_reel` doesn't care how the reel file was produced.

What's actually missing is a way for JORDAN, from inside a running session,
to type a forensic query and have stages 1-3 run and land on his timeline
without an outside agent doing it for him first. Grepped main.cpp for
`becky-judge`/`becky-hits`/`VerbFindQuotes`: zero hits — there is no button,
no chat verb, no code path that starts this pipeline from inside the app.

### Recommended next action (not built this pass — a scope decision)

The cleanest additive shape, consistent with "extend the ask seam, never
fork it" (H-6's rule) and "no MCP server, no separate AI tool surface"
(`BUILD-INPUTS.md:18-22`): a new bridge verb in `cmd/clip`, e.g.
`forensic_query(query string) -> {timeline, note}`, that shells `becky-judge`
then `becky-hits` against the open folder (same ExecCommand-building pattern
`internal/assistant/funnel.go` already uses for `becky-quotes`) and finishes
by calling the EXISTING internal `a.LoadReel(path)` — reusing, not
duplicating, the reel-loading code path load_reel already has. This is
explicitly a recommendation, not a spec ready to implement: it needs a design
call on (a) whether this belongs in `cmd/clip` (mine) or `internal/assistant`
(not mine — would need that agent) as a new `assistant.Verb`, and (b) how
long a judge+hits pass realistically takes on Jordan's case-folder scale,
which determines whether it needs the same started/progress/done event
treatment as `ask` (H-5's mechanism already generalizes to this — `a.emit`
is verb-agnostic — so wiring it in later costs nothing extra).

### One-command proof of what EXISTS today (no GUI)

```
becky-judge.exe --selftest
becky-hits.exe --selftest
```
Both are offline, synthetic, no model/qmd required — proves the pipeline's
Go code is sound independent of the in-app wiring question above.

---

## Full verification (this pass)

```
cd X:\AI-2\becky-tools\becky-go
go build ./...                     # clean, no output
go vet ./...                       # clean, no output
go test -race ./cmd/clip/...       # 90 tests, all PASS (21.1s)
go test ./internal/editmodel/... ./internal/edittools/... ./internal/ctlagent/...  # ok (cached, untouched)
gofmt -l .                         # lists ~most of the repo — pre-existing Windows CRLF
                                    # condition (CLAUDE.md §5's documented exception),
                                    # confirmed NOT introduced by this pass: edit_batch.go
                                    # and edit_batch_test.go (this pass's only new-content
                                    # files) produce an EMPTY `gofmt -d` diff; the touched
                                    # pre-existing files (app.go, main.go, util.go,
                                    # app_test.go, bridge_test.go) show only whole-file
                                    # line-ending diffs, not real formatting drift.
```

## Files changed this pass

| File | Change |
|---|---|
| `becky-go/cmd/clip/app.go` | `EventEmitter` type + `App.emit` field + `emitEvent` helper (H-5); `Ask` brackets its turn with started/done events |
| `becky-go/cmd/clip/edit_batch.go` | `ApplyEditBatch` emits one done event per batch; new `batchSummary` helper |
| `becky-go/cmd/clip/main.go` | `cmdBridge` wires `app.emit` to a new NDJSON `{"event":...}` line writer; wire-format doc comment extended |
| `becky-go/cmd/clip/util.go` | new `truncateText` helper (event text length cap) |
| `becky-go/cmd/clip/app_test.go` | `TestAskEmitsStartedAndDoneEvents`, `TestAskWithNoEmitterStillWorks` |
| `becky-go/cmd/clip/edit_batch_test.go` | `TestApplyEditBatchEmitsDoneEvent`, `TestApplyEditBatchEmptyBatchSkipsEvent` |
| `becky-go/cmd/clip/bridge_test.go` | `TestTruncateText` |

No changes to `internal/editmodel`, `internal/edittools`, or `internal/ctlagent` —
audited, already correct for their part of this contract, nothing to fix.
