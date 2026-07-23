# BECKY REVIEW 3 — UI PARITY SPECS (generated 2026-07-20)

Produced by a 10-agent workflow comparing Becky Review 3 (native C++/ImGui)
against `gui/BeckyReviewNative` (the WPF REFERENCE — ten rounds of Jordan's
feedback live in its design; copy its LOOK, never its architecture).

Each area has a SPEC and an adversarial VERIFY pass that checked every anchor
against the real main.cpp. **Read the VERIFY section before applying a SPEC** —
the verifier caught anchors that do not exist and collisions with work already
done.

## THE HEADLINE FINDING

> The real reason the toolbar looks wrong is not the labels — it is the
> CONTAINER. It lives inside the video pane (~56% of window width), so thirteen
> controls wrap into ragged rows. The reference puts the transport in the
> TIMELINE HEADER at full window width.

---

# SPEC — 

# SPEC — Timeline surrounding controls (native/becky-review/main.cpp)

## Honest finding first: 2 of the 3 items you listed are wrong or already done

| Item | Status |
|---|---|
| >1s loading bar (checklist 14) | **Already done** — `main.cpp:5106-5151`. Not re-proposed. |
| Empty-state message | **ALREADY EXISTS** — `main.cpp:3102-3106` draws `"timeline empty - load a reel from the engine"` centred in the lane. The briefing's "blank slab with no explanation" is not accurate. It needs **two small fixes** (wording + contrast), not a new feature. See Change C. |
| Count + duration + zoom readout row | **Genuinely missing.** No clip-count readout exists anywhere in the file (verified: no `g_track[0].size()` reaches any text widget). See Changes A + B. |

Three changes total. No new globals, no new helpers — everything reuses what is already declared: `g_pps` (1752), `g_zoomReq` (1757), `g_track` (1338), `g_compDur` (1339), `fmtTime` (2247).

---

## Change A — the header row above the ruler

**Anchor:** `main.cpp:5173`. Insert the block **immediately BEFORE** this exact line (it is the last statement inside `if (ImGui::Begin("timeline", ...))`):

```cpp
            drawTimeline(curSec, playing);
```

`drawTimeline` takes its geometry from `ImGui::GetContentRegionAvail()` at `main.cpp:2631-2632`, so it shrinks to fit automatically — no other edit is needed to make room.

**Insert:**

```cpp
            // ---- timeline header row (the reference app's .tlhead) ----
            // Left: "N clips - M:SS". Right of it at a FIXED x: the zoom readout.
            // Both readouts existed in the WPF/HTML reference (index.html #tlCount /
            // #tZoom) and were the only way to know at a glance that a reel actually
            // loaded and how deep the current zoom is.
            //
            // Compact FramePadding for this row only: at the default (10,7) the row
            // costs ~38px, which at the minimum timeline height drops lanesH under
            // 90 and silently switches the caption lane OFF. At (8,3) it costs ~30px.
            {
                ImGui::PushStyleVar(ImGuiStyleVar_FramePadding, ImVec2(8, 3));

                char durb[24]; fmtTime(g_compDur, durb, sizeof durb, false);
                size_t nclips = g_track[0].size();
                // NEON GREEN IS AN ACCESSIBILITY AID, not decoration - it is the
                // reference's --neon (#39ff14) and it is the one number he glances
                // at to confirm the reel is really loaded. Do not tone it down.
                ImGui::TextColored(ImVec4(0.224f, 1.0f, 0.078f, 1.0f), "%d clip%s - %s",
                                   (int)nclips, nclips == 1 ? "" : "s", durb);

                // THE ZOOM GROUP SITS AT A FIXED X, never SameLine() after the
                // variable-width count: "9 clips" vs "128 clips" would slide the
                // -/+ buttons sideways under his cursor between edits. Same
                // never-move rule fixedButton() exists for (main.cpp:874-888);
                // fixedButton itself can't help here because the thing that
                // changes width is the TEXT BEFORE the buttons, not their labels.
                const float zoomX = ImGui::CalcTextSize("8888 clips - 88:88:88").x + 28.0f;
                const float bw = ImGui::GetFrameHeight();   // square, so both match

                ImGui::SameLine(zoomX);
                // -/+ do NOT do their own zoom math. They post the SAME request the
                // Up/Down keys post (main.cpp:4227-4228); drawTimeline drains it a
                // few lines below via applyWheel() (main.cpp:2759), which is also
                // the wheel's path. One zoom implementation, three ways in.
                if (ImGui::Button("-##zoomout", ImVec2(bw, 0))) g_zoomReq = -1;
                if (ImGui::IsItemHovered())
                    ImGui::SetTooltip("Zoom the timeline out (also: Down arrow, or the wheel over the timeline)");

                ImGui::SameLine();
                char zb[24]; snprintf(zb, sizeof zb, "%.4g px/s", g_pps);
                ImGui::TextColored(ImVec4(0.78f, 0.81f, 0.85f, 1.0f), "%s", zb);

                // "+" is pinned at the far edge of a slot sized for the WIDEST value
                // the readout can ever show ("2000 px/s" - the clamp at main.cpp:2741),
                // so it stays put while the number changes under the wheel.
                ImGui::SameLine(zoomX + bw + ImGui::CalcTextSize("2000 px/s").x
                                + ImGui::GetStyle().ItemSpacing.x * 2.0f);
                if (ImGui::Button("+##zoomin", ImVec2(bw, 0))) g_zoomReq = 1;
                if (ImGui::IsItemHovered())
                    ImGui::SetTooltip("Zoom the timeline in (also: Up arrow, or the wheel over the timeline)");

                ImGui::PopStyleVar();
            }
```

Notes for the applying agent:
- `g_zoomReq` set here is consumed **in the same frame** — `drawTimeline` runs on the next line and drains it at `main.cpp:2759`. Zero click-to-zoom latency, and it anchors on the playhead exactly like the keyboard path.
- `%.4g` deliberately: prints `0.5`, `60`, `192`, `2000` and never rounds the 0.5 floor down to a misleading `0`.
- ASCII only (`-`, not `·`). The reference's middle dot is not in ImGui's default glyph range and non-ASCII in this source has broken the build before.

---

## Change B — one number so the row doesn't squeeze the lane

**Anchor:** `main.cpp:4562`, this exact line:

```cpp
        const float timelineH = (std::max)(180.0f, availH * 0.26f);
```

**Replace with:**

```cpp
        // Floor raised by the ~30px header row added below (clip count + zoom
        // readout) so the lane keeps the same usable height it had before it:
        // laneH must clear 70 for thumbnails and lanesH must clear 90 for the
        // caption lane (main.cpp:2642 / 2732), both of which the row would
        // otherwise push under at the old 180 floor.
        const float timelineH = (std::max)(212.0f, availH * 0.26f);
```

Only bites below a ~722px window height; above that `availH * 0.26f` already wins and nothing changes.

---

## Change C — fix the empty state that's already there (2 lines)

**Anchor:** `main.cpp:3103-3105`, these exact lines:

```cpp
        const char* msg = "timeline empty - load a reel from the engine";
        ImVec2 ts = ImGui::CalcTextSize(msg);
        dl->AddText(ImVec2(tlX + (tlW - ts.x) / 2, aY + (laneH - ts.y) / 2), IM_COL32(120, 128, 140, 255), msg);
```

**Replace with:**

```cpp
        // Name the gesture that actually fills it. Double-clicking a search hit
        // adds that clip to the timeline (main.cpp:4604 addHitToTimeline) - that
        // is how the reel gets BUILT, and "load a reel from the engine" told him
        // about the other path only. Wording follows the reference's .tlempty hint.
        const char* msg = "timeline empty - double-click a quote in the search results to add clips, or use Load Reel";
        ImVec2 ts = ImGui::CalcTextSize(msg);
        // Brightened from (120,128,140): on the near-black lane (24,27,33) the old
        // grey was under a 3:1 contrast ratio - the one message on screen when
        // nothing else is, and the hardest one to read.
        dl->AddText(ImVec2(tlX + (tlW - ts.x) / 2, aY + (laneH - ts.y) / 2), IM_COL32(178, 186, 200, 255), msg);
```

---

## Verify after applying

1. `native\becky-review\_build.bat` → clean compile.
2. Launch with an empty timeline: header reads `0 clips - 0:00` in neon green, `60 px/s` between `-` and `+`; lane centre shows the new hint.
3. Click `+` five times, then `-` five times: the number must return to `60` and the `+` button must not move a pixel at any value.
4. Wheel over the timeline, then press Up/Down: the readout must track all three inputs identically.
5. Load a reel: count and duration update; the hint disappears; the caption lane still renders at the smallest window height you can drag to.

**Files:** `X:\AI-2\becky-tools\native\becky-review\main.cpp` (anchors 3103, 4562, 5173).

skipped: a shared "status row" helper and any theming — three call sites, one file, immediate-mode. Add when a second panel needs the same row.

---

# SPEC — 

Read `main.cpp` (5204 lines), the WPF/HTML reference header, and the engine's render-destination logic. Spec follows.

---

# SPEC — Becky Review 3 header / global chrome

## Assessment first (what I am NOT proposing)

- **The menu bar itself is fine.** `BeginMainMenuBar` + `Open Folder... Ctrl+O` + the ENGINE DOWN / MPV DOWN flags already work and match the reference's function. Keep all of it.
- **Not proposing a taller bar.** Raising `FramePadding` around `BeginMainMenuBar` would desync `const float menuH = ImGui::GetFrameHeight();` (main.cpp:4558) which every panel rect is derived from. Everything below is drawn *inside* the existing bar height. Zero layout risk.
- **Not proposing** the reference's `forensic video review` subtitle, a recent-folders menu, or changing `"%.1fs / %.0fs"` to timecode (that readout belongs to whoever owns the timeline; after this change nothing sits to its right, so its jittering width no longer pushes anything).
- **The real gap is one thing:** `g_folderRoot` is set (main.cpp:3512) and then displayed **nowhere permanent** — only its `baseName` inside a transient `g_renderMsg` (main.cpp:3529). "Which drive am I in" is currently unanswerable at a glance.

**Important correction to the premise, verified in source:** the wrong-drive render bug is already fixed *engine-side*. `becky-go/cmd/clip/export.go:519` + `becky-go/internal/reel/reel.go:348` (`RenderDirFor`) now put renders in a `Rendered/` folder **next to the first clip's source**, not next to the browsed folder. So the folder chip must **not** claim to be the output location. Change 3 surfaces the render destination separately, and only when it is on a different drive from the browsed folder — which is exactly the state that burned him.

---

## Change 1 — new helpers (3 small functions)

**Anchor:** insert immediately AFTER main.cpp:3477 (the closing `}` of `cardColorFor`) and BEFORE main.cpp:3479 `// ---- library helpers ----`.

Placed here deliberately: it is the first point where `kPalette` (main.cpp:3467) is in scope, and it is above the menu bar at 4522.

```cpp
// ---- header: WHICH FOLDER IS OPEN, on WHICH DRIVE (safety, not decoration) ----
//
// Jordan works across two drives that must never be confused: X: is his own video
// work, E: is a REMOVABLE criminal-case evidence drive. g_folderRoot was set at
// boot and then shown NOWHERE permanent - only its basename, for a few seconds,
// inside the transient g_renderMsg line - so "which case am I in" could not be
// answered by looking. The menu bar now carries it permanently.
//
// The colour is keyed to the DRIVE LETTER, deterministically, so a given drive
// always wears the SAME colour and the wrong drive is wrong on sight before he has
// read a character. No hardcoded drive list: a new evidence volume gets a stable
// colour for free.
static uint32_t driveColor(const std::string& path) {
    char d = path.empty() ? '?' : path[0];
    if (d >= 'a' && d <= 'z') d = (char)(d - 'a' + 'A');
    if (d < 'A' || d > 'Z') return IM_COL32(0x8A, 0x8A, 0x8A, 255);   // UNC / relative
    return kPalette[(d - 'A') % 8];
}
// Black or white ink, whichever actually READS on that chip. kPalette spans gold
// through blueviolet; one fixed ink colour is illegible on half of it. Threshold
// 105 was checked against all 8 palette entries plus the grey fallback.
static ImU32 inkFor(uint32_t bg) {
    int r = bg & 0xFF, g = (bg >> 8) & 0xFF, b = (bg >> 16) & 0xFF;   // IM_COL32 packs R,G,B,A low->high
    return ((r * 299 + g * 587 + b * 114) / 1000 > 105) ? IM_COL32(0, 0, 0, 255)
                                                        : IM_COL32(255, 255, 255, 255);
}
// Trim the MIDDLE of a path to fit maxW, keeping the two load-bearing ends: the
// DRIVE LETTER and the actual folder name. A plain right-truncating ellipsis would
// eat the folder name; a left one would eat the drive. Both are the point.
// Cached, because this runs every frame and CalcTextSize per candidate is not free.
static std::string elideMiddle(const std::string& s, float maxW) {
    static std::string cacheIn, cacheOut;
    static float cacheW = -1.0f;
    if (s == cacheIn && maxW == cacheW) return cacheOut;
    std::string out = s;
    if (ImGui::CalcTextSize(s.c_str()).x > maxW) {
        const size_t head = (s.size() > 2 && s[1] == ':') ? 3 : 2;   // "X:\" or "\\"
        for (size_t tail = s.size(); tail > 4; tail--) {
            std::string cand = s.substr(0, head) + "..." + s.substr(s.size() - tail);
            if (ImGui::CalcTextSize(cand.c_str()).x <= maxW) { out = cand; break; }
            out = cand;
        }
    }
    cacheIn = s; cacheW = maxW; cacheOut = out;
    return out;
}
```

No new includes: `ImGui`, `IM_COL32`, `kPalette` and `std::string` are all already in scope at this point.

---

## Change 2 — brand with the reference's neon accent

**Anchor:** REPLACE main.cpp:4523, exactly:

```cpp
            ImGui::Text("Becky Review (native)");
```

with:

```cpp
            // Brand, matching the reference app's .brandbar (ui/app.css): a neon
            // diamond + white "becky" + neon "review". [COLOR] - #39FF14 is the
            // accessibility palette, not styling.
            {
                const ImVec2 c = ImGui::GetCursorScreenPos();
                const float h = ImGui::GetTextLineHeight(), s = h * 0.42f;
                const ImVec2 m(c.x + s, c.y + h * 0.5f);
                ImGui::GetWindowDrawList()->AddQuadFilled(
                    ImVec2(m.x, m.y - s), ImVec2(m.x + s, m.y),
                    ImVec2(m.x, m.y + s), ImVec2(m.x - s, m.y), IM_COL32(0x39, 0xFF, 0x14, 255));
                ImGui::Dummy(ImVec2(s * 2.4f, h));
            }
            ImGui::SameLine(0, 0); ImGui::TextUnformatted("becky");
            ImGui::SameLine(0, 0); ImGui::TextColored(ImVec4(0.224f, 1.0f, 0.078f, 1.0f), " review");
            ImGui::SameLine(0, 0); ImGui::TextDisabled(" 3");
```

`" 3"` rather than `"(native)"` because that is the name on his desktop button, and because both apps are open side by side while he compares them.

---

## Change 3 — the folder chip (the actual point of this spec)

**Anchor:** REPLACE main.cpp:4536–4537, exactly these two lines:

```cpp
            if (!g_engine.alive) ImGui::TextColored(ImVec4(1, 0.4f, 0.4f, 1), "  ENGINE DOWN");
            if (!g_mpvAvailable.load()) ImGui::TextColored(ImVec4(1, 0.4f, 0.4f, 1), "  MPV DOWN");
```

with the block below. (main.cpp:4535 `ImGui::Text("%.1fs / %.0fs", ...)` stays put, immediately above; main.cpp:4538 `ImGui::EndMainMenuBar();` stays immediately below.)

```cpp
            // ---- RIGHT of the bar: health flags, then WHICH FOLDER IS OPEN ----
            //
            // Right-aligned and drawn LAST so the left cluster's width can never push
            // it, and its own width can never push anything. The chip is a FILLED
            // drive-coloured badge, not text, because a line of grey path text in a
            // busy bar is exactly what he does not see.
            {
                const bool haveFolder = !g_folderRoot.empty();
                const std::string full = haveFolder ? g_folderRoot
                                                    : std::string("NO FOLDER OPEN - press Ctrl+O");
                const ImGuiStyle& st = ImGui::GetStyle();
                const float pad = st.FramePadding.x;
                const float barW = ImGui::GetWindowWidth();

                // ---- optional second badge: where a render would actually LAND ----
                // Mirrors becky-go/internal/reel/reel.go RenderDirFor(): output goes to a
                // "Rendered" folder NEXT TO THE FIRST CLIP'S SOURCE - NOT next to the
                // folder being browsed. Browsing E: while the timeline holds X: footage is
                // the exact state that put eight personal renders onto the evidence volume
                // (see the comment on renderDir in becky-go/cmd/clip/export.go). Shown ONLY
                // when the two drives disagree, so it is silent in the normal case.
                std::string warnDir;
                if (haveFolder && !g_track[0].empty()) {
                    const std::string& src = g_track[0].front().source;
                    if (src.size() > 2 && src[1] == ':' && toupper((unsigned char)src[0]) != toupper((unsigned char)g_folderRoot[0])) {
                        size_t i = src.find_last_of("/\\");
                        std::string dir = (i == std::string::npos) ? src : src.substr(0, i);
                        warnDir = (baseName(dir) == "Rendered") ? dir : dir + "\\Rendered";
                    }
                }
                const std::string warnTxt = warnDir.empty() ? std::string()
                                                            : std::string("renders -> ") + warnDir.substr(0, 2);

                // measure everything, then place once
                const std::string shown = elideMiddle(full, (std::max)(140.0f, barW * 0.45f));
                const ImVec2 ts = ImGui::CalcTextSize(shown.c_str());
                const float chipW = ts.x + pad * 2;
                float x = barW - chipW - pad;
                if (!warnTxt.empty()) x -= ImGui::CalcTextSize(warnTxt.c_str()).x + pad * 3;
                if (!g_engine.alive)          x -= ImGui::CalcTextSize("ENGINE DOWN").x + pad * 2;
                if (!g_mpvAvailable.load())   x -= ImGui::CalcTextSize("MPV DOWN").x + pad * 2;
                if (x > ImGui::GetCursorPosX()) ImGui::SetCursorPosX(x);

                if (!g_engine.alive) { ImGui::TextColored(ImVec4(1, 0.25f, 0.25f, 1), "ENGINE DOWN"); ImGui::SameLine(); }
                if (!g_mpvAvailable.load()) { ImGui::TextColored(ImVec4(1, 0.25f, 0.25f, 1), "MPV DOWN"); ImGui::SameLine(); }

                // one badge painter, used for the warning and the folder chip
                auto badge = [&](const char* txt, uint32_t bg, const char* tip, const char* openPath) {
                    const ImVec2 t = ImGui::CalcTextSize(txt);
                    const ImVec2 p = ImGui::GetCursorScreenPos();
                    ImGui::InvisibleButton(txt, ImVec2(t.x + pad * 2, t.y));   // same height as a Text item
                    ImDrawList* dl = ImGui::GetWindowDrawList();
                    const ImVec2 a(p.x, p.y - 3), b(p.x + t.x + pad * 2, p.y + t.y + 3);
                    dl->AddRectFilled(a, b, bg, st.FrameRounding);
                    if (ImGui::IsItemHovered()) {
                        dl->AddRect(a, b, IM_COL32(255, 255, 255, 255), st.FrameRounding, 0, 2.0f);
                        ImGui::SetTooltip("%s", tip);
                    }
                    dl->AddText(ImVec2(p.x + pad, p.y), inkFor(bg), txt);
                    if (openPath && *openPath && ImGui::IsItemClicked())
                        ShellExecuteW(nullptr, L"open", L"explorer.exe", utf8ToWide(openPath).c_str(), nullptr, SW_SHOWNORMAL);
                };

                if (!warnTxt.empty()) {
                    const std::string tip = "This timeline's footage lives on another drive.\n"
                                            "A render will land in:\n" + warnDir +
                                            "\n(click to open it in Explorer)";
                    badge(warnTxt.c_str(), driveColor(warnDir), tip.c_str(), warnDir.c_str());
                    ImGui::SameLine();
                }
                const uint32_t bg = haveFolder ? driveColor(g_folderRoot) : IM_COL32(0xDC, 0x14, 0x3C, 255);
                const std::string tip = haveFolder ? (full + "\n(click to open this folder in Explorer)")
                                                   : std::string("No case folder is open. Press Ctrl+O.");
                badge(shown.c_str(), bg, tip.c_str(), haveFolder ? g_folderRoot.c_str() : nullptr);
            }
```

