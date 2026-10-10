"""Mode C: keyword (BM25) shortlist of 8 skills, then one System One choice among them + none.
usage: route_c.py <url> <label>"""
import json, sys, time, math, re, os, urllib.request, collections
S = os.path.dirname(os.path.abspath(__file__))
cat = json.load(open(S + "/skill_catalog.json", encoding="utf-8"))
cases = json.load(open(S + "/skill_cases.json", encoding="utf-8"))
url, label = sys.argv[1], sys.argv[2]
STOP = set("the a an and or to of for in on with is it this that my me i you your use when user users be by as at from are can do does".split())
tok = lambda s: [w for w in re.findall(r"[a-z0-9]+", s.lower()) if w not in STOP and len(w) > 1]
docs = {k: tok(k.replace("-", " ") + " " + v) for k, v in cat.items()}
N, avg = len(docs), sum(map(len, docs.values())) / len(docs)
df = collections.Counter(w for d in docs.values() for w in set(d))
def bm25(q):
    out = {}
    for k, d in docs.items():
        tf = collections.Counter(d); s = 0.0
        for w in set(tok(q)):
            if w in tf:
                idf = math.log(1 + (N - df[w] + .5) / (df[w] + .5))
                s += idf * tf[w] * 2.2 / (tf[w] + 1.2 * (.25 + .75 * len(d) / avg))
        out[k] = s
    return sorted(out, key=lambda k: -out[k])[:8], out
NONE = "No skill: a plain answer, a quick fact, a thank-you, a yes/no reply, or a stop."
rows = []
for c in cases:
    short, sc = bm25(c["p"])
    crit = {k: cat[k] or k for k in short}; crit["none"] = NONE
    req = {"state": {"request": c["p"]}, "questions": {"skill": {"type": "choice", "instructions": "Which skill should the assistant load to handle `request`? Pick none when no skill is needed.", "criteria": crit}}}
    t = time.time()
    if url.startswith("cli:"):
        import subprocess
        o = subprocess.run([S + "/becky-decide.exe", "--model", url[4:]], input=json.dumps(req), capture_output=True, text=True, encoding="utf-8")
        a = json.loads(o.stdout)["answers"]["skill"]
    else:
        a = json.load(urllib.request.urlopen(urllib.request.Request(url + "/v1/systemone", json.dumps(req).encode(), {"Content-Type": "application/json"}), timeout=300))["answers"]["skill"]
    rows.append({"p": c["p"], "ok": c["ok"], "short": short, "pick": a["choice"], "p_pick": a["probabilities"][a["choice"]], "conf": a.get("confidence"), "secs": time.time() - t, "in_short": any(k in c["ok"] for k in short)})
t1 = sum(r["pick"] in r["ok"] for r in rows)
reach = sum(r["in_short"] for r in rows if r["ok"] != ["none"])
none = [r for r in rows if r["ok"] == ["none"]]
sure = [r for r in rows if r["p_pick"] >= 0.8]
print(f"{label} mode C: right pick {t1}/{len(rows)}; shortlist held the answer {reach}/{len(rows)-len(none)}; none-cases right {sum(r['pick']=='none' for r in none)}/{len(none)}; picks at >=0.8: {len(sure)} of which right {sum(r['pick'] in r['ok'] for r in sure)}; median {sorted(r['secs'] for r in rows)[len(rows)//2]*1000:.0f} ms")
json.dump(rows, open(S + f"/routeC-{label}.json", "w", encoding="utf-8"), indent=1)
