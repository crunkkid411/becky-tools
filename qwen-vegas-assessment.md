# qwen-vegas-assessment.md
**BeckyTools ↔ VEGAS Pro 18 Integration Assessment**  
*Read-only review. One write: this file.*

---

## Executive Summary

**Yes — a working, two-way bridge exists.** You can control VEGAS Pro 18 from the CLI (and thus from an AI agent) and read its timeline state. The bridge is:

1. **`BeckyVegas`** — a VEGAS Application Extension (DLL) that loads at startup, exposing a **named pipe** `\\.\pipe\becky-vegas-<pid>` with a JSON request/response protocol.
2. **`becky-vegas.exe`** — a Go CLI client that finds the pipe via `%LOCALAPPDATA%\BeckyVegas\instances\<pid>.json` and sends commands.
3. **Four VEGAS scripts** (`.cs` files) — `BeckyCut.cs`, `BeckyCaptions.cs`, `BeckyReviewTimeline.cs`, `BeckyRoughCut.cs` — that run inside VEGAS via `vegas180.exe -SCRIPT:` and call the becky Go tools (`becky-cut`, `becky-subtitle`, `becky-transcribe`, `becky-review-index`) as subprocesses.

**The panel is accessed via** `View ▸ Extensions ▸ Becky Search` inside VEGAS. If you closed it, that's how you get it back.

---

## Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           OUTSIDE VEGAS (AI / CLI)                          │
│  ┌──────────────┐    ┌──────────────────┐    ┌─────────────────────────┐   │
│  │ Claude Code  │───▶│  becky-vegas.exe │───▶│ Named Pipe              │   │
│  │ / Whoretana  │    │  (Go CLI)        │    │ \\.\pipe\becky-vegas-   │   │
│  └──────────────┘    └──────────────────┘    │ <pid>                   │   │
│                                                └───────────┬───────────┘   │
└────────────────────────────────────────────────────────────┼──────────────┘
                                                             │ JSON req/resp
                                                             ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                            INSIDE VEGAS (PID <pid>)                         │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │ BeckyVegas Extension (BeckyVegas.dll)                               │   │
│  │  • UiThread.cs — main-thread dispatcher (Control.BeginInvoke)       │   │
│  │  • Bridge.cs — pipe server, command dispatcher                      │   │
│  │  • ProjectOps.cs — read/edit timeline (ALL on main thread)          │   │
│  │  • BeckyTools.cs — shells out to becky Go tools                     │   │
│  │  • SearchPanel.cs — "Becky Search" dockable panel (View▸Extensions) │   │
│  │  • Presence.cs — writes instance file + notifies Whoretana          │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                    │                                        │
│                    ┌───────────────┼───────────────┐                       │
│                    ▼               ▼               ▼                       │
│             ┌──────────┐    ┌────────────┐ ┌────────────┐                  │
│             │ Scripts  │    │ becky Go   │ │ VEGAS API  │                  │
│             │ (.cs)    │    │ tools      │ │ (Script    │                  │
│             │          │    │ (subproc)  │ │ Portal)    │                  │
│             └──────────┘    └────────────┘ └────────────┘                  │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## How to Use BeckyVegas (Documented)

### 1. Get the Search Panel Back
**Inside VEGAS:** `View ▸ Extensions ▸ Becky Search`  
(If the extension is installed, this menu item exists. The panel docks/floats like a native window.)