---

## Why it holds

- **Everything it touches already exists:** `g_folderRoot` (3319), `g_track` (read unlocked on the UI thread at 1631 in the same frame), `baseName` (74), `utf8ToWide` (960), `ShellExecuteW` (already used at 3548), `kPalette` (3467), `ImGui::GetStyle()`.
- **No new globals.** The only state is the three static cache locals inside `elideMiddle`.
- **No layout change.** `menuH` at main.cpp:4558 is untouched — the bar's height is unchanged.
- **Nothing can shift under his cursor.** The right cluster is measured then placed absolutely; the `if (x > GetCursorPosX())` guard means a narrow window degrades to "chip draws after the time readout, tooltip still has the truth" instead of overlapping.
- **Drive colours are distinct where it matters:** E → index 4 → `#FF57D1` (black ink); X → index 7 → `#FF8C00` (black ink); C → index 2 → `#DC143C` (white ink). Every kPalette entry was checked against the 105 luminance threshold.

## Runnable check

Launch, then: (1) with no folder, the bar shows a crimson `NO FOLDER OPEN - press Ctrl+O`; (2) Ctrl+O an `X:\` folder → orange chip with the full path; (3) Ctrl+O an `E:\` folder → pink chip, same position, obviously different colour; (4) with an `E:\` folder open, drag an `X:\` clip onto the timeline → a second badge `renders -> X:` appears left of the chip; (5) click either badge → Explorer opens that exact directory.

## Skipped

- Recent-folders menu, a taller bar, the `forensic video review` subtitle — YAGNI, add if he asks.
- Replicating `RenderDirFor` fully in C++ (only the first-clip + `Rendered` case is mirrored; the drive-mismatch gate makes the remaining cases irrelevant to the warning). If it ever drifts, the pairing is named in the comment.

---

# SPEC — 

# SPEC — right-hand "ask becky" panel (native/becky-review/main.cpp)

Verified against `main.cpp` (5204 lines, ImGui **1.90.9**, default ProggyClean font = **ASCII only**), the reference `gui/BeckyReviewNative/ui/{index.html,app.css,app.js}`, the engine verbs in `becky-go/cmd/clip/bridge.go` (`status`, `ask`, `save_answer`, `apply_proposal`, `reject_proposal` all exist), and `GUI-ACCEPTANCE-CHECKLIST.md` items **25, 32, 79**.

**Not compiled** — this is a paste-ready spec, not an applied change.

## What is already fine — propose nothing

- The **activity feed** (`main.cpp:4964-4985`) has no reference equivalent and is better than the reference. Keep verbatim, only move it inside the new scroll child.
- The **proposal Apply/Reject card** (`main.cpp:5064-5096`) already matches the reference's `.card` structurally (preview line, before→after diff list, two inline buttons, no modal). Only its *blocking* engine calls change.
- **Colours** come from `kPalette[0]` = `#14FF39` (checklist item 32 slot 1). No new green constant — the reference CSS `--neon: #39ff14` is the *older* value and item 32 supersedes it.

## The four real defects in this panel

1. `ImGui::Text("Q&A / Ask-Becky")` — engineering label, no brand mark, no status. Reference: robot + "ask becky" + a status card naming the live backend.
2. **No suggestion chips at all.**
3. Input is a headless `InputTextMultiline` with **no placeholder and no send button**.
4. **`ask` / `apply_proposal` / `reject_proposal` still call blocking `engineCall` on the UI thread** (`main.cpp:5035, 5078, 5091`) with a 30s timeout. This is exactly the freeze `engineCallAsync` was written for, and these three call sites were missed. A Claude Code turn takes 10-40s — the window is **dead** for that whole span.

Also missing: **checklist item 25** — a Q&A card is not clickable-to-answer. Cards can only play clips; there is no `save_answer` call anywhere in the app.

---

## EDIT 1 — new globals + the robot mark + a card parser

**Anchor:** insert immediately AFTER `main.cpp:3477`, the closing brace of `cardColorFor`:

```cpp
    g_cardColor[id] = c; return c;
}
```

**Insert:**

```cpp
// ---- ask-becky panel state (matches gui/BeckyReviewNative ui/index.html .chat) ----
static char g_askBuf[512] = { 0 };   // was a function-static inside the frame; the chips
                                     // and the Q&A cards both need to write it, so it is
                                     // file scope now.
static bool g_askFocus = false;      // "put the caret in the ask box next frame" (same
                                     // one-shot pattern as g_capEditFocus, main.cpp:3270)
static std::string g_askEcho;        // the question he last sent, echoed above the answer
static std::string g_backendSummary; // engine `status` -> one plain sentence
static bool g_backendOK = false;     // any backend live? drives the status card's colour
static std::string g_answerCardID;   // non-empty => the ask box is answering THIS card
static std::string g_answerCardQ;

// Real prompts for a video editor reviewing his OWN footage. The reference's chips
// ("find every threat to the host family") are forensic-case examples and read as
// nonsense in an edit session. Each of these maps to a verb the engine actually has:
// compile -> ask/apply_proposal, dead air -> autocut_silence, lower-third -> overlay.
static const char* kAskChips[3] = {
    "compile every take where I said the intro line",
    "cut the dead air out of this reel",
    "turn the lower-third on",
};

// A DRAWN robot, not a glyph. The app ships ImGui's default ProggyClean font (ASCII
// only) and CLAUDE.md bans non-ASCII bytes in native C++ source, so the reference's
// U+1F916 would render as a box or break the build. Six primitives, no font atlas
// rebuild, reads as a robot at a glance - which is the whole job of the mark.
static void askBeckyMark(float h) {
    ImDrawList* d = ImGui::GetWindowDrawList();
    ImVec2 p = ImGui::GetCursorScreenPos();
    const ImU32 neon = kPalette[0];                       // #14FF39, palette slot 1
    float w = h * 0.86f, x = p.x, y = p.y + h * 0.20f;
    d->AddLine({ x + w * 0.5f, y }, { x + w * 0.5f, y - h * 0.14f }, neon, 2.0f);
    d->AddCircleFilled({ x + w * 0.5f, y - h * 0.16f }, h * 0.08f, neon);
    d->AddRect({ x, y }, { x + w, y + h * 0.62f }, neon, h * 0.15f, 0, 2.0f);
    d->AddRectFilled({ x + w * 0.20f, y + h * 0.20f }, { x + w * 0.38f, y + h * 0.35f }, neon, 1.5f);
    d->AddRectFilled({ x + w * 0.62f, y + h * 0.20f }, { x + w * 0.80f, y + h * 0.35f }, neon, 1.5f);
    d->AddLine({ x + w * 0.28f, y + h * 0.48f }, { x + w * 0.72f, y + h * 0.48f }, neon, 2.0f);
    ImGui::Dummy(ImVec2(w, h * 0.82f));
}
```

## EDIT 2 — split the card parse out of `refreshCards` so `save_answer` can reuse it

`refreshCards` (`main.cpp:3682`) makes a **blocking** `engineCall` and is safe only on the boot thread. `save_answer` returns the updated question list in its own reply, so the parse must be callable without a second round trip.

**Anchor — replace `main.cpp:3681-3700` in full** (from the comment line `// render Q&A cards from the engine \`questions\` verb (G-1).` through the closing `}` of `refreshCards`):

```cpp
// render Q&A cards from the engine `questions` verb (G-1).
// Parse split out of refreshCards so save_answer's reply (which carries the updated
// list) can refresh the cards WITHOUT a second blocking round trip on the UI thread.
static void cardsFromJSON(const json& d) {
    g_cards.clear();
    if (d.contains("questions") && d["questions"].is_array()) {
        for (auto& q : d["questions"]) {
            QACard card;
            card.id = q.value("id", std::string());
            card.question = q.value("question", std::string());
            card.answered = q.value("answered", false);
            card.answer = q.value("answer", std::string());
            if (q.contains("clip_ids") && q["clip_ids"].is_array())
                for (auto& cid : q["clip_ids"]) card.clipIDs.push_back(cid.get<std::string>());
            g_cards.push_back(card);
        }
    }
}
static void refreshCards() {
    g_cardsErr.clear();
    json r = engineCall("questions", {}, 8.0);
    if (!r.value("ok", false)) { g_cardsErr = r.value("error", std::string("questions unavailable")); g_cards.clear(); return; }
    cardsFromJSON(r.contains("data") ? r["data"] : r);
}
```

## EDIT 3 — ask the engine which backend is live, on the boot thread

The status sentence is the engine's, not ours (`assist.go:284-290`). `bootWork` writes it before `g_bootDone.store(true)` (`main.cpp:3951`) and the panel is not drawn until that flag is observed (`main.cpp:3992`), so this is the file's existing single-writer-then-flag pattern — no lock needed.

**Anchor:** insert immediately AFTER `main.cpp:3933`:

```cpp
            if (const char* qp = getenv("BECKY_REVIEW_QUESTIONS")) { (void)qp; refreshCards(); }
```

**Insert:**

```cpp
            // Which AI is actually powering the chat. Jordan's rule is anti-"are you
            // lying to me": the panel SAYS the backend rather than implying one.
            {
                json st = engineCall("status", {}, 8.0);
                if (st.value("ok", false)) {
                    const json& sd = st.contains("data") ? st["data"] : st;
                    g_backendSummary = sd.value("summary", std::string());
                    g_backendOK = sd.value("claude_cli", false) || sd.value("api", false) || sd.value("local", false);
                }
            }
```

## EDIT 4 — the panel body

**Anchor — replace `main.cpp:4957` through `main.cpp:5096` in full.** First line replaced is:

```cpp
            ImGui::Text("Q&A / Ask-Becky");
```

Last line replaced is the closing `}` of the `if (g_proposalPending)` block at 5096, immediately before `main.cpp:5097`'s `        }` and `main.cpp:5098`'s `ImGui::End();`. Leave 4955/4956 (`SetNextWindowPos` + `Begin("qa"...)`) and 5097/5098 untouched.

```cpp
            const ImVec4 neonV = ImGui::ColorConvertU32ToFloat4(kPalette[0]);   // #14FF39
            const ImVec4 warnV = ImVec4(0.95f, 0.85f, 0.45f, 1.0f);             // same yellow the work indicator uses

            // ---- header: robot mark + "ask becky" (reference: .chathead/.chattitle) ----
            askBeckyMark(ImGui::GetFontSize() * 1.25f);
            ImGui::SameLine(0.0f, 9.0f);
            ImGui::SetWindowFontScale(1.15f);
            ImGui::TextColored(neonV, "ask becky");
            ImGui::SetWindowFontScale(1.0f);
            ImGui::Separator();

            // ---- status card (reference: .intro) ----
            // Never blank: if the engine never answered, say THAT rather than showing a
            // confident empty box.
            {
                const char* txt = g_backendSummary.empty()
                    ? "Checking which AI is connected..."
                    : g_backendSummary.c_str();
                ImVec4 edge = g_backendSummary.empty() ? ImVec4(0.5f, 0.5f, 0.5f, 1.0f)
                                                       : (g_backendOK ? neonV : warnV);
                float wrapW = ImGui::GetContentRegionAvail().x - ImGui::GetStyle().WindowPadding.x * 2.0f;
                float h = ImGui::CalcTextSize(txt, nullptr, false, wrapW).y + ImGui::GetStyle().WindowPadding.y * 2.0f;
                ImGui::PushStyleColor(ImGuiCol_ChildBg, ImVec4(0.05f, 0.07f, 0.05f, 1.0f));
                ImGui::PushStyleColor(ImGuiCol_Border, ImVec4(edge.x * 0.6f, edge.y * 0.6f, edge.z * 0.6f, 1.0f));
                ImGui::BeginChild("askstatus", ImVec2(0, h), ImGuiChildFlags_Border);
                ImGui::PushTextWrapPos(0.0f);
                ImGui::TextColored(g_backendOK ? ImVec4(0.86f, 0.86f, 0.86f, 1.0f) : warnV, "%s", txt);
                ImGui::PopTextWrapPos();
                ImGui::EndChild();
                ImGui::PopStyleColor(2);
            }

            // The chips + ask bar are PINNED to the foot of the panel, like the reference
            // and like every chat app - a control that drifts vertically with the amount of
            // content above it is a control he has to hunt for (same rule as fixedButton).
            // Height is measured from last frame's layout: exact, and one frame of lag on a
            // window resize is invisible.
            static float s_qaBottomH = 0.0f;
            float midH = -(s_qaBottomH > 0.0f ? s_qaBottomH : ImGui::GetFrameHeight() * 5.0f);
            ImGui::BeginChild("qamid", ImVec2(0, midH), ImGuiChildFlags_None);

            // H-5: "what becky is doing", passively. No buttons, nothing to click,
            // nothing that can steal focus from the timeline - Jordan keeps editing
            // while this fills in behind him. Hidden entirely when idle so it never
            // costs him vertical space (or reading effort) for nothing.
            {
                std::vector<Activity> recent;
                {
                    std::lock_guard<std::mutex> lk(g_activityMx);
                    size_t n = g_activityLog.size();
                    size_t from = n > 6 ? n - 6 : 0;   // newest few; the deque keeps 50
                    recent.assign(g_activityLog.begin() + (long)from, g_activityLog.end());
                }
                if (!recent.empty()) {
                    ImGui::TextDisabled("becky is working");
                    for (auto it = recent.rbegin(); it != recent.rend(); ++it) {
                        ImVec4 col = (it->kind == "done") ? ImVec4(0.55f, 0.75f, 0.55f, 1.0f) : warnV;
                        ImGui::TextColored(col, "%s", it->text.c_str());
                        if (ImGui::IsItemHovered() && !it->source.empty())
                            ImGui::SetTooltip("%s (%s)", it->source.c_str(), it->kind.c_str());
                    }
                    ImGui::Separator();
                }
            }

            if (!g_cardsErr.empty()) ImGui::TextColored(ImVec4(1, 0.4f, 0.4f, 1), "%s", g_cardsErr.c_str());
            if (!g_cards.empty()) {
                float cardsH = (std::min)((float)g_H * 0.45f, ImGui::GetContentRegionAvail().y * 0.62f);
                ImGui::BeginChild("cards", { 0, cardsH }, ImGuiChildFlags_Border);
                for (size_t i = 0; i < g_cards.size(); i++) {
                    QACard& c = g_cards[i];
                    ImGui::PushID((int)i);
                    ImVec4 col = ImGui::ColorConvertU32ToFloat4(cardColorFor(c.id));
                    ImGui::PushStyleColor(ImGuiCol_Text, col);
                    bool open = ImGui::CollapsingHeader(c.answered ? (c.question + "  [answered]").c_str() : c.question.c_str());
                    ImGui::PopStyleColor();
                    if (open) {
                        bool haveClip = false;
                        if (ImGui::SmallButton("Play tied clips")) {
                            // G-1: play EVERY clip tied to this answer, in order - not just the
                            // first match. Collect before mutating (seekToSpan-style track
                            // replacement clears g_track[0], so a live iterate-and-break would
                            // both skip later ties and corrupt the loop). Search the REAL reel
                            // (the pre-preview backup if one is already active), never a track
                            // that's already showing a different card's preview.
                            const std::vector<Clip>& realReel = g_inTiedPreview ? g_reelBeforePreview : g_track[0];
                            std::vector<Clip> tied;
                            for (auto& tc : realReel)
                                if (std::find(c.clipIDs.begin(), c.clipIDs.end(), tc.id) != c.clipIDs.end())
                                    tied.push_back(tc);
                            if (!tied.empty()) {
                                if (!g_inTiedPreview) { g_reelBeforePreview = g_track[0]; g_inTiedPreview = true; }
                                g_track[0].clear();
                                for (auto& tc : tied) g_track[0].push_back(tc);
                                packTrack(0); recomputeDur();
                                curSec = 0; playing = true; g_playingExt = true; lastComposed = -1;
                                g_quietDirty = true;
                                for (auto& tc : tied) peaksRequest(tc.source, tc.in - 1.0, tc.out + 5.0);
                                haveClip = true;
                            }
                        }
                        (void)haveClip;
                        ImGui::SameLine();
                        // CHECKLIST 25: "each clickable to type and submit an answer exactly
                        // like sending a chat". This retargets the ONE ask box below rather
                        // than growing a second input per card - one place the caret ever is.
                        if (ImGui::SmallButton("Answer this")) {
                            g_answerCardID = c.id; g_answerCardQ = c.question;
                            g_askBuf[0] = 0; g_askFocus = true;
                        }
                        if (!c.answer.empty()) ImGui::TextWrapped("answer: %s", c.answer.c_str());
                    }
                    ImGui::PopID();
                }
                ImGui::EndChild();
                ImGui::Separator();
            }

            // ---- the exchange (reference: .messages) ----
            if (!g_askEcho.empty()) ImGui::TextDisabled("you: %s", g_askEcho.c_str());
            if (!g_askAnswer.empty()) ImGui::TextWrapped("%s", g_askAnswer.c_str());

            if (g_proposalPending) {
                ImGui::Separator();
                ImGui::PushStyleColor(ImGuiCol_Text, ImVec4(1.0f, 0.85f, 0.25f, 1.0f));
                ImGui::TextWrapped("Proposed: %s", g_proposalPreview.c_str());
                ImGui::PopStyleColor();
                if (g_proposalDiff.is_array()) {
                    for (auto& dl : g_proposalDiff) {
                        std::string label = dl.value("label", std::string());
                        std::string before = dl.value("before", std::string());
                        std::string after = dl.value("after", std::string());
                        ImGui::BulletText("%s: %s -> %s", label.c_str(), before.c_str(), after.c_str());
                    }
                }
                // ASYNC. apply_proposal re-cuts the timeline server-side and took up to 30s
                // ON THE UI THREAD - the exact dead-window freeze engineCallAsync exists to
                // kill. The card is dismissed immediately (he sees his click land) and the
                // result arrives on the UI thread via drainAsync.
                if (ImGui::Button("Apply##proposal")) {
                    std::string pid = g_proposalID, prev = g_proposalPreview;
                    g_proposalPending = false; g_proposalID.clear();
                    engineCallAsync("apply_proposal", { {"id", pid} }, 120.0, "Applying becky's edit...",
                        [prev](const json& ar) {
                            if (ar.value("ok", false)) {
                                const json& d = ar.contains("data") ? ar["data"] : ar;
                                if (d.contains("timeline")) loadTimelineView(d["timeline"]);
                                g_askAnswer = "Applied: " + prev + " (Ctrl+Z reverts the whole pass)";
                            } else {
                                g_askAnswer = "Apply failed: " + ar.value("error", std::string("?"));
                            }
                        });
                }
                ImGui::SameLine();
                if (ImGui::Button("Reject##proposal")) {
                    std::string pid = g_proposalID;
                    g_askAnswer = "Rejected: " + g_proposalPreview;
                    g_proposalPending = false; g_proposalID.clear();
                    engineCallAsync("reject_proposal", { {"id", pid} }, 10.0, "", [](const json&) {});
                }
            }
            ImGui::EndChild();   // qamid

            // ================= pinned foot: chips + ask bar =================
            float footY0 = ImGui::GetCursorPosY();
            ImGui::Separator();

            // ---- suggestion chips (reference: .chip - neon pill, transparent fill) ----
            {
                ImGuiStyle& stl = ImGui::GetStyle();
                ImGui::PushStyleVar(ImGuiStyleVar_FrameRounding, ImGui::GetFrameHeight() * 0.5f);
                ImGui::PushStyleVar(ImGuiStyleVar_FrameBorderSize, 1.0f);
                ImGui::PushStyleColor(ImGuiCol_Button, ImVec4(0, 0, 0, 0));
                ImGui::PushStyleColor(ImGuiCol_ButtonHovered, ImVec4(neonV.x, neonV.y, neonV.z, 0.16f));
                ImGui::PushStyleColor(ImGuiCol_ButtonActive, ImVec4(neonV.x, neonV.y, neonV.z, 0.30f));
                ImGui::PushStyleColor(ImGuiCol_Text, neonV);
                ImGui::PushStyleColor(ImGuiCol_Border, ImVec4(neonV.x * 0.45f, neonV.y * 0.45f, neonV.z * 0.45f, 1.0f));
                float availW = ImGui::GetContentRegionAvail().x, used = 0.0f;
                for (int i = 0; i < 3; i++) {
                    float w = ImGui::CalcTextSize(kAskChips[i]).x + stl.FramePadding.x * 2.0f;
                    if (i > 0 && used + stl.ItemSpacing.x + w <= availW) { ImGui::SameLine(); used += stl.ItemSpacing.x + w; }
                    else used = w;
                    // A chip FILLS the box and focuses it, never sends - he edits the wording
                    // before it costs a model turn (same as the reference's chip handler).
                    if (ImGui::Button(kAskChips[i])) {
                        snprintf(g_askBuf, sizeof g_askBuf, "%s", kAskChips[i]);
                        g_answerCardID.clear();
                        g_askFocus = true;
                    }
                }
                ImGui::PopStyleColor(5);
                ImGui::PopStyleVar(2);
            }

            // ---- ask bar: input + green send (reference: .askbar / .sendbtn) ----
            if (!g_answerCardID.empty()) {
                ImGui::TextColored(neonV, "answering:");
                ImGui::SameLine();
                ImGui::TextDisabled("%s", g_answerCardQ.c_str());
                ImGui::SameLine();
                if (ImGui::SmallButton("cancel")) { g_answerCardID.clear(); g_answerCardQ.clear(); g_askBuf[0] = 0; }
            }
            {
                std::string hint = g_answerCardID.empty() ? std::string("ask becky...")
                                                          : ("answer: " + g_answerCardQ);
                float sendW = ImGui::CalcTextSize("Send").x + ImGui::GetStyle().FramePadding.x * 2.0f;
                ImGui::SetNextItemWidth(ImGui::GetContentRegionAvail().x - sendW - ImGui::GetStyle().ItemSpacing.x);
                if (g_askFocus) { ImGui::SetKeyboardFocusHere(); g_askFocus = false; }
                bool submit = ImGui::InputTextWithHint("##ask", hint.c_str(), g_askBuf, sizeof g_askBuf,
                                                       ImGuiInputTextFlags_EnterReturnsTrue);
                ImGui::SameLine();
                ImGui::PushStyleColor(ImGuiCol_Button, neonV);
                ImGui::PushStyleColor(ImGuiCol_ButtonHovered, ImVec4(neonV.x * 0.82f, neonV.y * 0.82f, neonV.z * 0.82f, 1.0f));
                ImGui::PushStyleColor(ImGuiCol_ButtonActive, ImVec4(neonV.x * 0.66f, neonV.y * 0.66f, neonV.z * 0.66f, 1.0f));
                ImGui::PushStyleColor(ImGuiCol_Text, ImVec4(0, 0, 0, 1));
                if (ImGui::Button("Send", ImVec2(sendW, 0))) submit = true;
                ImGui::PopStyleColor(4);

                if (submit && g_askBuf[0]) {
                    std::string q(g_askBuf);
                    g_askBuf[0] = 0;
                    if (!g_answerCardID.empty()) {
                        // CHECKLIST 25: submitting an answer is the SAME gesture as sending a chat.
                        std::string aid = g_answerCardID, aq = g_answerCardQ;
                        g_answerCardID.clear(); g_answerCardQ.clear();
                        g_askEcho = q;
                        g_askAnswer = "Saving your answer...";
                        engineCallAsync("save_answer", { {"id", aid}, {"question", aq}, {"answer", q} },
                                        30.0, "Saving your answer...", [aq](const json& r) {
                            if (r.value("ok", false)) {
                                const json& d = r.contains("data") ? r["data"] : r;
                                if (d.contains("questions")) cardsFromJSON(d);
                                g_askAnswer = "Answer saved for: \"" + aq + "\". becky will route it into the wiki.";
                            } else {
                                g_askAnswer = "Could not save that answer: " + r.value("error", std::string("?"));
                            }
                        });
                    } else {
                        // ASYNC + 120s. This was engineCall("ask", ..., 30.0) straight on the UI
                        // thread: a Claude Code turn regularly runs 10-40s, so the window was
                        // dead (and often TIMED OUT at 30s on a good answer) every single ask.
                        // Off-thread the long timeout costs nothing.
                        g_askEcho = q;
                        g_askAnswer = "thinking...";
                        engineCallAsync("ask", { {"utterance", q} }, 120.0, "becky is thinking...",
                            [](const json& r) {
                                if (r.value("ok", false)) {
                                    const json& d = r.contains("data") ? r["data"] : r;
                                    g_askAnswer = d.value("preview_text", std::string());
                                    std::string note = d.value("note", std::string());
                                    if (g_askAnswer.empty()) g_askAnswer = note.empty() ? d.dump() : note;
                                    else if (!note.empty()) g_askAnswer += "\n(" + note + ")";
                                    // A mutating turn carries an id + at least one action - anything
                                    // else (a plain answer, a Tier-0 command that already ran) has
                                    // nothing to approve, so no card.
                                    std::string id = d.value("id", std::string());
                                    json actions = d.value("actions", json::array());
                                    if (!id.empty() && actions.is_array() && !actions.empty()) {
                                        g_proposalID = id;
                                        g_proposalPreview = d.value("preview_text", std::string("(edit proposed)"));
                                        g_proposalNote = note;
                                        g_proposalDiff = d.value("preview", json::array());
                                        g_proposalPending = true;
                                    } else {
                                        g_proposalPending = false;
                                    }
                                } else {
                                    g_askAnswer = r.value("error", std::string("ask failed"));
                                    g_proposalPending = false;
                                }
                            });
                    }
                }
            }
            s_qaBottomH = ImGui::GetCursorPosY() - footY0;
```

