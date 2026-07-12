"""Pre-render trace-dataset.json lines to WAV via becky-tts (BUILD-SPEC 5.2).

Usage:
  python generate_trace_audio.py             # render everything missing (idempotent)
  python generate_trace_audio.py --only ack. # only entries whose id starts with prefix
  python generate_trace_audio.py --check     # validate dataset, no rendering, exit 0/1
"""
import json
import re
import subprocess
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
DATASET = HERE / "trace-dataset.json"
AUDIO = HERE / "audio"
REPO = HERE.parents[2]  # gui/Whoretana/traces -> becky-tools
CATALOG_EXE = REPO / "becky-go" / "bin" / "becky-catalog.exe"
TTS_EXE = REPO / "becky-go" / "bin" / "becky-tts.exe"

# desktop pseudo-tools live in the sidecar (spec D16), not the catalog
PSEUDO_TOOLS = {"screenshot", "desktop_click", "desktop_type", "desktop_press", "desktop_move"}
OUTCOMES = {"ok", "partial", "error"}
PLACEHOLDER = re.compile(r"\{[a-z_]+\}")
TTS_TIMEOUT_S = 240  # measured ~29 s/line on this box; huge margin


def load_dataset():
    with open(DATASET, encoding="utf-8") as f:
        return json.load(f)


def catalog_verbs():
    out = subprocess.run([str(CATALOG_EXE), "--json"], capture_output=True, text=True,
                         timeout=30, check=True).stdout
    cat = json.loads(out)
    return {t["verb"] for t in cat.get("tools", []) + cat.get("ops", [])}


def safe_dir(entry_id):
    # ids are filesystem-safe by convention; belt-and-braces for future edits
    return re.sub(r"[^A-Za-z0-9._-]", "_", entry_id)


def check(data):
    errors = []
    ids = set()
    try:
        verbs = catalog_verbs() | PSEUDO_TOOLS
    except Exception as e:
        errors.append(f"becky-catalog --json failed: {e}")
        verbs = None
    for entry in data.get("entries", []):
        eid = entry.get("id")
        if not eid or not isinstance(eid, str):
            errors.append(f"entry missing id: {entry}")
            continue
        if eid in ids:
            errors.append(f"duplicate id: {eid}")
        ids.add(eid)
        lines = entry.get("lines")
        if not isinstance(lines, list) or not lines:
            errors.append(f"{eid}: lines missing or empty")
            continue
        for i, line in enumerate(lines):
            if not isinstance(line, str) or not line.strip():
                errors.append(f"{eid}: line {i} empty or not a string")
        match = entry.get("match")
        if match is not None:
            tool, outcome = match.get("tool"), match.get("outcome")
            if verbs is not None and tool not in verbs:
                errors.append(f"{eid}: match.tool '{tool}' not in catalog or pseudo-tools")
            if outcome not in OUTCOMES:
                errors.append(f"{eid}: match.outcome '{outcome}' not in {sorted(OUTCOMES)}")
            if outcome == "error":
                for i, line in enumerate(lines):
                    if isinstance(line, str) and "{short}" not in line:
                        errors.append(f"{eid}: error line {i} missing {{short}}")
    if not data.get("entries"):
        errors.append("dataset has no entries")
    for e in errors:
        print(f"CHECK FAIL: {e}")
    n = len(data.get("entries", []))
    print(f"check: {n} entries, {len(ids)} unique ids, {len(errors)} errors")
    return len(errors) == 0


def render_line(line, voice, out_wav):
    tmp_txt = out_wav.with_suffix(".txt")
    tmp_txt.write_text(line, encoding="utf-8")
    detail = ""
    try:
        proc = subprocess.run(
            [str(TTS_EXE), "--in", str(tmp_txt), "--out", str(out_wav),
             "--voice", voice, "--json"],
            capture_output=True, text=True, timeout=TTS_TIMEOUT_S)
        ok = proc.returncode == 0 and out_wav.exists() and out_wav.stat().st_size > 0
        if not ok:
            detail = (proc.stderr or proc.stdout or "").strip()[:200]
    except subprocess.TimeoutExpired:
        ok, detail = False, "timeout"
    finally:
        tmp_txt.unlink(missing_ok=True)
    if not ok:
        out_wav.unlink(missing_ok=True)
    return ok, detail


def render(data, only=None):
    default_voice = data.get("voice", "default")
    rendered = skipped = placeholders = failed = 0
    for entry in data.get("entries", []):
        eid = entry["id"]
        if only and not eid.startswith(only):
            continue
        voice = entry.get("voice", default_voice)
        out_dir = AUDIO / safe_dir(eid)
        for i, line in enumerate(entry["lines"]):
            if PLACEHOLDER.search(line):
                placeholders += 1
                continue
            out_wav = out_dir / f"{i}.wav"
            if out_wav.exists() and out_wav.stat().st_size > 0:
                skipped += 1
                continue
            out_dir.mkdir(parents=True, exist_ok=True)
            ok, detail = render_line(line, voice, out_wav)
            if ok:
                rendered += 1
                print(f"rendered {eid}/{i}.wav", flush=True)
            else:
                failed += 1
                print(f"FAILED {eid}/{i}: {detail}", flush=True)
    print(f"done: rendered={rendered} existing={skipped} placeholder-skipped={placeholders} failed={failed}")
    return failed == 0


def main(argv):
    data = load_dataset()
    if "--check" in argv:
        return 0 if check(data) else 1
    only = None
    if "--only" in argv:
        idx = argv.index("--only")
        if idx + 1 >= len(argv):
            print("--only requires a prefix")
            return 2
        only = argv[idx + 1]
    return 0 if render(data, only) else 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