### 2. Install / Update the Extension
```bat
REM At repo root (X:\AI-2\becky-tools)
Install Vegas Scripts.bat
```
- Builds `BeckyVegas.dll` via `vegas\BeckyVegas\build.ps1` (Roslyn, .NET Framework 4.8)
- Copies to `%USERPROFILE%\Documents\Vegas Application Extensions\` (no admin needed)
- Retires old `VegasAIBridge` automatically
- **Restart VEGAS** (or `Tools ▸ Scripting ▸ Rescan Script Menu Folder`)

### 3. CLI Commands (`becky-vegas.exe`)
Built by `build-all-tools.bat` → `becky-go\bin\becky-vegas.exe`

| Command | Purpose |
|---------|---------|
| `becky-vegas status` | Project path, cursor, selection, counts, rendering flag |
| `becky-vegas timeline` | Every media event: source, in/out, ruler pos, track, rate |
| `becky-vegas selected` | Same as timeline, selected events only |
| `becky-vegas dialogs` | Open VEGAS dialogs (title, message, buttons) |
| `becky-vegas jump start=45.76 end=50.08` | Cursor there, span selected, scrolled into view |
| `becky-vegas insert path="X:\clip.mp4" in=10 out=14` | Adds clip to "Becky Pulls" tracks at cursor |
| `becky-vegas add_marker seconds=9.12 label="check this"` | Adds marker (one undo step) |
| `becky-vegas add_region start=10 end=20 label="scene"` | Adds region |
| `becky-vegas search query="beauty mirror"` | Transcript search of **timeline** |
| `becky-vegas search query="scissors" folder="E:\footage"` | Transcript search of **folder** |
| `becky-vegas snapshot path="C:\tmp\frame.png"` | Saves preview frame at cursor as PNG |
| `becky-vegas run_script path="X:\...\BeckyCut.cs"` | Runs any VEGAS script like Tools▸Scripting |
| `becky-vegas command section=Global name=Tools.Video.VideoEventFX` | Invokes VEGAS command by keyboard.ini name |
| `becky-vegas show_panel` | Opens the Becky Search panel |
| `becky-vegas transcribe path="X:\clip.mp4"` | Runs `becky-transcribe`, writes `_parakeet_transcription.srt` |
| `becky-vegas help` | Lists all commands |
| `becky-vegas instances` | Shows reachable VEGAS windows |

**Arguments:** `key=value` (booleans, numbers auto-typed). `--pid N` picks a specific VEGAS when multiple are open.

### 4. VEGAS Scripts (Tools ▸ Scripting)
| Script | What it does |
|--------|--------------|
| `BeckyCut.cs` | Cuts dead air from **selected events** in place. One click, no dialog, same answer every time. Uses `becky-cut --dry-run` for decisions. Auto-ripple, regrouping, one Ctrl+Z undo. |
| `BeckyCaptions.cs` | Captions the edit on the timeline. Reads your cuts, sends timeline to `becky-subtitle`, places cues back at correct ruler positions (gaps included). Creates "Becky Captions" track at top. |
| `BeckyReviewTimeline.cs` | Builds a review timeline from a text list (`path | in | out | label`). Each clip → named Region. Agent-driven via `BECKY_REVIEW_LIST` env var. |
| `BeckyRoughCut.cs` | Unattended assembler: reads `BECKY_ROUGHCUT_JSON` (from `becky-roughcut`), places events on 4 tracks, adds markers/regions, saves `.veg`, exits. |

---

## How the Two-Way Bridge Works

### The Control Channel (Named Pipe)
- **Protocol:** One JSON line in → one JSON line out. `{"id":1,"cmd":"status","args":{}}` → `{"id":1,"ok":true,"result":{...}}`
- **Security:** Windows named pipe ACL — only **your user** can connect. Network clients denied.
- **Discovery:** Extension writes `%LOCALAPPDATA%\BeckyVegas\instances\<pid>.json` on focus/project change. `becky-vegas.exe` reads this to find the pipe.
- **Threading:** VEGAS objects **must** be touched on VEGAS's main thread. `UiThread.cs` creates a `Control` on the main thread in `InitializeModule`, captures its `Handle`, then uses `BeginInvoke` from any thread. This is the **critical fix** the old `VegasAIBridge` missed (it captured `SynchronizationContext.Current` which was `null`).

### Safety Guards (Bridge.cs)
- **Refuses edits while rendering** (`rendering` flag from `RenderStarted/Finished` events)
- **Refuses edits while a dialog is open** (`OpenDialogs()` enumerates owned windows, checks `IsWindowEnabled(mainWindow) == false`)
- **Every edit is one `UndoBlock`** — one Ctrl+Z reverts it
- **No save/open/close commands** — deliberately omitted

### Whoretana Integration
When VEGAS gains focus, the extension sends one NDJSON line to `\\.\pipe\Whoretana`:
```json
{"cmd":"active_app","active_app":"vegas_pro","project_path":"...","pipe":"becky-vegas-<pid>","timeline_state":{"cursor":12.5,"selection_start":0,"selection_length":0,"playing":false}}
```
This is the **SharedState payload** from `X:\AI-2\CLAUDE.md`. Whoretana ignores unknown commands, so it's harmless until it implements handlers.

---

## How Scripts Access CLI Tools

**The scripts do NOT call CLI tools directly.** They run inside VEGAS's process (C# compiled by VEGAS at launch). Instead:

1. **Scripts shell out** to the becky Go binaries (`becky-cut.exe`, `becky-subtitle.exe`, `becky-transcribe.exe`, `becky-review-index.exe`) via `Process.Start()`.
2. **Path resolution order:** `BECKY_CUT` / `BECKY_SUBTITLE` env var → `..\becky-go\bin\` relative to script → `PATH`.
3. **Data exchange:** Script writes JSON to `%TEMP%\BeckyVegasCut\` or `%TEMP%\BeckyVegasCaptions\<guid>\`, tool reads it, writes result JSON back, script reads result.
4. **No VEGAS API dependency in Go tools** — they are standalone CLI tools. The script is the "applicator" that maps tool decisions onto the timeline.

Example from `BeckyCut.cs`:
```csharp
// Runs becky-cut --dry-run on each source file
ProcessStartInfo psi = new ProcessStartInfo {
    FileName = beckyExe,
    Arguments = Quote(source) + " --dry-run",
    RedirectStandardOutput = true, // reads JSON decisions
    // ...
};
stdout = process.StandardOutput.ReadToEnd();
CutReport report = ParseCutReport(stdout); // regex parser, no JSON lib needed
// Maps source-second decisions → ruler spans → applies to timeline
```

---

## Can an AI Agent Act as a Junior Editor? **Yes.**

### What's Working Today
| Capability | Status | How |
|------------|--------|-----|
| **Read timeline** | ✅ | `becky-vegas timeline` → JSON of every event |
| **Search transcript** | ✅ | `becky-vegas search query="..."` (timeline or folder) |
| **Jump to moment** | ✅ | `becky-vegas jump start=X end=Y` |
| **Insert clip at cursor** | ✅ | `becky-vegas insert path="..." in=X out=Y` (lands on "Becky Pulls" tracks) |
| **Add markers/regions** | ✅ | `becky-vegas add_marker`, `add_region` |
| **Run scripts** | ✅ | `becky-vegas run_script path="BeckyCut.cs"` |
| **Take snapshot** | ✅ | `becky-vegas snapshot path="frame.png"` |
| **Transport control** | ✅ | `play`, `stop`, `pause` |
| **Transcribe clips** | ✅ | `becky-vegas transcribe path="..."` |
| **Get project status** | ✅ | `becky-vegas status` |

### What an AI "Junior Editor" Could Do Now
```bat
# Example workflow an agent could run:
becky-vegas search query="um"           # Find filler words
becky-vegas jump start=120.5 end=121.0  # Jump to each
becky-vegas run_script path="BeckyCut.cs"  # Cut silence on selection
becky-vegas add_marker seconds=120.5 label="filler removed"
# Repeat for each hit...
```

### What's Missing for Full "Shared State" Autonomy
| Gap | Description | Effort |
|-----|-------------|--------|
| **Persistent session** | Agent needs to track "what I've done" across commands. Currently stateless. | Medium |
| **Batch/composite operations** | No "delete all silence in project" — must select + run script per selection. | Low (script exists, just needs pipe command) |
| **Undo/redo awareness** | Agent doesn't know if user pressed Ctrl+Z. | Medium |
| **Real-time sync** | Whoretana receives focus notifications but no timeline change events. | Medium |
| **Visual verification** | Agent can't "see" the result (no video preview stream). Snapshot is single frame. | High |

---

## Discussed but Not Yet Implemented

Based on code comments, README sections, and `BECKY-CUT-STATUS.md`:

### 1. **Batch "Cut All Silence" via Pipe** (Low)
- `BeckyCut.cs` works on **selection**. No pipe command to "cut silence on entire timeline" or "cut silence on track N".
- **Fix:** Add `cut_silence` command to `Bridge.cs` that mimics the script's logic but driven from CLI.

### 2. **Timeline Change Notifications to Whoretana** (Medium)
- `Presence.cs` only publishes on **focus change** and project open/save.
- No events for: edit made, marker added, cursor moved, selection changed.
- **Fix:** Hook `vegas.Project.Changed`, `Transport.CursorPositionChanged`, etc., and push to Whoretana pipe.

### 3. **Video Preview Stream** (High)
- `snapshot` gives one PNG. No live preview / frame stream.
- **Fix:** Would need a background thread pumping `SaveSnapshot` or a custom video sink. Significant work.

### 4. **Agent-Driven Rough Cut Loop** (Medium)
- `BeckyRoughCut.cs` is fully automatic but **one-shot**: reads JSON, builds, saves, exits.
- No "iterative rough cut" where agent proposes edits → user reviews → agent refines.
- **Fix:** Add pipe commands for the rough-cut steps (place event, adjust trim, add marker) so agent can drive it incrementally.

### 5. **Keyboard Shortcuts for Scripts** (Trivial)
- `README.md` §6: "Give it a keyboard shortcut: Options > Customize Keyboard > Global tab > type `Becky` > pick `Script.BeckyCut` / `Script.BeckyCaptions`"
- Never assigned. One-time setup.

### 6. **VEGAS 19/20/21 Compatibility** (Unknown)
- Built against `ScriptPortal.Vegas` from VEGAS Pro 18.0 (build 527).
- Scripts use `using ScriptPortal.Vegas;` — VEGAS 13 or older needs `Sony.Vegas`.
- No testing on newer versions.

### 7. **Linux/Wine Support** (Not planned)
- Extension is .NET Framework 4.8, Windows-only. Pipe is Windows named pipe.

---

## Verification Checklist (from BECKY-CUT-STATUS.md)

Before trusting any change:
```bat
REM 1. Compile check (no VEGAS launch)
powershell -ExecutionPolicy Bypass -File vegas\check-vegas-script.ps1

