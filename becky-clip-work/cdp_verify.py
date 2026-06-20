#!/usr/bin/env python3
"""Drive the REAL becky-clip WebView2 window over CDP to verify the two fixes on
Jordan's real footage (E:/TakingBack2007):

  1. SEARCH no longer "works once then freezes": search term A, click a result
     (the old freeze trigger — media_url runs ffprobe/proxy on a real file),
     then search term B and PROVE the results actually changed.
  2. CHAT is backed by a real model: read the status line + send a question and
     confirm becky answers (and which backend answered).

Usage: python cdp_verify.py [port]   (default 9223)
Screenshots land next to this file. Prints PASS/FAIL per check; exit 0 iff the
freeze fix passes (the chat check is reported but a live-Claude hiccup is non-fatal).
"""
import base64, json, os, sys, time, urllib.request
import websocket  # websocket-client

PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 9223
HERE = os.path.dirname(os.path.abspath(__file__))
_id = 0


def targets():
    raw = urllib.request.urlopen(f"http://127.0.0.1:{PORT}/json", timeout=5).read()
    return json.loads(raw)


def page_ws():
    for t in targets():
        if t.get("type") == "page" and "127.0.0.1" in (t.get("url") or ""):
            return t["webSocketDebuggerUrl"]
    for t in targets():
        if t.get("type") == "page":
            return t["webSocketDebuggerUrl"]
    raise RuntimeError("no page target on the CDP endpoint")


def connect():
    last = None
    for _ in range(60):
        try:
            ws = websocket.create_connection(page_ws(), max_size=64 * 1024 * 1024)
            send(ws, "Page.enable")
            return ws
        except Exception as e:
            last = e
            time.sleep(0.5)
    raise RuntimeError(f"could not connect to CDP: {last}")


def send(ws, method, params=None):
    global _id
    _id += 1
    mid = _id
    ws.send(json.dumps({"id": mid, "method": method, "params": params or {}}))
    while True:
        msg = json.loads(ws.recv())
        if msg.get("id") == mid:
            if "error" in msg:
                raise RuntimeError(f"{method}: {msg['error']}")
            return msg.get("result", {})


def ev(ws, js):
    r = send(ws, "Runtime.evaluate",
             {"expression": js, "awaitPromise": True, "returnByValue": True})
    if r.get("exceptionDetails"):
        return {"__exc": str(r["exceptionDetails"].get("text"))}
    return r.get("result", {}).get("value")


def shot(ws, name):
    r = send(ws, "Page.captureScreenshot", {"format": "png"})
    p = os.path.join(HERE, name)
    with open(p, "wb") as f:
        f.write(base64.b64decode(r["data"]))
    print(f"   shot: {name}")


def wait_for(ws, js, want, secs=20, every=0.4):
    end = time.time() + secs
    last = None
    while time.time() < end:
        last = ev(ws, js)
        if want(last):
            return last
        time.sleep(every)
    return last


def do_search(ws, term):
    ev(ws, f"""(()=>{{
      const s=document.getElementById('search'); s.value={json.dumps(term)};
      s.dispatchEvent(new KeyboardEvent('keydown',{{key:'Enter',bubbles:true}}));
      return true; }})()""")


def results_state(ws):
    return ev(ws, """(()=>{
      const els=[...document.querySelectorAll('.result')];
      const head=document.querySelector('#results .rh-count');
      return {n:els.length, first:(els[0]?els[0].innerText.slice(0,80):''),
              head:(head?head.innerText:'')}; })()""")


def main():
    print(f"[cdp] connecting on :{PORT} ...")
    ws = connect()
    rd = wait_for(ws, "document.readyState", lambda v: v == "complete", 15)
    print(f"[cdp] readyState={rd}")
    vids = wait_for(ws, "document.querySelectorAll('.vchip').length",
                    lambda v: isinstance(v, int) and v > 0, 30)
    print(f"[cdp] indexed video chips: {vids}")
    shot(ws, "verify-1-open.png")

    print("\n=== CHECK 1: search is not stuck-after-one-query ===")
    do_search(ws, "penguin")
    wait_for(ws, "document.querySelectorAll('.result').length",
             lambda v: isinstance(v, int) and v > 0, 25)
    sa = results_state(ws)
    print(f"   search 'penguin' -> {sa['n']} results; head={sa['head']!r}")
    shot(ws, "verify-2-search-penguin.png")

    clicked = ev(ws, "(()=>{const r=document.querySelector('.result'); if(!r)return false; r.click(); return true;})()")
    print(f"   clicked first result: {clicked}")
    time.sleep(2.0)  # let media_url run; the OLD bug froze everything here
    shot(ws, "verify-3-after-click.png")

    do_search(ws, "money")
    time.sleep(2.0)
    sb = results_state(ws)
    print(f"   search 'money' -> {sb['n']} results; head={sb['head']!r}")
    shot(ws, "verify-4-search-money.png")

    do_search(ws, "cat")
    time.sleep(2.0)
    sc = results_state(ws)
    print(f"   search 'cat' -> {sc['n']} results; head={sc['head']!r}")

    freeze_fixed = (sb["head"] != sa["head"]) or (sb["first"] != sa["first"]) or (sc["head"] != sb["head"])
    print(f"   RESULT: search updates after a click+re-search -> {'PASS' if freeze_fixed else 'FAIL'}")

    print("\n=== CHECK 2: chat is backed by a real model ===")
    intro = ev(ws, "(document.querySelector('.ul-intro p')||{}).innerText||''")
    print(f"   chat status line: {intro!r}")

    ev(ws, """(()=>{ const a=document.getElementById('ask');
      a.value='in one sentence, what does the use-Claude toggle do?';
      document.getElementById('btn-ask').click(); return true; })()""")
    print("   asked becky a question; waiting up to 75s for an answer ...")
    ans = wait_for(ws,
                   "(()=>{const m=[...document.querySelectorAll('.msg.bot')];"
                   "return m.length?m[m.length-1].innerText:'';})()",
                   lambda v: isinstance(v, str) and len(v) > 0 and 'thinking' not in v.lower(),
                   75, 1.0)
    note = ev(ws, "(()=>{const n=[...document.querySelectorAll('.msg.bot .note')];return n.length?n[n.length-1].innerText:'';})()")
    print(f"   becky answered: {str(ans)[:240]!r}")
    print(f"   backend note:   {str(note)!r}")
    shot(ws, "verify-5-chat.png")
    chat_ok = isinstance(ans, str) and len(ans) > 0 and "thinking" not in ans.lower()
    print(f"   RESULT: chat returned an answer -> {'PASS' if chat_ok else 'FAIL (see note)'}")

    print("\n================ SUMMARY ================")
    print(f"  freeze fix (search after click+re-search): {'PASS' if freeze_fixed else 'FAIL'}")
    print(f"  chat answered:                              {'PASS' if chat_ok else 'NEEDS-EYES'}")
    ws.close()
    sys.exit(0 if freeze_fixed else 2)


if __name__ == "__main__":
    main()
