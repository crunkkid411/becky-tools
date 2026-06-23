# Go packages & frameworks, explained plainly (for Jordan)

**Bottom line:** Using good libraries is the *right* instinct, not a guilty pleasure. In
Go they almost never make your program slow or bloated at runtime — a well-written
library is usually *faster* than code you'd hand-roll. The real costs are about *trust
and upkeep*, not speed. And becky-tools itself is genuinely lean.

---

## Part A — The explainer

### The three words, in plain terms

Think of building a tool like cooking a meal.

| Term | What it is | Kitchen analogy |
|------|-----------|-----------------|
| **Standard library** | Code that ships *inside* Go itself — free, always there, no download. | The stove, sink, knives, and tap water that come with the kitchen. |
| **Package** | One focused bundle of code someone else wrote that you pull in to do *one* job (read a WAV file, draw a button). | A single jarred ingredient — a good store-bought pesto. |
| **Framework** | A *big* package that wants to run the show and you build *inside* it. | A meal-kit subscription: the box dictates the recipe; you follow its structure. |

A package is something you *call*. A framework is something that *calls you* — it owns
the shape of your program. becky uses lots of packages and (deliberately) very few
frameworks.

### `go.mod` and `go.sum`, one line each

- **`go.mod`** — the shopping list: the exact outside packages this project uses, and which versions.
- **`go.sum`** — the receipt: a tamper-proof fingerprint of each one, so nobody can swap in a different (malicious) version behind your back.

### The core question: do dependencies cause bloat or slowness?

**Myth vs reality:**

| The worry | The honest Go reality |
|-----------|----------------------|
| "A big library bloats my program." | Go's linker does **dead-code elimination** — it throws away every function you don't actually call. Pull in a huge library, use one function, and only that one function's worth of code ships. |
| "More dependencies = slower program." | Dependencies almost never affect **runtime speed**. A mature library is usually *faster* than hand-rolled code, because experts already optimized it over years. |
| "Each dependency is a heavy thing to ship." | Go compiles to **one single file** (a static binary) with no runtime to install. A lean CLI tool is a few megabytes total — the whole thing, dependencies included. |
| "Dependencies are basically free, then." | No — they have *real* costs, just **not the ones people fear**. See below. |

So where the worry is usually pointed (speed, size) is mostly wrong in Go. The costs are
real but **different**:

1. **Trust / supply-chain risk** — you're running a stranger's code. A popular,
   widely-audited package is safe; an obscure one-person project is a question mark.
   (`go.sum` guards against *tampering*, but not against the author themselves being careless.)
2. **Abandonment risk** — if the author walks away, *you* inherit the bugs and the
   security patching. The fix: prefer libraries that are popular and actively maintained.
3. **Compile time & binary size** — real, but **modest** in Go. A GUI library adds a few
   seconds to builds and a few MB to that one binary. Not a runtime problem.
4. **Coupling / learning cost** — every library is one more thing to understand, and the
   more your code wraps around it, the harder it is to swap out later.

### When a library genuinely helps vs when hand-rolling is fine

**Reach for a battle-tested library when the job is hard to get *exactly* right:**
- File formats and codecs — *don't hand-roll a WAV decoder* or a MIDI parser; the edge cases (odd bit depths, malformed headers) will bite you.
- Security-sensitive code — crypto, auth, parsing untrusted input. Rolling your own here is how real vulnerabilities get born.
- Complex, well-trodden domains — audio engines, GUI toolkits, databases. Years of other people's bug-fixes are baked in.

**Hand-roll when it's small, simple, and yours:**
- A one-off helper, a tiny bit of glue, a format *you* invented.
- Logic so simple that a dependency would be more code (and more risk) than just writing the ten lines.
- The rule of thumb: *don't add a dependency to save yourself five lines; do add one to save yourself five hundred — or to avoid a class of bug you can't see.*

### How this maps to becky's own rules

becky already says, in its own words: **"prefer battle-tested libraries over hand-rolled
solutions"** and **"research existing implementations before writing new code."** That's
exactly the right call — and this explainer is the *why* behind it.

