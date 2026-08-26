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
FINE_FRAME = 96       # 6 ms  - edge refinement needs sub-video-frame resolution
FINE_HOP = 32         # 2 ms  -> a video frame at 30fps is ~16 fine hops
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


def frame_db_fine(x):
    """Same as frame_db but at 2 ms resolution, for locating an edge to the
    nearest video frame rather than to the nearest 10 ms analysis hop."""
    n = 1 + max(0, (len(x) - FINE_FRAME) // FINE_HOP)
    if n <= 0:
        return np.zeros(0, dtype=np.float32)
    idx = np.arange(FINE_FRAME)[None, :] + FINE_HOP * np.arange(n)[:, None]
    rms = np.sqrt(np.mean(np.square(x[idx]), axis=1))
    return 20.0 * np.log10(rms + EPS)


def vad_speech_spans(x, model, threshold=0.5, min_silence=0.10, min_speech=0.10):
    """Silero VAD (via sherpa-onnx) over the WHOLE file -> speech spans.

    Run on the whole recording ONCE, never per segment: sherpa's VAD is
    STREAMING and cannot latch onto speech that is already running at sample 0,
    so feeding it a keep-segment that starts on a word scores that word 0% and
    deletes it. becky-cut learned this the hard way; segments are scored by
    OVERLAP with this whole-file result instead.
    """
    import sherpa_onnx
    cfg = sherpa_onnx.VadModelConfig()
    cfg.silero_vad.model = model
    cfg.silero_vad.threshold = threshold
    cfg.silero_vad.min_silence_duration = min_silence
    cfg.silero_vad.min_speech_duration = min_speech
    cfg.sample_rate = SR
    vad = sherpa_onnx.VoiceActivityDetector(cfg, buffer_size_in_seconds=60)
    spans = []

    def drain():
        while not vad.empty():
            seg = vad.front
            spans.append((seg.start / SR, (seg.start + len(seg.samples)) / SR))
            vad.pop()

    W = 512
    for i in range(0, len(x), W):
        vad.accept_waveform(x[i:i + W])
        drain()
    vad.flush()
    drain()
    return spans


def overlap_pct(span, spans):
    """% of `span` covered by any of `spans`."""
    s, e = span
    if e <= s:
        return 0.0
    cov = sum(max(0.0, min(e, b) - max(s, a)) for a, b in spans)
    return 100.0 * cov / (e - s)


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


def detect(path, fps=30.0, min_gap=0.30, min_speech=0.20,
           pad_pre=0.0, pad_post=0.08, hysteresis_db=8.0, max_internal=1.0,
           reach=0.30, vad_model=None, vad_pct=20.0, words=None,
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

    # 3. REFINE EACH EDGE AT FINE RESOLUTION.
    #
    # Jordan's rough-cut standard: a cut lands on "the nearest frame to where
    # speech begins", and non-speech before a line is delivered (the speaker
    # settling into position) is trimmed out. Blanket padding cannot do that -
    # a fixed 0.30s pre-pad IS 9 frames of silence on every single event, which
    # is what he measured at 13 frames and rejected.
    #
    # So instead of padding, find the real edge: from the first sample that is
    # confidently speech (>= thr), walk back only while the signal is still
    # audibly above room tone (>= onset_bar), and stop. A plosive stops the walk
    # immediately; a fricative ramp is picked up in full. Same, mirrored, for the
    # tail. The walk is bounded so a noisy room cannot drag an edge outward.
    fdb = frame_db_fine(x)
    fhop = FINE_HOP / SR
    onset_bar = max(diag["noise_floor_db"] + 10.0, thr - 14.0)
    diag["onset_bar_db"] = round(onset_bar, 2)

    def refine(s, e):
        a, b = int(s / fhop), min(int(e / fhop), len(fdb) - 1)
        if b <= a:
            return s, e
        seg = fdb[a:b + 1]
        loud = np.flatnonzero(seg >= thr)
        if loud.size == 0:
            return s, e
        i, j = a + int(loud[0]), a + int(loud[-1])
        lim = int(reach / fhop)
        k = i
        while k > max(0, i - lim) and fdb[k - 1] >= onset_bar:
            k -= 1
        m = j
        while m < min(len(fdb) - 1, j + lim) and fdb[m + 1] >= onset_bar:
            m += 1
        return k * fhop, m * fhop

    refined = []
    for s, e in merged:
        rs, re_ = refine(s, e)
        rs = max(0.0, rs - pad_pre)
        re_ = min(dur, re_ + pad_post)
        if refined and rs <= refined[-1][1]:
            refined[-1][1] = max(refined[-1][1], re_)
        else:
            refined.append([rs, re_])

    # 4. snap to the frame grid. START snaps DOWN to the frame boundary at or
    #    immediately before the onset, with NO pre-pad: that frame IS "the
    #    nearest frame to where speech begins", so head slack is under one frame
    #    by construction and can never clip the onset. Jordan has to touch a cut
    #    only when it is off by a frame or more, so the whole budget goes here -
    #    starting tight matters monumentally more than ending tight.
    out = []
    for s, e in refined:
        fs = int(np.floor(s * fps)) / fps
        fe = int(np.ceil(e * fps)) / fps
        fe = min(fe, dur)
        if fe > fs:
            out.append([round(fs, 6), round(fe, 6)])

    # 5. VAD PASS - drop the spans that are confidently NOT speech.
    #
    # Level detection cannot tell a cough, a chair scrape, a lip smack or the
    # speaker repositioning from a word: all of them are "loud enough". Silero
    # VAD can. becky-cut's rule is used verbatim: a segment with less than
    # vad_pct% speech is not speech.
    #
    # CONFIDENT means two independent signals agree. A span is dropped only when
    # the VAD says it is not speech AND Parakeet transcribed no word starting
    # inside it. Either one alone leaves the span on the timeline - a false drop
    # deletes something Jordan said, which is far worse than a stray noise clip
    # he can delete in one keystroke.
    dropped = []
    if vad_model and os.path.exists(vad_model):
        vspans = vad_speech_spans(x, vad_model)
        keep2 = []
        for s_, e_ in out:
            pct = overlap_pct((s_, e_), vspans)
            has_word = any(s_ <= w < e_ for w in (words or []))
            if pct < vad_pct and not has_word:
                dropped.append([round(s_, 3), round(e_, 3), round(pct, 1)])
            else:
                keep2.append([s_, e_])
        out = keep2
        diag["vad_speech_pct_whole_file"] = round(
            sum(b - a for a, b in vspans) / dur * 100.0, 1)

    diag["dropped_nonspeech"] = len(dropped)
    diag["dropped_nonspeech_s"] = round(sum(e - s for s, e, _ in dropped), 2)
    diag["dropped_spans"] = dropped

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

    # HEAD/TAIL SLACK IN VIDEO FRAMES - the exact thing Jordan measures with a
    # loop region on the Vegas timeline ("13 frames is WAY too long"). Counted as
    # frames from the cut until the audio first rises above room tone.
    heads, tails = [], []
    for s_, e_ in out:
        a_, b_ = int(s_ / fhop), min(int(e_ / fhop), len(fdb) - 1)
        seg = fdb[a_:b_ + 1]
        aud = np.flatnonzero(seg >= onset_bar)
        if aud.size == 0:
            continue
        heads.append(int(aud[0]) * fhop * fps)
        tails.append((len(seg) - 1 - int(aud[-1])) * fhop * fps)
    hp = np.array(heads) if heads else np.zeros(1)
    tp = np.array(tails) if tails else np.zeros(1)

    diag.update({
        "head_frames_p50": round(float(np.percentile(hp, 50)), 1),
        "head_frames_p95": round(float(np.percentile(hp, 95)), 1),
        "head_frames_max": round(float(hp.max()), 1),
        "tail_frames_p50": round(float(np.percentile(tp, 50)), 1),
        "tail_frames_p95": round(float(np.percentile(tp, 95)), 1),
        "tail_frames_max": round(float(tp.max()), 1),
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
    ap.add_argument("--min-gap", type=float, default=0.30,
                    help="silence shorter than this is a pause inside a sentence, not a cut")
    ap.add_argument("--pad-pre", type=float, default=0.0)
    ap.add_argument("--pad-post", type=float, default=0.08)
    ap.add_argument("--reach", type=float, default=0.30,
                    help="how far an edge may walk out from confident speech to catch a soft onset")
    ap.add_argument("--max-internal", type=float, default=1.0,
                    help="no kept span may contain a sub-threshold run longer than this")
    ap.add_argument("--vad-model", default="X:/AI-2/becky-tools/models/silero_vad.onnx",
                    help="silero_vad.onnx; the VAD pass is skipped if absent")
    ap.add_argument("--vad-pct", type=float, default=20.0,
                    help="becky-cut's bar: under this percent speech, a span is not speech")
    ap.add_argument("--no-vad", action="store_true")
    ap.add_argument("--words-dir", default=None,
                    help="folder of <stem>.words.json, the second opinion that makes a drop confident")
    ap.add_argument("--ffmpeg", default="ffmpeg")
    a = ap.parse_args()

    results = []
    for v in a.videos:
        wstarts = None
        if a.words_dir:
            wj = os.path.join(a.words_dir,
                              os.path.splitext(os.path.basename(v))[0] + ".words.json")
            if os.path.exists(wj):
                wstarts = [w["start"] for w in
                           json.load(open(wj, encoding="utf-8"))["words"]]
        r = detect(v, fps=a.fps, min_gap=a.min_gap, pad_pre=a.pad_pre,
                   pad_post=a.pad_post, max_internal=a.max_internal,
                   reach=a.reach, vad_model=None if a.no_vad else a.vad_model,
                   vad_pct=a.vad_pct, words=wstarts, ffmpeg=a.ffmpeg)
        results.append(r)
        print(f"{os.path.basename(v):32s} thr={r['threshold_db']:7.2f} "
              f"| {r['duration_s']:7.1f}s -> {r['kept_s']:7.1f}s ({r['segments']:4d} spans) "
              f"| head p50/p95/max {r['head_frames_p50']:4.1f}/{r['head_frames_p95']:4.1f}/{r['head_frames_max']:5.1f}f "
              f"tail {r['tail_frames_p50']:4.1f}/{r['tail_frames_p95']:4.1f}/{r['tail_frames_max']:5.1f}f"
              f" | vad dropped {r.get('dropped_nonspeech', 0):3d} ({r.get('dropped_nonspeech_s', 0):5.1f}s)", flush=True)

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
