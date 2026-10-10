"""Skill-routing test: can a System One model pick the right skill for a request?
usage: route_test.py <url> <label>   (url = local /v1/systemone server, or 'cli:<model>' to go through becky-decide)
Mode A: one choice over every skill + none. Mode B: one noul per skill, best wins, none if best < 0.5."""
import json, sys, time, subprocess, os
S = os.path.dirname(os.path.abspath(__file__))
cat = json.load(open(S + "/skill_catalog.json", encoding="utf-8"))
cases = json.load(open(S + "/skill_cases.json", encoding="utf-8"))
url, label = sys.argv[1], sys.argv[2]
NONE = "No skill: a plain answer, a quick fact, a thank-you, a yes/no reply, or a stop."
STATE = "A request Jordan typed to his AI coding assistant on his Windows PC."

def ask(req):
    t = time.time()
    if url.startswith("cli:"):
        out = subprocess.run([r"C:\Users\only1\bin\becky-decide.exe" if os.path.exists(r"C:\Users\only1\bin\becky-decide.exe") else "/tmp/bd.exe", "--model", url[4:]], input=json.dumps(req), capture_output=True, text=True, encoding="utf-8")
        a = json.loads(out.stdout)["answers"]
    else:
        import urllib.request
        a = json.load(urllib.request.urlopen(urllib.request.Request(url + "/v1/systemone", json.dumps(req).encode(), {"Content-Type": "application/json"}), timeout=600))["answers"]
    return a, time.time() - t

res = {"A": [], "B": []}
for c in cases:
    crit = {k: v or k for k, v in cat.items()}; crit["none"] = NONE
    a, dt = ask({"state": {"request": c["p"]}, "questions": {"skill": {"type": "choice", "instructions": "Which skill should the assistant load to handle `request`? Pick none when no skill is needed.", "criteria": crit}}})
    top = sorted(a["skill"]["probabilities"].items(), key=lambda x: -x[1])[:3]
    res["A"].append({"p": c["p"], "ok": c["ok"], "top": top, "secs": dt})
    qs = {f"s{i}": {"type": "noul", "instructions": {"question": "Is `skill` the right tool to handle `request`?", "request": c["p"], "skill": f"{k}: {v}"}} for i, (k, v) in enumerate(cat.items())}
    a, dt = ask({"state": STATE, "questions": qs})
    names = list(cat)
    sc = sorted(((names[int(k[1:])], v["noul"]) for k, v in a.items()), key=lambda x: -x[1])[:3]
    if sc[0][1] < 0.5: sc = [("none", 1 - sc[0][1])] + sc
    res["B"].append({"p": c["p"], "ok": c["ok"], "top": sc[:3], "secs": dt})
for m in "AB":
    r = res[m]
    t1 = sum(x["top"][0][0] in x["ok"] for x in r); t3 = sum(any(n in x["ok"] for n, _ in x["top"]) for x in r)
    nonecases = [x for x in r if x["ok"] == ["none"]]
    fp = sum(x["top"][0][0] != "none" for x in nonecases)
    print(f"{label} mode {m}: top1 {t1}/{len(r)}  top3 {t3}/{len(r)}  'none' cases wrongly given a skill {fp}/{len(nonecases)}  median {sorted(x['secs'] for x in r)[len(r)//2]:.2f}s/request")
json.dump(res, open(S + f"/route-{label}.json", "w", encoding="utf-8"), indent=1)
