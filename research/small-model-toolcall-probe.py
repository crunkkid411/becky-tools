# Evidence for research/model-minicpm5-2b.md Test B. Usage: python research/small-model-toolcall-probe.py <gguf> [port]
# Tool-calling smoke test through llama-server --jinja (OpenAI tools API). Stdlib only.
# Usage: python toolprobe.py <gguf> [port]
import json, subprocess, sys, time, urllib.request

SERVER = r"C:\llama.cpp\build\bin\llama-server.exe"
MODEL, PORT = sys.argv[1], int(sys.argv[2]) if len(sys.argv) > 2 else 18490
URL = f"http://127.0.0.1:{PORT}"

TOOLS = [
    {"type": "function", "function": {"name": "transcribe", "description": "Transcribe one video or audio file to a timestamped .srt",
        "parameters": {"type": "object", "properties": {"path": {"type": "string"}}, "required": ["path"]}}},
    {"type": "function", "function": {"name": "roughcut", "description": "Remove silence and retakes from a folder of raw footage and build a VEGAS timeline",
        "parameters": {"type": "object", "properties": {"folder": {"type": "string"}}, "required": ["folder"]}}},
    {"type": "function", "function": {"name": "search_footage", "description": "Find where a phrase is said in a folder of transcribed footage",
        "parameters": {"type": "object", "properties": {"folder": {"type": "string"}, "query": {"type": "string"}}, "required": ["folder", "query"]}}},
    {"type": "function", "function": {"name": "schedule", "description": "Run a named becky task every day at a given 24h time",
        "parameters": {"type": "object", "properties": {"task": {"type": "string", "enum": ["archive_iphone_history", "rebuild_search_index", "backup_projects"]},
                                                        "time": {"type": "string", "description": "HH:MM, 24-hour"}}, "required": ["task", "time"]}}},
]

CASES = [  # (request, expected calls as [(name, {arg: value-substring})])
    (r"transcribe E:\clips\fbi-recap-03.mp4", [("transcribe", {"path": "fbi-recap-03.mp4"})]),
    (r"find where I say 'county clerk' in X:\footage\23_hj-fbi-recap", [("search_footage", {"folder": "23_hj-fbi-recap", "query": "county clerk"})]),
    ("back up my projects every night at 11pm", [("schedule", {"task": "backup_projects", "time": "23:00"})]),
    (r"rough cut X:\footage\24_new and then transcribe X:\footage\24_new\a.mp4",
     [("roughcut", {"folder": "24_new"}), ("transcribe", {"path": "a.mp4"})]),
    ("what's the capital of France?", []),  # no tool fits: must call nothing
]


def post(path, body):
    req = urllib.request.Request(URL + path, json.dumps(body).encode(), {"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=300) as r:
        return json.loads(r.read())


proc = subprocess.Popen([SERVER, "-m", MODEL, "--port", str(PORT), "-ngl", "99", "-c", "8192", "--jinja", "--no-webui"],
                        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
try:
    t0 = time.time()
    while True:
        try:
            urllib.request.urlopen(URL + "/health", timeout=2); break
        except Exception:
            if time.time() - t0 > 180: sys.exit("server never came up")
            time.sleep(0.5)
    vram = subprocess.run(["nvidia-smi", "--query-gpu=memory.used", "--format=csv,noheader"], capture_output=True, text=True).stdout.strip()
    print(f"{MODEL.split(chr(92))[-1]}: loaded {time.time()-t0:.1f}s, GPU memory in use {vram}")
    ok = 0
    for req, want in CASES:
        body = {"messages": [{"role": "user", "content": req}], "tools": TOOLS, "temperature": 0, "seed": 42,
                "max_tokens": 400, "chat_template_kwargs": {"enable_thinking": False}}
        t = time.perf_counter()
        r = post("/v1/chat/completions", body)
        ms = round((time.perf_counter() - t) * 1000)
        msg = r["choices"][0]["message"]
        got = [(c["function"]["name"], json.loads(c["function"]["arguments"] or "{}")) for c in (msg.get("tool_calls") or [])]
        good = len(got) == len(want) and all(
            g[0] == w[0] and all(v.lower() in str(g[1].get(k, "")).lower() for k, v in w[1].items()) for g, w in zip(got, want))
        ok += good
        shown = got if got else ("text: " + (msg.get("content") or "")[:80].replace("\n", " "))
        print(f"{'PASS' if good else 'FAIL'} {ms:5d} ms  {req[:55]:55s} -> {shown}")
    print(f"score {ok}/{len(CASES)}")
finally:
    proc.terminate(); proc.wait(timeout=20)
