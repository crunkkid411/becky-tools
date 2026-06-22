# SPEC — `becky-ask` single-shot (scriptable, non-interactive) mode

> ## STATUS — DESIGN ONLY (not built). Cloud-buildable + testable; the VLM answer is the one local-model step.
> This spec ADDS a non-interactive, scriptable entry to `becky-ask` (`--question` /
> `--image --question`) that prints a plain answer and exits. It does **NOT** change,
> demote, or replace the interactive colored bubbletea TUI — that stays the untouched
> default. Read §1 first: a prior agent already got this wrong once.
> Re-verify any model-id / flag claim past its cited date before building (the
> `claude-api` skill is the in-repo re-verification path).

---

## 0. TL;DR

`becky-ask` today is **interactive-only**: with no TTY it either runs the narrow
`BECKY_ASK_RUN=<op>` env path or just prints what it parsed and exits
(`cmd/ask/main.go:main` → `runHeadless` / `printNoTTY`). There is no scriptable
"ask one question, print the answer, exit" mode. README "Medium" names this exact
gap:

> **becky-ask is interactive-only** — no scriptable mode. There's no `--image <f>
> --question "<q>"` single-shot CLI. This blocks programmatic use (e.g. overnight
> cross-checking). **Fix pending:** add non-interactive `--image --question` mode
> that prints answer + exits.

This spec adds exactly that, as a **pure ADDITION**:

- `becky-ask --question "<q>"` — answer a text request through the EXISTING intent /
  router path, print a clean plain answer, exit. (Optionally with a dropped target:
  `becky-ask --question "transcribe this" --target <file>`.)
- `becky-ask --image <file> --question "<q>"` — ask about an image; the answer comes
  from the local vision-language model (`becky-vision` / LFM2.5-VL), degrading
  gracefully when the model is absent.

The default — run with a real terminal and no `--question` — still launches the
colored bubbletea TUI exactly as it does now. Nothing about the interactive window
changes.

---

## 1. NON-NEGOTIABLE: the interactive colored TUI is an accessibility AID — do NOT touch it

Per `ACCESSIBILITY.md` (load-bearing): **Jordan is sighted with impaired vision**;
the high-contrast custom-palette bubbletea TUI (`cmd/ask/styles.go`) is *easier* for
him to read, **not** a barrier. ACCESSIBILITY.md fact 2 is explicit:

> Keep colored TUIs. Do NOT strip color, and do NOT replace a colored TUI with plain
> monochrome text "for accessibility."

A prior agent wrongly demoted the TUI. This spec must not repeat that. The rules this
spec binds itself to:

1. **The interactive colored TUI stays the DEFAULT.** When `becky-ask` is run with a
   real terminal (`term.IsTerminal`) and **no** single-shot flag, it launches the
   bubbletea program with `WithAltScreen()` + `WithMouseCellMotion()` exactly as
   `cmd/ask/main.go:main` does today. Byte-for-byte the same path.
2. **Single-shot is opt-in, only via an explicit flag.** It is NEVER the default and
   NEVER auto-selected just because something *looks* headless. The existing no-TTY
   behaviors (`BECKY_ASK_RUN`, `printNoTTY`) are preserved unchanged (§3.4).
3. **Single-shot is for SCRIPTS/agents, not for Jordan's daily use.** It prints plain
   linear text to stdout because a pipe/file consumer needs that — it is not a
   "more accessible" replacement for the window. The window remains his channel.
4. A regression test asserts the TUI default is preserved (§6).

If anything in this spec ever seems to argue for replacing the TUI, it is wrong — ask
Jordan, don't pivot.

---

## 2. Purpose + user need

The need is **programmatic** use of becky-ask's brain:

- **Overnight cross-checking.** A scheduled job asks "is there a person on screen in
  this frame?" across hundreds of extracted frames and writes the answers to a log —
  unattended, no terminal, exit codes a script can branch on.
- **Pipelines / other tools calling becky-ask.** `becky-pipeline`, `becky-clip`, or a
  shell loop can shell out `becky-ask --image f.png --question "..."` and parse the
  printed answer, the same way they already shell out `becky-transcribe`,
  `becky-identify`, etc. (`cmd/ask/run.go:runCommand` is the in-repo pattern for how
  becky-ask itself resolves+runs sibling tools.)
- **CI / verification.** A deterministic `--question` over the offline keyword catalog
  is a one-command proof the routing layer works with no model, no GPU, no display.

Today none of this is possible — the only non-interactive paths are the narrow
`BECKY_ASK_RUN=<op>` runner (one fixed op on a target) and the diagnostic
`printNoTTY` (just echoes what it parsed). Neither answers a free-text or image
question.

---

## 3. The design

### 3.1 Two new flags, routed through the EXISTING brain

The whole point is to REUSE the already-built intent → router → run path, not to fork
a second brain. The flags:

| Flag | Meaning |
|---|---|
| `--question "<q>"` (alias `--ask "<q>"`) | The plain-English request. Presence of this flag selects single-shot mode. |
| `--image <file>` | An image to ask ABOUT. Implies single-shot; routes the answer through the local VLM (§3.3). Requires `--question`. |
| `--target <path>` | Optional file/folder the question refers to (the same "Target" the TUI gets from argv drag-drop). Equivalent to a dropped path. |
| `--json` | Emit one JSON object instead of plain text (§4.2). |
| `--run` | For a text question that the gate classifies as a clear ACTION on a target: actually RUN the resulting becky-* command (default: print the command, don't run). Mirrors the TUI's opt-in confirm — see §3.5. |

**Mode selection (the only behavioral switch added to `main`):**

```
if --question (or --image) is set      → SINGLE-SHOT  (print answer, exit)   [NEW]
else if !term.IsTerminal(stdin)        → existing no-TTY path (BECKY_ASK_RUN / printNoTTY) [UNCHANGED]
else                                   → interactive colored bubbletea TUI   [UNCHANGED DEFAULT]
```

Single-shot is checked FIRST and only fires on an explicit flag, so it can never
shadow the TUI default and never changes the existing no-TTY env path.

### 3.2 Text question: `--question "<q>"`

A text question reuses the existing classifier and router verbatim:

1. Build the `Target` from `--target` (if given) via the existing `resolveTarget`
   (`cmd/ask/target.go`) — same quote-stripping / existence-checking as a drag-drop.
2. Build the local model client the same way the TUI does: `resolveIntentModel()` +
   `cfg.LlamaServer` → `newLlamaClient(...)` (`cmd/ask/run.go`, `cmd/ask/llama.go`).
   In a headless/no-GPU environment `cli.Ready()` fails and `classify` degrades to the
   deterministic + keyword-catalog path with an honest note — **never hard-fails**
   (`cmd/ask/intent.go:classify`, `cmd/ask/llama.go:Ready`).
3. Call `route(ctx, cli, question, target)` (`cmd/ask/router.go:route`) — the SAME
   single entry point the TUI's submit handler calls. It returns a `routed{Reply,
   Pending}`.
4. Print the answer (§4) and exit:
   - `routed.Pending == nil` (a question / clarify / catalog answer / new-tool note) →
     print `Reply`, exit 0.
   - `routed.Pending != nil` (the gate classified a clear ACTION on a target):
     - default (no `--run`): print the answer + the exact command (`commandString`)
       that WOULD run, and exit 0. This is the scriptable equivalent of the TUI's
       "Run it? (y/n)" prompt — show, don't do.
     - with `--run`: execute it via the existing `runCommand` (`cmd/ask/run.go`),
       print the result/`Saved` paths, exit with the command's exit code.

Crucially, the single-shot path adds **no new routing logic** — it is a thin headless
caller of `route(...)`. The act-vs-discuss target gate (`intent.go`) is unchanged, so
a question can never silently fire a tool call.

### 3.3 Image question: `--image <file> --question "<q>"`

The image path is the one that needs a vision model. becky already has the right tool:
**`becky-vision`** (`cmd/vision/main.go` + `internal/vision`), a local **LFM2.5-VL**
GGUF wrapper run via `llama-mtmd-cli` (per `SPEC-BECKY-VISION-MODELS.md`). It is
already OFFLINE, DETERMINISTIC (`--temp 0`), and DEGRADE-NEVER-CRASH (a missing
binary/model/mmproj/image → `vision.Result{Degraded:true, Error:...}`, exit 0 — see
`internal/vision/vision.go:Describe`/`degrade` and `cmd/vision/main.go:printReport`).

So becky-ask's image answer is produced by **resolving and shelling the sibling
`becky-vision` binary** with `--json`, exactly the resolve-a-sibling-becky-*-exe
pattern becky-ask already uses (`cmd/ask/run.go:binPathFor` → `runCommand`):

```
becky-vision --image <file> --prompt "<question>" --json
```

Then becky-ask reads `vision.Result.Description` (or the degrade note) and prints it
in the chosen format (§4). Why shell `becky-vision` rather than import
`internal/vision` directly:

- It keeps the **single-tool principle** (CLAUDE.md): becky-ask stays a router, the
  VLM lives in its one tool. becky-ask already shells siblings; this is one more.
- It inherits `becky-vision`'s degrade contract for free — if the VLM binary/model is
  absent, becky-ask surfaces the plain-language degrade note and still exits cleanly.
- The model path / mmproj discovery stays owned by `internal/vision`
  (`DiscoverModels`), so there is no second copy of model resolution.

(Open Decision D1 in §7 records the import-vs-shell choice for confirmation. Default:
shell the sibling.)

**Degrade-never-crash for `--image`:** if `becky-vision` is not found next to
becky-ask, or returns `Degraded:true`, becky-ask prints a plain note (`"couldn't
read the image — the vision model or a file was missing"`) and exits 0 in
plain-text mode, or emits `{"degraded":true,...}` exit 0 in `--json` mode. A genuinely
missing `--image` file (when `--image` was given) is a usage error → exit 2.

