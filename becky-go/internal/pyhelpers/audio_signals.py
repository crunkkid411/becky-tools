#!/usr/bin/env python3
"""becky audio-signal helper: the signals a TRANSCRIPT throws away.

Emits loudness, pitch and pause structure over time so an outer tool can pick
moments on evidence rather than on words alone.

Why this exists: a transcript records WHAT was said and loses HOW. Jordan's own
content analysis names the missing signals directly - "audio spikes on
punchlines", "vocal pitch >15% increase = comedic emphasis", "jump cuts synced
to breath intakes" - and his personality profile names the failure mode: "the
tone flip (sincere words, sarcastic delivery) is exactly the nuance a
transcript-only pipeline loses". Transcript stays a signal. This adds INDEPENDENT
ones so becky can corroborate before concluding, instead of trusting one channel.

WHY EVERY THRESHOLD IS DERIVED FROM THE FILE'S OWN DISTRIBUTION
--------------------------------------------------------------
becky has been burned by absolute constants tuned on one recording: auto-editor's
fixed dB gate shredded words on quiet raw footage, because the number was right
for material that had already been compressed and gained. So NOTHING here is an
absolute level:

  * speech/silence      - Otsu split of THIS file's own frame-dB histogram
  * loudness spike      - rise over a TRAILING ROLLING MEDIAN of this file, and
                          the cut is a PERCENTILE of this file's rise distribution
  * pitch rise          - ratio against the SPEAKER'S OWN rolling median F0
  * breath gap          - a silence run bounded by this file's own speech mask

The one measure that is deliberately NOT file-derived is the voicing cut on YIN
periodicity: it is a normalised correlation, already independent of gain and mic,
and taking a percentile of it would declare a fixed fraction of EVERY file voiced
- including a file of pure noise. Levels adapt to the file; quality measures must
not.

The only tunable numbers that are not distribution-derived are EDITORIAL, not
acoustic: the 1.15 pitch ratio (Jordan's documented ">15%") and the 0.08-0.50 s
breath-gap span (how long a breath is, in seconds, which no distribution can
tell you). Both are flags, and both are ratios/durations - neither depends on the
recording's gain, mic or room.

WHY A HAND-ROLLED YIN AND NOT LIBROSA
-------------------------------------
becky's default interpreter (internal/config detectPython -> the kevs venv) has
numpy and scipy but NOT librosa; only Anaconda base has it. A helper that only
runs under one of becky's two interpreters is a helper that silently degrades in
production, so F0 is YIN (de Cheveigne & Kawahara 2002) in numpy: FFT
autocorrelation, sliding-window difference function, cumulative mean
normalisation, parabolic interpolation. Verified against librosa.yin on real
footage (see --selftest for the offline proof).

Input:  a media file (ffmpeg decodes it), optionally --start/--end seconds.
Output: ONE JSON line on stdout.

  {"ok": true, "sr": 16000, "hop": 0.01, "window": [t0, t1],
   "baseline": {...},                       # what the file's own distribution said
   "envelope": [{"t":0.0,"db":-51.2,"f0":0.0}, ...],
   "spikes":      [{"t":..,"rise_db":..,"db":..}],
   "pitch_rises": [{"t":..,"t0":..,"t1":..,"dur":..,"f0":..,"base_f0":..,
                    "ratio":..,"peak_ratio":..}],   # f0/ratio are RUN MEDIANS
   "speech":      [{"t0":..,"t1":..}],
   "breath_gaps": [{"t":..,"t0":..,"t1":..,"dur":..}],
   "windows":     [{"t0":..,"t1":..,"score":..,"spikes":..,"pitch_rises":..,
                    "peak_rise_db":..,"max_pitch_ratio":..,"speech_frac":..,
                    "breath_gaps":..}]}

On any failure prints {"ok": false, "reason": "..."} and exits 0, so the Go
caller surfaces a clean note instead of a stack trace.

Offline and deterministic: no network, no models, no randomness - the same file
and flags produce byte-identical JSON.

Requires: numpy, scipy, ffmpeg on PATH (or --ffmpeg).
"""
import argparse
import json
import math
import subprocess
import sys

import numpy as np

SR = 16000  # ffmpeg resamples to this; speech F0 and syllable rate need no more.
EPS = 1e-12


