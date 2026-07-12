#!/usr/bin/env python
"""Offline self-test (spec 3.6): no mic, no models, subprocess layer stubbed.
Run: python whoretana_voice.py --selftest   (exit 0 = all assertions pass)."""
import json, os, tempfile

import numpy as np

import brain_local
import traces
import whoretana_voice as wv

FAILS = []


def check(name, cond, detail=""):
    print(("PASS  " if cond else "FAIL  ") + name + (("  <- " + str(detail)) if detail and not cond else ""))
    if not cond:
        FAILS.append(name)


def wake_tests():
    cases = [  # 8-case accept/reject table incl. "cortana" fuzz (spec 3.6)
        ("whoretana", True), ("whoretana open notepad", True), ("cortana", True),
        ("hortana what's up", True), ("whore tana", True), ("who retana check this", True),
        ("banana", False), ("hey what time is it", False),
    ]
    for text, want in cases:
        got, _ = wv.wake_match(text)
        check("wake gate %r -> %s" % (text, want), got == want)
    _, payload = wv.wake_match("whoretana open my exports")
    check("wake payload strip", payload == "open my exports", payload)


def _trace_dir():
    d = tempfile.mkdtemp(prefix="whoretana_selftest_")
    ds = {"version": 1, "voice": "default", "entries": [
        {"id": "tool.footool.ok", "match": {"tool": "footool", "outcome": "ok"},
         "lines": ["Foo done.", "Foo is toast."]},
        {"id": "tool.footool.error", "match": {"tool": "footool", "outcome": "error"},
         "lines": ["FUCK. Foo fell over: {short}"]},
        {"id": "ack.generic", "lines": ["Okay."]},
        {"id": "special.greeting", "special": True, "lines": ["Hey. I'm here."]},
    ]}
    with open(os.path.join(d, "trace-dataset.json"), "w", encoding="utf-8") as f:
        json.dump(ds, f)
    for eid, n in (("tool.footool.ok", 2), ("ack.generic", 1), ("special.greeting", 1)):
        adir = os.path.join(d, "audio", eid)
        os.makedirs(adir)
        for i in range(n):
            open(os.path.join(adir, "%d.wav" % i), "wb").close()
    return d


def trace_tests():
    d = _trace_dir()
    st = traces.TraceStore(d)
    # precedence: tool+outcome beats exact-text beats TTS
    p = traces.choose_speech({"text": "Okay.", "tool": "footool", "clip": "footool.ok.7"}, st)
    check("precedence: tool+outcome first", p["text"].startswith("Foo") and p["wav"], p)
    p = traces.choose_speech({"text": "Okay."}, st)
    check("precedence: exact-text second", p["text"] == "Okay." and p["wav"], p)
    p = traces.choose_speech({"text": "some novel sentence"}, st)
    check("precedence: live TTS last", p["wav"] is None and p["text"] == "some novel sentence", p)
    # round-robin wraps (first pick above advanced footool.ok to index 1)
    seq = [st.pick_for_tool("footool", "ok")["text"] for _ in range(3)]
    check("round-robin wraps", seq == ["Foo is toast.", "Foo done.", "Foo is toast."], seq)
    # ...and persists via rr-state.json
    st2 = traces.TraceStore(d)
    check("round-robin persists", st2.pick_for_tool("footool", "ok")["text"] == "Foo done.")
    check("rr-state.json written", os.path.exists(os.path.join(d, "rr-state.json")))
    # placeholder lines always route to live TTS
    p = st.pick_for_tool("footool", "error", {"short": "exit 1"})
    check("placeholder routes to TTS", p["wav"] is None and "exit 1" in p["text"], p)
    # special entries surface the eye_reveal event payload
    p = st.pick_for_text("Hey. I'm here.")
    check("special flag surfaces", p["special"] == {"kind": "eye_reveal", "dur": 1.0}, p)
    # missing dataset dir is harmless (traces phase runs in parallel)
    st3 = traces.TraceStore(os.path.join(d, "nope"))
    check("missing dataset -> TTS fallthrough",
          st3.pick_by_id("ack.generic") is None and st3.pick_for_text("x") is None)


def _seq_chat(script):
    it = iter(script)
    return lambda messages, tools: next(it)


def _final(text):
    return {"choices": [{"message": {"role": "assistant", "content": text}}]}


def _call(name, arguments):
    return {"choices": [{"message": {"role": "assistant", "content": None, "tool_calls": [
        {"id": "1", "type": "function",
         "function": {"name": name, "arguments": arguments}}]}}]}


