# SPEC-BECKY-EYES.md — `becky-eyes` — frontier vision on frames becky already picked

> **SPEC — NOT BUILT, AWAITING JORDAN'S APPROVAL.**
> Research + design only. No Go code has been written. No new binary exists. Nothing in
> `becky-go/` has been changed.
>
> Authored 2026-08-17. Rests entirely on **`research/qwen38-max-video.md`** — read that
> first; every capability, price and limit below is cited there. Where that research says
> *candidate*, this spec says *verify before building*.

---

## 1. What this is, and why it is one tool

**One job: a batch of frames in → a grounded description of what is visible out.**
No frame selection (callers already do that), no indexing (`becky-embed` does that), no
retrieval (`becky-search` does that), no conclusions (`FORENSIC-OUTPUT-PHILOSOPHY.md`
forbids them from one signal).

It exists because becky's vision layer is the weak link and every other piece of the
"video memory graph" is already built (`research/qwen38-max-video.md` §2). `becky-validate`
tops out at Gemma-4 12B on an 8 GB RTX 3070; `SPEC-VIDEO-ANALYSIS.md` §2 established there
is no better local option at that size, and the open Qwen3.8 *vision* weights are 27B — out
of reach. So the upgrade is necessarily hosted, and the cheapest hosted route is a
subscription Jordan already pays for.

**Why not fold it into `becky-validate`?** Because `becky-validate` is audio-visual
cross-modal analysis of *short flagged clips* with a local model. `becky-eyes` is a
high-volume, credit-metered, network-touching describer for *long sweeps*. Different
failure modes, different cost model, different dependency. Single-tool principle: separate
binaries. They connect at one seam — `becky-validate --backend qoder` delegates to
`becky-eyes` for the visual half (§7).

**Where it sits:** `becky-clip`/`becky-motion` pick windows → **`becky-eyes` describes them**
→ `becky-embed` vectorises the descriptions → `becky-search` retrieves them. That chain is
the searchable video database from the Qwen3.8-Max announcement, owned by becky, offline
after the describe step, and auditable.

---

## 2. CLI contract (becky house style)

```
becky-eyes <video> [options]                    # sweep a file
becky-eyes --frames <dir> [options]             # describe an existing frame dir
becky-eyes --windows <json> [options]           # only the windows another tool flagged

Selection:
  --start <ts>            start (hh:mm:ss or seconds; default 00:00:00)
  --end <ts>              end (default: end of file)
  --fps <float>           sample rate (default 0.1 = 1 frame / 10 s)
  --batch <n>             frames per model call (default 20, hard max 20)
  --width <px>            downscale longest edge (default 640; drives token cost)

Model + money:
  --backend <b>           qoder (default) | modelstudio | mock
  --model <id>            qwen3.7-plus (default) | qwen3.8-max | <any --list-models id>
  --escalate <id>         second-pass model for low-confidence windows (default: none)
  --off-peak <mode>       require (default) | prefer | ignore
  --max-credits <n>       hard ceiling for this run; refuse to start past it (default 50)
  --i-am-paying           REQUIRED for any pay-per-token backend. Without it, refuse.

Output:
  --db <path>             also write descriptions into beckydb for becky-embed
  --cache <dir>           response cache (default <video>.eyes/); keyed by frame-set hash
  --format json|txt       (default json)
  --prompt <str>          override the built-in forensic describe prompt
  --verbose               stage headlines to stderr
```

**Exit codes:** `0` = descriptions produced (including partial/degraded, with a note);
`2` = bad invocation; `3` = nothing salvageable — and even then valid JSON carrying the
degrade reason. Never a panic, never half-JSON.

**stdout** = the findings JSON. **stderr** = plain-English stage headlines. The source video
is never modified.

### 2a. The two guards that are not optional

1. **`--i-am-paying`.** `CLAUDE.md`'s standing invariant, enforced in code exactly like
   `cmd/subtitle/openrouter.go`'s `isFreeModel`: the `modelstudio` backend (and any future
   per-token endpoint) **refuses to send a single request** unless the flag is present.
   Subscription backends (`qoder`) don't need it — that money is already spent.
2. **`--max-credits`.** Estimated before the run from frame count × batch size × the
   measured credits-per-call, and **checked again mid-run**. Over ceiling → stop, emit what
   was completed, `degrade: "credit-ceiling"`, exit 0. A 100-hour sweep must not be able to
   silently eat a month's quota (`research/qwen38-max-video.md` §3c).

