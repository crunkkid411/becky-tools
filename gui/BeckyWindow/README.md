# BeckyWindow — the native WPF "becky window" (v1: tool runner)

This is the **native Windows GUI** decided in `GUI-RULES.md` (2026-06-24) and specced in
`HANDOFF-BECKY-WPF-GUI.md`. It is a real `.exe` — **no browser, no web view, no server.** It reads becky's
tool list and runs becky tools on a file you pick, showing the result. It does **not** reimplement any tool; it
shells out to the existing `becky-*.exe`.

> **Honest note from the cloud agent:** I wrote this complete C# project but **could not compile or run it** (no
> Windows / .NET / display in the cloud). It is a finished app, **not a stub** — but the local agent must build
> it, run it, **screenshot it**, and fix any compile nits (handed you 95%, not a blank page). The Go half it
> talks to (`becky-catalog --json`) **is** built + tested + proven by the cloud agent.

## Build & run (local, Windows)
```
# needs the .NET 8 SDK (winget install Microsoft.DotNet.SDK.8)  -- one-time
cd gui/BeckyWindow
dotnet build          # compile
dotnet run            # launch the window
```
The window calls `becky-catalog --json` at startup, so **`becky-catalog.exe` and the `becky-*.exe` tools must
be on PATH** (the same place `build-all-tools.bat` puts them). `becky-catalog` is a normal `cmd/catalog` tool —
`build-all-tools.bat` auto-discovers it.

## What it does (v1)
1. **Pick file...** → native file dialog → the chosen path shows at the top.
2. A **button per becky tool** (loaded live from the catalog; border colored by tier — green/yellow/red).
3. **Click a tool** → runs `becky-<tool> "<your file>"`, shows the plain-English headline (from stderr) big and
   the full output below. A **red**-tier tool asks for confirmation first.
4. **Degrades, never crashes:** a missing/failed tool shows a message, the window stays alive.

## Definition of Done (the part only your machine can do — `CANVAS-NORTH-STAR.md`)
Build it, run it, and **screenshot each**:
- [ ] window opens, high-contrast, shows the **real tool list** from the catalog (not hardcoded).
- [ ] **Pick file...** selects a real video/audio file.
- [ ] clicking a **green** tool (e.g. `becky-transcribe`) runs it and the result shows in the window — **no
      terminal used.**
- [ ] a **red** tool (`becky-export`) prompts before running.
- [ ] resize / scroll — it never freezes.
- [ ] (degrade) rename a tool's exe away → clicking it shows a clean "not found" message, no crash.

Report what worked / what degraded **via the form or whoretana voice**, not a CLI/jargon dump (CLAUDE.md §2).

## Files
- `BeckyWindow.csproj` — .NET 8 WPF project.
- `App.xaml` / `App.xaml.cs` — app entry.
- `MainWindow.xaml` — the layout (high-contrast).
- `MainWindow.xaml.cs` — catalog load + tool buttons + run-tool + degrade logic.

## Next (after v1 ships, per the work order)
A text box → `becky-ask`; then **voice** (wire `becky-voice` — the Phase-0 brain is on master); then richer
native WPF panels (timeline / drum / mixer).
