#!/usr/bin/env python3
"""becky-speaking helper: decide WHICH tracked face is actually talking.

Runs LR-ASD (Springer IJCV 2025, MIT, 0.84M params) over face tracks that becky
has already built, and returns a per-frame speaking score for each track.

Why this model and not mouth-movement: mouth-aspect-ratio variance never consults
the audio, so chewing, laughing, yawning and an animated listener all read as
speech. LR-ASD corroborates TWO independent modalities - lip motion against the
actual soundtrack - which is becky's corroborate-then-conclude rule expressed as a
model.

Why tracks come IN rather than being found here: a face has to be followed through
time before it can be scored, and becky already owns that logic deterministically
in internal/facetrack (Go). Detection is a model call, association is arithmetic,
and they belong on their own sides of the boundary. This helper does perception
only.

Input (--tracks, JSON):
  {"tracks": [{"id": 1, "detections": [{"t": 0.04, "bbox": [x1,y1,x2,y2]}, ...]}]}
  bbox is in SOURCE pixels; t is seconds from the start of the file.

Output (stdout, one JSON line):
  {"ok": true, "fps": 25, "window": [t0,t1],
   "tracks": [{"id":1, "scored":750, "score_mean":1.8, "speaking_frac":0.71,
               "scores":[{"t":0.04,"score":2.1}, ...]}]}

score is LR-ASD's raw logit: > 0 means speaking. speaking_frac is the fraction of
scored frames above zero - the measure a caller thresholds on, rather than a
boolean that hides the evidence.

On any failure prints {"ok": false, "reason": "..."} and exits 0, so the Go caller
surfaces a clean note instead of a stack trace.

Requires: torch, cv2, numpy, python_speech_features, and the LR-ASD checkout
(--repo) which supplies model/Model.py + loss.py and the weights.
"""
import argparse
import json
import os
import subprocess
import sys
import tempfile

# LR-ASD's fixed input contract (Columbia_test.py): video at 25 fps, audio MFCC at
# 100 fps, so exactly 4 audio frames per video frame. Faces are resized to 224 and
# centre-cropped to 112x112 grayscale.
VIDEO_FPS = 25
AUDIO_FPS = 100
FACE_SIZE = 112