`--off-peak require` (the default) refuses to run outside **14:00–00:00 UTC** and says when
the window opens, in plain language. That is a 4–10x price difference, not a nicety.

---

## 3. Output JSON (synthetic values)

```json
{
  "tool": "becky-eyes v1.0.0",
  "source": "C:\\footage\\stream-2026-06-11.mp4",
  "backend": "qoder", "model": "qwen3.7-plus",
  "sampled": {"fps": 0.1, "width": 640, "batch": 20, "frames": 1080},
  "cache": {"dir": "stream-2026-06-11.eyes", "hits": 41, "misses": 13},
  "cost": {"calls": 54, "credits_estimated": 6.5, "off_peak": true},
  "windows": [
    {
      "start": 860.0, "end": 1060.0,
      "frames": ["f0087.jpg", "..."],
      "frame_set_sha256": "9c1f...",
      "description": "Two people stand in a doorway. The person in the red jacket steps
                      forward twice; the other moves back and to the left out of frame at
                      about the fourteenth frame.",
      "observed": ["doorway interior", "two people", "one steps forward", "one moves away"],
      "confidence": 0.62,
      "status": "candidate",
      "basis": "single hosted-model description; NOT corroborated by identify/presence",
      "disclaimer": "AI ANALYSIS - candidate, not conclusion"
    }
  ],
  "notes": {
    "degrade": null,
    "honesty": "descriptions are ONE signal. A description is never presence and never a name."
  }
}
```

**Every window is `candidate` on exit. `becky-eyes` cannot emit `corroborated`** — it has
exactly one signal by construction. Promotion happens upstream when `becky-identify` /
`becky-presence` / the transcript agree, per `FORENSIC-OUTPUT-PHILOSOPHY.md`. This is the
load-bearing design decision: it makes it *structurally impossible* for a hosted model's
prose to become a timeline entry on its own, which is the exact failure mode `CLAUDE.md`
records from 2026-06-24.

---

## 4. Architecture — becky layers

**Deterministic Go (`cmd/eyes/` + `internal/eyes/`)** — everything testable with no network
and no model: frame selection maths (timestamp → frame index → file, fps/width/batch
arithmetic); batching into ≤20-frame windows with stable ordering; the **frame-set hash**
(sha256 over frame bytes + prompt + model id) that keys the cache; the cache itself
(write-once, content-addressed); the credit **estimator and ceiling**; the off-peak clock
check; response parsing → the JSON above; the `--db` write into `beckydb`; the degrade
ladder. Use `internal/pathx` for basenames — Windows paths reach CI.

**Thin backend shim (`internal/eyes/backend_qoder.go`)** — owns only the subprocess. One
exec, verified shape from `research/qwen38-max-video.md` §4:

```
qoder --print --output-format json --model <id> \
      --attachment <f1> ... --attachment <f20> \
      --no-session-persistence --disallowed-tools "Write,Edit,Bash" \
      "<prompt>"
```

`--disallowed-tools` matters: we are borrowing a *coding agent* as an eyeball, and it must
not touch the repo while doing it. Identical in kind to `becky-vision` shelling out to
`llama-mtmd-cli` — no new pattern invented.

**`internal/eyes/backend_modelstudio.go`** — OpenAI-compatible POST, image-list mode with
the `fps` hint, copying `cmd/new-tool/cheap.go`'s client pattern (never a new HTTP client).
Gated behind `--i-am-paying`.

**`internal/eyes/backend_mock.go`** — canned responses so the whole pipeline runs in CI with
no network and no credits. This is what makes gates 1–4 meaningful for a network tool.

---

## 5. Determinism — "same frames → same descriptions"

becky is fixed-seed reproducible; a hosted model is not, and Qoder exposes no
seed/temperature control. Same resolution as `SPEC-DEEP-RESEARCH.md` §6: **localise the
non-determinism to one logged stage and make everything after it deterministic over the
captured snapshot.**

- Frame selection is pure arithmetic — identical every run.
- Every model response is written to `--cache` keyed by `frame_set_sha256`. A re-run with a
  warm cache is **byte-identical and costs zero credits**.
- The cache is the audit trail: "what did the model actually see, and what did it say" is
  answerable from bytes on disk, months later.
- A cold re-run may differ; that difference is **visible** as a cache miss, never silent.

---

## 6. Degrade, never crash