def escalation_tests():
    nm = {"type": "error", "action": "none", "text": brain_local.NOMATCH_TEXT}
    check("no-match detected", brain_local.is_no_match(nm))
    check("result is not no-match", not brain_local.is_no_match({"type": "result", "text": "Done."}))
    check("other errors not no-match",
          not brain_local.is_no_match({"type": "error", "text": "Nothing pending to confirm."}))
    wv.state["local_escalation"] = True
    check("no-match triggers escalation decision", wv.should_escalate(nm))
    wv.state["local_escalation"] = False
    check("escalation gated by setting", not wv.should_escalate(nm))
    wv.state["local_escalation"] = True

    # stub the subprocess layer: catalog + tool exec + the llama call itself
    brain_local.load_catalog = lambda: [
        {"verb": "becky-transcribe", "summary": "s", "tier": "green", "pack": "default"},
        {"verb": "becky-nuke", "summary": "s", "tier": "yellow", "pack": "default"}]
    brain_local.run_tool = lambda name, args, meta: ("stub says hi", None)

    rep = brain_local.escalate("transcribe it", chat_fn=_seq_chat([
        _call("becky_transcribe", '{"args":"clip.mp4"}'), _final("All transcribed.")]))
    check("tool loop reaches answer", rep["text"] == "All transcribed.", rep)

    rep = brain_local.escalate("loop forever", chat_fn=lambda m, t: _call("becky_transcribe", "{}"))
    check("tool loop capped at 3 rounds", "tool budget" in rep["text"], rep)

    rep = brain_local.escalate("nuke it", chat_fn=lambda m, t: _call("becky_nuke", "{}"))
    check("yellow tool bounces to confirm",
          rep.get("need_confirm") is True and rep.get("tier") == "yellow", rep)
    check("bounce stores pending call", brain_local.has_pending())
    rep = brain_local.escalate("yes", confirm=True, chat_fn=_seq_chat([_final("Nuked it.")]))
    check("confirm resumes stored call",
          rep["text"] == "Nuked it." and not brain_local.has_pending(), rep)
    rep = brain_local.escalate("yes", confirm=True, chat_fn=_seq_chat([_final("fresh chat")]))
    check("confirm without pending starts fresh", rep["text"] == "fresh chat", rep)

    rep = brain_local.escalate("click it", chat_fn=lambda m, t: _call("desktop_click", '{"x":5,"y":5}'))
    check("desktop gated without hands_free", rep.get("need_confirm") is True, rep)

    rep = brain_local.escalate("click it", hands_free=True, chat_fn=_seq_chat([
        _call("desktop_click", '{"x":5,"y":5}'), _final("clicked")]))
    check("hands_free executes desktop tool", rep["text"] == "clicked", rep)


def confirm_flow_tests():
    check("yes-word detected", wv._is_yes("Yes.") and wv._is_yes("do it") and not wv._is_yes("yesterday"))
    old_route = wv.route
    wv.route = lambda text, confirm=False: {"text": "%s|%s" % (text, confirm),
                                            "tier": "green", "action": "none"}
    wv._confirm["kind"], wv._confirm["text"] = "router", "nuke the drive"
    rep = wv._resume_confirm()
    wv.route = old_route
    check("router confirm re-routes original text", rep["text"] == "nuke the drive|True", rep)
    check("confirm consumed after resume", wv._confirm["kind"] is None)


def viseme_tests():
    sr = 16000
    t = np.arange(int(sr * 0.05)) / sr
    chunk = (0.4 * np.sin(2 * np.pi * 220 * t)
             + 0.05 * np.random.RandomState(7).randn(len(t))).astype(np.float32)
    v = wv.visemes(chunk, sr)
    check("viseme values all in 0..1",
          all(0.0 <= v[k] <= 1.0 for k in ("jaw", "funnel", "pucker", "smile")), v)
    check("viseme jaw active on sine", v["jaw"] > 0.2, v)
    s = wv.visemes(np.zeros(800, dtype=np.float32), sr)
    check("viseme silence -> jaw 0", s["jaw"] == 0.0 and s["funnel"] == 0.0, s)


def run():
    wake_tests()
    trace_tests()
    escalation_tests()
    confirm_flow_tests()
    viseme_tests()
    print("SELFTEST: %s (%d checks failed)" % ("FAIL" if FAILS else "ALL PASS", len(FAILS)))
    return 1 if FAILS else 0
