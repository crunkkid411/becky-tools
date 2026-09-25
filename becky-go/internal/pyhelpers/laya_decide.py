"""laya_decide.py - run Laya (open-source, Jev-compatible System One decision model) via ONNX Runtime.

Port of receptron/laya src/sequence.ts + src/laya.ts (MIT), which is itself a port of the checkpoint's
rl_common.py / rl_agent_api.py. Laya never writes text: it reads a state plus typed questions
(choice / score / noul) and returns calibrated probabilities in ONE forward pass.

stdin : {"state": <str|obj>, "questions": {qid: {"type","instructions","criteria"}}}
        or {"requests": [<that>, ...]} to answer many states with one model load
stdout: {"model":"laya","answers":{...},"usage":{...}}   (TypeSafe Jev system_one shape)
        or {"responses": [...]} for a batch
argv  : --model-dir DIR  [--cpu]
The DirectML provider fails on this graph's Reshape node (tested 2026-09-25), so the Go side
passes --cpu; CPU is ~0.5 s per 3-question call and uses no VRAM.
"""
import argparse
import json
import math
import os
import sys

import numpy as np
import onnxruntime as ort
from tokenizers import Tokenizer

QTYPES = {"choice": 0, "score": 1, "noul": 2}
QNAMES = ["choice", "score", "noul"]


def py_dumps(v):
    # Python's json.dumps(ensure_ascii=False) is what the model was trained on.
    return v if isinstance(v, str) else json.dumps(v, ensure_ascii=False)


def to_internal(q):
    crit = q.get("criteria")
    if q["type"] == "choice" and isinstance(crit, list):
        crit = {c: None for c in crit}
    ins = q["instructions"] if isinstance(q["instructions"], str) else json.dumps(q["instructions"])
    return {"t": q["type"], "ins": ins, "crit": crit}


def render_options(q):
    if q["t"] == "choice":
        return [f"{k}: {v}" if v else k for k, v in q["crit"].items()]
    if q["t"] == "score":
        return [f"level {i}: {c}" for i, c in enumerate(q["crit"])]
    c = q["crit"] or {}
    return ["false: " + (c.get("false") or "no, the statement does not hold"),
            "true: " + (c.get("true") or "yes, the statement holds")]


def size_bucket(k):
    return "2" if k <= 2 else "3-5" if k <= 5 else "6-10" if k <= 10 else "11+"


def confidence(p):
    k = len(p)
    if k < 2:
        return 1.0
    ent = -sum(x * math.log(max(x, 1e-12)) for x in p)
    return 1 - ent / math.log(k)


def softmax(z):
    z = np.asarray(z, dtype=np.float64)
    e = np.exp(z - z.max())
    return (e / e.sum()).tolist()


