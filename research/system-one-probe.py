# Usage: python research/system-one-probe.py <path-to-gguf> [port]
# Proof for research/system-one-models-jev.md section 5. Needs only C:\llama.cpp llama-server.
# Proof: a Jev-style "System One" decision from becky's EXISTING local llama-server.
# One token, read the option probabilities, no text generated. Stdlib only.
import json, math, subprocess, sys, time, urllib.request

SERVER = r"C:\llama.cpp\build\bin\llama-server.exe"
MODEL = sys.argv[1]
PORT = int(sys.argv[2]) if len(sys.argv) > 2 else 18477
URL = f"http://127.0.0.1:{PORT}"
LETTERS = "ABCDEFGHIJ"


def post(path, body):
    req = urllib.request.Request(URL + path, json.dumps(body).encode(), {"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=120) as r:
        return json.loads(r.read())


def choice(state, instructions, options):
    """options: {key: description}. Returns (choice, probs, confidence, ms)."""
    keys = list(options)
    lines = "\n".join(f"{LETTERS[i]}) {k}: {d}" for i, (k, d) in enumerate(options.items()))
    prompt = (f"State:\n{state}\n\nQuestion: {instructions}\nOptions:\n{lines}\n\n"
              f"Reply with the single letter of the best option.")
    body = {"messages": [{"role": "user", "content": prompt}],
            "max_tokens": 1, "temperature": 0, "logprobs": True, "top_logprobs": 20,
            "chat_template_kwargs": {"enable_thinking": False},
            # grammar pins the one token to a valid letter: the model cannot answer off-menu
            "grammar": "root ::= [" + LETTERS[:len(keys)] + "]"}
    t = time.perf_counter()
    r = post("/v1/chat/completions", body)
    ms = (time.perf_counter() - t) * 1000
    top = r["choices"][0]["logprobs"]["content"][0]["top_logprobs"]
    lp = {}
    for e in top:
        tok = e["token"].strip()
        if len(tok) == 1 and tok in LETTERS[:len(keys)] and tok not in lp:
            lp[tok] = e["logprob"]
    m = max(lp.values())
    z = sum(math.exp(v - m) for v in lp.values())
    probs = {keys[LETTERS.index(t)]: round(math.exp(v - m) / z, 3) for t, v in lp.items()}
    for k in keys:
        probs.setdefault(k, 0.0)
    best = max(probs, key=probs.get)
    # confidence = 1 - normalised entropy (same idea as Jev: a statistic of the distribution)
    h = -sum(p * math.log(p) for p in probs.values() if p > 0)
    conf = round(1 - h / math.log(len(keys)), 3)
    return best, probs, conf, round(ms)


def noul(state, instructions):
    best, probs, _, ms = choice(state, instructions, {"yes": "true", "no": "false"})
    return probs["yes"], ms


proc = subprocess.Popen([SERVER, "-m", MODEL, "--port", str(PORT), "-ngl", "99", "-c", "4096", "--no-webui"],
                        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
try:
    t0 = time.time()
    while True:
        try:
            with urllib.request.urlopen(URL + "/health", timeout=2) as r:
                if r.status == 200:
                    break
        except Exception:
            pass
        if time.time() - t0 > 180:
            sys.exit("server never came up")
        time.sleep(0.5)
    print(f"model loaded in {time.time()-t0:.1f}s: {MODEL.split(chr(92))[-1]}")
    choice("warm up", "Pick one.", {"a": None, "b": None})

    cases = [
        ("retake (should be YES)",
         'Line 41: "so the thing about the deputy is he never actually filed the"\n'
         'Line 42: "so the thing about the deputy is he never filed the report"',
         "Is Line 42 a retake of Line 41, meaning the speaker restarted and re-said the same sentence?"),
        ("not a retake (should be NO)",
         'Line 41: "so the thing about the deputy is he never filed the report"\n'
         'Line 42: "and that is why we went to the county clerk the next morning"',
         "Is Line 42 a retake of Line 41, meaning the speaker restarted and re-said the same sentence?"),
    ]
    for name, state, q in cases:
        p, ms = noul(state, q)
        print(f"NOUL  {name:30s} P(yes)={p:.3f}  {ms} ms")

    best, probs, conf, ms = choice(
        "Jordan says: hey becky, cut the dead air out of the fbi recap clips in my footage folder",
        "Which becky workflow should handle this request?",
        {"roughcut": "remove silence and retakes from raw footage",
         "transcribe": "produce a timestamped transcript",
         "shorts": "make vertical short clips from a finished video",
         "search": "find where something is said in footage",
         "none": "none of these fit"})
    print(f"CHOICE route -> {best} conf={conf} {probs} {ms} ms")

    best, probs, conf, ms = choice(
        "Jordan says: can you do the thing with the stuff from yesterday",
        "Which becky workflow should handle this request?",
        {"roughcut": "remove silence and retakes from raw footage",
         "transcribe": "produce a timestamped transcript",
         "shorts": "make vertical short clips from a finished video",
         "search": "find where something is said in footage",
         "none": "none of these fit"})
    print(f"CHOICE vague -> {best} conf={conf} {probs} {ms} ms")
finally:
    proc.terminate()
    proc.wait(timeout=20)