Note the old `if (g_cards.empty()) ImGui::TextDisabled("no review questions loaded");` is **deliberately deleted** — that string is half of what he was looking at. With no questions loaded the panel now shows the status card, the chips and the ask bar, i.e. something usable, which is what the reference does.

---

## Verification (must be run by the applying agent)

1. `cd X:\AI-2\becky-tools\native\becky-review && _build.bat` — clean compile.
2. Launch. The panel must show: robot + green **ask becky**, a bordered status card naming the live backend, three green pill chips, an **ask becky...** input with a green **Send** at the panel's foot.
3. Click chip 2 → its text lands in the box, caret is in the box. Press Enter → box clears, "thinking..." appears, **and the window still drags/scrubs while it thinks** (this is the freeze fix; if the window greys, the async conversion did not take).
4. Click the timeline, press `S` — split must work (checklist 79, already handled by `main.cpp:5169-5172`; confirm the new `InputTextWithHint` didn't regress it).
5. With `BECKY_REVIEW_QUESTIONS` set: expand a card → **Answer this** → hint becomes `answer: <question>` → Enter saves and the card flips to `[answered]`.

## Skipped

- **"use Claude" checkbox** (reference `.claude` in the header) — not requested; add later as `ImGui::Checkbox` + `engineCallAsync("set_online", {{"on", v}}, ...)` in the header row, defaulting **off** (local model) exactly like `app.js:3873`.
- **Chat bubbles / scrollback** — one echoed question + one answer instead of a `.msg` history. Add a `std::deque<std::pair<std::string,std::string>>` if he asks to see the conversation.

---

# SPEC — 

# SPEC — TRANSPORT / TOOLBAR ROW, `native/becky-review/main.cpp`

## 0. Honest findings first (things I am NOT proposing)

| Checklist item | Status | Action |
|---|---|---|
| **7** — remove prev/next-frame buttons, keep shortcuts | **ALREADY MET.** There are no prev/next-frame buttons in `main.cpp`. The only frame-step is the Left/Right arrow keyboard path. | **Nothing.** Do not add them and do not "remove" anything. |
| **12** — screenshot button | **Functionally met** (`main.cpp:4907`, `grab_frame`, engine names `Screenshot_0001`). | Only relocated + given a camera icon below. No behaviour change. |
| **6a** — buttons never shift | **Met for the state-changing ones** — `fixedButton` (`main.cpp:883`) already reserves the widest label for Play/Pause, Overlay, Skip Quiet, extend. | Keep. **One real violation remains:** `"Render Selection (%d)"` at `main.cpp:4885` uses raw `ImGui::Button` with a variable-digit label, so it changes width at 9→10 clips. Fixed below. |
| **6b** — render-selection greyed not hidden | **Met** (`BeginDisabled` at `main.cpp:4886`). | Keep. |
| **13** — becky-clip button | **NOT met.** | See §6. My reading: the engine *is* `clip.exe`, so the visible becky-clip affordance is its automatic clip-finder — `autocut_silence` (`bridge.go:89`), which is also the reference's broom. Flagging this as interpretation, not verbatim instruction. |

**The real reason it "looks like shit" is not the labels — it is the container.** The toolbar lives inside the `"video"` window (`main.cpp:4691`), which is `vidW = g_W - libW - qaW` ≈ **56% of screen width** (`main.cpp:4564`). Thirteen buttons cannot fit, so it wraps into two ragged rows (`main.cpp:4904` has no `SameLine`, which is the wrap). The reference puts the transport in the **timeline header at full window width**, and that is why it reads as a toolbar. The core of this spec is that move.

---

## 1. Icons: RECOMMENDATION

**Draw the icons with `ImDrawList` primitives — but only where the shape is canonical. Keep words everywhere the concept is abstract.** Do not load an icon font (documented tofu-square failure).

Justification for a sighted, vision-impaired professional editor:

- **Silhouette survives low acuity; letterforms do not.** `▶ ‖ |◀◀ ✂` are shapes Jordan's eye has parsed for fifteen years. At 32 px a solid triangle is unambiguous; the word "Play" at the same box size is five thin strokes to resolve.
- **Primitives are pure solid fills at full palette contrast**, drawn at whatever `BECKY_UI_SCALE` he sets (`main.cpp:3880`). No hinting, no font fallback, no missing glyph.
- **But abstract controls have no canonical silhouette.** "Captions" and the 3-state "Overlay" stay as words — which is exactly what the reference does too (`index.html:95-97` are text, not glyphs). A hand-drawn shape for those would be a guess he has to decode.
- Two exceptions where I draw a figure anyway: the **runner** (checklist item 15 asks for it by name) and the **broom** (reference `#tTrimSilence`). Both get a full-sentence tooltip and a neon ON state, so the icon is a landmark, not the only information.

---

## 2. NEW CODE — icon primitives + helpers

**Anchor:** insert immediately **after** `main.cpp:888` (the closing `}` of `fixedButton`) and **before** `main.cpp:890` (`// ---- run an engine verb WITHOUT freezing the window ----`).

```cpp
// ---- TOOLBAR PALETTE (accessibility aid - never mute these) ----
// Same two accents the reference app uses (ui/app.css :root): neon #39FF14 for
// "this is on / this is the primary action", blue #00AEEF for "this touches the
// SELECTED clip". Jordan reads state by colour before he reads any label.
static const ImVec4 kTbNeon(0.224f, 1.000f, 0.078f, 1.00f);   // #39FF14
static const ImVec4 kTbNeonHi(0.550f, 1.000f, 0.420f, 1.00f);
static const ImVec4 kTbNeonLo(0.160f, 0.800f, 0.060f, 1.00f);
static const ImVec4 kTbBlue(0.000f, 0.682f, 0.937f, 1.00f);   // #00AEEF
static const ImVec4 kTbBlack(0.0f, 0.0f, 0.0f, 1.0f);

// ---- ICONS DRAWN FROM PRIMITIVES, NEVER FROM A FONT ----
// No icon font is loaded here and none will be: a glyph the font does not have
// renders as a tofu square, which has already cost this project a whole round of
// "why is the play button a box". Every icon below is triangles, lines and arcs -
// it cannot fail to a square, and it scales with FontGlobalScale (BECKY_UI_SCALE).
enum IconKind {
    ICO_START, ICO_PLAY, ICO_PAUSE, ICO_EXTL, ICO_EXTR,
    ICO_CUT, ICO_CAM, ICO_RUN, ICO_BROOM, ICO_UNDO, ICO_REDO
};

static void drawIcon(ImDrawList* dl, ImVec2 c, float s, IconKind k, ImU32 col, float th) {
    const float PI_ = 3.14159265f;
    switch (k) {
    case ICO_START:                                    // |<<  skip to start
        dl->AddRectFilled(ImVec2(c.x - s, c.y - s), ImVec2(c.x - s + th, c.y + s), col);
        dl->AddTriangleFilled(ImVec2(c.x + s * 0.05f, c.y - s), ImVec2(c.x + s * 0.05f, c.y + s),
                              ImVec2(c.x - s + th, c.y), col);
        dl->AddTriangleFilled(ImVec2(c.x + s, c.y - s), ImVec2(c.x + s, c.y + s),
                              ImVec2(c.x + s * 0.05f, c.y), col);
        break;
    case ICO_PLAY:
        dl->AddTriangleFilled(ImVec2(c.x - s * 0.72f, c.y - s), ImVec2(c.x - s * 0.72f, c.y + s),
                              ImVec2(c.x + s * 0.92f, c.y), col);
        break;
    case ICO_PAUSE:
        dl->AddRectFilled(ImVec2(c.x - s * 0.68f, c.y - s), ImVec2(c.x - s * 0.16f, c.y + s), col);
        dl->AddRectFilled(ImVec2(c.x + s * 0.16f, c.y - s), ImVec2(c.x + s * 0.68f, c.y + s), col);
        break;
    case ICO_EXTL:                                     // [<  extend selected clip earlier
        dl->AddRectFilled(ImVec2(c.x - s, c.y - s), ImVec2(c.x - s + th, c.y + s), col);
        dl->AddTriangleFilled(ImVec2(c.x + s, c.y - s * 0.82f), ImVec2(c.x + s, c.y + s * 0.82f),
                              ImVec2(c.x - s * 0.30f, c.y), col);
        break;
    case ICO_EXTR:                                     // >]  extend selected clip later
        dl->AddRectFilled(ImVec2(c.x + s - th, c.y - s), ImVec2(c.x + s, c.y + s), col);
        dl->AddTriangleFilled(ImVec2(c.x - s, c.y - s * 0.82f), ImVec2(c.x - s, c.y + s * 0.82f),
                              ImVec2(c.x + s * 0.30f, c.y), col);
        break;
    case ICO_CUT:                                      // scissors - split at playhead
        dl->AddLine(ImVec2(c.x - s * 0.55f, c.y + s * 0.50f), ImVec2(c.x + s * 0.62f, c.y - s), col, th);
        dl->AddLine(ImVec2(c.x + s * 0.55f, c.y + s * 0.50f), ImVec2(c.x - s * 0.62f, c.y - s), col, th);
        dl->AddCircle(ImVec2(c.x - s * 0.62f, c.y + s * 0.70f), s * 0.30f, col, 12, th);
        dl->AddCircle(ImVec2(c.x + s * 0.62f, c.y + s * 0.70f), s * 0.30f, col, 12, th);
        break;
    case ICO_CAM:                                      // camera - screenshot the preview
        dl->AddRect(ImVec2(c.x - s, c.y - s * 0.48f), ImVec2(c.x + s, c.y + s * 0.86f), col, 3.0f, 0, th);
        dl->AddRectFilled(ImVec2(c.x - s * 0.52f, c.y - s), ImVec2(c.x - s * 0.04f, c.y - s * 0.44f), col, 2.0f);
        dl->AddCircle(ImVec2(c.x, c.y + s * 0.20f), s * 0.40f, col, 16, th);
        dl->AddCircleFilled(ImVec2(c.x, c.y + s * 0.20f), s * 0.15f, col, 10);
        break;
    case ICO_RUN:                                      // running person - skip the quiet parts
        dl->AddCircleFilled(ImVec2(c.x + s * 0.20f, c.y - s * 0.62f), s * 0.26f, col, 12);
        dl->AddLine(ImVec2(c.x + s * 0.20f, c.y - s * 0.30f), ImVec2(c.x - s * 0.12f, c.y + s * 0.14f), col, th);
        dl->AddLine(ImVec2(c.x - s * 0.12f, c.y + s * 0.14f), ImVec2(c.x + s * 0.36f, c.y + s * 0.44f), col, th);
        dl->AddLine(ImVec2(c.x + s * 0.36f, c.y + s * 0.44f), ImVec2(c.x + s * 0.30f, c.y + s * 0.96f), col, th);
        dl->AddLine(ImVec2(c.x - s * 0.12f, c.y + s * 0.14f), ImVec2(c.x - s * 0.62f, c.y + s * 0.60f), col, th);
        dl->AddLine(ImVec2(c.x - s * 0.62f, c.y + s * 0.60f), ImVec2(c.x - s * 0.88f, c.y + s * 0.26f), col, th);
        dl->AddLine(ImVec2(c.x + s * 0.06f, c.y - s * 0.16f), ImVec2(c.x + s * 0.64f, c.y + s * 0.08f), col, th);
        dl->AddLine(ImVec2(c.x + s * 0.06f, c.y - s * 0.16f), ImVec2(c.x - s * 0.52f, c.y - s * 0.34f), col, th);
        break;
    case ICO_BROOM:                                    // becky-clip: sweep out the dead air
        dl->AddLine(ImVec2(c.x + s * 0.78f, c.y - s * 0.95f), ImVec2(c.x - s * 0.06f, c.y + s * 0.04f), col, th);
        dl->AddQuadFilled(ImVec2(c.x - s * 0.36f, c.y + s * 0.00f), ImVec2(c.x + s * 0.22f, c.y + s * 0.00f),
                          ImVec2(c.x + s * 0.56f, c.y + s * 0.54f), ImVec2(c.x - s * 0.74f, c.y + s * 0.54f), col);
        for (int i = 0; i < 3; i++) {
            float f = -0.45f + 0.45f * (float)i;
            dl->AddLine(ImVec2(c.x + s * f * 0.95f, c.y + s * 0.54f),
                        ImVec2(c.x + s * f * 1.20f, c.y + s * 0.98f), col, th * 0.75f);
        }
        break;
    case ICO_UNDO:
    case ICO_REDO: {                                   // arc + arrowhead
        float r = s * 0.70f;
        dl->PathArcTo(c, r, PI_, PI_ * 2.0f, 24);
        dl->PathStroke(col, 0, th);
        float ex = (k == ICO_UNDO) ? c.x - r : c.x + r;
        dl->AddTriangleFilled(ImVec2(ex - s * 0.32f, c.y - s * 0.06f),
                              ImVec2(ex + s * 0.32f, c.y - s * 0.06f),
                              ImVec2(ex,             c.y + s * 0.52f), col);
    } break;
    }
}

// A toolbar icon button. FIXED footprint whatever it shows (item 6: nothing on
// this row ever moves), and the icon inherits ImGui's disabled alpha for free
// because GetColorU32 multiplies by style.Alpha - so BeginDisabled greys the
// drawn icon exactly the way it greys a text button.
static bool iconButton(const char* id, IconKind k, const char* tip,
                       bool on = false, const ImVec4* tint = nullptr) {
    const float h = ImGui::GetFrameHeight();
    if (on) {
        ImGui::PushStyleColor(ImGuiCol_Button,        kTbNeon);
        ImGui::PushStyleColor(ImGuiCol_ButtonHovered, kTbNeonHi);
        ImGui::PushStyleColor(ImGuiCol_ButtonActive,  kTbNeonLo);
    }
    bool pressed = ImGui::Button(id, ImVec2(h * 1.30f, h));
    ImVec2 mn = ImGui::GetItemRectMin(), mx = ImGui::GetItemRectMax();
    if (on) ImGui::PopStyleColor(3);
    ImU32 col = on   ? ImGui::GetColorU32(kTbBlack)
              : tint ? ImGui::GetColorU32(*tint)
                     : ImGui::GetColorU32(ImGuiCol_Text);
    drawIcon(ImGui::GetWindowDrawList(),
             ImVec2((mn.x + mx.x) * 0.5f, (mn.y + mx.y) * 0.5f),
             h * 0.30f, k, col, std::max(2.0f, h * 0.075f));
    if (tip && ImGui::IsItemHovered(ImGuiHoveredFlags_AllowWhenDisabled))
        ImGui::SetTooltip("%s", tip);
    return pressed;
}

// A thin vertical rule between toolbar GROUPS. Grouping is what makes a row of
// 18 controls readable as five things instead of eighteen.
static void toolSep() {
    ImGui::SameLine(0, 10);
    ImVec2 p = ImGui::GetCursorScreenPos();
    float h = ImGui::GetFrameHeight();
    ImGui::GetWindowDrawList()->AddLine(ImVec2(p.x, p.y + 3), ImVec2(p.x, p.y + h - 3),
                                        ImGui::GetColorU32(ImGuiCol_Separator), 1.0f);
    ImGui::Dummy(ImVec2(1, h));
    ImGui::SameLine(0, 10);
}
```

---

## 3. NEW GLOBAL — caption lane toggle

**Anchor:** insert **after** `main.cpp:2361`:
```cpp
static std::string g_capErr;         // plain-language load/save problem, shown in the lane
```
**Insert:**
```cpp
// "Captions" toolbar toggle (the reference app's #tCaptions). Default ON so a
// reel that has an .srt behaves byte-identically to before this button existed.
static bool g_capLaneOn = true;
```

**Anchor:** `main.cpp:2642` — replace this exact line:
```cpp
    bool showCaps = !g_capPath.empty() && lanesH > 90;
```
**with:**
```cpp
    bool showCaps = g_capLaneOn && !g_capPath.empty() && lanesH > 90;
```

---

## 4. THE TOOLBAR FUNCTION

**Anchor:** insert **after** `main.cpp:3655` (closing `}` of `playWholeVideo`) and **before** `main.cpp:3657` (`// openTranscript opens a video's transcript (B-8)...`). Every helper it calls (`clipAtComp` 1387, `sourceFps` 1405, `setOverlayMode` 1522, `emitThreshold` 1821, `loadTimelineView` 1865, `loadCaptions` 2413, `openInFileBrowser` 3547, `pickOpenReelFile` 3563, `convertEditIfNeeded` 3600, `queueEdit` 390, `engineCallAsync` 917) is already defined above this point.

```cpp
// ============================ THE TOOLBAR ROW ================================
// ONE row, full window width, in the TIMELINE header - not two ragged rows
// squeezed into the 56%-wide video pane, which is what it was and what made it
// read as a pile of text instead of a transport.
//
// Order and grouping mirror the reference app (gui/BeckyReviewNative/ui/index.html
// .transport + .tlactions), because ten rounds of Jordan's feedback are baked into
// that order and his hands already know it:
//
//   [ |<< ][ >/|| ][ 1x ] | [ [< ][ >] ][ scissors ] | [ cam ][ runner ][ broom ]
//   | [Captions][Overlay: ...]  ......  [ undo ][ redo ] | [Save][Load][EDL]
//   [Render Selection (N)][Export]
//
// Everything is exactly ImGui::GetFrameHeight() tall, so the row is even; every
// icon box is the same width, so the eye can count positions; every label that
// changes uses fixedButton, so nothing ever slides under his cursor.
static void drawTransportBar(double& curSec, bool& playing, double& lastComposed, HWND hwnd) {
    ImGuiStyle& st = ImGui::GetStyle();
    const float h   = ImGui::GetFrameHeight();
    const float iw  = h * 1.30f;
    const float gap = st.ItemSpacing.x;
    auto tw = [&](const char* s) { return ImGui::CalcTextSize(s).x + st.FramePadding.x * 2.0f; };

    // The same playhead rule the keyboard edits use (editT, main.cpp:4017): while
    // playing, edit at the STOCK, not at the flying playhead.
    const double t = (playing && g_stockSec >= 0) ? g_stockSec : curSec;

    // ---------------- GROUP 1: transport ----------------
    if (iconButton("##tostart", ICO_START, "Back to the start of the reel")) {
        curSec = 0; g_playingExt = playing;
    }
    ImGui::SameLine();
    if (iconButton("##play", playing ? ICO_PAUSE : ICO_PLAY, "Play / pause (Space)", playing)) {
        playing = !playing; g_playingExt = playing;
    }
    ImGui::SameLine();
    {
        bool fast = g_playRate > 1.5;
        if (fast) {
            ImGui::PushStyleColor(ImGuiCol_Button,        kTbNeon);
            ImGui::PushStyleColor(ImGuiCol_ButtonHovered, kTbNeonHi);
            ImGui::PushStyleColor(ImGuiCol_ButtonActive,  kTbNeonLo);
            ImGui::PushStyleColor(ImGuiCol_Text,          kTbBlack);
        }
        // "1x" and "2x" are the same two characters, so this box never resizes.
        if (ImGui::Button(fast ? "2x##rate" : "1x##rate", ImVec2(iw, h)))
            g_playRate = fast ? 1.0 : 2.0;
        if (fast) ImGui::PopStyleColor(4);
        if (ImGui::IsItemHovered()) ImGui::SetTooltip("Playback speed - click for 2x");
    }
    toolSep();

    // ---------------- GROUP 2: edit the SELECTED clip (BLUE) ----------------
    // Blue, exactly as in the reference (.tbtn2.extend), because these three act on
    // the SELECTION while the transport acts on the reel - one glance tells them apart.
    {
        Clip* sc = nullptr;
        for (auto& c : g_track[0]) if (g_sel.count(c.id)) { sc = &c; break; }
        bool canTrim = sc && !sc->id.empty() && !g_editsInFlight.count(sc->id);
        double fps = sc ? sourceFps(sc->source) : 30.0;
        if (fps <= 0) fps = 30.0;
        const double oneFrame = 1.0 / fps;

        if (!canTrim) ImGui::BeginDisabled();
        if (iconButton("##extl", ICO_EXTL,
                "Extend the selected clip one frame EARLIER (its own source rate)", false, &kTbBlue)
            && sc->in > oneFrame) {
            EditReq req; req.verb = "set_trim";
            req.args = { {"id", sc->id}, {"in", sc->in - oneFrame}, {"out", sc->out} };
            req.kind = 2; req.t = t; req.group = g_group;
            g_editsInFlight.insert(sc->id);
            queueEdit(std::move(req));
        }
        ImGui::SameLine();
        if (iconButton("##extr", ICO_EXTR,
                "Extend the selected clip one frame LATER (its own source rate)", false, &kTbBlue)) {
            EditReq req; req.verb = "set_trim";
            req.args = { {"id", sc->id}, {"in", sc->in}, {"out", sc->out + oneFrame} };
            req.kind = 2; req.t = t; req.group = g_group;
            g_editsInFlight.insert(sc->id);
            queueEdit(std::move(req));
        }
        if (!canTrim) ImGui::EndDisabled();
    }
    ImGui::SameLine();
    // Split at the playhead - the SAME queued edit the S key raises (main.cpp:4049),
    // so a click and a keypress are one code path and can never drift apart.
    {
        Clip* c = clipAtComp(0, t);
        bool canSplit = c && !c->id.empty() && !g_editsInFlight.count(c->id);
        if (!canSplit) ImGui::BeginDisabled();
        if (iconButton("##split", ICO_CUT, "Split the clip at the playhead (S)")) {
            double srcT = c->in + (t - c->compStart);
            EditReq req; req.verb = "split"; req.args = { {"id", c->id}, {"at", srcT} };
            req.kind = 0; req.t = t; req.group = g_group;
            g_editsInFlight.insert(c->id);
            queueEdit(std::move(req));
            if (!playing) lastComposed = -1;
        }
        if (!canSplit) ImGui::EndDisabled();
    }
    toolSep();

    // ---------------- GROUP 3: capture + automation ----------------
    if (iconButton("##shot", ICO_CAM, "Screenshot the preview (Screenshot_0001.png, then 0002...)")) {
        Clip* cur = nullptr;
        for (auto& c : g_track[0])
            if (curSec >= c.compStart && curSec < c.compStart + (c.out - c.in)) { cur = &c; break; }
        if (!cur && !g_track[0].empty()) cur = &g_track[0].back();
        if (cur) {
            double srcT = cur->in + (curSec - cur->compStart);
            json r = engineCall("grab_frame", { {"source", cur->source}, {"t", srcT} }, 20.0);
            g_renderMsg = r.value("ok", false)
                ? "Saved " + r.value("data", json::object()).value("path", std::string())
                : "Screenshot failed: " + r.value("error", std::string("?"));
        } else g_renderMsg = "Screenshot failed: no clip at playhead";
        g_renderMsgAt = nowSec();
    }
    ImGui::SameLine();
    // Item 15: the playback-threshold toggle, "drawn as a running-person icon".
    if (iconButton("##quiet", ICO_RUN,
            "Skip everything quieter than the threshold bar during playback.\n"
            "Drag the bar on the timeline to set the level. The evidence is NOT cut.",
            g_thrOn)) {
        g_thrOn = !g_thrOn;
        g_quietDirty = true;
        emitThreshold(true);
    }
    ImGui::SameLine();
    // Item 13: the becky-clip button. See section 6 for what it does and why set_clips.
    {
        Clip* c = clipAtComp(0, t);
        bool canAuto = c && !c->id.empty() && !g_editsInFlight.count(c->id);
        if (!canAuto) ImGui::BeginDisabled();
        if (iconButton("##autoclip", ICO_BROOM,
                "becky-clip: find the real clips inside this one (drop the dead air)\n"
                "and replace it with them. ONE Ctrl+Z puts it back.")) {
            std::string tid = c->id, tsrc = c->source, tlab = c->label;
            double tin = c->in, tout = c->out;
            engineCallAsync("autocut_silence", { {"name", baseName(tsrc)} }, 180.0,
                            "becky-clip: finding the clips...",
                [tid, tsrc, tlab, tin, tout](const json& r) {
                    if (!r.value("ok", false)) {
                        g_renderMsg = "becky-clip failed: " + r.value("error", std::string("?"));
                        g_renderMsgAt = nowSec(); return;
                    }
                    const json& d = r.contains("data") ? r["data"] : r;
                    json segs = d.value("segments", json::array());
                    std::string note = d.value("note", std::string());
                    // Rebuild the WHOLE clip list with the target replaced by its loud
                    // segments. set_clips is ONE undoable engine edit (app.go SetClips),
                    // so Ctrl+Z restores the original clip in a single press - N separate
                    // add_clip calls would cost him N presses, the "phantom moves" undo bug.
                    json clips = json::array();
                    int made = 0;
                    for (auto& cc : g_track[0]) {
                        if (cc.id != tid) {
                            clips.push_back({ {"source", cc.source}, {"in", cc.in},
                                              {"out", cc.out}, {"label", cc.label} });
                            continue;
                        }
                        for (auto& s : segs) {
                            double a = std::max(tin,  s.value("in",  0.0));
                            double b = std::min(tout, s.value("out", 0.0));
                            if (b - a < 0.20) continue;      // slivers are noise, not clips
                            clips.push_back({ {"source", tsrc}, {"in", a}, {"out", b}, {"label", tlab} });
                            made++;
                        }
                        if (made == 0)
                            clips.push_back({ {"source", cc.source}, {"in", cc.in},
                                              {"out", cc.out}, {"label", cc.label} });
                    }
                    if (made == 0) {
                        g_renderMsg = note.empty() ? "becky-clip found nothing to cut in this clip"
                                                   : "becky-clip: " + note;
                        g_renderMsgAt = nowSec(); return;
                    }
                    engineCallAsync("set_clips", { {"clips", clips} }, 30.0, "Applying becky-clip...",
                        [made](const json& r2) {
                            if (r2.value("ok", false)) {
                                loadTimelineView(r2.contains("data") ? r2["data"] : r2);
                                g_renderMsg = "becky-clip: " + std::to_string(made)
                                            + " clips (Ctrl+Z undoes it)";
                            } else g_renderMsg = "becky-clip apply failed: "
                                               + r2.value("error", std::string("?"));
                            g_renderMsgAt = nowSec();
                        });
                });
        }
        if (!canAuto) ImGui::EndDisabled();
    }
    toolSep();

    // ---------------- GROUP 4: word toggles (no canonical icon exists) ----------------
    {
        bool on = g_capLaneOn;
        if (on) {
            ImGui::PushStyleColor(ImGuiCol_Button,        kTbNeon);
            ImGui::PushStyleColor(ImGuiCol_ButtonHovered, kTbNeonHi);
            ImGui::PushStyleColor(ImGuiCol_ButtonActive,  kTbNeonLo);
            ImGui::PushStyleColor(ImGuiCol_Text,          kTbBlack);
        }
        if (fixedButton("Captions##caps", { "Captions" })) g_capLaneOn = !g_capLaneOn;
        if (on) ImGui::PopStyleColor(4);
        if (ImGui::IsItemHovered())
            ImGui::SetTooltip("Caption lane on / off. Click a caption to fix the words,\n"
                              "drag it (or its edges) to fix the timing.");
    }
    ImGui::SameLine();
    {
        const char* ovLabel = g_ovMode == 0 ? "Overlay: Off##ov"
                            : g_ovMode == 1 ? "Overlay: On (hidden)##ov"
                                            : "Overlay: On (shown)##ov";
        if (fixedButton(ovLabel, { "Overlay: Off", "Overlay: On (hidden)", "Overlay: On (shown)" }))
            setOverlayMode((g_ovMode + 1) % 3);
    }

    // ---------------- right-aligned: history + output ----------------
    // Reserve the RIGHT group's exact width and push it to the window edge, the way
    // .tlactions sits opposite .transport in the reference. Export ends up in the
    // same screen corner every session - a fixed target he never has to hunt for.
    const float rightW = iw * 2 + gap + 21.0f
                       + tw("Save") + gap + tw("Load") + gap + tw("EDL") + gap
                       + tw("Render Selection (99)") + gap + tw("Export");
    {
        float usedX  = ImGui::GetItemRectMax().x - ImGui::GetWindowPos().x;
        float targetX = ImGui::GetContentRegionMax().x - rightW;
        if (targetX > usedX + 12.0f) ImGui::SameLine(targetX);
        else                         ImGui::SameLine(0, 12.0f);
    }

    // Same 250ms debounce the keyboard uses (main.cpp:4143/4180): a double-fired undo
    // walks PAST the intended edit, which once emptied a whole demo reel.
    if (iconButton("##undo", ICO_UNDO, "Undo (Ctrl+Z)")) {
        double n = nowSec();
        if (n - g_lastUndoQueued > 0.25) {
            g_lastUndoQueued = n;
            EditReq req; req.verb = "undo"; req.args = json::object(); req.kind = 4; req.t = curSec;
            queueEdit(std::move(req));
        }
    }
    ImGui::SameLine();
    if (iconButton("##redo", ICO_REDO, "Redo (Ctrl+Y or Ctrl+Shift+Z)")) {
        double n = nowSec();
        if (n - g_lastRedoQueued > 0.25) {
            g_lastRedoQueued = n;
            EditReq req; req.verb = "redo"; req.args = json::object(); req.kind = 4; req.t = curSec;
            queueEdit(std::move(req));
        }
    }
    toolSep();

    if (fixedButton("Save##savereel", { "Save" })) {
        engineCallAsync("save_reel", { {"path", ""} }, 20.0, "Saving reel...", [](const json& r) {
            g_renderMsg = r.value("ok", false)
                ? "Saved reel " + r.value("data", json::object()).value("path", std::string())
                : "Save reel failed: " + r.value("error", std::string("?"));
            g_renderMsgAt = nowSec();
        });
    }
    if (ImGui::IsItemHovered()) ImGui::SetTooltip("Save this reel to a file");
    ImGui::SameLine();
    if (fixedButton("Load##loadreel", { "Load" })) {
        std::string picked = pickOpenReelFile(hwnd);
        if (!picked.empty()) {
            std::string path = convertEditIfNeeded(picked);
            if (!path.empty()) {
                json r = engineCall("load_reel", { {"path", path} }, 30.0);
                if (r.value("ok", false)) {
                    loadTimelineView(r.contains("data") ? r["data"] : r);
                    curSec = 0; playing = false; g_playingExt = false; lastComposed = -1;
                    loadCaptions(path);
                    g_renderMsg = "Loaded reel " + baseName(path);
                } else g_renderMsg = "Load reel failed: " + r.value("error", std::string("?"));
                g_renderMsgAt = nowSec();
            }
        }
    }
    if (ImGui::IsItemHovered()) ImGui::SetTooltip("Load a reel from a file");
    ImGui::SameLine();
    if (fixedButton("EDL##edl", { "EDL" })) {
        engineCallAsync("write_edl", { {"output", ""} }, 30.0, "Writing EDL...", [](const json& r) {
            if (r.value("ok", false)) {
                std::string p = r.value("data", json::object()).value("path", std::string());
                g_renderMsg = "Wrote EDL " + p; openInFileBrowser(p);
            } else g_renderMsg = "Export EDL failed: " + r.value("error", std::string("?"));
            g_renderMsgAt = nowSec();
        });
    }
    if (ImGui::IsItemHovered()) ImGui::SetTooltip("Write an EDL of this timeline");
    ImGui::SameLine();

    // Item 6: GREYED, NEVER HIDDEN - and fixedButton against the widest count the
    // label can ever show, because the raw Button it replaces grew a whole character
    // at 9 -> 10 selected clips and shoved Export sideways mid-session.
    {
        char selLabel[48];
        snprintf(selLabel, sizeof selLabel, "Render Selection (%d)##rsel", (int)g_sel.size());
        if (g_sel.empty()) ImGui::BeginDisabled();
        ImGui::PushStyleColor(ImGuiCol_Text, kTbBlue);
        bool go = fixedButton(selLabel, { "Render Selection (99)" });
        ImGui::PopStyleColor();
        if (go) {
            std::vector<std::string> ids(g_sel.begin(), g_sel.end());
            engineCallAsync("export_selection", { {"ids", ids}, {"output", ""} }, 300.0,
                            "Rendering selection...", [](const json& r) {
                if (r.value("ok", false)) {
                    const json& d = r.contains("data") ? r["data"] : r;
                    std::string caps = d.value("captions", std::string());
                    g_renderMsg = "Rendered " + d.value("mp4", std::string()) +
                                  (caps.empty() ? "  - NO captions (use Export for a captioned file)"
                                                : "  - captions burned in");
                    openInFileBrowser(d.value("mp4", std::string()));
                } else g_renderMsg = "Render failed: " + r.value("error", std::string("?"));
                g_renderMsgAt = nowSec();
            });
        }
        if (g_sel.empty()) ImGui::EndDisabled();
        if (ImGui::IsItemHovered(ImGuiHoveredFlags_AllowWhenDisabled))
            ImGui::SetTooltip("Render only the selected clips");
    }
    ImGui::SameLine();

    // The primary action, solid neon on black text - the one control on this row
    // that is impossible to miss, in the same corner the reference puts it.
    ImGui::PushStyleColor(ImGuiCol_Button,        kTbNeon);
    ImGui::PushStyleColor(ImGuiCol_ButtonHovered, kTbNeonHi);
    ImGui::PushStyleColor(ImGuiCol_ButtonActive,  kTbNeonLo);
    ImGui::PushStyleColor(ImGuiCol_Text,          kTbBlack);
    bool doExport = fixedButton("Export##export", { "Export" });
    ImGui::PopStyleColor(4);
    if (ImGui::IsItemHovered()) ImGui::SetTooltip("Export the whole compilation");
    if (doExport) {
        engineCallAsync("export", { {"output", ""} }, 300.0, "Rendering video...", [](const json& r) {
            if (r.value("ok", false)) {
                const json& d = r.contains("data") ? r["data"] : r;
                std::string caps = d.value("captions", std::string());
                g_renderMsg = "Rendered " + d.value("mp4", std::string()) +
                              (caps.empty() ? "  - NO captions in this file"
                                            : "  - captions burned in");
                openInFileBrowser(d.value("mp4", std::string()));
            } else g_renderMsg = "Render failed: " + r.value("error", std::string("?"));
            g_renderMsgAt = nowSec();
        });
    }
}
```

---

## 5. WIRING — three edits in the main loop

### 5a. Give the timeline window the row's height back

**Anchor:** `main.cpp:4562` — replace this exact line:
```cpp
        const float timelineH = (std::max)(180.0f, availH * 0.26f);
```
**with:**
```cpp
        // + one toolbar row: the transport moved out of the 56%-wide video pane and
        // into this header, where it has the whole window width. Growing the panel by
        // exactly the row's height means the CLIPS keep every pixel they had (item 4).
        const float toolbarH = ImGui::GetFrameHeight() + ImGui::GetStyle().ItemSpacing.y;
        const float timelineH = (std::max)(180.0f, availH * 0.26f) + toolbarH;
```

### 5b. Reclaim the video pane and delete the old two rows

**Anchor:** `main.cpp:4706` — replace this exact line:
```cpp
                float ctrlH = ImGui::GetTextLineHeightWithSpacing() * 2 + ImGui::GetFrameHeightWithSpacing() * 2;
```
**with:**
```cpp
                // Only the two TEXT lines now (position readout + g_renderMsg). Both
                // button rows moved to the timeline header, so the preview gets that
                // ~90px of height back - which for a video editor is the whole point.
                float ctrlH = ImGui::GetTextLineHeightWithSpacing() * 2;
```

**DELETE `main.cpp:4782` through `main.cpp:4949` inclusive** — that is everything from
```cpp
            if (fixedButton(playing ? "Pause##play" : "Play##play", { "Pause", "Play" })) { playing = !playing; g_playingExt = playing; }
```
down to and including the closing `});` + `}` of the `Export EDL` block:
```cpp
            if (ImGui::Button("Export EDL")) {
                ...
                });
            }
```

**KEEP** `main.cpp:4781` (`ImGui::Text("%.1f / %.1f s", curSec, g_compDur);`) and `main.cpp:4950` (the `g_renderMsg` line) exactly where they are. After the delete they sit adjacent.

### 5c. Draw the row in the timeline header

**Anchor:** `main.cpp:5173` — insert **before** this exact line:
```cpp
            drawTimeline(curSec, playing);
```
**Insert:**
```cpp
            drawTransportBar(curSec, playing, lastComposed, hwnd);
            ImGui::Separator();
```
`drawTimeline` sizes itself from `ImGui::GetContentRegionAvail()` (`main.cpp:2631-2632`), so it automatically takes whatever is left — and 5a already handed it back the same amount. No other timeline change is needed.

---

## 6. Sizing table (what an applying agent should verify on screen)

| Control | Width | Height | Colour |
|---|---|---|---|
| Every icon button | `GetFrameHeight() * 1.30` (~41 px at default scale) | `GetFrameHeight()` (~32 px) | text white; **neon fill + black icon when ON**; blue icon for the two extend buttons |
| `1x/2x` | same `1.30 × h` box | same | neon fill + black text when 2x |
| `Captions`, `Overlay: …` | `fixedButton` widest state | same | neon fill + black text when captions ON |
| `Save`, `Load`, `EDL`, `Export` | `fixedButton` | same | Export = neon fill, black text |
| `Render Selection (N)` | `fixedButton` against `"Render Selection (99)"` | same | blue text; `BeginDisabled` when empty |
| Group separator | 1 px rule, 10 px either side | `h - 6` | `ImGuiCol_Separator` |

Total measured width at `BECKY_UI_SCALE=1.35` is ≈1350 px, so the row fits one line on his 1920 desktop with the right group flush to the edge. Below ≈1400 px of window width the right-align degrades to a plain 12 px gap and the row simply runs long rather than wrapping — deliberate, because a wrapping toolbar is the bug being fixed.

---

## 7. Build + what to check

```
X:\AI-2\becky-tools\native\becky-review\_build.bat
```
ImGui here is **1.90.9** (`native/timeline-bench/third_party/imgui/imgui.h:30`) — `AddQuadFilled`, `AddTriangleFilled`, `AddCircle`, `PathArcTo`, `PathStroke(col, flags, thickness)` and both `GetColorU32` overloads are all present. `NOMINMAX` is defined at `main.cpp:27`, so bare `std::max`/`std::min` compile. No new includes, no new dependency.

On launch, confirm by eye: one even row across the full window; the nine icon boxes identical in size; Play flips to two bars without the row moving; select 9 then 10 clips and check `Render Selection` does not push `Export`; deselect and check it greys instead of vanishing; toggle the runner and confirm the threshold bar appears and the button turns solid neon.

---

Skipped: an `overlay`-icon, a `name` toggle (not present in this app), and any restyling of the 3-state Overlay label — all already meet item 6 and none are in scope. Add when Jordan asks for the filename line.

---

# SPEC — 

# SPEC — LEFT LIBRARY / SEARCH PANEL → CARDS

Target: `X:\AI-2\becky-tools\native\becky-review\main.cpp` (5204 lines, ImGui **1.90.9**, `FontGlobalScale = 1.35`).
I read the reference (`gui\BeckyReviewNative\ui\app.css` `.file`/`.tbtn`/`.findhead`/`.searchbar`/`.sortbtn`, `ui\app.js` `fileRowHTML`/`renderFiles`/`sortControlHTML`) and the live ImGui panel (main.cpp:4567-4687). **I did not compile this** — the applying agent must run `native\becky-review\_build.bat`.

## Already as good as the reference — propose nothing

- Search-hit rows (`main.cpp:4587-4611`): the `"%d quotes - %d playable, %d transcript-only"` summary, click=play-at-verbatim-timestamp, double-click=add to timeline, hover tooltip. Reference `.qrow` has no more than this.
- The flowing transcript view + "search within this transcript" (`main.cpp:4612-4627`) — checklist 23/24/78 already met.
- The `!ImGui::GetIO().WantTextInput` focus guard and the single `g_libSel` shared by mouse+arrows (`main.cpp:4637-4640`, `4680-4683`) — checklist 73/74/75/76 already met. **Do not touch those lines.**
- Checklist **19** (crown = "most relevant") and **21** (index icon) are *quote-list* items, not the video list. Out of this area — leave to whoever owns the hits list.

The actual gap is exactly two things: the video rows, and the search row.

---

## 1. `VideoRow` gains a display-name cache

**Anchor — main.cpp:3317, replace this exact line:**
```cpp
struct VideoRow { std::string path, name, date; bool hasTranscript = false; };
```
**with:**
```cpp
struct VideoRow {
    std::string path, name, date; bool hasTranscript = false;
    // B-1 card display cache: the middle-ellipsised name and the width it was
    // measured at. Recomputed only when the panel width changes, so a 2258-video
    // corpus costs zero CalcTextSize work per frame while the panel is still.
    std::string disp; float dispW = -1.0f;
};
```

## 2. Two new globals

**Anchor — main.cpp:3324, insert immediately AFTER this exact line:**
```cpp
static int g_sortMode = 0;
```
**add:**
```cpp
// "Transcribe all" is in flight (UI thread only: set on click, cleared in the
// drainAsync callback, which also runs on the UI thread).
static bool g_transcribeAllBusy = false;
```

**Anchor — main.cpp:3407, insert immediately AFTER this exact line:**
```cpp
static char g_searchBuf[256] = { 0 };
```
**add:**
```cpp
// Checklist 20: qmd is a persistent TOGGLE (the reference's "smart" pill), not a
// second submit button - so Enter always runs the mode he can see is armed.
static bool g_smartSearch = false;
```

## 3. New helpers: middle-ellipsis, pill, card

**Anchor — main.cpp:3490-3491, the end of `sortLibrary()`. Insert the whole block immediately AFTER these two exact lines:**
```cpp
    std::sort(g_videos.begin(), g_videos.end(), cmp);
}
```

```cpp
// ---------------- B-1: the library is CARDS, not a flat list ----------------
// The reference GUI (gui/BeckyReviewNative) shows each video as a tall rounded
// card: a big readable filename, a dim date/status line, and one large round
// green "+" that transcribes it. This app showed an ImGui::Selectable per video,
// which sliced the filename mid-word and offered no visible affordance at all.
// Jordan reads the screen with difficulty - a truncated name is not a cosmetic
// problem, it is the difference between finding the video and not.
// Immediate-mode: two InvisibleButtons and a handful of ImDrawList calls. No
// widget framework, no theme system.

// MIDDLE-ellipsis, not tail-ellipsis. His filenames are
// "2026-07-19_they_tried_to_kill_me.mp4" - the head (the date he scans by) and
// the tail (the extension, and the digits that tell near-duplicates apart) are
// BOTH load-bearing; the middle is the disposable part. Tail-truncation throws
// away the half that disambiguates. The card also tooltips the FULL name.
static std::string midEllipsis(const std::string& s, float maxW) {
    if (maxW <= 0.0f || ImGui::CalcTextSize(s.c_str()).x <= maxW) return s;
    size_t tail = (std::min)(s.size() / 3, (size_t)12);
    std::string tailS = s.substr(s.size() - tail);
    for (size_t head = s.size() - tail; head > 1; head--) {
        std::string out = s.substr(0, head - 1) + "..." + tailS;
        if (ImGui::CalcTextSize(out.c_str()).x <= maxW) return out;
    }
    return "..." + tailS;
}

// The reference's little rounded segmented control (.sortbtn / .smartbtn): a pill
// that is TINTED WITH ITS ACCENT COLOUR when on, outlined when off. Colour is how
// he reads state - never render these as plain grey text.
static bool pillButton(const char* label, bool on, ImU32 accent) {
    const float S = ImGui::GetIO().FontGlobalScale;
    ImVec4 a = ImGui::ColorConvertU32ToFloat4(accent);
    ImGui::PushStyleVar(ImGuiStyleVar_FrameRounding, 999.0f);
    ImGui::PushStyleVar(ImGuiStyleVar_FramePadding, ImVec2(11.0f * S, 4.0f * S));
    ImGui::PushStyleColor(ImGuiCol_Button,        on ? ImVec4(a.x * 0.22f, a.y * 0.22f, a.z * 0.22f, 1.0f) : ImVec4(0, 0, 0, 0));
    ImGui::PushStyleColor(ImGuiCol_ButtonHovered, ImVec4(a.x * 0.36f, a.y * 0.36f, a.z * 0.36f, 1.0f));
    ImGui::PushStyleColor(ImGuiCol_ButtonActive,  ImVec4(a.x * 0.52f, a.y * 0.52f, a.z * 0.52f, 1.0f));
    ImGui::PushStyleColor(ImGuiCol_Text,          on ? ImVec4(1, 1, 1, 1) : ImVec4(0.62f, 0.66f, 0.72f, 1.0f));
    bool hit = ImGui::Button(label);
    ImGui::GetWindowDrawList()->AddRect(ImGui::GetItemRectMin(), ImGui::GetItemRectMax(),
        on ? accent : IM_COL32(255, 255, 255, 38), 999.0f, 0, on ? 2.0f : 1.0f);
    ImGui::PopStyleColor(4); ImGui::PopStyleVar(2);
    return hit;
}

struct LibCardResult { bool clicked = false, dbl = false, plus = false; };

// ONE number, so the card and the list clipper can never disagree about row height.
static float libCardHeight() {
    const float S = ImGui::GetIO().FontGlobalScale;
    return 10.0f * S * 2.0f + ImGui::GetTextLineHeight() * 2.0f + 4.0f * S;
}
static float libCardStride() { return libCardHeight() + 8.0f * ImGui::GetIO().FontGlobalScale; }

// One card. `accent` is the colour this video's clips already wear on the timeline
// (0 = none on the timeline yet). Returns what the user did; the CALLER performs
// the actions, so this helper needs nothing declared later in the file.
static LibCardResult drawLibraryCard(VideoRow& v, bool selected, bool justViewed,
                                     bool inFlight, ImU32 accent) {
    LibCardResult res;
    const float S    = ImGui::GetIO().FontGlobalScale;
    const float pad  = 10.0f * S;
    const float lh   = ImGui::GetTextLineHeight();
    const float btnD = 30.0f * S;                    // round action button
    const float h    = libCardHeight();
    const float w    = ImGui::GetContentRegionAvail().x;
    if (w < 40.0f) return res;                       // InvisibleButton asserts on a zero size
    ImDrawList* dl   = ImGui::GetWindowDrawList();
    const ImVec2 p0  = ImGui::GetCursorScreenPos();
    const ImVec2 p1  = ImVec2(p0.x + w, p0.y + h);

    // --- the card body. AllowOverlap so the round button submitted below can
    //     steal the click when the cursor is over it.
    ImGui::SetNextItemAllowOverlap();
    ImGui::InvisibleButton("##card", ImVec2(w, h));
    bool hov    = ImGui::IsItemHovered();
    res.clicked = ImGui::IsItemClicked(ImGuiMouseButton_Left);
    res.dbl     = hov && ImGui::IsMouseDoubleClicked(ImGuiMouseButton_Left);
    if (ImGui::IsItemClicked(ImGuiMouseButton_Right)) ImGui::OpenPopup("rowctx");

    // Checklist 35/101: selection is a FILL, never a yellow/white outline.
    ImU32 bg = selected ? IM_COL32(28, 44, 28, 255)
             : hov      ? IM_COL32(24, 28, 22, 255)
                        : IM_COL32(20, 22, 26, 255);
    dl->AddRectFilled(p0, p1, bg, 7.0f * S);
    dl->AddRect(p0, p1, selected ? IM_COL32(0x14, 0xFF, 0x39, 255) : IM_COL32(255, 255, 255, 26),
                7.0f * S, 0, selected ? 2.0f : 1.0f);
    // Checklist 22: the transcript just viewed keeps a green outline after "back".
    if (justViewed)
        dl->AddRect(ImVec2(p0.x - 1, p0.y - 1), ImVec2(p1.x + 1, p1.y + 1),
                    IM_COL32(0x14, 0xFF, 0x39, 255), 8.0f * S, 0, 2.0f);
    // Checklist 32/36/37: the card wears the SAME colour its clips wear on the
    // timeline, so "which library video is the crimson stuff from" is one glance.
    if (accent) dl->AddRectFilled(p0, ImVec2(p0.x + 4.0f * S, p1.y), accent, 7.0f * S, ImDrawFlags_RoundCornersLeft);

    // --- text. Name is big; the sub-line is dim and never competes with it.
    const float textX = p0.x + pad + (accent ? 6.0f * S : 0.0f);
    const float textR = p1.x - pad - btnD - 8.0f * S;
    const float nameW = textR - textX;
    if (v.dispW != nameW) { v.disp = midEllipsis(v.name, nameW); v.dispW = nameW; }
    dl->PushClipRect(ImVec2(textX, p0.y), ImVec2(textR, p1.y), true);
    dl->AddText(ImVec2(textX, p0.y + pad), IM_COL32(235, 238, 245, 255), v.disp.c_str());
    std::string sub = v.date;
    const char* status = inFlight ? "transcribing..." : (v.hasTranscript ? nullptr : "no transcript");
    if (status) { if (!sub.empty()) sub += "  -  "; sub += status; }
    if (!sub.empty())
        dl->AddText(ImVec2(textX, p0.y + pad + lh + 4.0f * S),
                    inFlight ? IM_COL32(0xFF, 0xD7, 0x00, 255) : IM_COL32(150, 158, 170, 255), sub.c_str());
    dl->PopClipRect();

    // --- the round action button (the reference's green "+"). DRAWN, not a glyph:
    //     the default ImGui font has no "+"/refresh mark that stays crisp at 1.35x.
    const ImVec2 bc = ImVec2(p1.x - pad - btnD * 0.5f, (p0.y + p1.y) * 0.5f);
    ImGui::SetCursorScreenPos(ImVec2(bc.x - btnD * 0.5f, bc.y - btnD * 0.5f));
    res.plus = ImGui::InvisibleButton("##add", ImVec2(btnD, btnD)) && !inFlight;
    const bool bhov = ImGui::IsItemHovered();
    // The button is INSIDE the card, so a click on it also registered as a card
    // click above (ImGui resolves overlap after the fact). Clicking "+" must not
    // also open the transcript.
    if (bhov) { res.clicked = false; res.dbl = false; }
    const float r = btnD * 0.5f;
    if (inFlight) {
        float a0 = (float)(ImGui::GetTime() * 3.0);
        dl->PathArcTo(bc, r - 2.0f * S, a0, a0 + 4.2f, 24);
        dl->PathStroke(IM_COL32(0xFF, 0xD7, 0x00, 255), 0, 3.0f * S);
    } else if (v.hasTranscript) {
        ImU32 tc = bhov ? IM_COL32(0x14, 0xFF, 0x39, 255) : IM_COL32(170, 178, 190, 255);
        dl->AddCircle(bc, r - 1.0f, bhov ? IM_COL32(0x14, 0xFF, 0x39, 255) : IM_COL32(255, 255, 255, 60), 0, 2.0f);
        dl->AddLine(ImVec2(bc.x - r * 0.34f, bc.y + r * 0.02f), ImVec2(bc.x - r * 0.06f, bc.y + r * 0.30f), tc, 2.5f * S);
        dl->AddLine(ImVec2(bc.x - r * 0.06f, bc.y + r * 0.30f), ImVec2(bc.x + r * 0.38f, bc.y - r * 0.30f), tc, 2.5f * S);
    } else {
        dl->AddCircleFilled(bc, bhov ? r : r - 1.0f, IM_COL32(0x14, 0xFF, 0x39, 255));
        float k = r * 0.44f;
        dl->AddLine(ImVec2(bc.x - k, bc.y), ImVec2(bc.x + k, bc.y), IM_COL32(0, 0, 0, 255), 3.0f * S);
        dl->AddLine(ImVec2(bc.x, bc.y - k), ImVec2(bc.x, bc.y + k), IM_COL32(0, 0, 0, 255), 3.0f * S);
    }
    if (bhov) {
        ImGui::SetMouseCursor(ImGuiMouseCursor_Hand);
        ImGui::SetTooltip("%s", inFlight ? "transcribing..." : v.hasTranscript
            ? "re-transcribe locally (writes a SEPARATE _parakeet_transcription.srt; your original is never touched)"
            : "transcribe this video (local Parakeet ASR)");
    } else if (hov) {
        ImGui::SetTooltip("%s", v.name.c_str());   // the FULL name, never ellipsised
    }

    // Advance EXACTLY one stride so ImGuiListClipper's fixed item height matches.
    ImGui::SetCursorScreenPos(ImVec2(p0.x, p1.y + 8.0f * S));
    return res;
}
```

## 4. Search row → magnifier + box + "smart" pill + clear

**Anchor — main.cpp:4573-4579, replace these exact lines:**
```cpp
            bool submitted = ImGui::InputText("##search", g_searchBuf, sizeof g_searchBuf, ImGuiInputTextFlags_EnterReturnsTrue);
            if (submitted) runSearch(false);
            if (ImGui::SmallButton("Search")) runSearch(false);
            ImGui::SameLine();
            if (ImGui::SmallButton("Smart (qmd)")) runSearch(true);
            ImGui::SameLine();
            if (ImGui::SmallButton("Clear")) { g_searchBuf[0] = 0; g_hits.clear(); g_searchMode.clear(); g_searchErr.clear(); }
```
**with:**
```cpp
            // ONE search row, like the reference: [magnifier][box][smart pill][x].
            // Three same-size SmallButtons ("Search" / "Smart (qmd)" / "Clear") read
            // as three equal choices and the middle one got clipped at 320px.
            {
                const float S  = ImGui::GetIO().FontGlobalScale;
                const float fh = ImGui::GetFrameHeight();
                ImDrawList* dl = ImGui::GetWindowDrawList();

                ImVec2 mp = ImGui::GetCursorScreenPos();
                if (ImGui::InvisibleButton("##mag", ImVec2(fh, fh))) runSearch(g_smartSearch);
                bool mh = ImGui::IsItemHovered();
                ImU32 mc = mh ? IM_COL32(0x14, 0xFF, 0x39, 255) : IM_COL32(150, 158, 170, 255);
                ImVec2 mc0 = ImVec2(mp.x + fh * 0.45f, mp.y + fh * 0.42f);
                float mr = fh * 0.20f;
                dl->AddCircle(mc0, mr, mc, 0, 2.0f * S);
                dl->AddLine(ImVec2(mc0.x + mr * 0.7f, mc0.y + mr * 0.7f),
                            ImVec2(mc0.x + mr * 1.9f, mc0.y + mr * 1.9f), mc, 2.5f * S);
                if (mh) ImGui::SetTooltip("search every transcript in this folder (Enter)");
                ImGui::SameLine(0, 6 * S);

                float pillW  = ImGui::CalcTextSize("smart").x + 22.0f * S;
                float clearW = fh;
                ImGui::SetNextItemWidth((std::max)(60.0f, ImGui::GetContentRegionAvail().x - pillW - clearW - 14.0f * S));
                if (ImGui::InputTextWithHint("##search", "search all transcripts", g_searchBuf, sizeof g_searchBuf,
                                             ImGuiInputTextFlags_EnterReturnsTrue))
                    runSearch(g_smartSearch);
                ImGui::SameLine(0, 6 * S);

                // Checklist 20: a TOGGLE, blue when on, so single-word keyword search
                // stays one click away and Enter always runs the armed mode.
                if (pillButton("smart", g_smartSearch, IM_COL32(0x00, 0xAE, 0xEF, 255))) {
                    g_smartSearch = !g_smartSearch;
                    if (g_searchBuf[0]) runSearch(g_smartSearch);
                }
                if (ImGui::IsItemHovered())
                    ImGui::SetTooltip("%s", g_smartSearch ? "smart search ON - qmd finds meaning, not just the exact word"
                                                          : "smart search OFF - exact keyword match");
                ImGui::SameLine(0, 4 * S);

                ImVec2 xp = ImGui::GetCursorScreenPos();
                if (ImGui::InvisibleButton("##clr", ImVec2(clearW, fh)))
                    { g_searchBuf[0] = 0; g_hits.clear(); g_searchMode.clear(); g_searchErr.clear(); }
                bool xh = ImGui::IsItemHovered();
                ImU32 xc = xh ? IM_COL32(0xDC, 0x14, 0x3C, 255) : IM_COL32(150, 158, 170, 255);
                ImVec2 xm = ImVec2(xp.x + clearW * 0.5f, xp.y + fh * 0.5f);
                float xk = clearW * 0.22f;
                dl->AddLine(ImVec2(xm.x - xk, xm.y - xk), ImVec2(xm.x + xk, xm.y + xk), xc, 2.5f * S);
                dl->AddLine(ImVec2(xm.x - xk, xm.y + xk), ImVec2(xm.x + xk, xm.y - xk), xc, 2.5f * S);
                if (xh) ImGui::SetTooltip("clear the search");
            }
```

## 5. List header → "14 videos" + sort pills + Transcribe all

**Anchor — main.cpp:4630-4632, replace these exact lines:**
```cpp
                const char* sortLabel = g_sortMode == 0 ? "Date (newest)" : g_sortMode == 1 ? "Date (oldest)" : g_sortMode == 2 ? "Name A-Z" : "Name Z-A";
                if (ImGui::SmallButton(sortLabel)) { g_sortMode = (g_sortMode + 1) % 4; sortLibrary(); }
                if (g_orphanCount > 0) { ImGui::SameLine(); ImGui::TextDisabled("(+%d orphan transcripts)", g_orphanCount); }
```
**with:**
```cpp
                // Checklist 18, reference .findhead: the count, then two sort pills.
                // The old control was ONE button cycling four hidden states - you had
                // to click it and read it to find out what it did.
                {
                    const float S = ImGui::GetIO().FontGlobalScale;
                    ImGui::Text("%d video%s", (int)g_videos.size(), g_videos.size() == 1 ? "" : "s");
                    ImGui::SameLine(0, 12 * S);
                    // Clicking the ACTIVE pill flips its direction; clicking the other
                    // switches to it (date->newest, name->Z-A, both his stated defaults).
                    if (pillButton(g_sortMode == 1 ? "oldest" : "newest", g_sortMode <= 1, IM_COL32(0x14, 0xFF, 0x39, 255)))
                        { g_sortMode = (g_sortMode == 0) ? 1 : 0; sortLibrary(); }
                    ImGui::SameLine(0, 4 * S);
                    if (pillButton(g_sortMode == 2 ? "A-Z" : "Z-A", g_sortMode >= 2, IM_COL32(0x14, 0xFF, 0x39, 255)))
                        { g_sortMode = (g_sortMode == 3) ? 2 : 3; sortLibrary(); }

                    if (g_transcribeAllBusy) ImGui::BeginDisabled();
                    if (fixedButton(g_transcribeAllBusy ? "Transcribing all..." : "Transcribe all",
                                    { "Transcribe all", "Transcribing all..." })) {
                        g_transcribeAllBusy = true;
                        // Whole-folder ASR: minutes to hours, so it goes through
                        // engineCallAsync (never the UI thread) and its reply lands on
                        // the UI thread via drainAsync.
                        engineCallAsync("transcribe_all", json::object(), 7200.0, "Transcribing every video...",
                            [](const json& r) {
                                g_transcribeAllBusy = false;
                                if (!r.value("ok", false)) {
                                    g_renderMsg = "Transcribe all failed: " + r.value("error", std::string("unknown"));
                                    return;
                                }
                                const json& d = r.contains("data") ? r["data"] : r;
                                if (d.contains("folder")) applyFolderView(d["folder"], g_folderRoot);
                                int okN = d.value("transcribed", 0), badN = d.value("failed", 0);
                                g_renderMsg = "Transcribed " + std::to_string(okN) +
                                              (badN ? (", " + std::to_string(badN) + " failed") : "");
                            });
                    }
                    if (g_transcribeAllBusy) ImGui::EndDisabled();
                    if (g_orphanCount > 0) { ImGui::SameLine(); ImGui::TextDisabled("(+%d orphan transcripts)", g_orphanCount); }
                }
```
`transcribe_all` is a real engine verb (`becky-go/cmd/clip/bridge.go:107` → `TranscribeAll()`), and its reply carries `{folder, transcribed, failed}` (`becky-go/cmd/clip/transcribe.go:355`). Note: `applyFolderView` resets `g_libSel` to 0 — same as the reference's `applyFolder`, so this matches behaviour he has already accepted.

## 6. The list itself → clipper + cards

**Anchor — main.cpp:4641-4672, replace the whole block from this exact line:**
```cpp
                ImGui::BeginChild("videos", { 0, 0 }, false);
```
**down to and including this exact line (the `EndChild` at 4672):**
```cpp
                ImGui::EndChild();
```
*(that is, the entire `for (int i = 0; i < (int)g_videos.size(); i++)` loop and its `EndChild`. Leave everything after it — the `// B-5: Space plays...` comment and its key handling at 4673-4683 — untouched.)*

```cpp
                ImGui::BeginChild("videos", { 0, 0 }, false);
                // The colours the timeline already assigned per source, so a card
                // wears the same colour as its clips (checklist 37).
                // ponytail: rebuilt each frame - O(clips on the timeline), a few
                // hundred at most. Key it off a track revision counter only if a
                // reel ever gets big enough to measure.
                std::map<std::string, ImU32> srcCol;
                for (auto& c : g_track[0]) srcCol.emplace(baseName(c.source), IM_COL32(c.r, c.g, c.b, 255));

                // His real corpus is 2258 videos (see the I-1 note at the top of this
                // file). Build only the cards actually on screen - a card is more draw
                // work than a Selectable was, and this is a responsiveness requirement,
                // not an optimisation.
                ImGuiListClipper clip;
                clip.Begin((int)g_videos.size(), libCardStride());
                if (g_libScrollPending && g_libSel >= 0) clip.IncludeItemByIndex(g_libSel);
                while (clip.Step()) {
                    for (int i = clip.DisplayStart; i < clip.DisplayEnd; i++) {
                        VideoRow& v = g_videos[i];
                        ImGui::PushID(i);
                        bool inFlight;
                        { std::lock_guard<std::mutex> lk(g_transcribeMx); inFlight = g_transcribeInFlight.count(v.path) != 0; }
                        auto it = srcCol.find(baseName(v.path));
                        LibCardResult res = drawLibraryCard(v, g_libSel == i, g_libJustViewedIdx == i, inFlight,
                                                            it == srcCol.end() ? 0u : it->second);
                        // ONE selection model (B-4): mouse click sets the SAME index arrows move.
                        if (res.clicked) g_libSel = i;
                        if (res.dbl) { openTranscript(v.path); g_libJustViewedIdx = i; }
                        if (res.plus) { g_libSel = i; requestTranscribe(v.path, v.name); }
                        // Opened by the card's right-click (drawLibraryCard), same ID scope.
                        if (ImGui::BeginPopup("rowctx")) {
                            g_libSel = i;
                            if (ImGui::MenuItem("Open in File Browser")) openInFileBrowser(v.path);
                            if (ImGui::MenuItem("Copy File Name")) ImGui::SetClipboardText(baseName(v.path).c_str());
                            if (inFlight) ImGui::BeginDisabled();
                            if (ImGui::MenuItem(v.hasTranscript ? "Re-transcribe" : "Transcribe")) requestTranscribe(v.path, v.name);
                            if (inFlight) ImGui::EndDisabled();
                            ImGui::EndPopup();
                        }
                        // The last submitted item is the round button, which is vertically
                        // centred in the card - so this centres the CARD.
                        if (g_libSel == i && g_libScrollPending) { ImGui::SetScrollHereY(0.5f); g_libScrollPending = false; }
                        ImGui::PopID();
                    }
                }
                clip.End();
                ImGui::EndChild();
```

---

## Verify (the applying agent must actually do this)

1. `native\becky-review\_build.bat` — must link clean.
2. Launch, `Ctrl+O` a folder with long filenames. Screenshot. Check: cards ~60px tall with a gap; **no name sliced at the right edge** (middle-ellipsis with `...`); hovering a card tooltips the full name; the green `+` is a filled circle, not a glyph box.
3. Click a `+` → the sub-line turns yellow "transcribing..." and the button becomes a spinning arc; the transcript does **not** open (the `bhov` guard).
4. Arrow Up/Down through a 1000+ row folder — selection scrolls into view and the frame rate does not move (clipper).
5. Toggle `smart` → pill goes blue, Enter re-runs in qmd mode.

**Skipped deliberately:** a crown/"most relevant" pill (belongs to the quote list, item 19), an "index" icon (item 21, same), and a nicer empty state. **Not proposed but worth flagging to whoever owns global style:** this app has no TTF loaded — it is ProggyClean bitmap-scaled 1.35×, which is why every label looks soft next to the WPF reference. One `io.Fonts->AddFontFromFileTTF("C:\\Windows\\Fonts\\segoeui.ttf", 17.0f)` + `FontGlobalScale = 1.0f` would fix it app-wide. That is a global change, not mine to make here — do not apply it twice.

---

# SPEC — 

Read the real file (5233 lines). Findings:

## 1. ANCHORS

| Spec anchor | Reality |
|---|---|
| **A: insert before `drawTimeline(curSec, playing);` at `main.cpp:5173`** | **WRONG LINE — actual 5202.** Line 5173 is inside the work-indicator block: `d->AddRectFilled(ImVec2(p0.x + std::max(0.0f, sx), p0.y),`. The *text* anchor is unique (only call site is 5202) and the "last statement inside `if (ImGui::Begin("timeline"...))`" description is correct — but an agent applying by line number lands in the middle of the progress-bar draw. |
| **B: `main.cpp:4562` `const float timelineH = (std::max)(180.0f, availH * 0.26f);`** | **EXACT MATCH.** |
| **C: `main.cpp:3103-3105`** | **EXACT MATCH**, all three lines verbatim. |
| Supporting refs: 1338 `g_track[2]`, 1339 `g_compDur`, 1752 `g_pps=60`, 1757 `g_zoomReq`, 2247 `fmtTime`, 2631-2632 avail, 2642 `lanesH>90`, 2732 `laneH>70`, 2741 clamp `min(2000,max(0.5,...))`, 2759 drain, 4227-4228 Up/Down, 4604 `addHitToTimeline`, 4959 `Button("Load Reel")` | **All correct.** |
| "loading bar already done, `main.cpp:5106-5151`" | **Wrong lines** (5106 is `Button("Apply##proposal")`). The work indicator is really **5130-5180**. Conclusion is right, citation isn't. |

## 2. WOULD NOT COMPILE
Nothing. Vendored ImGui is **1.90.9**; `Button(label, ImVec2)`, `SameLine(float)`, `GetFrameHeight()`, `CalcTextSize`, `SetTooltip`, `PushStyleVar(ImGuiStyleVar_FramePadding, ImVec2)` all match. `snprintf` already used ~20×. No name shadowing (`bw` at 5168 is in a closed scope). All globals exist with the stated types.

## 3. COLLIDES WITH EXISTING WORK
- **No clip-count or px/s readout exists** — confirmed, `grep px/s` = 0 hits. Change A is genuinely new.
- **Duration is already on screen**: line 4781 `ImGui::Text("%.1f / %.1f s", curSec, g_compDur);` in the transport bar. Change A duplicates the duration half in a second format. Not a regression, but it's not "missing".
- Change A adds no zoom math of its own (just sets `g_zoomReq`) — correctly does **not** re-implement Up/Down zoom or wheel zoom. Nothing else is re-proposed.

## 4. HARD-RULE VIOLATIONS
None. No icon font, no colour stripped (adds neon green + brightens grey), no blocking call, no text shrink. The `+`/`-` sit at fixed x so they don't move.

**But there is a real overlap bug** (see fix): the `+` button is positioned for a slot sized `"2000 px/s"` (9 chars), while `%.4g` produces **5-6 char numbers for nearly every real value** — `60 → 79.35 → 104.9 → 138.8 → 183.5` and, at the low clamp, `0.6613`. Default font is ProggyClean (no `AddFontFromFile` anywhere) = fixed 7px advance × `FontGlobalScale` 1.35 = 9.45px/char. So `"79.35 px/s"` is 10 chars = ~9px wider than the slot, `"0.6613 px/s"` ~19px wider. The opaque button frame draws **over the last character of the readout on the very first zoom-in click**. Spec verify step 3 would pass while this is broken.

## 5. TOO BIG
No. ~35 lines added, 2 lines changed, one constant. Correctly refuses the shared-helper refactor.

## Other factual errors worth correcting in the comments
- **"Only bites below a ~722px window height"** — false. 722 is the *old* 180-floor crossover. With 212: `menuH` = 17.55+14 = 31.55, so the floor wins until `g_H ≈ 847`. **The default window is `g_W=1280, g_H=800` (line 1920)** — so Change B shrinks the video pane by ~12px at the default size, on every launch. Harmless, but say so honestly.
- **"the old grey was under a 3:1 contrast ratio"** — false. `(120,128,140)` on `COL_LANE (24,27,33)` (line 2222, value confirmed) is **4.32:1**. The brightening is still an improvement (~8:1); the justification is wrong.
- Change B's own math checks out at the default scale: at 180 the row drops `lanesH` to 87.4 (<90 → caption lane off); at 212 `lanesH`=119.4, `laneH`=79.4 (>70 → thumbnails survive). Correct. **Caveat:** at `BECKY_UI_SCALE ≥ 2.2` plus a sub-847px window, `laneH` falls to 68 and thumbnails switch off anyway.

---

## VERDICT: APPLY WITH THE FIXES BELOW

**Fix 1 — anchor.** Change A inserts immediately before **line 5202** (not 5173), i.e. between the `ImGui::SetWindowFocus();` block that ends at 5201 and:
```cpp
            drawTimeline(curSec, playing);
```

**Fix 2 — the `+` slot must fit the widest string `%.4g` can produce.** Replace the `SameLine` before `+##zoomin` with:
```cpp
                // Slot sized for the WIDEST string %.4g can produce: 4 significant
                // digits plus a decimal point, e.g. "0.6613 px/s" at the 0.5 clamp
                // (main.cpp:2741). "2000 px/s" is NOT the widest - every ordinary
                // zoom step ("79.35", "104.9", "183.5") is longer than it, and the
                // button's opaque frame would print over the last character.
                ImGui::SameLine(zoomX + bw + ImGui::CalcTextSize("888888 px/s").x
                                + ImGui::GetStyle().ItemSpacing.x * 2.0f);
```

**Fix 3 — corrected verify step 3:** "Click `+` five times, then `-` five times: the readout returns to `60`, and at **every** intermediate value (`79.35`, `104.9`, `138.8`, `183.5`, `242.7`) the `+` button neither moves nor touches the last character of the number."

**Fix 4 — corrected comment in Change B:** replace "Only bites below a ~722px window height" with "Bites below a ~847px window height — including the default 800px window (`g_W/g_H`, main.cpp:1920), where the video pane loses ~12px."

Optional: drop the duration from Change A (already at main.cpp:4781) and keep it to `"N clips"`, which is the only genuinely missing readout.

---

# SPEC — 

**VERDICT: APPLY WITH THE FIXES BELOW** — the design is sound and mostly compiles, but the delete range is wrong in a way that guarantees a build failure, and two shipped controls are silently re-implemented.

---

## 1. ANCHORS

Real file is **5233 lines**. Anchors split into two groups: everything below ~4782 is **correct**, everything from the button rows onward is **off by +29**.

**CORRECT (verified, text is unique — apply as written):**

| Spec anchor | Real | Status |
|---|---|---|
| after `888` / before `890` (`// ---- run an engine verb...`) | 888 / 890 | ✓ |
| `2361` `static std::string g_capErr;` | 2361 | ✓ |
| `2642` `bool showCaps = !g_capPath.empty() && lanesH > 90;` | 2642 | ✓ unique |
| after `3655` (close of `playWholeVideo`) / before `3657` | 3655 / 3657 | ✓ |
| `4562` `const float timelineH = (std::max)(180.0f, availH * 0.26f);` | 4562 | ✓ unique |
| `4691` `Begin("video")`, `4564` `vidW` | 4691 / 4564 | ✓ |
| `4706` `float ctrlH = ...* 2 + ...* 2;` | 4706 | ✓ unique |
| `4781` `ImGui::Text("%.1f / %.1f s"...)` KEEP | 4781 | ✓ |
| `4782` `fixedButton(playing ? "Pause##play"...)` DELETE-start | 4782 | ✓ unique |

**WRONG:**

- **`5173` `drawTimeline(curSec, playing);`** → real **5202**. (Text is unique, so §5c survives a text-search apply.)
- **DELETE `4782`–`4949`** → real end is **4978**. **This is the build-breaker.** Real 4949 is `g_renderMsgAt = nowSec();` *inside the Screenshot handler*; 4950 is its closing `}`. Cutting at 4949 leaves an orphan brace plus intact `Save Reel` (4952) / `Load Reel` (4959) / `Export EDL` (4972) blocks — mismatched braces **and** duplicate buttons/IDs against the new toolbar's Save/Load/EDL.
- **KEEP `4950` (g_renderMsg line)** → real **4979**. 4950 is `}`.
- §0 table: `main.cpp:4907` screenshot → real **4936**. `main.cpp:4885` Render Selection → real **4914**. `main.cpp:4886` `BeginDisabled` → real **4915**. `main.cpp:4904` "has no SameLine, which is the wrap" → the row break is actually at **4935/4936** (before `Screenshot`); 4904 is inside the Render lambda. And ImGui never wraps — the second row is deliberate, not overflow.

**Corrected §5b delete:**
```
DELETE main.cpp:4782 through main.cpp:4978 inclusive.
  4782 = if (fixedButton(playing ? "Pause##play" : "Play##play", ...
  4978 = the closing }  of the Export EDL block (4977 is  });  )
KEEP 4781 (the "%.1f / %.1f s" Text) and 4979 (the g_renderMsg TextDisabled).
```

**Corrected §5c anchor:** insert before **line 5202**, not 5173.

---

## 2. WOULD NOT COMPILE

Nothing, apart from the delete-range brace mismatch above. I checked every identifier:

- All globals exist and are declared **before** the §4 insert point: `g_stockSec` 1762, `g_thrOn` 1767, `g_quietDirty` 1769, `g_lastUndoQueued` 1786, `g_lastRedoQueued` 1787, `g_playRate` 537, `g_ovMode` 1370, `g_group` 1340, `g_sel` 1765, `g_editsInFlight` 298, `g_playingExt` 1760, `g_renderMsg` 2170, `g_compDur` 1339, `baseName` 74, `nowSec` 66.
- All helpers precede 3655: `clipAtComp` 1387, `sourceFps` 1405, `setOverlayMode` 1522, `emitThreshold` 1821, `loadTimelineView` 1865, `loadCaptions` 2413, `openInFileBrowser` fwd-decl 2571, `pickOpenReelFile` 3563, `convertEditIfNeeded` 3600, `queueEdit` 390, `engineCallAsync` 917, `fixedButton` 883.
- ImGui is **1.90.9** as claimed (`native/timeline-bench/third_party/imgui/imgui.h:30` — the build.bat include path, so the spec cited the right tree). `AddQuadFilled` 2845, `AddTriangleFilled` 2847, `AddCircle` 2848, `AddRect(...,rounding,flags,thickness)` 2841, `PathArcTo` 2883, `PathStroke(ImU32, ImDrawFlags=0, float=1.0f)` 2882, both `GetColorU32` overloads 457-459, `ImGuiHoveredFlags_AllowWhenDisabled` 1268 — all present, all signatures match.
- `NOMINMAX` is at line **27** as claimed; `<algorithm>` at 47. Bare `std::max` fine.
- Engine contracts check out: `autocut_silence` takes `{"name"}` and returns `{segments:[{in,out}], note}` (`cmd/clip/bridge.go:89`, `autocut.go:35`); `set_clips` takes `{"clips":[{source,in,out,label}]}` (`bridge.go:130`, `argClipSpecs` 405-425). Field names in the spec's JSON match exactly.
- `hwnd` and `lastComposed` are in scope at 5202.

**One real runtime bug:** the spec calls `engineCallAsync("autocut_silence", ..., 180.0, ...)` but the engine's own bound is `autoCutTimeout = 15 * time.Minute` (`cmd/clip/autocut.go:26`). On any long source the UI times out at 3 min while `becky-cut` is still running — the broom silently fails on exactly the footage it's for. **Fix: `900.0`.**

---

## 3. COLLIDES WITH WORK ALREADY DONE

**Two shipped controls are re-implemented without the spec noticing:**

**a) The running-person icon already exists** — `main.cpp:4866-4886`, already drawn from `ImDrawList` primitives (head circle, torso, two arms, two legs), already with the "no icon font / documented square-play failure" comment, already colour-coded: `IM_COL32(242,217,115,255)` amber when ON vs `IM_COL32(150,160,175,210)` dim when off. §0's table does not list item 15 as met, and §4 re-adds it as `ICO_RUN` with **different state semantics** (neon fill + black icon). That is a regression of a shipped, deliberately-chosen colour affordance. Either keep the existing block verbatim inside the new bar, or accept the restyle knowingly — but do not present it as new work.

