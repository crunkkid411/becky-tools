# SPEC — `becky-quotes` — LLM-curated quote regions as a `_QUOTES.srt` sidecar

> ## STATUS — DESIGN ONLY / PROPOSED (not built)
> Authored 2026-06-16 by the wiki-side agent (Claude) after hand-building a
> non-LLM prototype of the deterministic half of this tool (see §1 Prior art).
> **This spec contains several LOW-CONFIDENCE areas that the build agent MUST
> resolve before/while building — they are collected, blatantly, in
> §13 "Open questions & low-confidence flags." Do not treat this as a finished,
> ready-to-code spec. Do the due diligence in §13 first.**
> Re-verify any model-id / flag / path claim past its cited date before building.

---

## 0. TL;DR

`becky-quotes` reads a video's **full transcript** (`.srt`) and produces a small
**second** `.srt` that contains ONLY the passages an LLM judged important, each
expanded to a self-contained "quote block," timestamped **verbatim** from the
full transcript. A human imports that small `.srt` into a video editor (a script
they already have turns each cue into a timeline **region**) to quickly review /
verify the AI-flagged moments and optionally export clips.

Hard rules from the requester (non-negotiable):

1. **Quotes are selected intelligently by an LLM** — semantic judgment of what
   matters — **NOT** by exact word/phrase search. Exact-phrase search is allowed
   ONLY when the user explicitly asks for it (`--exact`).
2. **Recursive context expansion.** For every selected quote, the LLM must decide
   whether the **sentence immediately before** and the **sentence immediately
   after** should also be included. If YES, include it, then **re-assess** the new
   block's new neighbors. If YES again, repeat. Stop when the answer is NO (or a
   safety cap is hit).
3. **Only the hand-selected quote blocks** go in the output `.srt` — **never the
   whole dialogue.**
4. **Timestamps in the output must be identical** to the timestamps in the full
   `.srt` transcript (copied from real cue boundaries, never invented or rounded).
5. **Output filename = the video filename with `_QUOTES` appended before the
   extension** (e.g. `foo.mp4` → `foo_QUOTES.srt`).
6. **The original video and the original `.srt` transcript are NEVER modified.**
   The `_QUOTES.srt` is an ADDITIONAL artifact, not a replacement for either.

---

## 1. Prior art (reference implementation of the DETERMINISTIC half)

A working, non-LLM prototype of the boundary-mapping / expansion / merge / SRT-emit
machinery already exists and was validated on three multi-hour livestream
transcripts:

`C:\Users\only1\Documents\Obsidian\llm-wiki-CLANCY-TRIAL\tools\triage\make_quote_srt.py`

What it already does correctly and should be PORTED (Go) / reused as the
deterministic core:

- Parses an `.srt` into cues `[{start, end, text}]` (robust to BOM, CRLF, blank lines).
- Normalizes text for matching; builds a `char → cue-index` map so any matched
  span resolves back to exact cue boundaries.
