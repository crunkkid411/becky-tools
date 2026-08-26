#!/usr/bin/env python3
"""speechcut.py - find the SPEAKING parts of a recording from its own audio.

WHY THIS EXISTS (2026-08-25, Jordan's Rode Wireless GO II footage).

Two earlier approaches both left 20+ minutes of dead air on the timeline:

  1. auto-editor / becky-cut use an ABSOLUTE amplitude threshold. Jordan dialled
     his in for an iPhone 13 in a controlled room. A Rode Wireless GO II has a
     ridiculously low noise floor (measured here: -78 dBFS room tone) AND a very
     quiet programme level, so a threshold picked for one mic is meaningless for
     the other.
  2. becky-roughcut cut on TRANSCRIPT CUES instead of audio. Parakeet cue ends
     run long (an 11-second "dude."), so every over-long cue dragged its silence
     onto the timeline with it.

This does neither. It reads the waveform, finds where THIS recording's own
silence sits and where THIS recording's own speech sits, and puts the threshold
between them (Otsu's method on the dB histogram). Quiet mic, loud mic, same
code, no dial to turn. That is Jordan's own suggestion: "calibrate becky-cut
based on the actual volume of the speech relative to the silence."

Output is keep-spans in seconds, snapped to the video frame grid.

    python speechcut.py <video> [<video> ...] --out spans.json [--fps 30]
"""

import argparse
import json
import os
import subprocess
import sys

import numpy as np

SR = 16000            # analysis rate; speech energy lives well below 8 kHz
FRAME = 320           # 20 ms
HOP = 160             # 10 ms  -> 100 frames/sec
EPS = 1e-10


def decode_mono(path, ffmpeg="ffmpeg"):
    """Decode the whole audio track to mono float32 in [-1, 1]."""
    cmd = [ffmpeg, "-v", "error", "-nostdin", "-i", path,
           "-vn", "-ac", "1", "-ar", str(SR), "-f", "s16le", "-"]
    p = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if p.returncode != 0:
        raise RuntimeError(f"ffmpeg failed on {path}: {p.stderr.decode(errors='replace')[:400]}")
    pcm = np.frombuffer(p.stdout, dtype="<i2")
    return pcm.astype(np.float32) / 32768.0