# --- audio in -------------------------------------------------------------


def load_audio(ffmpeg, media, start, end):
    """Decode to 16 kHz mono float32 through a pipe (no temp file to clean up)."""
    cmd = [ffmpeg, "-v", "error"]
    if start > 0:
        cmd += ["-ss", f"{start:.6f}"]
    if end > start:
        cmd += ["-t", f"{end - start:.6f}"]
    cmd += ["-i", media, "-ac", "1", "-ar", str(SR), "-vn", "-f", "s16le", "-"]
    p = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if p.returncode != 0 or len(p.stdout) < 2:
        msg = p.stderr.decode("utf-8", "replace").strip().splitlines()
        raise RuntimeError("ffmpeg produced no audio: " + (msg[-1] if msg else "empty output"))
    return np.frombuffer(p.stdout, dtype=np.int16).astype(np.float32) / 32768.0


def frame_view(y, frame_len, hop):
    """Non-copying (n_frames, frame_len) view. Tail shorter than a frame is dropped."""
    n = 1 + (len(y) - frame_len) // hop
    if n <= 0:
        return np.zeros((0, frame_len), dtype=y.dtype)
    return np.lib.stride_tricks.as_strided(
        y, shape=(n, frame_len), strides=(y.strides[0] * hop, y.strides[0]), writeable=False
    )


# --- distribution-derived helpers ----------------------------------------


def rolling_median(a, k):
    """Centred rolling median, edge-replicated. scipy is a hard dep already."""
    from scipy.ndimage import median_filter

    k = max(3, int(k) | 1)  # odd, >= 3
    if len(a) <= k:
        return np.full_like(a, float(np.median(a)) if len(a) else 0.0)
    return median_filter(a, size=k, mode="nearest")


