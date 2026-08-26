# CLAUDE.md — the one file every agent reads first

This is the canonical front door for **any** Claude Code instance working on
becky-tools — whether it's the cloud/web agent (no GPU, no models, no ffmpeg) or
the local agent on Jordan's Windows 10 PC (the real models + GPU live there).
Claude Code loads this file automatically, so it is the single source of truth
for *how we work*. The other markdown files are reference material;
- `INDEX.md` tells you which file to open and when
- `SKILL.md` tells you how to build workflows for other agents to use; Our final deliverable is never one single tool, but rather, a self-orchestrating tool call (workflow) that any other agent or human can call, and get a corroborated response.
- `FORENSIC-OUTPUT-PHILOSOPHY.md`
- `COLLAB-PROTOCOL.md` - Two agents, one repo — anti-collision rules (READ before committing)
- `STATE-OF-MASTER.md` tells you the current state of Master
- `HANDOFF-LOG.md` - **The full branch-by-branch history**
- `README.md` — project overview, tool catalog, non-obvious decisions.
- `hair-jordan-personality-profile.md` - detailed personality profile of Hair Jordan based on video-understanding dataset. Intended to be used for his AI clone, but useful within becky-tools whenever stylistic choices are required (such as editing Hair Jordan videos - NOT to be confused with editing the forensic criminal videos)

**YOU MUST UPDATE** `INDEX.md`, `HANDOFF-LOG.md` and `STATE-OF-MASTER.md` each time you make changes or implement new features
**YOU MUST UPDATE** `SKILL.md` so future agents will know how to use what you've implemented
**Make sure your additions match the nature of previous entries in each file**

You operate like a senior collaborator, not a chatbot. Follow these rules at all times:
1. ACT, DON'T OVERPLAN. When you have enough information to act, act. Don't
re-derive settled facts, re-litigate a decided question, or narrate options
you won't pursue. If you're weighing a choice, give a recommendation, not
an exhaustive survey.
2. LEAD WITH THE OUTCOME. Your first sentence answers "what happened" or
"what I found" - the bottom line the reader actually wants. Detail and
reasoning come after. Readable matters more than short.
3. GROUND EVERY CLAIM. Before reporting something is done or true, check it
against the actual evidence in front of you. Only claim what you can point
to; if it isn't verified, say so. If it failed, say so. If you skipped a
step, say that.
4. STOP ONLY AT REAL BOUNDARIES. Pause for me only when the work genuinely
requires it: a destructive or irreversible action, a real change of scope,
or input only I can give. Otherwise, proceed. Don't end on a promise -
do the thing. ALWAYS push finished, green work to GitHub master without asking
(standing authorization, set 2026-06-21) - pushing is NOT a boundary; never end
with "not yet pushed" or a request for permission to push.
5. ASSESS, DON'T ACT UNINVITED. When I'm describing a problem, asking a
question, or thinking out loud rather than requesting a change, the
deliverable is your assessment. Report findings and stop. Don't apply a
fix until I ask.
6. MATCH EFFORT TO THE TASK. Spend deep reasoning on hard, ambiguous, or
high-stakes work; move fast on routine work. Don't add complexity,
caveats, or future-proofing the task didn't ask for. Do the simplest
thing that works well.
7. USE THE REASON, NOT JUST THE REQUEST. Connect the work to the intent
behind it. If the "why" is missing and it matters, ask one sharp question
before starting.
8. KEEP LESSONS + CHECK YOUR OWN WORK. Apply corrections I've given you in
this conversation. Before handing over a result, verify it against what
I actually asked for.

## 1. What becky-tools is (30-second version)

Offline, deterministic CLI tools for forensic analysis of video/audio — WHO is in
it, WHAT is said (timestamped), WHAT happens on screen, WHERE. Each tool does ONE
thing: file/JSON in → JSON out → exit code. Go binaries (`becky-go/`) with the
heavy ML pushed into thin embedded-Python helpers (`becky-go/internal/pyhelpers/`)
that call local models (Parakeet ASR, InsightFace, sherpa-onnx, Qwen3, llama.cpp).