def extract_audio(ffmpeg, video, start, end, wav_path):
    """16 kHz mono PCM for the window - what MFCC expects."""
    cmd = [ffmpeg, "-y", "-v", "error", "-ss", f"{start:.6f}", "-t", f"{end - start:.6f}",
           "-i", video, "-ac", "1", "-ar", "16000", "-vn", wav_path]
    subprocess.run(cmd, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--video", required=True)
    ap.add_argument("--tracks", required=True, help="JSON of face tracks (see module docstring)")
    ap.add_argument("--repo", required=True, help="LR-ASD checkout (model/ + loss.py + weight/)")
    ap.add_argument("--weights", default="", help="default: <repo>/weight/finetuning_TalkSet.model")
    ap.add_argument("--start", type=float, default=0.0)
    ap.add_argument("--end", type=float, default=0.0)
    ap.add_argument("--ffmpeg", default="ffmpeg")
    ap.add_argument("--device", default="auto", help="auto | cuda | cpu")
    ap.add_argument("--pad", type=float, default=0.40,
                    help="fraction of the face box added around it; LR-ASD was trained on "
                         "crops with visible chin and cheeks, not a tight face box")
    args = ap.parse_args()

    import cv2
    import numpy
    import torch
    import python_speech_features

    repo = os.path.abspath(args.repo)
    if not os.path.isdir(repo):
        raise FileNotFoundError(f"LR-ASD checkout not found: {repo}")
    sys.path.insert(0, repo)
    from model.Model import ASD_Model  # noqa: E402
    from loss import lossAV  # noqa: E402

    weights = args.weights or os.path.join(repo, "weight", "finetuning_TalkSet.model")
    if not os.path.exists(weights):
        raise FileNotFoundError(f"LR-ASD weights not found: {weights}")

    with open(args.tracks, encoding="utf-8") as fh:
        tin = json.load(fh)
    tracks = tin.get("tracks") or []
    if not tracks:
        print(json.dumps({"ok": False, "reason": "no tracks given"}))
        return

    # Window: default to the span the tracks actually cover.
    all_t = [d["t"] for tr in tracks for d in tr.get("detections", [])]
    if not all_t:
        print(json.dumps({"ok": False, "reason": "tracks contain no detections"}))
        return
    start = args.start if args.start > 0 else min(all_t)
    end = args.end if args.end > start else max(all_t)
    if end <= start:
        print(json.dumps({"ok": False, "reason": "window has no duration"}))
        return

    dev = args.device
    if dev == "auto":
        dev = "cuda" if torch.cuda.is_available() else "cpu"

    # --- model -------------------------------------------------------------
    net = ASD_Model().to(dev).eval()
    head = lossAV().to(dev).eval()
    sd = torch.load(weights, map_location=dev, weights_only=False)
    net.load_state_dict({k[len("model."):]: v for k, v in sd.items() if k.startswith("model.")},
                        strict=False)
    head.load_state_dict({k[len("lossAV."):]: v for k, v in sd.items() if k.startswith("lossAV.")},
                         strict=False)

    tmpdir = tempfile.mkdtemp(prefix="becky-asd-")
    wav = os.path.join(tmpdir, "a.wav")
    try:
        extract_audio(args.ffmpeg, args.video, start, end, wav)
        from scipy.io import wavfile
        sr, audio = wavfile.read(wav)
        mfcc = python_speech_features.mfcc(audio, sr, numcep=13, winlen=0.025, winstep=0.010)

        # ONE sequential decode for every track.
        #
        # The first version seeked per frame with cv2.CAP_PROP_POS_MSEC. On a
        # 3.5-hour source that is both slow and INACCURATE - the frame you get back
        # is not reliably the frame you asked for - so video drifted against audio
        # and the model's confidence decayed smoothly across the window, which read
        # like the subject going quiet. Ground truth said he talked throughout.
        # Decoding the window once, in order, at exactly LR-ASD's 25 fps removes
        # the drift and the seek cost together.
        n = int(round((end - start) * VIDEO_FPS))
        if n <= 0:
            print(json.dumps({"ok": False, "reason": "window has no frames"}))
            return

        # Which box does each track want at each 25 fps slot? None = not on screen.
        grid = {}
        for tr in tracks:
            dets = sorted(tr.get("detections", []), key=lambda d: d["t"])
            boxes, j = [], 0
            for i in range(n):
                t = start + i / VIDEO_FPS
                while j + 1 < len(dets) and abs(dets[j + 1]["t"] - t) <= abs(dets[j]["t"] - t):
                    j += 1
                # Only score where the track has a nearby detection: a face that is
                # not on screen must not contribute a speaking score.
                boxes.append(dets[j]["bbox"] if dets and abs(dets[j]["t"] - t) <= 0.5 else None)
            grid[tr.get("id")] = boxes

        probe = cv2.VideoCapture(args.video)
        if not probe.isOpened():
            raise RuntimeError(f"OpenCV could not open {args.video}")
        W = int(probe.get(cv2.CAP_PROP_FRAME_WIDTH))
        H = int(probe.get(cv2.CAP_PROP_FRAME_HEIGHT))
        probe.release()
        if W <= 0 or H <= 0:
            raise RuntimeError("could not read the video's frame size")

        faces = {tid: [] for tid in grid}
        times = {tid: [] for tid in grid}
        frame_bytes = W * H * 3
        cmd = [args.ffmpeg, "-v", "error", "-ss", f"{start:.6f}", "-t", f"{end - start:.6f}",
               "-i", args.video, "-vf", f"fps={VIDEO_FPS}", "-pix_fmt", "bgr24",
               "-f", "rawvideo", "-"]
        proc = subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        try:
            for i in range(n):
                buf = proc.stdout.read(frame_bytes)
                if len(buf) < frame_bytes:
                    break  # source ended early; score what we have
                frame = numpy.frombuffer(buf, dtype=numpy.uint8).reshape(H, W, 3)
                gray_cache = None
                for tid, boxes in grid.items():
                    bb = boxes[i]
                    if bb is None:
                        continue
                    x1, y1, x2, y2 = bb
                    cx, cy = (x1 + x2) / 2.0, (y1 + y2) / 2.0
                    half = max(x2 - x1, y2 - y1) * (0.5 + args.pad)
                    a0, b0 = int(max(0, cx - half)), int(max(0, cy - half))
                    c0, d0 = int(min(W, cx + half)), int(min(H, cy + half))
                    if c0 - a0 < 8 or d0 - b0 < 8:
                        continue
                    if gray_cache is None:
                        gray_cache = cv2.cvtColor(frame, cv2.COLOR_BGR2GRAY)
                    # Match LR-ASD's TRAINING preprocessing exactly: resize the
                    # padded face box to 224 and take the CENTRE 112 - i.e. the
                    # middle half, a 2x centre zoom (Columbia_test.py:221-223).
                    # Resizing straight to 112 keeps the whole padded box, which
                    # hands the model a face zoomed out 2x from anything it saw in
                    # training. It still returns confident-looking scores, which is
                    # exactly the kind of quiet wrongness that is impossible to spot
                    # downstream.
                    big = cv2.resize(gray_cache[b0:d0, a0:c0], (224, 224))
                    off = FACE_SIZE // 2
                    crop = big[112 - off:112 + off, 112 - off:112 + off]
                    faces[tid].append(crop)
                    times[tid].append(round(start + i / VIDEO_FPS, 4))
        finally:
            try:
                proc.stdout.close()
            except OSError:
                pass
            proc.wait(timeout=30)

        out_tracks = []
        for tr in tracks:
            tid = tr.get("id")
            V_list, T_list = faces.get(tid, []), times.get(tid, [])
            if len(V_list) < VIDEO_FPS // 2:  # under half a second is not scorable
                out_tracks.append({"id": tid, "scored": len(V_list),
                                   "score_mean": None, "speaking_frac": None,
                                   "note": "too few frames on screen to judge", "scores": []})
                continue

            V = numpy.array(V_list, dtype=numpy.float32)
            # Align audio to the frames actually scored.
            astart = int((T_list[0] - start) * AUDIO_FPS)
            need = len(V_list) * (AUDIO_FPS // VIDEO_FPS)
            A = mfcc[astart:astart + need].astype(numpy.float32)
            if A.shape[0] < need:  # pad a short tail rather than dropping the track
                A = numpy.pad(A, ((0, need - A.shape[0]), (0, 0)))

            with torch.no_grad():
                ea = net.forward_audio_frontend(torch.from_numpy(A).unsqueeze(0).to(dev))
                ev = net.forward_visual_frontend(torch.from_numpy(V).unsqueeze(0).to(dev))
                out = net.forward_audio_visual_backend(ea, ev)
                scores = numpy.array(head.forward(out, labels=None), dtype=float)

            m = min(len(scores), len(T_list))
            scores, T_list = scores[:m], T_list[:m]
            out_tracks.append({
                "id": tid,
                "scored": m,
                "score_mean": round(float(scores.mean()), 4),
                "speaking_frac": round(float((scores > 0).mean()), 4),
                "scores": [{"t": T_list[i], "score": round(float(scores[i]), 3)} for i in range(m)],
            })

        print(json.dumps({"ok": True, "fps": VIDEO_FPS, "device": dev,
                          "window": [round(start, 4), round(end, 4)], "tracks": out_tracks}))
    finally:
        try:
            if os.path.exists(wav):
                os.remove(wav)
            os.rmdir(tmpdir)
        except OSError:
            pass


if __name__ == "__main__":
    try:
        main()
    except Exception as e:  # noqa: BLE001 - report cleanly to the Go caller
        print(json.dumps({"ok": False, "reason": f"{type(e).__name__}: {e}"}))
        sys.exit(0)
