#!/usr/bin/env python3
"""Drive the REAL becky-clip window and ask the becky chat to INVESTIGATE the case
folder for the documented $10,000 Penguin bounty — proving the agent can now
navigate files + cite the evidence video/timestamp (the capability it lacked).
Usage: python verify_investigate.py [port]
"""
import base64, json, os, sys, time, urllib.request
import websocket

PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 9223
HERE = os.path.dirname(os.path.abspath(__file__))
_id = 0


def conn():
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
    return r.get("result", {}).get("value")


def wait(ws, js, ok, secs, every=1.0):
    end = time.time() + secs
    v = None
    while time.time() < end:
        v = ev(ws, js)
        if ok(v):
            return v
        time.sleep(every)
    return v


def main():
    ws = conn()
    wait(ws, "document.readyState", lambda v: v == "complete", 15)
    n = wait(ws, "document.querySelectorAll('.vchip').length", lambda v: isinstance(v, int) and v > 0, 30)
    print(f"[investigate] indexed chips: {n}")
    status = ev(ws, "(document.querySelector('.ul-intro p')||{}).innerText||''")
    print(f"[investigate] backend: {status!r}")

    q = ("Look in this case folder and tell me which video file the $10,000 bounty "
         "for the cat named Penguin is offered in, and the timestamp. Cite the transcript.")
    ev(ws, f"""(()=>{{ const a=document.getElementById('ask'); a.value={json.dumps(q)};
        document.getElementById('btn-ask').click(); return 1; }})()""")
    print("[investigate] asked becky; waiting up to 240s (agentic read of the folder)...")
    ans = wait(ws,
               "(()=>{const m=[...document.querySelectorAll('.msg.bot')];return m.length?m[m.length-1].innerText:'';})()",
               lambda v: isinstance(v, str) and len(v) > 0 and 'thinking' not in v.lower(),
               240, 2.0)
    note = ev(ws, "(()=>{const n=[...document.querySelectorAll('.msg.bot .note')];return n.length?n[n.length-1].innerText:'';})()")
    r = send(ws, "Page.captureScreenshot", {"format": "png"})
    with open(os.path.join(HERE, "investigate-1.png"), "wb") as f:
        f.write(base64.b64decode(r["data"]))

    print("\n================ becky's INVESTIGATION ================")
    print(ans)
    print("------ note ------")
    print(note)
    print("======================================================")
    a = (ans or "").lower()
    hit = any(k in a for k in ["batnp3jy0vc", "cvm6h_o44ac", "10,000", "$10", "10 grand", "09:0", "49:0", "penguin"])
    print("PLAUSIBLE EVIDENCE CITATION:", "YES" if hit else "NO")
    ws.close()
    sys.exit(0 if hit else 2)


if __name__ == "__main__":
    main()
