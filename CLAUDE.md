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
- **ONE PAID EXCEPTION: System One decision models, $5 a month total, enforced in code.** Jordan, 2026-10-07: "I am happy to
  PAY for Jev" and chose "$5 a month"; "jev is already breaking one of our rules (it's paid), so
  let's not be legalistic in the way we use it; we're after effectiveness". All Jev calls go
  through `internal/systemone` `Hosted` (OpenRouter `/api/alpha/decisions`; default Perplexity Decider
  v1.1, Jev via `WithModel(JevModel)`). Jordan, later that night: the $5 "can be used on whichever system
  one decision model seems most appropriate for the task". It refuses any model OpenRouter does not list
  with the "decisions" output (so never a chat model) and refuses to send once the month's ledger
  (`research/jev/spend-YYYY-MM.json`) reaches `MonthlyCapUSD`. Never call Jev around it.
  Forensic evidence stays local (Laya / ImaJev); Jev is for Jordan's own content and agents.
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
- **NEVER EDIT PATH, AND NEVER TELL ANYONE TO.** An old becky-go build script printed
  `setx` on PATH with `%PATH%` appended as the "next step". Jordan ran it and it erased his
  whole user PATH (`setx` keeps at most 1,024 characters; in PowerShell it saves the literal
  text `%PATH%`). opencode, winget, browser-harness, bun and lms were broken for weeks
  (repaired 2026-09-17). Tools reach PATH because `build-all-tools.bat` copies them into
  `C:\Users\only1\bin`; nothing else is needed. Enforced by `scripts/check-launchers.sh`
  (pre-commit + CI) and, for local agents, `~/.claude/hooks/block-path-overwrite.py`.
- **Offline + deterministic.** No network at runtime; same input → same output
  (fixed seeds). The only "AI in the loop" is an explicit local model call.
- **Degrade, never crash.** Missing model/ffmpeg/audio → typed degrade error and a
  partial result, not a panic.
- **Paths may be Windows paths even when running on Linux/CI.** Use
  `internal/pathx` (separator-agnostic Base/Dir), not `filepath.Base` on a value
  that originated as a `C:\...` path. (This is why CI is green on Linux.)
- **The five gates + the circuit breakers (from `STANDARDS-ENGINEERING.md`).** A branch is
  not "ready" until `go build/vet/test ./...` + `gofmt -l` + `build-all-tools.bat` are green
  (a cloud agent hands #5 to local but still passes 1–4). Every fixed bug ships a regression
  test; tests assert VALUES, not truthiness. **Max 3 auto-fix rounds on one failure, then
  stop and flag**; after 2 failed attempts at an error, stop guessing and research it.
  `scripts/install-hooks.sh` wires a pre-commit gate so this can't be skipped.

---

## Topic rules - loaded only when the work needs them (moved here 2026-10-10)

These invariants used to sit in this file. They now live in `topic-rules/<name>/SKILL.md` (tracked in
git, same authority as this file). The System One picker hands the right one to the agent with the
message; any agent can also just read the file. Read the matching one BEFORE starting that kind of work:
- `becky-video-editing-rules` - clipping vs editing, iterative quality, an LLM watches every render, detectors
  are signals, name the drawtext font, markers are assertions, extract verbatim, specialist tool + calibration,
  plus the editing LESSONS.
- `becky-forensic-rules` - corroborate, then conclude; recall is for detection, not naming.
- `becky-music-rules` - music is deterministic (arrangement build order).
- `becky-model-selection` - research a model CLASS, then verify.
- `becky-cloud-handoff` - the provable handoff, cloud <-> local protocol, copy-paste prompt, minimal trigger.
  **Cloud agents: read it first.**
- `msys2-native-builds` - MSYS2 / Shotcut native builds on this PC.

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

---

