#!/usr/bin/env python
"""Local brain: llama-server (Gemma 4 E4B) manager + escalation tool loop (spec 3.1/3.2).
On-demand faucet: spawned lazily on first escalation, killed on quit ONLY if we spawned it."""
import base64, json, os, re, shlex, subprocess, time

import requests

from traces import REPO, becky

LLAMA_EXE = r"C:\Users\only1\ai-memory\llama-cpp\llama-server.exe"
MODEL = os.path.join(REPO, "models", "gemma4", "gemma-4-E4B-it-qat-UD-Q4_K_XL.gguf")
MMPROJ = os.path.join(REPO, "models", "gemma4", "mmproj-F16.gguf")
BASE = "http://127.0.0.1:8033"
LAUNCH = [LLAMA_EXE, "-m", MODEL, "--mmproj", MMPROJ, "-ngl", "99", "-c", "8192",
          "--host", "127.0.0.1", "--port", "8033", "--jinja"]

# becky-go/cmd/becky-voice/main.go handleIntent() (the `!ok` branch after matchInPack):
# a failed catalog match is the ONLY Type:"error" event whose Text starts this way --
#   "Didn't catch that — try saying the tool name or 'what can you do'."
# Other error Texts there: "Nothing pending to confirm.", `Don't know a pack named %q.`,
# "parse error: %v" -- none share the prefix, so prefix-match (em-dash-encoding-proof).
NOMATCH_TEXT = "Didn't catch that — try saying the tool name or 'what can you do'."
NOMATCH_PREFIX = "Didn't catch that"

PERSONA = ("You are Whoretana, a terse profane-casual desktop voice assistant - direct, "
           "funny, never robotic. Active pack: %s. "
           "Answer in 2 sentences or less unless asked for detail.")

BUILTINS = [
    ("screenshot", "Capture the screen; the image comes back so you can see it.", {}),
    ("desktop_click", "Click the mouse at screen pixel x,y (button: left|right|double).",
     {"x": "integer", "y": "integer", "button": "string"}),
    ("desktop_type", "Type text at the current keyboard focus.", {"text": "string"}),
    ("desktop_press", "Press a key or combo, e.g. 'enter' or 'ctrl+shift+s'.", {"keys": "string"}),
    ("desktop_move", "Move the mouse to screen pixel x,y.", {"x": "integer", "y": "integer"}),
]

_proc = None
_pending_call = None  # bounced non-green tool call awaiting the user's yes (spec 3.2.4)


def has_pending():
    return _pending_call is not None


def is_no_match(ev):
    return ev.get("type") == "error" and str(ev.get("text", "")).startswith(NOMATCH_PREFIX)


def health(timeout=1.0):
    try:
        return requests.get(BASE + "/health", timeout=timeout).status_code == 200
    except Exception:
        return False


def ensure_server(wait=90.0, after_spawn=None):
    """Health-check, spawn hidden if down, poll until ok. Returns True when serving."""
    global _proc
    if health():
        return True
    if _proc is None or _proc.poll() is not None:
        if not (os.path.exists(LLAMA_EXE) and os.path.exists(MODEL)):
            return False
        _proc = subprocess.Popen(LAUNCH, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                                 creationflags=getattr(subprocess, "CREATE_NO_WINDOW", 0))
        if after_spawn:
            try:
                after_spawn()
            except Exception:
                pass
    t0 = time.time()
    while time.time() - t0 < wait:
        if health():
            return True
        if _proc is not None and _proc.poll() is not None:
            return False  # crashed while loading
        time.sleep(1.0)
    return False


def shutdown():
    global _proc
    if _proc is not None and _proc.poll() is None:
        try:
            _proc.kill()
        except Exception:
            pass
    _proc = None


# ---- tool schema + execution ---------------------------------------------------
def load_catalog():
    r = subprocess.run([becky("becky-catalog"), "--json"], capture_output=True,
                       text=True, encoding="utf-8", timeout=30)
    return (json.loads(r.stdout) or {}).get("tools") or []


def build_tools(active_pack="default"):
    tools, meta = [], {}
    try:
        cat = load_catalog()
    except Exception:
        cat = []
    for t in cat:
        if t.get("pack") not in ("default", active_pack):
            continue
        name = t["verb"].replace("-", "_")
        meta[name] = {"verb": t["verb"], "tier": t.get("tier", "green")}
        tools.append({"type": "function", "function": {
            "name": name, "description": t.get("summary", ""),
            "parameters": {"type": "object", "properties": {
                "args": {"type": "string", "description": "full command-line arguments"}}}}})
    for name, desc, props in BUILTINS:
        tools.append({"type": "function", "function": {
            "name": name, "description": desc,
            "parameters": {"type": "object",
                           "properties": {k: {"type": v} for k, v in props.items()}}}})
    return tools, meta


