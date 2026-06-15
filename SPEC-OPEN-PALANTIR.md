# SPEC-OPEN-PALANTIR.md — `becky-palantir` — cross-evidence entity & link graph

> **SPEC — NOT BUILT, AWAITING JORDAN'S APPROVAL.**
> Research + design only. No Go code has been written. No new binary exists. Nothing
> in `becky-go/` has been changed. Jordan approves before any build starts.
>
> Authored 2026-06-14. The external project this wraps (OpenPlanter) was researched live
> on 2026-06-14 against its public GitHub README + DEMO + the launch write-ups; sources
> cited inline and in §12. **Several integration-critical facts are NOT documented by
> OpenPlanter and are marked `ASSUMPTION (local agent must verify)` — the local agent
> confirms them by actually running the binary before any code depends on them.**

---

## 1. One-line purpose

`becky-palantir` **fuses the outputs of becky's other tools into a single, queryable
who-knew-whom / where / when entity-and-link graph over Jordan's OWN evidence corpus** —
by driving the open-source [OpenPlanter](https://github.com/ShinMegamiBoson/OpenPlanter)
investigation agent as its entity-resolution / link-analysis engine, then normalizing
OpenPlanter's free-form output back into a deterministic becky JSON graph that obeys
`FORENSIC-OUTPUT-PHILOSOPHY.md` (corroborated edges stated plainly; weak links surfaced
as candidates, never auto-asserted).

This is **forensic case analysis over Jordan's local evidence** — transcripts, face/voice
IDs, events, OSINT frames + EXIF/GPS, OCR text that becky already produced. It is **not**
mass surveillance and **not** third-party data mining. The default mode never touches the
network; OpenPlanter's web-search powers are an explicit, logged, opt-in enrichment step.

---

## 2. Where it fits the catalog (and why it's a NEW tool, not bolted onto an old one)

becky already answers, per clip: WHO (`identify`), WHAT-happens (`events`), WHAT-said
(`transcribe`), WHERE (`osint` EXIF/GPS), on-screen text (`ocr`). `consolidate` rolls those
into per-corpus coverage ("Braxton recognized in 41/… videos"). `search` makes the corpus
queryable as text. `cluster` groups recurring unknown faces/voices as "Person A."

**The missing layer is the GRAPH** — the relational question that no current tool answers:

- *Who co-occurs with whom, and where, and when?* (Person↔Person contact/co-occurrence)
- *Which people are tied to which places/devices?* (Person↔Place, Person↔Device)
- *What is the timeline of an entity across the whole corpus?* (Entity↔Event edges)

`consolidate` counts coverage per entity; it does **not** build edges BETWEEN entities.
`identify` names a person IN ONE CLIP; it does **not** relate two named people across the
corpus. `cluster` groups one stranger's own appearances; it does **not** link distinct
people. So this is a genuinely new capability, and per the single-tool principle it is its
**own** tool that does ONE thing — *build and query the cross-evidence entity graph* — and
nothing else. It does not re-detect, re-identify, or re-transcribe; it consumes what the
other tools emit and emits a graph.

| Tool | Answers | Granularity |
|---|---|---|
| `identify` | who is in THIS clip | per-clip, per-person |
| `cluster` | which appearances are the SAME unknown | per-person, own appearances |
| `consolidate` | how much of the corpus covers each entity | per-corpus, per-entity counts |
| **`becky-palantir`** | **how entities RELATE across the corpus** | **per-corpus, entity↔entity edges** |

---

## 3. The boundary — a thin becky wrapper around a fat external agent

OpenPlanter is a recursive LLM agent: it ingests heterogeneous datasets, resolves entities
with an LLM, spawns sub-agents (default `--max-depth 4`), can run shell, and can web-search
via Exa ([MarkTechPost launch piece, 2026-02-21](https://www.marktechpost.com/2026/02/21/is-there-a-community-edition-of-palantir-meet-openplanter-an-open-source-recursive-ai-agent-for-your-micro-surveillance-use-cases/);
[OpenPlanter README](https://github.com/ShinMegamiBoson/OpenPlanter)). It is powerful but
**online, non-deterministic, and free-form in its output** — the opposite of every becky
invariant. So `becky-palantir` is a deterministic *shell* around that non-deterministic
core, with the becky discipline enforced at the boundary, exactly the way `validate` wraps
Gemma and `identify` wraps InsightFace/CAM++:

```
  becky JSON corpus (deterministic)                          becky JSON graph (deterministic)
  identify.json / events.json / osint │                      │  graph.json (becky schema, §6)
  transcript / ocr_text / cluster.json│                      │  corroborated edges + candidate edges
                                      ▼                      ▲
   ┌───────────────────────────────────────────────────────────────────────────────┐
   │ becky-palantir  (Go CLI — JSON in / JSON out / exit code; the ONLY thing on PATH)│
   │                                                                                  │
   │  [A] PREPARE      deterministic: harvest becky outputs → a flat, neutral         │
   │                   evidence dataset (CSV/JSONL) + a fixed task brief written into  │
   │                   an OpenPlanter WORKSPACE dir. No model. Pure Go.                │
   │                                                                                  │
   │  [B] DRIVE        invoke OpenPlanter HEADLESS as a subprocess over that workspace │
   │   (helper stub)   (`openplanter-agent --task … --workspace … --headless`), with a │
   │                   STRICT prompt that forces it to WRITE one graph file in our     │
   │                   schema. Offline (Ollama) by default; web only if --enrich.      │
   │                                                                                  │
   │  [C] NORMALIZE    deterministic: read OpenPlanter's written graph file, validate, │
   │                   re-attach becky provenance, apply the corroborate-then-conclude │
   │                   rule, emit the becky graph.json. No model. Pure Go.             │
   └───────────────────────────────────────────────────────────────────────────────┘
```

**Load-bearing consequence:** OpenPlanter's only job is the *messy middle* — read the flat
evidence, resolve entities, propose edges. The becky wrapper owns intake, the output
schema, provenance, confidence gating, determinism of FORMAT, and degradation. If
OpenPlanter is missing, flaky, or returns garbage, the wrapper degrades to a partial graph
**built by becky's own deterministic co-occurrence pass** (§7) — it never crashes and never
emits half-JSON.

---

## 4. What becky already produces that becomes graph material

No new model and no new detection — this is a post-processing layer over existing outputs,
the same cheap-but-high-value shape as `cluster`:

- **Persons** — `identify.json` `corroborated[]` (named) + `unidentified[]`, and `cluster`
  "Person A/B" groupings for recurring unknowns. Each carries `source_file`,
  `timestamp`/`frame_index`, `source_sha256`, confidence.
- **Places** — `osint` EXIF/GPS (`gps_lat`/`gps_lon`, place), `framematch` same-room pairs,
  capture-location notes. Plus OCR'd signage/addresses from `ocr_text`.
- **Devices** — `osint` EXIF `make`/`model`/device id, container metadata.
- **Events** — `events.json` (scene/phone/multi-face) + `motion` bursts, each timestamped.
- **Time** — capture time from EXIF/QuickTime (`capture_time_source`), never raw mtime
  (per README's evidence-integrity rule).
- **Said-text / on-screen-text** — transcript lines + `ocr_text`, as edge EVIDENCE (e.g. a
  name spoken near another person → a candidate contact edge).

Every one of these already ties back to `source_file` + `source_sha256` + a timestamp, so
**provenance is free** and is carried verbatim onto every node and edge.

---

## 5. CLI contract (becky house style)

```
becky-palantir --corpus <dir>                 # root holding becky outputs (identify.json, events.json, …)
  [--identify-glob "<dir>/**/identify.json"]  # explicit harvest globs (override auto-discovery)
  [--events-glob   "<dir>/**/events.json"]
  [--osint-glob    "<dir>/**/osint*.json"]
  [--cluster       <cluster.json>]            # fold in becky-cluster "Person A" groupings
  [--db            <forensic.db>]             # alternative: read consolidated rows instead of globs

  [--engine openplanter|cooccur-only]         # default openplanter; cooccur-only = pure-Go, no agent
  [--openplanter-bin <path>]                  # openplanter-agent binary (else PATH / config)
  [--provider ollama|openai|anthropic|openrouter|cerebras]   # default ollama (LOCAL, offline)
  [--model <name>]                            # default per provider (ollama: llama3.2)
  [--max-depth 4] [--max-steps 100]           # passed through to OpenPlanter recursion controls
  [--enrich]                                  # ALLOW OpenPlanter web search (Exa). OFF by default.
  [--seed <int>]                              # recorded; passed to provider if it honors it (§7)

  [--edge-conclude 2]                         # ≥N independent signals → corroborated edge (default 2)
  [--cache <dir>]                             # cache OpenPlanter run by input-hash (deterministic replay)
  [--workspace <dir>]                         # OpenPlanter workspace (default <corpus>/.palantir-ws)
  [--output <file>]                           # graph JSON here instead of stdout
  [--query "<text>"]                          # optional: after build, answer one graph query and exit
  [--verbose]
```

Behavior: JSON-in / JSON-out, exit-coded, **offline + deterministic OUTPUT by default**,
graceful-degrade, never modifies source inputs (works on copies in the workspace). JSON to
stdout; plain-English headlines/progress to stderr only under `--verbose`. Exit 0 on a
completed graph OR a clean degrade-with-note; nonzero only on a hard error (e.g. corpus dir
unreadable).

`--query` (optional, second job? No — same job, read side): the tool's ONE job is the
graph; querying is just reading the graph it built. A query like `"who co-occurs with John
Clancy"` runs a deterministic Go traversal over the already-built graph and prints the
answer. It does **not** invoke OpenPlanter. (If Jordan prefers strict single-responsibility,
`--query` can be split into a trivial `becky-palantir-query` reader later; recommendation is
to keep it as a read-only verb on the same binary, mirroring how `becky-cluster` both builds
and reports clusters.)

---

## 6. Output JSON — the becky entity graph (synthetic values)

This is the **deterministic contract**: the format never varies even though OpenPlanter's
internal reasoning is non-deterministic. The normalizer (step C) is what guarantees it.

```json
{
  "tool": "becky-palantir v1.0.0",
  "engine": "openplanter",
  "engine_version": "openplanter-agent 0.5.1",
  "provider": "ollama",
  "model": "llama3.2",
  "enrichment": {"web_search": false, "note": "local corpus only; no network used"},
  "corpus": { "root": "X:\\evidence\\case-2601", "files_ingested": 312, "evidence_rows": 4188 },
  "determinism": {
    "input_sha256": "f3a1…",            // hash of the prepared evidence dataset (step A)
    "output_format": "deterministic",
    "reasoning": "non-deterministic-cached",
    "cache_hit": true,
    "seed": 7
  },

  "nodes": [
    {
      "node_id": "person:john-clancy",
      "kind": "person",
      "label": "John Clancy",
      "status": "documented",                 // documented = becky-identify corroborated name
      "aliases": ["John", "speaker JC"],
      "appearances": 87,
      "distinct_source_files": 41,
      "provenance": [
        {"source_file": "20250703_2031.mp4", "source_sha256": "ab12…",
         "timestamp": 5.0, "signal": "voice+face", "confidence": 0.94, "from": "identify.json"}
      ]
    },
    {
      "node_id": "person:cluster-A",
      "kind": "person",
      "label": "Person A (unnamed)",
      "status": "candidate",                   // recurring unknown from becky-cluster, no name yet
      "appearances": 41, "distinct_source_files": 41,
      "provenance": [ {"source_file": "…", "signal": "face-cluster", "confidence": 0.71, "from": "cluster.json"} ]
    },
    {
      "node_id": "place:gps:32.9_-96.7",
      "kind": "place",
      "label": "2601 Chatham Cir (candidate)",
      "status": "candidate",
      "provenance": [ {"source_file": "…", "signal": "exif-gps", "from": "osint", "gps_lat": 32.90, "gps_lon": -96.70},
                      {"source_file": "…", "signal": "framematch-same-room", "from": "framematch"} ]
    },
    {
      "node_id": "device:apple-iphone-13",
      "kind": "device", "label": "Apple iPhone 13", "status": "documented",
      "provenance": [ {"source_file": "…", "signal": "exif-make-model", "from": "osint"} ]
    },
    {
      "node_id": "event:phone-handoff:clip0207@12.3",
      "kind": "event", "label": "phone handed over", "status": "documented",
      "provenance": [ {"source_file": "clip0207.mp4", "timestamp": 12.3, "signal": "events", "from": "events.json"} ]
    }
  ],

  "edges": [
    {
      "edge_id": "co-occur:john-clancy~cluster-A",
      "kind": "co_occurrence",                 // co_occurrence | contact | location | device | timeline
      "source": "person:john-clancy",
      "target": "person:cluster-A",
      "directed": false,
      "status": "documented",                  // ≥2 independent corroborating signals → CONCLUDE (§7)
      "summary": "John Clancy and Person A appear together in 9 clips, twice in the same room.",
      "confidence": 0.88,
      "corroborating_signals": [
        {"signal": "same-clip co-appearance", "count": 9, "from": "identify.json"},
        {"signal": "same-room (framematch)",  "count": 2, "from": "framematch"}
      ],
      "provenance": [
        {"source_file": "20250704_0915.mp4", "source_sha256": "cd34…", "timestamp": 12.3},
        {"source_file": "20250709_1102.mp4", "source_sha256": "ef56…", "timestamp": 41.0}
      ]
    },
    {
      "edge_id": "location:cluster-A@place:gps:32.9_-96.7",
      "kind": "location",
      "source": "person:cluster-A", "target": "place:gps:32.9_-96.7",
      "directed": true,
      "status": "candidate",                   // ONE signal only → candidate, NOT asserted
      "summary": "Person A may be tied to 2601 Chatham Cir (single GPS-tagged clip).",
      "confidence": 0.41,
      "corroborating_signals": [ {"signal": "exif-gps in one clip", "count": 1, "from": "osint"} ],
      "provenance": [ {"source_file": "20250711_2210.mp4", "timestamp": 0.0} ]
    }
  ],

  "summary": {
    "documented_edges": 1,
    "candidate_edges": 1,
    "top_findings": [
      "John Clancy and Person A appear together across 9 clips and share a room twice — a corroborated association."
    ]
  },
  "degraded": false,
  "notes": {
    "honesty": "documented edges are corroborated conclusions; candidate edges are single-signal leads for human review, never asserted as fact.",
    "scope": "built only from Jordan's own becky evidence outputs; no third-party or network data unless --enrich was set (it was not)."
  }
}
```

Schema rules (enforced by the normalizer, §7):
- **`kind` is a closed set.** Nodes: `person | place | device | event`. Edges:
  `co_occurrence | contact | location | device | timeline`. Anything OpenPlanter invents
  outside this set is mapped to the nearest kind or dropped with a logged note — the schema
  is becky's, not the agent's.
- **`status` is the philosophy gate.** `documented` only when `corroborating_signals` has
  ≥ `--edge-conclude` independent entries (default 2) from **different** signal families;
  otherwise `candidate`. A lone weak signal is a candidate, never a conclusion — same rule
  `identify`'s `fuse.go` uses for names, applied to edges.
- **Every node and edge carries `provenance[]`** back to `source_file` + `source_sha256` +
  timestamp. An edge a human can't trace to clips is a bug, not a finding.
- **Plain language in `summary`/`label`** (FORENSIC-OUTPUT-PHILOSOPHY §2/§5): "appear
  together in 9 clips," not "co_occurrence_count=9 over node pair."

---

## 7. Offline + deterministic reconciliation (the hard part)

OpenPlanter is online (Exa web search) and LLM-driven (non-deterministic). becky requires
offline + deterministic. The reconciliation is the core of this spec:

1. **Default provider = Ollama (local), web search OFF.** `--provider ollama` runs the LLM
   locally with no API key ([OpenPlanter README — "Ollama runs models locally with no API
   key"](https://github.com/ShinMegamiBoson/OpenPlanter)). `--enrich` is required to let
   OpenPlanter use Exa/`fetch_url`; without it the wrapper does **not** set `EXA_API_KEY`
   in the subprocess env and the task brief explicitly forbids web tools. So the default run
   touches only Jordan's corpus and the local model. *ASSUMPTION (local agent must verify):
   that OpenPlanter cleanly no-ops its `web_search`/`fetch_url` tools when no `EXA_API_KEY`
   is present and the prompt forbids them — the README does not document a hard "offline"
   switch. If it can't be reliably prevented from reaching the network, fall back to
   `--engine cooccur-only` for any run that must stay offline.*

2. **Determinism of FORMAT, not of REASONING.** We cannot make an LLM bit-identical across
   runs (`--seed` is recorded and passed through, but *ASSUMPTION (local agent must verify):
   OpenPlanter exposes no documented seed/determinism flag* — see §12). becky's contract is
   therefore: **the OUTPUT SCHEMA is 100% deterministic** (step C is pure Go), and a
   **cached/seeded mode** makes a given corpus reproducible:
   - The prepared evidence dataset (step A) is hashed (`input_sha256`).
   - On a cache hit (`--cache`), becky-palantir **replays the stored OpenPlanter raw output**
     for that exact input hash instead of re-running the agent → identical graph, no network,
     no model. This is how "same input → same output" is honored in spirit: re-running over
     unchanged evidence yields the cached, byte-identical normalized graph.
   - `determinism.reasoning` is honestly labelled `non-deterministic-cached` (cache present)
     or `non-deterministic` (fresh run) so the analyst is never misled into thinking the LLM
     step was reproducible when it wasn't.

3. **The deterministic floor — `--engine cooccur-only`.** becky-palantir can build the graph
   **with no LLM at all**: a pure-Go pass that emits an edge for every pair of entities that
   appear in the same clip (co_occurrence), every entity↔place with shared GPS/room
   (location), every entity↔device sharing EXIF (device), and every entity↔event by
   timestamp (timeline), then applies the same ≥2-signal conclude rule. This is fully
   offline, fully deterministic, and is BOTH (a) the degrade target when OpenPlanter is
   missing/broken, and (b) a first-class engine Jordan can pick for a reproducible,
   audit-clean graph. OpenPlanter's value-add over this floor is *entity resolution* (merging
   "John" / "JC" / a cluster into one node) and *non-obvious cross-clip links* — but the
   floor guarantees a useful graph always exists.

4. **Enrichment is explicit, optional, and logged.** With `--enrich`, any web lookup
   OpenPlanter performs is captured into `enrichment.note` + per-edge `from: "web:<url>"`
   provenance, so web-derived links are visually distinct from corpus-derived ones and can
   never be mistaken for evidence from Jordan's footage. Default stays local.

5. **Degrade, never crash.** Missing `openplanter-agent` binary, missing local model,
   subprocess timeout, or an unparseable/empty graph file → log a typed note, fall back to
   `--engine cooccur-only`, set `"degraded": true` with a plain-language reason, and emit a
   valid partial graph at exit 0. (Same discipline as a missing Gemma/ffmpeg in `validate`.)

---

## 8. Integration stub contract (what the local agent wires up)

The cloud agent cannot run OpenPlanter (no binary, no model, no network at runtime). So the
DRIVE step (B) is a **documented helper stub** — the local agent only installs OpenPlanter
and confirms the exact call. The contract the cloud agent codes against:

### 8a. What becky HANDS OpenPlanter (step A output → workspace)
A workspace directory `<workspace>/` containing:
- **`evidence.jsonl`** — one flat record per becky observation, neutral columns:
  `{ "row_id", "kind", "label", "source_file", "source_sha256", "timestamp", "signal",
  "confidence", "gps_lat", "gps_lon", "device", "text" }`. (JSONL because OpenPlanter ingests
  heterogeneous files and has `read_file`/`search_files`; a flat table is the easiest thing
  for its LLM to resolve over.)
- **`entities_seed.csv`** — the already-known becky entities (named persons, places, devices,
  clusters) so OpenPlanter starts from becky's IDs and only has to find *links*, not re-derive
  identities. Columns: `node_id,kind,label,status,aliases`.
- **`TASK.md`** — the fixed, strict task brief (the prompt). It instructs OpenPlanter to:
  (1) resolve entities using `entities_seed.csv` as ground truth (do not invent persons);
  (2) propose only edges of the closed kind-set (§6); (3) attach, for every edge, the
  `row_id`s from `evidence.jsonl` that support it and a confidence in [0,1]; (4) **write the
  result to exactly `<workspace>/graph.out.json` via `write_file`** in the specified shape;
  (5) **not** use `web_search`/`fetch_url` unless enrichment is enabled. The required
  `graph.out.json` shape is the *raw* form the normalizer expects (a minimal subset of §6:
  `nodes:[{node_id,kind,label,aliases}]`, `edges:[{kind,source,target,confidence,evidence_row_ids:[…]}]`).

### 8b. How becky INVOKES OpenPlanter (the stub call)
```
openplanter-agent \
  --workspace <workspace> \
  --headless \
  --provider <provider> --model <model> \
  --max-depth <n> --max-steps <n> \
  [--reasoning-effort low] \
  --task "@TASK.md"     # ASSUMPTION: pass the brief as the task; see verify-list
# env: EXA_API_KEY set ONLY when --enrich; provider key set per --provider (none for ollama)
```
*ASSUMPTION (local agent must verify), because OpenPlanter's DEMO/README do not document a
machine-readable export ([DEMO.md is a vision doc, not an API ref](https://github.com/ShinMegamiBoson/OpenPlanter/blob/main/DEMO.md);
GUI graph is Cytoscape.js with no documented disk export):*
  - that `--task` accepts a long brief (or whether the brief must be inlined / fed on stdin);
  - that `--headless` actually runs to completion non-interactively and respects the
    "write graph.out.json" instruction reliably (an agent may ignore an output-path order —
    if so, the normalizer must instead scan `<workspace>` for the newest JSON the agent wrote
    and/or parse `.openplanter/` session artifacts);
  - the exact `.openplanter/` artifact layout (`settings.json` + `credentials.json` are
    documented; the rest is not).

### 8c. What becky PARSES BACK (step C input)
`<workspace>/graph.out.json` in the raw shape from 8a(4). The normalizer:
1. validates it (closed kind-set; every edge's `evidence_row_ids` resolve to real
   `evidence.jsonl` rows — drop any hallucinated edge whose rows don't exist, with a note);
2. re-attaches full becky provenance from the resolved rows (source_file/sha/timestamp);
3. recomputes each edge's `corroborating_signals` from the resolved rows and applies the
   ≥`--edge-conclude` conclude rule to set `status` (becky decides documented-vs-candidate,
   **not** the LLM's self-reported confidence — the LLM confidence is kept only as a tiebreak);
4. emits the §6 graph.

**Net:** the cloud agent writes step A (Go) and step C (Go) fully, plus a stub `DriveOpenPlanter()`
with this exact contract and a `// LOCAL AGENT: confirm flags here` marker. The local agent's
whole job is to make 8b real and confirm the 8c file actually appears.

---

## 9. Build plan (cloud vs local split)

**Cloud / web agent (here) — all deterministic, unit-testable without OpenPlanter:**
- `cmd/palantir/` Go CLI + flags (§5); `internal/` reuse for harvest (the same glob/db
  readers `consolidate`/`cluster` use), provenance, `pathx` for Windows paths.
- **Step A (PREPARE)** — harvest becky outputs → `evidence.jsonl` + `entities_seed.csv` +
  `TASK.md`; hash inputs. Pure Go.
- **Step C (NORMALIZE)** — validate + provenance-reattach + conclude-rule + emit §6 graph.
  Pure Go. **This is where the philosophy is enforced and most of the tests live.**
- **`--engine cooccur-only`** — the full pure-Go graph builder (degrade target + first-class
  engine). Pure Go, fully testable.
- **Step B stub** — `DriveOpenPlanter()` with the §8 contract, `// LOCAL AGENT` markers,
  graceful "binary not found → degrade" path.
- **Unit tests** — feed synthetic becky outputs + a synthetic `graph.out.json` and assert:
  closed kind-set enforced; hallucinated-edge rows dropped; ≥2-signal → `documented`,
  1-signal → `candidate`; provenance present on every node/edge; missing-binary →
  `cooccur-only` degrade at exit 0; cache replay → byte-identical graph; `--enrich` off →
  no `EXA_API_KEY` in the built subprocess env. (All run on CI with no model/network.)
- Push to `claude/*` branch, open **draft PR**, update CLAUDE.md §6 handoff.

**Local agent (Jordan's Win10 PC) — everything that needs the real binary/model/network:**
- Install OpenPlanter (pin a Release per Q below, or `pip install -e .`); `ollama pull` the
  chosen local model; run `openplanter-agent --list-models` / a smoke `--task`.
- **Confirm the §8 ASSUMPTIONS** — does `--headless` + `--task @TASK.md` run to completion?
  does it write `graph.out.json` where told, or must we scan the workspace? what's actually
  in `.openplanter/`? Wire the stub to the confirmed call.
- Run on a real slice of the corpus; tune the `TASK.md` prompt + `--max-depth`/`--reasoning-effort`
  so edges come back well-formed and grounded; confirm the offline (Ollama, no Exa) path
  truly makes no network calls (packet-watch); compare OpenPlanter's graph to the
  `cooccur-only` floor to confirm it ADDS value (entity merges, cross-clip links) rather than
  noise. Report quality + cost, commit to the same branch, run `build-all-tools.bat`.

---

## 10. Honesty / forensic guardrails

- **Documented edge = corroborated conclusion; candidate edge = single-signal lead.** State
  documented edges plainly ("John and Person A appear together in 9 clips"); flag candidates
  as leads for human review. Never assert a candidate as fact. (FORENSIC-OUTPUT-PHILOSOPHY
  top principle + §6 tags.)
- **becky owns the verdict, not the LLM.** `status` is computed deterministically from
  resolved becky provenance, not taken from OpenPlanter's self-reported confidence. An LLM
  saying "0.9 confident" with one supporting clip is still a `candidate`.
- **Hallucination guard.** Any edge whose `evidence_row_ids` don't resolve to real
  `evidence.jsonl` rows is DROPPED with a logged note — the graph can only contain links
  traceable to Jordan's actual evidence.
- **Network provenance is visible.** Web-enriched edges (only under `--enrich`) carry
  `from: "web:<url>"`; corpus edges never do. No analyst can confuse the two.
- **Scope statement in every output** ("built only from Jordan's own becky evidence; no
  third-party or network data unless --enrich"). This tool is for analyzing *owned*
  evidence, and the output says so.
- **Never modifies sources.** Works in a copy-only workspace; the corpus is read-only.

---

## 11. Open decisions for Jordan (need a human call)

1. **License compatibility.** OpenPlanter is MIT ([README](https://github.com/ShinMegamiBoson/OpenPlanter))
   — permissive and compatible. But becky *drives it as a separate subprocess binary* (does
   not vendor its source), so there's no license entanglement either way. Confirm we're happy
   depending on an external MIT tool; if not, `--engine cooccur-only` is a fully in-house
   fallback that needs no third party at all.
2. **Pin a Release vs build from source.** Pre-built binaries exist
   ([Releases](https://github.com/ShinMegamiBoson/OpenPlanter/releases/latest)) as a Windows
   `.msi` (Tauri GUI) and there's a `pip install -e .` CLI. For a *headless* driver we want
   the **CLI** (`openplanter-agent`), not the GUI. Decision: pin one CLI version (reproducible)
   vs track latest. Recommendation: pin.
3. **How much web enrichment to allow.** Default is OFF (local corpus only). Does Jordan ever
   want `--enrich` (Exa web search to, e.g., resolve a business name or address)? If yes, it
   needs an `EXA_API_KEY` and a clear logged-and-flagged policy; if never, we can hard-remove
   the web path for a guaranteed-offline tool.
4. **Which local model / provider.** Ollama `llama3.2` (default, fully offline, free) vs a
   bigger local model for better entity resolution vs a hosted API (Anthropic/OpenAI/OpenRouter/
   Cerebras) for quality at the cost of sending case evidence off-machine. **For forensic
   content the default must stay local-only** (same privacy rule as `becky-new-tool` §7). A
   hosted provider should be opt-in and clearly forbidden for sensitive cases.
5. **GUI vs headless.** OpenPlanter ships a Tauri desktop GUI with an interactive Cytoscape
   graph. becky-palantir uses ONLY the headless CLI and renders its own deterministic JSON;
   Jordan can still open the GUI separately to explore. Confirm we don't need becky to launch
   the GUI (recommendation: no — keep becky headless; GUI is a manual, optional viewer).
6. **`--query` on the same binary vs a separate reader** (§5). Recommendation: keep it on the
   same binary as a read-only verb.

---

## 12. Honest unknowns / risks the LOCAL agent MUST verify

OpenPlanter's public docs are a **product-vision README + a conceptual DEMO**, light on API
specifics ([DEMO.md is explicitly aspirational, with no commands/schemas](https://github.com/ShinMegamiBoson/OpenPlanter/blob/main/DEMO.md)).
The following are unverified and gate the integration:

- **No documented machine-readable graph export.** The graph is shown as an in-GUI
  Cytoscape.js viz; no `graph.json`/`entities.json` export format is documented. The entire
  §8 contract (forcing OpenPlanter to `write_file` a graph in OUR shape via `TASK.md`) is the
  workaround — **the local agent must confirm the agent actually obeys the output-path/shape
  instruction**, and if it doesn't, fall back to scanning the workspace / `.openplanter/`
  session artifacts for whatever it did write. *(This is the #1 risk.)*
- **Headless reliability.** `--headless` + `--task` are documented as flags, but whether a
  long `TASK.md` brief is accepted via `--task "@file"` / stdin / inline, and whether the run
  completes unattended without prompting, is unverified.
- **Offline guarantee.** Ollama-local is documented, but whether `web_search`/`fetch_url`
  fully no-op with no `EXA_API_KEY` (vs erroring or stalling) is unverified — critical for the
  "default offline" promise. If unreliable, the offline default becomes `--engine cooccur-only`.
- **No determinism/seed control.** No seed/temperature/determinism flag is documented; the
  `--seed` we expose may be ignored. becky's determinism therefore rests on the **cache replay**
  + the deterministic normalizer, NOT on OpenPlanter being reproducible. Local agent confirms.
- **Entity-resolution quality vs the floor.** Unknown whether OpenPlanter's LLM resolution
  meaningfully beats becky's deterministic co-occurrence floor on this corpus, or just adds
  plausible-but-wrong links. The build plan bakes in a direct A/B (`openplanter` vs
  `cooccur-only`) so this is measured, not assumed. No benchmarks are published.
- **Model/flag drift.** Default models cited (`gpt-5.2`, `claude-opus-4-6`, `llama3.2`, etc.)
  and flag names are from the 2026-06-14 README and may change; re-verify at build time.

---

## 13. Sources (live, verified 2026-06-14)
- OpenPlanter repository (README — interfaces, providers/env vars, tool list, `--workspace`/
  `--task`/`--headless`/`--max-depth`/`--recursive`/`--reasoning-effort` flags, Ollama-local,
  MIT license, `pip install -e .`, Docker, Python 3.10+):
  https://github.com/ShinMegamiBoson/OpenPlanter
- OpenPlanter README (raw): https://raw.githubusercontent.com/ShinMegamiBoson/OpenPlanter/main/README.md
- OpenPlanter DEMO (conceptual workflows; confirms NO documented commands/schemas/export —
  the basis for §12's #1 risk): https://github.com/ShinMegamiBoson/OpenPlanter/blob/main/DEMO.md
- OpenPlanter Releases (pre-built binaries; GUI .msi / CLI):
  https://github.com/ShinMegamiBoson/OpenPlanter/releases/latest
- MarkTechPost launch write-up (recursive sub-agent max-depth 4, ~19 tools, Exa web search,
  shell execution, entity resolution, heterogeneous-dataset ingest, MIT, ~1.4k stars):
  https://www.marktechpost.com/2026/02/21/is-there-a-community-edition-of-palantir-meet-openplanter-an-open-source-recursive-ai-agent-for-your-micro-surveillance-use-cases/
- i10x.ai overview (OSINT framing, entity resolution across CSV/JSON/PDF, Exa search):
  https://i10x.ai/news/openplanter-open-source-ai-agent-osint
- becky governing docs this spec inherits: `FORENSIC-OUTPUT-PHILOSOPHY.md` (corroborate-then-
  conclude; documented vs candidate; plain language), `CLAUDE.md` (single-tool principle;
  offline+deterministic; degrade-never-crash; cloud↔local handoff), `README.md` (tool catalog;
  provenance/capture-time rules), `SPEC-PERSON-CLUSTERING.md` (closest existing tool — the
  post-processing-over-existing-outputs pattern), `SPEC-BECKY-NEW-TOOL.md` §7 (local-only model
  privacy rule for forensic content).
```
