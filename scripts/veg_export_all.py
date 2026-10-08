"""veg_export_all.py - export every VEGAS project under a folder, read-only.

  python veg_export_all.py [--root X:\\Videos] [--out X:\\AI-2\\edit-learning-work\\vegs]

For each .veg (not .bak): SHA-256 the original, copy it into --out, run
vegas/BeckyDumpProject.cs on the COPY headless, then SHA-256 the original
again and record whether it is unchanged. VEGAS never sees the original
path. One project at a time; a project that hangs or crashes VEGAS is killed
after TIMEOUT and recorded, and the run moves on. Results: <out>\\index.jsonl
(one line per project; re-runs skip projects already exported).
"""
import argparse
import ctypes
import ctypes.wintypes as wt
import hashlib
import json
import os
import shutil
import subprocess
import time

VEGAS = r"C:\Program Files\VEGAS\VEGAS Pro 18.0\vegas180.exe"
SCRIPT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "vegas", "BeckyDumpProject.cs")
TIMEOUT = 240


def sha(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


user32 = ctypes.windll.user32
BM_CLICK = 0x00F5
WM_COMMAND, IDCANCEL = 0x0111, 2
ENUM = ctypes.WINFUNCTYPE(wt.BOOL, wt.HWND, wt.LPARAM)


def _text(h):
    buf = ctypes.create_unicode_buffer(512)
    user32.GetWindowTextW(h, buf, 512)
    return buf.value.replace("&", "")  # drop keyboard-shortcut marks ("Ignore &all ...")


def _children(h):
    out = []
    user32.EnumChildWindows(h, ENUM(lambda c, _: out.append(c) or True), 0)
    return out


def answer_missing_media(pid):
    """VEGAS asks about media it cannot find (old projects point at the external
    D: drive). Answer "Ignore all missing files and leave them offline": it only
    affects this read of the copy, nothing is saved. Returns any OTHER dialog's
    text so a stall can be reported."""
    tops = []
    user32.EnumWindows(ENUM(lambda h, _: tops.append(h) or True), 0)
    other = ""
    for h in tops:
        owner = wt.DWORD()
        user32.GetWindowThreadProcessId(h, ctypes.byref(owner))
        if owner.value != pid or not user32.IsWindowVisible(h):
            continue
        kids = {c: _text(c) for c in _children(h)}
        if _text(h) == "Search for Missing Files":  # opened by a wrong answer: back out
            user32.PostMessageW(h, WM_COMMAND, IDCANCEL, 0)
            continue
        if _text(h) != "VEGAS Pro 18.0":
            continue
        if any(t.startswith("Warning: An error occurred while loading") for t in kids.values()):
            for c, t in kids.items():  # a plug-in not installed here: VEGAS still opens the project
                if t == "OK":
                    user32.SendMessageW(c, BM_CLICK, 0, 0)
            continue
        if not any("could not be found" in t for t in kids.values()):
            other = " | ".join(t for t in kids.values() if t)[:300]
            continue
        for c, t in kids.items():
            if t.startswith("Ignore all missing files"):
                user32.SendMessageW(c, BM_CLICK, 0, 0)
        for c, t in kids.items():
            if t == "OK":
                user32.SendMessageW(c, BM_CLICK, 0, 0)
    return other


def kill_vegas(pid=None):
    """Close this run's VEGAS (and its crash reporter); never another VEGAS
    someone has open. With no pid (start of a run), nothing is killed."""
    if pid:
        subprocess.run(["taskkill", "/F", "/T", "/PID", str(pid)], capture_output=True)
        subprocess.run(["taskkill", "/F", "/IM", "ErrorReportClient.exe"], capture_output=True)


def export_one(src, out_dir):
    before = sha(src)
    copy = os.path.join(out_dir, before[:16] + ".veg")
    shutil.copyfile(src, copy)
    out_json = copy + ".edits.json"
    if os.path.exists(out_json):
        os.remove(out_json)  # a killed earlier run's file must not count as this run's export
    env = dict(os.environ, BECKY_DUMP_VEG=copy, BECKY_DUMP_OUT=out_json)
    t0 = time.time()
    proc = subprocess.Popen([VEGAS, "-SCRIPT:" + os.path.abspath(SCRIPT)], env=env)
    status, dialog = "timeout", ""
    while time.time() - t0 < TIMEOUT:
        dialog = answer_missing_media(proc.pid) or dialog
        if os.path.exists(out_json):
            status = "ok"
            break
        if proc.poll() is not None:
            status = "exited without output"
            break
        time.sleep(2)
    if status == "ok":
        try:
            proc.wait(timeout=60)  # let VEGAS exit by itself
        except subprocess.TimeoutExpired:
            pass
    kill_vegas(proc.pid)
    if status == "ok":
        with open(out_json, encoding="utf-8") as f:
            if "error" in json.load(f):
                status = "export error"
    return {"veg": src, "copy": copy, "status": status, "secs": round(time.time() - t0),
            "sha256": before, "original_unchanged": sha(src) == before,
            **({"dialog": dialog} if status != "ok" and dialog else {})}


def needs_keyframes(edits_path):
    """True when an export marks animated effect settings but has no keyframe values ("anim")."""
    with open(edits_path, encoding="utf-8") as f:
        text = f.read()
    return "[animated]" in text and '"anim"' not in text


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--root", default=r"X:\Videos")
    ap.add_argument("--out", default=r"X:\AI-2\edit-learning-work\vegs")
    ap.add_argument("--redo-animated", action="store_true",
                    help="re-export ok projects whose export has animated effects but no keyframe values (pre-2026-10-07 exporter)")
    a = ap.parse_args()
    os.makedirs(a.out, exist_ok=True)
    index = os.path.join(a.out, "index.jsonl")
    done = set()
    if os.path.exists(index):
        with open(index, encoding="utf-8") as f:
            done = {r["veg"]: r for r in map(json.loads, filter(str.strip, f)) if r["status"] == "ok"}
    if a.redo_animated:
        done = {v for v, r in done.items() if not needs_keyframes(r["copy"] + ".edits.json")}
    vegs = sorted(os.path.join(d, n) for d, _, files in os.walk(a.root) for n in files if n.lower().endswith(".veg"))
    for i, src in enumerate(vegs, 1):
        if src in done:
            continue
        row = export_one(src, a.out)
        with open(index, "a", encoding="utf-8") as f:
            f.write(json.dumps(row) + "\n")
        print(f"[{i}/{len(vegs)}] {row['status']:22s} unchanged={row['original_unchanged']} {src}", flush=True)


if __name__ == "__main__":
    main()