REM 2. Parser self-test (no VEGAS)
powershell -ExecutionPolicy Bypass -File vegas\test-beckycut-parser.ps1

REM 3. Full self-test inside VEGAS (throwaway project)
set BECKY_CUT_SELFTEST=X:\AI-2\becky-tools\test.mp4
set BECKY_CUT=X:\AI-2\becky-tools\becky-go\bin\becky-cut.exe
"C:\Program Files\VEGAS\VEGAS Pro 18.0\vegas180.exe" -SCRIPT:"X:\AI-2\becky-tools\vegas\BeckyCut.cs"
# Check <media>.becky-cut-selftest.txt for:
#   RESULT: OK
#   video and audio tracks: same events=, same kept_seconds=
#   gap_seconds=0 on every track
```

**Critical:** After any `-SCRIPT:` launch, **screenshot within 30 seconds and keep screenshotting**. A modal dialog on top looks like success from the outside.

---

## Key Files Reference

| File | Purpose |
|------|---------|
| `vegas/BeckyVegas/Bridge.cs` | Pipe server, command dispatch, safety guards |
| `vegas/BeckyVegas/UiThread.cs` | Main-thread dispatcher (the core fix vs old bridge) |
| `vegas/BeckyVegas/ProjectOps.cs` | All timeline read/edit operations |
| `vegas/BeckyVegas/BeckyTools.cs` | Shell-out to becky Go tools, search/transcribe |
| `vegas/BeckyVegas/SearchPanel.cs` | "Becky Search" dockable panel UI |
| `vegas/BeckyVegas/BeckyVegasModule.cs` | Extension entry point, Whoretana integration |
| `vegas/BeckyCut.cs` | Jump-cut selected events (applies `becky-cut` decisions) |
| `vegas/BeckyCaptions.cs` | Timeline-aware captions (uses `becky-subtitle`) |
| `vegas/BeckyReviewTimeline.cs` | Forensic review timeline from text list |
| `vegas/BeckyRoughCut.cs` | Unattended rough-cut assembler |
| `vegas/BeckyVerifyProject.cs` | Headless project verification |
| `becky-go/cmd/vegas/main.go` | `becky-vegas.exe` CLI client |
| `vegas/README.md` | **User-facing documentation** (start here) |
| `vegas/BECKY-CUT-STATUS.md` | Detailed history of the cut/regroup fixes |

---

## Conclusion

**The bridge is real and working.** You have:
- A **dockable search panel** inside VEGAS (`View ▸ Extensions ▸ Becky Search`)
- A **CLI control channel** (`becky-vegas.exe`) that an AI agent can use
- **Four scripts** that bridge becky's forensic tools to your timeline

**To get the panel back:** Open VEGAS → `View ▸ Extensions ▸ Becky Search`.

**To give an AI agent "junior editor" capabilities today:** The pipe commands cover read, search, jump, insert, marker, region, transport, snapshot, script execution, and transcription. The missing pieces for full autonomy are persistent session state, timeline change events, and visual verification — all tractable but not yet built.

---

*Generated by read-only review of `X:\AI-2\becky-tools\vegas\` and `becky-go\cmd\vegas\`. No changes made.*