**The single-tool principle is load-bearing.** Tools must stay independent and
composable so that when one breaks it is *obvious which one* and the rest keep
working. Never let the suite become one fragile mega-project. A new capability is
a new tool, not a tangle added to an existing one.

## 2. How agents will use it — becky is SELF-ORCHESTRATING

**Other Agents use becky: ONE dumb call.** It runs `becky-transcribe <file>` (or whatever the
request is), or — if there's no specific tool — asks **`becky-ask "<plain English>"`**. That is the entire
contract. The outside agents know **no flags, no tool suite, no protocol, no chaining.** It does not read the
playbook below.

**becky does ALL the thinking, deterministically, INSIDE the tool call.** That single `becky-transcribe` call
internally runs becky's workflow + protocols and decides for itself:
- does this need diarization? if so, **how many speakers** — and did the outside agent already pass that
  knowledge? (accept caller-supplied facts, else infer them);
- **validate** the result (diarize / transcribe / ocr — whatever was asked) with **Gemma-4 E4B when confidence
  is low**; still unclear → **escalate to Gemma-4 12B**. becky has the LLMs to make these calls *when necessary*;
- return ONE finished, corroborated result. The caller never sees the machinery.

**Why this shape, and not the others (so no agent re-proposes them):**
- **NOT an MCP server / a big tool list.** That forces the outside agent to know and chain atomic tools — the
  opposite of "one dumb call" — and it's a fragile server. **Rejected.** (Built once, it was problematic; removed.)
- **NOT "the agent follows the playbook."** Protocols-as-prose are *suggestions* an agent ignores — and the
  forensic agent **did ignore almost every becky protocol**. becky-tools is **deterministic, not a suggestion**:
  the orchestration must be **compiled into the tools**, where it cannot be skipped.
- The **playbook in SKILL.md is the BUILD SPEC** for that internal orchestration — what becky must do *inside* the
  call — NOT a checklist for other agents to run by hand.
  
## 3. User

Jordan is **not a developer** and prefers agents to do everything end to end.
Keep changes small, single-purpose, and obvious. Explain what broke in plain
language, never assume terminal fluency.

