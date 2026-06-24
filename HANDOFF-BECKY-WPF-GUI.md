# HANDOFF — the native becky GUI is **WPF** (C#/.NET, Windows-native). The minimal "becky window", built to finish.

> **DECISION (Jordan, 2026-06-24):** the becky GUI is a **native WPF app** (C#/.NET on Windows). This
> **supersedes the Go+Gio `becky-canvas` attempts** (3 of them failed this week — Gio has no widgets, so every
> button was hand-drawn; that's the root cause, not the agent). **No browsers, no web views, no localhost/server**
> (Jordan's hard constraint — servers are fragile on his box; even an MCP server dropped mid-conversation proving
> the point). WPF is native, fast (compiled, GPU-composited), has real widgets + huge resources, and Claude Code
> builds it reliably (it already built a working Qt chat inside Shotcut). becky's Go tools are **NOT rewritten** —
> the WPF window just **shells out to the existing `becky-*.exe`** (JSON in / JSON out) and shows the result.
>
> This is THE becky GUI going forward. v1 below is a **tool-runner** (use becky without prompting Claude Code);
> it grows into the voice shell (becky-voice, Phase 0 brain already built + on master) and richer panels later.

---

## RULES FOR THE BUILDING AGENT (same as `HANDOFF-BECKY-VOICE.md` — not optional)
1. **Finish to a WORKING, SCREENSHOTTED window.** "It compiles" is NOT done. Done = the window opens, the named
   buttons work, you **screenshot it**, and the DONE box is truthfully checkable. You CAN screenshot + click
   (Win32) — use that to verify every step, like a human would.
2. **NEVER make Jordan run a CLI command or answer a settled technical question.** Decide from this doc. If truly
   blocked, use a **form** (chips) or **whoretana voice** — never a jargon dump, never "run X and paste output."
3. **One step = one finished deliverable.** Drive it to its DONE box; don't stop each increment to ask.
4. **Reuse the existing `becky-*.exe` tools; never reimplement one.** The GUI is a thin shell over them.
5. **High-contrast, large, readable** per `ACCESSIBILITY.md` (Jordan's palette) — color is an accessibility AID,
   keep it.

---

## What v1 IS (scope — deliberately small so it actually ships)
A single native window where Jordan: **picks a file/folder → sees the list of becky tools → clicks one → it runs
the real tool → the result shows in the window.** That's it. No DAW, no timeline, no voice yet. The point is:
**becky becomes usable without a terminal or a Claude Code prompt.**

## Architecture (native, no server)
```
  [ WPF window (.NET 8, C#) ]
     • startup: read the tool list from `becky catalog --json` (Step 1)
     • a file/folder picker (native OpenFileDialog / drag-drop)
     • a button per tool (label + plain summary, color-coded by tier green/yellow/red)
     • click -> Process.Start("becky-<tool>.exe", <args>) -> capture stdout(JSON)+stderr(headline)
     • show: the plain-English headline BIG, the output in a scroll area, a "open output file" button
     • degrade: missing exe / error -> a clear message, never a crash
```
No localhost, no web view — just a `.exe` launching other `.exe`s. The Phase-0 `internal/catalog` (already on
master) is the single source of truth for the tool list + tiers.

---

## STEPS (each to its DONE box)

### Step 1 — `becky catalog --json` (CLOUD-buildable, Go, verifiable now)
- **WHAT:** a tiny subcommand that prints the `internal/catalog` as JSON (per tool: `name`, `summary`,
  `example`, `tier`, `pack`) — the data the WPF reads at startup.
- **HOW:** add to `cmd/becky` (or `cmd/catalog`) a `catalog --json` path that marshals `internal/catalog`.
- **WHY:** one source of truth for the tool list; the GUI never hardcodes tools, and stays in sync with the Go side.
- **VERIFY (cloud):** `becky catalog --json` exits 0 and emits a JSON array; a Go test asserts the shape + that a
  known tool (e.g. `becky-transcribe`, tier green) is present. `go build/vet/test ./...` + `gofmt` clean.
- **DONE:** the command prints the catalog JSON; tested. *(Cloud will build + prove this and hand it over.)*

### Step 2 — WPF scaffold + tool list (LOCAL)
- **WHAT:** `dotnet new wpf` app `BeckyWindow`; on startup run `becky catalog --json` and render one button per
  tool (label + summary, tier color).
- **VERIFY:** build, run, **screenshot**: the window opens and shows the REAL tool list from the catalog.
- **DONE:** window opens with the live tool list (screenshot attached), no crash.

### Step 3 — run a tool, show the result (LOCAL)
- **WHAT:** clicking a tool runs the real `becky-<tool>.exe` (with the picked file as input) via `Process`,
  captures stdout+stderr, shows the headline big + output in a scroll area + "open output file".
- **VERIFY:** pick a real file, click a **GREEN** tool (e.g. transcribe), **screenshot** the result in the window.
- **DONE:** a real becky tool runs from a click and its result shows in the window — no terminal used.

### Step 4 — file picker + high-contrast styling + degrade (LOCAL)
- **WHAT:** native file/folder picker + drag-drop; apply Jordan's high-contrast palette + large text; a missing
  tool / error shows a clear message, never crashes.
- **VERIFY:** **screenshot** the styled window; test a missing-exe path shows a message (no crash).
- **DONE:** readable, file-pickable, degrades cleanly.

### Step 5 — the mouse+keyboard Definition-of-Done (LOCAL, `CANVAS-NORTH-STAR.md`)
- Open it → click **three** different tools on a real file → see three real results → resize/scroll → it never
  freezes. **Screenshot each.** Report what worked / what degraded **via the form or voice**, not a jargon dump.
- **DONE:** Jordan can open the window and use three becky tools end-to-end without touching a terminal.

## LATER (after v1 ships and is used)
- A text box → `becky-ask` (type a request in plain English). Then **voice** (wire `becky-voice` Phase 1/3 — the
  Phase-0 brain is already on master). Then richer panels (timeline, drum, mixer) as native WPF controls.

## The one-command cloud proof handed to local
`becky catalog --json` (Step 1) — runs the real catalog, emits JSON, tested, exit 0. If that's green, Steps 2–5
are "build the provided WPF project + fix what the screenshots show," not "design a GUI from scratch."
