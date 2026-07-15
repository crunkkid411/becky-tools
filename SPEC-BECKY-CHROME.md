# SPEC — becky-chrome: make every CLI tool drive Jordan's REAL Chrome reliably

**Status: SPEC ONLY — nothing built.** Jordan, 2026-07-14: *"we're not going to mess with
it now."* This documents the root cause and the exact fix so it stops getting re-diagnosed
every time browser control fails.

---

## 0. The answer, in one paragraph

Chrome control keeps failing not because the tools are dumb, but because **modern Chrome
deliberately refuses to be driven through its own automation plumbing when that plumbing
points at your real, logged-in profile.** Since **Chrome 136 (April 2025)**, the
`--remote-debugging-port` switch is *ignored* on the default profile directory — a security
change to stop cookie-stealing malware. Every Claude browser method (the official
Claude-in-Chrome extension, browser-harness, chrome-devtools-mcp) drives Chrome through
that debug port / debugger API. So against your real profile they either can't attach, or
silently spawn a **fresh empty Chrome** with none of your logins — which is exactly the
"sandboxed / blocked / can't reach anything" behavior. Qwen-CLI works because it never
touches that plumbing: it moves the real mouse and types real keys at the Windows level,
and Chrome can't tell it apart from a human.

**The fix is piping, not a new tool:** run **one** Chrome dedicated to automation, launched
with the debug port open *and* a non-default profile folder (Chrome 136 requires this), log
into the sites once, and point **every** CLI tool at that one shared Chrome. Plus a Win32
OS-level fallback for the cases CDP still can't do (native file dialogs, aggressive
anti-bot).

---

## 1. Why it breaks — the reasoning in full

### The two ways to drive a browser

| Approach | How it works | Does Chrome fight it? |
|---|---|---|
| **Automation plumbing (CDP)** — CDP / the extension `chrome.debugger` API | Connects to Chrome's DevTools debug port and issues protocol commands | **YES** — this is the door Chrome 136 slammed on real profiles |
| **OS-level (human emulation)** — Win32 `SendInput`/`mouse_event` + screenshots | Moves the real cursor and types real keys into the focused window | **NO** — Chrome cannot distinguish it from Jordan |

Every Claude browser tool is in the first row. Qwen-CLI is in the second row. That single
distinction is the whole story.

### The Chrome 136 change (the load-bearing fact)

