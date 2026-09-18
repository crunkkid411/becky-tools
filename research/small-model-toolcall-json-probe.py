# Evidence for research/proposal-small-model-automation.md (JSON-forced tool calls; shows the path-mangling trap).
# Usage: python research/small-model-toolcall-json-probe.py <gguf> [port]
# Same 5 cases as toolprobe.py, but no native tool format: a JSON schema (llama.cpp turns it into a grammar)
# forces {"calls":[{"name":..,"arguments":{..}}]} with names from a closed list.
import json, sys
import os; exec(open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "small-model-toolcall-probe.py")).read().split("proc = subprocess.Popen")[0])
item = {"oneOf": [{"type": "object", "properties": {"name": {"const": t["function"]["name"]}, "arguments": t["function"]["parameters"]},
                   "required": ["name", "arguments"]} for t in TOOLS]}
schema = {"type": "object", "properties": {"calls": {"type": "array", "items": item, "maxItems": 4}}, "required": ["calls"]}
menu = "\n".join(json.dumps(t["function"]) for t in TOOLS)
proc = subprocess.Popen([SERVER, "-m", MODEL, "--port", str(PORT), "-ngl", "99", "-c", "8192", "--jinja", "--no-webui"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
try:
    while True:
        try: urllib.request.urlopen(URL + "/health", timeout=2); break
        except Exception: time.sleep(0.5)
    ok = 0
    for req, want in CASES:
        prompt = f"Tools:\n{menu}\n\nRequest: {req}\n\nReturn the tool calls needed, in order. If no tool fits, return an empty calls list."
        body = {"messages": [{"role": "user", "content": prompt}], "temperature": 0, "seed": 42, "max_tokens": 300,
                "chat_template_kwargs": {"enable_thinking": False}, "response_format": {"type": "json_schema", "json_schema": {"schema": schema}}}
        t = time.perf_counter(); r = post("/v1/chat/completions", body); ms = round((time.perf_counter() - t) * 1000)
        got = [(c["name"], c["arguments"]) for c in json.loads(r["choices"][0]["message"]["content"])["calls"]]
        good = len(got) == len(want) and all(g[0] == w[0] and all(v.lower() in str(g[1].get(k, "")).lower() for k, v in w[1].items()) for g, w in zip(got, want))
        ok += good
        print(f"{'PASS' if good else 'FAIL'} {ms:5d} ms  {req[:50]:50s} -> {got}")
    print(f"score {ok}/{len(CASES)}")
finally:
    proc.terminate(); proc.wait(timeout=20)