**b) One-frame extend already exists** — `main.cpp:4811-4837`, as `<+1f` / `+1f>` via `fixedButton`, with `canTrim` gating, per-clip `sourceFps`, `g_editsInFlight`, tooltips, and a comment explaining the single-`set_trim`-per-press undo choice. The spec replaces these **labelled** controls with `[<` / `>]` icons. This contradicts §1's own rule ("abstract controls have no canonical silhouette → keep words"): a bracket-and-arrow does not say *one frame*. **Recommendation: keep `<+1f` / `+1f>` as `fixedButton` text, tinted `kTbBlue`.** That satisfies the blue-group grouping without trading away the only thing that says what the button does.

Not collisions (genuinely new, verified absent from `main.cpp`): split button, undo/redo buttons, Captions toggle, autocut/broom, `set_clips` — zero prior occurrences.

Correctly left alone: `fixedButton`, `engineCallAsync`, the work indicator, I-beam cursor, focus-on-click (5199-5201), Escape=delete (4070), Redo (4183). The §0 finding that `Render Selection (%d)` at real **4914** uses raw `ImGui::Button` with a variable-width label is **genuine** — that is a real item-6a violation and the fix is right.

---

## 4. HARD-RULE VIOLATIONS

**HIGH — a control can disappear.** The right group is right-aligned with `SameLine(targetX)`, and when it does not fit the spec deliberately falls back to `SameLine(0, 12.0f)` and lets the row run long. ImGui does **not** wrap, and the `timeline` window (5184) is opened without `ImGuiWindowFlags_HorizontalScrollbar` — so overflow is **clipped and unclickable**, not scrollable. `Export` and `Render Selection` are the two controls at the far right.