class Laya:
    def __init__(self, model_dir, cpu=False):
        with open(os.path.join(model_dir, "laya_config.json"), encoding="utf-8") as f:
            self.cfg = json.load(f)
        self.tok = Tokenizer.from_file(os.path.join(model_dir, "tokenizer", "tokenizer.json"))
        tid = self.tok.token_to_id
        self.cls, self.sep, self.mask, self.pad = tid("[CLS]"), tid("[SEP]"), tid("[MASK]"), tid("[PAD]")
        providers = ["CPUExecutionProvider"]
        if not cpu and "DmlExecutionProvider" in ort.get_available_providers():
            providers = ["DmlExecutionProvider", "CPUExecutionProvider"]
        so = ort.SessionOptions()
        so.log_severity_level = 3
        self.sess = ort.InferenceSession(os.path.join(model_dir, "laya.onnx"), so, providers=providers)
        self.provider = self.sess.get_providers()[0]

    def encode(self, text):
        return self.tok.encode(text, add_special_tokens=False).ids

    def build(self, state, q):
        # [CLS] <type> question: instructions [SEP] [MASK] opt0 [MASK] opt1 ... [SEP] state [SEP]
        max_len, head_max = self.cfg["max_len"], self.cfg["head_max_len"]
        scrub = lambda s: s.replace("[MASK]", " ")
        head = self.encode(f"{q['t']} question: {scrub(q['ins'])}")
        opts = [[self.mask] + self.encode(" " + scrub(o))[:48] for o in render_options(q)]
        budget = head_max - sum(map(len, opts))
        if budget < 16:
            per = max(4, (head_max - 16) // max(1, len(opts)))
            opts = [o[:per] for o in opts]
            budget = head_max - sum(map(len, opts))
        head = head[:max(8, budget)]
        seq = [self.cls] + head + [self.sep]
        markers = []
        for o in opts:
            markers.append(len(seq))
            seq += o
        seq.append(self.sep)
        room = max(0, max_len - len(seq) - 1)
        seq += self.encode(scrub(py_dumps(state)))[:room] + [self.sep]
        return seq[:max_len], [m for m in markers if m < max_len]

    def system_one(self, state, questions):
        if not questions:
            raise ValueError("at least one question is required")
        items = []
        for qid, raw in questions.items():
            q = to_internal(raw)
            ids, markers = self.build(state, q)
            if len(markers) != len(render_options(q)):
                raise ValueError(f"question {qid!r}: options do not fit in head_max_len={self.cfg['head_max_len']}")
            items.append((qid, q, ids, markers))
        n, L, K = len(items), max(len(i[2]) for i in items), max(len(i[3]) for i in items)
        input_ids = np.full((n, L), self.pad, dtype=np.int64)
        attn = np.zeros((n, L), dtype=np.int64)
        mpos = np.zeros((n, K), dtype=np.int64)
        mmask = np.zeros((n, K), dtype=bool)
        qtype = np.zeros((n,), dtype=np.int64)
        for r, (_, q, ids, markers) in enumerate(items):
            input_ids[r, :len(ids)] = ids
            attn[r, :len(ids)] = 1
            mpos[r, :len(markers)] = markers
            mmask[r, :len(markers)] = True
            qtype[r] = QTYPES[q["t"]]
        logits, act = self.sess.run(["logits", "act_probs"], {
            "input_ids": input_ids, "attention_mask": attn, "marker_pos": mpos,
            "marker_mask": mmask, "qtype": qtype})
        r4 = lambda x: round(float(x), 4)
        answers = {}
        for r, (qid, q, ids, markers) in enumerate(items):
            k = len(markers)
            qt = QTYPES[q["t"]]
            temp = self.cfg["temperature_by_options"].get(f"{QNAMES[qt]}:{size_bucket(k)}", self.cfg["temperature"][qt])
            p = softmax(logits[r, :k] / temp)
            ext = {"act_probability": r4(act[r, 0])}
            if q["t"] == "choice":
                keys = list(q["crit"].keys())
                best = int(np.argmax(p))
                answers[qid] = {"type": "choice", "choice": keys[best],
                                "probabilities": {kk: r4(p[i]) for i, kk in enumerate(keys)},
                                "confidence": r4(confidence(p)), "rl_agent": ext}
            elif q["t"] == "score":
                answers[qid] = {"type": "score", "score": r4(sum(i * v for i, v in enumerate(p))),
                                "legend": {str(i): c for i, c in enumerate(q["crit"])},
                                "probabilities": {str(i): r4(v) for i, v in enumerate(p)},
                                "confidence": r4(confidence(p)), "rl_agent": ext}
            else:
                answers[qid] = {"type": "noul", "noul": r4(p[1]), "rl_agent": ext}
        return {"model": "laya", "answers": answers,
                "usage": {"input_tokens": int(sum(len(i[2]) for i in items)), "output_tokens": 0}}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--model-dir", required=True)
    ap.add_argument("--cpu", action="store_true", help="skip the DirectML GPU provider")
    a = ap.parse_args()
    req = json.load(sys.stdin)
    laya = Laya(a.model_dir, cpu=a.cpu)
    if "requests" in req:  # batch: load the model once, answer many states
        out = {"responses": [laya.system_one(r["state"], r["questions"]) for r in req["requests"]]}
    else:
        out = laya.system_one(req["state"], req["questions"])
    out["provider"] = laya.provider
    json.dump(out, sys.stdout, ensure_ascii=False)


if __name__ == "__main__":
    main()