### 3.4 Relationship to the existing no-TTY / `BECKY_ASK_RUN` paths

These are **kept unchanged**. The contract after this spec:

- `BECKY_ASK_RUN=<op> becky-ask <file>` — still runs `runHeadless(op, args)`
  (`cmd/ask/main.go`): one fixed op (e.g. `transcribe`) on a target, prints `Saved:`
  paths to stderr, exits. Single-shot does NOT replace it; `--question` is the
  free-text superset, `BECKY_ASK_RUN` is the fixed-op shortcut. If both are somehow
  set, the explicit `--question`/`--image` flag wins (single-shot), since it is the
  more specific intent.
- `becky-ask <file>` piped/headless with no env and no flag — still `printNoTTY`
  (diagnostic echo of the parsed target + applicable actions). Unchanged.

Single-shot slots in as a THIRD non-interactive entry, selected only by its flag.

### 3.5 Safety posture (carried from the TUI)

The TUI never runs a typed action without an explicit `y` (`SPEC-BECKY-ASK.md` §2.5
Open Q3; `router.go:actReply`). Single-shot keeps the same posture: **show, don't do,
by default.** A clear action prints its command but does not run it unless `--run` is
passed. `--run` is the scriptable equivalent of pressing `y` — an explicit opt-in the
caller chose. This means an overnight script that only wants ANSWERS (the common case)
can never accidentally launch a 30-minute transcription.