The honest flip side, which becky's own notes admit: the project has sometimes erred by
**hand-rolling things mature tools already do well** (hand-building a DAW and a video
editor in raw code), then pivoting to *drive* proven tools instead (REAPER for audio,
kdenlive for video). That pivot **is** this principle in action — let the experts' code do
the hard part; becky stays the smart brain on top.

---

## Part B — Reality check: is becky-tools lean or bloated?

I read `becky-go/go.mod` and `go.sum`. Here are the actual numbers.

### The headline

becky-tools is **lean and its dependency choices are sensible.** For a project of **73
command-line tools + 83 internal packages**, it leans on only **9 direct dependencies**.
Most of the heavy lifting is done with Go's standard library and becky's own hand-written
code (the pure-Go WAV decoder, MIDI writer, FFT/key-detection, etc.) — which is the
correct instinct for a forensic tool that must stay deterministic and offline.

### The numbers

- **9 direct dependencies** (the ones becky deliberately chose).
- **~26 indirect dependencies** (pulled in *by* those 9 — mostly tiny terminal-color and text-layout helpers).
- **~61 modules total** across the whole tree. For 156 packages of code, that's a **small** footprint.

### The notable (heaviest) ones — and whether they're justified

| Dependency | What it's for | Used by | Verdict |
|-----------|---------------|---------|---------|
| `gioui.org` (Gio) | The native GUI window toolkit | becky-canvas, drummachine, nle (27 files) | **Justified** — hand-rolling a windowing/GPU toolkit is exactly the "don't do it yourself" case. |
| `modernc.org/sqlite` | The local case database | 1 file (`internal/beckydb`) | **Justified** — and a *smart* pick: a pure-Go SQLite, so no C compiler needed. Don't hand-roll a database. |
| `charmbracelet/bubbletea` + `bubbles` + `lipgloss` | The colored, high-contrast TUI (becky-ask) | ~9 files | **Justified** — this is Jordan's accessibility aid (the high-contrast palette). Battle-tested, hugely popular. |
| `jchv/go-webview2` | The window for becky-clip | 1 file | **Justified** — uses the browser engine already in Windows; no bundled browser shipped. |
| `hypebeast/go-osc` | Talks to music apps over OSC | 2 files | **Justified** — a standard protocol; not worth re-implementing. |
| `golang.org/x/...` (term, image, shiny, text, net, sys) | Official Go extensions | scattered | **Justified** — these are effectively first-party Go, the safest dependencies that exist. |

The ~26 indirect packages are almost all small, reputable terminal/text utilities
(`go-runewidth`, `go-colorful`, `uniseg`, `go-humanize`) dragged in by the GUI and TUI
libraries — normal and harmless.

### Is binary size or dep count a real concern here? No.

The dead-code-elimination story shows up clearly in becky's *own* built binaries:

- **Lean CLI forensic tools** (no GUI): **~3–4 MB each** — e.g. `becky-transcribe` 3.8 MB, `becky-report` 3.3 MB, `becky-stems` 3.2 MB. These don't touch Gio or SQLite, so none of that code ships in them.
- **GUI tools**: **~12–18 MB** — e.g. `becky-canvas` 17.9 MB, `becky-drummachine` 12.9 MB. Bigger *only because* a real GUI toolkit is genuinely large code.

That spread is the proof: a tool only pays for what it actually uses. The ~3 MB CLI tools
sit in the same project, with the same `go.mod`, as the 18 MB GUI app — and stay tiny,
because Go's linker strips everything they don't call.

### One-paragraph verdict

becky-tools is **lean, not bloated.** Nine carefully-chosen direct dependencies for 156
packages of code is disciplined; every heavy one (GUI, SQLite, the colored TUI) is a
textbook "don't hand-roll this" case, and the pure-Go SQLite choice even sidesteps a C
toolchain. Binary size is a non-issue: the forensic CLI tools are ~3 MB and only the GUI
apps are large, exactly as Go's dead-code elimination predicts. If anything, becky's
*instinct to use good libraries is under-used*, not over-used — its occasional mistake has
been hand-rolling complex tools (a DAW, an NLE) that mature software already does well,
which it has since corrected by driving REAPER and kdenlive instead.