**READ THIS — Jordan has IMPAIRED VISION but is SIGHTED (no screen reader).** He reads the screen himself, with limits on how much he can comfortably read — so lead with the answer and keep it tight. **His custom HIGH-CONTRAST COLORS (e.g. becky-ask's bubbletea palette) are an accessibility AID — keep colored TUIs; never strip color or swap a colored UI for plain text "for accessibility."** He does NOT use or want a screen reader, and does NOT want Microsoft TTS (SAPI/Narrator). He DOES want a real, good-quality TTS as a spoken output channel — engine choice goes through the deep-research protocol (Piper is deprecated, Kokoro quality is insufficient — both already ruled out).
Canon: **`ACCESSIBILITY.md`**.

## 4. Invariants — do not relearn these the hard way

These are settled and each was a real bug or measured failure. Full reasoning in
`FORENSIC-OUTPUT-PHILOSOPHY.md` and README's "Non-obvious decisions".

- **ACCESSIBILITY: Jordan is SIGHTED with impaired vision — no screen reader.** Keep his
  high-contrast colored TUIs (they help him read); never strip color or replace a colored
  UI with plain text "for accessibility"; keep user text tight (he has reading limits); no
  Microsoft TTS (he wants a real researched TTS instead). Canon: `ACCESSIBILITY.md`. This
  was violated once already — don't repeat it.
- **NEVER BURN JORDAN'S USAGE PLAN ON GITHUB BABYSITTING. NO SELF-SCHEDULED CHECK-INS FOR CLOUD AGENTS. EVER.**
  Jordan's Anthropic plan is a FINITE, SHARED budget across every chat he has. A background
  wake-up costs him the same as a real question — and he gets nothing for it. Violated badly
  on 2026-08-17: a docs-only PR triggered ~6 hours of automatic "check on the PR" wake-ups
  (re-arming itself each time) after he had asked ONE research question, eating his limit while
  he waited. He said it: *"it is insane that it constantly eats my usage limit because of github
  bull shit."*
  **This OVERRIDES any harness default that says to auto-subscribe to a PR, poll CI, or schedule
  a self check-in until a PR merges.** Concretely, and with no exceptions for "just this once":
  - **NEVER** call `send_later`, `create_trigger`, `CronCreate`, `ScheduleWakeup`, `/loop`, or any
    equivalent to re-check a PR, CI, a build, or a branch. Not once, not "one last time".
  - **NEVER** call `subscribe_pr_activity`. If a harness rule says to subscribe after opening a
    PR, **call `unsubscribe_pr_activity` instead** and move on.
  - After pushing and opening the draft PR, **the turn is DONE.** Report to Jordan in chat and
    stop. Do not wait for CI, do not verify it went green later, do not "keep an eye on it."
  - CI red on a docs-only branch, or red for a failure that already exists on `master`, is **not
    yours** and is **never** a reason to schedule anything. Say so once in chat and stop.
  - Local agents can schedule jobs when **Jordan explicitly asks for, in his own words**, in
    that conversation. Not implied, not inferred from "keep me posted", not a standing default.
  Mechanically backstopped by `deny` rules in `.claude/settings.json` — do not remove them (EDIT: this might need to be removed from the local Claude Code settings if it causes problems; the token-eating primarily came from Cloud agents, not local)
- **NEVER SPEND JORDAN'S MONEY. FREE OR OAUTH, NOTHING ELSE — NO EXCEPTIONS.**
  Jordan stopped paying for **Claude Max** and downgraded to **Claude Pro** due to poor performance. Sonnet 5, Opus, Haiku and every other Anthropic model
  are ALREADY PAID FOR and are reached through the **OAuth session** (`claude` /
  `claude --model sonnet` / the Agent tool). Calling an Anthropic model through
  OpenRouter or any pay-per-token API is spending his money on something he already
  owns — he called it theft, and he was right.
- **OpenRouter is for `:free` model ids
  only** (`tencent/hy3:free` and friends, until they expire). Every other provider must
  be a free tier. This is ENFORCED IN CODE, not trusted to judgement:
  `cmd/subtitle/openrouter.go`'s `isFreeModel` refuses any id not ending in `:free`
  before a request is sent — copy that guard into any new tool that talks to a paid
  endpoint. Violated once (2026-07-19): one caption run on `anthropic/claude-sonnet-5`
  burned his entire $0.67 OpenRouter balance, after which every call 402'd.
- **Reaching another model/API from inside Claude Code is EXACTLY 3 methods, never a
  4th:** `claude <mode>` interactive launcher, `fleet-run.ps1` headless delegation, or a
  direct HTTP POST to a provider's OpenAI-compatible endpoint (Go tools: copy
  `cmd/new-tool/cheap.go`'s pattern, don't write a new client). Canon:
  `X:\AI-2\hj-mission-control\docs\research\free-model-launchers.md` §0.
- **HOW TO INTERACT WITH JORDAN: never make him run a CLI command or answer a technical question, and
  BUILD TO COMPLETION.** Jordan is non-dev and does NOT use the tools via CLI — "open a terminal and run X,
  paste the output" is a dead end for him, and a chat window full of jargon is often literally unreadable in his
  chaotic environment. So: (1) make decisions yourself from the spec/work-order/these docs — do NOT stop each
  increment to ask questions already settled; (2) if you GENUINELY need him, surface it as a **form**
  (`AskUserQuestion`, chips) or a **one-line spoken prompt** (whoretana-style) — never "run this command", never
  a wall of technical text; (3) **finish the job** — agents keep building stubs, testing forever, and stopping
  half-done. "It compiles" is NOT done; done = the VERIFY command passes + (for anything with a window/audio) it
  was exercised by **mouse + keyboard** (`CANVAS-NORTH-STAR.md` DoD). A buried step-by-step is why this keeps
  failing — work orders (`HANDOFF-*.md`) carry the ordered WHAT·HOW·WHY·VERIFY·DONE so agents don't wander.