The spec's "≈1350 px at 1.35" is optimistic. With the app's real style (`FramePadding(10,7)`, `ItemSpacing(9,7)`, font scale 1.35 → `FrameHeight` ≈ 32, `iw` ≈ 41) I estimate **≈1470-1500 px** — fits 1920, but the margin is ~400 px. `FramePadding` is never scaled (`ScaleAllSizes` is not called), so only the text grows: overflow lands somewhere around `BECKY_UI_SCALE` ≈ **1.9-2.0**, and the code accepts up to **3.0** (`main.cpp:3883`) with its own comment inviting `"1.6"`. It also breaks on any non-maximised window. **This is unverified arithmetic, not a measurement — but the clipping mechanism is certain.** Add `ImGuiWindowFlags_HorizontalScrollbar` to the `timeline` window, or wrap the right group to a second row when `targetX <= usedX`. Do not ship the silent-clip fallback.

**MEDIUM — undo and redo are visually identical.** `ICO_UNDO` and `ICO_REDO` draw the *same* top arc and the *same* downward-pointing triangle; only the triangle's x-position differs (`c.x - r` vs `c.x + r`). Two adjacent ~41 px boxes distinguishable only by which end a small wedge sits on is the wrong call for impaired acuity. Mirror the arc (`PathArcTo(c, r, PI, 2*PI)` vs a reversed sweep) and point the arrowheads in opposite directions, or label them.

