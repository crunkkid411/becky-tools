import sys, json, urllib.request, websocket
PORT = 9223
def page_ws():
    data = json.load(urllib.request.urlopen(f"http://127.0.0.1:{PORT}/json"))
    for t in data:
        if t.get("type") == "page":
            return t["webSocketDebuggerUrl"]
    raise SystemExit("no page target")
class CDP:
    def __init__(self):
        self.ws = websocket.create_connection(page_ws(), max_size=None)
        self.i = 0
    def send(self, method, params=None):
        self.i += 1
        self.ws.send(json.dumps({"id": self.i, "method": method, "params": params or {}}))
        while True:
            m = json.loads(self.ws.recv())
            if m.get("id") == self.i:
                return m
    def evaluate(self, expr):
        r = self.send("Runtime.evaluate", {"expression": expr, "returnByValue": True, "awaitPromise": True})
        if "error" in r: return {"_cdp_error": r["error"]}
        res = r["result"].get("result", {})
        if res.get("subtype") == "error" or "exceptionDetails" in r.get("result", {}):
            return {"_js_error": str(r["result"].get("exceptionDetails") or res)}
        return res.get("value", res)
    def screenshot(self, path):
        self.send("Page.enable")
        r = self.send("Page.captureScreenshot", {"format":"png"})
        import base64
        open(path,"wb").write(base64.b64decode(r["result"]["data"]))
        return path
def main():
    c = CDP()
    cmd = sys.argv[1]
    if cmd == "shot":
        print(c.screenshot(sys.argv[2]))
    elif cmd == "eval":
        print(json.dumps(c.evaluate(sys.argv[2]), indent=2, default=str))
main()