---

## 4. CLI contract — flags, stdout format, exit codes

### 4.1 Flags (full list)

```
becky-ask --question "<text>"                 [--target <path>] [--run] [--json]
becky-ask --image <file> --question "<text>"  [--json]
becky-ask --ask "<text>"                      (alias of --question)
```

- `--question` / `--ask` string — the request. Setting either selects single-shot.
- `--image` string — image to ask about; requires `--question`; routes via the VLM.
- `--target` string — optional file/folder the text question refers to.
- `--run` bool (default false) — execute a classified action instead of just printing
  its command.
- `--json` bool (default false) — machine-readable output (§4.2).

Flags are parsed with the standard library `flag` package, the same as `cmd/vision`.
Any leftover positional args are treated as a dropped `--target` (so
`becky-ask --question "transcribe this" clip.mp4` works), matching the TUI's
argv-as-target behavior (`resolveTarget`).

### 4.2 stdout format (clean + parseable)

**Plain mode (default).** One answer, plain linear text, to **stdout**, no ANSI color
codes (single-shot output is consumed by scripts/files, and lipgloss styling is for
the human window only — so the single-shot renderer must NOT apply `beckyStyle` etc.).
Examples:

```
$ becky-ask --question "can becky transcribe?"
Yes — becky can transcribe. Run:  becky-transcribe <video>

$ becky-ask --question "transcribe this" --target clip.mp4
becky-transcribe "clip.mp4"
# (the exact command; not run without --run)

$ becky-ask --image frame_0007.png --question "is there a person on screen?"
Yes — one person is visible, center-left, facing the camera.
```

