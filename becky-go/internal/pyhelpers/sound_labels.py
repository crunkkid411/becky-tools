#!/usr/bin/env python3
"""becky sound-label helper: names the sound inside short spans of a 16 kHz WAV.

Why this exists: a VAD (Silero, TEN VAD, NVIDIA MarbleNet) only says talking /
not talking, so it cannot tell a breath from a chair creak. Jordan, 2026-10-06:
"fast movement in my chair does not = breath, and that does change the nature of
the edit". This runs a sound event detector - PretrainedSED's BEATs_strong_1
(MIT; AudioSet Strong, 447 labels every 40 ms) - over only the 10 s chunks that
hold a span, and reports for each span:

  breath       how strongly it hears a breath (Breathing / Pant), 0-1
  other        the strongest OTHER sound - movement, impact, laughter, a cough...
               anything that is not the room, breathing or a voice - 0-1
  other_label  that sound's name
  voice        the share of the span where a voice is heard (> 0.5), 0-1

The numbers are measured on the span minus --trim seconds at each end: the
transcript's word edges are not exact, so the edges carry speech.
Research + the test on Jordan's footage: research/breath-vs-movement-sound-labels.md.

Offline: the checkpoint must already be at <repo>/resources/BEATs_strong_1.pt.
The helper refuses rather than let PretrainedSED download it.

Input:  --wav <16 kHz mono WAV> --repo <PretrainedSED dir> --spans <JSON file: [[a, b], ...]>
Output: ONE JSON line on stdout:
  {"ok": true, "device": "cuda", "chunks": 12, "seconds": 3.9,
   "spans": [{"breath": 0.52, "other": 0.08, "other_label": "Surface contact", "voice": 0.0}, ...]}
  or {"ok": false, "reason": "..."}
"""
import argparse
import contextlib
import json
import math
import os
import sys
import time

HOP = 0.04  # seconds per frame: 250 frames per 10 s chunk
CHUNK = 10.0
SR = 16000

BREATH = ("Breathing", "Pant")
VOICE = ("Speech", "Male speech, man speaking", "Female speech, woman speaking",
         "Child speech, kid speaking", "Conversation", "Narration, monologue", "Whispering",
         "Human voice", "Babbling", "Speech synthesizer", "Hubbub, speech noise, speech babble")
# Always-on room sound and the parent labels of breath and voice: they say
# nothing about what happened in the gap. On Jordan's stream "Background noise"
# sits at ~0.5 and "Mechanisms" at ~0.3 in every gap.
IGNORE = ("Background noise", "Mechanisms", "Channel, environment and background", "Mains hum",
          "Hum", "Noise", "Environmental noise", "Static", "Inside, small room",
          "Inside, large room or hall", "Inside, public space", "Outside, urban or manmade",
          "Air conditioning", "Mechanical fan", "Hiss", "Buzz", "Echo", "Reverberation",
          "Human sounds", "Respiratory sounds")
# Breathing by another name: neither a breath score nor an "other" sound. On the
# 27-livestream the clean breath at 14:07 also scored Sigh 0.15, and a sigh, gasp
# or sniff is still breathing - unlike a cough, a laugh or a bump, which stay
# "other" and stop a gap from being called a breath.
BREATH_KIN = ("Gasp", "Sigh", "Sniff", "Snort", "Wheeze", "Snoring")


def fail(reason):
    print(json.dumps({"ok": False, "reason": reason}))
    sys.exit(0)


def load_model(repo, device):
    import torch
    from models.beats.BEATs_wrapper import BEATsWrapper
    from models.prediction_wrapper import PredictionsWrapper
    with contextlib.redirect_stdout(sys.stderr):  # it prints "Loading pretrained checkpoint"
        model = PredictionsWrapper(BEATsWrapper(), checkpoint="BEATs_strong_1")
    model.eval().to(device)
    return model, torch


