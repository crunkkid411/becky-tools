# Go GUI iteration & AI design tooling — research

**Question (Jordan's):** is there anything like **21st.dev Magic MCP** or **superdesign** for **Go**
GUI/TUI design, and is there a fast "edit code → SEE the window change" loop for **Gio** (the
toolkit `becky-canvas` is pinned to in `GUI-RULES.md`)? He works visually, has low vision, and the
AI agents need to verify UI changes **without a human in the loop**.

Written 2026-06-22. Every link is real and was fetched/searched live; nothing here is invented.
This is honest about the tension with the `GUI-RULES.md` "no embedded browsers" pin — see §6.

---

## TL;DR — lead with the answer

1. **No Magic-MCP / v0 / superdesign equivalent exists that OUTPUTS Go (Gio or Fyne) code.** Every
   one of those tools emits **web** code (React/TypeScript/Tailwind, or raw HTML/CSS/SVG). They are
   trained on the gigantic web-component corpus that simply does not exist for Go GUI. So the AI-UI-
   generator path and the Gio path do **not** meet today — you can't ask Magic to "make a Gio mixer."

2. **The single best "see UI updates in real time" loop for becky-canvas, that an AGENT can drive
   headlessly and Jordan can also watch, is: add a `becky-canvas --render-frame out.png` headless
   mode that renders ONE Gio frame to a PNG via `gioui.org/gpu/headless`, then have the agent audit
   that PNG with `becky-vision`.** This is a few hours of work, needs no new toolkit, no browser, and
   is faster + more deterministic than launching a real window and OS-screenshotting it (the current
   habit). It is the natural completion of the `BECKY_CANVAS_MODE` screenshot hook already in the code.

3. **For genuine hot-reload of Gio (edit `.go` → window mutates with state preserved), `got-reload`
   exists and has a Gio demo — but it's beta and brittle.** Use it as an *optional* developer
   accelerator, not as the verification path. The robust loop is file-watch (`wgo`/`air`) → rebuild
   → re-render the headless PNG, which on this project is ~1–3s and is the dependable workhorse.

4. **The one big decision Jordan/we must make** (§6): do we keep the `GUI-RULES.md` "native pixels
   only, no browsers" pin and accept that AI-design tools can only produce *throwaway HTML mockups*
   the agent then re-implements in Gio by hand — **OR** do we admit a **web-frontend lane (Wails v3 /
   plain HTML preview)** specifically for the *design-exploration* surface, which directly conflicts
   with that pin but unlocks Magic-MCP/superdesign/Vite-hot-reload wholesale. My recommendation:
   **keep the pin for the shipped app, and use superdesign/HTML purely as a non-shipped "mood-board
   → Gio" step.** Best of both, no WebView2 in the product.

---

## Recommended concrete workflow for becky-canvas (build this)

This builds directly on what the repo already does (`BECKY_CANVAS_MODE` deterministic-screenshot
hook in `cmd/canvas/gui.go`, the `becky-vision` VLM, the launch→screenshot→audit habit in MEMORY).

### A. The agent's verification loop (no human needed) — HIGH VALUE, build first

```
1. agent edits the Gio layout code (e.g. cmd/canvas/gui_mixerpanel.go)
2. agent runs:  becky-canvas --render-frame X:\tmp\frame.png --mode mixer --size 1180x760
                (headless: build a gtx, lay out ONE frame, render via gioui.org/gpu/headless → PNG.
                 No window, no GPU display needed, deterministic, exits 0.)
3. agent runs:  becky-vision --image X:\tmp\frame.png --question "Is the mixer readable? Are the
                fader/pan/mute controls aligned and high-contrast? Anything overlapping or cut off?"
4. agent reads the verdict, edits again, repeats — a tight closed loop with NO human and NO real window.
5. Only when it looks right does Jordan open the real window for the hear/see gates.
```

Why this is the right primitive:
- It's **toolkit-pinned-safe** — pure Gio, no browser, satisfies `GUI-RULES.md`.
- `gioui.org/gpu/headless` renders an op list straight to an image buffer with no visible window —
  this is exactly what golden-image tests use, so it's a supported, stable path.
- It closes the "compiles ≠ done" gap that CLAUDE.md keeps fighting: the agent can now SEE its own UI
  output, not just assert it compiled.
- It gives Jordan (low vision) the same PNG — and `becky-vision` can also *describe* it aloud later
  via the new TTS, so he doesn't have to strain to read a dense panel.

### B. The fast dev loop (for a human or agent iterating quickly)

```
wgo -file=.go go run -tags gui ./cmd/canvas -- --render-frame frame.png --mode mixer
```
`wgo` (or `air`) re-runs on every `.go` save → a fresh `frame.png` lands in ~1–3s on this repo.
Pair it with any image viewer that auto-refreshes (or re-run `becky-vision`). This is the dependable
"edit → see" loop. It is a **rebuild**, not in-process hot-reload, but at becky-canvas's size it's
fast enough and never gets into the brittle-interpreter failure modes of true hot reload.

### C. Optional accelerator: true Gio hot reload via got-reload (beta, opt-in)

For sub-second "tweak a layout function, watch the live window re-layout without losing state," wire
`got-reload` against `cmd/canvas` (it has a working **Gio** demo, `got-reload/giodemo`). Treat it as
a *nice-to-have* for a focused styling session, **never** as the verification gate — it's beta, it
can't reload `main`/`init` or change signatures, and Yaegi has known closure/variable bugs.

### D. The design-exploration step (where the "Magic/superdesign" idea actually fits)

When Jordan wants to explore a *new* layout from scratch ("design me a better mixer"), let an AI
design tool generate **throwaway HTML/CSS mockups** as a mood-board, then have the agent re-implement
the chosen direction in Gio. **superdesign** is the best fit here because it ships as a **Claude Code
skill** (no API key, runs locally, writes `.superdesign/*.html` you can open/screenshot). The HTML is
**never shipped** — it's a reference image, exactly like a Figma export. This respects the no-browser
pin (the product stays pure Gio) while still giving Jordan the visual, generate-variations workflow
he liked about 21st.dev.

---

## 1. AI-assisted UI generation — does anything target Go? (No.)

| Tool | What it outputs | Runs in | Could it emit Go GUI? |
|---|---|---|---|
| **21st.dev Magic MCP** | React + TypeScript + Tailwind components (multiple variations) | Cursor/Windsurf/VS Code/Claude as an MCP server (needs API key) | **No** — web only |
| **superdesign** (superdesigndev) | HTML / CSS / SVG mockups + components (also React/Vue), 1–5 variations, auto-preview canvas | VS Code/Cursor/Windsurf extension **and** a **Claude Code skill / MCP** (local, no API key) | **No** — web/markup only |
| **v0** (Vercel) | React/Next + Tailwind | Web app | **No** — web only |
| **Claude "Design" / artifacts** | HTML/React | Claude apps | **No** — web only |

**Finding:** there is **no** Magic-MCP-style "natural language → Gio/Fyne widget tree" generator, and
there is unlikely to be one soon — these tools are LLM wrappers riding the enormous public React/HTML
training corpus, which has no Go-GUI analogue. The closest workable path is **(a)** generate web
mockups with superdesign/Magic as *references*, then **(b)** have a coding agent (Claude) translate
the chosen mockup into Gio by hand. The agent writes Gio fine from a clear spec + a reference image;
what's missing is the *generative variation* step, which superdesign supplies on the web side.

Note on superdesign being **framework-agnostic at the prompt level**: it can be told to emit React or
Vue, but its native, reliable output is HTML/CSS/SVG into `.superdesign/` with a live canvas preview.
That HTML-as-mood-board is the usable bridge for a Go project.

---

## 2. The Gio dev loop today (the honest state)

Gio is an **immediate-mode** toolkit (you re-describe the whole UI every frame). There is **no
official hot-reload**. The realistic options, fastest-iteration-last:

1. **Plain rebuild** — `go run -tags gui ./cmd/canvas`. Reliable, ~1–3s here, zero extra tooling.
2. **File-watch + rebuild** — `wgo` or `air` in front of the run command. Same as above but automatic
   on save. **This is the practical "edit → see" loop.** `wgo` is the leaner choice (one binary,
   "slap `wgo` in front of any command"); `air` is heavier (`.air.toml`) but popular. Both work on
   Windows/PowerShell.
3. **Headless render-to-PNG** — `gioui.org/gpu/headless` renders an op list to an image with **no
   window at all**. This is the agent-friendly path (workflow A above) and is *faster* than launching
   a real window because there's no window/GPU-present round-trip. **This is the key enabler and it's
   already 90% wired** — `BECKY_CANVAS_MODE` exists for deterministic screenshots; it just needs a
   `--render-frame` flag that calls the headless GPU instead of opening `app.NewWindow`.
4. **True in-process hot reload** — `got-reload` (BSD-3, **beta**) rewrites function bodies and swaps
   them at runtime via the **Yaegi** Go interpreter; **has a Gio demo** (`got-reload/giodemo`) where
   you edit a layout function and see it change live with state preserved. Caveats: can't reload
   `main`/`init`, can't change signatures/add types/modify package vars, no cgo, Yaegi closure bugs,
   "might work in your project, might not." **Opt-in accelerator, not a gate.**

**Fastest practical loop = #2 (watch+rebuild) feeding #3 (headless PNG), audited by becky-vision.**

---

## 3. Alternative Go GUI frameworks — iteration ergonomics & AI-assistability

| Framework | Dev-loop / preview | Headless render-to-image (agent-verifiable) | AI-tool compatibility | License | Windows | Verdict for becky |
|---|---|---|---|---|---|---|
| **Gio** (pinned) | rebuild; `got-reload` beta hot-reload (Gio demo exists) | **Yes** — `gioui.org/gpu/headless` → PNG | low (no generator; agent hand-writes) | MIT/Unlicense | D3D11, no cgo, excellent | **Keep.** Add `--render-frame`. |
| **Fyne** | rebuild; `fyne` CLI = build/package/`serve`(wasm), **no hot-reload** | **Yes, best-in-class** — `test.AssertRendersToImage` / `Capture()` / `NewCanvasWithPainter` render any canvas to a PNG **in a unit test, fully headless** | low (no generator) | BSD-3 | good (needs a C compiler / OpenGL) | Strong *testing* story; switching toolkits is a bigger move than adding a render flag to Gio. Not worth a migration. |
| **Wails** (v2 WebView2 / **v3** alpha) | **`wails dev` = true Vite hot-reload** of the web frontend (Svelte/React/Vue/Lit/vanilla); auto-rebuilds Go too | via browser/devtools screenshot of the webview | **High** — frontend is web, so Magic-MCP/superdesign/v0 all apply directly | MIT | v2 = **WebView2** (the exact thing `GUI-RULES.md` retired); v3 aims at more-native webviews but still web-tech UI | **Conflicts with the pin.** Only reconsider as a *separate design-lane*, see §6. |
| **gocv** | n/a (it's OpenCV bindings, not a GUI toolkit) | renders frames to image (that's its job) | n/a | Apache-2.0 | needs OpenCV | Not a GUI framework — keep for vision, not UI. |

Key contrasts:
- **Best AI-tooling compatibility = Wails**, *because* its UI is web — which is also exactly why it
  breaks the no-browser rule. That tension is the whole decision (§6).
- **Best headless visual-test ergonomics = Fyne** (`AssertRendersToImage` is a turnkey golden test),
  but **Gio can do the same thing** via `gpu/headless`; it's just slightly more code. Given the pin,
  the heavy investment in Gio panels, and that a migration buys no *generation*, **stay on Gio and
  add the render-to-PNG mode** rather than switch to Fyne.

---

## 4. TUI design tooling (for the Bubble Tea TUIs like becky-ask)

The project's colored TUIs are a Charm/Bubble Tea stack. The good news: **the agent-verifiable loop
already exists off-the-shelf here, and it's excellent.**

- **VHS** (`charmbracelet/vhs`) — scriptable terminal recorder. A `.tape` file (`Type "..."`,
  `Enter`, `Sleep`, `Screenshot out.png`, `Output demo.gif`) drives the TUI and produces **both a PNG
  screenshot AND an animated GIF** deterministically, headlessly (it runs the program in a pseudo-
  terminal). **This is the TUI analogue of the becky-canvas render-frame idea** — an agent writes a
  tape, runs VHS, gets a PNG, audits it with `becky-vision`, and Jordan gets a GIF of the before/after.
  This is the single highest-leverage TUI tool to adopt.
- **Lipgloss** — declarative terminal styling (the colored layout layer); pairs with `lipgloss/table`.
- **Huh** — forms/prompts (`charmbracelet/huh`) if becky-ask wants structured input.
- **Hot reload for TUIs** — `air`/`wgo` rebuild-and-restart works, but TUIs are so fast to rebuild
  that VHS-driven snapshots are usually the better feedback signal than a live restart.
- **"AI generates a TUI layout" tool?** — **None exists.** Same reason as §1 (no training corpus).
  The practical substitute: the agent writes Lipgloss/Bubble Tea by hand, then **VHS → PNG →
  becky-vision** verifies it. No human needed.

---

## 5. Why the headless-PNG + becky-vision loop is the right call (synthesis)

- It **unifies** the GUI and TUI stories: Gio `gpu/headless` → PNG for the canvas; VHS → PNG for the
  TUI. Same downstream auditor (`becky-vision`), same "agent sees its own output" closed loop.
- It **respects every pin in `GUI-RULES.md`**: no browser, native pixels, Go-only on the cloud side,
  deterministic, degrade-never-crash.
- It **completes infrastructure that's already here**: `BECKY_CANVAS_MODE`, the launch→screenshot
  habit (MEMORY: `becky-canvas-icons-and-vision-loop.md`), and `becky-vision`. We're adding one flag
  and one tape format, not a new architecture.
- It **serves Jordan's low vision directly**: the agent pre-screens the UI so Jordan only opens the
  window when it's already plausibly right; and the PNG can be *described aloud* via the new TTS.

---

## 6. The tension we must surface (don't hide it)

`GUI-RULES.md` §0 pins the product to **native pixels, no embedded browser** (WebView2 retired as the
root cause of becky-clip's lag). **Every AI-UI-generation tool Jordan likes (Magic, superdesign, v0)
and the only Go framework with turnkey Vite hot-reload (Wails) are web-tech.** So there is a genuine
fork:

- **Option A (recommended): Keep the pin. Use web AI-design tools only as a non-shipped mood-board.**
  superdesign (as a Claude Code skill) generates HTML mockups → agent re-implements the winner in
  Gio → headless-PNG + becky-vision verifies. The shipped app stays pure, fast, native. Cost: the
  "generate 5 variations" magic only exists at the *mockup* stage, not on live Gio code; the agent
  does the Gio translation.

- **Option B: Admit a web design-lane (Wails v3 / live HTML preview) for exploration only.** Unlocks
  Magic-MCP/superdesign/Vite hot-reload wholesale on a real, clickable surface. Directly contradicts
  the no-browser rule if it ever leaks into the shipped path; Wails v2 on Windows *is* WebView2. Only
  sane if rigidly fenced to throwaway prototypes.

- **Option C: Wait for a Go-native AI-UI generator.** It doesn't exist and there's no signal it's
  coming. Not a plan.

**My recommendation: Option A.** It gives Jordan the visual, variation-driven design experience he
asked about *and* keeps the radio-ready native app the rules demand — with the headless-PNG +
becky-vision loop as the durable, human-free verification engine underneath both the GUI and the TUI.

---

## 7. Sources (all real, fetched/searched 2026-06-22)

- 21st.dev Magic MCP: https://21st.dev/magic · https://github.com/21st-dev/magic-mcp · https://deepwiki.com/21st-dev/magic-mcp
- superdesign: https://github.com/superdesigndev/superdesign · https://docs.superdesign.dev/cli-skill-tutorial · https://www.npmjs.com/package/@superdesign/cli · https://deepwiki.com/superdesigndev/superdesign
- Gio: https://gioui.org/ · https://github.com/gioui/gio · headless GPU render: https://pkg.go.dev/gioui.org/gpu/headless · https://pkg.go.dev/gioui.org/gpu
- got-reload (Gio hot reload, beta): https://github.com/got-reload/got-reload · Gio demo: https://github.com/got-reload/giodemo
- wgo (live reload): https://github.com/bokwoon95/wgo · https://matthewsetter.com/live-reload-go-projects-wgo/
- air (live reload): https://blog.logrocket.com/using-air-go-implement-live-reload/
- Fyne: https://github.com/fyne-io/fyne · CLI tools: https://pkg.go.dev/fyne.io/tools · headless render-to-image: https://pkg.go.dev/fyne.io/fyne/v2/test (`AssertRendersToImage`, `Capture`, `NewCanvasWithPainter`) · https://github.com/fyne-io/fyne/discussions/5544
- Wails (Vite hot reload, dev mode): https://wails.io/docs/guides/application-development/ · v3 alpha: https://v3alpha.wails.io/
- VHS (scriptable TUI screenshots/GIFs): https://github.com/charmbracelet/vhs
- Lipgloss: https://github.com/charmbracelet/lipgloss · Charm: https://github.com/charmbracelet
- (in-repo) GUI-RULES.md "no embedded browsers" pin; cmd/canvas/gui.go `BECKY_CANVAS_MODE` hook; MEMORY becky-canvas-icons-and-vision-loop.md