Plain mode prints ONLY the answer body (it strips the TUI's decorative "Run it?
(y/n)" prompt and banners — those are interactive-only). When a command is staged but
not run, the command string is the answer line.

**JSON mode (`--json`).** Exactly one JSON object to stdout, newline-terminated:

```json
{
  "question": "is there a person on screen?",
  "image": "frame_0007.png",
  "answer": "Yes — one person is visible, center-left, facing the camera.",
  "kind": "image",
  "command": null,
  "ran": false,
  "source": "lfm2.5-vl",
  "degraded": false,
  "error": ""
}
```

- `kind` ∈ `question | action | clarify | new_tool | image` — mirrors the router's
  `decision.Kind` (`intent.go`) plus `image` for the VLM path.
- `command` — the staged becky-* argv (array) when `kind==action`, else `null`.
- `ran` — true only when `--run` executed the command.
- `source` — honest provenance: `deterministic`, `qwen3.5`, `lfm2.5-vl`, or a
  `deterministic (model unavailable: …)` degrade string (from `decision.Source` /
  the vision result).
- `degraded` / `error` — set when a model was absent and we fell back (mirrors
  `vision.Result.Degraded`).

### 4.3 Exit codes

| Code | Meaning |
|---|---|
| `0` | Answered (a clean model-absent degrade still exits 0 — it answered honestly that it couldn't, which is a valid answer). |
| `1` | Unexpected error (e.g. `--run` command failed: exit code propagated from the tool when non-zero; otherwise 1). |
| `2` | Usage error: `--image` given without `--question`, an `--image` file that does not exist, or empty `--question`. |

This matches `cmd/vision`'s scheme (0 ran/degraded, 1 unexpected, 2 usage) so a caller
that already shells becky-vision sees consistent codes.

---

## 5. Deterministic / offline behavior

- **Offline.** The text path uses the local Qwen intent model or, when absent, the
  pure-Go keyword catalog (`catalog.go`/`router.go`) — no network. The image path uses
  the local LFM2.5-VL via `becky-vision` — no network. The only "AI in the loop" is an
  explicit local model call, per CLAUDE.md.
- **Deterministic.** The intent model already runs `temperature:0, seed:42`
  (`llama.go:chat`); `becky-vision` runs `--temp 0` (`internal/vision`). The keyword
  catalog and the gate are pure functions. Same input → same output.
- **Degrade-never-crash.** No model / no GPU / missing binary → an honest plain answer
  + exit 0 (text) or `degraded:true` + exit 0 (JSON). Never a panic. The
  deterministic+catalog path means the TEXT single-shot mode is fully functional with
  no model at all — which is also what makes it cloud-testable (§6).

---

## 6. Cloud-vs-local split + the BUILD PLAN

**Cloud-buildable + fully unit-testable (no model, no GPU, no display):** flag parsing,
mode selection, the routing CALL (with a nil or faked model client), output
formatting (plain + JSON), exit codes, and — load-bearing — the assertion that the
**interactive TUI default is preserved**. The text path's deterministic+catalog branch
runs end-to-end in CI.

**Local-only (needs Jordan's hardware):** the actual VLM answer for `--image` (the
LFM2.5-VL model + `llama-mtmd-cli`) and the live Qwen intent refinement. Design so the
routing around them is testable with a FAKE: inject the model the same way the codebase
already does — `route(ctx, cli, ...)` accepts a nil `*llamaClient` (deterministic
only), and the image path is a sibling-exec that a test fakes by pointing `binPathFor`
at a stub `becky-vision` (or by abstracting the vision call behind a small
`visionAsker` interface with a fake impl, mirroring `internal/vision`'s injectable
`runner`).

### Build plan (checkboxed)

- [ ] **B1 — Add the flags + mode switch in `cmd/ask/main.go`.** Parse `--question`,
      `--ask`, `--image`, `--target`, `--run`, `--json` BEFORE the TTY check. If a
      single-shot flag is present, call `runSingleShot(...)` and `os.Exit` its code.
      Otherwise fall through to the EXISTING `term.IsTerminal` branch — TUI default and
      the `BECKY_ASK_RUN`/`printNoTTY` paths untouched.
- [ ] **B2 — `cmd/ask/singleshot.go` (new): the text path.** `runSingleShot` resolves
      the target (`resolveTarget` + `--target`/positional), builds the model client
      (`resolveIntentModel` + cfg, nil-safe), calls `route(ctx, cli, q, t)`, and hands
      `routed` to the formatter. Honor `--run` (call `runCommand`) vs show-only.
- [ ] **B3 — `cmd/ask/singleshot.go`: the image path.** When `--image` is set, validate
      it exists (else exit 2), resolve sibling `becky-vision` via `binPathFor("vision")`,
      run `becky-vision --image <f> --prompt <q> --json`, parse `vision.Result`,
      surface description or degrade note. Behind a `visionAsker` interface for tests.
- [ ] **B4 — `cmd/ask/ssformat.go` (new): output.** `formatPlain(result)` (answer only,
      NO ANSI styling) and `formatJSON(result)` (the §4.2 object). A single
      `singleShotResult` struct carries question/image/answer/kind/command/ran/source/
      degraded/error.
- [ ] **B5 — Exit-code mapping** per §4.3, in one helper so it is asserted once.
- [ ] **B6 — Tests** (§ below). All green: `go build ./... && go vet ./... &&
      go test ./... && gofmt -l .` clean.
- [ ] **B7 — Docs:** flip the README "Medium" entry from "Fix pending" to done; add a
      one-line usage note to `SKILL.md`. (Do NOT edit CLAUDE.md/COLLAB/README in the
      cloud branch beyond the single Medium-issue line if the workflow permits; else
      leave it for local.) `build-all-tools.bat` auto-discovers — no edit; the binary is
      still `cmd/ask`.
- [ ] **B8 — LOCAL (hardware):** run `becky-ask --image <real frame> --question "..."`
      against the real LFM2.5-VL; confirm the answer + exit 0; confirm `--question`
      with the live Qwen refinement; **confirm the colored TUI still opens on a normal
      double-click / `becky-ask` in a console** (the accessibility invariant). Paste
      evidence.

### Unit tests (assert VALUES, not truthiness)

- [ ] `TestFlags_QuestionSelectsSingleShot` — `--question "x"` sets single-shot;
      no flags + (faked) TTY does NOT.
- [ ] `TestSingleShot_DoesNotLaunchTUI` — the single-shot path never calls
      `tea.NewProgram` (inject a TUI-launcher seam / spy; assert it is not invoked when
      `--question` is set). **The accessibility guard.**
- [ ] `TestInteractiveDefaultPreserved` — with no single-shot flag and a terminal, the
      code selects the bubbletea launch branch (assert the chosen mode == `modeTUI`).
- [ ] `TestSingleShot_TextQuestion_Catalog` — `--question "can becky transcribe?"`
      (nil model) → plain answer names `becky-transcribe`; exit 0.
- [ ] `TestSingleShot_Action_ShowOnly` — `--question "transcribe this" --target f` →
      prints `becky-transcribe "f"`, `ran=false`, exit 0; does NOT execute.
- [ ] `TestSingleShot_Action_Run` — same with `--run` and a faked runner → `ran=true`,
      exit code propagated.
- [ ] `TestSingleShot_JSON_Shape` — `--json` emits exactly one object with the §4.2
      keys and correct `kind`/`command`/`source` values.
- [ ] `TestSingleShot_Image_FakeVision` — `--image f.png --question "..."` with a fake
      `visionAsker` returning a known description → that description is the answer;
      `kind=="image"`, `source=="lfm2.5-vl"`.
- [ ] `TestSingleShot_Image_Degrades` — fake vision returns `Degraded:true` → plain
      degrade note + exit 0 (and `{"degraded":true}` in `--json`).
- [ ] `TestSingleShot_ImageWithoutQuestion_Usage` — `--image f.png` alone → exit 2.
- [ ] `TestSingleShot_MissingImage_Usage` — `--image nope.png` → exit 2.
- [ ] `TestSingleShot_PlainOutput_NoANSI` — plain mode output contains no ESC `\x1b`
      sequences (scripts must get clean text).
- [ ] `TestNoTTYPathsUnchanged` — `BECKY_ASK_RUN` and `printNoTTY` behavior unchanged
      when no single-shot flag is set (regression guard for §3.4).

---

## 7. Open Decisions for Jordan

- **D1 — Image path: shell `becky-vision` vs import `internal/vision`.** Default
  (this spec): shell the sibling binary (keeps the single-tool principle; inherits its
  degrade contract). Importing `internal/vision` directly is slightly faster (no
  process spawn) but couples the binaries. Confirm shell-the-sibling.
- **D2 — Should `--question` accept stdin?** e.g. `echo "..." | becky-ask --question -`
  or `becky-ask --question-stdin`, so a pipeline can stream a question without
  shell-quoting. Default: not in v1 (flag value only); add if a pipeline needs it.
- **D3 — `--run` for image-derived actions.** v1 `--run` only applies to a TEXT
  question that classifies as an action on a target. An image question only ever
  ANSWERS (no tool to run). Confirm that's the intended scope.
- **D4 — JSON to stdout while plain notes go to stderr?** v1 puts the single answer
  object on stdout and keeps any diagnostics (model-spawn logs) off stdout entirely, so
  `--json` stdout is always parseable. Confirm.
- **D5 — Default image prompt.** If `--question` is omitted for `--image` we exit 2
  (usage). Alternative: default to becky-vision's built-in "describe this image"
  prompt. Default here: require `--question` (the feature is *ask*, not *describe*).
  Confirm.
- **D6 — Re-verify externals at build time.** The Qwen intent model id, LFM2.5-VL
  GGUF, and `llama-mtmd-cli` invocation drift; re-verify against
  `SPEC-BECKY-VISION-MODELS.md` + `internal/freshness/manifest.json` before building B3.

---

## 8. Sources / references (in-repo grounding)

- The gap: `README.md` "Known issues → Medium" (becky-ask interactive-only).
- The accessibility invariant: `ACCESSIBILITY.md` (sighted, impaired vision; keep the
  colored TUI; do NOT replace it for "accessibility").
- The existing ask design this extends: `SPEC-BECKY-ASK.md` (the chat front-door; the
  intent → router → run brain; act-vs-discuss gate; opt-in safety posture §2.5).
- The output contract: `FORENSIC-OUTPUT-PHILOSOPHY.md` (plain words; say what's known;
  corroborate-then-conclude — single-shot must render the underlying tools' findings
  faithfully and add no new hedging).
- Real code grounded:
  - `becky-go/cmd/ask/main.go:main` (TTY guard; `runHeadless`; `printNoTTY`;
    `BECKY_ASK_RUN`) — where the mode switch is added.
  - `becky-go/cmd/ask/router.go:route` / `routed` — the single routing entry point
    single-shot calls.
  - `becky-go/cmd/ask/intent.go:classify` / `classifyDeterministic` / `decision` —
    the act-vs-discuss gate; nil-model degrade; deterministic+catalog fallback.
  - `becky-go/cmd/ask/llama.go:newLlamaClient` / `Ready` — the local Qwen client and
    its honest absence handling.
  - `becky-go/cmd/ask/run.go:binPathFor` / `runCommand` / `resolveIntentModel` — how
    becky-ask resolves and runs sibling becky-* binaries (the image path reuses this).
  - `becky-go/cmd/ask/actions.go:commandFor` / `commandString` — the exact, assertable
    command rendering.
  - `becky-go/cmd/ask/target.go:resolveTarget` — argv/path → `Target`.
  - `becky-go/cmd/vision/main.go` + `becky-go/internal/vision/vision.go`
    (`Describe`, `Result{Degraded,Error}`, `Provenance`, injectable `runner`) — the
    local LFM2.5-VL VLM the `--image` path drives; the degrade contract it inherits.
  - The VLM model choice: `SPEC-BECKY-VISION-MODELS.md` (LFM2.5-VL, image-only;
    Gemma-4 stays for audio).
