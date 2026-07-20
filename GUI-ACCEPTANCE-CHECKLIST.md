# GUI-ACCEPTANCE-CHECKLIST.md — acceptance criteria for ANY Becky Review GUI

Extracted verbatim from Jordan's ten iteration documents on the Becky Review GUI:
`becky-review-user-feedback.md` **[fb1]**, `becky-review-user-feedback2.md` **[fb2]**,
`becky-review-user-feedback3.md` **[fb3]**, `becky-review-user-feedback4.md` **[fb4]**,
`becky-review-gui-user-feedback5.md` **[fb5]**, `becky-review-gui-user-feedback6.md` **[fb6]**,
`becky-review-gui-user-feedback7.md` **[fb7]**, `becky-review-user-feedback8.md` **[fb8]**,
`becky-review-user-feedback9.md` **[fb9]**, `becky-review-user-lag.md` **[lag]**,
plus `ACCESSIBILITY.md` **[ACC]** and `GUI-RULES.md` **[GUI]**.

**This is the acceptance criteria for EVERY becky review GUI — the WPF `gui\BeckyReviewNative`,
the native C++/ImGui `native\becky-review` ("Becky Review 3"), and any future one.** These are not
suggestions. Jordan stated each of them as a concrete requirement, several of them two and three
times because they were ignored. A new app that does not meet these has not been built — it has
been restarted.

**Jordan is a professional video editor, sighted with impaired vision, no screen reader, mouse and
keyboard only.** Items tagged **[COLOR]** are an **ACCESSIBILITY AID** — his high-contrast palette
helps him read the screen. Never strip, mute, desaturate, or "tone down" color anywhere in this app.

Where two documents conflict, the **later** document wins; conflicts are flagged inline.

---

## LAYOUT

