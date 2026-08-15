# LOCAL WORK ORDER — REAPER Chat brain v2: kill the resource hog, answer with Claude or OpenCode Zen

**Do NOT merge-and-stop.** The branch is done only when every box below is checked with evidence.

Branch: `claude/reaper-chat-resource-errors-fuz41p`. Run from `becky-go/` unless noted. Windows 10, PowerShell.

**AUTHORIZATION — READ FIRST (Jordan, 2026-07-22):** The in-REAPER chat is **Jordan's own,
based on the Dawzy paper (2025)**. It predates becky and is NOT ours to rebuild. A Claude
Opus session tried to recreate it from scratch — that whole becky-side "REAPER Chat brain"
(llama-server on :11435 + whatever auto-starts it) was **UNAUTHORIZED** and is what broke
his machine. Jordan's standing order: *"its fine if u can fix it easily, but if not, then
declare it broken so the local claude code can disable that shit."* So this work order is
**ONE quick fix attempt, then the kill switch** (Step 5). Do NOT research, do NOT iterate,
do NOT touch the Dawzy chat extension itself — only what serves its port. Budget: ~15 min.

**What changed and why:** Jordan: *"reaper chat auto starts a BROKEN chatbox that hogs system
resources AND ALSO returns errors every time i open reaper."* The unauthorized brain put a
llama.cpp `llama-server` + 4B GGUF on :11435 (GPU/RAM hog), and whenever it wasn't up, the
Dawzy chat (hard-wired to `localhost:11435/v1/chat/completions`) errored at every REAPER
launch. The brain is now a **featherweight Go proxy** on :11435 (a few MB, zero GPU) that
relays each chat turn to **Claude Code OAuth** (`claude -p` — one small already-paid call per
message, the default) or **OpenCode Zen free models** (`--backend zen`, free-only ENFORCED in
`reaperbrain.IsZenFree` — paid ids are refused before any request leaves the machine).
`start-becky-brain.ps1` asks 1 (Claude) / 2 (Zen) with high-contrast colors and remembers the
answer.

## Step 0 — deterministic layer green
    go build ./... ; go vet ./... ; go test ./...
- [ ] DONE WHEN: all pass. Paste the last line.
  (A `gofmt -l .` complaint that is only CRLF on Windows is cosmetic — don't block.)

## Step 1 — PROVE the wire path offline (cloud already proved this) — no REAPER needed
    go run ./cmd/becky-reaper brain --selftest
- [ ] DONE WHEN: it prints `SELFTEST PASS`. Cloud's measured run (2026-07-22, Linux CI box):
  `/health OK`, `chat completion OK (294 bytes, echoed the user turn)`,
  `streaming (SSE) OK (596 bytes, chunks + [DONE])`, `/v1/models OK`,
  `Zen spend guard OK (paid ids refused, free ids allowed)`, `SELFTEST PASS`. Paste yours.

## Step 2 — build the REAL binaries
    build-all-tools.bat
- [ ] DONE WHEN: it finishes and `becky-go\bin\becky-reaper.exe` has today's date. Paste `dir bin\becky-reaper.exe`.

## Step 3 — KILL THE HOG: remove whatever auto-starts the UNAUTHORIZED llama-server brain
The repo never installed an autostart, so a previous agent put something on the machine.
Check each of these and delete/disable any entry that runs `llama-server`, `start-becky-brain`,
or `becky-reaper brain` the OLD way — **but do NOT touch Jordan's Dawzy chat extension/scripts
themselves** (only kill what launches a model server):
1. REAPER: Options > Show REAPER resource path > `Scripts\__startup.lua` (delete the launch line
   or the whole file if that's all it does).
2. REAPER: Actions list > filter "startup" (SWS "Run at startup" actions).
3. Windows: `shell:startup` folder + Task Scheduler + Task Manager > Startup tab.
- [ ] DONE WHEN: after a reboot-free retest (close REAPER, `taskkill /im llama-server.exe /f` if
  running, reopen REAPER) **no `llama-server.exe` appears in Task Manager** and GPU/RAM stay flat.
  Report what you found and where.

## Step 4 — the ONE fix attempt: does the chat answer through the new brain?
    Double-click "Start Becky REAPER Brain.bat"  (press 1 = Claude, or Enter)
    Open REAPER, type into the chat: "change tempo to 128"
- [ ] DONE WHEN: a real answer comes back, no connection error. First reply takes a few seconds
  (claude -p cold start) — that is normal. Report the reply text.
- [ ] ALSO (only if Jordan wants Zen): run the .bat again, press 2, paste his OpenCode Zen key
  once when asked (it stores to his user env). Same chat test. Free models only — a paid id is
  refused in code.
- If the chat still errors after ONE honest attempt (brain running + `brain --check` says OK):
  **STOP. Do not debug the chat. Go straight to Step 5.**

## Step 5 — THE KILL SWITCH (Jordan's order: can't fix quickly = disable it)
Declare the becky reaper-chat integration BROKEN and make REAPER boring again:
1. Step 3's autostart removal (if not already done) — nothing may launch a model server.
2. If the chatbox itself auto-opens broken at REAPER launch and Jordan wants it gone for now:
   close/undock it and save the layout, or disable its startup hook in the Dawzy scripts'
   config — DISABLE, never delete; it's his.
3. Note in HANDOFF-LOG.md that the brain is disabled and the Dawzy chat is untouched.
- [ ] DONE WHEN: REAPER opens with no chat error, no llama-server, flat GPU/RAM. Tell Jordan in
  one sentence what was disabled and that his Dawzy chat files were not modified.

## Step 6 — OPTIONAL, only if Step 4 worked: never see the startup error again
The chatbox errors at REAPER launch only when nothing serves :11435. The proxy is light enough
to just always run. Create a login-startup shortcut (one command, PowerShell):
    $s=(New-Object -ComObject WScript.Shell).CreateShortcut("$env:APPDATA\Microsoft\Windows\Start Menu\Programs\Startup\Becky REAPER Brain.lnk")
    $s.TargetPath="X:\AI-2\becky-tools\becky-go\bin\becky-reaper.exe"
    $s.Arguments="brain --start"
    $s.WindowStyle=7
    $s.Save()
- [ ] DONE WHEN: after sign-out/in (or manually launching the shortcut), `becky-reaper brain --check`
  prints `OK - REAPER Chat can connect`, and opening REAPER shows no chat error.

## If something fails
Most likely NON-code causes: `claude` not on PATH for a fresh shell (open a new terminal;
`claude --version`), or the old llama-server still holding :11435 (Step 3). **No research
loops, no second theories — one failed attempt lands on Step 5's kill switch.** This entire
work order must not cost Jordan more than ~15 minutes of agent time.