- Emits a region whose `start` = the matched first cue's start and `end` = the
  matched last cue's end — i.e. **timestamps copied verbatim from the full srt**
  (satisfies hard rule #4).
- Caps runaway spans (`--max-region`), de-duplicates, sorts chronologically,
  re-numbers cues, writes a clean SRT (no comment lines inside).
- Uses a stated timestamp only as a **tie-breaker** to disambiguate a recurring phrase.

What it DOES NOT do, and what this tool adds:

- It selected quotes by **exact text match** against a hand-written list. This
  tool replaces that with **LLM semantic selection** (hard rule #1) and the
  **recursive sentence-context expansion** (hard rule #2).

The build agent should treat `make_quote_srt.py` as the trusted spec for the
mapping/snapping/merge/emit behavior and focus net-new effort on the LLM
selection + expansion loop.

## 1a. Where it sits in becky

- Group: **Index/report**, next to `review` ("LLM annotate") and downstream of
  `transcribe`. It consumes a transcript that `transcribe` (or a reused YouTube
  sidecar via `internal/sidecar`) already produced.
- It is a **sharp single-purpose tool**: transcript in → `_QUOTES.srt` + JSON out.
- It MUST obey the toolbox conventions (README "Conventions"): JSON to stdout,
  diagnostics to stderr, exit 0 / nonzero, **offline / local models only**, never
  modify source, degrade gracefully. Binary: `becky-go/cmd/quotes/main.go` →
  `bin/becky-quotes.exe`; add to `build-all-tools.bat`.
  (Tool/binary name `becky-quotes` is a proposal; the spec file is named
  `SPEC-BECKY-SRT-QUOTES.md` per the request. See §13.8 to confirm the final name.)

---

## 2. I/O contract

### Inputs (flags)
| Flag | Required | Meaning |
|---|---|---|
| `--srt <path>` | yes | the FULL transcript `.srt` (read-only source of text + timestamps) |
| `--video <path>` | recommended | used ONLY to derive the output name (and for provenance in JSON). Not read for content. If omitted, derive the name from `--srt` (strip a trailing `.en`/`.srt`). |
| `--out <path>` | no | override the output path (default = §3 naming) |
| `--criteria <text>` / `--criteria-file <path>` | no | what "important" means for THIS run (the selection objective / prompt). See §4. |
| `--exact "<phrase>\|<phrase>..."` | no | OPT-IN literal-search mode (hard rule #1 exception). Disables LLM selection. |
| `--select-from-json <path>` | no | EXTERNAL selection input (escape hatch; see §4.3 and §13.1). |
| `--model <id>` | no | local LLM id (default = the wired local text model; see §4.4) |
| `--max-context-sentences <N>` | no | expansion cap per side (default proposed in §5) |
| `--max-region-seconds <S>` | no | hard cap on a single region's duration |
| `--merge-gap <S>` | no | merge resulting blocks closer than S seconds (§7) |
| `--preserve-cues` | no | emit the original cues unchanged inside each block instead of one merged cue (see §6 + §13.4) |
| `--temperature <t>` | no | default 0 for reproducibility (§5, §13.7) |
| `--log <path>` | no | write the selection/expansion rationale sidecar (NOT inside the srt) |
| `--verbose` | no | stderr progress |

### Outputs
- **Artifact:** `<video-stem>_QUOTES.srt` (§3), written next to the video (or `--out`).
- **stdout (JSON):** a summary the orchestrator can consume, e.g.
  ```json
  {
    "tool": "becky-quotes",
    "srt_in": "...full.en.srt",
    "out": "..._QUOTES.srt",
    "model": "<id-or-exact-or-external>",
    "criteria": "<text or default-id>",
    "regions": [
      {"index":1,"start":"00:13:12,640","end":"00:13:20,560",
       "start_cue":412,"end_cue":417,
       "text":"<verbatim spoken text of the block>",
       "selected_because":"<one-line LLM rationale>",
       "expanded_before":1,"expanded_after":0}
    ],
    "counts": {"selected": 17, "after_merge": 16}
  }
  ```
- **stderr:** diagnostics (silent without `--verbose`).
- **Exit:** 0 success; nonzero on error (missing srt, model unavailable in
  LLM mode with no fallback, etc.). Degrade gracefully per README (a missing
  model → a clear note, not a crash or a fabricated result).

### Invariants (assert in tests)
- The input `.srt` and the `--video` file are **byte-identical** before and after
  the run (sha256 check). The tool opens them read-only.
- Every emitted `start`/`end` value **exists as a real cue boundary** in the input
  `.srt` (membership assertion). Nothing is rounded or synthesized.

---

## 3. Output naming

`<video filename with its extension removed>_QUOTES.srt`, in the video's directory
unless `--out` overrides.

- `2026-06-16_Stream_[abc].mp4` → `2026-06-16_Stream_[abc]_QUOTES.srt`
- `2026-06-15 18-24-32.mp4` → `2026-06-15 18-24-32_QUOTES.srt`

Note / confirm (§13.5): if the transcript is named `<stem>.en.srt`, the output is
still keyed to the **video** stem (`<stem>_QUOTES.srt`), NOT `<stem>.en_QUOTES.srt`.
Strip a trailing `.en` if deriving the stem from the srt name.

---

## 4. Selection — the LLM "brain" (hard rule #1)

This is the heart of the tool and the **least certain** part (see §13.1).

### 4.1 Default mode — intelligent semantic selection
The LLM reads the transcript and selects the passages that matter, judged against a
**criteria** (the selection objective). It does NOT grep for words. Output of this
stage = a set of **anchor sentences** (by sentence index / cue range), each with a
one-line rationale.

### 4.2 Criteria (`--criteria` / `--criteria-file`)
"Important" is domain-specific, so it must be a parameter. Examples:
- Forensic/legal (the originating use case): "Select statements that are evidence
  in a domestic-violence / no-contact-order case: admissions, threats, references
  to the protected person or the order, witness/helper targeting, false-reporting,
  defamation, location/identity intel, independent-crime admissions."
- Generic salience (a podcast/lecture): "Select the most quotable / decision-
  relevant statements; skip filler and small talk."
**There is no safe universal default.** Proposal: require `--criteria`
(or `--criteria-file`); if absent, use a conservative generic-salience prompt AND
print a stderr warning that no criteria was supplied. Confirm in §13.2.

### 4.3 External selection (`--select-from-json`) — escape hatch
A stronger external agent (e.g. a frontier model) supplies the selection as JSON
(anchor cue indices or verbatim quote strings + optional rationale). The tool then
does ONLY the deterministic expansion + snapping + emit. This keeps the rest of the
tool offline/deterministic while side-stepping local-model quality limits (§13.1).
Format proposal:
```json
{"anchors":[{"quote":"<verbatim words>","hint":"00:13:14"}, {"cue":412}]}
```

### 4.4 Model + offline constraint (CRITICAL — read §13.1)
becky is **offline, local-models-only** (README line 1, "no LLM between steps
unless a tool explicitly calls a **local** model"). The wired local text model is
**Qwen3.5-4B via `llama-server`** using the `internal/avlm` transport pattern
(per `SPEC-BECKY-ASK.md` §2.5, dated 2026-06-08 — re-verify the model id/path
before building). Use that transport. **A 4B local model may NOT be good enough
for nuanced selection — this is flagged hard in §13.1.**

### 4.5 Long transcripts (token budget)
Multi-hour transcripts (10k–25k words) will not fit a small local context window.
Proposal: **chunked map-reduce** — slide a window (e.g. 8–12 min of transcript with
1–2 sentence overlap), run selection per window, then merge the per-window anchor
sets (de-dup by cue range). Flag the window size as needing calibration (§13.6).

### 4.6 Exact mode (`--exact`)
Literal phrase search (OR-separated). This is the ONLY non-LLM selection path and
is opt-in. Reuse the `make_quote_srt.py` matching logic for this mode.

---

## 5. Recursive sentence-context expansion (hard rule #2)

Run AFTER selection, per anchor.

1. **Sentence segmentation.** Segment the transcript into sentences (terminal
   punctuation `.?!` with abbreviation guards), and map each sentence to a cue
   range `[first_cue, last_cue]` (a sentence may start mid-cue; keep char offsets).
   Parakeet ASR output carries punctuation/casing, so this is feasible — but it is
   imperfect (run-ons, missing terminals); see §13.3 for the fallback.
2. **Seed** the block with the anchor sentence(s).
3. **Expansion loop:**
   ```
   repeat:
     prev = sentence immediately before the current block (if any)
     next = sentence immediately after the current block (if any)
     ask LLM: "Does PREV add necessary context to understand BLOCK? yes/no"
     ask LLM: "Does NEXT add necessary context to understand BLOCK? yes/no"
     if prev==yes: block = prev + block
     if next==yes: block = block + next
     if neither extended: break
     if block reached --max-context-sentences on a side: stop extending that side
     if block duration >= --max-region-seconds: break
   ```
   The LLM judges each neighbor **freshly against the current (already expanded)
   block** — exactly the "re-assess after each inclusion" the requester described.
4. **Caps (safety).** Proposed defaults (CALIBRATE, §13.6): `--max-context-sentences 4`
   per side, `--max-region-seconds 90`. These prevent a chatty stream from
   expanding a block into the whole dialogue.
5. **Determinism.** `--temperature 0`, fixed seed. Record each yes/no decision
   (+ the model's short reason) to the `--log` sidecar so a human can audit WHY a
   block is the size it is (forensic requirement; see `FORENSIC-OUTPUT-PHILOSOPHY.md`).

---

## 6. Boundary snapping + timestamp identity (hard rule #4)

After expansion, a block is a span of sentences. Convert to cues:
- `start_cue` = the cue containing the block's first spoken word.
- `end_cue`   = the cue containing the block's last spoken word.
- Region `start` = `cues[start_cue].start`; region `end` = `cues[end_cue].end`.

Both values are copied **verbatim** from the full transcript → satisfies hard rule
#4. Sentence boundaries that fall mid-cue are snapped OUTWARD to the enclosing cue
boundaries (never trimmed to a sub-cue time, which would invent a timestamp).

**Region text** = the verbatim concatenation of the spanned cues' text (the actual
spoken words), NOT a paraphrase and NOT the LLM's rewording. This keeps the
displayed quote faithful to the audio.

**Cue representation (default vs `--preserve-cues`):**
- DEFAULT: emit **one merged cue per block** (one timeline region per quote), with
  `start`/`end` as above. This matches "import the quote blocks as a region."
- `--preserve-cues`: instead copy the block's original cues **unchanged** (each line
  byte-identical to the full srt). Choose this if the import script wants the
  original granularity. **Which one the import script expects is unconfirmed — see
  §13.4 / §13.5.**

---

## 7. Overlap / merge / order

After expansion, two anchors near each other may produce overlapping or adjacent
blocks. Merge blocks that overlap or sit within `--merge-gap` seconds (proposed
default 0; set >0 to glue near-neighbors) into a single region. Then sort by
`start` and re-number cues `1..N`. De-duplicate identical regions.

---

## 8. SRT format details

Standard SubRip: integer index, `HH:MM:SS,mmm --> HH:MM:SS,mmm`, text line(s),
blank-line separator. **No comment lines inside the file** (some importers choke).
Provenance/rationale lives in stdout JSON and the optional `--log`, never in the srt.
Default region text is the bare verbatim quote block; `--label-prefix` (off by
default) could prepend a short tag — keep OFF unless requested, since the requester
asked for "only the exact quotes."

---

## 9. CLI examples

```bat
REM intelligent selection with case criteria, default local model
becky-quotes --srt "stream.en.srt" --video "stream.mp4" ^
  --criteria-file "clancy-criteria.txt" --log "stream.quotes.log.json"

REM external (frontier-agent) selection, tool only expands + emits
becky-quotes --srt "stream.en.srt" --video "stream.mp4" ^
  --select-from-json "anchors.json"

REM user explicitly wants literal phrase search
becky-quotes --srt "stream.en.srt" --video "stream.mp4" ^
  --exact "two restraining orders|press charges|copyright office"
```

---

## 10. Algorithm (end to end)

```
1. parse --srt -> cues[]            (reuse internal/sidecar)
2. assert read-only; record sha256 of srt + video
3. segment cues -> sentences[] with cue ranges     (§5.1)
4. SELECT anchors:
     if --exact:            literal match (make_quote_srt.py logic)
     elif --select-from-json: load anchors
     else:                  LLM semantic selection vs --criteria, chunked (§4)
5. for each anchor: recursive sentence-context expansion via LLM yes/no (§5)
6. map each block -> [start_cue,end_cue] -> snap to cue timestamps (§6)
7. merge overlaps/near-neighbors; sort; renumber (§7)
8. write <video-stem>_QUOTES.srt   (verbatim text; standard SRT)
9. print JSON summary to stdout; (optional) write --log rationale
10. re-check sha256 of srt + video unchanged; exit 0
```

---

## 11. Conventions compliance checklist (README "Conventions")
- [ ] JSON to stdout, diagnostics to stderr, exit codes.
- [ ] Offline; the ONLY model call is a **local** llama-server (or none in `--exact`).
- [ ] Source video + source `.srt` never modified (read-only + sha256 guard).
- [ ] Degrade gracefully: local model unavailable in LLM mode → clear stderr note +
      nonzero exit (do NOT silently fall back to a fake selection); suggest `--exact`
      or `--select-from-json` in the message.
- [ ] `cmd/quotes/main.go` imports no sibling cmd; shared logic in `internal/`.
- [ ] Add to `build-all-tools.bat`.

---

## 12. Test plan
1. **Golden transcripts:** the three 2026-06-15 Clancy transcripts already have a
   hand-curated reference (`* - CLAUDE highlighted quotes.srt` in
   `llm-wiki-CLANCY-TRIAL` / `E:\TakingBack2007`). Measure selection overlap vs the
   reference (recall of the known-important lines).
2. **Timestamp identity:** assert every emitted `start`/`end` is a member of the
   full srt's cue-boundary set.
3. **Source integrity:** sha256 of the video and the full `.srt` unchanged.
4. **Expansion:** craft a short transcript where a quote is unintelligible without
   the prior sentence; assert the tool includes exactly that prior sentence and
   stops (no runaway).
5. **Merge:** two anchors 1 cue apart → one region.
6. **`--exact`:** literal matches only; no LLM call.
7. **Reproducibility:** same inputs + `--temperature 0` → identical output twice.
8. **Naming:** `foo.mp4` → `foo_QUOTES.srt`; `.en.srt` input does not produce
   `.en_QUOTES.srt`.

---

## 13. Open questions & LOW-CONFIDENCE FLAGS (build agent: resolve these FIRST)

**These are stated bluntly because the requester asked for blunt honesty where I'm
unsure. Do not skip them.**

1. **LLM selection quality vs the offline/local-model rule — the biggest risk.**
   becky forbids cloud LLMs. The wired local model is ~4B (Qwen3.5-4B). I am **not
   confident a 4B local model selects legal-significance quotes from hours of
   transcript as well as a frontier model** — and the originating use case is
   evidentiary, where bad selection is costly. Build agent MUST: (a) benchmark a
   local instruct model on a golden transcript vs the hand-curated reference;
   (b) consider the largest local instruct model that fits the box; and (c) keep the
   `--select-from-json` escape hatch first-class so a stronger external agent can do
   the selection while the tool stays offline for everything else. **Confirm with the
   user whether "selected intelligently by an LLM" permits a local model, or whether
   selection is expected to be fed in from a stronger model.**
2. **Default "importance" criteria is undefined for a general tool.** I propose
   requiring `--criteria`. Confirm the desired default behavior when none is given.
3. **Sentence segmentation over ASR text** (run-ons, missing terminal punctuation,
   `...`, digits). Reliability is unproven. Propose: punctuation segmenter with
   abbreviation guards; **fallback to cue-level expansion** (treat each cue as the
   unit) if sentence detection is poor. Confirm acceptable.
4. **"Timestamps identical to the full srt" — interpretation.** I implement
   `start = first-cue.start`, `end = last-cue.end` (both copied verbatim). Confirm
   this satisfies the requirement, vs. needing every original cue **preserved**
   inside the block (`--preserve-cues`).
5. **The user's timeline-import script is unknown to me.** I don't know whether it
   expects one cue per region, uses cue TEXT as the region label, has a max text
   length, or tolerates overlapping cues. **Get the import script (or its format
   requirements)** so the emitted SRT imports cleanly. This also settles §13.4.
6. **Expansion caps + chunk window sizes are guesses** (`max-context-sentences 4`,
   `max-region-seconds 90`, ~8–12 min window). Calibrate on real transcripts.
7. **Forensic determinism.** LLM selection is inherently non-deterministic; for
   evidence work that is a hazard. I recommend `--temperature 0` + fixed seed + a
   rationale `--log`. Confirm this is sufficient, and whether the selection itself
   must be reproducible run-to-run (it may not be, across model/driver updates).
8. **Tool name.** I propose `cmd/quotes` → `bin/becky-quotes.exe`; the spec file is
   `SPEC-BECKY-SRT-QUOTES.md` per request. Confirm the final binary name (e.g.
   `becky-quotes` vs `becky-srt-quotes`).
9. **Overlap with `becky-review` ("LLM annotate").** Check whether selection should
   be a mode of `review` rather than a new binary, to avoid two tools that both call
   a local LLM over a transcript. I lean toward a dedicated tool (single purpose,
   clean I/O) but the build agent should confirm against the current `cmd/review`.

---

## 14. Non-goals
- Not a transcriber (consumes an existing `.srt`).
- Not a clipper/exporter (it emits regions; `becky-cut`/`export` or the user's
  editor handles actual media export).
- Does not rewrite, summarize, or paraphrase quotes — region text is verbatim.
- Does not edit the source video or the source transcript, ever.
