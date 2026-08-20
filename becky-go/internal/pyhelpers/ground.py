#!/usr/bin/env python3
"""becky grounding helper: WHERE in the frame is the thing I named?

Every framing signal becky has is a PERSON detector - MediaPipe Pose,
InsightFace, LR-ASD. That is enough right up until the shot has no person in it,
and then the crop path has nothing to aim at and honestly refuses.

Jordan's own edit says that case is not rare and is not skippable. Measured over
all 915 frames of his vertical short (research/jordan-edit-reverse-engineered.md):
8.3% contain no face at all, and shot 19 holds 1.27 seconds on a POINTING FINGER
with nobody's face in frame, because the gesture is the joke. His rubber-snake
prank is the same shape - "the clip itself is obviously meant to be the focal
point" on a POV shot where no faces are visible.

So this helper answers the one question a face detector cannot: "where is the
rubber snake / the pointing hand / the plate", as a box the existing crop path
can already consume.

MODEL: Reka Edge 2603 (7B, image/video+text, grounded detection via a literal
`Detect: <things>` instruction). Verified on this machine 2026-08-19: 6496 MiB
VRAM at Q4_K_M + mmproj-Q8_0, a 1920x1080 frame answered in 2.3s, six 640px
frames in one request for 450 prompt tokens. llama.cpp build 9551 already
carries the yasa2 vision tensors, so no rebuild was needed.

TRAPS, both found by running it:
  * The chat template ships thinking=1. Free-form answers come back as
    meta-commentary because the real answer went into the thinking block, while
    `Detect:` keeps working - exactly the kind of half-broken that ships. Start
    llama-server with reasoning disabled.
  * The output has MISMATCHED TAGS. Verbatim:
        <answer><ref>person</ref><bbox>18,05,72,100</answer><ref>microphone</ref><bbox>49,69,54,76</bbox>
    the first <bbox> is closed by </answer>. Parse it with a tolerant regex,
    never an XML parser.
  * Coordinates are PERCENTAGES of the frame, not pixels.

ACCURACY, measured on his footage and stated plainly: the person box is tight
and correct; a small prop (a clip-on microphone) landed near but not on it. Trust
it to find the subject, corroborate it before trusting it to find an object.

Output (stdout, one JSON line):
  {"ok": true, "target": "...", "fps": 4.0, "frames": 12,
   "detections": [{"t": 1.25, "boxes": [{"label": "pointing hand",
                                         "x": 0.49, "y": 0.69, "w": 0.05, "h": 0.07}]}]}
x/y/w/h are FRACTIONS of frame width/height, which is what internal/crop wants.
On any failure prints {"ok": false, "reason": "..."} and exits 0, so the Go
caller surfaces a note instead of a stack trace.
"""
import argparse
import base64
import json
import os
import re
import subprocess
import sys
import tempfile
import urllib.error
import urllib.request

# THE TAGS ARE NOT XML AND ARE NOT EVEN CONSISTENT. Four real replies to the
# SAME frame, differing only in the noun asked for (measured 2026-08-20):
#
#   <answer>Platinum blonde hair</ref><bbox>28,12,81,100</answer>
#   <answer>person in the center</ref><bbox>28,12,81,100</answer>
#   <answer>\n<list>\n<item>person ...</ref><bbox>28,11,81,100</bbox>\n</list>
#   <ref>person</ref><bbox>18,05,72,100</answer><ref>microphone</ref><bbox>...
#
# Note the first three have NO OPENING <ref> at all - the label sits directly
# after <answer> or <item> and is closed by </ref>. A pattern anchored on <ref>
# matched none of them, so becky reported "nothing grounded" on a frame holding
# three people and REFUSED the short over it.
#
# So the anchor is <bbox> and its four numbers, which every variant does emit.
# The LABEL is whatever text precedes it since the last tag - best effort, and
# never load-bearing: the crop needs the box, the label is only for the note.
BOX_RE = re.compile(r"([^<>]*?)\s*(?:</ref>|<bbox>)\s*<?/?b?box?>?\s*"
                    r"([0-9]{1,3}(?:\s*,\s*[0-9]{1,3}){3})")

# A fifth reply shape, and the one that matters most: NO tags and NO label at
# all, just semicolon-separated quadruples.
#
#   <answer>00,00,21,100;34,07,94,100</answer>
#
# That is the TWO-SHOT answer - two people, both boxed - so losing it loses
# exactly the case where framing is hardest. Only tried when the tagged pattern
# above finds nothing, so a labelled reply is never re-parsed by the looser rule.
BARE_BOX_RE = re.compile(r"\b([0-9]{1,3}(?:\s*,\s*[0-9]{1,3}){3})\b")


