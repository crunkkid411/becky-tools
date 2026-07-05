#!/usr/bin/env python
"""Trace dataset: load, lookup precedence, round-robin, playback selection (spec 3.4/5.1).
trace-dataset.json is authored by the traces phase; a missing file, missing wav,
or a {placeholder} line all fall through to live TTS."""
import json, os, re

HERE = os.path.dirname(os.path.abspath(__file__))


def find_repo():
    d = HERE
    for _ in range(9):
        if os.path.isdir(os.path.join(d, "becky-go")):
            return d
        nd = os.path.dirname(d)
        if nd == d:
            break
        d = nd
    return HERE


REPO = find_repo()
BIN = os.path.join(REPO, "becky-go", "bin")
TRACES_DIR = os.path.normpath(os.path.join(HERE, "..", "traces"))


def becky(tool):
    p = os.path.join(BIN, tool + ".exe")
    return p if os.path.exists(p) else tool


class TraceStore:
    def __init__(self, traces_dir=None):
        self.dir = traces_dir or TRACES_DIR
        self.rr_path = os.path.join(self.dir, "rr-state.json")
        self.by_id, self.by_match, self.by_line, self.rr = {}, {}, {}, {}
        self.load()

    def load(self):
        entries = []
        try:
            with open(os.path.join(self.dir, "trace-dataset.json"), encoding="utf-8") as f:
                txt = f.read()
            try:
                data = json.loads(txt)
            except ValueError:  # tolerate // comment lines (spec 5.1 example is jsonc)
                data = json.loads(re.sub(r"^\s*//.*$", "", txt, flags=re.M))
            entries = data.get("entries") or []
        except Exception:
            pass  # no dataset yet -> every lookup misses -> live TTS
        for e in entries:
            eid = e.get("id", "")
            if not eid or not e.get("lines"):
                continue
            self.by_id[eid] = e
            m = e.get("match") or {}
            if m.get("tool"):
                self.by_match[(m["tool"], m.get("outcome", "ok"))] = e
            for i, ln in enumerate(e["lines"]):
                self.by_line.setdefault(ln, (e, i))
        try:
            with open(self.rr_path, encoding="utf-8") as f:
                self.rr = json.load(f)
        except Exception:
            self.rr = {}

    def _save_rr(self):
        try:
            os.makedirs(self.dir, exist_ok=True)
            with open(self.rr_path, "w", encoding="utf-8") as f:
                json.dump(self.rr, f)
        except Exception:
            pass

    def _wav(self, eid, i):
        adir = os.path.join(self.dir, "audio", eid)
        # ponytail: generator (phase C) may number 0- or 1-based; detect per entry dir
        base = 0 if os.path.exists(os.path.join(adir, "0.wav")) else 1
        p = os.path.join(adir, "%d.wav" % (i + base))
        return p if os.path.exists(p) else None

    def _make(self, e, i, fmt):
        line = e["lines"][i]
        special = {"kind": "eye_reveal", "dur": 1.0} if e.get("special") else None
        if re.search(r"\{[A-Za-z_][^}]*\}", line):  # placeholder line -> always live TTS
            try:
                text = line.format(**(fmt or {}))
            except Exception:
                text = re.sub(r"\{[^}]*\}", "", line).strip()
            return {"text": text, "wav": None, "special": special}
        return {"text": line, "wav": self._wav(e.get("id", ""), i), "special": special}

    def _pick(self, e, fmt=None):
        eid, n = e.get("id", ""), len(e["lines"])
        i = int(self.rr.get(eid, 0)) % n
        self.rr[eid] = (i + 1) % n
        self._save_rr()
        return self._make(e, i, fmt)

    def pick_by_id(self, eid, fmt=None):
        e = self.by_id.get(eid)
        return self._pick(e, fmt) if e else None

    def pick_for_tool(self, tool, outcome, fmt=None):
        e = self.by_match.get((tool, outcome)) or self.by_id.get("tool.%s.%s" % (tool, outcome))
        return self._pick(e, fmt) if e else None

    def pick_for_text(self, text):
        hit = self.by_line.get(text)
        if not hit:
            return None
        e, i = hit
        return self._make(e, i, None)


def choose_speech(rep, store):
    """Spec 3.4 precedence: tool+outcome trace > exact-line trace > live TTS."""
    text = rep.get("text") or ""
    tool = rep.get("tool") or ""
    parts = (rep.get("clip") or "").split(".")
    pick = None
    if tool and len(parts) >= 3:  # clip = "<tool>.<outcome>.<n>" per cmd/becky-voice/main.go execute()
        pick = store.pick_for_tool(tool, parts[-2], {"short": text})
    if pick is None:
        pick = store.pick_for_text(text)
    if pick is None:
        pick = {"text": text, "wav": None, "special": None}
    return pick
