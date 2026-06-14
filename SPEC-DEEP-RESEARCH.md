# SPEC-DEEP-RESEARCH.md — `becky-research` — the cited deep-research harness

> **SPEC — NOT BUILT, AWAITING JORDAN'S APPROVAL.**
> Research + design only. No Go code has been written. No new binary exists. Nothing
> in `becky-go/` has been changed. Jordan approves before any build starts.
>
> Authored 2026-06-14. Every architecture/library/pattern below was checked against
> current (2026-06) deep-research agents — GPT-Researcher, LangChain Open Deep
> Research, Stanford STORM, the local llama.cpp+SearXNG agents, and the 2026
> citation-faithfulness papers — with sources cited inline. Where I could not confirm
> something is current, I say so.

---

## 1. What this is and where it sits

`becky-research` is **one tool that does ONE thing: turn a research question (or a
pile of dropped URLs/papers) into a single, fully-cited report** — every claim tied to
a captured source, conclusions stated only when corroborated. It joins the
**Utility/meta** row of the catalog next to `web2md`, `eval`, and `new-tool`. It is a
sibling of `becky-new-tool`, not part of it: `new-tool` *builds a becky tool*;
`becky-research` *answers a question with citations*. Keep them separate (single-tool
principle) — `becky-new-tool`'s S2 "research" stage may later shell out to
`becky-research`, but neither absorbs the other.

It is the becky house pattern applied to the web: a **deterministic Go orchestrator**
owns planning, fan-out scheduling, dedup, RRF ranking, source caching, citation
assembly, verification bookkeeping, and report rendering; a **thin Python/LLM helper
stub** owns only the model calls and a pluggable `search`/`fetch` backend. The network
is treated exactly like ffmpeg or a model weight in the rest of becky: an **explicit,
logged, cached step** — never an ambient runtime dependency.

The forensic angle is real, not decorative. Jordan's corpus work constantly needs
"what is the current state of X" answered *with sources he can show*, and his tools
need to know when an upstream model/library they depend on has shipped a new version.
`FORENSIC-OUTPUT-PHILOSOPHY.md` governs the output: a corroborated finding is stated
plainly with its citations; a lone weak source is a `candidate`, never asserted.

---

## 2. The two use cases this must serve