**MEDIUM — primary action renamed.** The shipped button is `Render` (4900); the spec silently renames it `Export`, and rewrites the sibling message from "use **Render** for a captioned file" to "use **Export**". Nothing in the checklist asks for this, and "Render" is the word in every feedback doc. Keep `Render`.

**LOW — `Captions` is clickable with no reel loaded.** `g_capLaneOn` toggles but `showCaps` also needs `!g_capPath.empty()`, so with no `.srt` the button lights neon and nothing happens. Wrap in `BeginDisabled(g_capPath.empty())`.

**LOW — `Separator()` height unbudgeted.** §5a reserves `GetFrameHeight() + ItemSpacing.y` but §5c also emits `ImGui::Separator()` (~15 px). The clip lanes lose that, contra the "clips keep every pixel" claim. Add `+ ImGui::GetStyle().ItemSpacing.y * 2 + 1.0f` to `toolbarH`.

**Not violations:** no icon font is loaded (correct, and it matches the existing 4866 precedent); nothing is muted or de-coloured; `engineCallAsync` is used for the long verbs. The two remaining synchronous `engineCall`s on the UI thread (Screenshot 20 s, Load Reel 30 s) are carried over unchanged from 4942/4964 — pre-existing, not a new violation, but also not fixed while the file is being edited anyway.