1. The gray area above the clips must show a **timecode ruler with tick marks**. `[fb1]`
2. Each clip must show a **thumbnail as a small fixed icon at the leftmost part of the clip** — not an image that resizes as you zoom. `[fb1]`
3. The clip thumbnail must be **inset/pushed back** so it does not cover the waveform — he cuts visually at zero-crossings and the thumbnail blocks them. `[fb6][fb7]`
4. Timeline clips must be **2× as tall as they are now**; toolbar and ruler move up to compensate, and neither the toolbar, the ruler, nor the thumbnails change size. `[fb7]`
5. Each clip must draw its **audio waveform inside the clip body**. `[fb4][lag]`
6. Timeline toolbar buttons must **never shift position**; "render selection (N)" must be **grayed out**, never hidden, when nothing is selected. `[fb7]`
7. **Remove the "previous frame" and "next frame" buttons** from the toolbar (keep the functionality and the keyboard shortcuts). `[fb7]`
8. Add **two visually distinct left/right arrow buttons** that extend the selected clip by one video frame left / right. `[fb1]` *(fb1 placed these next to prev/next-frame; fb7 later deleted those buttons — keep the extend buttons, place them where prev/next used to be.)*
9. The playhead must be **the same size as now but all black — no white outline** — with the **top portion significantly larger so it covers the gray ruler**. `[fb5]`
10. The white part of the playhead must contain **two tiny vertical hashmarks** (reference image: `X:\AI-2\becky-tools\playhead.JPG`). `[fb7]`
11. There must be a **second playhead "stock"** — an identical black bar, without the moving white head — showing where playback will return on pause. `[fb7]`
12. There must be a **"screenshot" button** that saves the current preview frame as `Screenshot_0001`, incrementing per shot. `[fb5]`
13. There must be a **becky-clip button on the timeline** (he asked whether it was integrated and could not see a button). `[fb5]`
14. There must be a **non-intrusive, semi-transparent loading bar** for any operation taking more than one second. `[fb5]`
15. There must be a **playback-threshold toggle button on the timeline toolbar, drawn as a running-person icon**. `[fb7]`
16. When playback-threshold is ON, a **horizontal bar must appear across all clips** and be draggable up/down by mouse (reference: `X:\AI-2\becky-tools\playback-threshold.JPG`). `[fb7]`
17. Sections **below** the threshold bar must be **visually dimmed**. `[fb7]`
18. The left panel must offer three sort controls: **date created (newest first, default)**, **name (Z-A)**, and **"most relevant"**. `[fb1][fb2]`
19. The "most relevant" sort must be a **crown icon button with no text, in the same color as the blue "render selection" button**. **[COLOR]** `[fb2]`
20. The left panel must have a **qmd toggle switch** so single-word search is still available. `[fb2]`
21. Results that have **not yet been indexed** must show an **"index" icon**, similar to the existing "plus" (transcribe) button. `[fb2]`
22. After using "back", the transcript that was **just viewed must be marked with a green outline**. **[COLOR]** `[fb7]`
23. A single video's transcript must be shown **minimally — the filename must not be repeated on every segment** (reference: `X:\AI-2\becky-tools\descript-gui.JPG`), with no loss of functionality. `[fb4][lag]`
24. A single video's transcript must **flow as one continuous word-processor-style document**, with segments highlighted by timestamp, **not** broken into separated boxes with gaps (reference: audapolis). `[fb8]`
25. Review questions ("?" entries) must appear in the **right-hand panel where chat bubbles normally go**, each clickable to type and submit an answer exactly like sending a chat. `[fb3]`
26. The overlay must order its lines **Date first, Timecode second, filename third**. `[fb3][fb4][lag]` *(stated twice — fb4 flags it as a SECOND REQUEST after it was only done in preview.)*
27. The overlay must render **"ORIG TC 01:32:34:02"** — a space after "TC". `[fb3][fb4][lag]`
28. The overlay Date must include the timezone, e.g. **"Date: 2026-03-02 UTC"**. `[fb3][fb4][lag]`
29. The "overlay" button must be a **3-state toggle**: (a) ON but not shown in preview — **default**, (b) ON and shown in preview, (c) OFF and not rendered. `[fb7]`
30. The "Load reel" window must list **.json files at the TOP**, not the bottom. `[fb5]`
31. Timeline thumbnail images must be written to a **`timeline_thumbnails` subfolder inside `render`**, not loose in `render`. `[fb7]`

## READABILITY / COLOR

32. **[COLOR — ACCESSIBILITY AID]** Every multi-color assignment (Q&A, timeline clips per source video, etc.) must use **exactly this palette, in this order**: `#14FF39` Green, `#00AEEF` Blue, `#DC143C` Crimson, `#8A2BE2` Purple, `#FF57D1` Hot Pink, `#FFD700` Yellow, `#16F0EA` Cyan, `#FF8C00` Orange. `[fb3][fb4][lag]`
33. **[COLOR]** The **entire clip body** must take the source-video color — "not just a single stripe at the top (it's barely visible)". `[fb2]`
34. **[COLOR]** Clip **trim handles and borders must be the same color as the rest of the clip** — not green. `[fb4][lag]`
35. **[COLOR]** A **selected clip must become fully opaque**; unselected clips are transparent. Selection is shown by opacity, not an outline. `[fb2]`
36. **[COLOR]** Once a color is assigned to a video/question it must be **persistent for the rest of the project** — deleting all clips of video 2 must never recolor video 3. `[fb7]`
37. **[COLOR]** Clips from different source videos must be **color-coded so it is visually obvious which clips came from the same video**. `[fb1]`
38. The overlay preview must render text at **identical size to the final render**, or the overlay must not be previewed at all (while still rendering, and still keeping the "overlay" and "name" toggles). `[fb5]`
39. **[COLOR]** Never strip, flatten, or monochrome the UI "for accessibility" — his high-contrast colors are the accessibility aid. `[ACC]`
40. The mouse cursor over the timeline must be an **I-beam**, not a hand. `[fb1]`