From Google's own blog: starting Chrome 136, `--remote-debugging-port` and
`--remote-debugging-pipe` are **not respected when debugging the default Chrome data
directory.** They must be accompanied by `--user-data-dir` pointing at a **non-standard**
directory (which gets a different encryption key, so a compromised debug session can't
decrypt the real profile's cookies/passwords). Google explicitly recommends a custom
user-data-dir for any debugging, and "Chrome for Testing" for automation.

**Consequence for us:** point any CDP tool at Jordan's normal, logged-in Chrome and the
port is ignored. The tool's fallback is to launch its own Chrome — a blank one. That blank
browser is the "sandbox" the forensic agent reported. Nothing was actually sandboxing the
agent; **Chrome refused the connection and the tool quietly opened an empty browser.**

### Why "the system can't communicate with itself"

Each tool (extension, browser-harness, chrome-devtools-mcp) expects or spawns *its own*
Chrome, and Chrome blocks the debug port on the one profile that has the logins. So there
is **no shared, reachable Chrome** for the tools to meet at. The fix is to create exactly
that: one always-on Chrome with the port open on a dedicated profile, that everything dials
into.

### Why the file-upload step specifically died (reverse-image OSINT)

Two independent reasons, both fixed below:
1. **Native OS file dialog.** Clicking a page's "Upload" button opens the Windows
   *Open File* dialog — an OS window, not part of the page. CDP screenshot+click can't
   operate it. (The clean CDP path is `DOM.setFileInputFiles`, which sets the file directly
   on the `<input type=file>` and never opens the dialog.)
2. **No real browser to begin with** (the blank-Chrome fallback above) — no logins, and on
   a cloud run, no access to the local image file at all.

---

## 2. Fix 1 — the shared debug Chrome (THE pipe; primary)

Run one Chrome instance for automation. It sits alongside Jordan's everyday Chrome; his
normal browsing profile is never touched.

### One-time launch

```bat
"C:\Program Files\Google\Chrome\Application\chrome.exe" ^
  --remote-debugging-port=9222 ^
  --user-data-dir="X:\AI-2\becky-chrome-profile"
```

- `--remote-debugging-port=9222` opens the automation door.
- `--user-data-dir="X:\AI-2\becky-chrome-profile"` is a **dedicated** folder — Chrome 136
  requires it to be non-default or it ignores the port. This folder becomes a real,
  persistent profile (cookies, logins survive restarts) — it is **not** a sandbox, just a
  second profile.

### One-time human step (do this once, in that window)

Sign into the OSINT engines Jordan uses (PimEyes, Google, Yandex, etc.). Those logins
persist in `becky-chrome-profile` forever after. This is the only manual step, and it's
one-time.

### Point every tool at it

All three methods can attach to the same instance instead of spawning their own:

- **browser-harness** — it already connects to a running Chrome over CDP; give it the
  becky-chrome endpoint (resolve the WebSocket from `http://127.0.0.1:9222/json/version`).
  Its own SKILL rule is *"connect to the user's running Chrome, don't launch your own"* —
  becky-chrome simply *is* that running Chrome, with the port guaranteed open.
- **chrome-devtools-mcp** — configure it to attach to `127.0.0.1:9222` (its documented
  "connect to existing Chrome" mode) rather than launch a fresh browser.
- **Official Claude-in-Chrome extension** — the extension attaches to the browser it's
  installed in; run it *inside* the becky-chrome instance (install/enable it in that
  profile), or use its attach-to-debugging mode.

### Proof it's live (the one-command check, for when this is built)

```bat
curl http://127.0.0.1:9222/json/version
```

Returns the browser build + a `webSocketDebuggerUrl`. If that JSON comes back, the pipe is
open and every tool can attach. If it 404s / refuses, Chrome ignored the port (almost
always: the user-data-dir was the default one, or Chrome was already running).

### Known catches (write these down so they don't cost hours again)

- **You cannot add the port to an already-open Chrome.** becky-chrome must be launched as
  its own instance. The separate `--user-data-dir` guarantees it's a distinct instance, so
  it coexists with Jordan's normal Chrome.
- **Separate profile = separate logins.** That's the unavoidable tax of the 136 change:
  you log into the OSINT sites once in becky-chrome. It's still a real logged-in profile.
- **Uploads:** use `DOM.setFileInputFiles` (CDP) to attach a file to an `<input type=file>`
  — never click "Upload" and try to drive the OS dialog.

---

## 3. Fix 2 — OS-level control (the Qwen way; bulletproof fallback)

For anything CDP still can't do — native file dialogs, aggressive anti-bot, drag-drop —
drive the real window at the OS level, exactly how Qwen-CLI does and exactly how becky
already drives the Shotcut window (PowerShell + Win32 `SetCursorPos`/`mouse_event`/
`SendKeys`, screenshot to verify; see the `driving-native-windows-guis` note).

- **Pro:** Chrome cannot refuse real mouse/keys; no port, no profile juggling; can operate
  the native Windows file-open box that CDP can't.
- **Con:** the Chrome window must be foreground, one task at a time.

**Standardization:** becky-chrome on 9222 (Fix 1) is the daily driver — fast, all tools
share it, headless-ish. OS-level SendInput (Fix 2) is the fallback when a page fights CDP.
Between the two, there is no browser task Qwen can do that this stack can't.

---

## 4. What "building this" actually is (when Jordan says go)

No new tool. It's a launcher + config, matching the two existing Task-Scheduler launchers:

1. `Start Becky Chrome.bat` — launches the command in §2 (idempotent: if
   `http://127.0.0.1:9222/json/version` already answers, do nothing). Optional: a hidden
   ONLOGON Task-Scheduler entry so becky-chrome is always up.
2. A one-line config change per tool telling it to attach to `127.0.0.1:9222` instead of
   spawning its own Chrome.
3. First-run: the human logs into the OSINT engines once in that window.
4. VERIFY (offline, one command): §2's `curl .../json/version` returns the browser, then
   any tool attaches and screenshots a page — proving a real, logged-in, debuggable Chrome.

That is the entire "piping" job.

---

## 5. Non-goals / rejected

- **A new browser-control tool.** The tools are fine; the connection was refused. Rejected.
- **Chrome for Testing as the daily driver.** Google recommends it for automation, but it's
  a separate binary without Jordan's real logins/extensions. becky-chrome (real Chrome.exe
  + dedicated profile) keeps his real browser and logins. Chrome-for-Testing stays a
  fallback only.
- **Driving the default profile over CDP.** Impossible since Chrome 136 by design. Don't
  fight it — use a dedicated profile (Fix 1) or go OS-level (Fix 2).
- **Cloud sessions for browser work.** A cloud agent has no access to the local Chrome,
  logins, or files. Browser work is local-only. Rejected.

---

## 6. Sources (verified 2026-07-14)

- Changes to remote debugging switches to improve security — Chrome for Developers:
  https://developer.chrome.com/blog/remote-debugging-port
- browser-use #1520 — Chrome ≥136 no longer drivable over CDP on the default `--user-data-dir`:
  https://github.com/browser-use/browser-use/issues/1520
- chrome-devtools-mcp #1830 — autoConnect fails against the default profile after 136:
  https://github.com/ChromeDevTools/chrome-devtools-mcp/issues/1830
- Setting up a Chrome debugging profile for Claude Code + authenticated sites (raf.dev):
  https://raf.dev/blog/chrome-debugging-profile-mcp/
- chrome.debugger API reference:
  https://developer.chrome.com/docs/extensions/reference/api/debugger