---

## 5. TOO BIG

No. It is one new function plus two helpers, three surgical main-loop edits, one global. The container move (video pane → timeline header) is the actual fix and is correctly scoped. The `set_clips` broom is the only piece that is a new *feature* rather than a relocation — it is well-formed (single undoable edit, value-captured clip fields, UI-thread callback via `drainAsync`) and worth keeping, with the 900 s timeout fix.

---

## APPLY ORDER

1. §2, §3, §4 as written — anchors are correct.
2. §4: `180.0` → `900.0` on the `autocut_silence` call.
3. §4: revert the two extend buttons to `fixedButton("<+1f##extl", {"<+1f"})` / `fixedButton("+1f>##extr", {"+1f>"})` with `PushStyleColor(ImGuiCol_Text, kTbBlue)`; drop `ICO_EXTL`/`ICO_EXTR`.
4. §4: `"Export##export"` → `"Render##export"`, and the selection message back to "use Render for a captioned file".
5. §4: `if (fixedButton("Captions##caps", ...))` wrapped in `BeginDisabled(g_capPath.empty())` / `EndDisabled()`.
6. §5a: `toolbarH = ImGui::GetFrameHeight() + ImGui::GetStyle().ItemSpacing.y * 3.0f + 1.0f;`
7. §5b: **delete 4782–4978**, keep 4781 and 4979.
8. §5c: insert before **line 5202**.
9. Line 5184: add `| ImGuiWindowFlags_HorizontalScrollbar` to the `timeline` window flags.
10. Build `X:\AI-2\becky-tools\native\becky-review\_build.bat`, then launch and verify at `BECKY_UI_SCALE=1.35` **and** `2.0` that Render/Render Selection are still reachable.

Files: `X:\AI-2\becky-tools\native\becky-review\main.cpp`, `X:\AI-2\becky-tools\native\becky-review\_build.bat`, `X:\AI-2\becky-tools\becky-go\cmd\clip\autocut.go`, `X:\AI-2\becky-tools\becky-go\cmd\clip\bridge.go`.

---

# SPEC — 

Every anchor verified, every identifier resolved, ImGui version and style metrics checked. Three real defects, none structural.

---

## 1. ANCHORS — all 14 match byte-for-byte

The spec says the file is 5204 lines; it is **5233**. The drift is entirely *below* line 4558, so nothing the spec touches moved. Confirmed exact:

| Line | Actual content | Spec claim |
|---|---|---|
| 3467 | `static const uint32_t kPalette[8] = {` | ✓ |
| 3477 | `}` (close of `cardColorFor`) | ✓ |
| 3479 | `// ---- library helpers ----` | ✓ |
| 4523 | `ImGui::Text("Becky Review (native)");` | ✓ |
| 4535–4538 | time readout / ENGINE DOWN / MPV DOWN / `EndMainMenuBar` | ✓ |
| 4558 | `const float menuH = ImGui::GetFrameHeight();` | ✓ |

Also confirmed: `baseName` @74, `utf8ToWide` @960, `g_mpvAvailable` @509, `g_track` @1338, `g_folderRoot` @3319, `ShellExecuteW` @3549. **No anchor failures.**

## 2. WOULD NOT COMPILE — nothing

Vendored ImGui is **1.90.9**. All four APIs match the spec's call shapes: `InvisibleButton(const char*, const ImVec2&, flags=0)` @537, `AddRect(min,max,col,rounding,ImDrawFlags,thickness)` @2841 (6-arg form — correct), `AddRectFilled(...,rounding)` @2842, `AddQuadFilled(p1..p4,col)` @2845.

`IMGUI_USE_BGRA_PACKED_COLOR` is **not** defined, so `IM_COL32` packs R,G,B,A low→high — `inkFor`'s channel extraction is right. `<cctype>`, `<algorithm>`, `<cmath>`, `<shellapi.h>` all included; `shell32.lib` linked. `Clip::source` exists (line 1332). The two `const std::string tip` declarations are in disjoint scopes — no shadowing.

Palette math independently recomputed and **correct**: E→idx4 `#FF57D1` lum 151→black; X→idx7 `#FF8C00` lum 158→black; C→idx2 `#DC143C` lum 84→white.

## 3. LAYOUT — sound, and safer than the spec argues

A menu bar runs `ImGuiLayoutType_Horizontal`, so `ItemSize` auto-calls `SameLine()`. The explicit `SameLine()` / `SameLine(0,0)` calls are **idempotent overrides**, not double-spacing — Change 2's zero-gap "becky review" works as written. With the app's real style (`FramePadding=(10,7)` @3888, `ItemSpacing=(9,7)` @3889) the reservation **over**-reserves ~11px per DOWN flag, so it errs toward a gap, never an overflow.

## 4. NO RACE (the spec's weakest-looking claim is safe, for a reason it doesn't give)

Reading `g_folderRoot` and `g_track[0]` every frame looks like a boot-thread race. It isn't: the render loop `continue`s at **line 3992** on `!g_bootDone.load()` — line 4522 never executes until `bootWork` has finished every write (`g_bootDone.store(true)` @3951 is its last act). `g_folderRoot` is written only via `loadFolder`→`applyFolderView`, called at 3922/3925 (bootWork, pre-gate) and 4530 (UI thread). No cross-thread reader exists.

## 5. NO COLLISIONS, NO HARD-RULE VIOLATIONS

Nothing in the already-done list lives in the menu bar. The work indicator is a separate floating window at the bottom-right of the *upper* half (@5155) — no overlap. `fixedButton` is in the transport row (@4782+).

The diamond is `AddQuadFilled` **geometry, not a font glyph** — correctly sidesteps the documented square-glyph failure. No colour stripped (DOWN flags go 0.4→0.25 red, i.e. *more* saturated). No text shrink, no blocking call, no control moves.

---

## DEFECTS — 3, all local

**D1 — unquoted path to `explorer.exe`. Real bug on any folder with a space.**
`ShellExecuteW(nullptr, L"open", L"explorer.exe", <path>, ...)` splits `X:\Case Files\Jan` into two args and opens Documents. The file's own helper at 3548 quotes it for exactly this reason. Don't quote — drop the exe:

```cpp
if (openPath && *openPath && ImGui::IsItemClicked())
    ShellExecuteW(nullptr, L"open", utf8ToWide(openPath).c_str(), nullptr, nullptr, SW_SHOWNORMAL);
```

**D2 — `elideMiddle` thrashes during window resize.** `maxW = max(140, barW*0.45)` changes every frame of a resize drag → cache misses every frame → the loop starts at `tail = s.size()` (building candidates *longer* than the input) and walks down one char at a time, one `CalcTextSize` per step. ~200 calls/frame on a deep path, for the whole drag. One-line fix — bucket the width so it re-fits once per 16px, not once per pixel:

```cpp
static std::string elideMiddle(const std::string& s, float maxW) {
    maxW = floorf(maxW / 16.0f) * 16.0f;   // a resize drag must not re-fit every frame
    static std::string cacheIn, cacheOut;
```

**D3 — the disambiguator is the lowest-contrast glyph on the bar.** The spec's stated reason for `" 3"` is that both apps sit side by side for comparison — so "3" is the one token that must read at a glance, and `ImGui::TextDisabled` renders it in dimmed grey. Change to `ImGui::Text(" 3");`.

Minor, not worth changing: the crimson `NO FOLDER OPEN` chip duplicates the existing `TextDisabled("Ctrl+O to open a folder")` @4636. Redundant but harmless — the bar chip is the glance-visible one.

---

**VERDICT: APPLY WITH THE THREE FIXES ABOVE.**

Not too big — it is ~60 added lines in two contiguous sites plus three file-scope helpers, with zero refactor. The spec's core engineering claims (menu height untouched, no new globals, right cluster measured-then-placed, engine-side render path already fixed) all hold against the real source.

---

# SPEC — 

Verified everything against the real file. Report:

---

## 1. ANCHORS

| Edit | Spec says | Reality | Status |
|---|---|---|---|
| EDIT 1 | after 3477 (`}` of `cardColorFor`) | 3476 = `g_cardColor[id] = c; return c;`, **3477 = `}`** | ✅ correct |
| EDIT 2 | replace 3681–3700 | 3681 = `// render Q&A cards from the engine \`questions\` verb (G-1).`, 3700 = `}` | ✅ correct |
| EDIT 3 | after 3933 | 3933 = `if (const char* qp = getenv("BECKY_REVIEW_QUESTIONS")) { (void)qp; refreshCards(); }` | ✅ correct |
| **EDIT 4** | **replace 4957–5096** | **4957 is inside the "Save Reel" button block. `ImGui::Text("Q&A / Ask-Becky")` is at 4986; the closing `}` of `if (g_proposalPending)` is at 5125** | ❌ **off by 29** |

File is **5233 lines, not 5204** — it grew after the spec was written, and all the drift is below ~4960. **Corrected EDIT 4 anchor: replace lines 4986–5125.** Line 5126 is `}` (closes `if (ImGui::Begin("qa"...))`), 5127 is `ImGui::End();` — leave both. The spec's *text* anchors are right; only the numbers are wrong. Apply by text, not by number.

