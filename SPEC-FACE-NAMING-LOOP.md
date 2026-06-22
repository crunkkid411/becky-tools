# SPEC-FACE-NAMING-LOOP.md — the "who is this?" naming loop (cluster → name → enroll)

> **SPEC — NOT BUILT, AWAITING JORDAN'S APPROVAL.**
> Design only. No Go code has been written; nothing in `becky-go/` is changed. The
> upstream `becky-cluster` and the enroll/identify paths this loop wires together
> ALREADY EXIST (cited below) — this spec is the glue, not a redesign of any of them.
> Jordan approves before any build starts.
>
> Authored 2026-06-22. Read with: `ACCESSIBILITY.md` (load-bearing — Jordan is
> SIGHTED with impaired vision, NO screen reader), `SPEC-PERSON-CLUSTERING.md` (the
> upstream `becky-cluster` this loop CONSUMES), `SPEC-BECKY-ASK.md` +
> `cmd/ask/styles.go` (the colored-TUI pattern to mirror),
> `FORENSIC-OUTPUT-PHILOSOPHY.md` (naming = the human supplying ground truth).

---

## 1. Purpose + user need

Turn anonymous **recurring** faces/voices into **named KB enrollees fast, offline,
and eyes-friendly** — with **zero cloud-LLM credits**.

becky-cluster already converts "unidentified face" into "**Person A, appears in 41
clips**" (`cmd/cluster/main.go` `Cluster` struct: `MemberCount`,
`DistinctSourceFiles`, `Representative`, `SuggestedName *string` left `nil` until a
human names it). What is missing is the **human-in-the-loop close**: a fast way for
Jordan to *look at* a cluster's representative face, *type the name*, and have the
**whole cluster enrolled under that name** — so `becky-identify` recognizes that
person corpus-wide from then on. SPEC-PERSON-CLUSTERING §7.3 named this exact
"name-once → enroll" step as the close and left it unbuilt; this spec builds it.

Two deliverables:

1. **`becky-name`** — a high-contrast colored TUI that walks a `becky-cluster`
   output JSON: shows one cluster at a time (representative face image + the big
   readable facts), takes a typed name, and **enrolls the whole cluster** under that
   name by shelling out to the existing `becky-enroll` teach path.