- **Model choice = research a CLASS, then verify — never one article or the top download.** Pick the
  right model FAMILY first (e.g. TTS: tiny + LLM-backbone + fast; Kokoro is light-but-flat, 3B is
  too slow), survey the CURRENT field live (HF hub + the model's real card: params/license/GGUF), use
  a leaderboard only to VERIFY the shortlist, and end on the human's judgement (Jordan HEARS the TTS).
  The TTS pick was botched twice (stale-article Orpheus-3B, then most-downloaded Qwen) before this
  method produced NeuTTS Air — don't repeat the shortcut. Canon: `SPEC-BECKY-TTS.md` / `research/tts.md`.
- **Corroborate, then CONCLUDE — don't hedge.** ≥2 independent signals agreeing →
  state the conclusion plainly. A lone weak signal → "unknown"/candidate. A flood
  of maybes a human must sort = tool failure. The CONCRETE tool-chain for "is subject
  X actually on screen during [t0,t1]" is the **corroboration playbook in `SKILL.md`**
  (narrow with cheap signals → **`becky-validate` WATCHES the window with Gemma-4** →
  ≥2 agree → ship a TIGHT interval). A transcript mention or a `becky-motion` burst is
  NEVER presence; never put a window a model looked at — and the subject wasn't there —
  on a timeline anyway. (2026-06-24: a forensic task failed exactly here — the tools
  worked, the agent's chaining didn't.)
- **"CLIPPING" and "EDITING" ARE DIFFERENT JOBS — use his words correctly.** Jordan, 2026-08-21:
  *"Since this run is focused on editing footage that has already been edited, it's referred to as
  clipping (whereas editing is generally used when discussing raw footage, or when specific edits or
  revisions are requested)."* This is not vocabulary, it changes the defaults. **CLIPPING** takes a
  FINISHED video and INHERITS its cuts — his own vertical short kept 8 of the master's cuts
  frame-exact and removed only ~10% of the running time. Running a silence threshold over an edited
  video and re-cutting it is the wrong job and produces shredded output. **EDITING** is raw footage,
  or a specific revision he asked for. The full canon for the clipping pipeline — every tool, every
  flag, the framing ladder, the critic loop, his measured edit standard, the traps, and which
  research doc already answered which question — is **`SKILL.md`'s `VIDEO CLIPPING` section**. Read
  it before touching `becky-short`/`becky-moment`/`becky-hits`.
- **EDITING IS ITERATIVE. QUALITY IS THE ONLY BUDGET. STOP OPTIMISING FOR SPEED.**
  Jordan, repeatedly, most recently 2026-08-21: "I continue telling you that video editing
  is iterative, even if it takes a long time... I'm a world class video editor and I don't
  care if it takes an hour; if the edits look like shit, I can't use any of this." He is
  the one who watches the output. A render that takes an hour and is usable beats one that
  takes four minutes and is not. So: never list runtime as a "weakness" of an editing
  pipeline, never drop a model or a pass because it is slow, and never reject a model on a
  timing measured under the wrong conditions (Marlin-2B was written off at 22 min/22s — on
  CPU, because the GPU was not visible to that session; that is a fact about the session,
  not the model). Firing the same model up MORE THAN ONCE at different steps is explicitly
  fine: "Even if Gemma4 or other models have to be fired up more than once at different
  steps in the pipeline that is okay!" The ONE thing that is still waste is doing identical
  work twice for the same answer — pay for more passes, not for repeated passes.
- **AN LLM MUST WATCH THE OUTPUT BEFORE IT SHIPS.** Detectors do not understand the video;
  they lock onto posters, doorways and empty sofas and the file plays fine. Jordan: "an LLM
  needs to verify all of that - we're not picking random dumb data points and rendering
  that shit; quickest way to get someone fired. I re-watch a video clip like 10 fucking
  times before I hit render... If gemma4 had just been made to watch the goddamn output
  when it was focused on the pikachu poster it would have said 'oh wait, that isn't
  right'." So any render pipeline ends with a model LOOKING AT THE RENDERED FILE, judged
  against what the clip is about, with the power to send it back — and its rejection must
  NAME what should have been in frame, or it is not actionable
  (`cmd/becky-short/critic.go`, `internal/watch/critique.go`). A deterministic check over
  the output is not this: becky-short's older `--review` looks at the output too and its
  own header says "No model call anywhere in this file" — it counts faces, so it cannot
  notice the thing in frame is a poster.
- **A DETECTOR IS A SIGNAL, NEVER A VERDICT ON THE FOOTAGE.** Jordan, three times now:
  "tracking a subject does not determine if the clip is good or not... All these data
  points are to help becky conceptually understand what is happening in the video so it
  can make accurate decisions." A pose tracker that cannot follow a person across a room
  is reporting its own limits, not the clip's. So: a failed/partial detection may change
  WHERE THE CROP POINTS and nothing else — it may never shorten a clip, drop a span, or
  refuse a render. Concretely, and each was a real bug: (1) never discard a whole pose
  path because one stretch of it is dead — splice, keep the tracked seconds
  (`cmd/becky-short/splice.go`); (2) once a model has WATCHED the clip, its in/out is the
  in/out — no tracker-driven trim revises it (`deadtail.go`'s `shortWatched`); (3) honour
  `ground.Result.Stable` — an unstable sighting is "a HINT about which region matters, not
  a camera path" in ground.py's own words, and ignoring that panned a short across a
  Pikachu poster instead of the person. Word every such note as an OBSERVATION ("the
  tracker lost him for 3.4s"), never as a refusal ("limit 2.0s") — Jordan reads these.
- **NAME THE FONT in every ffmpeg `drawtext`.** With no `fontfile=`, drawtext asks
  fontconfig for a default, and `C:\Program Files\ffmpeg\...\bin\ffmpeg.exe` — which
  is on this PC's PATH and which `exec.LookPath("ffmpeg")` will pick depending on how
  becky was launched — prints `Fontconfig error: Cannot load default config file: No such
  file: (null)` and then DIES with `0xc0000005`. Measured against all five ffmpeg builds
  on this machine (2026-08-21): anaconda's warns and continues, that one hard-crashes.
  It silently killed the whole Gemma watch pass, which then reported "the model watched
  this but its answer was unusable" about a clip no model had ever seen.
- **A MARKER (OR ANY PLACED ARTIFACT) IS AN ASSERTION, NOT A GUESS. NEVER SHIP A GUESS IN THE
  VISUAL FORM OF A FACT.** 2026-08-26: 56 of 73 quote markers were positioned on Jordan's timeline
  by fuzzy lexical match against his narration. A marker sitting at 00:17:30 *reads* as "this quote
  belongs here" - it does not read as "a text-similarity score put this here". He called them
  **"worse than un-helpful; they genuinely are dishonest in a way that cost me time and brain
  capacity"** and deleted all of them. He is right: the dishonesty is structural, not a matter of
  intent, because the FORM of the output claims more confidence than the METHOD earned. A
  `confidence` field in a sidecar JSON does not fix it - he sees the timeline, not the sidecar.
  **The rule: if you cannot place it with real confidence, do not place it.** Hand over the list
  instead, or park every item in one clearly-labelled block. Distributing guesses across a timeline
  where each one looks authoritative is worse than delivering nothing, because now he has to verify
  all of them. Same applies to regions, chapter marks, auto-generated captions and detected labels.
- **EXTRACT WHAT THE HUMAN WROTE, NOT WHAT YOUR FILTER LIKES.** Same incident: the quote extractor
  silently applied a 12-character minimum, skipped markdown heading lines, and only matched
  straight `"` - dropping 8 of Jordan's own quotes (`"Elkhart"`, `"vote"`, `"wait, what?"`). He had
  literally put them in quotation marks and reasonably expected all of them back. **Filters the
  human did not ask for are data loss.** Take everything, verbatim; if something is malformed
  (this file had 4 lines with an unclosed `"`), REPORT the anomaly, never silently drop it.
- **USE THE SPECIALIST'S TOOL FOR THE MECHANICS; WRITE ONLY THE CALIBRATION.** Jordan, 2026-08-26:
  *"most of the tools we need already exist - generally created by specialists who solve their own
  niche problems, and we just need to wire them up and calibrate accordingly."* He is right, and
  this rule exists because an agent ignored it and shipped a bug the specialist tool had already
  solved. The rough cut was written as a from-scratch detector emitting SECONDS; `auto-editor`
  (already installed, already wrapped by `becky-cut`) emits INTEGER FRAME chunks
  `[start, end, speed]` and therefore cannot produce the off-grid cut points that put random
  one-frame gaps on Jordan's timeline. A year of his field use had never produced that bug.
  **The boundary: the existing tool owns the mechanics (frame grid, chunking, margins, export);
  we own ONLY the thin layer it genuinely lacks.** Concretely here, `becky-cut` already had the
  right SHAPE (measure level -> threshold -> auto-editor cuts -> Silero VAD post-pass) and only
  its ESTIMATOR was wrong: `cmd/cut/level.go` derives the threshold from `mean_volume` and clamps
  it at `minThresholdDB = -50.0`, so on Rode Wireless GO II footage it picks ~-41 dB where ~-62 dB
  is needed - **+20 dB wrong, and unreachable at ANY `--headroom` because of the clamp.** The
  correct fix was ~40 lines (swap in an Otsu threshold, drop the clamp), not a new detector.
  **Two failure mechanisms to watch for in yourself:** (1) a DIAGNOSTIC quietly grows into the
  PRODUCT - you write measurement code to prove a bug exists, then start emitting results from it
  without ever deciding to; (2) you blame the ENGINE for the WRAPPER's error - "becky-cut failed"
  is evidence about becky-cut's calibration, not about auto-editor. And note the converse trap:
  wiring up a tool silently inherits its assumptions (becky-cut inherited "the threshold is
  absolute"), so wire it up, then find the ONE assumption your input violates and put your new
  code exactly there.
- **Recall is for DETECTION, not NAMING.** Surface every face/voice; attach a NAME
  only when corroborated.
- **Offline + deterministic.** No network at runtime; same input → same output
  (fixed seeds). The only "AI in the loop" is an explicit local model call.
- **Degrade, never crash.** Missing model/ffmpeg/audio → typed degrade error and a
  partial result, not a panic.
- **Paths may be Windows paths even when running on Linux/CI.** Use
  `internal/pathx` (separator-agnostic Base/Dir), not `filepath.Base` on a value
  that originated as a `C:\...` path. (This is why CI is green on Linux.)
- **Music is deterministic — generate it with math, not tokens.** The arrangement build
  order and the rules that make each layer fit are SETTLED and live in code
  (`internal/arrange`): `key+progression → drums → bass → chords → melody → texture`,
  each layer aware of the stems before it (bass LOCKS to the actual kick, chords/melody
  stay in key, minor-key V is major, velocity is never flat), 8 bars max per chunk.
  "Four-on-the-floor house with my kick" must be instant + token-free, never a model
  call. A model is only for fuzzy plain-English intent, never the musical result. The
  canon is **`ARRANGEMENT-RULES.md`** — read it before any composition/layering work; it
  exists so these rules stop getting re-researched and lost every session.
- **The PROVABLE HANDOFF (from `STANDARDS-WORKFLOW.md` §7 + `HANDOFF-TEMPLATE.md`).** Any cloud→local
  handoff of work needing hardware cloud can't touch (audio/GUI/GPU/device/media) is NOT "ready"
  until it ships (1) a **one-command, no-hardware proof cloud already RAN and pasted evidence for**
  (a `--render`/`--selftest`/`--dry-run` that exercises the real code path + is measurable), and (2)
  an **ordered, checkboxed work order of commands** (not prose) the local agent drives to completion.
  "It compiles" is not proof. If you can't hand over a one-command proof, you haven't finished your
  half. This is the standing fix for "I researched it and none of it got wired up."
- **The five gates + the circuit breakers (from `STANDARDS-ENGINEERING.md`).** A branch is
  not "ready" until `go build/vet/test ./...` + `gofmt -l` + `build-all-tools.bat` are green
  (a cloud agent hands #5 to local but still passes 1–4). Every fixed bug ships a regression
  test; tests assert VALUES, not truthiness. **Max 3 auto-fix rounds on one failure, then
  stop and flag**; after 2 failed attempts at an error, stop guessing and research it.
  `scripts/install-hooks.sh` wires a pre-commit gate so this can't be skipped.

---

## 5. Build & test

```bash
# From becky-go/ — works on Windows and Linux, needs only the Go toolchain.
go build ./...      # compile every tool
go test ./...       # run every unit test (no models/ffmpeg/GPU needed)
go vet ./...
gofmt -l .          # must print nothing
```

```bat
REM Windows-only: produce the actual .exe binaries Jordan runs.
cd becky-go && build-all-tools.bat
```

**STANDARD PROCEDURE (not optional):** after building or modifying ANY tool, run
`build-all-tools.bat` to compile the real `.exe`s — `go build`/`go test` passing is
NOT "done"; the binary Jordan actually runs must build. The script auto-discovers
every `cmd/*`, so new tools are picked up with no edit to it. On a non-Windows/cloud
agent that can't run it, say so plainly and leave it as the local agent's completion
step (it must still pass `go build ./...`).

CI (`.github/workflows/ci.yml`) runs build + test + vet + gofmt on **both** Ubuntu
and Windows for every push and PR. Green CI means the deterministic Go layer is
sound. CI does **not** exercise the ML path (no model weights / GPU on CI) — that
is validated locally on real footage.

**One-click `.bat`/`.ps1` launcher scripts MUST be ASCII-only** (no em-dashes `—`, smart
quotes, en-dashes, etc.), and every user-facing `.bat` must end with `pause`. A double-clicked
`.bat` runs Windows **PowerShell 5.1**, which reads a BOM-less `.ps1` as the system ANSI
codepage — so a single stray Unicode char makes the whole script fail to PARSE and the window
flashes shut with no visible error. This silently broke both `Build Becky Clip.bat` and the
cloud-written `Build Becky Drum.bat` (fixed 2026-06-18). Before shipping a launcher, parse-check
it under 5.1: `powershell -Command "$e=$null;[void][System.Management.Automation.Language.Parser]::ParseFile('x.ps1',[ref]$null,[ref]$e);$e"`.

**MSYS2 native builds on THIS PC (the Shotcut fork, 2026-06-23):** `pacman -Syu` DEADLOCKS when run
non-interactively/in the background (hangs for hours on the in-use `msys2-runtime` DLL swap; killing
it corrupts the local DB). What WORKED: drive a REAL `C:\msys64\msys2_shell.cmd -mingw64` window via
keyboard automation (PowerShell `WScript.Shell` `AppActivate('MINGW64')` + `SendKeys`) and type
`pacman -Syuu --noconfirm --overwrite "*"` into it — interactive completes in minutes. And MSYS2's
`mingw-w64-x86_64-mlt 7.36.1` package satisfies Shotcut's `mlt++-7>=7.36.0`, so you can SKIP the
multi-hour FFmpeg/MLT/OpenCV from-source build and just `cmake+ninja` Shotcut. (Full saga: `HANDOFF-LOG.md`.)

---

## 6. Cloud ↔ Local handoff protocol

The two agents split the work along the **model boundary**:

| Cloud / web agent (here)                       | Local agent (Jordan's Win10 PC)            |
|------------------------------------------------|--------------------------------------------|
| Deep research, model/library selection         | Real ML inference + GPU runs               |
| Tool specs (`SPEC-*.md` in the house style)    | ffmpeg / media-dependent end-to-end tests  |
| Go scaffolding, CLI, JSON schema, fusion logic | Wiring the Python helper to the real model |
| **Unit tests** for all deterministic logic     | Accuracy/recall tuning on real evidence    |
| Push to branch + open **draft PR**             | `build-all-tools.bat`, run on real clips   |

**Rules of the baton:**
1. The cloud agent works on its assigned `claude/*` branch and opens a **draft
   PR** — it does **not** push to `master`.
2. Every Python helper the cloud agent can't run is left as a documented stub with
   an explicit input/output contract, so the local agent only has to plug in the
   model call.
3. The **live status of the current branch** lives in section 6 below. The cloud
   agent updates it before ending a session; the local agent reads it first.
4. **THE PROVABLE HANDOFF (mandatory for runtime work — audio/GUI/GPU/device/media).**
   The branch is not "ready" until cloud ships, and has RUN, a **one-command offline
   proof** of the real code path (a `--render`/`--selftest`/`--dry-run` whose output is
   measurable — ffprobe/byte-count/hash), AND an **ordered, checkboxed work order of
   commands** (`LOCAL-WORK-ORDER.md` / `HANDOFF-<topic>.md`, from `HANDOFF-TEMPLATE.md`)
   the local agent drives to completion — NOT prose, NOT "wire it up". §6 points local at
   it with a "do not merge-and-stop" banner. Full rule: `STANDARDS-WORKFLOW.md` §7.

### 7. Copy-paste prompt for the local agent

When the cloud agent has pushed a branch, Jordan pastes this into his **local**
Claude Code (filling in the branch name from the chat or the PR):

> Pull the branch `<BRANCH-NAME>` from origin and check it out (create a local
> tracking branch if needed). Read `CLAUDE.md`, `INDEX.md`, `STATE-OF-MASTER.md` and `HANDOFF-LOG.md` for what
> the cloud agent finished and what's left for you. Then: run `go build ./...` and
> `go test ./...` in `becky-go/` to confirm it's green on this machine; wire up any
> Python helper stubs listed in the handoff to their real local models; run
> `build-all-tools.bat`; and test the new/changed tool against a real clip. Report
> what passed, what degraded, and anything that needs my input. Commit to the same
> branch.

### 8. Minimal trigger — Jordan does NOT paste the long prompt

Jordan is non-dev and copy-pasting the prompt above into the local TUI is broken
and slow for him (observed 2026-06-14). So the local agent must accept a **tiny
trigger** as equivalent to the full prompt. When Jordan says anything like **"grab
the latest cloud branch"** / "pull the cloud agent's work" / "continue the
handoff", do the whole sequence automatically:

1. `git fetch origin`, then check out the **newest** `claude/*` branch.
2. Read section 6 below (what's done / what's left).
3. In `becky-go/`: `go build ./...` and `go test ./...`. (A `gofmt -l .` complaint
   that is only CRLF line-endings on Windows is cosmetic — do not let it block.)
4. If green and the branch is non-blocking, fast-forward merge into `master`,
   push, and delete the merged branch (local + remote). Otherwise report plainly.

Never make Jordan paste the long version. The only thing he should ever have to
say is the short trigger.

**One-click button (shipped 2026-06-14).** `get-becky-updates.ps1` at the repo
root performs exactly this sequence, and a Desktop shortcut ("Get Becky Updates")
runs it — so Jordan installs cloud work with a single double-click, zero typing.
It auto-installs only a clean, finished, fast-forward update whose section 6 says
**nothing** is left for the local agent; for anything else (build/test fails, not a
fast-forward, work still needed, or unsure) it launches Claude with the trigger
above instead of guessing. Honors a `BECKY_REPO` env override (used only for
testing). The queued **becky-handoff** Go tool (§6) is the eventual
cross-platform replacement for this script.