## INTERACTION

41. **Ctrl+Z = Undo** and **Ctrl+Shift+Z = Redo**, each with a visible button. `[fb1]`
42. Undo must undo the **immediate last action** — splitting a clip then Ctrl+Z must un-split it, not move the segment to the end of the timeline. `[fb7]`
43. **Mouse wheel = zoom the timeline** in/out (not sideways scroll). `[fb2]`
44. **Ctrl + mouse wheel = scroll the timeline sideways**. `[fb2]` *(supersedes fb1's "Ctrl+scroll should only zoom".)*
45. Wheel zoom must **center on the playhead**. `[fb6]` *(supersedes fb1's "zoom toward the mouse".)*
46. Scroll-to-zoom must work **at all times** — it currently dies when fully zoomed out and after clicking anything in the left panel, only recovering via the "+" button. `[fb5]`
47. Zooming out must **reveal the empty space to the right of the last clip**; the end of the last clip must not lock as if it were the end of the timeline. `[fb7]`
48. **Up arrow = zoom in, Down arrow = zoom out** when the timeline or a clip is selected. `[fb5]`
49. **Left / Right arrow move the playhead one frame** in that direction. `[fb2]`
50. **Ctrl+Left / Ctrl+Right move the playhead to the beginning / end of the clip and must keep travelling across the entire timeline** — they must not get stuck at the current clip's edge. `[fb2][fb5][fb7]` *(fb5 flags this **CRITICAL** as a regression; fb7 notes "we've tried to fix this several times".)*
51. **Middle-click (scroll-wheel) + drag pans the timeline** left/right without rearranging any clips. `[fb3]`
52. **Click + drag on the gray ruler area pans the timeline sideways**. `[fb1]`
53. **Clicking the ruler moves the playhead** (and the secondary stock) — like single-clicking a clip. `[fb1]`
54. **Ctrl+click selects multiple clips**; with one clip selected, **Shift+click selects everything in between**. `[fb1]`
55. When multiple clips are selected, **click+dragging one must move them all**. `[fb7]`
56. With multiple clips selected there must be a **button to render just those clips**. `[fb1]`
57. During playback, **clicking inside a clip sets the return position** and does not move the playhead; on pause, playback returns there. `[fb6]`
58. Once that return-position feature is used, the timeline must **stop auto-scrolling** even if the playhead leaves the view, until the user scrolls manually. `[fb6]`
59. **Pause returns the playhead to where it was when playback started**; **Enter stops the playhead where it is**. `[fb6]`
60. **[COLOR]** When the secondary playback stock is moved during playback it must **slowly flash black and white** at roughly text-cursor speed. `[fb7]`
61. Clicking to move the secondary stock must **also select that clip**, so clips can be selected and deleted during playback regardless of playhead position — without interrupting playback. `[fb7]`
62. **Cut / split during playback applies at the secondary stock**, not the moving playhead, and must not pause playback. `[fb7]`
63. After a split, the **clip AFTER the playhead is the one selected**. `[fb5]`
64. After a **ripple edit**, if the playhead was not over the deleted clip, the playhead must move with the ripple, keeping its position relative to the underlying clip, so playback is unaffected. `[fb6][fb7]`
65. A **2x playback speed button**, with keyboard shortcut **Shift+Spacebar**. `[fb4][lag]`
66. **Esc must also delete the selected clip** (two hotkeys for delete, deliberately). `[fb4][lag]`
67. **Right-click a clip** → "Open file in File Browser", "Copy File Name" (full name **with extension, of the video file, not the transcript**), "Open transcript in the Left Side panel" (which jumps the left panel to that clip's timecode). `[fb3][fb4][lag]`
68. **Right-click a quote or video in the left panel** → "Open file in File Browser", "Copy File Name" (full name with extension, of the video file, not the transcript). `[fb3][fb4][lag]`
69. **"Open in file browser" must always select the relevant file** in the file browser window. `[fb5]`
70. **Double-clicking "render selection" or "export"** must open the containing folder with the new .mp4 selected once the render finishes; single-click behavior is unchanged. `[fb5]`
71. **Drag footage onto the timeline from any folder.** `[fb6][fb7]`
72. The drag thumbnail must not obstruct the drop target ("thumbnail in the way when dragging"). `[fb6]`
73. **Up/Down arrow keys navigate the left panel**, and must navigate **from the current selection**, not from the top of the list. `[fb2][fb4][lag]`
74. The left panel must have **ONE selection at a time shared by mouse and keyboard** — a mouse-selected item becomes the arrow-key anchor, and arrowing away deselects it. `[fb5]`
75. **Spacebar immediately after arrow-selecting** an item in the left panel plays that clip in the preview window. `[fb5]`
76. **Enter in the left panel = double-click** (quote → added to timeline; video → opens transcript). `[fb4][fb5][lag]`
77. Clicking a video then pressing **"back" must return to the same scroll position** in the list, not the top. `[fb1]`
78. After clicking a video, the user must be able to **search within that specific transcript**. `[fb2]`
79. **Clicking the timeline after typing in a search box or an answer box must return keyboard focus to the timeline**, whether or not the answer was submitted. `[fb4][fb6][lag]` *(stated three times — currently focus stays trapped in the text box and all timeline shortcuts are dead.)*
80. Render naming: all clips from the **same source** → `clips_ORIGINAL-FILENAME_NNNN.mp4`; clips from **multiple sources** → `clips_compilation_NNNN.mp4`, `NNNN` starting at `0001` and incrementing so nothing overwrites. `[fb3]`
81. Running the forensic-hits workflow must **not overwrite** `_forensic_hits.json` — it must write `_forensic_hits1.json`, `_forensic_hits2.json`, etc. `[fb3]`
82. If a new forensic-hits file arrives while a session is open, the app must either **ask to save/export first, or open a second instance** — Vegas Pro–style multiple open projects. `[fb3]`
83. Exported **EDL files must include an audio track** — they currently load into Vegas Pro 18 with no audio for anything. `[fb5]`
84. **"Open Forensic Hits.bat" must also produce a Vegas Pro compatible .edl**. `[fb5]`

## PERFORMANCE

85. **Splitting a clip 20 times in rapid succession must produce zero "preparing" states and zero dropped frames.** `[fb9]`
86. **Adding a clip whose source is already on the timeline must cause no measurable lag.** `[fb9]`
87. **During any background media work the system mouse cursor must never stutter** — background workers run at BELOW-normal priority with a capped pool. `[fb9]`
88. **A 5-minute scripted stress drive at Jordan-speed must produce no UI-thread stall greater than 100 ms.** `[fb9]`
89. No **~20-second full stall** under rapid split/click bursts (observed). `[fb9]`
90. No **~2 seconds of system-wide lag on every new clip added** (observed). `[fb9]`
91. **Caches must be keyed by SOURCE FILE + resolution level, never by clip** — split/trim/reorder/duplicate must cost zero recomputation. `[fb9]`
92. **The timeline must keep playing while a new segment is being proxied**, including other already-ready clips. `[fb6]`
93. **Adding a single quote to the timeline must not block on proxy generation** — the proxy wait made it "essentially unusable". `[fb6]`
94. **"Save" must not freeze the program** (only the preview window stayed responsive). `[fb1]`
95. **Adjusting clip lengths and making cuts must not make clips or waveforms "flash"** — "ridiculously jarring and visually fatiguing". `[fb7]`
96. **Deleting clips must not move the timeline view** — only the clips affected by the ripple edit move. `[fb6]`
97. **The timeline must stay responsive past ~3 minutes of clips** — it currently degrades. `[lag]`
98. Timeline responsiveness is a **correctness requirement**: "Lag on the timeline can turn a 2 hour human review session into 4 hours." `[fb7][GUI]`
99. Waveforms must **stream in progressively** (coarse then refined) with a placeholder drawn for missing data — never waited on. `[fb9]`
100. **No embedded browser engine.** Native pixels only. `[GUI]`

## MUST-NEVER-DO — his words, quoted

101. > "Pleae remove the yellow outline around the selected clip." `[fb2]` **[COLOR]**
102. > "'split clip' popup should not exist when user makes a cut" `[fb4][lag]`
103. > "all the little popups on the timeline need to go away; 'Removed 1 clip', 'Reordered clip', 'Undo', 'Redo'" `[fb5]`
104. > "'render' folder should NOT appear in search or browse" `[fb5]`
105. > "when a user clicks within a clip, the playhead should not be affected" `[fb6]`
106. > "deleting clips should NOT move the timeline, only the clips affected by the ripple edit" `[fb6]`
107. > "When user clicks the timeline ruler it should move the Playhead (and secondary playhead stock), but it should NOT change the selected clip" `[fb7]`
108. > "ensure trim handles and borders are not green" `[fb4][lag]` **[COLOR]**
109. > "This should not happen. If clips from video 3 are #DC143C then they must remain #DC143C for the rest of the project regardless of whether the other clips are on the timeline or not" `[fb7]` **[COLOR]**
110. > "buttons on the timeline toolbar should not move" `[fb7]`
111. > "remove 'previous frame' and 'next frame' buttons, I don't use them" `[fb7]`
112. > "there is no reason to have the filename listed for every segment, as the user has already seleccted that file" `[fb4][lag]`
113. > "it should not automatically close my session" `[fb3]`
114. > "It should not [overwrite]. Rather, it should add digits to the end" `[fb3]`
115. > "overlay preview still looks like shit (the text is significantly bigger than when it renders - it covers the entire preview window). Either make it identical to how it looks after render, or simply do not preview the overlays" `[fb5]`
116. > "thumbnail preview images are littering the 'render' folder" `[fb7]`
117. > "ctrl + back and ctrl + foreward (arrow keys) no longer navigate beyond the clip - it needs to navigate across the entire timeline, not get stuck at the current clip's edge" `[fb5]` — marked **CRITICAL**
118. > "Do NOT strip color, and do NOT replace a colored TUI with plain monochrome text 'for accessibility.'" `[ACC]` **[COLOR]**
119. > "No embedded browser engines, ever. Native pixels only." `[GUI]`
120. > "the 'hand' mouse icon should be replaced with the 'Ibeam'" `[fb1]`

---

## VAGUE — NEEDS JUDGEMENT (not checkable from a screenshot)

- "MANY of the same problems keep coming back or never actually get fixed. SIMPLE things like clicking somewhere and making it active. very basic, fundamental bullshit" `[fb6]` — a general regression complaint; the closest checkable proxy is item 79 (focus return) and item 46 (scroll-zoom dying).
- "please take a surgical approach. Every change should be thought out, and the adjacent consequences considered. Most of our issues stem from things being overengineered, or user requests being ignored." `[fb7]` — process instruction, not a UI criterion.
- "Visually showing the audio waveform inside each clip would help timeline navigation significantly. **IF it will not add enormous burden / lag**, please implement" `[fb4]` — conditional; item 5 assumes it ships, but performance (items 85–99) outranks it.
- "DOM-rebuild-per-edit - you mentioned this before, help me understand. Do we need this? What, specifically would it do?" `[fb5]` — an open question to answer, not a requirement.
- The engine questions in `[lag]` (Proxy? FFMPEG? Flyleaf? Vulkan/D3D12? LibVLCSharp? Media Foundation?) — he explicitly says: "These questions are NOT build specs - I am curious how it works."
- "Playback Threshold ... **This could be the single biggest breakthrough if done correctly**" `[fb7]` — the mechanics are checkable (items 15–17); the quality bar ("skips them seamlessly") needs human judgement.