def parse_boxes(text):
    """Pull every (label, box) out of a Reka detection reply.

    Boxes are returned as fractions. A degenerate or inverted box is dropped
    rather than passed on - a zero-area crop target is worse than none.
    """
    found = BOX_RE.findall(text or "")
    if not found:
        found = [("", nums) for nums in BARE_BOX_RE.findall(text or "")]
    out = []
    for label, nums in found:
        try:
            x1, y1, x2, y2 = (float(v.strip()) / 100.0 for v in nums.split(","))
        except ValueError:
            continue
        if x2 <= x1 or y2 <= y1:
            continue
        x1, y1 = max(0.0, x1), max(0.0, y1)
        x2, y2 = min(1.0, x2), min(1.0, y2)
        if x2 <= x1 or y2 <= y1:
            continue
        out.append({"label": label.strip(), "x": x1, "y": y1, "w": x2 - x1, "h": y2 - y1})
    return out


def stability(dets):
    """How much do the boxes AGREE with each other across the window?

    MEASURED on the real footage this helper was written for - the pointing-hand
    shot in the BLINDFOLD master, 47.9-49.3s at 3fps: two of four frames returned
    a box, one landed roughly on the hand and one landed on empty counter at the
    opposite corner of the frame. Per-frame grounding of a SMALL, non-person
    target is not trustworthy.

    So this reports the disagreement rather than smoothing it away. becky's rule
    (FORENSIC-OUTPUT-PHILOSOPHY.md) is that a lone weak signal is a candidate,
    never a conclusion, and a caller handed a jumpy path with no warning would
    aim a camera at empty room - which is the exact bug the crop path already
    shipped once.

    Returns (found_frac, median_jump, stable). median_jump is centre-to-centre
    movement between consecutive sightings, as a fraction of the frame diagonal.
    """
    seen = [d for d in dets if d["boxes"]]
    found = len(seen) / len(dets) if dets else 0.0
    jumps = []
    for a, b in zip(seen, seen[1:]):
        ax, ay = a["boxes"][0]["x"] + a["boxes"][0]["w"] / 2, a["boxes"][0]["y"] + a["boxes"][0]["h"] / 2
        bx, by = b["boxes"][0]["x"] + b["boxes"][0]["w"] / 2, b["boxes"][0]["y"] + b["boxes"][0]["h"] / 2
        jumps.append(((ax - bx) ** 2 + (ay - by) ** 2) ** 0.5)
    jumps.sort()
    med = jumps[len(jumps) // 2] if jumps else 0.0
    # A real subject does not cross a quarter of the frame between samples, and a
    # target seen in under half the frames was never really tracked.
    return found, med, bool(len(seen) >= 2 and found >= 0.5 and med <= 0.25)


def extract_frames(ffmpeg, video, start, end, fps, outdir):
    """Decode the window ONCE, sequentially. Per-frame seeking on a long source
    was measured at 42s against 6s for one sequential decode (asd.py's lesson)."""
    cmd = [ffmpeg, "-y", "-v", "error", "-ss", f"{start:.6f}"]
    if end > start:
        cmd += ["-t", f"{end - start:.6f}"]
    cmd += ["-i", video, "-vf", f"fps={fps},scale=640:-2",
            os.path.join(outdir, "f_%05d.jpg")]
    subprocess.run(cmd, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
    return sorted(os.path.join(outdir, f) for f in os.listdir(outdir) if f.startswith("f_"))


def _post(server, image_path, prompt, timeout, max_tokens=300):
    with open(image_path, "rb") as fh:
        b64 = base64.b64encode(fh.read()).decode()
    body = json.dumps({
        "model": "reka",
        "messages": [{"role": "user", "content": [
            {"type": "image_url", "image_url": {"url": "data:image/jpeg;base64," + b64}},
            {"type": "text", "text": prompt},
        ]}],
        "max_tokens": max_tokens,
        "temperature": 0,
    }).encode()
    req = urllib.request.Request(server.rstrip("/") + "/v1/chat/completions", data=body,
                                 headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        out = json.loads(resp.read())
    return out["choices"][0]["message"].get("content") or ""


def ask(server, image_path, target, timeout):
    return _post(server, image_path, "Detect: " + target, timeout)


# Answers that are true of any frame and therefore ground nothing. Reka reaches
# for these when it cannot see a subject, and detecting them returns a box
# around the whole frame - which is a centre crop wearing a disguise, the exact
# thing this helper exists to stop.
_USELESS_SUBJECTS = {
    "the scene", "scene", "the image", "image", "the video", "video",
    "the frame", "frame", "the background", "background", "the room", "room",
    "nothing", "unknown", "the picture", "picture", "everything",
}


def name_subject(server, image_path, timeout):
    """Ask WHAT the viewer is meant to look at, as a short noun phrase.

    This is the half a person detector cannot do. It runs only when `Detect:
    person` came back empty, so its answer is by construction about a shot with
    no person in it - a prop, a plate, a hand, a screen.

    Returns "" when the answer is too generic to ground (see _USELESS_SUBJECTS);
    the caller must then REFUSE the span rather than fall back to centre.
    """
    txt = _post(server, image_path,
                "In one short noun phrase of at most four words, name the single object "
                "the viewer is meant to look at in this frame. Name a concrete physical "
                "thing, not the scene as a whole. Reply with the noun phrase only.",
                timeout, max_tokens=40)
    # The model wraps free-form answers in tags AND opens with a preamble even
    # when told not to. Measured: "The answer is silver knife" came back and was
    # then fed to `Detect:` verbatim, which of course found nothing. Strip both.
    txt = re.sub(r"</?[a-z_]+>", " ", txt, flags=re.I)
    txt = " ".join(txt.split())
    txt = re.sub(r"^(the\s+answer\s+is|the\s+viewer\s+is\s+meant\s+to\s+look\s+at|"
                 r"this\s+is|it\s+is|i\s+see|the\s+object\s+is|answer:)\s*[:\-]?\s*",
                 "", txt, flags=re.I)
    txt = re.sub(r"^(a|an|the)\s+", "", txt, flags=re.I)
    txt = txt.strip().strip('."\'' + "“”").strip()
    if not txt or len(txt.split()) > 6:
        return ""
    if txt.lower() in _USELESS_SUBJECTS:
        return ""
    return txt


def selftest():
    """Offline proof: no model, no media, no network."""
    checks = []

    def ck(name, ok, detail=""):
        checks.append((name, ok, detail))

    # The real, mismatched-tag reply this helper was written against.
    real = ("<answer>\n<ref>person</ref><bbox>18,05,72,100</answer>"
            "<ref>microphone</ref><bbox>49,69,54,76</bbox>")
    b = parse_boxes(real)
    ck("parses the real reply despite </answer> closing the first <bbox>", len(b) == 2,
       f"got {len(b)}")

    # THE REPLY SHAPES THAT SHIPPED A BUG. Reka answers the SAME frame in at
    # least four formats depending on the noun asked for, and three of them have
    # NO OPENING <ref> - the label is closed by </ref> having never been opened.
    # A pattern anchored on <ref> matched none of them, so becky reported
    # "nothing grounded" on a frame holding three people and refused the short.
    for name, reply, want in [
        ("bare label closed by </ref>",
         "<answer>Platinum blonde hair</ref><bbox>28,12,81,100</answer>", 1),
        ("label wrapped in <list>/<item>",
         "<answer><list><item>person</ref><bbox>28,11,81,100</bbox></list>", 1),
        ("untagged semicolon-separated pair (the TWO-SHOT answer)",
         "<answer>00,00,21,100;34,07,94,100</answer>", 2),
    ]:
        got = parse_boxes(reply)
        ck("parses " + name, len(got) == want, f"got {len(got)}, want {want}: {got}")

    # Prose with no coordinates must stay EMPTY - a tolerant parser that starts
    # inventing boxes from sentence text is worse than one that misses.
    ck("prose with no coordinates yields no boxes",
       parse_boxes("<answer>person in the center; person on the left</assistant>") == [], "")

    ck("labels survive", [x["label"] for x in b] == ["person", "microphone"],
       str([x["label"] for x in b]))
    ck("coordinates are fractions, not percentages",
       b and abs(b[0]["x"] - 0.18) < 1e-9 and abs(b[0]["w"] - 0.54) < 1e-9,
       str(b[0] if b else None))
    ck("the microphone box is the small one",
       len(b) == 2 and b[1]["w"] < 0.1 and b[1]["h"] < 0.1, str(b[1] if len(b) == 2 else None))
    ck("an inverted box is dropped, not passed on",
       parse_boxes("<ref>x</ref><bbox>80,80,20,20</bbox>") == [], "")
    ck("a zero-area box is dropped", parse_boxes("<ref>x</ref><bbox>10,10,10,10</bbox>") == [], "")
    ck("out-of-range coordinates are clamped into the frame",
       parse_boxes("<ref>x</ref><bbox>0,0,150,150</bbox>")[0]["w"] == 1.0, "")
    ck("no detection is an empty list, not a crash", parse_boxes("nothing here") == [], "")
    ck("empty input is survivable", parse_boxes("") == [] and parse_boxes(None) == [], "")

    def det(t, box):
        return {"t": t, "boxes": ([box] if box else [])}

    steady = [det(0, {"x": .4, "y": .4, "w": .1, "h": .1}),
              det(1, {"x": .42, "y": .41, "w": .1, "h": .1}),
              det(2, {"x": .43, "y": .42, "w": .1, "h": .1})]
    ck("a target that holds still is STABLE", stability(steady)[2], str(stability(steady)))

    # The real measurement: one box roughly on the hand, one on empty counter at
    # the opposite corner, and two frames with nothing at all.
    real_run = [det(0, None), det(1, None),
                det(2, {"x": .21, "y": .33, "w": .11, "h": .18}),
                det(3, {"x": .06, "y": .77, "w": .28, "h": .23})]
    ck("the real pointing-hand run is reported UNSTABLE", not stability(real_run)[2],
       str(stability(real_run)))
    ck("a target found in under half the frames is unstable",
       not stability([det(0, None), det(1, None), det(2, None),
                      det(3, {"x": .4, "y": .4, "w": .1, "h": .1})])[2], "")
    ck("a single sighting is never called stable",
       not stability([det(0, {"x": .4, "y": .4, "w": .1, "h": .1})])[2], "")

    print("ground.py --selftest (offline; no model, no media, no network)")
    ok = 0
    for name, good, detail in checks:
        print(f"  {'PASS' if good else 'FAIL'}  {name}" + (f"   [{detail}]" if not good else ""))
        ok += bool(good)
    print(f"\n{ok}/{len(checks)} PASS")
    return 0 if ok == len(checks) else 1


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--video")
    ap.add_argument("--target", help='what to find, plain English: "pointing hand, plate"')
    ap.add_argument("--server", default="http://127.0.0.1:8099",
                    help="a running llama-server holding Reka Edge (start it with reasoning OFF)")
    ap.add_argument("--start", type=float, default=0.0)
    ap.add_argument("--end", type=float, default=0.0)
    ap.add_argument("--fps", type=float, default=4.0,
                    help="frames per second to ground; grounding is the expensive step, so this "
                         "is deliberately coarser than tracking")
    ap.add_argument("--ffmpeg", default="ffmpeg")
    ap.add_argument("--timeout", type=float, default=120.0)
    ap.add_argument("--selftest", action="store_true")
    args = ap.parse_args()

    if args.selftest:
        return selftest()
    if not args.video:
        print(json.dumps({"ok": False, "reason": "--video is required"}))
        return 0

    try:
        with tempfile.TemporaryDirectory(prefix="becky-ground-") as tmp:
            frames = extract_frames(args.ffmpeg, args.video, args.start, args.end, args.fps, tmp)
            if not frames:
                print(json.dumps({"ok": False, "reason": "no frames decoded from that window"}))
                return 0

            # SELF-ORCHESTRATION: with no --target, find the target first.
            # `Detect: person` covers the overwhelmingly common case in one
            # call; only when a shot genuinely has nobody in it do we spend a
            # second call asking what it is ABOUT. That ordering matters -
            # asking the open question first invites "the scene" on a shot with
            # an obvious person in it.
            target = args.target
            named = ""
            if not target:
                target = "person"
                probe = frames[len(frames) // 2]
                try:
                    if not parse_boxes(ask(args.server, probe, "person", args.timeout)):
                        named = name_subject(args.server, probe, args.timeout)
                        if not named:
                            print(json.dumps({
                                "ok": False,
                                "reason": "no person in this shot and no nameable subject either - "
                                          "REFUSING rather than returning a centre crop"}))
                            return 0
                        target = named
                except (urllib.error.URLError, OSError) as e:
                    print(json.dumps({"ok": False,
                                      "reason": f"grounding server unreachable at {args.server}: {e}"}))
                    return 0
            args.target = target

            dets = []
            for i, f in enumerate(frames):
                try:
                    boxes = parse_boxes(ask(args.server, f, args.target, args.timeout))
                except (urllib.error.URLError, OSError) as e:
                    print(json.dumps({"ok": False,
                                      "reason": f"grounding server unreachable at {args.server}: {e}"}))
                    return 0
                dets.append({"t": round(args.start + i / args.fps, 4), "boxes": boxes})
        found, med, stable = stability(dets)
        print(json.dumps({"ok": True, "target": args.target, "named": named, "fps": args.fps,
                          "frames": len(dets), "found_frac": round(found, 3),
                          "median_jump": round(med, 3), "stable": stable,
                          "note": ("" if stable else
                                   "boxes disagree across the window - treat as a HINT about which "
                                   "region matters, not as a camera path"),
                          "detections": dets}))
    except subprocess.CalledProcessError as e:
        print(json.dumps({"ok": False, "reason": f"ffmpeg failed: {e.stderr.decode('utf-8', 'replace')[-400:]}"}))
    except Exception as e:  # degrade, never crash - the Go caller turns this into a note
        print(json.dumps({"ok": False, "reason": f"{type(e).__name__}: {e}"}))
    return 0


if __name__ == "__main__":
    sys.exit(main())