### 2a. Reading-list synthesizer (the iPhone-reading-list case)
Jordan drops in a set of URLs / arXiv links / PDFs — e.g. an AI-paper reading list
pulled off his iPhone (this dovetails with the planned "iPhone-history research
ingester"). `becky-research` fetches and caches each, deep-reads them, and produces:
- a synthesized brief over the whole list (themes, agreements, contradictions),
- per-source one-line "what's new here," and
- a **self-upgrade flag stream**: when a source describes a newer version of a model
  or library becky already uses (Parakeet, InsightFace, sherpa-onnx, CAM++,
  Qwen3/Qwen3-VL, Gemma-4, llama.cpp, sqlite-vec, auto-editor), it emits a
  `becky_upgrade` finding — "the OCR/VLM model we use just shipped vN; here's the
  source + what changed + which tool it touches." This is the becky-specific value the
  generic agents don't have: it knows becky's own dependency surface (read from a
  small bundled `internal/research/becky_deps.json` manifest) and watches for it.

### 2b. Autonomous topic/entity research → one cited report
Given a plain question ("what's the best local speaker-diarization model in 2026 for
8 GB VRAM?" or "who is <entity> and what's the public record"), the tool plans
sub-questions, fans out searches, fetches + caches the best sources, verifies each
drafted claim against its captured source, and renders one report with a numbered
source list — the GPT-Researcher / Open Deep Research / STORM shape, but offline-
reproducible and forensic-honest.

Both cases run the **same engine**; 2a seeds the source set from the user's URLs and
skips the search-planning fan-out, 2b generates it. One tool, two entry modes.

---

## 3. The pipeline (deterministic stages; a model is called only inside named stages)

Control flow is plain Go over a resumable run-state — **no LLM decides what happens
next** (the becky-new-tool discipline). The widely-used loop in the field is
"plan → fan-out search → fetch → (read/extract) → rank/dedup → draft → **verify** →
synthesize → cite" (GPT-Researcher's planner/executor/publisher; LangChain's
supervisor/sub-agent with a per-URL citation-verification pass; STORM's
perspective-question pre-writing then cited writing). becky implements that as fixed
stages with the heavy reasoning pushed to the helper and everything else deterministic.

```
                         becky-research  (orchestrator: deterministic state machine)
                         run dir: <out>\.research\<slug>-<YYYY-MM-DD>\
                         run-state: state.json  (single source of truth, resumable)

  R1 PLAN ───────────► R2 FAN-OUT SEARCH ──► R3 FETCH + CACHE ──► R4 RANK + DEDUP
  (helper: question      (det scheduler over     (det: pluggable      (det: RRF over
   -> sub-questions       N sub-questions ×       fetch backend,       per-subquestion
   + search queries;      M queries; bounded      content-hash to      result lists +
   2a: skip, use the      concurrency, fixed      cache/<sha256>.json,  vec/FTS rerank,
   dropped URLs)          order)                  never re-fetch)       near-dup collapse)
        │                                                                     │
        ▼                                                                     ▼
  R5 READ + EXTRACT ──► R6 DRAFT ──────────► R7 VERIFY ───────────► R8 SYNTHESIZE
  (helper: per-source    (helper: claims      (det + helper: every    (det assembly +
   evidence snippets,     each tied to a       claim re-checked         helper synthesis
   each with a source      [n] source id        against its cited       OVER VERIFIED
   id + char span)        from R5 only)        snapshot; entailment    claims only)
        │                                       judge = supports/                │
        │                                       partial/unsupported)             ▼
        │                                            │                    R9 RENDER
        └──── unsupported claims dropped or ◄────────┘                    (det: report.md +
              demoted to "candidate"                                       findings JSON,
                                                                           numbered cites)

  Legend:  (det) = Go only, no model      (helper) = one call into the Python/LLM stub
```

Stage detail (what / who runs it / why it can or can't be deterministic):

- **R1 PLAN** · helper. Turn the question into ≤K sub-questions + per-sub-question
  search queries (case 2b). Case 2a skips R1–R2 and loads the user's URLs as the
  source set. Bounded (`--max-subquestions`, default 5; `--max-queries-per` default 3)
  to stop the fan-out exploding — the field's standard guardrail (LangChain caps
  sub-agents and rounds; the 2026 faithfulness paper shows *more* search depth *lowers*
  factual accuracy, so we cap deliberately, not just for cost).
- **R2 FAN-OUT SEARCH** · det scheduler + pluggable `search` backend. Issues every
  query in a **fixed, sorted order** with bounded concurrency; collects ranked result
  lists. The backend is one function (see §5) — SearXNG/Tavily/Brave/local — chosen by
  flag. Offline run with a pre-seeded cache skips live search with a note.
- **R3 FETCH + CACHE** · det + pluggable `fetch` backend. For each unique URL, fetch
  once, store the **raw captured snapshot** as `cache/<sha256-of-url>.json`
  (`{url, fetched_at, http_status, content_sha256, title, text, raw_html_path}`).
  Already-cached URL → reused, never re-fetched. This cache IS the determinism anchor
  (§4). Reuses the existing `web2md` fetch/clean path where possible.
- **R4 RANK + DEDUP** · det. Merge the per-sub-question result lists with **Reciprocal
  Rank Fusion** — the exact algorithm `becky-search` already runs (FTS5+vec+OCR RRF in
  `cmd/search`); reuse it, don't reinvent. Collapse near-duplicate captures by
  `content_sha256` (exact) + a cheap shingle/MinHash similarity (near-dup), keeping the
  highest-ranked representative. Dedup-before-draft saves tokens and stops one source
  being cited five ways.
- **R5 READ + EXTRACT** · helper. Per top-ranked source, extract evidence snippets,
  each carrying its **source id + character span** into the cached text. No snippet =
  no citable evidence. (becky already has `embed`/Qwen3 vectors — optional vec rerank
  of snippets reuses that.)
- **R6 DRAFT** · helper. Compose claims; **every claim must reference a `[n]` source id
  drawn only from R5 snippets.** A claim with no snippet id is rejected by R7 by
  construction.
- **R7 VERIFY** · det bookkeeping + helper entailment judge. The load-bearing,
  anti-hallucination stage. For each drafted claim, re-present the claim **with its
  cited snapshot text** and get a verdict: `supports` / `partial` / `unsupported`.
  This directly answers the 2026 finding that deep-research agents keep link-validity
  >94% and relevance >80% **yet only 39–77% factual accuracy** — a working, relevant
  link is *not* support. We separate "the link works" (det HTTP check in R3) from "the
  source actually backs this claim" (R7), exactly the split that paper recommends.
  `unsupported` claims are **dropped**; `partial`/single-source claims are demoted to
  `candidate`. Corroboration = ≥2 independent verified sources → `corroborated`.
- **R8 SYNTHESIZE** · det assembly + helper. Synthesis may add **nothing not present in
  a verified claim** (the same rule that fixed `becky-validate`'s two-stage captioning:
  the synthesizer combines, it does not invent). Self-upgrade flags (2a) are matched
  here against `becky_deps.json`.
- **R9 RENDER** · det. Emit `report.md` (numbered inline `[n]` citations + a source
  table with url/title/fetched_at/sha) and the findings JSON (§4). Pure templating.

---

## 4. CLI contract + JSON schema (becky house style)

```
becky-research "<question>"                         # case 2b: autonomous topic research
becky-research --urls urls.txt                      # case 2a: synthesize a reading list
becky-research --urls-stdin                         #   (one URL/path per line on stdin)

Options:
  --out <dir>                 run dir (default <cwd>\.research\<slug>-<date>)
  --search <backend>          searxng|tavily|brave|local|none   (default from config)
  --fetch  <backend>          web2md|raw|none                   (default web2md)
  --model  <id>               local synthesis model (default: llama-server Qwen3 per config)
  --max-subquestions <n>      R1 cap (default 5)
  --max-queries-per <n>       R2 cap per sub-question (default 3)
  --max-sources <n>           hard cap on fetched URLs (default 25)
  --offline                   no live search/fetch; use only the cached snapshot
  --self-upgrade on|off       2a becky-dependency watch (default on)
  --deps <file>               override bundled internal/research/becky_deps.json
  --format md|json|both       (default both: report.md + stdout JSON)
  --resume | --from <stage> | --force <stage>
  --verbose                   stage headlines to stderr
# stdout: the findings JSON (machine-readable). stderr: plain-English stage headlines.
# report.md is written to the run dir. Source inputs are never modified.
```

**Exit codes:** `0` = report produced (possibly partial + degraded, with a note);
`2` = bad invocation (no question and no `--urls`); `3` = hard failure with nothing
salvageable (and even then JSON carries the degrade reason — never a panic, never
half-JSON).

**Findings JSON (synthetic values; follows `FORENSIC-OUTPUT-PHILOSOPHY.md`):**
```json
{
  "tool": "becky-research v1.0.0",
  "mode": "reading-list",
  "question": "synthesize this AI-paper reading list",
  "run_dir": ".research/ai-reading-2026-06-14",
  "deterministic_over_snapshot": true,
  "snapshot_sha256": "tree-hash-of-cache-dir",
  "findings": [
    {
      "claim": "Qwen3-VL added llama.cpp GGUF vision support on 2025-10-30.",
      "status": "corroborated",                 // corroborated | candidate | unknown
      "confidence": 0.91,
      "verify": "supports",                      // R7 entailment verdict
      "cites": [3, 7],                           // ≥2 independent => corroborated
      "basis": "two independent sources agree; both re-checked against captured text"
    },
    {
      "claim": "Model X reaches 95% on benchmark Y.",
      "status": "candidate",
      "confidence": 0.44,
      "verify": "partial",
      "cites": [5],
      "basis": "single source; number stated in abstract, not corroborated — verify before trusting"
    }
  ],
  "becky_upgrades": [
    {
      "component": "InsightFace buffalo_l (used by becky-identify, becky-events)",
      "current_in_becky": "buffalo_l",
      "found": "buffalo_l2 released 2026-05",
      "status": "candidate",
      "cites": [11],
      "recommendation": "evaluate before adopting; affects internal/faceembed"
    }
  ],
  "sources": [
    {"id": 3, "url": "https://...", "title": "...", "fetched_at": "2026-06-14T09:12:00Z",
     "content_sha256": "ab12...", "cache_file": "cache/ab12...json", "link_ok": true}
  ],
  "dropped_claims": [
    {"claim": "...", "verify": "unsupported", "reason": "cited source does not state this"}
  ],
  "notes": {
    "degrade": null,
    "honesty": "every claim is tied to a captured source; candidates are NOT conclusions"
  }
}
```
Discipline: corroborated findings are stated plainly **by their conclusion**; a lone
verified source is a `candidate`; an unsupported draft claim is **dropped and listed in
`dropped_claims`** so the verification is auditable (the becky norm of showing the
basis, like `becky-validate` keeping per-frame captions). No claim ever appears without
a `cites` entry that resolves to a cached snapshot.

---

## 5. Architecture as becky layers — and the helper stub contract

**Deterministic Go orchestrator (`becky-go/cmd/research/` + `internal/research/`):**
owns everything that must be reproducible and testable without a model or network —
the stage state machine + resumable `state.json`; the fan-out **scheduler** (fixed
order, bounded concurrency); **RRF ranking** (reuse `cmd/search`'s RRF) and **dedup**
(content-hash + shingle/MinHash near-dup); the **source cache** (`cache/<sha>.json`,
write-once, content-addressed); **citation assembly** (stable `[n]` numbering, one
number per unique URL across all sub-questions — LangChain's exact rule); the
**verification bookkeeping** (which claim → which snapshot → which verdict); and the
**renderer** (report.md + findings JSON templating). The `becky_deps.json` match for
self-upgrade flags is deterministic string/semver matching in Go.

**Thin Python/LLM helper stub (`internal/pyhelpers/research_helper.py`):** owns ONLY
the model + network touch points, materialized and run as a subprocess exactly like
`face_embed.py` / `avlm`. It exposes **four pure functions** the local agent plugs into
real backends. JSON in on stdin, JSON out on stdout, one call per line of work:

```jsonc
// op: "plan"     in: {question, max_subquestions, max_queries_per}
//                out: {subquestions:[{q, queries:[...]}]}
// op: "search"   in: {query, k}            // PLUGGABLE search backend
//                out: {results:[{url, title, rank, snippet}]}   // ranked, rank=1 best
// op: "fetch"    in: {url}                 // PLUGGABLE fetch backend (or reuse web2md)
//                out: {url, http_status, title, text, content_sha256}
// op: "extract"  in: {question, source_id, text}
//                out: {snippets:[{source_id, span:[start,end], text}]}
// op: "draft"    in: {question, snippets:[{source_id, span, text}]}
//                out: {claims:[{claim, cites:[source_id...]}]}   // cites MUST be from input
// op: "verify"   in: {claim, snapshot_text}
//                out: {verdict:"supports|partial|unsupported", why}
// op: "synthesize" in:{question, verified_claims:[...]}
//                out: {report_sections:[{heading, body_with_[n]_cites}]}
```

The contract is precise so the **local agent only wires two things**: (1) point
`plan/extract/draft/verify/synthesize` at the local synthesis model
(`llama-server` Qwen3, per `~/.becky/config.json` — the embedding/synthesis runner
becky already runs); (2) point `search`/`fetch` at a real backend (SearXNG self-host is
the offline-friendly default — verified as the standard local-deep-research pairing in
2026; Tavily/Brave for hosted). Everything else is Go the cloud agent already unit-
tested. Heavy lifting stays in the stub; the orchestrator never embeds model logic.

---

## 6. Determinism — "same snapshot → same report"

becky's invariant is offline + deterministic at runtime. A research tool needs the web,
so we **localize the non-determinism to one explicit, logged stage (R2/R3) and make
everything after it deterministic over the captured snapshot:**

- **Capture once, then freeze.** R3 writes every fetched page to a content-addressed
  cache and records a `snapshot_sha256` = a Merkle/tree hash over the whole cache dir.
  R4–R9 read **only** the cache, never the live web.
- **Re-run = reuse.** `--resume` and a populated cache reproduce byte-identical ranking,
  dedup, citation numbering, and (with `temperature 0` + fixed seed on the local model)
  the same synthesis. `--offline` enforces this: it *forbids* any live call and runs
  purely over the snapshot, so an audited report can be regenerated later from its own
  captured corpus — the forensic provenance bar (you can answer "what did you read?"
  with the exact bytes).
- **Fixed seeds + greedy decode** on every helper model call (the becky-wide rule).
  Determinism holds *over a snapshot*; a fresh live fetch may differ because the web
  changed — and that difference is visible as a new `snapshot_sha256`, not silent.

---

## 7. Degrade, never crash

Every failure path yields a typed degrade note + a partial result, exit 0:
- **No network / search backend down** → if cache present, run offline over it with
  `degrade:"no-network; used cached snapshot"`; if no cache, emit an empty-but-valid
  findings JSON with `degrade:"no-network; nothing cached"` and exit 0.
- **No local model** → R1/R5–R8 can't reason; still return the fetched, ranked,
  deduped sources as `candidate` raw material with `degrade:"no-model; sources only"`.
- **Some URLs 404 / paywalled** → mark those `link_ok:false`, drop their would-be
  claims in `dropped_claims`, continue with the rest.
- **Empty results** → valid JSON, empty `findings`, `degrade:"no-results"`, exit 0.
- Never a panic, never half-written JSON, never a fabricated source to fill a gap.

---

## 8. Build plan — cloud vs local split (the model boundary)

**CLOUD agent can build + unit-test NOW (all deterministic Go, no model/network):**
- `cmd/research/` state machine over `state.json` (resume / from / force), CLI flags,
  exit codes, stdout/stderr discipline.
- `internal/research/`: the fan-out **scheduler** (fixed order, bounded concurrency —
  test with a fake search backend); **RRF + dedup** (reuse `cmd/search` RRF; unit-test
  rank fusion + content-hash + shingle near-dup on fixtures); the **content-addressed
  cache** (write-once, `snapshot_sha256` tree hash — test reproducibility); **citation
  assembly** (stable `[n]` numbering, one-per-URL — golden tests); **verification
  bookkeeping** + status logic (`supports/partial/unsupported` → `corroborated/
  candidate/unknown`; ≥2-independent rule — pure-function tests); the **renderer**
  (report.md + findings JSON golden tests); `becky_deps.json` + the self-upgrade semver
  matcher (table-driven tests). Use `internal/pathx` for any path basename (Windows
  paths reach CI).
- A **fake helper** (records ops, returns canned JSON) so the whole pipeline runs in CI
  with no model and no network — proving the deterministic layer end-to-end.

**LOCAL agent wires (the model + network boundary):**
- Implement `research_helper.py`'s `plan/extract/draft/verify/synthesize` against the
  resident `llama-server` Qwen3 (greedy, fixed seed) per `~/.becky/config.json`.
- Implement the pluggable `search`/`fetch`: stand up SearXNG locally (default) and/or
  wire Tavily/Brave; reuse `web2md` for `fetch`. Confirm the four-op JSON contract.
- Run the two real cases end-to-end (a real iPhone reading list; one autonomous topic),
  tune `--max-*` caps and the verify prompt on real output, confirm `--offline` re-run
  reproduces the report from the snapshot. Report what degraded.

---

## 9. Open decisions for Jordan
1. **Search backend default.** SearXNG self-host (offline-friendly, private — best for
   forensic) vs a hosted API (Tavily/Brave — easier, but a network dependency and a
   third party sees the queries). Recommend **SearXNG default, hosted opt-in**.
2. **Live vs cached-deterministic default.** Should a bare `becky-research "..."` always
   fetch live (freshest) or prefer an existing snapshot (reproducible)? Recommend
   **live by default, `--offline` to freeze**, with the `snapshot_sha256` always
   printed so freshness is explicit.
3. **Self-upgrade aggressiveness.** How loud should `becky_upgrades` be — flag only
   confirmed new releases of becky's exact deps, or also "an alternative model now beats
   what we use"? Recommend **start narrow (exact-dep new versions only), as `candidate`
   until corroborated**, to avoid a flood of "you could switch to X" noise.
4. **Verify model = synthesis model, or a second one?** Using a *different* local model
   as the entailment judge (cf. becky's "second-AI" habit) catches more, but costs a
   second model load. Default: same model, `temperature 0`; second-model judge opt-in.
5. **Hosted-model escalation.** If the local model's synthesis is weak on a hard topic,
   may R8 escalate to a hosted model (privacy cost) or stay strictly local? Default
   **strictly local**; escalation opt-in and never for sensitive/forensic content.

---

## Sources (verified 2026-06-14)
- [GPT-Researcher — planner/executor/publisher, parallel sub-question crawl, dedup + source tracking, local docs + MCP (GitHub README)](https://github.com/assafelovic/gpt-researcher)
- [LangChain Open Deep Research — supervisor/sub-agent fan-out, search→read→reflect loop, one-citation-number-per-URL, sub-agent/round caps, pluggable models+tools/MCP](https://docs.langchain.com/oss/python/deepagents/deep-research)
- [LangChain blog — Open Deep Research (bring-your-own model/search/MCP, LangGraph)](https://www.langchain.com/blog/open-deep-research)
- [Stanford STORM — multi-perspective question-asking pre-writing → cited writing; citation precision/recall (GitHub)](https://github.com/stanford-oval/storm)
- ["Cited but Not Verified" — link validity >94% & relevance >80% but only 39–77% factual accuracy; separate link-works / relevant / fact-check; AST citation parsing; more search depth lowers accuracy (arXiv 2026)](https://arxiv.org/html/2605.06635v1)
- [ReportBench — statement vs citation hallucination failure types (arXiv 2025/2026)](https://arxiv.org/html/2508.15804v1)
- [CiteCheck — retrieval-grounded detection of citation hallucinations (arXiv 2026)](https://arxiv.org/html/2605.27700v1)
- [Detecting/correcting reference hallucinations in commercial LLMs + deep research agents (arXiv 2026)](https://arxiv.org/html/2604.03173v1)
- [local-deep-research — llama.cpp/Ollama + 10+ search engines (arXiv/PubMed/private docs), ~95% SimpleQA local (GitHub)](https://github.com/LearningCircuit/local-deep-research)
- [deep_research_protocol — local LLM + web search via llama.cpp + SearXNG (GitHub)](https://github.com/jwest33/deep_research_protocol)
- [Reciprocal Rank Fusion for hybrid/multi-query retrieval + dedup-before-context (Elasticsearch / OpenSearch refs)](https://www.elastic.co/docs/reference/elasticsearch/rest-apis/reciprocal-rank-fusion)
- In-repo reuse: `becky-go/cmd/search` (FTS5+vec+OCR **RRF** — reuse for R4), `internal/pyhelpers` (embedded-Python subprocess pattern), `web2md` (fetch/clean for R3), `embed` (Qwen3 vectors for snippet rerank), `~/.becky/config.json` (llama-server model), `FORENSIC-OUTPUT-PHILOSOPHY.md`, `SPEC-BECKY-NEW-TOOL.md` (the deterministic-orchestrator + helper-stub house pattern).