def trailing_median(a, k):
    """Median of the k frames ENDING at each frame.

    A punchline is loud RELATIVE TO WHAT CAME JUST BEFORE IT. A centred window
    includes the spike in its own baseline, which drags the baseline up and
    shrinks exactly the events being looked for.

    scipy's `origin` slides the window wholly into the past, which is the whole
    job. Building this by shifting a CENTRED median instead was wrong at the
    head of the file: the first value of a centred median is itself computed
    from edge-replicated samples, so the shift propagated that contaminated
    value across the entire first window - a synthetic burst 11.1 dB above its
    surroundings was reported as 19.4 dB.
    """
    from scipy.ndimage import median_filter

    if len(a) < 3:
        return np.full_like(a, float(np.median(a)) if len(a) else 0.0)
    k = min(max(3, int(k)), len(a))
    return median_filter(a, size=k, origin=(k - 1) // 2, mode="nearest")


def otsu(values, bins=256):
    """Otsu's split of a 1-D histogram - the level that best separates two modes.

    Speech recordings are bimodal in dB (room tone vs voice). Otsu FINDS that
    valley from the data instead of us naming a dB number that only fits one mic
    in one room.
    """
    values = values[np.isfinite(values)]
    if len(values) < bins:
        return float(np.median(values)) if len(values) else 0.0
    lo, hi = float(np.min(values)), float(np.max(values))
    if hi - lo < 1e-6:
        return lo
    hist, edges = np.histogram(values, bins=bins, range=(lo, hi))
    w = hist.astype(np.float64)
    centers = (edges[:-1] + edges[1:]) / 2.0
    tot = w.sum()
    w0 = np.cumsum(w)
    w1 = tot - w0
    s = np.cumsum(w * centers)
    stot = s[-1]
    ok = (w0 > 0) & (w1 > 0)
    mu0 = np.where(ok, s / np.maximum(w0, 1), 0.0)
    mu1 = np.where(ok, (stot - s) / np.maximum(w1, 1), 0.0)
    var = np.where(ok, w0 * w1 * (mu0 - mu1) ** 2, -1.0)
    return float(centers[int(np.argmax(var))])


def runs(mask):
    """[(start, stop)] index pairs of each True run in a boolean array."""
    if len(mask) == 0:
        return []
    d = np.diff(mask.astype(np.int8))
    starts = list(np.flatnonzero(d == 1) + 1)
    stops = list(np.flatnonzero(d == -1) + 1)
    if mask[0]:
        starts.insert(0, 0)
    if mask[-1]:
        stops.append(len(mask))
    return list(zip(starts, stops))


# --- pitch: YIN in numpy --------------------------------------------------


def yin(y, sr, hop, fmin, fmax, win=1024, thresh=0.10, block=512):
    """Frame-wise F0 (Hz) and periodicity (0..1). 0 Hz where no period was found.

    YIN with a SLIDING integration window: the difference function always
    integrates over exactly `win` samples, so d(tau) is not biased low at long
    lags the way a fixed-frame version is (that bias is the classic
    octave-too-low error). Returned periodicity is 1 - d'(tau*), i.e. how
    periodic the frame actually was, which is what gates a pitch reading from
    being reported at all.
    """
    tau_max = int(sr / fmin) + 1
    tau_min = max(2, int(sr / fmax))
    flen = win + tau_max
    frames = frame_view(y, flen, hop)
    n = len(frames)
    f0 = np.zeros(n, dtype=np.float64)
    per = np.zeros(n, dtype=np.float64)
    if n == 0:
        return f0, per

    nfft = 1 << int(math.ceil(math.log2(flen + win)))
    taus = np.arange(tau_min, tau_max)

    for b0 in range(0, n, block):  # chunked: the padded FFT of every frame at
        b1 = min(n, b0 + block)     # once would be gigabytes on a long file
        X = frames[b0:b1].astype(np.float64)
        head = X[:, :win]
        # d(tau) = E(0) + E(tau) - 2*ACF(tau), sliding window of length `win`.
        A = np.fft.rfft(X, nfft, axis=1)
        B = np.fft.rfft(head[:, ::-1], nfft, axis=1)
        corr = np.fft.irfft(A * B, nfft, axis=1)[:, win - 1: win - 1 + tau_max]
        cs = np.cumsum(np.concatenate([np.zeros((b1 - b0, 1)), X * X], axis=1), axis=1)
        energy = cs[:, win:win + tau_max] - cs[:, :tau_max]  # E(tau), tau=0..tau_max-1
        d = energy[:, :1] + energy - 2.0 * corr
        np.maximum(d, 0.0, out=d)

        # Cumulative mean normalised difference; d'(0) := 1 by definition.
        run = np.cumsum(d[:, 1:], axis=1)
        denom = run / np.arange(1, d.shape[1])[None, :]
        with np.errstate(divide="ignore", invalid="ignore"):
            dn = np.where(denom > EPS, d[:, 1:] / denom, 1.0)
        dn = np.nan_to_num(dn, nan=1.0, posinf=1.0)

        seg = dn[:, tau_min - 1: tau_max - 1]  # dn index i == tau i+1
        # Absolute threshold: FIRST tau under `thresh`, not the global minimum -
        # the global minimum is routinely an octave multiple of the true period.
        under = seg < thresh
        has = under.any(axis=1)
        idx = np.where(has, np.argmax(under, axis=1), np.argmin(seg, axis=1))
        # Descend to the BOTTOM of that dip, not the shoulder where it first
        # crossed the threshold. A fixed 3-step walk read a 98 Hz tone as
        # 100.6 Hz (2.7% high) because the dip is wider than 3 samples at long
        # lags; descending until the curve turns back up stops at the first
        # local minimum, which is YIN's actual rule.
        rr = np.arange(len(idx))
        for _ in range(seg.shape[1]):
            nxt = np.minimum(idx + 1, seg.shape[1] - 1)
            step = seg[rr, nxt] < seg[rr, idx]
            if not step.any():
                break
            idx = np.where(step, nxt, idx)

        tau = taus[idx].astype(np.float64)
        # Parabolic interpolation on the three points around the minimum.
        i0 = np.clip(idx - 1, 0, seg.shape[1] - 1)
        i2 = np.clip(idx + 1, 0, seg.shape[1] - 1)
        r = np.arange(len(idx))
        s0, s1, s2 = seg[r, i0], seg[r, idx], seg[r, i2]
        den = 2.0 * (2.0 * s1 - s2 - s0)
        with np.errstate(divide="ignore", invalid="ignore"):
            shift = np.where(np.abs(den) > EPS, (s2 - s0) / den, 0.0)
        tau = tau + np.clip(np.nan_to_num(shift), -1.0, 1.0)

        f0[b0:b1] = np.where(tau > 0, sr / np.maximum(tau, EPS), 0.0)
        per[b0:b1] = np.clip(1.0 - s1, 0.0, 1.0)
    return f0, per


# --- analysis -------------------------------------------------------------


def analyse(y, sr, args, t_offset=0.0):
    hop = max(1, int(round(args.hop_sec * sr)))
    hop_sec = hop / sr

    # --- loudness envelope (dBFS RMS) ------------------------------------
    win = max(hop, int(round(0.025 * sr)))
    fr = frame_view(y, win, hop)
    if len(fr) < 8:
        raise RuntimeError("audio too short to analyse")
    rms = np.sqrt(np.mean(fr.astype(np.float64) ** 2, axis=1))
    db = 20.0 * np.log10(np.maximum(rms, EPS))
    t = t_offset + np.arange(len(db)) * hop_sec
    n = len(db)

    # --- speech vs silence: Otsu on THIS file's dB histogram --------------
    thr_db = otsu(db)
    speech_mask = db >= thr_db
    # Fill blips shorter than min_speech/min_sil so a single frame of consonant
    # silence does not shatter one sentence into a dozen "segments".
    min_sp = max(1, int(round(args.min_speech / hop_sec)))
    min_si = max(1, int(round(args.min_silence / hop_sec)))
    for a, b in runs(~speech_mask):
        if b - a < min_si:
            speech_mask[a:b] = True
    for a, b in runs(speech_mask):
        if b - a < min_sp:
            speech_mask[a:b] = False
    sp_runs = runs(speech_mask)

    # --- loudness spikes ---------------------------------------------------
    base_k = max(3, int(round(args.baseline_sec / hop_sec)))
    # The baseline is built from SPEECH FRAMES ONLY, interpolated across the
    # silences. Measured against raw dB it was not finding punchlines at all: a
    # 3 s trailing window in a file that is 27% speech usually sits in room tone,
    # so every re-entry after a pause scored a ~50 dB "rise" and the top spikes
    # were just speech onsets. A punchline is loud relative to the SPEAKING
    # either side of it, which is what this measures. (Verified on
    # test-for-clips.mp4: cut fell from 46.5 dB to a believable few dB.)
    si = np.flatnonzero(speech_mask)
    db_sp = np.interp(np.arange(n), si, db[si]) if len(si) >= 2 else db
    base_db = trailing_median(db_sp, base_k)
    rise = db - base_db
    # The cut is a PERCENTILE OF THIS FILE'S OWN rise distribution over speech
    # frames. On a quiet raw recording a 6 dB jump is a shout; on a compressed
    # upload it is nothing. Only the file can say which this is.
    sp_rise = rise[speech_mask]
    if len(sp_rise) < 32:
        sp_rise = rise
    rise_cut = float(np.percentile(sp_rise, args.spike_pct))
    rise_cut = max(rise_cut, 1.0)  # a sub-1 dB "spike" is not audible to anyone
    spike_mask = (rise >= rise_cut) & speech_mask
    # One punchline is ONE event. Left alone, `rise` dips under the cut for a
    # frame or two mid-word and reports the same shout three times, which is
    # exactly the flood-of-maybes a human then has to sort.
    merge = max(1, int(round(args.spike_merge / hop_sec)))
    for a, b in runs(~spike_mask):
        if a > 0 and b < n and b - a <= merge:
            spike_mask[a:b] = True
    spikes = []
    for a, b in runs(spike_mask):
        k = a + int(np.argmax(rise[a:b]))
        spikes.append({"t": round(float(t[k]), 3), "db": round(float(db[k]), 2),
                       "rise_db": round(float(rise[k]), 2),
                       "dur": round(float((b - a) * hop_sec), 3)})

    # --- pitch -------------------------------------------------------------
    f0, per = yin(y, sr, hop, args.fmin, args.fmax, win=args.pitch_win)
    m = min(n, len(f0))
    f0 = np.pad(f0[:m], (0, n - m))
    per = np.pad(per[:m], (0, n - m))
    # Voiced = periodic AND above the file's own speech floor.
    #
    # DELIBERATELY NOT file-derived: periodicity is 1 - d'(tau*), a NORMALISED
    # correlation that is already independent of gain, mic and room - the very
    # things a file-derived threshold exists to absorb. Taking a percentile of it
    # instead declares a fixed fraction of every file "voiced" no matter what is
    # in it, so a file of pure noise would report 60% voiced. (It also read a
    # clean 156 Hz tone as unvoiced, because p40 of a synthetic tone is exactly
    # 1.0.) Levels adapt to the file; quality measures do not.
    per_cut = args.min_periodicity
    voiced = speech_mask & (per >= per_cut) & (f0 >= args.fmin) & (f0 <= args.fmax)
    f0 = np.where(voiced, f0, 0.0)

    pitch_rises = []
    med_f0 = 0.0
    if voiced.sum() >= 16:
        vi = np.flatnonzero(voiced)
        # Kill isolated octave/triple flips before anything reads the track.
        # Cross-checked against librosa.yin on real footage, both implementations
        # throw the odd 1-2 frame reading an octave out; real speech pitch cannot
        # move 3x in 10 ms, so a short median filter removes the artefact while
        # leaving a genuine (>=80 ms) jump into falsetto untouched.
        cont = np.interp(np.arange(n), vi, f0[vi])
        cont = rolling_median(cont, max(3, int(round(args.f0_smooth / hop_sec))))
        f0 = np.where(voiced, cont, 0.0)
        med_f0 = float(np.median(f0[vi]))
        # Baseline = the SPEAKER'S OWN rolling median. Interpolating across
        # unvoiced gaps is safe here precisely because it is a slow baseline,
        # and ratios are only ever READ at voiced frames.
        base_f0 = rolling_median(cont, max(3, int(round(args.f0_baseline_sec / hop_sec))))
        with np.errstate(divide="ignore", invalid="ignore"):
            ratio = np.where(base_f0 > EPS, f0 / base_f0, 0.0)
        ratio = np.where(voiced, np.nan_to_num(ratio), 0.0)
        hot = ratio >= args.pitch_ratio
        min_pr = max(1, int(round(args.min_pitch_dur / hop_sec)))
        for a, b in runs(hot):
            if b - a < min_pr:
                continue
            # Every reported number describes THE WHOLE RUN, and t is its
            # midpoint - t, f0 and ratio must all refer to the same thing or a
            # caller that seeks to t lands where the reported pitch never
            # happened. Medians, not the peak frame: cross-checked against
            # librosa.yin on test-for-clips.mp4, run medians agreed on 10 of the
            # top 12 rises, while single peak frames above 250 Hz agreed only
            # half the time. Reporting the peak would headline the one number
            # that does not survive a second opinion.
            pitch_rises.append({
                "t": round(float(t[a] + (b - a) * hop_sec / 2), 3),
                "t0": round(float(t[a]), 3),
                "t1": round(float(t[b - 1] + hop_sec), 3),
                "dur": round(float((b - a) * hop_sec), 3),
                "f0": round(float(np.median(f0[a:b])), 1),
                "base_f0": round(float(np.median(base_f0[a:b])), 1),
                "ratio": round(float(np.median(ratio[a:b])), 3),
                "peak_ratio": round(float(np.max(ratio[a:b])), 3)})
    else:
        ratio = np.zeros(n)

    # --- breath gaps -------------------------------------------------------
    # A short silence FLANKED BY SPEECH is where a jump cut lands without
    # clipping a word. A silence at the head or tail of the file is not a breath,
    # it is just nothing, so both ends are excluded.
    breath = []
    lo = max(1, int(round(args.breath_min / hop_sec)))
    hi = max(lo, int(round(args.breath_max / hop_sec)))
    for a, b in runs(~speech_mask):
        if a == 0 or b == n or not (lo <= b - a <= hi):
            continue
        k = a + int(np.argmin(db[a:b]))
        breath.append({"t": round(float(t[k]), 3), "t0": round(float(t[a]), 3),
                       "t1": round(float(t[b - 1]), 3),
                       "dur": round(float((b - a) * hop_sec), 3)})

    # --- per-window aggregates --------------------------------------------
    # Components are emitted RAW so an outer tool can rank however it likes;
    # `score` is one documented default, normalised against the file's own p95 of
    # each component so it is comparable across windows of the same file.
    wl = max(1, int(round(args.win / hop_sec)))
    p95_rise = float(np.percentile(rise[speech_mask], 95)) if speech_mask.sum() > 8 else 1.0
    p95_ratio = float(np.percentile(ratio[voiced], 95)) if voiced.sum() > 8 else args.pitch_ratio
    denom_rise = max(p95_rise, 1.0)
    denom_ratio = max(p95_ratio - 1.0, 0.05)
    windows = []
    for a in range(0, n, wl):
        b = min(n, a + wl)
        if b - a < wl // 2:
            break
        w_sp = [s for s in spikes if t[a] <= s["t"] < t[b - 1] + hop_sec]
        w_pr = [p for p in pitch_rises if t[a] <= p["t"] < t[b - 1] + hop_sec]
        w_br = [g for g in breath if t[a] <= g["t"] < t[b - 1] + hop_sec]
        seg_sp = speech_mask[a:b]
        peak_rise = float(np.max(rise[a:b][seg_sp])) if seg_sp.any() else 0.0
        seg_v = voiced[a:b]
        max_ratio = float(np.max(ratio[a:b][seg_v])) if seg_v.any() else 0.0
        speech_frac = float(seg_sp.mean())
        score = (0.45 * min(1.0, max(peak_rise, 0.0) / denom_rise)
                 + 0.45 * min(1.0, max(max_ratio - 1.0, 0.0) / denom_ratio)
                 + 0.10 * speech_frac)
        windows.append({
            "t0": round(float(t[a]), 3), "t1": round(float(t[b - 1] + hop_sec), 3),
            "score": round(score, 4), "spikes": len(w_sp), "pitch_rises": len(w_pr),
            "peak_rise_db": round(peak_rise, 2), "max_pitch_ratio": round(max_ratio, 3),
            "speech_frac": round(speech_frac, 3), "breath_gaps": len(w_br)})

    out = {
        "ok": True, "sr": sr, "hop": round(hop_sec, 6),
        "window": [round(float(t[0]), 3), round(float(t[-1] + hop_sec), 3)],
        "baseline": {
            "speech_thresh_db": round(thr_db, 2),
            "noise_db": (round(float(np.median(db[~speech_mask])), 2) if (~speech_mask).any() else None),
            "speech_db": (round(float(np.median(db[speech_mask])), 2) if speech_mask.any() else None),
            "spike_rise_cut_db": round(rise_cut, 2),
            "rise_db_p95": round(p95_rise, 2),
            "f0_median_hz": round(med_f0, 1),
            "periodicity_cut": round(per_cut, 3),
            "speech_frac": round(float(speech_mask.mean()), 3),
            "voiced_frac": round(float(voiced.mean()), 3),
        },
        "spikes": spikes,
        "pitch_rises": pitch_rises,
        "speech": [{"t0": round(float(t[a]), 3), "t1": round(float(t[b - 1] + hop_sec), 3)}
                   for a, b in sp_runs],
        "breath_gaps": breath,
        "windows": windows,
    }
    if args.envelope_hz > 0:
        step = max(1, int(round((1.0 / args.envelope_hz) / hop_sec)))
        out["envelope"] = [{"t": round(float(t[i]), 3), "db": round(float(db[i]), 1),
                            "f0": round(float(f0[i]), 1)} for i in range(0, n, step)]
    return out


# --- selftest -------------------------------------------------------------


def tone(f, dur, sr=SR, amp=0.3):
    """Deterministic voiced-like tone: harmonics, no noise, no RNG."""
    n = int(dur * sr)
    x = np.arange(n) / sr
    y = np.zeros(n, dtype=np.float64)
    for h, a in ((1, 1.0), (2, 0.5), (3, 0.25), (4, 0.12)):
        y += a * np.sin(2 * np.pi * f * h * x)
    return (amp * y / np.abs(y).max()).astype(np.float32)


def selftest(args):
    """Offline proof, no media and no network. Asserts VALUES, not truthiness."""
    passed, failed, lines = 0, 0, []

    def check(name, cond, got):
        nonlocal passed, failed
        if cond:
            passed += 1
            lines.append(f"  PASS  {name}  ({got})")
        else:
            failed += 1
            lines.append(f"  FAIL  {name}  ({got})")

    # 1. YIN reads a known pitch back.
    for hz in (98.0, 147.0, 220.0):
        f0, per = yin(tone(hz, 1.5).astype(np.float32), SR, 160, args.fmin, args.fmax,
                      win=args.pitch_win)
        est = float(np.median(f0[per > 0.8])) if (per > 0.8).any() else 0.0
        err = abs(est - hz) / hz * 100
        check(f"yin reads {hz:.0f} Hz", err < 1.0, f"{est:.2f} Hz, {err:.2f}% error")

    # 2. A tone burst IS a spike; a flat tone is NOT.
    #    Shaped like real material - room tone, speech, a louder moment, speech -
    #    so the speech/silence split and the speech-only baseline are both
    #    actually exercised. The burst is exactly 20*log10(0.90/0.25) = 11.1 dB
    #    above the speech either side, and that is what must be reported back.
    room = tone(40.0, 0.4, amp=0.0006)
    talk, loud = tone(130.0, 2.5, amp=0.25), tone(130.0, 0.4, amp=0.90)
    burst = np.concatenate([room, talk, loud, talk, room])
    want_db = 20 * math.log10(0.90 / 0.25)
    r = analyse(burst, SR, args)
    hits = [s for s in r["spikes"] if abs(s["t"] - 2.9) < 0.25]
    check("tone burst is ONE spike at the burst", len(hits) == 1 and len(r["spikes"]) == 1,
          f"{len(r['spikes'])} spikes, {len(hits)} at t~2.9s")
    if hits:
        check(f"burst rise measures the real {want_db:.1f} dB jump",
              abs(hits[0]["rise_db"] - want_db) < 2.5,
              f"{hits[0]['rise_db']:.1f} dB vs {want_db:.1f} dB synthesised")
    flat = analyse(tone(130.0, 6.0, amp=0.3), SR, args)
    check("flat tone yields no spike", len(flat["spikes"]) == 0, f"{len(flat['spikes'])} spikes")

    # 3. A pitch step IS a rise; a constant pitch is NOT.
    step = np.concatenate([tone(120.0, 4.0), tone(156.0, 2.0)])  # exactly +30%
    r = analyse(step, SR, args)
    pr = [p for p in r["pitch_rises"] if abs(p["t0"] - 4.0) < 0.15]
    check("30% pitch step detected at the step", len(pr) == 1,
          f"{len(r['pitch_rises'])} rises, {len(pr)} with t0 at 4.0s")
    if pr:
        check("reported ratio is the real 1.30, not a guess",
              abs(pr[0]["ratio"] - 1.30) < 0.04, f"ratio {pr[0]['ratio']}")
        check("reported f0 is the real 156 Hz", abs(pr[0]["f0"] - 156.0) < 2.0,
              f"{pr[0]['f0']} Hz vs 156 Hz synthesised")
    check("constant pitch yields no pitch rise", len(flat["pitch_rises"]) == 0,
          f"{len(flat['pitch_rises'])} rises")

    # 4. Breath gap: short silence between speech is found, head/tail is not.
    sil = np.zeros(int(0.20 * SR), dtype=np.float32)
    gapped = np.concatenate([sil, tone(130.0, 2.0), sil, tone(130.0, 2.0), sil])
    r = analyse(gapped, SR, args)
    mid = [g for g in r["breath_gaps"] if 2.0 < g["t"] < 2.5]
    check("mid gap found as a breath candidate", len(mid) == 1,
          f"{len(r['breath_gaps'])} gaps, {len(mid)} mid")
    if mid:
        check("gap duration measured, not assumed", 0.15 <= mid[0]["dur"] <= 0.28,
              f"{mid[0]['dur']:.3f}s")
    check("two speech segments recovered", len(r["speech"]) == 2, f"{len(r['speech'])}")

    # 5. Determinism: same input -> byte-identical JSON.
    a = json.dumps(analyse(burst, SR, args), sort_keys=True)
    b = json.dumps(analyse(burst, SR, args), sort_keys=True)
    check("deterministic (identical JSON on rerun)", a == b, f"{len(a)} bytes")

    # 6. Thresholds track the file, not a constant: the SAME material 20 dB
    #    quieter must still produce the same spike.
    rb = analyse(burst, SR, args)
    r2 = analyse((burst * 0.1).astype(np.float32), SR, args)
    g2 = [s for s in r2["spikes"] if abs(s["t"] - 2.9) < 0.25]
    check("gain-invariant: -20 dB copy reports the same rise",
          len(g2) == 1 and abs(g2[0]["rise_db"] - rb["spikes"][0]["rise_db"]) < 1.0,
          f"{g2[0]['rise_db']:.1f} dB vs {rb['spikes'][0]['rise_db']:.1f} dB at full gain"
          if g2 else f"{len(r2['spikes'])} spikes")

    print("\n".join(lines))
    print(f"{passed}/{passed + failed} PASS")
    return 0 if failed == 0 else 1


# --- cli ------------------------------------------------------------------


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--media", help="video/audio file (ffmpeg decodes it)")
    ap.add_argument("--start", type=float, default=0.0, help="window start, seconds")
    ap.add_argument("--end", type=float, default=0.0, help="window end, seconds (0 = whole file)")
    ap.add_argument("--ffmpeg", default="ffmpeg")
    ap.add_argument("--hop-sec", type=float, default=0.010, help="analysis frame step")
    ap.add_argument("--win", type=float, default=2.0, help="scoring window length, seconds")
    ap.add_argument("--envelope-hz", type=float, default=10.0,
                    help="envelope points per second in the output (0 = omit)")
    # spike
    ap.add_argument("--baseline-sec", type=float, default=3.0,
                    help="trailing window the loudness baseline is taken over")
    ap.add_argument("--spike-pct", type=float, default=97.0,
                    help="percentile OF THIS FILE'S rise distribution that counts as a spike")
    ap.add_argument("--spike-merge", type=float, default=0.25,
                    help="spikes closer together than this are one event")
    # pitch
    ap.add_argument("--fmin", type=float, default=70.0)
    ap.add_argument("--fmax", type=float, default=350.0)
    ap.add_argument("--pitch-win", type=int, default=1024, help="YIN integration window, samples")
    ap.add_argument("--pitch-ratio", type=float, default=1.15,
                    help="F0 vs the speaker's own rolling median that counts as emphasis "
                         "(Jordan's documented '>15%%'); a RATIO, so gain/mic independent")
    ap.add_argument("--f0-baseline-sec", type=float, default=10.0,
                    help="window the speaker's own median F0 is taken over")
    ap.add_argument("--f0-smooth", type=float, default=0.05,
                    help="median filter on the F0 track; removes 1-2 frame octave flips")
    ap.add_argument("--min-pitch-dur", type=float, default=0.08,
                    help="a rise shorter than this is a glitch, not emphasis")
    ap.add_argument("--min-periodicity", type=float, default=0.60,
                    help="voicing cut on YIN periodicity; scale-free by construction, "
                         "so deliberately NOT file-derived (see the code comment)")
    # segmentation
    ap.add_argument("--min-speech", type=float, default=0.12)
    ap.add_argument("--min-silence", type=float, default=0.06)
    ap.add_argument("--breath-min", type=float, default=0.08)
    ap.add_argument("--breath-max", type=float, default=0.50)
    ap.add_argument("--selftest", action="store_true",
                    help="offline synthetic proof; prints N/N PASS")
    args = ap.parse_args()

    if args.selftest:
        sys.exit(selftest(args))
    if not args.media:
        print(json.dumps({"ok": False, "reason": "--media is required (or --selftest)"}))
        return
    y = load_audio(args.ffmpeg, args.media, args.start, args.end)
    if len(y) < SR // 4:
        print(json.dumps({"ok": False, "reason": "less than 0.25 s of audio decoded"}))
        return
    print(json.dumps(analyse(y, SR, args, t_offset=args.start)))


if __name__ == "__main__":
    try:
        main()
    except SystemExit:
        raise
    except Exception as e:  # noqa: BLE001 - report cleanly to the Go caller
        print(json.dumps({"ok": False, "reason": f"{type(e).__name__}: {e}"}))
        sys.exit(0)