def tool_gate(name, meta, hands_free):
    if name in ("screenshot", "desktop_move"):
        return "green"
    if name in ("desktop_click", "desktop_type", "desktop_press"):
        return "green" if hands_free else "yellow"  # D16 confirm gate
    return meta.get(name, {}).get("tier", "green")


def grab_screenshot():
    import mss
    import mss.tools
    with mss.mss() as s:
        g = s.grab(s.monitors[1])
        png = mss.tools.to_png(g.rgb, g.size)
    return "data:image/png;base64," + base64.b64encode(png).decode("ascii")


def desktop_tool(name, a):
    import pyautogui
    x, y = int(a.get("x", 0) or 0), int(a.get("y", 0) or 0)
    if name == "desktop_click":
        b = str(a.get("button") or "left")
        pyautogui.click(x, y, clicks=2 if b == "double" else 1,
                        button="left" if b == "double" else b)
    elif name == "desktop_type":
        pyautogui.write(str(a.get("text", "")), interval=0.02)
    elif name == "desktop_press":
        keys = [k for k in re.split(r"[+\s]+", str(a.get("keys", "")).lower()) if k]
        if len(keys) > 1:
            pyautogui.hotkey(*keys)
        elif keys:
            pyautogui.press(keys[0])
    elif name == "desktop_move":
        pyautogui.moveTo(x, y)
    return "ok"


def run_tool(name, args, meta):
    """Returns (result_text, image_data_url_or_None)."""
    if name == "screenshot":
        return "screenshot captured, image attached", grab_screenshot()
    if name.startswith("desktop_"):
        return desktop_tool(name, args), None
    m = meta.get(name)
    if not m:
        return "unknown tool: " + name, None
    argv = [becky(m["verb"])] + shlex.split(str(args.get("args") or ""), posix=False)
    try:
        r = subprocess.run(argv, capture_output=True, text=True, encoding="utf-8", timeout=180)
        out = (r.stdout or "").strip() or (r.stderr or "").strip() or ("exit %d" % r.returncode)
    except Exception as e:
        out = "tool failed: %s" % e
    return out, None


def _chat(messages, tools):
    r = requests.post(BASE + "/v1/chat/completions",
                      json={"messages": messages, "tools": tools,
                            "temperature": 0.6, "max_tokens": 400}, timeout=180)
    r.raise_for_status()
    return r.json()


def escalate(text, pack="default", hands_free=False, chat_fn=None, confirm=False):
    """Spec 3.2: tool-call loop, max 3 rounds; non-green tools bounce to need_confirm.
    confirm=True resumes the stored bounced call (the user said yes)."""
    global _pending_call
    chat = chat_fn or _chat
    pend, _pending_call = _pending_call, None
    if confirm and pend:
        tools, meta, messages = pend["tools"], pend["meta"], pend["messages"]
        out, img = run_tool(pend["name"], pend["args"], meta)
        messages.append({"role": "tool", "tool_call_id": pend["id"], "content": out[:4096]})
        if img:
            messages.append({"role": "user", "content": [
                {"type": "image_url", "image_url": {"url": img}},
                {"type": "text", "text": "(the screenshot you just took)"}]})
    else:
        tools, meta = build_tools(pack)
        messages = [{"role": "system", "content": PERSONA % pack},
                    {"role": "user", "content": text}]
    for _ in range(3):
        try:
            msg = chat(messages, tools)["choices"][0]["message"]
        except Exception as e:
            return {"text": "Local brain choked: %s" % e, "tier": "green", "action": "none"}
        calls = msg.get("tool_calls") or []
        if not calls:
            return {"text": (msg.get("content") or "").strip() or "Got nothing.",
                    "tier": "green", "action": "none"}
        messages.append(msg)
        for c in calls:
            fn = c.get("function") or {}
            name = fn.get("name", "")
            try:
                args = json.loads(fn.get("arguments") or "{}")
            except Exception:
                args = {}
            tier = tool_gate(name, meta, hands_free)
            if tier != "green":  # yellow AND red both bounce; NEVER auto-execute red (3.2.4)
                _pending_call = {"name": name, "args": args, "id": c.get("id", ""),
                                 "tools": tools, "meta": meta, "messages": messages}
                return {"text": "That one needs a yes from you - %s is a %s action." % (name, tier),
                        "tool": name, "tier": tier, "action": "await_confirm",
                        "need_confirm": True}
            out, img = run_tool(name, args, meta)
            messages.append({"role": "tool", "tool_call_id": c.get("id", ""),
                             "content": out[:4096]})
            if img:  # spec 3.2.6: screenshot re-enters as image_url so the mmproj sees it
                messages.append({"role": "user", "content": [
                    {"type": "image_url", "image_url": {"url": img}},
                    {"type": "text", "text": "(the screenshot you just took)"}]})
    return {"text": "I hit my tool budget - narrow it down for me?", "tier": "green",
            "action": "none"}