| Failure | Behaviour |
|---|---|
| `qoder` not on PATH / not signed in | `degrade:"no-backend"`, frames still extracted + listed, exit 0 |
| Outside off-peak, `--off-peak require` | refuse before spending; state when the window opens; exit 0 |
| Credit ceiling reached mid-run | stop, emit completed windows, `degrade:"credit-ceiling"`, exit 0 |
| Model returns unparseable output | that window `status:"unknown"`, raw response kept in cache, continue |
| ffmpeg missing | `degrade:"no-ffmpeg"`, exit 0 (`--frames` mode still works) |
| `modelstudio` without `--i-am-paying` | refuse, exit 2, explain in one plain sentence |

---

## 7. The seam to `becky-validate` (add a rung, don't build a ladder)

`becky-validate` already has `--backend gemma4-local | fusion | mock`. Add **`qoder`**: it
shells to `becky-eyes` for the visual half and keeps its own audio/cross-modal logic. No new
escalation protocol, no second confidence scale, no duplicated disclaimer.

Resulting ladder, cheapest first — becky's existing doctrine with two rungs added:

| Tier | Model | Cost | When |
|---|---|---|---|
| 0 | Gemma-4 E4B local | free, offline | default, always first |
| 1 | Gemma-4 12B local | free, offline | E4B unsure |
| 2 | **Qwen3.7-Plus @ Qoder** | ~0.04x off-peak | bulk sweeps; local unsure |
| 3 | **Qwen3.8-Max @ Qoder** | ~0.25x off-peak | the windows that decide something |

---

## 8. Build plan — cloud vs local split

**Cloud can build + unit-test now (no model, no network, no credits):** the whole of
`internal/eyes` — frame-selection maths, batching, frame-set hashing, the write-once cache,
credit estimator + ceiling, off-peak clock, response parsing, JSON rendering (golden tests),
degrade ladder, `--i-am-paying` refusal (assert it never constructs a request), and the
`mock` backend driving the full pipeline end-to-end in CI.

**Local must verify first — and this is step 1, before any Go is written:**

- [ ] **Prove the seam in one command.** Extract 3 frames from any clip, then:
      `qoder --print --output-format json --model qwen3.7-plus --attachment a.jpg --attachment b.jpg --attachment c.jpg "Describe what is visible in these 3 frames."`
      Paste the raw output into `HANDOFF-*.md`. **If this fails, the whole spec is wrong**
      and the fallback is the Agent SDK (`research/qwen38-max-video.md` §4a) or Model Studio.
- [ ] Confirm the real attachment cap (spec assumes 20, from the IDE's documented limit).
- [ ] Confirm `qwen3.8-max` is a valid `--model` id: `qoder --list-models`.
- [ ] **Measure credits per call.** Note the balance, run 10 calls, note it again. Every
      cost number in `research/qwen38-max-video.md` §3c scales off this one measurement.
- [ ] Confirm off-peak billing is visible in the account (14:00–00:00 UTC).
- [ ] Then: wire the real backend, run a 1-hour sweep, check descriptions against footage
      Jordan knows, and report what degraded.

Per `STANDARDS-WORKFLOW.md` §7, cloud cannot ship a "provable handoff" for this one without
a Qoder subscription and a signed-in CLI — **neither exists in the cloud container**. The
`mock` backend is the one-command proof cloud *can* provide, and the checklist above is the
work order. Said plainly rather than dressed up as ready.

---

## 9. Open decisions for Jordan

1. **The ToS question (`research/qwen38-max-video.md` §4b.1).** Bulk video-frame analysis on
   a coding subscription: no source forbids it, none blesses it. Yours to call. Recommend
   **start with one hour of real footage, watch the account, scale up only if clean.**
2. **Default model.** Recommend **Qwen3.7-Plus** (0.04x off-peak — a 100-hour sweep is ~11%
   of a Pro month) with Qwen3.8-Max as `--escalate`. Max-by-default costs ~6x more for
   descriptions that are better only on the hard windows.
3. **Sample rate.** 1 frame / 10 s is the cost-driven default. 1 frame / 5 s doubles both
   fidelity and spend. Recommend **0.1 fps for sweeps, 0.5 fps on escalation**.
4. **Does `becky-eyes` write to `beckydb` by default, or only with `--db`?** Recommend
   **`--db` opt-in** — a describe run and an index write are different decisions.
5. **Documentary vs forensic prompt.** The built-in prompt is forensic (neutral, observational,
   no inference). A documentary sweep might want "what would make this watchable". Recommend
   **forensic default, `--prompt` override**, never two hidden modes.