2. **The inline "teach me" remedy in `becky-identify`** — when identify leaves
   someone unnamed, the output should tell you how to fix it, inline:
   `not enrolled — teach me: becky "this is <name>" <clip>`. This is the explicit
   "Medium" gap in README ("Enrollment UX … output should include the remedy
   inline", README:241-242).

This is the planned feature in README:254-255 ("Face-naming 'who is this?' loop …
`cluster` → `becky-ask` → `enroll` the cluster"), made concrete. (We use a dedicated
`becky-name` TUI rather than overloading `becky-ask` — `becky-ask` is a chat
front-door; the naming loop is a focused review-one-card-at-a-time flow that wants
its own model. It REUSES `cmd/ask/styles.go` so the palette is identical.)

### Why it matters (forensic grounding)

A cluster is a `[CANDIDATE]` same-person grouping, never an identity conclusion
(SPEC-PERSON-CLUSTERING §8; `cmd/cluster/main.go` Notes `honesty`). **Naming a
cluster is the human supplying ground truth** — the one legitimate way a CANDIDATE
becomes a named identity per `FORENSIC-OUTPUT-PHILOSOPHY.md`. The loop preserves
that: nothing is named until Jordan looks at the representative frame and types the
name. From that point the name is `verified_by = "human:cluster-name"` (the path
SPEC-PERSON-CLUSTERING §7.4 already reserves).

---

## 2. The workflow design

```
becky-cluster <clips|--db>           → clusters.json   (Person A/B/…, no names yet)
        │
        ▼
becky-name --clusters clusters.json --kb kb-final
        │   colored TUI, one card per cluster:
        │     ┌──────────────────────────────────────────┐
        │     │  Who is this?            (1 of 6)          │   ← title bar (pink)
        │     │  [ representative face image ]            │   ← image preview
        │     │  Person A — seen in 41 clips (38 files)   │   ← BIG cyan facts
        │     │  cohesion 0.71  ·  voice                  │   ← dim quality line
        │     │  Name ▸ ______________________            │   ← LARGE green input
        │     │  Enter = enroll   s = skip   q = quit     │   ← amber help
        │     └──────────────────────────────────────────┘
        │   human types "Braxton", presses Enter
        ▼
becky-enroll --clip <each cluster member clip> --name "Braxton" --kb kb-final
        │   (the EXISTING single-clip teach path, cmd/enroll/clip.go runLearnClip)
        ▼
kb-final/{voice-prints,face-prints}/Braxton/…   → becky-identify now names Braxton
```

And the second, independent deliverable closes the loop from the *other* end:

```
becky-identify clip.mp4 --kb kb-final
        → unidentified[]:
            { "type":"face", "description":"unidentified face",
              "confidence":0.41,
              "remedy":"not enrolled — teach me: becky \"this is <name>\" <clip>" }
```

So whether Jordan starts from a corpus sweep (`cluster → name`) or from a single
identify run (read the remedy, run `becky "this is …"`), the path to a named KB
entry is one obvious step.

### 2a. cluster → review card → name → enroll (the TUI loop)

`becky-name` reads a `becky-cluster` `Output` JSON (the exact struct in
`cmd/cluster/main.go:39` — `Clusters []Cluster`, each with `ClusterID`,
`MemberCount`, `DistinctSourceFiles`, `Cohesion`, `Representative`, `Members []Member`).
It does NOT re-cluster and does NOT compute embeddings — it consumes what
`becky-cluster` already produced. For each cluster:

1. **Show the card** — representative face image (`Cluster.Representative`, the
   strongest member's clip/frame becky-cluster already selected in
   `cmd/cluster/main.go` `buildCluster`) + the big facts ("Person A — seen in 41
   clips, 38 files; cohesion 0.71; voice").
2. **Take the name** — large green text input (mirrors `inputStyle` in
   `cmd/ask/styles.go:34`). Blank + Enter or `s` = **skip** (leave
   `SuggestedName` null, move on — never guess). `q` = quit.
3. **Enroll the whole cluster** — on a typed name + Enter, call the enroll seam for
   each member clip under that name (see §3c), appending to the KB.
4. **Advance** to the next cluster; show a one-line confirmation
   ("Enrolled Braxton from 41 clips → kb-final"; or the honest partial:
   "Enrolled Braxton: voice ✓, face skipped (no clean frame)").

Determinism + honesty: clusters are presented **in the order `becky-cluster` emitted
them** (biggest first — `cmd/cluster/main.go` sorts by `MemberCount` desc). A skip
is recorded, not a name invented. Low-`cohesion` clusters are flagged on the card
("loose grouping — double-check") per SPEC-PERSON-CLUSTERING §8.

### 2b. The inline "teach me" remedy in identify

In `cmd/identify/face.go`, the unnamed path appends
`Unidentified{Type:"face", Description:"unidentified face", Confidence: bestSim}`
(face.go:114-118); the voice path does the same in `voice.go`. Add a `Remedy` field
to `Unidentified` (`cmd/identify/main.go:63`) populated with the literal:

```
not enrolled — teach me: becky "this is <name>" <clip>
```

`<clip>` is filled with the actual input file (`Output.File`, identify/main.go:35);
`<name>` stays a literal placeholder (the human supplies it — mirrors how
`cmd/ask/plan.go` `adaptCommand` fills resolved paths but leaves user-value
placeholders like `<query>`/`<name>` intact). This is a pure string addition to a
deterministic, already-tested code path — **fully cloud-buildable + testable**.

---

## 3. CLI / TUI shape + image display + the enroll call

### 3a. `becky-name` CLI

```
becky-name --clusters <clusters.json> --kb <kb-dir> [options]

  --clusters <file>   becky-cluster Output JSON (required)
  --kb <dir>          knowledge base to enroll into (required; appended, never clobbered)
  --bin <dir>         dir holding becky-enroll/becky-diarize (default: dir of this exe)
  --modality face|voice|both   only review clusters of this modality (default: both)
  --min-clips N       only review clusters with >= N members (default: from the file)
  --device cpu|cuda   passed through to becky-enroll
  --names <file>      NON-INTERACTIVE: apply a {cluster_id: name} map (no TUI; headless/CI)
  --dry-run           show what WOULD be enrolled (the enroll argv per cluster); enroll nothing
```

becky-name is a **thin orchestrator** in the becky house style: it reads JSON, draws
the TUI, and shells out to the EXISTING `becky-enroll` for the actual embedding —
exactly as `cmd/enroll/runners.go` shells out to `becky-diarize` rather than
reimplementing it (runners.go:7 "thin orchestrator: it CHAINS the existing
binaries"). No new model, no new embedding code.

### 3b. The colored TUI (ACCESSIBILITY.md is load-bearing)

Built on **bubbletea + lipgloss + bubbles** (`textinput`), the SAME stack as
`becky-ask` (`cmd/ask/main.go:29`, `cmd/ask/model.go:25-27`). It **reuses
`cmd/ask/styles.go` verbatim** — the neon-green/pink/amber/cyan palette is an
accessibility AID for Jordan, not decoration (ACCESSIBILITY.md fact #2: "Keep colored
TUIs. Do NOT strip color … do NOT replace a colored TUI with plain monochrome text
'for accessibility'"). Concretely:

- **Title bar** ("Who is this?  (1 of 6)") → `titleBarStyle` / `promptStyle` (pink).
- **The big facts line** ("Person A — seen in 41 clips") → `targetBarStyle` (cyan,
  bold — it reads as live state, styles.go:48-51) at a large, uncluttered size.
- **Name input** → `textinput` colored with `inputStyle` (green, styles.go:34) — the
  one place Jordan types, kept big and obvious.
- **Quality/skip line** ("cohesion 0.71 · voice") → `systemStyle` (dim, styles.go:29).
- **Help footer** ("Enter = enroll · s = skip · q = quit") → `helpStyle` (styles.go:38).
- **Busy** ("enrolling Braxton…") → `busyStyle` (amber bold, styles.go:58).

Prompts stay **concise** (ACCESSIBILITY.md fact #1: lead with the answer, no walls
of text). One cluster per screen — never a scrolling wall of all six.

**No-TTY guard, mirrored from becky-ask** (`cmd/ask/main.go:41`): if stdin is not a
terminal, becky-name does NOT launch bubbletea. With `--names <file>` it applies the
map headlessly and exits; with nothing, it prints what it parsed (cluster count,
modalities) and exits 0. This keeps the loop **scriptable + headless-testable** and
crash-free off a terminal — and is what lets cloud verify the orchestration without a
display (§5).

### 3c. In-terminal image display (the real constraint — see Open Decisions)

The card needs to SHOW the representative face. A terminal can't natively render a
JPEG, so options, in preference order:

1. **External viewer (default, most robust):** `becky-name` opens
   `Cluster.Representative` in the OS image viewer (Windows: `start`, via a detached
   `os/exec` with no console window — reuse `internal/proc.NoWindow`, the
   CREATE_NO_WINDOW helper becky-clip already uses to avoid console flash). The TUI
   stays the keyboard surface; the photo opens beside it. Zero terminal-graphics
   dependency, works everywhere.
2. **Inline terminal graphics (nicer, terminal-dependent):** if the terminal
   supports it, draw the image *in* the card via the Kitty graphics protocol or
   iTerm2 inline images (sixel as a fallback). Detected at startup; falls back to
   option 1 when unsupported. Windows Terminal's protocol support is the gating
   unknown — hence an Open Decision, not a hard requirement.
3. **Pre-extracted thumbnail:** if `Representative` is a video clip rather than a
   still, becky-name first extracts the representative frame to a temp JPG via
   `osintexport.ExtractFrameRotated` (the SAME rotation-corrected extractor
   `cmd/identify/face.go:163` and `cmd/enroll/runners.go:96` use) at the member's
   `Timestamp` — so the face shown is upright (the F1 rotation lesson,
   SPEC-PERSON-CLUSTERING §2) and matches what was clustered.

The image-display method is a **display-side concern behind a small interface**
(`type imageShower interface { Show(path string) error }`), so the cluster-walk /
name-capture / enroll-dispatch LOGIC is testable headless with a fake shower (§5).

### 3d. The enroll call (reusing the existing teach path)

Naming cluster `face-A` "Braxton" enrolls EVERY member clip under "Braxton" by
invoking the EXISTING single-clip teach path — `becky-enroll --clip <clip> --name
"Braxton" --kb <kb>` (`cmd/enroll/main.go:74` routes `--clip`+`--name` to
`runLearnClip`, `cmd/enroll/clip.go:43`). That path already:

- **APPENDS** to the KB without clobbering existing people (clip.go:5-10).
- Picks a clean voice span + the clearest **single-face** frame, reusing the
  enroll machinery (`enrollPerson`, enroll.go:61), which already applies the
  **face-collision guard** (`bestSingleFace` requires `NFaces == 1`,
  enroll.go:358-370) — the Shelby bug SPEC-PERSON-CLUSTERING §7.3 warns about.
- **SKIPS with a recorded reason** rather than fabricating (enroll.go:120-123) —
  becky-name surfaces that reason on the card ("face skipped: no clean frame").
- Writes the entity record so identify shows the friendly name (clip.go:120-127).

For a cluster of N clips, becky-name calls the teach path per distinct member clip
(deduped by `Member.SourceFile`), so all of that person's good frames/spans across
the cluster enrich one KB identity. Members already arrive **strongest-first**
(becky-cluster sorts by `DetScore` desc in `buildCluster`), so the best frame is
tried first. becky-name caps the number of clips it teaches from per cluster
(e.g. top 5 by det score) to keep enrollment bounded — the rest are provenance, not
needed for a good print.

becky-name does NOT re-implement any enroll logic — it only chooses which clips/name
to hand to `becky-enroll`. That keeps the single-tool principle intact (CLAUDE.md
§1): one tool clusters, one tool enrolls, one tool names-and-dispatches.

---

## 4. Deterministic / offline / degrade-never-crash

- **Zero cloud-LLM credits, fully offline.** No model call in becky-name at all —
  it reads JSON, shows an image, takes a typed string, and shells out to
  `becky-enroll` (which runs the same local sherpa/InsightFace stack identify uses).
  The "AI" here is the human reading a face. The identify remedy is a static string.
- **Deterministic.** Cluster order is fixed (becky-cluster's emitted order). The
  enroll argv for a given cluster + name is a pure function of the inputs (testable
  with golden argv, like `cmd/ask/plan_test.go` asserts adapted commands). `--names`
  map mode is fully reproducible.
- **Degrade-never-crash.** Missing image viewer / unsupported terminal graphics →
  fall back (external viewer → "image at: <path>" text line) and keep going, never
  panic. A member clip that won't enroll → record the skip reason on the card and
  continue to the next cluster (mirrors enroll's per-person skip discipline). A
  malformed clusters.json → a plain-language error and exit, not a stack trace
  (`beckyio.Fatalf`, the toolset convention). No-TTY → headless/parse mode, exit 0
  (§3b).
- **Read-only sources.** becky-name never modifies the original clips; it only
  reads the clusters JSON and writes into the KB via becky-enroll (which is itself
  append-only). The clusters.json is not modified — naming results go to the KB +
  an optional `--out named.json` audit (which clusters got which names + skip log).

---

## 5. Cloud-vs-local split (design the seam so logic is testable headless)

The split follows the model/hardware boundary (CLAUDE.md §4).

| Cloud-buildable + TESTABLE (here)                                  | Local-only (Jordan's PC: display + models)            |
|--------------------------------------------------------------------|-------------------------------------------------------|
| The **identify `Remedy` string** + its wiring in the unnamed path  | The actual face/voice **embedding + enroll** (InsightFace / sherpa-onnx + ffmpeg) |
| The **cluster-walk state machine** (next/skip/name/quit)           | **In-terminal image rendering** + the OS image viewer |
| The **enroll-argv builder** (`cluster + name → becky-enroll argv`) | Running becky-enroll on real clips, the GPU path      |
| The **`--names` headless apply** path (no TTY, no display)         | The visual TUI on a real terminal (colors on screen)  |
| JSON load/validate, dedupe-by-source, dry-run output               | Confirming Braxton is then named by `becky-identify`  |

**The testable seam:** define two interfaces so all decision logic runs headless —

```go
type imageShower interface { Show(path string) error }      // local: real viewer; test: record-only fake
type enroller interface { Enroll(clip, name, kb string) (EnrollOutcome, error) } // local: exec becky-enroll; test: fake
```

The cluster-walk, name capture, skip handling, per-cluster clip selection, and argv
construction all run against these fakes in unit tests (no display, no models, no
ffmpeg). This is the same discipline becky-cluster used (`fake.go` deterministic
fakes for the network step, SPEC-PERSON-CLUSTERING-style) and that `cmd/ask`'s
no-TTY mode enables. Cloud ships #1–#4 green; local wires the real `imageShower` +
real `exec.Command("becky-enroll", …)` enroller and runs the visual loop. Per the
PROVABLE-HANDOFF rule (CLAUDE.md §2), cloud ships a one-command offline proof:
`becky-name --clusters fixture.json --kb /tmp/kb --names map.json --dry-run` prints
the exact enroll argv per cluster — measurable, no hardware.

---

## 6. Build plan + unit tests

Cloud builds the deterministic halves first; the visual + model wiring is the
local completion step.

**Phase A — identify remedy (smallest, highest-value, fully cloud-verifiable)**
- [ ] Add `Remedy string \`json:"remedy,omitempty"\`` to `Unidentified`
      (`cmd/identify/main.go:63`).
- [ ] In `cmd/identify/face.go` (face.go:114) and `voice.go` (the parallel unnamed
      path), set `Remedy` to `not enrolled — teach me: becky "this is <name>" <clip>`
      with `<clip>` = the input file (`Output.File`), `<name>` left literal.
- [ ] Factor a `remedyLine(clip string) string` helper so the exact string has one
      source of truth (and is unit-assertable).
- [ ] **Test** `TestRemedyLine_ExactString` — assert the produced string EQUALS
      `not enrolled — teach me: becky "this is <name>" <clip-path>` for a sample
      clip (assert the VALUE, not truthiness — STANDARDS-ENGINEERING).
- [ ] **Test** `TestIdentify_Unidentified_HasRemedy` — a synthetic below-threshold
      face/voice produces an `Unidentified` whose `Remedy` is the exact string.
- [ ] **Test** `TestIdentify_Named_NoRemedy` — a named identification carries no
      remedy (the field is omitempty + empty).

**Phase B — becky-name orchestration core (cloud-buildable + testable headless)**
- [ ] New `cmd/name/` + `internal/naming/` (loader, walk state machine, argv
      builder, `--names` apply, dry-run). Pure logic over the `imageShower` /
      `enroller` interfaces.
- [ ] Read + validate a `becky-cluster` `Output` JSON; degrade-never-crash on bad
      input.
- [ ] `enrollArgs(cluster Cluster, name, kb, bin string) [][]string` — the per-clip
      `becky-enroll --clip … --name … --kb …` argv (deduped by `SourceFile`, capped,
      strongest-first).
- [ ] `applyNames(clusters, map[clusterID]name, fakeEnroller)` — the headless apply.
- [ ] **Test** `TestEnrollArgs_GoldenArgv` — assert the exact argv slice for a
      2-clip cluster named "Braxton" (paths filled, name quoted, kb passed).
- [ ] **Test** `TestEnrollArgs_DedupesAndCaps` — a cluster with repeated
      `SourceFile`s yields one argv per distinct clip, capped at the limit.
- [ ] **Test** `TestApplyNames_WiresClusterToEnroll` — with a FAKE enroller,
      naming `face-A`="Braxton" calls Enroll once per distinct member clip with
      name "Braxton" (assert the recorded calls — cluster→enroll wiring with fakes,
      exactly as the prompt requires).
- [ ] **Test** `TestApplyNames_SkipLeavesUnnamed` — a blank/skip entry produces NO
      enroll calls and records the skip.
- [ ] **Test** `TestLoadClusters_BadJSON_Degrades` — malformed JSON → typed error,
      no panic.
- [ ] **Test** `TestDryRun_PrintsArgvNoEnroll` — `--dry-run` records zero real
      Enroll calls and emits the argv (the offline proof from §5).
- [ ] **Test** `TestNoTTY_HeadlessParse` — non-terminal stdin with no `--names`
      prints the parsed summary and exits 0 (mirrors `cmd/ask` no-TTY).

**Phase C — the visual TUI (LOCAL — needs a terminal + display)**
- [ ] bubbletea Model-Update-View for the card (reuse `cmd/ask/styles.go`); the
      large name `textinput`; Enter/skip/quit keys.
- [ ] Real `imageShower`: external OS viewer (detached, `internal/proc.NoWindow`),
      with optional inline Kitty/iTerm2/sixel if detected; representative-frame
      extraction via `osintexport.ExtractFrameRotated` when `Representative` is a
      video.
- [ ] Real `enroller`: `exec.Command("becky-enroll", …)`; surface skip reasons on
      the card.

**Phase D — local verification (the hardware step cloud cannot do)**
- [ ] `becky-cluster` a real corpus slice → `becky-name --clusters … --kb kb-final`
      → see a face, type a name, confirm the KB gains
      `face-prints/<name>/` + `voice-prints/<name>/`.
- [ ] Re-run `becky-identify <a member clip> --kb kb-final` → that person is now
      NAMED (the loop closed corpus-wide).
- [ ] `build-all-tools.bat` (auto-discovers `cmd/name`).

Every fixed bug ships a regression test; tests assert values, not truthiness
(STANDARDS-ENGINEERING, the five gates).

---

## 7. Open Decisions for Jordan

1. **In-terminal image display method.** Default is the **external OS image viewer**
   beside the TUI (most robust, works on any terminal). Do you want becky-name to
   ALSO try **inline** images (Kitty/iTerm2/sixel) when the terminal supports it —
   accepting that Windows Terminal's support is the gating unknown — or keep it
   simple with the external viewer only for v1?
2. **Voice clusters in the same loop?** The card can play the representative voice
   clip (the `Representative` for a voice cluster is a `.wav`) instead of/along with
   showing a face — voice is the more reliable modality (SPEC-PERSON-CLUSTERING §6).
   Do you want `becky-name` to handle voice clusters too in v1 (play the clip, then
   name), or face-first and add voice next?
3. **How many clips per cluster to enroll from** (default cap: top 5 by det score).
   More clips = a richer print but slower enrollment. Confirm the cap.
4. **Voice input later (deferred, NOT built here).** Naming by SPEAKING the name
   instead of typing is a natural fit and a long-standing want — but it depends on
   ASR (`becky-transcribe`) for input and good TTS (`SPEC-BECKY-TTS.md`, the
   researched engine — Piper/Kokoro already ruled out, ACCESSIBILITY.md fact #5) for
   spoken prompts. This spec keeps it **typed** for v1 and references those specs as
   the optional future channel; confirm that's the right v1 scope.
5. **Write-back to `identifications` rows** (SPEC-PERSON-CLUSTERING §7.4,
   `verified_by = "human:cluster-name"`) so `becky-consolidate` instantly reflects
   "Braxton recognized in 41 videos" — include in v1, or keep v1 to KB enrollment
   only and add the DB write-back next?