Also wrong: spec cites `assist.go:284-290` — no such file in `cmd/clip`. It is `becky-go/internal/assistant/assist.go:266`.

## 2. WOULD NOT COMPILE

Nothing. I checked all of it:
- ImGui is **1.90.9** confirmed (`imgui.h:30`). `BeginChild(str_id, size, ImGuiChildFlags, ...)`, `ImGuiChildFlags_Border/_None`, `InputTextWithHint`, `SetWindowFontScale` all present.
- `engineCallAsync(verb, args, timeoutSec, label, cb)` at 917 — signature matches all five call sites.
- All globals exist: `g_askAnswer`(3452), `g_H`(1920), `Activity`/`g_activityLog`/`g_activityMx`(107-114), `g_inTiedPreview`/`g_reelBeforePreview`(1351-2), `g_quietDirty`(1769), `g_capEditFocus`(2365), `kPalette`/`cardColorFor`(3467-3477).
- `<algorithm>`, `<cstdio>` included (47, 42).
- All PushStyleColor/Var counts balance (2/5+2/4). BeginChild/EndChild balance.
- All five verbs exist in `bridge.go` (170/172/205/212/214/220), and `BackendStatus` really has `summary`/`claude_cli`/`api`/`local` json tags.
- The boot-gate claim is **true** — `if (!g_bootDone.load())` is at 3992 exactly as stated, so the `g_backendSummary` single-writer-then-flag reasoning is sound.
- `engineCall` is genuinely thread-safe (id-multiplexed replies + `writeMx` on the pipe write, 223-254), so moving `ask` to 120s async is safe and will not garble concurrent verbs.

## 3. COLLIDES WITH WORK ALREADY DONE

No regressions of the listed features. The "Play tied clips" block is copied **verbatim** from the live code (5028-5052) including the `g_inTiedPreview`/`g_reelBeforePreview` guard — good, that's preserved rather than re-derived. Timeline, fixedButton, focus-on-click (5195-5201), work indicator, Skip Quiet are untouched.

Two real interaction problems it *introduces*:

**(a) Empty work-indicator label clobbers a live one.** `beginWork` is "newest wins" (866). The spec passes `""` for reject_proposal. Reject during a 120s `ask` blanks the "becky is thinking..." text while the ask is still running — and if it crosses 1s you get an empty floating box. Fix: pass a real label.

**(b) `apply_proposal`'s callback calls `loadTimelineView` from inside `drawTimeline`.** `drainAsync()` is called at line **2737, inside `drawTimeline` (starts 2628)**, and `loadTimelineView` does `g_track[0].clear()` + rebuild. Today's async callers (export/save_reel/write_edl) never touch `g_track`, so this hazard is new. I traced it: the only pre-2737 `g_track` use is line 2713 which completes first, and everything from 2804 on re-reads `.size()` and is bounds-checked — so it does **not** crash today. But it is a live landmine: it now mutates the timeline mid-render. Flagging as MEDIUM, not a blocker.

## 4. VIOLATES A HARD RULE

**(a) BLOCKER — the chips do not fit and will be visibly clipped.** `FontGlobalScale = 1.35f` (3885 — an explicit accessibility setting for Jordan, overridable to 3.0 via `BECKY_UI_SCALE`), and `FramePadding = (10,7)`, `WindowPadding = (12,10)` (3888-3893). ProggyClean advance ≈7px × 1.35 = **9.45px/char**. Chip width = chars×9.45 + 20:

| Chip | chars | width | avail @1280 (qaW=300 → 276px) | @1920 (398px) |
|---|---|---|---|---|
| "compile every take where I said the intro line" | 46 | **455px** | overflows by 179 | **overflows by 57** |
| "cut the dead air out of this reel" | 33 | **332px** | overflows by 56 | fits |
| "turn the lower-third on" | 23 | 237px | fits | fits |

ImGui buttons don't wrap text. Chip 1 is clipped at **every** realistic window size. The spec's own verification step 2 ("three green pill chips") fails on first launch. At `BECKY_UI_SCALE=2.0` all three blow out. The wrap loop is fine — it's the chip *strings* that are too long.

**(b) HIGH — the input gets smaller.** `InputTextMultiline` at `{-1, g_H*0.15f}` (~120px, wraps, multi-line) becomes a **single-line** `InputTextWithHint` about 209px wide at 1280. A chip inserts a 455px prompt into a 209px box — he can see under half of what he's about to send, with no wrapping. That's a control shrinking on a user with impaired vision, and it compounds (a).

**(c) MEDIUM — more dimmed text.** `TextDisabled` added for `g_askEcho` ("you: …") and `g_answerCardQ`. Consistent with existing style but it's net-more low-contrast text.

**(d) MEDIUM — the status card lies on engine failure.** EDIT 3 inserts inside the `else` of `if (!engineOk)` (3901-3934). Engine dead → `g_backendSummary` stays empty → card shows **"Checking which AI is connected..." forever**. That directly contradicts the spec's own stated anti-"are you lying to me" goal.

No icon font is loaded — the drawn robot correctly avoids that documented failure, and `GetFontSize()` does include `FontGlobalScale`, so the mark scales with him. Colour is added, not stripped. Good on those.

## 5. TOO BIG

No. It's ~140 lines replacing ~140 lines in one panel, plus one function split. Correctly scoped.

---

## VERDICT: **APPLY WITH THE FIXES BELOW**

The async conversion is the real win and it's correct — that defect (#4 in the spec) is genuine and the fix is sound. Four fixes required:

**FIX 1 — corrected EDIT 4 anchor:** replace lines **4986–5125** (not 4957–5096).

**FIX 2 — chips must fit. Separate short label from full prompt payload:**

```cpp
// Label is what fits a 300px panel at FontGlobalScale 1.35 (main.cpp:3885);
// payload is the real prompt. A chip that runs off the panel edge is a chip
// he cannot read - ProggyClean is ~9.45px/char here, so the label budget is
// ~26 chars. Keep labels short; put the wording in the payload.
static const char* kAskChipLabel[3] = { "compile my takes", "cut dead air", "lower-third on" };
static const char* kAskChipPrompt[3] = {
    "compile every take where I said the intro line",
    "cut the dead air out of this reel",
    "turn the lower-third on",
};
```

and in the chip loop, measure/draw the **label**, insert the **prompt**:

```cpp
float w = ImGui::CalcTextSize(kAskChipLabel[i]).x + stl.FramePadding.x * 2.0f;
if (i > 0 && used + stl.ItemSpacing.x + w <= availW) { ImGui::SameLine(); used += stl.ItemSpacing.x + w; }
else used = w;
if (ImGui::Button(kAskChipLabel[i])) {
    snprintf(g_askBuf, sizeof g_askBuf, "%s", kAskChipPrompt[i]);
    g_answerCardID.clear();
    g_askFocus = true;
}
```

**FIX 3 — keep the input multi-line so he can see the whole prompt.** Replace the `InputTextWithHint` + `SetNextItemWidth` block with a two-row layout: multiline box on its own row, Send under it.

```cpp
float inW = ImGui::GetContentRegionAvail().x;
if (g_askFocus) { ImGui::SetKeyboardFocusHere(); g_askFocus = false; }
bool submit = ImGui::InputTextMultiline("##ask", g_askBuf, sizeof g_askBuf,
                                        ImVec2(inW, ImGui::GetFrameHeight() * 2.2f),
                                        ImGuiInputTextFlags_EnterReturnsTrue);
if (g_askBuf[0] == 0) {   // hint, drawn manually: InputTextMultiline has no WithHint variant
    ImVec2 mn = ImGui::GetItemRectMin();
    ImGui::GetWindowDrawList()->AddText(
        ImVec2(mn.x + ImGui::GetStyle().FramePadding.x, mn.y + ImGui::GetStyle().FramePadding.y),
        ImGui::GetColorU32(ImGuiCol_TextDisabled),
        g_answerCardID.empty() ? "ask becky..." : "type your answer...");
}
```
then the Send button on the next row (unchanged styling, `ImVec2(sendW, 0)`).

**FIX 4 — label the reject, and don't lie when the engine is down:**

```cpp
engineCallAsync("reject_proposal", { {"id", pid} }, 10.0, "Discarding that edit...", [](const json&) {});
```

and in EDIT 3, move the fallback outside the `else`/success path — after the `if (st.value("ok", false))` block add:

```cpp
                } else {
                    g_backendSummary = "The becky engine is not answering - the chat is offline.";
                    g_backendOK = false;
                }
```

and set the same string in an `else` on `if (!engineOk)` at 3901 so a dead engine reports itself instead of "Checking..." forever.

Files: `X:\AI-2\becky-tools\native\becky-review\main.cpp`, `X:\AI-2\becky-tools\becky-go\internal\assistant\assist.go` (reference only), `X:\AI-2\becky-tools\becky-go\cmd\clip\bridge.go` (reference only).

---

# SPEC — 

All anchors verified against the real file. Here is the review.

---

## 1. ANCHORS — all six match exactly

I checked every anchor byte-for-byte. **All seven anchor sites match the real file**, at the exact line numbers claimed:

| Spec anchor | Real line | Status |
|---|---|---|
| `struct VideoRow {...};` | 3317 | exact match |
| `static int g_sortMode = 0;` | 3324 | exact match |
| `static char g_searchBuf[256] = { 0 };` | 3407 | exact match |
| end of `sortLibrary()` | 3490-3491 | exact match |
| search row (`InputText`→`Clear`) | 4573-4579 | exact match |
| `sortLabel` block | 4630-4632 | exact match |
| `BeginChild("videos")` … `EndChild()` | 4641 … 4672 | exact match |

Only wrong claim: the file is **5233 lines, not 5204**. Harmless — no anchor is line-number-dependent, they're all exact-text.

## 2. WOULD NOT COMPILE — nothing found

Vendored ImGui is **1.90.9** as claimed (`X:\AI-2\becky-tools\native\timeline-bench\third_party\imgui\imgui.h:30`). Every API verified present with matching signatures: `SetNextItemAllowOverlap` (imgui.h:880), `ImGuiListClipper::IncludeItemByIndex` (2580), `InputTextWithHint` (614), `ColorConvertU32ToFloat4` (927), `PathArcTo` (2883), `PathStroke(col, flags, thickness)` (2882), `AddRect/AddRectFilled/AddCircle` (2841/2842/2848), `ImDrawFlags_RoundCornersLeft` (2773), `ImDrawList::PushClipRect` (2825).

App-side identifiers all check out: `Clip` has `uint8_t r,g,b` (main.cpp:1326), `g_track[2]` (1338), `fixedButton(const char*, initializer_list<const char*>)` (883), `engineCallAsync(verb,args,timeout,label,cb)` (917) delivered on the UI thread by `drainAsync()` (935, called at 2737), `applyFolderView` (3511), `baseName` (74), `g_transcribeMx/g_transcribeInFlight` (3343/3344). `<map>`, `<set>`, `<algorithm>` all included (52/55/47). `transcribe_all` is real (`becky-go/cmd/clip/bridge.go:107`) and its reply is `{folder, transcribed, failed, errors}` (`transcribe.go:355-360`) — the spec's field names are correct.

`(std::min)`/`(std::max)` are parenthesized against the windows.h macros. No name collisions for `clip`, `srcCol`, `res` in the enclosing scope. No pre-existing `midEllipsis`/`pillButton`/`drawLibraryCard`.

## 3. COLLIDES WITH EXISTING WORK — none

No existing clipper, pill, card, or "Transcribe all" anywhere in the file. `cardColorFor`/`g_cardColor` (3466-3476) is the **Q&A card** palette keyed by `c.id`, used only at 5023 — unrelated to `srcCol`, no collision. The spec correctly *reuses* `fixedButton`, `engineCallAsync` and the `beginWork` indicator rather than reinventing them, and leaves 4637-4640 / 4673-4683 (focus guard, arrows, Space/Enter) untouched as promised.

## 4. REAL BUGS

**(a) CONFIRMED — the "Transcribe all" failure message never appears.** Display is gated at main.cpp:4979: `if (!g_renderMsg.empty() && nowSec() - g_renderMsgAt < 8.0)`. The spec's callback sets `g_renderMsg` in both branches but **never `g_renderMsgAt`**. On the failure path it returns early, so `g_renderMsgAt` is hours stale → the error is silently swallowed. Jordan clicks the button, waits an hour, gets nothing. (The success path only works by accident, because `applyFolderView` sets `g_renderMsgAt` at 3530.)

**(b) `w < 40.0f` early return desyncs the clipper.** `drawLibraryCard` returns without advancing the cursor, while `ImGuiListClipper` assumes a fixed `libCardStride()` per item. Unreachable at `libW >= 320`, but it's a landmine one layout change away.

**(c) Right-click menu dies on scroll.** With the clipper, if item `i` leaves the display range, `BeginPopup("rowctx")` is never called and the menu closes. The old code submitted all rows. Minor regression.

**(d) "Transcribe all" is live with no folder open** — it renders before the `g_videos.empty()` check at 4635.

**(e) `midEllipsis` is a linear scan.** On a panel drag, every visible card recomputes: ~20 cards × ~60 CalcTextSize each ≈ 1200/frame. Survivable, but a binary search is three lines.

## 5. HARD-RULE VIOLATIONS

**(a) The OFF-state pill is less visible than the control it replaces.** `pillButton` off = transparent background, `IM_COL32(255,255,255,38)` outline (15% alpha), grey text. It replaces a `SmallButton` that had the app's normal opaque button background. For a user with impaired vision, a 15%-alpha outline is effectively invisible. This mutes an existing affordance.

**(b) Two controls change identity.** The explicit "Search" button disappears (becomes a small drawn magnifier) and "Smart (qmd)" changes from *run-now* to *arm-a-toggle*. Defensible per checklist 20, but it is a muscle-memory change and must be a deliberate decision, not a side effect.

**(c) The sort pills lose their self-describing label and gain no tooltip.** The old button read "Date (newest)"; the new one reads "newest" with no explanation that clicking the active pill flips direction.

Not violations: no icon font (primitives are drawn deliberately — correct), no text-size reduction, nothing blocks the UI thread, `fixedButton` is used correctly so the button doesn't move.

## 6. TOO BIG — no

~180 lines, self-contained, three new file-scope helpers plus two replaced UI blocks. It's an addition, not a refactor.

---

## VERDICT: APPLY WITH THE FIXES BELOW

**Fix (a) — required.** In §5's callback, set the timestamp in both branches:
```cpp
    if (!r.value("ok", false)) {
        g_renderMsg = "Transcribe all failed: " + r.value("error", std::string("unknown"));
        g_renderMsgAt = nowSec();
        return;
    }
    const json& d = r.contains("data") ? r["data"] : r;
    if (d.contains("folder")) applyFolderView(d["folder"], g_folderRoot);
    int okN = d.value("transcribed", 0), badN = d.value("failed", 0);
    g_renderMsg = "Transcribed " + std::to_string(okN) +
                  (badN ? (", " + std::to_string(badN) + " failed") : "");
    g_renderMsgAt = nowSec();
```

**Fix (b) — required.** In `drawLibraryCard`, move `p0` above the guard and advance one stride:
```cpp
    const ImVec2 p0  = ImGui::GetCursorScreenPos();
    if (w < 40.0f) { ImGui::SetCursorScreenPos(ImVec2(p0.x, p0.y + libCardStride())); return res; }
    ImDrawList* dl   = ImGui::GetWindowDrawList();
    const ImVec2 p1  = ImVec2(p0.x + w, p0.y + h);
```
(delete the original `if (w < 40.0f) return res;` and the later duplicate `p0`.)

**Fix (d) — required.** Gate the button in §5:
```cpp
    if (!g_videos.empty()) {
        if (g_transcribeAllBusy) ImGui::BeginDisabled();
        ...
        if (g_transcribeAllBusy) ImGui::EndDisabled();
    }
```

**Fix 5(a) — required (accessibility).** In `pillButton`, make the off state readable:
```cpp
    ImGui::PushStyleColor(ImGuiCol_Text,          on ? ImVec4(1, 1, 1, 1) : ImVec4(0.80f, 0.84f, 0.90f, 1.0f));
    bool hit = ImGui::Button(label);
    ImGui::GetWindowDrawList()->AddRect(ImGui::GetItemRectMin(), ImGui::GetItemRectMax(),
        on ? accent : IM_COL32(255, 255, 255, 130), 999.0f, 0, on ? 2.0f : 1.5f);
```

**Fix 5(c) — required.** Add tooltips after each sort pill in §5:
```cpp
    if (ImGui::IsItemHovered()) ImGui::SetTooltip("%s", g_sortMode <= 1
        ? "sorted by date - click again to flip newest/oldest" : "sort by date");
```
(and the matching one for the name pill.)

**Fix (c) — optional, 3 lines.** Add `static int g_libCtxIdx = -1;` next to `g_libSel`; in the loop after `drawLibraryCard`, `if (ImGui::IsPopupOpen("rowctx")) g_libCtxIdx = i;`; and beside the existing include, `if (g_libCtxIdx >= 0) clip.IncludeItemByIndex(g_libCtxIdx);`.

**Fix (e) — optional.** Replace the `midEllipsis` linear loop with a binary search over `head`.

**Flag to the caller, not a fix:** `openTranscript` (main.cpp:3658) calls `engineCall("transcript", ..., 25.0)` **synchronously on the UI thread**. The spec makes double-click hit it more discoverably but does not introduce it. It is a pre-existing 25-second freeze risk of exactly the class `engineCallAsync` was written to kill — it belongs to whoever owns that path.

---

# SPEC — ONE STABLE COLOUR PER SOURCE VIDEO (ratified 2026-07-22, Jordan's rule)

Jordan, verbatim: "it's supposed to be 1 color for that source video - all
clips from that source video are made a certain color and that color does not
change for the rest of the project."

## The rule (binding for every future agent)

1. Every clip from the same source video wears the SAME colour, everywhere it
   appears: timeline clips, auditions/previews, Q&A card chips.
2. The colour is assigned when the source FIRST appears in the project, from
   the fixed high-contrast 8-entry palette, in first-appearance order —
   "#14FF39 (video 1), #00AEEF (video 2), #DC143C (video 3)" is what Jordan
   has learned to read. The palette is an ACCESSIBILITY AID (impaired vision);
   never mute, desaturate or reorder it. Collisions past 8 sources are fine.
3. Once assigned, the colour NEVER changes for the life of the project —
   across edits, deletes, re-orders, reel reloads, app/engine restarts.
   Deleting every clip of a source does NOT release its colour.

## Where it lives (do not reimplement elsewhere)

- **Owner: the engine.** `becky-go/cmd/clip/clipcolor.go` — `clipColor(source)`
  is the single choke point; every `TimelineView` clip carries `color`.
- **Persistence (the 2026-07-22 fix):** the source→colour map is written to
  `%LOCALAPPDATA%\becky\colors\<fnv64-of-project-dir>.json` on every new
  assignment and loaded by `LoadClipColors()` from `OpenFolder` AND `LoadReel`
  (reel-before-folder boot order is safe: disk wins over pre-load
  assignments). Before this, the map died with the engine process and every
  relaunch re-dealt colours in that session's first-appearance order — the
  exact "colors are going wild" bug.
- **Native mirror:** `main.cpp` `g_srcRGB`/`paintClipFromKnownSource()` —
  locally-built preview clips (seekToSpan auditions, add_clip fallback) reuse
  the engine's colour; crimson only for a source the engine has never stated.
- **Regression tests:** `clipcolor_test.go` — `TestClipColorsSurviveEngineRestart`
  is Jordan's scenario verbatim; keep it green.
