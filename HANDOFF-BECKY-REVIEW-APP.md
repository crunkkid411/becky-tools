# HANDOFF — Becky Review app (the one-window forensic video reviewer). BUILD THIS TO COMPLETION.

> **Plain version for Jordan (one breath):** one window. On one side, web-page buttons + your clip list
> (the part the AI is good at). On the other side, a real built-in video player that scrubs smoothly,
> frame by frame. becky finds the moments (e.g. "every segment where the cat is close to the camera");
> they show up as a list; you click one and it plays instantly in the smooth player. No fragile
> background server. This is the long-term home for reviewing forensic video.
>
> **Decision settled (Jordan, 2026-06-27):** build the new app. The HOW is decided (this doc). The
> research behind it is `research/gui-embedding-revisit-2026-06.md` — read it once for the *why*, then
> follow the steps here for the *do*.

---

## RULES FOR THE BUILDING AGENT (not optional — same as HANDOFF-BECKY-WPF-GUI.md + CANVAS-NORTH-STAR.md)
1. **Finish to a WORKING, SCREENSHOTTED window each step.** "It compiles" is NOT done. Done = the
   window opens, the step's feature works, you **screenshot it**, and the step's DONE box is truthfully
   checkable. You can screenshot + click (Win32) — use that to verify like a human would.
2. **Walking skeleton FIRST.** Prove the two hard unknowns (a built-in video pane that seeks; a
   WebView2 pane with no server) BEFORE any features. Do NOT build the clip list before video plays.
3. **NEVER make Jordan run a CLI command or answer a jargon question.** Decide from this doc. If truly
   blocked, use a form (chips) or one spoken line — never a wall of technical text.
4. **One step = one finished deliverable.** Drive each to its DONE box; don't stop mid-step to ask.
5. **REUSE existing becky code; never reimplement a tool.** The engine already exists (§2). The app is
   a thin shell over `becky-*.exe` (JSON in/out), exactly like becky-window.