def frame_db(x):
    """Per-frame RMS in dBFS, one value every HOP samples."""
    n = 1 + max(0, (len(x) - FRAME) // HOP)
    if n <= 0:
        return np.zeros(0, dtype=np.float32)
    idx = np.arange(FRAME)[None, :] + HOP * np.arange(n)[:, None]
    rms = np.sqrt(np.mean(np.square(x[idx]), axis=1))
    return 20.0 * np.log10(rms + EPS)


def otsu_db(db, lo=-90.0, hi=0.0, bins=180):
    """Otsu's threshold over the dB histogram.

    Speech recordings are strongly bimodal: a room-tone cluster and a speech
    cluster. Otsu picks the split that minimises within-class variance, i.e.
    exactly the valley between them, with no absolute level baked in.
    """
    d = np.clip(db, lo, hi)
    hist, edges = np.histogram(d, bins=bins, range=(lo, hi))
    centers = 0.5 * (edges[:-1] + edges[1:])
    total = hist.sum()
    if total == 0:
        return lo
    w0 = np.cumsum(hist) / total                       # weight of class 0
    m_cum = np.cumsum(hist * centers) / total
    m_tot = m_cum[-1]
    w1 = 1.0 - w0
    ok = (w0 > 0) & (w1 > 0)
    between = np.zeros_like(w0)
    between[ok] = ((m_tot * w0[ok] - m_cum[ok]) ** 2) / (w0[ok] * w1[ok])
    return float(centers[int(np.argmax(between))])


def pick_threshold(db):
    """Return (threshold_db, diagnostics).

    Otsu on its own can land badly on a recording that is nearly all speech or
    nearly all silence (one mode instead of two), so it is sanity-checked
    against the recording's own percentiles and clamped to sit between the
    noise floor and the speech level.
    """
    floor = float(np.percentile(db, 5))    # room tone
    speech = float(np.percentile(db, 90))  # programme level
    span = speech - floor
    t = otsu_db(db)
    # Keep the threshold inside the gap, never hugging either mode.
    lo = floor + 0.20 * span
    hi = floor + 0.75 * span
    clamped = min(max(t, lo), hi)
    return clamped, {
        "noise_floor_db": round(floor, 2),
        "speech_level_db": round(speech, 2),
        "dynamic_range_db": round(span, 2),
        "otsu_db": round(t, 2),
        "threshold_db": round(clamped, 2),
    }


def spans_from_mask(mask, hop_s):
    """Contiguous True runs -> [(start_s, end_s), ...]"""
    if mask.size == 0:
        return []
    d = np.diff(mask.astype(np.int8))
    starts = list(np.flatnonzero(d == 1) + 1)
    ends = list(np.flatnonzero(d == -1) + 1)
    if mask[0]:
        starts.insert(0, 0)
    if mask[-1]:
        ends.append(mask.size)
    return [(s * hop_s, e * hop_s) for s, e in zip(starts, ends)]


def detect(path, fps=30.0, min_gap=0.35, min_speech=0.20,
           pad_pre=0.30, pad_post=0.28, hysteresis_db=8.0, max_internal=1.0,
           ffmpeg="ffmpeg"):
    x = decode_mono(path, ffmpeg)
    dur = len(x) / SR
    db = frame_db(x)
    thr, diag = pick_threshold(db)
    hop_s = HOP / SR

    # Hysteresis thresholding, both directions: take every run of frames above
    # the LOW bar that contains at least one frame above the HIGH bar. A word's
    # quiet unvoiced onset ("Th-", "Qu-", "s-") sits below the high bar, so a
    # one-way scan clips it off the front - which is precisely the syllable
    # chopping a human editor avoids when they nudge a cut to a zero crossing.
    hi = db >= thr
    lo = db >= (thr - hysteresis_db)
    mask = np.zeros_like(hi)
    for a, b in ((int(s / hop_s), int(round(e / hop_s))) for s, e in spans_from_mask(lo, hop_s)):
        if hi[a:b].any():
            mask[a:b] = True

    # Hysteresis can be held open across a long quiet stretch by low-level noise
    # (a chair creak, a breath, HVAC), which is how a 7.9s hole survived into an
    # earlier build of this timeline. Break the mask over any run of genuinely
    # sub-threshold audio longer than max_internal, so "no kept span hides a long
    # silence" is a guarantee of the algorithm and not a property we got lucky on.
    for qs, qe in spans_from_mask(db < thr, hop_s):
        if qe - qs >= max_internal:
            mask[int(qs / hop_s):int(round(qe / hop_s))] = False

    spans = spans_from_mask(mask, hop_s)

    # 1. bridge natural in-sentence pauses
    merged = []
    for s, e in spans:
        if merged and s - merged[-1][1] < min_gap:
            merged[-1][1] = e
        else:
            merged.append([s, e])
    # 2. drop clicks / lip noise that never became a word
    merged = [sp for sp in merged if sp[1] - sp[0] >= min_speech]
    # 3. pad so no syllable is clipped, then re-merge anything that now touches
    padded = []
    for s, e in merged:
        s = max(0.0, s - pad_pre)
        e = min(dur, e + pad_post)
        if padded and s <= padded[-1][1]:
            padded[-1][1] = max(padded[-1][1], e)
        else:
            padded.append([s, e])
    # 4. snap outward to whole video frames (a cut lands on a frame boundary or
    #    Vegas resamples it; outward means we never eat into speech)
    out = []
    for s, e in padded:
        fs = int(np.floor(s * fps)) / fps
        fe = int(np.ceil(e * fps)) / fps
        fe = min(fe, dur)
        if fe > fs:
            out.append([round(fs, 6), round(fe, 6)])

    kept = sum(e - s for s, e in out)

    # ACCEPTANCE CHECK, in the tool rather than in a claim: how much silence
    # SURVIVES inside what we kept, and what is the single worst stretch. This
    # is the number Jordan reads off the timeline as "dead air", so the tool has
    # to report it instead of asserting the cut is clean.
    quiet = db < thr
    worst, held = 0.0, 0.0
    for s, e in out:
        seg = quiet[int(s / hop_s):int(e / hop_s)]
        held += float(seg.sum()) * hop_s
        for qs, qe in spans_from_mask(seg, hop_s):
            worst = max(worst, qe - qs)

    diag.update({
        "residual_silence_s": round(held, 2),
        "worst_dead_air_s": round(worst, 2),
        "source": os.path.abspath(path),
        "duration_s": round(dur, 3),
        "kept_s": round(kept, 3),
        "removed_s": round(dur - kept, 3),
        "removed_pct": round(100.0 * (dur - kept) / dur, 1) if dur else 0.0,
        "segments": len(out),
        "spans": out,
    })
    return diag


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("videos", nargs="+")
    ap.add_argument("--out", required=True)
    ap.add_argument("--fps", type=float, default=30.0)
    ap.add_argument("--min-gap", type=float, default=0.35,
                    help="silence shorter than this is a pause inside a sentence, not a cut")
    ap.add_argument("--pad-pre", type=float, default=0.30)
    ap.add_argument("--pad-post", type=float, default=0.28)
    ap.add_argument("--max-internal", type=float, default=1.0,
                    help="no kept span may contain a sub-threshold run longer than this")
    ap.add_argument("--ffmpeg", default="ffmpeg")
    a = ap.parse_args()

    results = []
    for v in a.videos:
        r = detect(v, fps=a.fps, min_gap=a.min_gap, pad_pre=a.pad_pre,
                   pad_post=a.pad_post, max_internal=a.max_internal, ffmpeg=a.ffmpeg)
        results.append(r)
        print(f"{os.path.basename(v):34s} thr={r['threshold_db']:7.2f}dB "
              f"floor={r['noise_floor_db']:7.2f} speech={r['speech_level_db']:7.2f} "
              f"| {r['duration_s']:8.1f}s -> {r['kept_s']:8.1f}s "
              f"(cut {r['removed_pct']:4.1f}%, {r['segments']} spans)", flush=True)

    tot = sum(r["duration_s"] for r in results)
    kept = sum(r["kept_s"] for r in results)
    print(f"\nTOTAL {tot/60:.1f} min -> {kept/60:.1f} min "
          f"(removed {(tot-kept)/60:.1f} min, {100*(tot-kept)/tot:.1f}%)")
    with open(a.out, "w", encoding="utf-8") as f:
        json.dump({"fps": a.fps, "files": results}, f, indent=1)
    print(f"wrote {a.out}")


def _selftest():
    """assert-based check: a synthetic quiet-mic recording must come back with
    exactly the speech spans we put in, regardless of absolute level."""
    rng = np.random.default_rng(0)
    dur, sr = 20.0, SR
    n = int(dur * sr)
    # Rode-like: -78 dBFS room tone, speech only 30 dB above it (very quiet).
    sig = rng.normal(0, 10 ** (-78 / 20), n).astype(np.float32)
    truth = [(2.0, 5.0), (9.0, 12.0), (15.0, 18.0)]
    t = np.arange(n) / sr
    for s, e in truth:
        m = (t >= s) & (t < e)
        sig[m] += (0.7 * np.sin(2 * np.pi * 180 * t[m]) *
                   (1 + 0.5 * np.sin(2 * np.pi * 4 * t[m]))).astype(np.float32) * 10 ** (-48 / 20)
    db = frame_db(sig)
    thr, diag = pick_threshold(db)
    assert diag["noise_floor_db"] < -60, diag
    mask = db >= thr
    got = [sp for sp in spans_from_mask(mask, HOP / SR) if sp[1] - sp[0] >= 0.2]
    assert len(got) == 3, f"expected 3 speech spans, got {len(got)}: {got}"
    for (gs, ge), (ts, te) in zip(got, truth):
        assert abs(gs - ts) < 0.25 and abs(ge - te) < 0.25, f"{(gs,ge)} vs {(ts,te)}"
    print("selftest OK", {k: diag[k] for k in ("noise_floor_db", "speech_level_db", "threshold_db")})


if __name__ == "__main__":
    if len(sys.argv) > 1 and sys.argv[1] == "--selftest":
        _selftest()
    else:
        main()