def label_chunks(model, torch, pcm, chunks, device):
    """Frame probabilities (250 x 447) for each needed 10 s chunk."""
    seg = int(CHUNK * SR)
    out = {}
    for c in sorted(chunks):
        x = torch.from_numpy(pcm[c * seg:(c + 1) * seg]).float()[None].to(device)
        n = x.shape[1]
        if n < seg:
            x = torch.nn.functional.pad(x, (0, seg - n))
        with torch.no_grad():
            y, _ = model(model.mel_forward(x))
        out[c] = torch.sigmoid(y)[0].T.float().cpu().numpy()[:max(1, int(round(250 * n / seg)))]
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--wav", required=True)
    ap.add_argument("--repo", required=True)
    ap.add_argument("--spans", required=True)
    ap.add_argument("--trim", type=float, default=0.1)
    ap.add_argument("--cpu", action="store_true")
    a = ap.parse_args()

    ckpt = os.path.join(a.repo, "resources", "BEATs_strong_1.pt")
    if not os.path.isfile(ckpt):
        fail("the sound labeler is not installed: " + ckpt + " is missing")
    try:
        import numpy as np
        import soundfile as sf
        spans = json.load(open(a.spans, encoding="utf-8"))
        pcm, sr = sf.read(a.wav, dtype="float32")
    except Exception as e:  # noqa: BLE001 - any failure is one typed answer
        fail("could not read the input: %s" % e)
    if sr != SR:
        fail("the WAV must be 16 kHz (got %d)" % sr)
    if pcm.ndim > 1:
        pcm = pcm.mean(axis=1)
    nframes = int(math.ceil(len(pcm) / SR / HOP))

    windows = []
    for s0, s1 in spans:
        lo, hi = s0 + a.trim, s1 - a.trim
        if hi - lo < 2 * HOP:
            mid = (s0 + s1) / 2
            lo, hi = mid - HOP, mid + HOP
        f0 = max(0, int(math.floor(lo / HOP)))
        f1 = min(nframes, max(f0 + 1, int(math.ceil(hi / HOP))))
        windows.append((f0, f1))
    chunks = {f // 250 for f0, f1 in windows for f in range(f0, f1)}

    sys.path.insert(0, a.repo)
    os.chdir(a.repo)  # PretrainedSED finds resources/ relative to the working folder
    t0 = time.time()
    try:
        from data_util import audioset_classes
        classes = list(audioset_classes.as_strong_train_classes)
        device = "cpu"
        import torch
        if not a.cpu and torch.cuda.is_available():
            device = "cuda"
        try:
            model, torch = load_model(a.repo, device)
            probs = label_chunks(model, torch, pcm, chunks, device)
        except RuntimeError as e:  # out of graphics memory: the CPU is slower, not wrong
            if device != "cuda":
                raise
            print("cuda failed (%s), using the CPU" % e, file=sys.stderr)
            device = "cpu"
            model, torch = load_model(a.repo, device)
            probs = label_chunks(model, torch, pcm, chunks, device)
    except Exception as e:  # noqa: BLE001
        fail("the sound labeler failed: %s" % e)

    idx = {c: i for i, c in enumerate(classes)}
    missing = [c for c in BREATH + VOICE if c not in idx]
    if missing:
        fail("labels missing from the model: %s" % ", ".join(missing))
    bi = [idx[c] for c in BREATH]
    vi = [idx[c] for c in VOICE]
    oi = [i for c, i in idx.items() if c not in BREATH + VOICE + IGNORE + BREATH_KIN]

    res = []
    for f0, f1 in windows:
        rows = [probs[f // 250][f % 250] for f in range(f0, f1)
                if f // 250 in probs and f % 250 < len(probs[f // 250])]
        if not rows:
            res.append({"breath": 0.0, "other": 0.0, "other_label": "", "voice": 0.0})
            continue
        m = np.stack(rows)
        other = m[:, oi].max(axis=0)
        k = int(other.argmax())
        res.append({"breath": round(float(m[:, bi].max()), 3),
                    "other": round(float(other[k]), 3),
                    "other_label": classes[oi[k]],
                    "voice": round(float((m[:, vi].max(axis=1) > 0.5).mean()), 3)})
    print(json.dumps({"ok": True, "device": device, "chunks": len(chunks),
                      "seconds": round(time.time() - t0, 1), "spans": res}))


if __name__ == "__main__":
    main()