6. **High-contrast, large, readable** per ACCESSIBILITY.md (Jordan's palette). Color is an aid — keep it.
7. **Max 3 auto-fix rounds on one failure, then stop and flag** (STANDARDS-ENGINEERING.md). After 2
   failed attempts at an error, research it; don't guess in a loop (this is the anti-token-burn rule).

---

## 1. The design in one picture

```
  ┌─ Becky Review  (native WPF, .NET 8, Windows) ─────────────────────────┐
  │  top bar: [ Pick folder... ]  [ search box ]  high-contrast theme      │
  │                                                                        │
  │  ┌── LEFT: WebView2 (HTML, NO server) ──┐  ┌── RIGHT: libmpv video ──┐ │
  │  │  • clip/result list (DOM)            │  │  • native video player  │ │
  │  │  • transcript search results         │  │  • GPU decode, smooth   │ │
  │  │  • timeline strip = <canvas> +       │  │    frame-step + scrub   │ │
  │  │    precomputed thumbnails            │  │  • becky draws playhead │ │
  │  └──────────────────────────────────────┘  └─────────────────────────┘ │
  │   (two separate child regions — they don't overlap, so no airspace bug) │
  └────────────────────────────────────────────────────────────────────────┘
        │ shells out to existing becky-*.exe (JSON in/out) — engine unchanged
```

- **LEFT = WebView2** loaded via `SetVirtualHostNameToFolderMapping` (a virtual address, NOT a TCP
  server). This is the AI-buildable, AI-verifiable, customizable surface.
- **RIGHT = libmpv** (the mpv engine) embedded as a native pane. This is what makes video smooth and
  frame-exact. **Video does NOT go through the web page** — that path is broken for seeking without a
  server (see the research doc). Keeping video native is the whole point of the split.

---

## 2. REUSE MAP — what already exists (do NOT rebuild)

| Need | Already built | Where |
|---|---|---|
| Clip-list / segment model | `edl.Reel` / `edl.Clip` | `becky-go/internal/edl` |
| Folder index + transcript search | `footage` + `quotes` | `becky-go/internal/footage`, `internal/quotes` |
| The whole HTML/JS review UI (lists, search, timeline strip) | becky-clip's `assets/` | `becky-go/cmd/clip/assets/{index.html,app.css,app.js}` |
| Find segments → a Reel | becky-clip / the forensic tools | (produces a Reel JSON) |
| Reel → review list / OTIO | `becky-otio` | `becky-go/cmd/becky-otio` |
| Tool catalog (for any tool buttons) | `becky catalog --json` | `becky-go/cmd/catalog` |
| Proxy / frame grab | `internal/reel` (`Proxy`, `GrabFrame`) | `becky-go/internal/reel` |

**Start the LEFT pane's HTML from becky-clip's `assets/` files** — they already render a clip list, a
search box, and a timeline strip. You are RE-HOSTING that UI in WPF/WebView2 and swapping its video
playback over to the libmpv pane. You are NOT writing a new UI from scratch.

---

## 3. THE STEPS (walking skeleton first — each ends in a screenshot)

> Put the project in `gui/BeckyReview/` (sibling of `gui/BeckyWindow/`). .NET 8 WPF.

### Step 0 — empty window opens
- New WPF project (.NET 8): `dotnet new wpf -n BeckyReview -o gui/BeckyReview`. Set the window to the
  high-contrast palette (copy colors from `gui/BeckyWindow`). Two empty side-by-side `Grid` columns.
- **DONE:** `dotnet run` opens a high-contrast window with two blank panels. **Screenshot it.**

### Step 1 — LEFT pane: WebView2 shows local HTML with NO server (de-risk #2)
- Add NuGet `Microsoft.Web.WebView2`. Use `WebView2CompositionControl` (avoids the WPF airspace bug).
- After `EnsureCoreWebView2Async`, serve local files with **no server**:
  ```csharp
  webView.CoreWebView2.SetVirtualHostNameToFolderMapping(
      "beckyreview.local", uiFolderAbsolutePath,
      CoreWebView2HostResourceAccessKind.Allow);
  webView.CoreWebView2.Navigate("https://beckyreview.local/index.html");
  ```
- Put a minimal `index.html` (or becky-clip's `assets/index.html`) in `uiFolder`.
- **DONE:** the window's left side shows the HTML page (a heading + a fake list). **Screenshot it.**

### Step 2 — RIGHT pane: libmpv plays AND frame-steps a video (de-risk #1 — THE riskiest unknown)
- Bundle the mpv runtime: ship `libmpv-2.dll` (or `mpv.exe`) next to the app (download the Windows
  build from mpv.io / shinchiro builds). No compiler needed — it's a prebuilt DLL.
- **For the Phase-0 PROOF, use the simplest embed: spawn `mpv.exe` into a WPF child HWND** via
  `--wid`. Put a `System.Windows.Forms.Panel` inside a `WindowsFormsHost` on the right column, then:
  ```csharp
  var hwnd = videoPanel.Handle; // WinForms Panel handle
  Process.Start(new ProcessStartInfo {
      FileName = "mpv.exe",
      Arguments = $"--wid={hwnd} --hr-seek=yes --hwdec=auto-safe --keep-open=yes \"{videoPath}\"",
      UseShellExecute = false,
  });
  ```
  `--hr-seek=yes` = frame-exact seeking; `--hwdec=auto-safe` = GPU decode (D3D11VA/NVDEC on the 3070).
- **DONE:** a real video frame shows in the right pane, it plays, and pressing left/right arrow steps
  one frame at a time smoothly. **Screenshot a frame.** (If `--wid` is flaky, that's expected — it's
  the cheap proof; Step 8 upgrades to the libmpv render API. But it MUST show + seek video here.)

### Step 3 — the two panes coexist (de-risk the layout)
- Left WebView2 + right libmpv visible at once, resizable, no flicker/airspace fight (they don't
  overlap, so this should "just work" — confirm it).
- **DONE:** screenshot the whole window with HTML on the left and a video frame on the right.

> ⛔ Do NOT proceed past Step 3 until video plays + seeks and both panes show together. If libmpv
> can't be embedded after the 3-fix limit, STOP and flag — that's the load-bearing risk and Jordan
> needs to know before more is built.

### Step 4 — load a folder → real clip list (reuse the engine)
- Top bar "Pick folder..." (native dialog). Shell `becky-*.exe` to index it (reuse the becky-clip
  engine path / `footage`), get back JSON, render the list in the LEFT HTML pane.
- **DONE:** pick a real case folder → the left pane lists real videos/clips. **Screenshot it.**

### Step 5 — click a clip → it plays in the libmpv pane at the right spot
- JS in the page calls into C# (WebView2 `WebMessageReceived` / host object) with the clicked clip's
  file + in-point. C# tells mpv to load+seek: send mpv the `loadfile`/`seek` commands (IPC named pipe
  `--input-ipc-server=\\.\pipe\beckympv`, or via the render-API player in Step 8).
- **DONE:** click a clip in the list → the video pane jumps to that moment and plays. **Screenshot it.**

### Step 6 — THE cat use case, end to end
- becky finds the candidate segments (the forensic tools → a Reel JSON) → `becky-otio --format
  vegas-list` (or read the Reel directly) → the app shows the candidates as a clickable list → click
  each → review in the smooth libmpv pane.
- **DONE:** on a real clip set, Jordan can click through the candidate segments and review them
  smoothly, in this one window. **Screenshot the loop.** (This is the actual product goal.)

### Step 7 — AI can verify its OWN window (the thing that failed on Gio)
- Enable CDP on the WebView2 pane (in-process `CallDevToolsProtocolMethodAsync`, or launch with
  `--remote-debugging-port=0` for an external agent). Confirm an agent can read the DOM + screenshot.
- **DONE:** a script drives the left UI over CDP and screenshots it — verification no longer needs a
  human to eyeball every change.

### Step 8 — polish the video pane (upgrade from the Step-2 proof)
- Replace the `--wid` spawn with the **libmpv render API** (P/Invoke `libmpv-2.dll`, or a maintained
  binding) so becky draws its own playhead/markers over the video and controls it in-process. Add the
  timeline strip's precomputed **sprite-sheet thumbnails** (grab frames with `internal/reel.GrabFrame`).
- **DONE:** smooth scrub with a becky-drawn playhead; thumbnail strip; no second process.

### In parallel (cheap, independent) — the proxy fix
- Run `HANDOFF-PROXY-SNAPPINESS.md` Steps 1–2 on a real laggy clip. All-intra + CFR proxies make
  scrubbing smooth in ANY player including libmpv. This is the other half of "snappy" and is worth
  doing early since it's cheap and helps every step above.

---

## 4. The mandatory hardware Definition-of-Done (CANVAS-NORTH-STAR.md)
The app is DONE when, on Jordan's PC: the window opens; he picks a real folder; clips list on the left;
he clicks one and it **plays + scrubs smoothly** on the right; he frame-steps without lag; nothing
freezes. Screenshot each. Report what worked / what degraded via a form or one spoken line — never a
jargon dump.

## 5. Honest risks + fallbacks
- **libmpv embedding is the #1 risk** (Step 2). `--wid` is the cheap proof; the render API is the clean
  end state. If BOTH fail after the fix limit, the documented fallback is: relax the no-server rule
  for *media only* (a tiny loopback Range-server feeding a WebView2 `<video>`) — but try libmpv first;
  it's the better answer and needs no server.
- **Don't rewrite the engine.** If you're writing forensic/search/Reel logic, stop — it exists (§2).
- **Don't start the clip list before video plays.** Walking skeleton first, or this spirals.
- **Reuse becky-clip's HTML.** It already has the list/search/timeline-strip UI.

## 6. Cloud's half (done) vs local's half (this doc)
- **Done by cloud + pushed:** the research/decision (`research/gui-embedding-revisit-2026-06.md`), the
  `becky-otio` tool (Reel → review list/OTIO, tested), the Vegas review script (interim review path),
  and `HANDOFF-PROXY-SNAPPINESS.md`. The engine pieces this app reuses are already built + green.
- **Local's half:** Steps 0–8 above (WPF + WebView2 + libmpv — all need Windows/.NET/GPU/a display,
  which cloud cannot build or verify). Drive each step to its screenshot DONE box.
