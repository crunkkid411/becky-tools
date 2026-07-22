# LOCAL WORK ORDER — REAPER Chat brain v2: kill the resource hog, answer with Claude or OpenCode Zen

**Do NOT merge-and-stop.** The branch is done only when every box below is checked with evidence.

Branch: `claude/reaper-chat-resource-errors-fuz41p`. Run from `becky-go/` unless noted. Windows 10, PowerShell.

**What changed and why:** Jordan: *"reaper chat auto starts a BROKEN chatbox that hogs system
resources AND ALSO returns errors every time i open reaper."* The old brain put a llama.cpp
`llama-server` + 4B GGUF on :11435 (GPU/RAM hog), and whenever it wasn't up, REAPER Chat errored
at every REAPER launch. The brain is now a **featherweight Go proxy** on :11435 (a few MB, zero
GPU) that forwards each chat turn to **Claude Code OAuth** (`claude -p`, already paid by Max —
the default) or **OpenCode Zen free models** (`--backend zen`, free-only ENFORCED in
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

## Step 3 — KILL THE HOG: remove whatever auto-starts the OLD llama-server brain
The repo never installed an autostart, so something on the machine is launching it with REAPER.
Check each of these and delete/disable any entry that runs `llama-server`, `start-becky-brain`,
or `becky-reaper brain` the OLD way:
1. REAPER: Options > Show REAPER resource path > `Scripts\__startup.lua` (delete the launch line
   or the whole file if that's all it does).
2. REAPER: Actions list > filter "startup" (SWS "Run at startup" actions).
3. Windows: `shell:startup` folder + Task Scheduler + Task Manager > Startup tab.
- [ ] DONE WHEN: after a reboot-free retest (close REAPER, `taskkill /im llama-server.exe /f` if
  running, reopen REAPER) **no `llama-server.exe` appears in Task Manager** and GPU/RAM stay flat.
  Report what you found and where.

## Step 4 — the check only a human can do: chat works in REAPER
    Double-click "Start Becky REAPER Brain.bat"  (press 1 = Claude, or Enter)
    Open REAPER, type into REAPER Chat: "change tempo to 128"
- [ ] DONE WHEN: a real answer comes back, no connection error. First reply takes a few seconds
  (claude -p cold start) — that is normal. Report the reply text.
- [ ] ALSO (only if Jordan wants Zen): run the .bat again, press 2, paste his OpenCode Zen key
  once when asked (it stores to his user env). Same chat test. Free models only — a paid id is
  refused in code.

## Step 5 — never see the startup error again (recommended)
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
`claude --version`), or the old llama-server still holding :11435 (Step 3). Two failed fix
attempts → stop and research, per STANDARDS-ENGINEERING.
