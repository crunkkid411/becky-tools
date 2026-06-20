#!/usr/bin/env python3
"""Drive the REAL becky-clip window to render a 2-clip compilation from Jordan's
footage, then report the produced MP4 path + sidecars so a corroboration pass
(ffprobe / ffmpeg volumedetect / becky-validate gemma4) can inspect it.

This proves the END-TO-END flow the user actually uses: search -> add clips ->
overlay -> Export. It writes the output MP4 path to render_out.txt for the bash
corroboration step. Usage: python verify_render.py [port]  (default 9223)
"""
import json, os, sys, time, urllib.request
import websocket

PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 9223
HERE = os.path.dirname(os.path.abspath(__file__))
_id = 0


def ws_connect():
    last = None
    for _ in range(60):
        try:
            ts = json.loads(urllib.request.urlopen(f"http://127.0.0.1:{PORT}/json", timeout=5).read())
            url = next((t["webSocketDebuggerUrl"] for t in ts if t.get("type") == "page"), None)
            if url:
                ws = websocket.create_connection(url, max_size=64 << 20)
                send(ws, "Page.enable")
                return ws
        except Exception as e:
            last = e
            time.sleep(0.5)
    raise RuntimeError(f"CDP connect failed: {last}")


def send(ws, method, params=None):
    global _id
    _id += 1
    mid = _id
    ws.send(json.dumps({"id": mid, "method": method, "params": params or {}}))
    while True:
        m = json.loads(ws.recv())
        if m.get("id") == mid:
            if "error" in m:
                raise RuntimeError(f"{method}: {m['error']}")
            return m.get("result", {})


def ev(ws, js):
    r = send(ws, "Runtime.evaluate", {"expression": js, "awaitPromise": True, "returnByValue": True})
    if r.get("exceptionDetails"):
        return {"__exc": str(r["exceptionDetails"].get("text"))}
    return r.get("result", {}).get("value")


def shot(ws, name):
    import base64
    r = send(ws, "Page.captureScreenshot", {"format": "png"})
    with open(os.path.join(HERE, name), "wb") as f:
        f.write(base64.b64decode(r["data"]))


def wait(ws, js, ok, secs, every=0.5):
    end = time.time() + secs
    v = None
    while time.time() < end:
        v = ev(ws, js)
        if ok(v):
            return v
        time.sleep(every)
    return v


def main():
    ws = ws_connect()
    wait(ws, "document.readyState", lambda v: v == "complete", 15)
    n = wait(ws, "document.querySelectorAll('.vchip').length", lambda v: isinstance(v, int) and v > 0, 30)
    print(f"[render] indexed video chips: {n}")

    # search and wait for playable results
    ev(ws, "(()=>{const s=document.getElementById('search');s.value='penguin';"
           "s.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true}));return 1;})()")
    wait(ws, "document.querySelectorAll('.result:not(.transcript-only)').length",
         lambda v: isinstance(v, int) and v > 0, 30)
    playable = ev(ws, "document.querySelectorAll('.result:not(.transcript-only)').length")
    print(f"[render] playable search results: {playable}")
    if not playable:
        print("FAIL: no playable results to add")
        sys.exit(2)

    # add the first 2 playable results to the timeline (double-click => addClipFrom)
    ev(ws, """(()=>{const r=[...document.querySelectorAll('.result:not(.transcript-only)')].slice(0,2);
        r.forEach(e=>e.dispatchEvent(new MouseEvent('dblclick',{bubbles:true})));return r.length;})()""")
    clips = wait(ws, "document.querySelectorAll('#timeline .clip').length",
                 lambda v: isinstance(v, int) and v >= 2, 25)
    print(f"[render] clips on timeline: {clips}")

    # turn the forensic lower-third on (extra burned-in content to verify)
    ev(ws, "(()=>{const b=document.getElementById('btn-overlay');if(b&&!b.classList.contains('on'))b.click();return 1;})()")
    time.sleep(0.5)
    shot(ws, "render-1-timeline.png")

    # trigger export DIRECTLY via the bridge, capturing the structured result.
    ev(ws, """(()=>{ window.__exp=null;
        const id='exp'+Date.now(); const old=window.__beckyResolve;
        window.__beckyResolve=(rid,env)=>{ if(old)old(rid,env); if(rid===id) window.__exp=env; };
        window.beckyCall(id,'export','{}'); return 1; })()""")
    print("[render] exporting (ffmpeg render of the real clips)...")
    raw = wait(ws, "window.__exp", lambda v: isinstance(v, str) and len(v) > 0, 240, 2.0)
    shot(ws, "render-2-exported.png")
    if not isinstance(raw, str):
        print("FAIL: export did not return in time")
        sys.exit(2)
    env = json.loads(raw)
    if not env.get("ok"):
        print("FAIL: export error:", env.get("error"))
        sys.exit(2)
    d = env["data"]
    print("[render] EXPORT RESULT:")
    for k in ("mp4", "edl", "srt", "codec", "clips", "duration_sec", "output_mb", "audio_ok", "audio", "note"):
        print(f"    {k}: {d.get(k)}")
    with open(os.path.join(HERE, "render_out.txt"), "w") as f:
        f.write(d.get("mp4", ""))
    ws.close()


if __name__ == "__main__":
    main()
